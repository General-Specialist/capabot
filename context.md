# Capabot Codebase Context

Capabot is a self-hosted AI agent platform. Users configure LLM providers and skills; the server runs a ReAct loop and exposes a REST API + web UI. It also connects to Telegram, Discord, and Slack. Skills are markdown instruction files, native Go executables, or OpenClaw-compatible plugins (TS/JS/Python) that extend the agent's behavior.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                      cmd/capabot                        │
│  main.go → serve.go (wires everything together)         │
└────────────────────────┬────────────────────────────────┘
                         │
          ┌──────────────┼──────────────┐
          │              │              │
   ┌──────▼──────┐ ┌─────▼──────┐ ┌────▼────────┐
   │ internal/api│ │ transports │ │ cron        │
   │ REST + SSE  │ │ Discord    │ │ scheduler   │
   │ web UI      │ │ Slack      │ │             │
   └──────┬──────┘ │ Telegram   │ └────┬────────┘
          │        │ HTTP       │      │
          │        └─────┬──────┘      │
          └──────────────▼─────────────┘
                  ┌──────────────┐
                  │internal/agent│
                  │  ReAct loop  │
                  └──┬───────┬───┘
                     │       │
          ┌──────────▼─┐  ┌──▼──────────────┐
          │internal/llm│  │internal/tools + │
          │Router →    │  │internal/skill   │
          │Anthropic   │  │(shell, file,    │
          │OpenAI      │  │browser, memory, │
          │Gemini      │  │plugins...)      │
          │OpenRouter  │  └─────────────────┘
          └────────────┘
                  ┌──────────────┐
                  │internal/     │
                  │memory        │
                  │Postgres store│
                  └──────────────┘
```

**Request path (simplified)**:
1. Message arrives via API (`POST /api/chat/stream`) or transport (Discord/Telegram/Slack)
2. `serve.go` closures (`runAgent` / `runAgentWithPrompt`) create a fresh `agent.Agent` per request
3. Agent runs the ReAct loop: call LLM → execute tools → call LLM → ... → return
4. LLM calls go through `llm.Router` which picks a provider and handles retries
5. Tool calls hit `internal/tools` (built-ins), plugins (TS/JS/Python), or native skills (Go)
6. Plugin hooks intercept tool calls before/after execution (if registered)
7. Messages and tool executions are persisted to Postgres via `internal/memory`

**Skill tiers**:
- **Tier 1** (Markdown): SKILL.md instructions injected into the system prompt — no code
- **Tier 2** (Native Go): `main.go` compiled on first use to `skill.bin`, called as subprocess
- **Tier 3** (Plugin): TS/JS/Python subprocess using OpenClaw's `register(api)` protocol. Can register tools, hooks, HTTP routes, LLM providers, commands, and services. Full OpenClaw `definePluginEntry` compatibility via embedded SDK shim.

**Deployment**: Docker (`CMD ["capabot", "serve"]`) or direct binary. Config at `~/.capabot/config.yaml`. Database: Postgres (optional — most features work without it). `CAPABOT_AUTOUPDATE=1` enables self-update via git pull.

---

## Top-Level Config

### `config.example.yaml`
The canonical config reference. Copy to `~/.capabot/config.yaml`. Key sections:
- `server.addr` — HTTP API listen address (default `:8080`)
- `providers.*` — LLM provider keys + default models
- `agent.*` — `max_iterations`, `context_budget_pct`, `max_tool_output_tokens`
- `skills.dirs` — list of directories scanned for `SKILL.md` subdirs
- `security.*` — API key, rate limit RPM, content filtering, session TTL, `shell_allowlist`
- `transports.*` — Telegram, Discord, Slack tokens

### `go.mod`
Module: `github.com/polymath/capabot`. Direct dependencies:
- `google/uuid`, `bwmarrin/discordgo` (Discord gateway), `jackc/pgx/v5` (Postgres), `rs/zerolog` (logging), `google.golang.org/genai` (Gemini SDK), `gopkg.in/yaml.v3`

---

## `cmd/capabot/` — CLI entry point

### `main.go`
Dispatches to subcommands:
- `serve` → `runServe`
- `chat` → `runChat`
- `dev` → `runDev`
- `skill lint|import|create|init|install|search`
- `agent list`
- `config set`
- `migrate`

Spawns `updater.CheckAndUpdate()` as a goroutine on startup.

Each subcommand creates its own `flag.FlagSet` with a `--config` flag. `expandHome` converts leading `~` to the home directory.

### `helpers.go`
`loadOrDefault(path)` — loads config from file if it exists, otherwise returns `config.Default()` with env overrides. Used by all subcommands.

### `serve.go`
The main wiring function. Startup sequence:
1. Load config
2. Create log broadcaster (writes to stderr + in-memory ring for web UI `/api/logs`)
3. Signal handling (SIGINT/SIGTERM → context cancel)
4. Init Postgres pool + store; call `store.MarkStaleRunsAsFailed` on startup
5. Init LLM router
6. Init tool registry (built-in tools + memory tool if store available)
7. Init skill registry (loads dirs); spawn plugin processes and register tools/hooks/routes/providers; register native skills as tools
8. Register `skill_create` and `skill_edit` as _extended_ tools; start skill hot-reload poller (1s interval — detects newly installed skills and registers their plugin tools/hooks/providers without restart)
9. Build the default `AgentConfig`; define `runAgent`, `runAgentWithPrompt`, `runAgentEphemeral` closures (all attach plugin hooks to each agent)
10. `syncDiscordRoles` — creates Discord roles for personas/tags that don't have one yet (startup helper, extracted from `runServe`)
11. Start cron scheduler
12. Start API server on `cfg.Server.Addr`
13. Optional: content filter, session TTL cleanup goroutine
14. Start transports (HTTP always on `:8081`; Telegram/Discord/Slack if configured)
15. Block on `<-ctx.Done()`

Key closures defined in `serve.go`:
- `resolveMode(ctx)` — looks up the active mode from DB and returns tool registry + model + thinking flag
- `applyMode(cfg, ms, ctx)` — applies mode settings to an `AgentConfig`; model priority: `@tag` > mode default > `default_model` setting
- `runAgent` — creates a new agent each call, resolves mode
- `runAgentWithPrompt` — same but with custom system prompt + optional model override
- `runAgentEphemeral` — same but no store set (no message persistence); used by transports

`makeMessageHandler` — returns the handler for transport messages. Handles:
- Content filter check
- `/default_role`, `/chat`, `/execute`, `/mode` commands
- `@model-id` tag extraction
- `@PersonaName` or channel binding persona routing
- Single persona: runs agent with persona prompt
- Multiple personas (e.g. `@tag` targeting many): runs all in parallel goroutines

`avatarToDataURI` — reads a local avatar file from `~/.capabot/avatars/` and returns a base64 data URI for Discord webhook avatar display.

### `chat.go`
`runChat` — minimal interactive REPL. Creates one default agent, no store, reads from stdin, prints `Bot: <response>`. Maintains `history []llm.ChatMessage` for multi-turn context.

### `dev.go`
`runDev` — polls skill directories every 2 seconds for SKILL.md changes. On change: logs added/changed/removed, runs lint on changed files. Does NOT restart the serve process — it's purely a watcher. The comment "restart serve to apply changes" means you need to run `air` or restart manually.

`scanSkillFiles` / `diffSkillFiles` — file-mod-time diffing helpers.

### `skill_cmds.go`
CLI skill subcommands:
- `runSkillLint` — resolves paths to SKILL.md files, runs `skill.LintSkill`, exits 1 on errors
- `runSkillImport` — calls `skill.ImportSkill`
- `runSkillCreate` — scaffolds `<name>/SKILL.md` in current dir
- `runSkillInit` — like create, but with `--plugin` flag creates `index.ts` for plugin tier
- `runSkillSearch` — calls ClawHub API to search registry
- `runSkillInstall` — downloads URL (tar.gz or zip), ClawHub name, or GitHub shorthand (`owner/repo`), extracts, calls `ImportSkill`
- `extractZip` / `extractTarGz` — archive extraction with path traversal protection

`defaultSkillsDir()` — `~/.capabot/skills`

### `agent_cmds.go`
`runAgentList` — stub; prints "no agents configured". Not yet implemented.

### `config_cmds.go`
`runConfigSet` — validates key against a hardcoded `supportedKeys` set (e.g. `providers.anthropic.api_key`), calls `config.SetKey`.

`supportedKeyList()` — returns sorted list of supported keys using `sort.Strings`.

### `migrate.go`
Calls `initStore` (which runs migrations as a side effect). Prints "migrations applied successfully".

---

## `internal/config/`

### `config.go`
`Config` struct hierarchy. Key sub-structs:
- `ServerConfig` — `Addr`
- `DatabaseConfig` — `URL`
- `ProvidersConfig` — `Anthropic`, `OpenAI`, `Gemini`, `OpenRouter`
- `AgentConfig` — `MaxIterations`, `ContextBudgetPct`, `MaxToolOutputTokens`
- `SkillsConfig` — `Dirs []string`
- `SecurityConfig` — `ShellAllowlist`, `APIKey`, `RateLimitRPM`, `ContentFiltering`, `SessionTTLDays`, `DrainTimeout`
- `TransportsConfig` — `Telegram`, `Discord`, `Slack`

`LoadFromFile(path)`:
1. Start with `Default()`
2. Decode YAML with `KnownFields(true)` (strict — unknown keys are errors)
3. Apply `applyEnvOverrides` (env takes precedence over file)
4. Run `validate`

`applyEnvOverrides` — maps `CAPABOT_*` env vars to config fields. Gemini key is read from `CAPABOT_GEMINI_API_KEY`, `GEMINI_API_KEY`, or `GOOGLE_API_KEY` (first non-empty wins).

`SetKey(path, key, value)` — reads raw YAML, walks dot-path to set a nested value, rewrites file with `0o600` perms.

`validate` — checks `server.addr` starts with `:`, log_level is valid, agent params are in range.

### `defaults.go`
`Default()` — returns sane defaults. Notable: `MaxIterations: 0` (unlimited), shell allowlist includes `open`, `node`, `npx` in addition to the usual suspects.

---

## `internal/agent/`

### `agent.go`
Core ReAct loop.

**`AgentConfig`** fields:
- `Model` — empty = use router's primary provider default
- `MaxIterations` — 0 = unlimited
- `DisableThinking` — suppresses extended thinking (Anthropic)
- `SummarizationModel` — cheap model for condensing old tool outputs; empty = dumb truncation
- `Mode` — name for usage tracking only

**`StoreWriter` interface** — minimal interface for persistence. Uses `memory.Message`, `memory.ToolExecution`, `memory.UsageRecord` directly. `*memory.Store` satisfies this interface with no adapter needed.

**`Run(ctx, sessionID, messages)`**:
1. Copies messages to avoid mutating caller slice
2. Loop: check ctx, compress old tool outputs, build windowed message slice, call LLM
3. On tool calls: execute each tool, truncate if needed, append to history
4. On no tool calls: return response
5. On max iterations: return `[max iterations reached]`

**`compressOldToolOutputs`** — mutates `history` in place. Finds last assistant message with tool calls; everything before that is "old" and gets compressed if `>300 chars`. Calls `llmSummarize` (cheap model) or falls back to `truncateOutput` (first 2 lines + stats).

**`buildToolDefs`** — converts registry tools to `[]llm.ToolDefinition` for the LLM request. Does NOT include extended tools.

**`ToolHook` interface** — `BeforeToolUse(ctx, toolName, params) (allow, modifiedParams, err)` and `AfterToolUse(ctx, toolName, params, result) (modifiedResult, err)`. Plugin hooks implement this interface.

**`executeTool`** — runs pre-hooks (can block or modify params), looks up tool by name, runs it, runs post-hooks (can modify result), emits events, persists audit log.

**Events**: `EventThinking`, `EventToolStart`, `EventToolEnd`, `EventResponse`. Emitted to `onEvent` callback (nil = no streaming).

### `context.go`
**`ContextManager`** — tracks token usage across iterations.
- `TruncateToolOutput(output)` — hard cap at `maxToolOutputTokens * 4` chars (4 chars ≈ 1 token approximation)
- `BuildMessages(history, windowSize=50)` — sliding window: keeps first message + last `windowSize-1` messages. Window boundary advances past any orphaned `tool` role messages to avoid sending tool results without their preceding assistant tool-call message.

### `tool.go`
**`Tool` interface**: `Name()`, `Description()`, `Parameters() json.RawMessage`, `Execute(ctx, params) (ToolResult, error)`

**`Registry`**: thread-safe map of tools. Two categories:
- Core tools: sent to LLM as tool definitions
- Extended tools: NOT sent to LLM; accessible only via the `use_tool` meta-tool

`Register` / `RegisterExtended` — return error if name already registered.
`List()` — core tools only.
`ExtendedNames()` / `ExtendedDescriptions()` — for building `use_tool` description.

Uses `sort.Strings` for deterministic output in `ExtendedNames`, `ExtendedDescriptions`, and `Names`.

### `filter.go`
**`ContentFilter`** — checks messages against a hardcoded list of prompt injection patterns (lowercased substring match after whitespace normalization). Conservative/high-precision approach. `maxLength` defaults to 32000.

---

## `internal/llm/`

### `provider.go`
Core types:
- **`Provider` interface**: `Chat`, `Stream`, `Models`, `Name`
- **`ChatRequest`**: `Model`, `Messages`, `System`, `Tools`, `MaxTokens`, `Temperature`, `StopSeqs`, `DisableThinking`
- **`ChatMessage`**: `Role`, `Content`, `Parts []MediaPart`, `ToolCalls`, `ToolResult`, `Metadata any`
  - `Metadata` is opaque provider data that must be round-tripped (e.g. Gemini thought signatures)
  - `Parts` carries binary multimodal content (images, PDFs) — not JSON-serialized
- **`ToolResult`**: `ToolUseID`, `Content`, `IsError`, `Parts []MediaPart`
- **`StreamChunk`**: `Delta`, `Thinking`, `ToolCall *ToolCall`, `Done`, `Usage`, `Err`
- **`CreditFetcher` interface** — optional; providers that can report account spend implement this

### `errors.go`
`HTTPStatusError{StatusCode, Body}` — returned by all providers on non-2xx responses.
`isRetryable(err)` — true for 429 or 5xx. Uses `errors.As` for unwrapping.

### `router.go`
**`Router`** implements `Provider`. Routes to primary provider, falls back to `Fallbacks []string` on retryable errors.

`Chat(req)`:
- If `req.Model` is set: find provider by model ID via `ChatWithModel`
- Otherwise: try providers in order `[primary, ...fallbacks]`

`ChatWithModel(modelID, req)` — iterates all providers' `Models()` list to find the right one.

`SetProvider(name, p)` — hot-swaps a provider (used when API keys are updated via UI).

`ProviderMap()` — returns a copy of the provider map (used by API server for `/api/providers`).

### `anthropic.go`
`AnthropicProvider` — direct HTTP client to `https://api.anthropic.com`. Models: `claude-opus-4-6`, `claude-sonnet-4-6`, `claude-haiku-4-5-20251001` (200k context each).

Notable: supports prompt caching (`cache_control: ephemeral`) on the system prompt block. Extended thinking is sent as `thinking: {type: "enabled", budget_tokens: 10000}`.

### `openai.go`
`OpenAIProvider` — OpenAI chat completions API. Also used as the base for OpenRouter. Models: `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`.

### `openrouter.go`
`OpenRouterProvider` — wraps `OpenAIProvider` with base URL `https://openrouter.ai/api/v1`. Adds `X-Title` and `HTTP-Referer` headers. Default model `anthropic/claude-sonnet-4-6`.

### `gemini.go`
`GeminiProvider` — uses the official `google.golang.org/genai` SDK. Thought signatures are stored in `ChatMessage.Metadata` and must be passed back in subsequent requests. Models: `gemini-3-flash-preview`, `gemini-2.5-pro-preview-05-06`, `gemini-2.5-flash-preview-04-17`.

---

## `internal/memory/`

### `pool.go`
`Pool` — thin wrapper over `*sql.DB` using `pgx/v5/stdlib`. Max 20 open / 5 idle connections.
`WriteTx(ctx, fn)` — runs fn in a transaction; rolls back on error.

### `migrations.go`
`Migrate(ctx, pool)` — embedded SQL migrations (`//go:embed migrations/*.sql`). Runs all in a single transaction. Uses a `schema_versions` table to track applied versions. Migrations are numbered `001` to `014`.

Migration files (what they add):
- 001: core tables (sessions, messages, memory, tool_executions)
- 002: skill_names array on automations
- 003: start_offset on automations
- 004: datepicker schema changes
- 005: persona avatar_url
- 006: persona tags
- 007: persona discord_role_id
- 008: discord_tag_roles table
- 009: persona avatar_position
- 010: system_prompt settings key
- 011: settings table
- 012: channel_bindings table
- 013: modes table
- 014: usage_log table

### `store.go`
Repository pattern. All DB access goes through this.

**Key types**:
- `Session` — `id`, `tenant_id`, `channel`, `title`, `user_id`, timestamps, `metadata`
- `Message` — `session_id`, `role`, `content`, `tool_call_id`, `tool_name`, `tool_input`, `token_count`
- `MemoryEntry` — `tenant_id`, `key`, `value`
- `ToolExecution` — audit log of tool calls
- `Persona` — `id`, `name`, `username`, `prompt`, `avatar_url`, `avatar_position`, `tags`, `discord_role_id`
- `Automation` — `id`, `name`, `rrule`, `start_at`, `end_at`, `prompt`, `skill_names`, `enabled`, `next_run_at`
- `AutomationRun` — `id`, `automation_id`, `status`, `output`, `started_at`, `finished_at`
- `ModeKeys` — `model` (default model for mode), `skill_names []string`, `system_prompt_override`
- `UsageRecord` — `provider`, `model`, `mode`, `input_tokens`, `output_tokens`

**Notable methods**:
- `MarkStaleRunsAsFailed` — sets any `running` automation runs to `failed` at startup
- `GetActiveMode` / `SetActiveMode` — stored in the `settings` table under key `active_mode`; returns `"default"` if unset
- `GetSystemPrompt` / `SetSystemPrompt` — stored in `settings` as `system_prompt`
- `GetChannelBinding` / `SetChannelBinding` / `DeleteChannelBinding` — `channel_bindings` table maps channel ID to `persona:<username>` or a tag name
- `SaveUsage` — writes to `usage_log` table for cost tracking

---

## `internal/skill/`

### `types.go`
**`SkillManifest`** — parsed YAML frontmatter from `SKILL.md`:
- `Name`, `Description`, `Version`, `Homepage`
- `Metadata SkillMetadata` — OpenClaw metadata under `openclaw`, `clawdbot`, or `clawdis` keys (aliases for same thing)
- `UserInvocable *bool`, `DisableModelInvocation bool`, `CommandDispatch`, `CommandTool`, `CommandArgMode`
- `Parameters json.RawMessage` — JSON Schema for executable skills (Tier 2/3)

**`SkillMetadataInner`** — OpenClaw metadata: `requires` (env vars, bins), `install` (package specs), `always`, `emoji`, `os`.

**`ParsedSkill`**: `{Manifest, Instructions, Warnings []ParseWarning}`

### `parser.go`
`ParseSkillMD(source)` — parses `---\nyaml\n---\nmarkdown` format. Deliberately forgiving:
- Malformed YAML → warning, not error
- Missing frontmatter → tries to extract name from first `# Heading`
- ~14% of ClawHub skills have no frontmatter name (comment in code)

### `registry.go`
`Registry` — thread-safe map of `ParsedSkill` by name. Also tracks:
- `skillPaths` — disk directory per skill
- `pluginPaths` — dir containing `index.ts`/`index.js`/`index.py` (Tier 3 plugin)
- `nativePaths` — dir containing `main.go` (Tier 2)

`LoadDir(dir)` — scans subdirectories for `SKILL.md`. Earlier dirs take precedence (workspace > user > bundled). Silently skips non-skill dirs.

`LoadNewSkills()` — re-scans all previously loaded directories and registers any skills that weren't present before. Returns names of newly loaded skills. Used by the hot-reload poller in `serve.go`.

Skill tiers are auto-detected at load time by presence of plugin entry points or `main.go`.

### `inject.go`
`BuildSystemPrompt(base, skills)` — appends each skill's instructions to the base prompt as `## Skill: <name>\n_<desc>_\n\n<instructions>`. This is how Tier 1 (markdown) skills work — pure prompt injection.

`ActiveSkillsFromNames(reg, names)` — filters registry by name list.

### `plugin.go`
`PluginProcess` — manages a long-running TS/JS/Python subprocess using JSON-line protocol over stdin/stdout.

**Lifecycle**: spawn subprocess (bun/node/python3) → plugin calls `register(api)` to register tools/hooks/routes/providers → sends "ready" → host dispatches invocations via JSON-line messages.

**Entry point detection**: probes for `index.ts` (bun), `index.js` (node), `index.py` (python3) in order.

**Embedded shim** (`//go:embed shim/host.mjs`): provides the full OpenClaw-compatible `api` object to plugins. Supports both `export function register(api)` and `definePluginEntry` patterns.

**OpenClaw SDK shim**: embedded `openclaw/plugin-sdk/` files are written to `node_modules/openclaw/` in the plugin directory so `import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry"` resolves.

**Registration types**: `RegisteredTool`, `RegisteredHook`, `RegisteredRoute`, `RegisteredProvider`.

**Invocation methods**: `Invoke` (tools), `InvokeHook` (pre/post tool hooks), `InvokeHTTP` (route handlers), `InvokeChat` (LLM providers).

**OpenClaw API coverage**: `registerTool`, `registerHook`, `on()`, `registerHttpRoute`, `registerProvider`, `registerCommand` (surfaced as tool), `registerService` (background start/stop), `registerWebSearchProvider` (extracts tool). No-op stubs for: `registerChannel`, `registerGatewayMethod`, `registerCli`, `registerSpeechProvider`, `registerMediaUnderstandingProvider`, `registerImageGenerationProvider`, `registerInteractiveHandler`, `registerContextEngine`, `registerMemoryPromptSection`.

### `plugin_tool.go`
`PluginTool` — wraps a `RegisteredTool` + `*PluginProcess` reference. Multiple `PluginTool`s can share the same process (one plugin registers many tools). `Run()` delegates to `proc.Invoke()`.

### `native.go`
`NativeExecutor` — compiles `main.go` to `skill.bin` using `go build`. Caches compiled binary (skips rebuild if bin is newer than main.go). If no `go.mod`, creates a temporary one.

Skill subprocess reads JSON params from stdin, writes `{"content":"...","is_error":false}` to stdout.

### `native_tool.go`
Adapter that wraps `NativeExecutor` as `agent.Tool` implementation.

### `importer.go`
`ImportSkill(srcDir, destRoot)` — copies an OpenClaw skill directory into `destRoot/<skillName>/`. Validates SKILL.md, checks binary dependencies, detects tier (Markdown/Native/Plugin), translates tool names. For plugin-only repos (no SKILL.md), auto-generates one from `package.json`. After copying, auto-installs npm dependencies if `package.json` exists (`bun install` preferred, falls back to `npm install`).

`ImportResult` contains `{SkillName, Tier, Warnings, Errors, MappedTools, InstallHints, DestPath}`.

### `toolmap.go`
`openClawToCapabot` — map from OpenClaw flat tool names (`exec`, `read`, `write`) to Capabot equivalents (`shell_exec`, `file_read`, `file_write`). Used by importer to generate `MappedTools` warnings.

### `lint.go`
`LintSkill(source)` — parses and validates SKILL.md. Errors on missing `name` or `description`. Warnings for missing version, instructions, etc.

### `clawhub.go`
`ClawHubClient` — fetches skills from `https://clawhub.ai` Convex backend.
- `SearchSkills(ctx, query)` — queries `listPublicPageV4` Convex function
- `DownloadSkill(ctx, name, destDir)` — downloads zip from ClawHub, extracts to temp dir

---

## `internal/transport/`

### `transport.go`
**`Transport` interface**: `Start(ctx)`, `Stop(ctx)`, `Send(ctx, OutboundMessage)`, `OnMessage(handler)`, `Name()`

**`InboundMessage`**: `ID`, `ChannelID`, `UserID`, `Username`, `Text`, `ReplyToID`, `Platform`
**`OutboundMessage`**: `ChannelID`, `ReplyToID`, `Text`, `Markdown`, `DisplayName`, `AvatarURL`, `AvatarData`

### `discord.go`
`DiscordTransport` — wraps `bwmarrin/discordgo` for the gateway connection. discordgo handles heartbeat, resume, and reconnect automatically.
- `Start` calls `session.Open()` then blocks until context is cancelled
- Event handlers: `onMessageCreate` (skips bots), `onInteractionCreate` (slash commands → ACK + synthetic InboundMessage)
- Registers global slash commands via REST on startup (`registerSlashCommands`)
- Persona replies use Discord webhooks (cached per channel in `webhooks map`, managed in `discord_send.go`)
- Intents: `IntentsGuildMessages | IntentMessageContent`

### `discord_roles.go`
`DiscordRoleClient` — creates and manages Discord roles for personas and tags via REST API. Used at startup to sync roles.

### `discord_send.go`
Helpers for sending messages via Discord REST API (regular messages and webhook-based persona messages).

### `slack.go`
`SlackTransport` — Socket Mode WebSocket connection. Uses `xapp-` token to get WebSocket URL, `xoxb-` token to send. Auto-reconnect with exponential backoff (1s → 30s max). Messages > 3000 chars split.

### `telegram.go`
`TelegramTransport` — supports both long-polling and webhook modes. Long-poll uses 30s timeout. Messages > 4096 chars split.

### `http.go`
`HTTPTransport` — simple REST transport on port `:8081` (separate from API server on `:8080`).
- `GET /healthz` — health check
- `POST /v1/chat` — synchronous request/response

---

## `internal/api/`

### `server.go`
`Server` — HTTP mux with all REST routes registered. Key routes:
- `GET /api/health` — version, uptime, skills count, provider count
- `POST /api/chat` — synchronous chat; uses `prepareChatRequest`, supports global sys prompt, model tag, single persona
- `POST /api/chat/stream` — SSE streaming; uses `prepareChatRequest`, supports all of the above plus multi-persona fan-out

`prepareChatRequest` — shared helper called by both chat handlers. Resolves: session ID, global system prompt, `@model-id` tag extraction, persona/tag mention (`resolvePersonas`). Returns `preparedChat` struct.
- `GET /api/logs` — SSE stream of log broadcaster
- `GET/POST /api/automations` — CRUD for scheduled automations
- `POST /api/automations/{id}/trigger` — manual trigger
- `GET /api/runs/{runID}/stream` — SSE stream of a running automation's agent events
- `GET/PUT /api/skills`, `POST /api/skills/install`, `POST /api/skills/create`
- `GET/PUT /api/config/keys` — hot-reload provider API keys
- `GET/POST/PUT/DELETE /api/personas` — persona management
- `GET/PUT /api/personas/system-prompt` — global system prompt prepended to all personas
- `GET/PUT /api/modes/active`, `PUT/DELETE /api/modes/{name}`
- `GET/PUT /api/settings/default-model`, `GET/PUT /api/settings/summarization-model`
- `GET /api/usage`, `GET /api/credits`
- `POST /api/avatars`, `GET /api/avatars/*` — avatar file upload/serve
- SPA static files at `/` if `StaticFS` is provided

Middleware stack (outermost first): `tenantMiddleware` → `rateLimitMiddleware` → `authMiddleware` → mux.

### `middleware.go`
- `tenantMiddleware` — reads `X-Tenant-ID` header, injects into context (`"default"` if absent)
- `authMiddleware` — Bearer token check. Skips static assets and `/api/health`
- `rateLimitMiddleware` — per-IP token-bucket. Lazily evicts stale buckets every 5 minutes (inside the request lock, no background goroutine). Only applies to `/api/` paths.
- `TenantIDFromContext(ctx)` — extracts tenant ID from context

### `automations.go`
CRUD handlers for automations. `handleAutomationsCreate` / `handleAutomationsUpdate` compute `next_run_at` via `computeNextRun`. `handleAutomationsTrigger` calls `scheduler.Trigger`.

### `config_keys.go`
`handleConfigKeysPut` — updates provider API keys in config file AND hot-reloads them into the live router via `router.SetProvider`.

### `modes.go`
Handlers for mode CRUD. Built-in modes (`default`, `chat`, `execute`) cannot be deleted.

### `personas.go`
CRUD for personas. On create/update, syncs Discord role if Discord is configured.

### `skills_create.go`
`handleSkillsCreate` — calls `tools.NewSkillCreateTool` indirectly via the agent (skill creation goes through the LLM). Actually the handler directly writes the skill directory.

### `usage.go`
`handleUsage` — returns usage log (token counts, costs) aggregated from `usage_log` table.
`handleCredits` — calls `CreditFetcher.FetchCredits` on providers that support it.

---

## `internal/cron/`

### `scheduler.go`
`Scheduler` — polls every 30 seconds for due automations. On each tick, queries DB for automations where `next_run_at <= now AND enabled = true`. For each:
1. Creates a `context.CancelFunc`, stores in `running[runID]`
2. Runs agent in goroutine with skill-injected system prompt
3. On completion: updates run record with status/output, schedules next run

Manual trigger via `Trigger(automationID)` sends to `triggerC` channel.

`Subscribe(runID)` — returns a channel of `AgentEvent`s for streaming run progress. Channel is closed when run finishes.

`StopRun(runID)` — cancels a running run.

### `parser.go`
`Parse(rrule)` — custom minimal RRule parser. Supports `FREQ=DAILY|WEEKLY|MONTHLY|YEARLY`, `INTERVAL=n`, `BYDAY=MO,WE,FR`, `BYHOUR=0-23`, `BYMINUTE=0-59`.

`Schedule.Next(from)` — computes next occurrence after `from`. For WEEKLY with BYDAY: finds next matching weekday. If `BYHOUR`/`BYMINUTE` are set, overrides the hour/minute of the computed date so automations can fire at a specific time of day.

---

## `internal/orchestrator/`

### `registry.go`
`AgentConfig` — named agent definition: `id`, `name`, `system_prompt`, `provider`, `model`, `skills`, `tools`, `max_tokens`, `temperature`.

`Registry` — thread-safe map of `AgentConfig` by ID. `List()` returns sorted by ID.

### `orchestrator.go`
`Orchestrator` — creates agents from registry configs, wires skills and tools.

`Dispatch(ctx, agentID, sessionID, messages)` — looks up config, builds `agent.Agent`, runs it. Injects spawn_agent tool for multi-agent delegation.

`buildAgent(cfg, sessionID)` — resolves provider by name from provider map, filters tools by `cfg.Tools` whitelist (if empty, all tools included), injects skill instructions into system prompt.

### `spawn_tool.go`
`SpawnAgentTool` — `spawn_agent` tool that lets an agent delegate to a peer agent by ID. Parameters: `{agent_id, task}`. Returns peer's response content.

---

## `internal/log/`

### `log.go`
`New(level, pretty)` — creates a `zerolog.Logger`. Pretty = ConsoleWriter with RFC3339 timestamps. Production = JSON to stderr.

`NewWithWriter(level, pretty, w)` — same but to a custom writer (used with broadcaster).

`WithContext(logger, tenantID, sessionID, agentID)` — adds structured fields.

`parseLevel` — string to `zerolog.Level`. Falls back to `InfoLevel` for unknown values.

### `broadcast.go`
`Broadcaster` — ring buffer (500 entries) + fan-out to subscribers. Implements `io.Writer`.

`Write(p)` — strips ANSI codes, stores in ring, sends to all subscriber channels (non-blocking; drops if slow).

`Recent(n)` — returns last n log lines from ring.

`Subscribe(ctx)` — returns a `chan string` that receives new log lines. Channel is closed when ctx is cancelled.

---

## `internal/tools/`

All tools implement `agent.Tool`. Each is in its own file.

### `shellexec.go`
`ShellExecTool` — allowlist-based shell execution. Supports both single command and batched `commands` array. Each command is `{command, args, cwd}`. Timeout defaults to 30s.

Only the first token (binary name) is checked against the allowlist.

### `fileops.go`
- `FileReadTool` — reads text (with optional line range), images (JPEG/PNG/GIF/WEBP as multimodal `Parts`), PDFs (as multimodal `Parts`). Max 1MB text, 32MB PDFs.
- `FileWriteTool` — writes files; creates parent dirs automatically
- `FileEditTool` — exact string replacement (old→new). Returns error if old_string not found or found multiple times
- `GlobTool` — walks filesystem matching glob pattern
- `GrepTool` — regex search in files with optional pattern/include filters

### `websearch.go`
`WebSearchTool` — pluggable backends: `brave` (API key required), `searxng` (self-hosted), `duckduckgo` (default, no key). DuckDuckGo uses the Instant Answer API (non-JS compatible).

### `webfetch.go`
`WebFetchTool` — fetches a URL, strips HTML tags, returns plain text. Respects a max-byte limit.

### `browser.go`
`BrowserTool` — long-running Node.js subprocess running Playwright. Browser persists across calls (cookies/sessions preserved). Actions: `navigate`, `click`, `type`, `get_text`, `screenshot` (returns base64 PNG as multimodal Part), `evaluate` (JS), `close`.

Auto-installs Playwright helper script to `~/.capabot/browser/` on first use. If the subprocess crashes, state is reset on the next `send()` error so the following call will restart it cleanly.

### `memory.go`
`MemoryTool` — persistent key-value store backed by `memory.Store`. Actions: `store`, `recall` (single key or list all), `delete`. Requires store to be non-nil.

### `todo.go`
`TodoTool` — in-memory (per-process, not persisted) task list. Call with `todos` array to replace list; call without to read. Supports multiple named lists via `list_id`.

### `schedule.go`
`ScheduleTool` — `time.Sleep` wrapper. Max delay 5 minutes. Used for introducing deliberate delays between actions.

### `notebook.go`
`NotebookTool` — reads/writes Jupyter notebook cells. Actions: `read`, `write_cell`, `insert_cell`.

### `search.go`
`SearchTool` — combined glob + grep in one tool for file searching.

### `usetool.go`
`UseToolTool` (`use_tool`) — meta-tool proxy for extended tools. Takes `{tool, input}` and dispatches to the named extended tool. Description dynamically includes all extended tool names and descriptions.

### `skill_create.go`
`SkillCreateTool` (`skill_create`) — extended tool. Takes `{name, description, code}`. Validates name format (`^[a-z][a-z0-9_-]{0,62}$`), writes Go source to `~/.capabot/skills/<name>/main.go`, writes `SKILL.md`, compiles via `NativeExecutor`, registers in both skill and tool registries. Skill is immediately usable.

### `skill_edit.go`
`SkillEditTool` (`skill_edit`) — extended tool. Edits an existing skill's `main.go`. Recompiles after edit.

---

## `internal/updater/`

### `updater.go`
`CheckAndUpdate()` — called as goroutine on startup. Rate-limited to once per minute (state stored in `~/.capabot/update.json`). Does `git fetch origin`, checks commit count, then `git pull --ff-only`.

Opt-in: only runs when `CAPABOT_AUTOUPDATE` is set. Not appropriate for Docker/Railway deployments where updates happen via image rebuild.

---

## Key Data Flows

### Chat request (API)
`POST /api/chat` or `/api/chat/stream` → `prepareChatRequest` (resolves session, sys prompt, model tag, persona) → single persona: `agentWithPrompt`; no persona: `defaultAgent`; multi-persona (stream only): `streamMultiAgent` → `agent.Run` → ReAct loop → returns `RunResult`

### Transport message (e.g. Discord)
`DiscordTransport` receives message → `makeMessageHandler` → resolve personas/channel binding → `runAgentEphemeral` (no store) → send response via transport

### Automation run
`cron.Scheduler` polls → finds due automation → runs `runAgent` with skill-injected prompt → stores result in `automation_runs` → schedules next occurrence

### Skill loading
`LoadDir` scans dirs → `ParseSkillMD` → detect tier (plugin entry point? main.go?) → register → in `serve.go`: plugins spawned as long-running subprocesses (tools/hooks/routes/providers registered), native skills compiled and registered as `agent.Tool`. Hot-reload poller (1s) calls `LoadNewSkills()` to detect skills installed via CLI while server is running.

### Model routing
`@modelname` tag in message → extracted, stripped from text → passed as `model` to `runAgent` → `llm.Router.Chat` with `req.Model` set → `ChatWithModel` finds provider by scanning all providers' `Models()` lists
