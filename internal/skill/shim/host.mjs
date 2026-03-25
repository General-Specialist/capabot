// host.mjs — Capabot plugin host shim.
// Spawned by the Go PluginProcess as: bun run host.mjs ./index.ts
// (or: node host.mjs ./index.js)
//
// Protocol (JSON lines over stdio):
//
// Plugin → Host (stdout):
//   {"type":"register_tool","name":"...","description":"...","parameters":{...}}
//   {"type":"register_hook","event":"pre_tool_use"|"post_tool_use","name":"..."}
//   {"type":"register_http_route","method":"GET","path":"/..."}
//   {"type":"register_provider","name":"...","models":[...]}
//   {"type":"ready"}
//   {"type":"result","id":"...","content":"...","is_error":false}
//
// Host → Plugin (stdin):
//   {"type":"invoke","id":"...","tool":"...","params":{...}}
//   {"type":"hook","id":"...","event":"pre_tool_use","tool":"...","params":{...}}
//   {"type":"http","id":"...","method":"GET","path":"/...","headers":{...},"body":"..."}
//   {"type":"chat","id":"...","model":"...","messages":[...],"system":"..."}
//   {"type":"shutdown"}

import { createInterface } from "node:readline";
import { resolve, dirname } from "node:path";

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
  id: "capabot-plugin",
  name: "capabot-plugin",
  version: "1.0.0",
  description: "",
  source: pluginPath,
  rootDir: dirname(pluginPath),
  registrationMode: "full",
  config: {},
  pluginConfig: {},
  runtime: { platform: "capabot" },
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
    providerHandlers.set(name, { chat });
    send({
      type: "register_provider",
      name,
      models: provider.models || [],
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
    // Unknown events are silently ignored (no-op) since capabot
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

  // --- No-op stubs for infrastructure capabot doesn't have ---

  registerChannel(_reg) {
    logger.warn("registerChannel is not supported in capabot (no-op)");
  },

  registerGatewayMethod(_method, _handler) {
    logger.warn("registerGatewayMethod is not supported in capabot (no-op)");
  },

  registerCli(_registrar, _opts) {
    logger.warn("registerCli is not supported in capabot (no-op)");
  },

  registerSpeechProvider(_provider) {
    logger.warn("registerSpeechProvider is not supported in capabot (no-op)");
  },

  registerMediaUnderstandingProvider(_provider) {
    logger.warn("registerMediaUnderstandingProvider is not supported in capabot (no-op)");
  },

  registerImageGenerationProvider(_provider) {
    logger.warn("registerImageGenerationProvider is not supported in capabot (no-op)");
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

  registerInteractiveHandler(_reg) {
    logger.warn("registerInteractiveHandler is not supported in capabot (no-op)");
  },

  onConversationBindingResolved(_handler) {
    // No-op — capabot doesn't have conversation bindings
  },

  registerContextEngine(_id, _factory) {
    logger.warn("registerContextEngine is not supported in capabot (no-op)");
  },

  registerMemoryPromptSection(_builder) {
    logger.warn("registerMemoryPromptSection is not supported in capabot (no-op)");
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
