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

2. **Full web UI (embedded in binary)** — Zero Go alternatives have a web dashboard. Capabot ships templ + htmx + Tailwind compiled into the same `CGO_ENABLED=0` binary. Conversations, skill management, agent config, provider routing — all in-browser with no separate frontend build.

3. **Multi-agent orchestration** — No lightweight alternative supports agent-to-agent delegation, parallel tool execution, or workflow chaining. Capabot's orchestrator enables parent→child agent spawning, which is required for complex OpenClaw workflows (e.g., research agent → writing agent → review agent).

4. **CVE-mapped security architecture** — Not just "we use Go." Every major OpenClaw CVE class (CVE-2026-32032 shell injection, CVE-2026-31992 command injection, CVE-2026-32000/31999/31995 allowlist bypasses, CVE-2026-22176 privilege escalation) maps to a specific architectural decision in Capabot that prevents it by construction.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────┐
│                    Web UI (Templ + HTMX)             │
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

**Logging** (`internal/log/`): zerolog-based structured JSON logger with context fields (tenant/session/agent ID), level filtering, `ParseLevel` helper. 12 unit tests.

### 1.1 Project Scaffolding ✅
- [x] Go module init, directory structure
- [ ] Makefile, CI pipeline
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
  router/               # auth, rate-limiting, routing
  skill/                # skill engine (loader, registry, executor)
  transport/            # channel adapter interface
  transport/telegram/   # Telegram adapter
  transport/discord/    # Discord adapter
  transport/slack/      # Slack adapter
  transport/http/       # HTTP API adapter
  web/                  # web UI (templ + htmx)
pkg/
  types/                # shared types, interfaces
  sandbox/              # WASM sandbox runtime
migrations/             # SQLite schema migrations
skills/                 # bundled default skills
web/
  templates/            # templ templates
  static/               # CSS, JS assets
```

### 1.2 Configuration System ✅
- [x] YAML config file (`~/.capabot/config.yaml`)
- [x] Environment variable overrides (`CAPABOT_` prefix)
- [x] Config struct with validation at startup (addr, log level, iterations, budget)
- [ ] Per-tenant config isolation

### 1.3 Storage Layer ✅
- [x] `modernc.org/sqlite` (pure Go, no CGo)
- [x] Schema: sessions, messages, memory (with embeddings)
- [x] Migration system (idempotent, tested for double-apply)
- [x] Per-tenant database isolation (separate SQLite files)
- [x] Repository pattern for all data access (`Store` with `CreateSession`, `GetSession`, `SaveMessage`, `GetMessages`, `StoreMemory`, `RecallMemory`, `DeleteMemory`)
- [x] **Vector memory**: Pure-Go brute-force cosine similarity over embeddings stored as raw little-endian bytes in SQLite. Tested at 5,000 vectors × 128 dims, recall <100ms.
- [x] **SQLite concurrency hardening**: WAL mode + `synchronous=NORMAL` enforced on every connection. Single-writer/multi-reader `Pool` with `WriteTx` serialization. Tested with 10 concurrent goroutines × 10 writes (100 total), zero lock errors.
- [ ] Schema: tenants, skills, config tables (not yet needed)
- **Upgrade path**: In-process pure-Go HNSW index backed by SQLite for persistence when any tenant exceeds 50K embeddings.

---

## Phase 2: LLM Provider System (Weeks 2-4) — 🔧 IN PROGRESS

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

### 2.2 Built-in Providers
- [x] **Gemini** — `google.golang.org/genai` SDK, `gemini-3-flash-preview` default. Full Chat + Stream + tool use. Handles Gemini 3's thinking/reasoning parts (skips `Thought` tokens). 11 unit tests (mock HTTP server) + 2 integration tests (real API). `internal/llm/gemini.go`
- [ ] **Anthropic** — Messages API (Claude models)
- [ ] **OpenAI-compatible** — Any provider exposing `/v1/chat/completions`
- [ ] **OpenRouter** — Single gateway to 100+ models

### 2.3 Routing & Fallback
- Model selection by task complexity (configurable tiers: fast/balanced/powerful)
- Fallback chain: if primary provider returns 429/5xx, try next
- Provider key rotation for rate-limit distribution
- Streaming via SSE/WebSocket to all transports
- Token budget tracking per tenant

---

## Phase 3: Transport Layer (Weeks 3-5)

### 3.1 Transport Interface
```go
type Transport interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg OutboundMessage) error
    OnMessage(handler func(ctx context.Context, msg InboundMessage))
    Name() string
}
```

All transports normalize messages into a unified `InboundMessage` / `OutboundMessage` format so the agent core never sees platform-specific types.

### 3.2 Telegram Adapter
- Webhook mode (production) + long-polling mode (development)
- Rich message support (markdown, inline keyboards, file uploads)
- Bot API via raw `net/http` (no SDK dependency)

### 3.3 Discord Adapter
- WebSocket gateway connection for real-time events
- Slash command registration
- Rich embeds, reactions, thread support
- Voice channel awareness (future)

### 3.4 Slack Adapter
- Socket Mode (no public endpoint needed) + Events API
- Slash commands, app home, modal interactions
- Thread-aware conversations

### 3.5 HTTP API Adapter
- RESTful API for programmatic access
- WebSocket endpoint for streaming responses
- API key auth, used by web UI and CLI

---

## Phase 4: Agent Core (Weeks 4-6) — 🔧 IN PROGRESS

### 4.1 Agent Loop ✅
- [x] ReAct-style loop: Observe → Think → Act → Observe (`internal/agent/agent.go`)
- [x] Tool/skill dispatch with configurable max iterations (default 25)
- [x] **Context window management (multi-strategy)** (`internal/agent/context.go`):
  - [x] **Sliding window**: `BuildMessages()` keeps first message + most recent N-1 messages
  - [x] **Token budget tracking**: `ContextManager` tracks cumulative usage from LLM, flags when input tokens exceed budget threshold (80% of context window)
  - [x] **Observation truncation**: Large tool outputs truncated to configurable max (default 4K tokens × 4 chars) with pointer to full content
- [x] System prompt composition passed to LLM on every iteration
- [x] Context cancellation support (graceful abort)
- [x] Audit logging: messages and tool executions persisted via `StoreWriter` interface
- **28 unit tests** — mock provider exercises: simple response, tool call→response, tool-not-found error recovery, max iterations safety, context cancellation, multiple parallel tool calls, persistence, output truncation, system prompt passthrough, tool definitions passthrough
- [ ] Long-term vector memory recall during loop (depends on embedding integration)
- [ ] Automatic summarization when context budget exceeded (future)

### 4.2 Session Management
- [x] Session persistence schema in SQLite (sessions, messages, tool_executions tables)
- [x] `memory.Store` API: `CreateSession`, `GetSession`, `SaveMessage`, `GetMessages`
- [x] `memory.Store.SaveToolExecution` for audit trail
- [ ] Per-user, per-channel session routing (depends on transport layer, Phase 3)
- [ ] Configurable session TTL and cleanup
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
- [x] `Tool` interface (`internal/agent/tool.go`)
- [x] `Registry` — thread-safe tool registration, lookup, listing. Sorted `Names()` for deterministic output
- [x] `ToolResult` type with content + error flag
- [x] 9 unit tests for registry operations
- [ ] Built-in tool implementations (Phase 4b):
  - `web_search` — search via configurable backend (SearXNG, Brave, etc.)
  - `web_fetch` — fetch and extract content from URLs
  - `file_read` / `file_write` — sandboxed filesystem access
  - `shell_exec` — sandboxed command execution (allowlist-only, no shell interpretation)
  - `memory_store` / `memory_recall` — long-term memory operations
  - `schedule` — cron-style task scheduling

---

## Phase 5: Skill Engine (Weeks 5-8) — ✅ CORE COMPLETE

**Design goal: run OpenClaw's 5,700+ skills without modification.** This is Capabot's primary competitive advantage — no other alternative has attempted skill-format compatibility.

**Status**: Parser, linter, importer, and tool name mapping are implemented and validated against 32,814 real ClawHub skills. Located in `internal/skill/`.

### OpenClaw Skill Format (what we must support)
OpenClaw skills are directories containing a `SKILL.md` with YAML frontmatter (name, description, tools, triggers, requires) and markdown instructions. Skills can optionally include `index.ts`/`index.py`/`index.go` code modules, `config.json`, and `package.json`. The frontmatter declares required binaries, env vars, and install commands.

Capabot's skill loader must:
1. [x] **Parse `SKILL.md` frontmatter with a forgiving parser** — `internal/skill/parser.go` using `goldmark` + custom AST walkers for instruction extraction, `gopkg.in/yaml.v3` in lenient mode for frontmatter.
2. [ ] Inject skill instructions into the agent's system prompt when active (depends on agent core, Phase 4)
3. [x] Map OpenClaw tool names to Capabot tool names (e.g., `system.run` → `shell_exec`) — `internal/skill/toolmap.go`
4. [ ] Evaluate `requires.bins` against the host PATH
5. [ ] Support skill precedence: workspace > user > bundled (same as OpenClaw)

### Implemented Components
- **`parser.go`** — Forgiving SKILL.md parser (frontmatter + markdown body extraction)
- **`parser_test.go`** — Unit tests for well-formed, malformed, and edge-case skills
- **`lint.go`** — `capabot skill lint` reports parse errors without failing
- **`lint_test.go`** — Lint validation tests
- **`importer.go`** — Copies and validates OpenClaw skill directories
- **`importer_test.go`** — Import workflow tests
- **`toolmap.go`** — OpenClaw→Capabot tool name translation table
- **`toolmap_test.go`** — Tool mapping tests
- **`types.go`** — Skill types (frontmatter struct, etc.)
- **`clawhub_integration_test.go`** — Validated against 32,814 real ClawHub skills

### Three-Tier Execution Model

**Tier 1: Markdown-only skills (OpenClaw compatible)** ✅
- Pure instruction injection — the LLM follows them using available tools
- This covers the majority of OpenClaw's skill catalog
- Zero code execution, zero security surface

**Tier 2: Native Go skills**
- [ ] Implement the `Tool` interface directly in Go
- Used for core/built-in tools (web search, file ops, shell exec, memory)
- Compiled into the binary

**Tier 3: WASM sandboxed skills (community/third-party code)**
- [ ] `wazero` runtime (pure Go, no CGo)
- For skills that need custom code execution beyond what tools provide
- Strict sandbox: no filesystem, no network unless explicitly granted via host functions
- Host function ABI for controlled access to: HTTP, filesystem, memory store
- Hot-loadable without recompiling the main binary

### Skill Registry & Import
- [x] `capabot skill import <openclaw-skill-dir>` — copies and validates OpenClaw skills
- [ ] Local registry in `~/.capabot/skills/`
- [ ] `capabot skill install <url>` — install from ClawHub-compatible registry
- [ ] Security: hash verification, permission manifest, no implicit shell access
- [x] Tool name mapping table for OpenClaw→Capabot translation

---

## Phase 6: Multi-Agent Orchestration (Weeks 7-9)

### 6.1 Orchestrator
- Agent-to-agent delegation (parent spawns child agents for subtasks)
- Parallel tool execution across agents
- Workflow engine: sequential, parallel, and conditional skill chains
- Configurable orchestration strategies (round-robin, priority, capability-based)

### 6.2 Agent Registry
```go
type AgentConfig struct {
    ID          string
    Name        string
    SystemPrompt string
    Provider    string      // which LLM provider/model
    Skills      []string    // enabled skills
    Tools       []string    // enabled tools
    MaxTokens   int
    Temperature float64
}
```

---

## Phase 7: Web UI (Weeks 8-11)

### 7.1 Tech Stack
- **templ** — type-safe Go HTML templating (compiles to Go, no runtime overhead)
- **htmx** — dynamic UI without a JS framework (SSE for streaming, WebSocket for real-time)
- **Tailwind CSS** — utility-first styling (compiled at build time, embedded in binary)
- All assets embedded via `//go:embed` — the web UI ships inside the single binary

### 7.2 Pages
- **Dashboard** — active agents, recent conversations, system health
- **Conversations** — full chat history, search, per-channel filtering
- **Skills** — browse installed skills, install from registry, create new
- **Agents** — configure agents (system prompt, model, skills, tools)
- **Providers** — manage LLM provider keys and routing rules
- **Settings** — tenant config, channel setup, security policies
- **Logs** — real-time log streaming, filterable by agent/channel/level

### 7.3 Real-Time Features
- Streaming LLM responses via SSE
- Live conversation updates via WebSocket
- Agent status indicators (thinking, executing tool, idle)

---

## Phase 8: Security Hardening (Ongoing)

This is the primary differentiator vs OpenClaw. Every CVE they've had maps to an architectural decision we avoid:

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

Additional measures:
- Per-tenant isolation (separate SQLite DBs, separate agent contexts)
- Rate limiting at router level (per-user, per-channel, per-provider)
- Audit logging for all tool executions
- Content filtering pipeline (prompt injection detection)
- Graceful shutdown with `context.Context` propagation and signal handling

---

## Phase 9: CLI & Developer Experience (Weeks 10-12)

### 9.1 CLI
```
capabot serve              # start gateway + all configured channels
capabot chat               # interactive CLI chat session
capabot skill install <url> # install a skill from URL or registry
capabot skill create <name> # scaffold a new skill
capabot agent list          # list configured agents
capabot config set <key> <value>
capabot migrate             # run database migrations
```

### 9.2 Developer Tooling
- `capabot dev` — hot-reload mode for skill development
- Skill SDK: Go module with helper types for building native skills
- WASM skill template: `capabot skill init --wasm`
- OpenClaw skill importer: `capabot import-skill <openclaw-skill-dir>`

---

## Verification Plan

### Unit Tests (per-package)
- LLM provider mock + real API integration tests
- Transport adapter tests with mock servers
- Skill engine: markdown parsing, Go tool dispatch, WASM execution
- Memory store: CRUD, per-tenant isolation, migration correctness

### Integration Tests
- Full agent loop: message in → LLM call → tool execution → response out
- Multi-channel: same conversation across Telegram + Discord
- Multi-agent: orchestrator delegates to child agents correctly
- Skill loading: markdown, native Go, and WASM skills all execute

### E2E Tests
- Send a message via Telegram bot → verify response
- Use web UI to configure agent → send chat → verify streaming response
- Install a skill from registry → use it in conversation
- Verify rate limiting blocks excessive requests
- Verify WASM sandbox prevents unauthorized file/network access

### Security Tests
- Fuzz tool argument parsing for injection vectors
- Verify allowlist enforcement with wrapper chain attempts
- Verify WASM skills cannot escape sandbox
- Verify per-tenant data isolation

---

## Key Dependencies

| Package | Purpose |
|---|---|
| `modernc.org/sqlite` | Embedded SQLite (pure Go) |
| `github.com/tetratelabs/wazero` | WASM runtime (pure Go) |
| `github.com/a-h/templ` | Type-safe HTML templates |
| `github.com/labstack/echo/v4` | HTTP framework (lightweight, fast) |
| `nhooyr.io/websocket` | WebSocket (standard-library compatible) |
| `gopkg.in/yaml.v3` | Config parsing |
| `github.com/rs/zerolog` | Structured logging |
| `yuin/goldmark` | Markdown parsing (forgiving SKILL.md extraction) |

**Zero CGo.** The entire binary compiles with `CGO_ENABLED=0` and cross-compiles to Linux/macOS/Windows/ARM.

---

## Architectural Dragons (Known Risks & Mitigations)

These are the non-obvious landmines that will bite during implementation if not addressed upfront.

### Dragon 1: Vector Search vs. `CGO_ENABLED=0`
- **Problem**: `sqlite-vec` is C. Using it requires CGo or shipping a dynamic `.so`/`.dylib`, breaking both the static binary and single-file deployment promises.
- **Decision**: Pure-Go brute-force cosine similarity at launch. Sub-10ms for <10K embeddings (typical chat memory). Upgrade path to pure-Go HNSW if agents need to index large corpora.
- **Tripwire**: If any single tenant's embedding count exceeds 50K, log a warning and recommend HNSW migration.

### Dragon 2: SKILL.md Parsing Chaos
- **Problem**: 5,700+ skills written by thousands of authors. Malformed YAML, missing `---` delimiters, Markdown that breaks standard AST parsers. If the parser is strict, half the ecosystem won't import.
- **Decision**: Forgiving parser using `goldmark` + custom AST walkers. `capabot skill lint` command for pre-import validation. Import never fails silently — it succeeds with warnings or fails with actionable errors.
- **Tripwire**: Maintain a compatibility test suite against the top 100 most-installed OpenClaw skills from ClawHub.

### Dragon 3: SQLite Concurrency Under Multi-Agent Load
- **Problem**: `modernc.org/sqlite` handles concurrency differently than `mattn/go-sqlite3`. Multi-agent orchestration means N agents reading/writing memory in parallel. Without proper connection management, `database is locked` errors will surface.
- **Decision**: WAL mode + `synchronous=NORMAL` on every connection. Single-writer/multi-reader connection pool. Write serialization via a dedicated goroutine with a channel-based write queue.
- **Tripwire**: Integration test that runs 10 concurrent agents doing interleaved reads/writes. Must complete without lock errors.

### Dragon 4: Context Window Exhaustion in ReAct Loops
- **Problem**: Every tool observation appends to the prompt. An agent that runs 5 web searches, 3 file reads, and 2 shell commands can easily burn through 128K tokens before producing a useful answer.
- **Decision**: Three-layer mitigation: (1) sliding window evicts intermediate tool outputs, keeping only final synthesis, (2) token budget tracking triggers summarization at 80% capacity, (3) large outputs truncated to 4K tokens with full content persisted to SQLite.
- **Tripwire**: Test an agent that deliberately runs 20+ tool calls in sequence. Verify it completes without context overflow and that evicted observations are retrievable via `memory_recall`.

---

## What This Replaces vs. What It Doesn't (Yet)

**Full replacement at launch (what no single alternative covers today):**
- Gateway + session management (OpenClaw parity)
- Telegram, Discord, Slack channels (PicoClaw has some, but no web UI or Slack)
- LLM routing with multi-provider support + streaming (ZeroClaw has providers, but CLI-only)
- **OpenClaw SKILL.md format compatibility** (nobody else has this)
- **Web UI for management** (nobody else has this in Go)
- **Multi-agent orchestration** (nobody else has this in lightweight alternatives)
- CLI for power users
- Security model (strictly superior to OpenClaw, formally mapped to CVEs)
- Vector/semantic memory (matches ZeroClaw, but in pure Go)

**Post-launch roadmap:**
- Additional channels (WhatsApp, Signal, iMessage, Teams, Matrix — target 15+ for OpenClaw parity)
- ClawHub-compatible skill registry server (host your own)
- OpenClaw skill bulk importer (batch migration of entire skill libraries)
- OpenClaw config migration tool (`capabot migrate-from-openclaw ~/.openclaw/`)
- Voice support (Telegram voice, Discord voice channels)
- Mobile apps (iOS/Android — thin clients connecting to gateway)
- Horizontal scaling: optional Postgres backend for multi-instance deployments
- OpenClaw session import (convert JSONL transcripts to Capabot's SQLite format)
