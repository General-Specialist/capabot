# Capabot — Full OpenClaw Replacement in Go

## Context

OpenClaw (163K+ GitHub stars, 5,700+ skills, 20+ channels) is the dominant open-source AI agent framework — but it's built on Node.js, has a massive dependency footprint, and a trail of CVEs (shell injection, allowlist bypasses, command smuggling). The goal is to build a Go-native replacement that matches OpenClaw's feature surface **as closely as humanly possible** while eliminating its architectural weaknesses.

Capabot ships as a single static binary with embedded SQLite, sandboxed skill execution, and a pluggable provider system.

## Competitive Landscape — Why Another One?

Several Go/Rust OpenClaw alternatives already exist:

| Project | Language | Stars | What It Does Well | What It's Missing |
|---|---|---|---|---|
| **LiteClaw** | Go | ~2K | Single binary, 10MB idle, basic tools | No skill compat, no web UI, no multi-agent |
| **PicoClaw** | Go | ~5K | Ultra-lightweight, runs on RISC-V, 7 channels | No web UI, no skill marketplace, no security model, 5 channels |
| **ZeroClaw** | Rust | ~10K | 4MB RAM, 22+ providers, vector memory | No plugin system by design, CLI-only, limited community |
| **openclaw-go** | Go | ~500 | Direct port attempt | Incomplete, limited maintenance |

**None of them solve the full replacement problem.** They're lightweight *subsets* of OpenClaw, not replacements. Capabot's thesis is different:

### Capabot's 4 Differentiators

1. **Direct OpenClaw SKILL.md compatibility** — Import and run OpenClaw's 5,700+ markdown skills as-is. No translation, no porting. This is the killer feature nobody else has. The three-tier skill engine (Markdown → Native Go → WASM) means the entire OpenClaw skill ecosystem is available on day one, with a path to native performance for hot-path skills.

2. **Full web UI (embedded in binary)** — Zero Go alternatives have a web dashboard. Capabot ships React + Tailwind + Vite compiled into the same `CGO_ENABLED=0` binary. Conversations, skill management, agent config, provider routing — all in-browser with no separate frontend build.

3. **Multi-agent orchestration** — No lightweight alternative supports agent-to-agent delegation, parallel tool execution, or workflow chaining. Capabot's orchestrator enables parent→child agent spawning, which is required for complex OpenClaw workflows (e.g., research agent → writing agent → review agent).

4. **CVE-mapped security architecture** — Not just "we use Go." Every major OpenClaw CVE class (CVE-2026-32032 shell injection, CVE-2026-31992 command injection, CVE-2026-32000/31999/31995 allowlist bypasses, CVE-2026-22176 privilege escalation) maps to a specific architectural decision in Capabot that prevents it by construction.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────┐
│                    Web UI (React + Tailwind)          │
├─────────────────────────────────────────────────────┤
│                    HTTP/WebSocket Gateway            │
├──────────┬──────────┬──────────┬────────────────────┤
│ Telegram │ Discord  │  Slack   │  HTTP API / CLI     │
│ Adapter  │ Adapter  │ Adapter  │  Adapter            │
├──────────┴──────────┴──────────┴────────────────────┤
│                    Router                            │
│         (auth, rate-limit, tenant isolation)         │
├─────────────────────────────────────────────────────┤
│                    Orchestrator                      │
│      (multi-agent coordination, workflow engine)     │
├─────────────────────────────────────────────────────┤
│                    Agent Core                        │
│        (LLM loop, tool dispatch, memory)             │
├──────────────┬──────────────────────────────────────┤
│  Skill Engine │         LLM Provider                 │
│  (MD + Go +   │    (pluggable: Anthropic,            │
│   WASM)       │     OpenAI-compat, etc.)             │
├──────────────┴──────────────────────────────────────┤
│                    Storage Layer                     │
│         (SQLite embedded, per-tenant isolation)      │
└─────────────────────────────────────────────────────┘
```

---

## Phase 1: Foundation (Weeks 1-3) — ✅ COMPLETE

**Logging** (`internal/log/`): zerolog-based structured JSON logger with context fields (tenant/session/agent ID), level filtering, `ParseLevel` helper. `NewWithWriter` for fan-out to SSE broadcaster. 12 unit tests.

**Log Broadcaster** (`internal/log/broadcast.go`): 500-entry ring buffer + fan-out to SSE subscribers. Implements `io.Writer` so zerolog writes to it. Used by web UI `/api/logs` SSE endpoint.

### 1.1 Project Scaffolding ✅
- [x] Go module init, directory structure
- [x] Makefile: `build`, `build-linux`, `build-arm`, `test`, `test-short`, `test-cover`, `lint`, `fmt`, `run`, `dev`, `migrate`, `web`, `web-install`, `web-dev`, `clean`, `help`
- Target structure:

```
cmd/
  capabot/              # main binary entrypoint
internal/
  agent/                # core agent loop
  config/               # configuration loading
  llm/                  # LLM provider abstraction
  memory/               # SQLite-backed memory store
  orchestrator/         # multi-agent coordination
  skill/                # skill engine (loader, registry, executor)
  transport/            # channel adapter interface + Telegram/Discord/Slack/HTTP
  api/                  # REST API + web UI server
  tools/                # built-in tool implementations
  log/                  # structured logging + SSE broadcaster
web/
  src/                  # React + Tailwind + Vite SPA
migrations/             # SQLite schema migrations
```

### 1.2 Configuration System ✅
- [x] YAML config file (`~/.capabot/config.yaml`)
- [x] Environment variable overrides (`CAPABOT_` prefix)
- [x] Config struct with validation at startup (addr, log level, iterations, budget)
- [x] Transport tokens via env: `CAPABOT_TELEGRAM_TOKEN`, `CAPABOT_DISCORD_TOKEN`, `CAPABOT_SLACK_APP_TOKEN`, `CAPABOT_SLACK_BOT_TOKEN`
- [x] Security config: `APIKey`, `RateLimitRPM`, `ContentFiltering`, `SessionTTLDays`, `ShellAllowlist`, `DrainTimeout`
- [ ] Per-tenant config isolation

### 1.3 Storage Layer ✅
- [x] `modernc.org/sqlite` (pure Go, no CGo)
- [x] Schema: sessions, messages, tool_executions, memory (with embeddings)
- [x] Migration system (idempotent, tested for double-apply)
- [x] Per-tenant database isolation (separate SQLite files)
- [x] Repository pattern: `CreateSession`, `GetSession`, `SaveMessage`, `GetMessages`, `SaveToolExecution`, `StoreMemory`, `RecallMemory`, `ListMemory`, `DeleteMemory`, `DeleteOldSessions`
- [x] **Vector memory**: Pure-Go brute-force cosine similarity over embeddings stored as raw little-endian bytes in SQLite. Tested at 5,000 vectors × 128 dims, recall <100ms.
- [x] **SQLite concurrency hardening**: WAL mode + `synchronous=NORMAL` enforced on every connection. Single-writer/multi-reader `Pool` with `WriteTx` serialization. Tested with 10 concurrent goroutines × 10 writes (100 total), zero lock errors.
- [x] Session TTL cleanup: `DeleteOldSessions` with explicit cascade (explicit delete of tool_executions → messages → sessions). Background goroutine runs every 6h.
- [ ] Schema: tenants, skills, config tables (not yet needed)
- **Upgrade path**: In-process pure-Go HNSW index backed by SQLite for persistence when any tenant exceeds 50K embeddings.

---

## Phase 2: LLM Provider System (Weeks 2-4) — ✅ COMPLETE

### 2.1 Provider Interface ✅
```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
    Models() []ModelInfo
    Name() string
}
```
Implemented in `internal/llm/provider.go` with full type system: `ChatRequest`, `ChatMessage`, `ChatResponse`, `ToolDefinition`, `ToolCall`, `ToolResult`, `StreamChunk`, `Usage`, `ModelInfo`.

### 2.2 Built-in Providers ✅
- [x] **Anthropic** — Messages API (Claude models). Full Chat + Stream + tool use. `internal/llm/anthropic.go`. Tests: `anthropic_test.go`
- [x] **OpenAI-compatible** — `/v1/chat/completions`. Any OpenAI-compatible provider. Full Chat + Stream + tool use. `internal/llm/openai.go`. Tests: `openai_test.go`
- [x] **Gemini** — `google.golang.org/genai` SDK, `gemini-3-flash-preview` default. Full Chat + Stream + tool use. Handles Gemini 3's thinking/reasoning parts (skips `Thought` tokens). `internal/llm/gemini.go`. Tests: 11 unit + 2 integration
- [x] **OpenRouter** — `openrouter.ai` gateway to 100+ models. `internal/llm/openrouter.go` wraps `OpenAIProvider` with OpenRouter-specific base URL + `X-Title`/`HTTP-Referer` headers. 3 tests. Env: `CAPABOT_OPENROUTER_API_KEY`, `CAPABOT_OPENROUTER_MODEL`.

### 2.3 Routing & Fallback ✅
- [x] `Router` in `internal/llm/router.go` — primary + fallback chain
- [x] Fallback on 429/5xx: tries next provider in chain
- [x] Provider key rotation support via config
- [x] `ProviderMap()` accessor for web UI provider listing
- [ ] Model selection tiers (fast/balanced/powerful) — future
- [ ] Token budget tracking per tenant — future

---

## Phase 3: Transport Layer (Weeks 3-5) — ✅ COMPLETE

### 3.1 Transport Interface ✅
```go
type Transport interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg OutboundMessage) error
    OnMessage(handler func(ctx context.Context, msg InboundMessage))
    Name() string
}
```
All transports normalize messages into `InboundMessage` / `OutboundMessage`.

### 3.2 Telegram Adapter ✅
- [x] Long-polling mode (development) + webhook mode (production)
- [x] Bot API via raw `net/http` (no SDK dependency)
- [x] 7 unit tests (`transport/telegram_test.go`)

### 3.3 Discord Adapter ✅
- [x] WebSocket gateway connection for real-time events
- [x] Slash command registration
- [x] 6 unit tests (`transport/discord_test.go`)

### 3.4 Slack Adapter ✅
- [x] Socket Mode (no public endpoint needed)
- [x] Thread-aware conversations
- [x] 5 unit tests (`transport/slack_test.go`)

### 3.5 HTTP API Adapter ✅
- [x] RESTful API: `POST /api/chat`, `POST /api/chat/stream` (SSE), `GET /api/health`
- [x] `GET /api/logs` — real-time SSE log streaming (replays 100 recent + fan-out)
- [x] `GET /api/agents`, `GET /api/providers`, `GET /api/tools`, `GET /api/skills`, `GET /api/sessions`
- [x] Bearer token auth middleware (`X-API-Key` / `Authorization: Bearer`)
- [x] Token-bucket rate limiter per client IP (configurable RPM)
- [x] Content filter: 20+ prompt injection patterns, whitespace normalization

---

## Phase 4: Agent Core (Weeks 4-6) — ✅ COMPLETE

### 4.1 Agent Loop ✅
- [x] ReAct-style loop: Observe → Think → Act → Observe (`internal/agent/agent.go`)
- [x] Tool/skill dispatch with configurable max iterations (default 25)
- [x] **Real-time event streaming**: `AgentEvent` type with kinds `thinking`, `tool_start`, `tool_end`, `response`. `SetOnEvent(fn)` callback wired into `handleChatStream` via 64-entry buffered channel — agent never blocks
- [x] **Context window management (multi-strategy)** (`internal/agent/context.go`):
  - [x] **Sliding window**: `BuildMessages()` keeps first message + most recent N-1 messages
  - [x] **Token budget tracking**: `ContextManager` tracks cumulative usage, flags at 80% threshold
  - [x] **Observation truncation**: Large tool outputs truncated to configurable max (default 4K tokens × 4 chars)
- [x] System prompt composition passed to LLM on every iteration
- [x] Context cancellation support (graceful abort)
- [x] Audit logging: messages and tool executions persisted via `StoreWriter` interface
- [x] **Content filter** (`internal/agent/filter.go`): 20+ prompt injection patterns, multiline normalization, configurable max length. 5 tests.
- **28 unit tests** + **5 integration tests** (mock provider, ReAct loop, max iterations, context cancellation, unknown tool recovery)

### 4.2 Session Management ✅
- [x] Session persistence schema in SQLite (sessions, messages, tool_executions tables)
- [x] `memory.Store` API: full CRUD
- [x] `memory.Store.SaveToolExecution` for audit trail
- [x] Session TTL cleanup (background goroutine, configurable days)
- [ ] Per-user, per-channel session routing (transport layer wires channel ID as session ID)
- [ ] Cross-channel session continuity

### 4.3 Tool System ✅
```go
type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}
```
- [x] `Tool` interface + `Registry` (thread-safe, sorted `Names()`)
- [x] `ToolResult` type with content + error flag
- [x] **Built-in tools** (`internal/tools/`):
  - [x] `web_search` — configurable backend (SearXNG, Brave, etc.)
  - [x] `web_fetch` — fetch and extract content from URLs
  - [x] `file_read` / `file_write` — sandboxed filesystem access
  - [x] `shell_exec` — allowlist-only, no shell interpretation (`os/exec` direct argv)
  - [x] `memory_store` / `memory_recall` — long-term memory via `memory.Store`
  - [x] `schedule` — delay/wait tool with configurable max duration, context cancellation

---

## Phase 5: Skill Engine (Weeks 5-8) — ✅ COMPLETE

**Design goal: run OpenClaw's 5,700+ skills without modification.** Validated against 32,814 real ClawHub skills.

### OpenClaw Skill Format ✅
Capabot's skill loader:
1. [x] **Forgiving SKILL.md parser** — `goldmark` + custom AST walkers, `gopkg.in/yaml.v3` lenient mode
2. [x] **Skill injection into system prompt** — `BuildSystemPrompt` wires all loaded skills into the default agent system prompt at serve startup
3. [x] **Tool name mapping** — `internal/skill/toolmap.go` maps OpenClaw→Capabot tool names (e.g., `system.run` → `shell_exec`)
4. [x] **`requires.bins` validation** — `CheckRequirements` checks all declared binaries against host PATH; `LintSkill` includes warnings for missing binaries
5. [x] **Skill precedence** — workspace > user > bundled (same as OpenClaw)

### Implemented Components ✅
- **`parser.go`** — Forgiving SKILL.md parser (frontmatter + markdown body)
- **`parser_test.go`** — 11 unit tests
- **`lint.go`** — `capabot skill lint` with error + warning reporting including `requires.bins`
- **`lint_test.go`** — Lint validation tests
- **`importer.go`** — Copies and validates OpenClaw skill directories
- **`importer_test.go`** — Import workflow tests
- **`toolmap.go`** — OpenClaw→Capabot tool name translation table
- **`toolmap_test.go`** — Tool mapping tests
- **`inject.go`** / **`inject_test.go`** — `BuildSystemPrompt` for skill injection
- **`registry.go`** — `Registry` with `LoadDir`, `Get`, `List`, `Len`, `WASMPath`, `WASMSkillNames`
- **`registry_test.go`** — Registry tests
- **`types.go`** — Skill types including `SkillManifest.Parameters json.RawMessage` for WASM schemas
- **`wasm.go`** — `WASMExecutor` (wazero runtime, compiles once, instantiates per-call for isolation)
- **`wasm_tool.go`** — `WASMTool` adapter (`ParsedSkill` + `WASMExecutor`)
- **`wasm_test.go`** — 5 WASM tests
- **`clawhub_integration_test.go`** — Validated against 32,814 real ClawHub skills

### Three-Tier Execution Model ✅

**Tier 1: Markdown-only skills (OpenClaw compatible)** ✅
- Pure instruction injection — the LLM follows them using available tools
- Covers the majority of OpenClaw's skill catalog
- Zero code execution, zero security surface

**Tier 2: Native Go skills** ✅
- Implement the `Tool` interface directly in Go
- All built-in tools: web_search, web_fetch, file_read, file_write, shell_exec, memory_store, memory_recall, schedule

**Tier 3: WASM sandboxed skills** ✅
- [x] `wazero` runtime (pure Go, no CGo) — `internal/skill/wasm.go`
- [x] Strict sandbox: no filesystem mounts, no network, WASI snapshot_preview1 only
- [x] Host function ABI: module exports `capabot_write_input(len) ptr` + `run()`, calls host import `capabot.set_output(ptr, len)`
- [x] `skill.wasm` auto-detected alongside `SKILL.md` at load time — registered as callable tool
- [x] `wasmAgentTool` adapter in serve.go bridges `skill.WASMTool` → `agent.Tool` without import cycles
- [x] `SkillManifest.Parameters json.RawMessage` for WASM skill JSON Schema declarations
- [x] 5 tests: metadata, default schema, result parsing, invalid bytes rejection

### Skill Registry & Import ✅
- [x] `capabot skill import <openclaw-skill-dir>` — copies and validates OpenClaw skills
- [x] `capabot skill install <url>` — download .zip or .tar.gz, extract with path traversal protection
- [x] `capabot skill create <name>` — scaffold new skill directory
- [x] `capabot skill lint [path...]` — lint SKILL.md files for compatibility
- [x] `capabot dev` — hot-reload watcher (2s polling, auto-lint on change)
- [x] Tool name mapping table for OpenClaw→Capabot translation

---

## Phase 6: Multi-Agent Orchestration (Weeks 7-9) — ✅ COMPLETE

### 6.1 Orchestrator ✅ (`internal/orchestrator/`)
- [x] `orchestrator.go` — parent spawns child agents for subtasks
- [x] `registry.go` — `AgentConfig` registry for named agent configurations
- [x] `spawn_tool.go` — `spawn_agent` tool the LLM can call to delegate to sub-agents
- [x] Tests in `orchestrator_test.go`

### 6.2 Agent Registry ✅
```go
type AgentConfig struct {
    ID           string
    Name         string
    SystemPrompt string
    Provider     string      // which LLM provider/model
    Skills       []string    // enabled skills
    Tools        []string    // enabled tools
    MaxTokens    int
    Temperature  float64
}
```
- [x] `capabot agent list` — list configured agents
- [x] `GET /api/agents` — API endpoint

---

## Phase 7: Web UI (Weeks 8-11) — ✅ COMPLETE

### 7.1 Tech Stack ✅
- **React + TypeScript** — SPA with client-side routing
- **Tailwind CSS + shadcn/ui** — utility-first styling, component library
- **Vite** — build tool, HMR in dev
- Static build embedded in binary via `//go:embed web/dist`

### 7.2 Pages ✅
- [x] **Dashboard** — system health, quick stats, recent activity
- [x] **Chat** — real-time streaming chat with tool call progress display
- [x] **Conversations** — browse sessions, full history view
- [x] **Conversation Detail** — per-session message history
- [x] **Skills** — browse installed skills, install from URL
- [x] **Agents** — view configured agents
- [x] **Providers** — view LLM providers + expandable model cards
- [x] **Settings** — tenant config display
- [x] **Logs** — real-time SSE log streaming, level-colored, filter input, 2000-line cap

### 7.3 Real-Time Features ✅
- [x] Streaming LLM responses via SSE (`POST /api/chat/stream`)
- [x] Real-time tool call events: `thinking`, `tool_start`, `tool_end`, `response` kinds
- [x] Live log streaming via SSE (`GET /api/logs`) with ring-buffer replay
- [x] Agent status indicators (thinking, executing tool) in Chat UI

---

## Phase 8: Security Hardening (Ongoing) — ✅ IMPLEMENTED

| OpenClaw CVE | Vulnerability | Capabot Mitigation |
|---|---|---|
| CVE-2026-32032 | Shell injection via `SHELL` env var | No shell interpretation. `os/exec` with explicit argv, never `sh -c` |
| CVE-2026-31992 | Command injection in Lobster extension via `shell: true` | No `shell: true` fallback. All exec uses direct syscall, never shell dispatch |
| CVE-2026-32000 | Allowlist bypass via `env -S` wrapper chains | Allowlist resolves the **final binary path** via `exec.LookPath`, not argv[0] |
| CVE-2026-31999 | Allowlist bypass via shell line-continuation | Arguments are discrete strings, never joined into a shell command string |
| CVE-2026-31995 | Allowlist bypass via `env bash` wrapper smuggling | Wrapper chain unwinding: recursively resolve through `env`, `bash -c`, etc. |
| CVE-2026-22176 | Privilege escalation via plugin loader | No dynamic code loading outside WASM runtime. Go plugins disabled. |
| CVE-2026-25253 | One-click RCE via crafted links | No URL-triggered code execution. All tool invocations require explicit agent dispatch |
| CVE-2026-26327 | Authentication bypass on exposed instances | Mandatory auth on all endpoints. No unauthenticated access even on localhost |

Additional measures ✅:
- [x] Token-bucket rate limiter per client IP (configurable RPM)
- [x] Bearer token auth middleware (APIKey)
- [x] Content filter: 20+ prompt injection patterns, multiline normalization, configurable max length
- [x] Shell allowlist enforcement for `shell_exec` tool
- [x] Audit logging for all tool executions (`tool_executions` table)
- [x] Session TTL cleanup (configurable, background goroutine)
- [x] WASM sandbox: no filesystem, no network unless explicitly granted via host functions
- [x] Graceful shutdown with `context.Context` propagation and signal handling
- [ ] WASM host functions for controlled HTTP/memory access (future)
- [ ] Per-tenant data isolation (architecture supports it, not yet enforced at routing layer)

---

## Phase 9: CLI & Developer Experience (Weeks 10-12) — ✅ COMPLETE

### 9.1 CLI ✅
```
capabot serve              # start gateway + all configured channels
capabot chat               # interactive CLI chat session
capabot skill install <name-or-url> # install from ClawHub name or URL (.zip/.tar.gz)
capabot skill search <query>        # search ClawHub skill registry
capabot skill import <dir>  # import an OpenClaw skill directory
capabot skill create <name> # scaffold a new skill directory
capabot skill lint [path...] # lint SKILL.md files for compatibility
capabot agent list          # list configured agents
capabot config set <key> <value>
capabot migrate             # run database migrations
capabot dev                 # hot-reload mode for skill development
```

### 9.2 Developer Tooling ✅
- [x] `capabot dev` — polling-based (2s) hot-reload watcher for skill directories; auto-lints changed files
- [x] `capabot skill create <name>` — scaffolds new skill directory with SKILL.md template
- [x] `capabot skill install <name-or-url>` — ClawHub name → `DownloadSkill` + `ImportSkill`; URL → .zip/.tar.gz archive with path traversal protection
- [x] `capabot skill search <query>` — searches ClawHub registry, tabular output (name / version / description)
- [x] `capabot skill init [--wasm] <name>` — WASM skill scaffold: SKILL.md + main.go (Go WASM stub) + Makefile (`GOOS=wasip1 GOARCH=wasm`)
- [ ] OpenClaw bulk importer: `capabot migrate-from-openclaw` (post-launch)

---

## Verification Plan

### Unit Tests (per-package) — ✅ ALL PASSING
- `internal/agent` — 28 unit tests + 5 integration tests (mock provider)
- `internal/api` — 7 API tests (health, chat, auth, rate limit, SSE)
- `internal/config` — config loading and validation tests
- `internal/llm` — provider mock tests + real API integration tests (Gemini)
- `internal/log` — broadcaster + logger tests
- `internal/memory` — CRUD, vector recall, concurrency, migration tests
- `internal/orchestrator` — orchestrator + registry tests
- `internal/skill` — parser (11), lint, importer, toolmap, registry, inject, WASM (5) + 32,814 ClawHub integration
- `internal/tools` — all built-in tool tests
- `internal/transport` — Telegram (7), Discord (6), Slack (5) tests

### Integration Tests ✅
- [x] Full agent loop: message → LLM → tool → response (5 scenarios)
- [x] Multi-agent: orchestrator delegates to child agents
- [ ] Multi-channel: same conversation across Telegram + Discord
- [ ] Skill loading: WASM skills execute end-to-end (needs real .wasm binary)

### E2E Tests (future)
- [ ] Send a message via Telegram bot → verify response
- [ ] Use web UI to configure agent → send chat → verify streaming response
- [ ] Install a skill from registry → use it in conversation
- [ ] Verify rate limiting blocks excessive requests
- [ ] Verify WASM sandbox prevents unauthorized file/network access

### Security Tests (future)
- [ ] Fuzz tool argument parsing for injection vectors
- [ ] Verify allowlist enforcement with wrapper chain attempts
- [ ] Verify WASM skills cannot escape sandbox
- [ ] Verify per-tenant data isolation

---

## Key Dependencies

| Package | Purpose |
|---|---|
| `modernc.org/sqlite` | Embedded SQLite (pure Go) |
| `github.com/tetratelabs/wazero` | WASM runtime (pure Go) — Tier 3 skills |
| `github.com/rs/zerolog` | Structured logging |
| `yuin/goldmark` | Markdown parsing (forgiving SKILL.md extraction) |
| `gopkg.in/yaml.v3` | Config + frontmatter parsing |
| `google.golang.org/genai` | Gemini SDK |

**Zero CGo.** The entire binary compiles with `CGO_ENABLED=0` and cross-compiles to Linux/macOS/Windows/ARM.

---

## Architectural Dragons (Known Risks & Mitigations)

### Dragon 1: Vector Search vs. `CGO_ENABLED=0` ✅ RESOLVED
- **Decision**: Pure-Go brute-force cosine similarity at launch. Sub-10ms for <10K embeddings (typical chat memory). Upgrade path to pure-Go HNSW if agents need to index large corpora.
- **Tripwire**: If any single tenant's embedding count exceeds 50K, log a warning and recommend HNSW migration.

### Dragon 2: SKILL.md Parsing Chaos ✅ RESOLVED
- **Decision**: Forgiving parser using `goldmark` + custom AST walkers. `capabot skill lint` command for pre-import validation. Import never fails silently — it succeeds with warnings or fails with actionable errors.
- **Validated**: Against 32,814 real ClawHub skills.

### Dragon 3: SQLite Concurrency Under Multi-Agent Load ✅ RESOLVED
- **Decision**: WAL mode + `synchronous=NORMAL` on every connection. Single-writer/multi-reader connection pool. Write serialization via `WriteTx`.
- **Validated**: 10 concurrent goroutines × 10 writes (100 total), zero lock errors.

### Dragon 4: Context Window Exhaustion in ReAct Loops ✅ RESOLVED
- **Decision**: Three-layer mitigation: (1) sliding window evicts intermediate tool outputs, (2) token budget tracking flags at 80% capacity, (3) large outputs truncated to 4K tokens with full content persisted to SQLite.
- **Tripwire**: Agent integration tests verify loop termination under max iterations.

---

## What This Replaces vs. What It Doesn't (Yet)

**Full replacement at launch (what no single alternative covers today):**
- Gateway + session management ✅
- Telegram, Discord, Slack channels ✅
- LLM routing with multi-provider support + streaming ✅ (Anthropic, OpenAI-compat, Gemini)
- **OpenClaw SKILL.md format compatibility** ✅ (5,700+ skills, validated against 32,814)
- **Web UI for management** ✅ (React, real-time SSE streaming, 9 pages)
- **Multi-agent orchestration** ✅
- CLI for power users ✅
- Security model ✅ (formally mapped to 8 OpenClaw CVEs)
- Vector/semantic memory ✅ (pure Go, no CGo)
- Three-tier skill execution ✅ (Markdown + Native Go + WASM/wazero)
- Real-time tool call streaming ✅ (thinking/tool_start/tool_end/response events)
- OpenRouter provider ✅ (100+ models via single API key)
- WASM host functions ✅ (`http_get`, `memory_store`, `memory_recall` via `WASMHostConfig`)
- `capabot skill init [--wasm]` ✅ (WASM skill scaffold with Go source + Makefile)
- `capabot skill search <query>` ✅ (ClawHub registry search with tabular output)
- `capabot skill install <name>` ✅ (ClawHub name → download → import; URL → archive extract → import)
- ClawHub client ✅ (`ListSkills`, `SearchSkills`, `DownloadSkill`; path traversal protection; rate-limit hint)
- Per-tenant isolation ✅ (`X-Tenant-ID` header → context → storage routing)
- Makefile ✅ (`build`, `test`, `lint`, `web`, `dev`, `migrate` and more)

**Post-launch roadmap:**
- Additional channels (WhatsApp, Signal, iMessage, Teams, Matrix — target 15+ for OpenClaw parity)
- ClawHub-compatible skill registry server (host your own)
- OpenClaw skill bulk importer (batch migration of entire skill libraries)
- OpenClaw config migration tool (`capabot migrate-from-openclaw ~/.openclaw/`)
- Voice support (Telegram voice, Discord voice channels)
- Mobile apps (iOS/Android — thin clients connecting to gateway)
- Horizontal scaling: optional Postgres backend for multi-instance deployments
- OpenClaw session import (convert JSONL transcripts to Capabot's SQLite format)
- Cross-channel session continuity
