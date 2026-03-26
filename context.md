# GoStaff Codebase Context

GoStaff is a self-hosted AI agent platform. Single Go binary, ~17 MB. Connects to LLM providers (Anthropic, OpenAI, Gemini, OpenRouter), serves a React web UI + REST API, bridges to chat platforms (Discord, Telegram, Slack). Has a 3-tier skill/plugin system compatible with OpenClaw's 30K+ skill ecosystem, scheduled automations, and persistent conversation memory.

---

## Architecture Overview

```
User --> Transport (Discord/Telegram/Slack/HTTP)
              |
              v
         serve.go (wiring layer)
              |
    +---------+------------+
    v         v            v
  Agent    API Server   Cron Scheduler
  (ReAct)  (REST+WebUI)  (automations)
    |         |
    +-- LLM Router --> Anthropic / OpenAI / Gemini / OpenRouter
    +-- Tool Registry --> shell, files, browser, web, memory, skills...
    +-- Skill Registry --> SKILL.md files (Tier 1/2/3)
    +-- Memory Store --> Postgres (sessions, messages, automations, people, usage)
```

**Key design decisions:**
- Single Go binary (`gostaff`), no microservices
- Postgres for all persistence (sessions, messages, memory, automations, people, usage tracking)
- Skills are markdown files with YAML frontmatter -- the LLM reads instructions from the markdown body
- Plugins are executable code (Go/TS/JS/Python) that the agent can invoke as tools via JSON-line subprocess IPC
- ReAct loop: the agent iterates (think -> act -> observe) until it produces a final text response or hits max iterations
- Multi-provider LLM routing with automatic fallback on 429/5xx errors
- OpenClaw plugin compatibility via embedded SDK shim (`internal/skill/shim/`)

---

## Project Structure

```
capabot/
+-- cmd/gostaff/          # CLI entry point
|   +-- main.go           # Command dispatch (serve, chat, dev, skill, config, migrate, update, version)
|   +-- serve.go          # Main server startup -- wires ALL subsystems together
|   +-- init.go           # Subsystem setup (store, router, tools, skills, plugins)
|   +-- transport_handler.go # Message routing logic
|   +-- chat.go           # Interactive CLI chat session
|   +-- dev.go            # Skill hot-reload watcher (polls every 2s)
|   +-- skill_cmds.go     # skill lint/import/init/install/search commands
|   +-- config_cmds.go    # config set <key> <value>
|   +-- helpers.go        # loadOrDefault config helper
|   +-- migrate.go        # Database migration runner
+-- internal/
|   +-- agent/            # ReAct agent loop + tool registry + context management
|   +-- llm/              # LLM provider interface + implementations (Anthropic, OpenAI, Gemini, OpenRouter)
|   +-- memory/           # Postgres store (sessions, messages, automations, people, usage)
|   +-- skill/            # Skill parser, registry, importer, linter, ClawHub client, plugin subprocess host
|   +-- tools/            # Built-in tools (shell, files, browser, web, memory, skills...)
|   +-- transport/        # Chat platform adapters (Discord, Telegram, Slack, HTTP)
|   +-- api/              # REST API + embedded web UI server
|   +-- sdk/              # Go SDK for compiled-in plugins + OpenClaw subprocess adapter
|   +-- cron/             # Automation scheduler (RRULE-based)
|   +-- log/              # Zerolog logger + SSE broadcaster for web UI
|   +-- config/           # YAML config loading + env overrides (GOSTAFF_*) + validation
|   +-- updater/          # Auto-update checker (GitHub releases)
+-- web/                  # React 19 frontend (Vite + Tailwind CSS v4)
|   +-- src/
|       +-- pages/        # ChatPage, DashboardPage, AutomationsPage, SkillsPage,
|       |                 # PluginsPage, PeoplePage, MemoryPage, SettingsPage, CostsPage
|       +-- components/   # Sidebar, AlertProvider, Calendar, DatePicker, Markdown, PillSwitch, etc.
|       +-- lib/          # api.ts (fetch wrapper + types)
+-- config.yaml           # User config (gitignored)
+-- config.example.yaml   # Template with all options documented
+-- docker-compose.yml    # Postgres + backend
+-- Makefile              # build, test, lint, dev, web targets
+-- .goreleaser.yaml      # Cross-platform release builds
+-- install.sh / install.ps1 # Manual install scripts (may be redundant with goreleaser)
```

---

## `cmd/gostaff/` -- CLI Entry Point

### `main.go`
Top-level command dispatch using `os.Args` + `flag.FlagSet` per subcommand. Declares `var version = "dev"` (goreleaser injects the real value via ldflags `-X main.version={{.Version}}`).

**Commands:** `serve`, `chat`, `dev`, `skill {lint,import,init,install,search}`, `config set`, `migrate`, `update`, `version`

### `serve.go` (~375 lines -- boot sequence only)
Boots every subsystem in order and blocks until shutdown:

1. Load config (YAML + env overrides)
2. Create logger with SSE broadcaster for web UI log streaming
3. Signal handling (SIGINT/SIGTERM -> graceful shutdown)
4. `initStore` -> open Postgres pool + run migrations
5. `initRouter` -> LLM providers + Router
6. `initToolRegistry` -> built-in tools
7. `initSkillRegistry` -> load skills from disk
8. `registerSDKPlugins` -> compiled Go + OpenClaw adapters
9. `registerNativeSkills` -> Tier 2 Go skills as callable tools
10. Register skill management tools + start hot-reload goroutine
11. Build `runAgent` closure (mode resolution, plugin hooks, store wiring)
12. `transport.SyncPeopleRoles` -> create Discord roles at startup
13. Start API server (REST + embedded web UI)
14. `makeMessageHandler` -> wire transport message handling
15. Start transport adapters (Discord, Telegram, Slack, HTTP)
16. Block until shutdown

### `init.go` (~260 lines -- subsystem setup)
All `init*` and `register*` functions extracted from serve.go:
- `initStore(ctx, dbURL)` -> opens Postgres, runs migrations
- `initRouter(ctx, cfg)` -> creates providers from config, returns Router
- `initToolRegistry(cfg, store)` -> registers all built-in tools
- `initSkillRegistry(cfg)` -> loads SKILL.md files from disk
- `registerNativeSkills(...)` -> compiles and registers Tier 2 Go skills
- `registerSDKPlugins(...)` -> initializes compiled-in + OpenClaw plugins
- `nativeAgentTool` adapter type (bridges `skill.NativeTool` -> `agent.Tool`)

### `transport_handler.go` (~425 lines -- message routing)
All transport message handling logic:
- `makeMessageHandler(...)` -> factory for transport message handling (content filter, shell approval flow, @person routing, @model routing, channel binding)
- `resolvePeople(ctx, store, text)` -> parses @username/@tag/Discord role mentions
- `extractModelTag(text, router)` -> strips @model-id tag from message
- `isApprovalResponse(text)` -> yes/no shell command approval detection
- `handleDefaultRoleCmd(...)` -> `/default_role` command
- `handleModeCmd(...)` -> `/chat`, `/execute`, `/mode` commands
- `checkContent(filter, text)` -> content filter wrapper

### `chat.go`
Standalone CLI chat. Loads config, initializes router + tools, creates a default agent, then runs an interactive `bufio.Scanner` loop. No Postgres needed.

### `dev.go`
Skill hot-reload watcher. Polls skill directories every 2s, diffs SKILL.md file modification times, auto-lints changed files, and logs add/change/remove events.

### `skill_cmds.go` (~528 lines)
Implements all `gostaff skill` subcommands:
- **lint**: Validates SKILL.md files
- **import**: Copies a skill directory + runs the importer (tool mapping, tier detection)
- **init**: Scaffolds a SKILL.md template; supports `--plugin` for Tier 3 TS plugin template
- **install**: Downloads from ClawHub registry, GitHub shorthand (`owner/repo`), or direct URL (zip/tar.gz)
- **search**: Queries ClawHub for matching skills

Also contains archive extraction (zip, tar.gz) with path-traversal protection.

### `config_cmds.go`
`gostaff config set <key> <value>` -- updates a dot-path key in the YAML config file. Supported keys are allowlisted in `supportedKeys` map.

### `migrate.go`
Opens Postgres and runs embedded SQL migrations, then exits.

---

## `internal/agent/` -- ReAct Agent Loop

### `agent.go` (~590 lines)
The core agent. Implements the ReAct (Reason + Act) loop.

**`Agent` struct fields:**
- `config AgentConfig` -- ID, model, system prompt, max iterations, max tokens, temperature, thinking toggle, summarization model, mode
- `provider llm.Provider` -- the LLM router
- `tools *Registry` -- available tools
- `ctxMgr *ContextManager` -- token budget tracking
- `store StoreWriter` -- optional persistence (messages + tool executions + usage)
- `onEvent func(AgentEvent)` -- streaming callback for progress events
- `hooks []ToolHook` -- plugin pre/post hooks for tool execution

**`Run(ctx, sessionID, messages)` loop:**
1. Check context cancellation
2. Compress old tool outputs (all except the most recent batch)
3. Apply sliding window to message history (`BuildMessages`, window=50)
4. Call LLM with system prompt + tools + messages
5. Track token usage + persist usage record
6. If no tool calls -> return final text response (retry once if empty)
7. If tool calls -> execute each tool, append results to history, continue loop
8. Hit max iterations -> return "[max iterations reached]"

**Tool execution flow:**
1. Look up tool in registry
2. Run pre-hooks (can block or modify params)
3. Execute tool
4. Run post-hooks (can modify result)
5. Emit events + persist audit log

**Output compression:**
- Old tool outputs (from prior iterations) get compressed before sending to LLM
- If `SummarizationModel` is set, uses a cheap LLM call to summarize
- Otherwise falls back to truncation (first 2 lines + stats)
- Threshold: 300 chars

**Token pricing table:** Hardcoded map of model -> [input, output] cost per million tokens. Covers Anthropic (Claude 3.5/4.x), OpenAI (GPT-4o), Gemini (2.0/2.5/3), and OpenRouter variants. Lives in `agent.go` lines ~540-552.

### `context.go`
**`ContextManager`** -- tracks token budget and provides truncation.
- `Budget()` = contextWindow x budgetPct
- `RecordUsage(usage)` -- updates latest input token count + cumulative output
- `TruncateToolOutput(output)` -- hard cap at maxToolOutputTokens x 4 chars

**`BuildMessages(history, windowSize)`** -- sliding window over conversation history. Keeps first message + most recent windowSize-1 messages. Adjusts cut point forward past orphaned "tool" role messages so the LLM never gets a tool result without its preceding assistant tool-call.

### `tool.go`
**`Tool` interface:** `Name()`, `Description()`, `Parameters()`, `Execute(ctx, params) (ToolResult, error)`

**`ToolResult`:** `Content string`, `IsError bool`, `Parts []llm.MediaPart`

**`Registry`** -- thread-safe tool registry with two tiers:
- **Core tools** -> sent to LLM as tool definitions (via `List()`)
- **Extended tools** -> NOT sent to LLM directly, accessible via the `use_tool` meta-tool (via `Get(name)`)

### `filter.go`
**`ContentFilter`** -- basic prompt injection detector. Checks message length + lowercased substring matching against known injection patterns (role hijacking, system prompt extraction, DAN/jailbreak, delimiter confusion). Conservative -- high precision, low recall.

---

## `internal/llm/` -- LLM Providers

### `provider.go`
**`Provider` interface:** `Chat(ctx, req) (*ChatResponse, error)`, `Stream(ctx, req) (<-chan StreamChunk, error)`, `Models() []ModelInfo`, `Name() string`

**`CreditFetcher` interface** (optional): `FetchCredits(ctx) (*CreditInfo, error)` -- implemented by OpenRouter.

**Key types:**
- `ChatRequest` -- model, messages, system prompt, tools, max tokens, temperature, stop sequences, disable thinking flag
- `ChatMessage` -- role, content, media parts, tool calls, tool result, metadata (for Gemini round-trip)
- `ChatResponse` -- content, thinking, tool calls, stop reason, usage, model, provider, metadata
- `StreamChunk` -- delta, thinking, tool call, done, usage, error
- `ToolDefinition` -- name, description, input schema (JSON)

### `router.go`
**`Router`** -- multi-provider routing with fallback. Implements `Provider` interface.
- `Chat()` -> tries model-based routing first (if model ID is set, finds the owning provider), then falls through to primary -> fallbacks on retryable errors
- `Stream()` -> same fallback strategy
- `ChatWithModel(ctx, modelID, req)` -> routes to the specific provider that owns modelID
- `SetProvider(name, p)` / `SetFallbacks(names)` -- thread-safe mutation
- `ProviderMap()` -- returns copy of provider map

### `anthropic.go` (~550 lines)
Anthropic Messages API implementation.
- Supports extended thinking (enabled by default, budget = 80% of maxTokens)
- Prompt caching via `cache_control: ephemeral` on system prompt or last tool
- Handles multimodal content (images, PDFs via base64)
- Full SSE streaming parser
- Models: Claude Opus 4.6, Sonnet 4.6, Haiku 4.5

### `openai.go` (~485 lines)
OpenAI Chat Completions API implementation.
- Also used as base for OpenRouter (via `chatWithHeaders` / `streamWithHeaders`)
- Handles multimodal (images as data URI, PDFs as file objects)
- Full SSE streaming parser with tool call accumulation across deltas
- Models: GPT-4o, GPT-4o Mini

### `openrouter.go`
Thin wrapper around `OpenAIProvider` pointed at `openrouter.ai/api/v1`. Adds `X-Title` and `HTTP-Referer` headers. Implements `CreditFetcher` via the `/auth/key` endpoint.
- Models: curated list of popular OpenRouter models (Claude, GPT, Gemini, Llama, Mistral, Qwen)

### `gemini.go` (~330 lines)
Google GenAI SDK implementation.
- Handles Gemini's thought signatures (preserved in `Metadata` for round-tripping)
- Converts tools via `ParametersJsonSchema` for raw JSON schema passthrough
- Models: Gemini 3 Flash Preview, 2.5 Pro, 2.5 Flash, 2.0 Flash

### `errors.go`
`HTTPStatusError` type + `isRetryable(err)` check (429 or 5xx).

---

## `internal/memory/` -- Postgres Persistence

### `pool.go`
Wraps `sql.DB` (via pgx driver). Max 20 open connections, 5 idle. Provides `WriteTx` for transactional writes.

### `migrations.go`
Embedded SQL migrations (`//go:embed migrations/*.sql`). Tracks applied versions in `schema_versions` table. 16 migrations (001-016).

### `schema.sql`
Consolidated schema (source of truth for DB structure). Tables:
- **sessions** -- id, tenant_id, channel, title, user_id, metadata (JSONB)
- **messages** -- session_id (FK), role, content, tool_call_id, tool_name, tool_input, token_count
- **tool_executions** -- session_id (FK), tool_name, input, output, duration_ms, success
- **memory** -- tenant_id, key, value (embedding column dropped in migration 016)
- **automations** -- name, rrule, prompt, skill_names (TEXT[]), start/end_at, start/end_offset, enabled, last/next_run_at
- **automation_runs** -- automation_id (FK), status (running/success/error/stopped), response, error
- **people** -- name, prompt, username, avatar_url, avatar_position, tags (TEXT[]), discord_role_id
- **discord_tag_roles** -- tag -> role_id mapping
- **settings** -- key/value store (active_mode, system_prompt, shell_mode, etc.)
- **channel_bindings** -- channel_id -> tag (auto-routes Discord channels to people/tags)
- **modes** -- name -> keys JSON (per-mode LLM provider overrides + model selection)
- **usage_log** -- provider, model, mode, input/output tokens, created_at

### `store.go` (~950 lines)
Repository-pattern access to all tables. Key method groups:
- **Sessions**: Create, Upsert, List, Get, Delete old, Count messages
- **Messages**: Save, Get by session
- **Memory**: Store (upsert), List, Delete
- **Tool Executions**: Save, Get by session
- **Automations**: CRUD + ListDue + StartRun/FinishRun + UpdateSchedule + MarkStaleRunsAsFailed
- **People**: CRUD + query by name/username/tag/discord_role_id
- **Settings**: Get/Set generic key-value + convenience wrappers (system prompt, active mode)
- **Discord Tag Roles**: Upsert/Get/Delete/List
- **Channel Bindings**: Get/Set/Delete
- **Modes**: Get/Set/Delete/List + GetActiveMode/SetActiveMode
- **Usage**: SaveUsage + GetUsageSummary (aggregated by provider/model/mode)

---

## `internal/skill/` -- Skill System

Skills are the primary extensibility mechanism. They come in three tiers:

### Tier 1: Markdown Skills (default)
A SKILL.md file with YAML frontmatter + markdown body. The instructions are injected into the agent's system prompt. No code execution.

### Tier 2: Native Go Skills
A SKILL.md + Go source file. Compiled at startup via `go build -buildmode=plugin`. Registered as a callable tool.

### Tier 3: Plugin Skills (OpenClaw-compatible)
A SKILL.md + executable code (TypeScript via Bun, JavaScript via Node, or Python 3). Invoked via stdin/stdout JSON-line protocol. Full OpenClaw SDK compatibility via embedded shim.

### `types.go`
Core types: `SkillManifest` (parsed YAML frontmatter), `ParsedSkill` (manifest + instructions + warnings), `SkillResult` (JSON envelope for executable skills), `InstallSpec`, `SkillRequires`, `SkillMetadata` (OpenClaw compatibility -- supports `metadata.openclaw`, `metadata.clawdbot`, `metadata.clawdis` aliases).

### `parser.go`
Parses SKILL.md files: extracts YAML frontmatter between `---` delimiters, then the markdown body as instructions. Returns `ParsedSkill`.

### `registry.go` (~349 lines)
Thread-safe skill registry. Loads skills from directories, tracks by name. Supports hot-reload via `LoadNewSkills()`. Also has `BuildSystemPrompt(basePrompt, skills)` -- concatenates the base prompt with all loaded skill instructions.

### `inject.go`
`BuildSystemPrompt(base, skills)` -- appends skill instructions to the system prompt, formatted as sections.

### `importer.go` (~380 lines)
Imports skills from external sources. Handles tier detection (looks for `index.ts/js/py`, `package.json`, `openclaw.plugin.json`, `clawdbot.plugin.json`) and generates warnings/install hints.

### `lint.go`
Validates SKILL.md files: checks frontmatter presence, required fields (name, description), version format, body length. Returns a `LintReport`.

### `plugin.go` (~513 lines) / `plugin_tool.go`
Tier 3 plugin executor. Spawns a subprocess (`bun run index.ts`, `node index.js`, or `python3 index.py`), communicates via JSON-line protocol:
- **Upstream (plugin -> host):** `register_tool`, `register_hook`, `register_http_route`, `register_provider`, `ready`, `error`
- **Downstream (host -> plugin):** `invoke`, `hook`, `http`, `chat`, `shutdown`
- 10-second init timeout; all registrations must complete before "ready"

### `native.go` / `native_tool.go`
Tier 2 native Go skill executor. Compiles Go source to a plugin at startup, loads it, and calls the exported `Run` function.

### `clawhub.go` (~291 lines)
HTTP client for the ClawHub skill registry:
- `/api/v1/search` -- vector search for skills
- `/api/v1/download?slug=` -- skill ZIP download (max 32 MiB)
- Convex API for browsing: `/api/query` with `listPublicPageV4` pagination
- Supports auth token (GitHub PAT) for higher rate limits

### `github.go`
GitHub archive downloader for `owner/repo[@ref]` skill installation.

### `npm.go`
NPM package resolver for skill dependencies.

### `shim/host.mjs` (~425 lines)
JavaScript host for Tier 3 subprocess plugins. Provides OpenClaw-compatible `api` object to plugins. Maps between OpenClaw event names and GoStaff's hook system.

### `shim/openclaw-sdk/`
Embedded SDK stubs so OpenClaw plugins can `import` from `openclaw/plugin-sdk` without installing anything. Contains `core.mjs` (utility stubs) and `plugin-entry.mjs` (factory pattern adapter).

---

## `internal/tools/` -- Built-in Tools

All tools implement the `agent.Tool` interface.

### Core Tools (sent to LLM)
| Tool | File | Description |
|------|------|-------------|
| `shell_exec` | `shellexec.go` | Runs shell commands. Three modes: `allowlist` (default), `prompt` (asks user approval), `unrestricted`. |
| `file_read` | `fileops.go` | Reads file content. Supports glob patterns and line ranges. |
| `file_write` | `fileops.go` | Writes content to a file. Creates parent directories. |
| `file_edit` | `fileops.go` | Applies search-and-replace edits to a file. |
| `web_search` | `websearch.go` | Searches via DuckDuckGo HTML scraping or SearXNG (if configured). |
| `web_fetch` | `webfetch.go` | Fetches a URL and extracts readable text (HTML -> text conversion). |
| `search` | `search.go` | Grep-like recursive file/content search. |
| `use_tool` | `usetool.go` | Meta-tool that proxies calls to extended tools. Lists available extended tools in its description. |

### Extended Tools (behind `use_tool`)
| Tool | File | Description |
|------|------|-------------|
| `browser` | `browser.go` | Playwright-based browser automation. Navigate, click, type, screenshot, evaluate. |
| `notebook` | `notebook.go` | JavaScript notebook execution via Bun. |
| `schedule` | `schedule.go` | Manage automations (list/create/update/delete). |
| `todo` | `todo.go` | In-memory TODO list management. |
| `memory` | `memory.go` | Persistent key-value memory (read/write/list/delete). |

### Skill Management Tools (registered as extended)
| Tool | File | Description |
|------|------|-------------|
| `skill_create_markdown` | `skill_create_markdown.go` | Creates a new Tier 1 SKILL.md. |
| `skill_edit` | `skill_edit.go` | Edits an existing SKILL.md. |
| `skill_delete` | `skill_delete.go` | Deletes a skill directory. |
| `plugin_create` | `plugin_create.go` | Creates a Tier 3 plugin (SKILL.md + source). |
| `plugin_edit` | `plugin_edit.go` | Edits an existing plugin's source. |
| `plugin_delete` | `plugin_delete.go` | Deletes a plugin skill. |
| `skill_search` | `skill_search.go` | Searches ClawHub for skills. |

---

## `internal/transport/` -- Chat Platform Adapters

### `transport.go`
**`Transport` interface:** `Start(ctx)`, `Send(ctx, OutboundMessage)`, `OnMessage(handler)`, `Name() string`

**`InboundMessage`:** ChannelID, UserID, Text, Attachments (filename + data + mime type)

**`OutboundMessage`:** ChannelID, Text, DisplayName, AvatarURL, AvatarData (base64 data URI for Discord webhooks)

### `discord.go`
Discord gateway bot using discordgo. Listens for `MessageCreate` events, ignores own messages, handles message attachments (images/PDFs -> MediaPart). Sends replies via webhooks (for custom display name + avatar) with fallback to regular channel messages. Splits long messages at 2000 chars.

### `discord_roles.go`
Creates Discord roles for people and tags.

### `discord_setup.go`
- `SyncPeopleRoles(ctx, client, store, logger)` -- creates Discord roles for all people/tags at startup
- `AvatarToDataURI(avatarURL)` -- reads local avatar files and converts to base64 data URI

### `discord_send.go`
Webhook-based message sending with display name/avatar override. Creates webhooks per channel on first use, caches them.

### `telegram.go`
Telegram Bot API. Supports long-polling (default) or webhook mode. Sends replies via `sendMessage` with Markdown parse mode. Handles photo/document attachments.

### `slack.go` (~402 lines)
Slack Socket Mode using the Slack API. Listens for `events_api` events (`message` type), sends replies via `chat.postMessage`. Handles file attachments via Slack file download.

### `http.go`
Simple HTTP transport. Exposes POST `/message` endpoint. Used as a universal fallback transport.

---

## `internal/api/` -- REST API + Web UI

### `server.go` (~964 lines)
HTTP server using Go's `net/http.ServeMux`. 53 endpoints:

**Chat & Conversations:**
- `POST /api/chat` -- send a message, get agent response
- `POST /api/chat/stream` -- SSE streaming chat
- `GET /api/conversations` -- list conversations
- `GET /api/conversations/{id}` -- get conversation details

**Skills:**
- `GET /api/skills` -- list loaded skills
- `GET /api/skills/catalog` -- browse ClawHub catalog
- `POST /api/skills/install` -- install from ClawHub/GitHub/URL
- `POST /api/skills/create` -- create Tier 3 plugin
- `POST /api/skills/create-markdown` -- create Tier 1 skill
- `GET/PUT/DELETE /api/skills/{name}` -- skill CRUD

**Automations:**
- `GET/POST /api/automations` -- list/create automations
- `GET/PUT/DELETE /api/automations/{id}` -- automation CRUD
- `POST /api/automations/{id}/trigger` -- trigger manually
- `GET /api/automations/{id}/runs` -- list runs

**Runs:**
- `GET /api/runs` -- list all recent runs
- `GET /api/runs/{automationID}/{runID}/trace` -- run trace
- `POST /api/runs/{runID}/stop` -- stop a run
- `GET /api/runs/{runID}/stream` -- SSE stream run output

**People (bot personas):**
- `GET/POST /api/people` -- list/create
- `GET/PUT /api/people/system-prompt` -- global system prompt
- `PUT/DELETE /api/people/{id}` -- update/delete

**Memory:**
- `GET/PUT /api/memory` -- list/store entries
- `DELETE /api/memory/{key}` -- delete entry

**Config & Settings:**
- `GET/PUT /api/config/keys` -- per-mode API key overrides
- `GET/PUT /api/config/transport-keys` -- transport tokens
- `GET/PUT /api/settings/execute-fallback` -- shell fallback toggle
- `GET/PUT /api/settings/shell-mode` -- shell mode
- `GET/PUT /api/settings/shell-approved` -- approved commands

**Modes:**
- `GET/PUT/DELETE /api/modes` -- mode CRUD
- `GET/PUT /api/modes/active` -- active mode

**Usage & Models:**
- `GET /api/usage` -- token usage summary
- `GET /api/credits` -- provider credit balance (OpenRouter)
- `GET /api/providers` -- list available providers/models

**Other:**
- `GET /api/health` -- health check
- `GET /api/logs` -- SSE stream of server logs
- `POST /api/avatars` -- upload avatar
- `GET /api/avatars/{file}` -- serve avatar images
- `GET /*` -- embedded web UI (when built)

### `middleware.go`
- API key authentication (Bearer token, optional)
- CORS (permissive: all origins, all methods)
- Rate limiting (per-IP, configurable RPM)

### Other API files
- `automations.go` -- CRUD handlers for automations
- `config_keys.go` -- per-mode API key management
- `modes.go` -- mode CRUD + activate
- `people.go` -- people CRUD + avatar upload
- `skills_create.go` -- skill installation/creation handlers
- `execute_fallback.go` -- toggles shell mode
- `usage.go` -- token usage summary + credit balance
- `memory_handlers.go` -- memory CRUD handlers

---

## `internal/sdk/` -- Plugin SDK

### `sdk.go`
Defines the `Plugin` interface and `Registration` struct. A plugin can register tools, hooks (pre/post tool execution), HTTP routes, and LLM providers.

**`Plugin` interface:** `Name()`, `Init() (*Registration, error)`

**`Registration`:** Tools ([]agent.Tool), Hooks ([]agent.ToolHook), Routes ([]Route), Providers ([]ProviderEntry)

### `openclaw.go` (~214 lines)
`OpenClawPlugin` -- wraps a Tier 3 plugin directory as a Go SDK Plugin. Spawns the plugin process, communicates via stdin/stdout JSON.

**Supported OpenClaw features:** registerTool, registerCommand (as tools with `cmd_` prefix), registerHook (pre/post tool use), registerHttpRoute, registerProvider, registerService, registerWebSearchProvider (as regular tool).

**Unsupported (no-ops):** registerChannel, registerGatewayMethod, registerCli, registerSpeechProvider, registerMediaUnderstandingProvider, registerImageGenerationProvider, registerInteractiveHandler, registerContextEngine, registerMemoryPromptSection.

**Limitation:** Provider `Stream()` returns error -- plugins only support request-response chat, not streaming.

---

## `internal/cron/` -- Automation Scheduler

### `scheduler.go` (~323 lines)
Polls `ListDueAutomations` every 30 seconds. For each due automation:
1. Start a run record
2. Build messages from the automation's prompt (with skill-specific system prompt injection)
3. Call the agent runner
4. Record success/failure
5. Calculate next run time from RRULE

### `parser.go`
RRULE parser -- converts RFC 5545 recurrence rules to next occurrence times. Supports FREQ (MINUTELY, HOURLY, DAILY, WEEKLY, MONTHLY, YEARLY), INTERVAL, BYDAY, COUNT, UNTIL.

---

## `internal/log/` -- Logging

### `log.go`
Wraps zerolog. `New(level, pretty)` creates a logger.

### `broadcast.go`
`Broadcaster` -- fan-out writer. All log output written to it gets forwarded to registered SSE listeners. The web UI connects to `/api/logs` to receive real-time logs.

---

## `internal/updater/` -- Auto-Update

### `updater.go`
Hits the GitHub Releases API, compares latest tag to current version, downloads the correct OS/arch tar.gz, extracts the binary, and atomically replaces the running executable. Called by `gostaff update`. Also exports `LatestTag()` for version checks.

---

## `web/src/` -- React Frontend

React 19 + Vite + Tailwind CSS v4 + React Router v7 + Framer Motion + Lucide icons.

### Pages
| Page | Route | Description |
|------|-------|-------------|
| ChatPage | `/`, `/chat` | Main chat interface with streaming responses, session management |
| DashboardPage | `/dashboard` | Server logs (SSE stream), session list, quick stats |
| AutomationsPage | `/automations` | CRUD for scheduled automations with RRULE builder |
| SkillsPage | `/skills` | List installed skills, install from ClawHub/GitHub/URL |
| PluginsPage | `/plugins` | List and manage Tier 3 plugins |
| PeoplePage | `/people` | CRUD for bot personas (name, prompt, avatar, tags) |
| MemoryPage | `/memory` | View/edit/delete key-value memory entries |
| SettingsPage | `/settings` | System prompt, shell mode, API keys, modes |
| CostsPage | `/costs` | Token usage breakdown by provider/model/mode, credit balance |

### Components
- **AlertProvider** -- toast notifications + confirm dialogs via React context (`useAlert` hook). Wraps the app. Supports `dismissKey` for "don't show again" persistence via localStorage.
- **Sidebar** -- navigation with Lucide icons, collapsible
- **Calendar** -- month view for automation scheduling
- **DatePicker** (~546 lines) -- date/time picker using Calendar
- **Markdown** -- renders markdown content with KaTeX math support
- **PillSwitch** -- toggle switch component
- **SkillPicker** -- multi-select for skills (used in automations)
- **TagPicker** -- tag input for people

### `lib/api.ts` (~406 lines)
Fetch wrapper. Base URL from `window.location.origin`. All API calls go through `apiFetch(path, options)`. Contains TypeScript interfaces for all API types and functions for every endpoint. Differentiates skills by `source: 'custom' | 'clawhub'` and `tier`.

---

## Key Data Flows

### Chat request (Web UI)
1. User types message in ChatPage
2. `POST /api/chat/stream` with `{session_id, message}` via SSE
3. API handler calls `runAgent(ctx, sessionID, messages, onEvent)` with SSE event callback
4. Agent runs ReAct loop -> emits thinking/tool_start/tool_end/response events
5. Events streamed to browser via SSE
6. Final response displayed in chat

### Chat request (Transport -- Discord/Telegram/Slack)
1. Message arrives via gateway/webhook/socket
2. `makeMessageHandler` checks content filter -> resolves @person/@tag mentions -> extracts @model tag
3. Calls `runAgentEphemeral(ctx, systemPrompt, modelID, channelID, messages)`
4. Agent runs ReAct loop (no streaming, usage-only persistence)
5. Response sent back via transport

### Automation run
1. Cron scheduler finds due automations (next_run_at <= NOW)
2. Creates automation_run record with status=running
3. Builds messages from automation prompt, injects skill instructions if skill_names set
4. Calls `runAgent(ctx, sessionID, messages, nil)`
5. Records success/failure + response
6. Calculates next run from RRULE

### Skill loading
1. `initSkillRegistry` scans configured directories for `<name>/SKILL.md`
2. Parser extracts YAML frontmatter + markdown body
3. Skills registered by name in registry
4. `BuildSystemPrompt` concatenates base prompt + all skill instructions
5. Hot-reload goroutine polls for new/changed SKILL.md files every 2s

### Model routing
1. Request may specify model via `@model-id` tag in message or mode's default model
2. Router checks if a model is set -> finds the provider that owns it
3. Falls through to primary -> fallback providers on retryable errors (429/5xx)

---

## Configuration

### `config.yaml` (YAML + env overrides)
```yaml
server:
  addr: ":8080"
log_level: info
database:
  url: postgres://...           # or GOSTAFF_DATABASE_URL
providers:
  anthropic:
    api_key: ""                 # or GOSTAFF_ANTHROPIC_API_KEY
    model: claude-sonnet-4-6-20250514
  openai:
    api_key: ""                 # or GOSTAFF_OPENAI_API_KEY
    base_url: https://api.openai.com/v1
    model: gpt-4o
  gemini:
    api_key: ""                 # or GEMINI_API_KEY / GOOGLE_API_KEY
    model: gemini-2.0-flash
  openrouter:
    api_key: ""                 # or GOSTAFF_OPENROUTER_API_KEY
    model: anthropic/claude-sonnet-4-5
agent:
  max_iterations: 25            # 0 = unlimited
  context_budget_pct: 0.8
  max_tool_output_tokens: 4096
skills:
  dirs: [~/.gostaff/skills]
security:
  api_key: ""                   # bearer token for REST API
  rate_limit_rpm: 0             # 0 = disabled
  content_filtering: false
  session_ttl_days: 0           # 0 = keep forever
  shell_allowlist: [ls, cat, head, tail, grep, wc, date, echo, pwd]
transports:
  telegram: { token: "" }
  discord: { token: "" }
  slack: { app_token: "", bot_token: "" }
```

### Modes
Modes are named configurations that override the default model + API keys + behavior:
- **default** -- full tools + extended thinking
- **chat** -- no tools, thinking disabled (fast + cheap)
- **execute** -- same as default but with unrestricted shell
- Custom modes via API/settings

---

## Database

Postgres 17. Connection via pgx. Migrations embedded in binary (16 migrations, 001-016).

**Key tables:**
- `sessions` + `messages` + `tool_executions` -- conversation history + audit trail
- `automations` + `automation_runs` -- scheduled agent tasks
- `people` -- bot personas with per-person system prompts
- `memory` -- persistent key-value store for the agent
- `settings` -- global config (system prompt, active mode, shell mode)
- `modes` -- per-mode LLM config (API keys, model, summarization model)
- `usage_log` -- token usage tracking for cost analysis
- `channel_bindings` / `discord_tag_roles` -- Discord-specific routing

---

## Dependencies

**Go (go.mod, Go 1.26.1):**
- `discordgo` -- Discord gateway
- `pgx/v5` -- Postgres driver
- `zerolog` -- structured logging
- `gorilla/websocket` -- WebSocket support
- `google.golang.org/genai` -- Gemini SDK
- `gopkg.in/yaml.v3` -- YAML parsing
- `google/uuid` -- UUID generation

15 total direct dependencies.

**Frontend (package.json):**
- React 19, React Router v7
- Vite + @vitejs/plugin-react
- Tailwind CSS v4 (via @tailwindcss/vite)
- Lucide React (icons)
- Framer Motion (animations)
- react-markdown + remark-gfm + rehype-highlight + KaTeX
- ~7 direct dependencies + devDeps

---

## How to Add a New Feature

### Add a new built-in tool
1. Create `internal/tools/mytool.go` implementing `agent.Tool` interface
2. Register in `initToolRegistry` in `cmd/gostaff/init.go` -- use `Register` for core (sent to LLM) or `RegisterExtended` for behind-use_tool
3. Add API endpoint if the tool needs web UI access

### Add a new API endpoint
1. Add handler in `internal/api/` (create new file or add to existing)
2. Register route in `server.go`'s `New()` constructor
3. Add corresponding fetch function in `web/src/lib/api.ts`
4. Add UI in the appropriate page component

### Add a new transport
1. Create `internal/transport/mytransport.go` implementing `Transport` interface
2. Add config type to `internal/config/config.go` under `TransportsConfig`
3. Wire in `cmd/gostaff/serve.go`

### Add a new LLM provider
1. Create `internal/llm/myprovider.go` implementing `Provider` interface
2. Add config type to `internal/config/config.go` under `ProvidersConfig`
3. Add env override in `applyEnvOverrides`
4. Wire in `initRouter` in `cmd/gostaff/init.go`
5. Add models to token pricing table in `internal/agent/agent.go`

### Add a new web page
1. Create `web/src/pages/MyPage.tsx`
2. Add route in `web/src/App.tsx`
3. Add nav link in `web/src/components/Sidebar.tsx`
4. Add API functions in `web/src/lib/api.ts`
