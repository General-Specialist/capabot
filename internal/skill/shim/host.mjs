// host.mjs — GoStaff plugin host shim.
// Spawned by the Go PluginProcess as: bun run host.mjs ./index.ts
// (or: node host.mjs ./index.js)
//
// Protocol (JSON lines over stdio):
//
// Plugin → Host (stdout):
//   {"type":"register_tool","name":"...","description":"...","parameters":{...}}
//   {"type":"register_hook","event":"pre_tool_use"|"post_tool_use","name":"..."}
//   {"type":"register_http_route","method":"GET","path":"/..."}
//   {"type":"register_provider","name":"...","models":[...],"config_schema":{...}}
//   {"type":"ready"}
//   {"type":"result","id":"...","content":"...","is_error":false}
//   {"type":"chunk","id":"...","delta":"..."}       (streaming)
//   {"type":"done","id":"...","usage":{...},"model":"..."}  (streaming complete)
//
// Host → Plugin (stdin):
//   {"type":"invoke","id":"...","tool":"...","params":{...}}
//   {"type":"hook","id":"...","event":"pre_tool_use","tool":"...","params":{...}}
//   {"type":"http","id":"...","method":"GET","path":"/...","headers":{...},"body":"..."}
//   {"type":"chat","id":"...","model":"...","messages":[...],"system":"..."}
//   {"type":"stream","id":"...","model":"...","messages":[...],"system":"..."}
//   {"type":"shutdown"}

import { createInterface } from "node:readline";
import { resolve, dirname } from "node:path";
import { readFileSync } from "node:fs";

const pluginPath = resolve(process.argv[2]);
if (!pluginPath) {
  process.stderr.write("usage: host.mjs <plugin-entry-point>\n");
  process.exit(1);
}

// --- Registries ---

const toolHandlers = new Map();
const hookHandlers = new Map(); // "event:name" -> handler
const routeHandlers = new Map(); // "GET /path" -> handler
const providerHandlers = new Map(); // provider name -> { chat }
const serviceHandlers = new Map(); // service id -> { start, stop }
const commandHandlers = new Map(); // command name -> handler
const capabilityHandlers = new Map(); // "kind:name" -> handler object
const memoryPromptHandlers = new Map(); // section name -> builder function

function send(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

// --- OpenClaw hook event name mapping ---
// Maps OpenClaw event names to our internal event names.
const hookEventMap = {
  before_tool_call: "pre_tool_use",
  after_tool_call: "post_tool_use",
  pre_tool_use: "pre_tool_use",
  post_tool_use: "post_tool_use",
};

// --- Logger (writes to stderr) ---
const logger = {
  debug: (...args) => process.stderr.write(`[debug] ${args.join(" ")}\n`),
  info: (...args) => process.stderr.write(`[info] ${args.join(" ")}\n`),
  warn: (...args) => process.stderr.write(`[warn] ${args.join(" ")}\n`),
  error: (...args) => process.stderr.write(`[error] ${args.join(" ")}\n`),
};

// --- Full OpenClaw-compatible API object ---

const api = {
  // Identity fields
  id: "gostaff-plugin",
  name: "gostaff-plugin",
  version: "1.0.0",
  description: "",
  source: pluginPath,
  rootDir: dirname(pluginPath),
  registrationMode: "full",
  config: {},
  pluginConfig: {},
  runtime: { platform: "gostaff" },
  logger,

  // --- Core registration methods (fully implemented) ---

  registerTool(toolOrFactory, opts) {
    // OpenClaw supports two shapes:
    // 1. { name, description, parameters, execute } (simple)
    // 2. A factory function (ctx) => tool | tool[] | null
    if (typeof toolOrFactory === "function") {
      // Factory pattern — call it with a minimal context
      const result = toolOrFactory({ config: {}, logger });
      if (!result) return;
      const tools = Array.isArray(result) ? result : [result];
      for (const t of tools) api.registerTool(t, opts);
      return;
    }

    const { name, description, parameters, execute } = toolOrFactory;
    const toolName = opts?.name || name;
    if (!toolName || typeof execute !== "function") {
      throw new Error("registerTool requires name and execute");
    }
    toolHandlers.set(toolName, execute);
    send({
      type: "register_tool",
      name: toolName,
      description: description || "",
      parameters: parameters || { type: "object" },
    });
  },

  registerHook(events, handler, opts) {
    if (typeof handler !== "function") {
      // OpenClaw also accepts { event, name, handler } shape (our original format)
      if (typeof events === "object" && !Array.isArray(events) && events.handler) {
        const { event, name, handler: h } = events;
        return api.registerHook(event, h, { name });
      }
      throw new Error("registerHook requires a handler function");
    }

    const eventList = Array.isArray(events) ? events : [events];
    for (const rawEvent of eventList) {
      const mapped = hookEventMap[rawEvent] || rawEvent;
      const key = opts?.name || `hook_${hookHandlers.size}`;
      hookHandlers.set(`${mapped}:${key}`, handler);
      send({ type: "register_hook", event: mapped, name: key });
    }
  },

  registerHttpRoute({ method, path, handler }) {
    if (!method || !path || typeof handler !== "function") {
      throw new Error("registerHttpRoute requires method, path, and handler");
    }
    const key = `${method.toUpperCase()} ${path}`;
    routeHandlers.set(key, handler);
    send({ type: "register_http_route", method: method.toUpperCase(), path });
  },

  registerProvider(provider) {
    // OpenClaw ProviderPlugin has { id, label, ... } but also the simpler
    // { name, models, chat } shape we originally supported.
    const name = provider.name || provider.id;
    const chat = provider.chat;
    if (!name || typeof chat !== "function") {
      throw new Error("registerProvider requires name/id and chat");
    }
    const streamChat = typeof provider.streamChat === "function" ? provider.streamChat : null;
    providerHandlers.set(name, { chat, streamChat });

    const configSchema = typeof provider.configSchema === "function"
      ? provider.configSchema()
      : provider.configSchema || undefined;
    send({
      type: "register_provider",
      name,
      models: provider.models || [],
      ...(configSchema ? { config_schema: configSchema } : {}),
    });
  },

  // --- OpenClaw event system (maps to hooks) ---

  on(hookName, handler, opts) {
    const mapped = hookEventMap[hookName];
    if (mapped) {
      // Known tool hook — register via our hook system
      const key = opts?.name || `on_${hookName}_${hookHandlers.size}`;
      hookHandlers.set(`${mapped}:${key}`, handler);
      send({ type: "register_hook", event: mapped, name: key });
    }
    // Unknown events are silently ignored (no-op) since gostaff
    // doesn't have the infrastructure for most OpenClaw lifecycle events.
  },

  // --- Additional methods (functional stubs) ---

  registerCommand(def) {
    // Commands bypass the LLM — store handler, register as a tool so the
    // host knows about it (host can dispatch "command" type messages).
    const name = def.name;
    if (!name || typeof def.handler !== "function") {
      throw new Error("registerCommand requires name and handler");
    }
    commandHandlers.set(name, def.handler);
    // Surface as a tool so the agent can invoke it
    toolHandlers.set(`cmd_${name}`, async (params) => {
      const result = await def.handler({ args: params.args || "", config: {}, logger });
      if (typeof result === "string") return result;
      return result?.content || JSON.stringify(result);
    });
    send({
      type: "register_tool",
      name: `cmd_${name}`,
      description: `[command] ${def.description || name}`,
      parameters: { type: "object", properties: { args: { type: "string" } } },
    });
  },

  registerService(service) {
    if (!service.id) return;
    serviceHandlers.set(service.id, service);
    // Start services immediately in the background
    if (typeof service.start === "function") {
      service.start({ config: {}, logger, stateDir: "/tmp" }).catch((err) => {
        logger.error(`service ${service.id} start failed: ${err.message}`);
      });
    }
  },

  // --- Channel registration (per-channel isolation) ---

  registerChannel(reg) {
    // OpenClaw registerChannel shape: { id, systemPrompt, skills, model, memoryIsolated, ... }
    // We send it to the host so it can store the configuration.
    const id = reg.id || reg.channelId;
    if (!id) {
      logger.warn("registerChannel: missing id, skipping");
      return;
    }
    send({
      type: "register_channel",
      name: id,
      system: reg.systemPrompt || reg.system_prompt || "",
      skill_names: reg.skills || reg.skill_names || [],
      model: reg.model || "",
      memory_isolated: reg.memoryIsolated ?? reg.memory_isolated ?? false,
      tag: reg.tag || reg.persona || "",
    });
  },

  registerGatewayMethod(method, handler) {
    // Gateway methods are custom API endpoints — delegate to registerHttpRoute.
    if (typeof method === "object") {
      // Object shape: { method, path, handler }
      api.registerHttpRoute(method);
    } else if (typeof method === "string" && typeof handler === "function") {
      // (path, handler) shape — register as POST
      api.registerHttpRoute({ method: "POST", path: method, handler });
    }
  },

  registerCli(registrar, _opts) {
    // CLI commands are surfaced as tools (like registerCommand but with cli_ prefix).
    if (typeof registrar === "function") {
      // Factory pattern: registrar is a function that receives a register helper
      registrar((def) => {
        const name = def.name || def.command;
        if (!name) return;
        const handler = def.handler || def.execute;
        if (typeof handler !== "function") return;
        toolHandlers.set(`cli_${name}`, async (params) => {
          const result = await handler({ args: params.args || "", flags: params.flags || {}, config: {}, logger });
          if (typeof result === "string") return result;
          return result?.content || JSON.stringify(result);
        });
        send({
          type: "register_tool",
          name: `cli_${name}`,
          description: `[cli] ${def.description || name}`,
          parameters: def.parameters || { type: "object", properties: { args: { type: "string" }, flags: { type: "object" } } },
        });
      });
    } else if (typeof registrar === "object") {
      // Direct object shape: { name, handler, description }
      api.registerCommand(registrar);
    }
  },

  registerSpeechProvider(provider) {
    const name = provider.name || provider.id || `speech_${capabilityHandlers.size}`;
    const handlers = {};
    if (typeof provider.textToSpeech === "function") handlers.tts = provider.textToSpeech;
    if (typeof provider.speechToText === "function") handlers.stt = provider.speechToText;
    if (typeof provider.tts === "function") handlers.tts = provider.tts;
    if (typeof provider.stt === "function") handlers.stt = provider.stt;
    capabilityHandlers.set(`speech:${name}`, handlers);
    send({ type: "register_capability", kind: "speech", name });
  },

  registerMediaUnderstandingProvider(provider) {
    const name = provider.name || provider.id || `media_${capabilityHandlers.size}`;
    const analyze = provider.analyze || provider.understand || provider.process;
    if (typeof analyze !== "function") {
      logger.warn(`registerMediaUnderstandingProvider: ${name} has no analyze function`);
      return;
    }
    capabilityHandlers.set(`media_understanding:${name}`, { analyze });
    send({ type: "register_capability", kind: "media_understanding", name });
  },

  registerImageGenerationProvider(provider) {
    const name = provider.name || provider.id || `imagegen_${capabilityHandlers.size}`;
    const generate = provider.generate || provider.generateImage || provider.create;
    if (typeof generate !== "function") {
      logger.warn(`registerImageGenerationProvider: ${name} has no generate function`);
      return;
    }
    capabilityHandlers.set(`image_generation:${name}`, { generate });
    send({ type: "register_capability", kind: "image_generation", name });
  },

  registerWebSearchProvider(provider) {
    // Web search providers define a tool — extract and register it
    if (provider.createTool) {
      const toolDef = provider.createTool({ config: {}, logger });
      if (toolDef && toolDef.execute) {
        api.registerTool({
          name: toolDef.name || `websearch_${provider.id}`,
          description: toolDef.description || `Web search via ${provider.label || provider.id}`,
          parameters: toolDef.parameters || { type: "object" },
          execute: toolDef.execute,
        });
        return;
      }
    }
    logger.warn("registerWebSearchProvider: no tool created (no-op)");
  },

  registerInteractiveHandler(reg) {
    const name = reg.name || reg.id || `interactive_${capabilityHandlers.size}`;
    const handler = reg.handler || reg.handle || reg.execute;
    if (typeof handler !== "function") {
      logger.warn(`registerInteractiveHandler: ${name} has no handler function`);
      return;
    }
    capabilityHandlers.set(`interactive:${name}`, { handler });
    send({ type: "register_capability", kind: "interactive", name });
  },

  onConversationBindingResolved(_handler) {
    // No-op — gostaff doesn't have conversation bindings
  },

  registerContextEngine(id, factory) {
    const name = typeof id === "string" ? id : (id?.id || id?.name || `context_${capabilityHandlers.size}`);
    // factory can be a function (ctx => engine) or an engine object directly
    let engine;
    if (typeof factory === "function") {
      engine = factory({ config: {}, logger });
    } else if (typeof id === "object" && !factory) {
      engine = id;
    } else {
      engine = factory;
    }
    const query = engine?.query || engine?.search || engine?.retrieve;
    if (typeof query !== "function") {
      logger.warn(`registerContextEngine: ${name} has no query function`);
      return;
    }
    capabilityHandlers.set(`context_engine:${name}`, { query });
    send({ type: "register_capability", kind: "context_engine", name });
  },

  registerMemoryPromptSection(builder) {
    const name = builder.name || builder.id || `memory_section_${memoryPromptHandlers.size}`;
    const build = builder.build || builder.generate || builder.get;
    if (typeof build !== "function") {
      logger.warn(`registerMemoryPromptSection: ${name} has no build function`);
      return;
    }
    memoryPromptHandlers.set(name, build);
    send({ type: "register_memory_prompt_section", name });
  },

  resolvePath(input) {
    return resolve(dirname(pluginPath), input);
  },
};

// --- Load the plugin ---

let mod;
try {
  mod = await import(pluginPath);
} catch (err) {
  send({ type: "error", message: `failed to load plugin: ${err.message}` });
  process.exit(1);
}

// Try different export shapes:
//   export function register(api) {}              — OpenClaw Pattern B
//   export default function(api) {}               — default function
//   export default { register(api) {} }           — definePluginEntry result
//   export default { id, name, register(api) {} } — definePluginEntry result
const register =
  mod.register ||
  mod.default?.register ||
  (typeof mod.default === "function" ? mod.default : null);

if (typeof register === "function") {
  // Populate api identity from definePluginEntry metadata if available
  const entry = mod.default || mod;
  if (entry.id) api.id = entry.id;
  if (entry.name) api.name = entry.name;
  if (entry.description) api.description = entry.description;

  // Load stored config.json from the plugin directory (if it exists)
  const pluginDir = dirname(pluginPath);
  try {
    const raw = readFileSync(resolve(pluginDir, "config.json"), "utf-8");
    const cfg = JSON.parse(raw);
    api.config = cfg;
    api.pluginConfig = cfg;
  } catch {
    // No config.json — leave as empty objects
  }

  // Send plugin-level configSchema to the host (if declared via definePluginEntry)
  const configSchema = entry.configSchema;
  if (configSchema && typeof configSchema === "object" && Object.keys(configSchema).length > 0) {
    send({ type: "register_config_schema", config_schema: configSchema });
  }

  try {
    await register(api);
  } catch (err) {
    send({ type: "error", message: `register() failed: ${err.message}` });
    process.exit(1);
  }
}

send({ type: "ready" });

// --- Message dispatch loop ---

const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });

for await (const line of rl) {
  if (!line.trim()) continue;

  let msg;
  try {
    msg = JSON.parse(line);
  } catch {
    continue;
  }

  try {
    switch (msg.type) {
      case "invoke": {
        const handler = toolHandlers.get(msg.tool);
        if (!handler) {
          send({ type: "result", id: msg.id, content: `unknown tool: ${msg.tool}`, is_error: true });
          break;
        }
        const result = await handler(msg.params || {});
        const content = typeof result === "string" ? result : JSON.stringify(result);
        send({ type: "result", id: msg.id, content, is_error: false });
        break;
      }

      case "hook": {
        // Find all handlers for this event
        const results = [];
        for (const [key, handler] of hookHandlers) {
          if (key.startsWith(msg.event + ":")) {
            const out = await handler({
              tool: msg.tool,
              params: msg.params,
              result: msg.result,
            });
            if (out) results.push(out);
          }
        }
        // Merge hook results: last one wins for modifications
        const merged = results.reduce((acc, r) => ({ ...acc, ...r }), {});
        send({
          type: "hook_result",
          id: msg.id,
          allow: merged.allow !== false,
          params: merged.params || msg.params,
          result: merged.result || msg.result,
        });
        break;
      }

      case "http": {
        const key = `${msg.method} ${msg.path}`;
        const handler = routeHandlers.get(key);
        if (!handler) {
          send({ type: "http_response", id: msg.id, status: 404, body: "not found" });
          break;
        }
        const resp = await handler({
          method: msg.method,
          path: msg.path,
          headers: msg.headers || {},
          body: msg.body || "",
          query: msg.query || {},
        });
        send({
          type: "http_response",
          id: msg.id,
          status: resp.status || 200,
          headers: resp.headers || {},
          body: typeof resp.body === "string" ? resp.body : JSON.stringify(resp.body),
        });
        break;
      }

      case "chat": {
        const provider = providerHandlers.get(msg.provider);
        if (!provider) {
          send({ type: "chat_response", id: msg.id, error: `unknown provider: ${msg.provider}` });
          break;
        }
        const resp = await provider.chat({
          model: msg.model,
          messages: msg.messages,
          system: msg.system,
          tools: msg.tools,
          max_tokens: msg.max_tokens,
        });
        send({
          type: "chat_response",
          id: msg.id,
          content: resp.content || "",
          tool_calls: resp.tool_calls || [],
          usage: resp.usage || {},
          model: resp.model || msg.model,
        });
        break;
      }

      case "stream": {
        const provider = providerHandlers.get(msg.provider);
        if (!provider) {
          send({ type: "done", id: msg.id, error: `unknown provider: ${msg.provider}` });
          break;
        }
        const streamReq = {
          model: msg.model,
          messages: msg.messages,
          system: msg.system,
          tools: msg.tools,
          max_tokens: msg.max_tokens,
        };

        if (provider.streamChat) {
          // Use native streaming if available.
          try {
            const iter = await provider.streamChat(streamReq);
            for await (const chunk of iter) {
              const delta = chunk.delta || chunk.content || chunk.text || "";
              if (delta) {
                send({ type: "chunk", id: msg.id, delta });
              }
            }
            send({ type: "done", id: msg.id, model: msg.model, usage: {} });
          } catch (err) {
            send({ type: "done", id: msg.id, error: err.message });
          }
        } else {
          // Fall back to non-streaming chat, emit result as single chunk + done.
          try {
            const resp = await provider.chat(streamReq);
            const content = resp.content || "";
            if (content) {
              send({ type: "chunk", id: msg.id, delta: content });
            }
            send({
              type: "done",
              id: msg.id,
              model: resp.model || msg.model,
              usage: resp.usage || {},
            });
          } catch (err) {
            send({ type: "done", id: msg.id, error: err.message });
          }
        }
        break;
      }

      case "capability_invoke": {
        const key = `${msg.kind}:${msg.name}`;
        const cap = capabilityHandlers.get(key);
        if (!cap) {
          send({ type: "result", id: msg.id, content: `unknown capability: ${key}`, is_error: true });
          break;
        }
        const action = msg.action || "default";
        const params = msg.params || {};

        let result;
        switch (msg.kind) {
          case "speech":
            if (action === "tts" && cap.tts) result = await cap.tts(params);
            else if (action === "stt" && cap.stt) result = await cap.stt(params);
            else { send({ type: "result", id: msg.id, content: `speech action ${action} not supported`, is_error: true }); break; }
            break;
          case "image_generation":
            result = await cap.generate(params);
            break;
          case "media_understanding":
            result = await cap.analyze(params);
            break;
          case "context_engine":
            result = await cap.query(params);
            break;
          case "interactive":
            result = await cap.handler(params);
            break;
          default:
            send({ type: "result", id: msg.id, content: `unknown capability kind: ${msg.kind}`, is_error: true });
            break;
        }
        if (result !== undefined) {
          const content = typeof result === "string" ? result : JSON.stringify(result);
          send({ type: "result", id: msg.id, content, is_error: false });
        }
        break;
      }

      case "memory_prompt": {
        const builder = memoryPromptHandlers.get(msg.name);
        if (!builder) {
          send({ type: "result", id: msg.id, content: "", is_error: false });
          break;
        }
        const sessionId = msg.params?.session_id || "";
        const text = await builder({ sessionId, config: {}, logger });
        const content = typeof text === "string" ? text : JSON.stringify(text);
        send({ type: "result", id: msg.id, content, is_error: false });
        break;
      }

      case "shutdown":
        // Stop services gracefully
        for (const [id, svc] of serviceHandlers) {
          if (typeof svc.stop === "function") {
            try { await svc.stop({ config: {}, logger, stateDir: "/tmp" }); }
            catch { /* ignore */ }
          }
        }
        process.exit(0);
    }
  } catch (err) {
    send({
      type: msg.type === "hook" ? "hook_result" : msg.type === "http" ? "http_response" : "result",
      id: msg.id,
      content: err.message,
      is_error: true,
      status: 500,
      body: err.message,
      allow: true,
    });
  }
}

process.exit(0);
