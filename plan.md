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

## Phase 1: Foundation (Weeks 1-3)

### 1.1 Project Scaffolding
- Go module init, directory structure, Makefile, CI pipeline
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

### 1.2 Configuration System
- YAML/TOML config file (`~/.capabot/config.yaml`)
- Environment variable overrides
- Per-tenant config isolation
- Config struct with validation at startup

### 1.3 Storage Layer
- `modernc.org/sqlite` (pure Go, no CGo)
- Schema: tenants, sessions, messages, memory, skills, config
- Migration system (embed SQL files via `//go:embed`)
- Per-tenant database isolation (separate SQLite files)
- Repository pattern for all data access
- **Vector memory**: Pure-Go brute-force cosine similarity over embeddings stored in SQLite. **NOT** `sqlite-vec` (it's C, breaks `CGO_ENABLED=0`). For chat memory (<10K embeddings per tenant), brute-force is sub-10ms and sufficient. If agents need to index large corpora (codebases, document sets), upgrade path is an in-process pure-Go HNSW index (e.g., `coder/hnsw` or hand-rolled) backed by SQLite for persistence — but this is post-launch optimization, not launch-blocking.
- **SQLite concurrency hardening**: Enforce `PRAGMA journal_mode=WAL` and `PRAGMA synchronous=NORMAL` on every connection. Implement a dedicated connection pool with single-writer/multi-reader topology tuned for `modernc.org/sqlite`. This is critical for Phase 6 multi-agent orchestration where parallel agents read/write memory simultaneously. Without WAL mode + connection pooling, `database is locked` errors will surface under concurrent ReAct loops.

---

## Phase 2: LLM Provider System (Weeks 2-4)

### 2.1 Provider Interface
```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
    Models() []ModelInfo
    Name() string
}
```

### 2.2 Built-in Providers
- **Anthropic** — Messages API (Claude models)
- **OpenAI-compatible** — Any provider exposing `/v1/chat/completions`
- **OpenRouter** — Single gateway to 100+ models

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

## Phase 4: Agent Core (Weeks 4-6)

### 4.1 Agent Loop
- ReAct-style loop: Observe → Think → Act → Observe
- Tool/skill dispatch with configurable max iterations
- **Context window management (multi-strategy)**:
  - **Sliding window for tool observations**: When an agent runs the same tool multiple times (e.g., 5× `web_search`), only the final synthesized answer stays in the active context. Raw intermediate results are evicted to SQLite long-term memory, retrievable via `memory_recall` if needed later.
  - **Token budget tracking**: Count tokens per message (using tiktoken-go or provider-reported usage). When context hits 80% of model's window, trigger automatic summarization of older conversation turns.
  - **Observation truncation**: Large tool outputs (e.g., full web pages from `web_fetch`) are truncated to a configurable max (default 4K tokens) with a pointer to the full content in SQLite.
- Conversation memory: short-term (in-session sliding window) + long-term (SQLite + vector recall)
- System prompt composition: base prompt + active skills + user context

### 4.2 Session Management
- Per-user, per-channel sessions
- Session persistence as structured data in SQLite (not JSONL — queryable)
- Configurable session TTL and cleanup
- Cross-channel session continuity (same user, different platforms)

### 4.3 Tool System
```go
type Tool interface {
    Name() string
    Description() string
    Parameters() JSONSchema
    Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}
```

Built-in tools:
- `web_search` — search via configurable backend (SearXNG, Brave, etc.)
- `web_fetch` — fetch and extract content from URLs
- `file_read` / `file_write` — sandboxed filesystem access
- `shell_exec` — sandboxed command execution (allowlist-only, no shell interpretation)
- `memory_store` / `memory_recall` — long-term memory operations
- `schedule` — cron-style task scheduling

---

## Phase 5: Skill Engine (Weeks 5-8)

**Design goal: run OpenClaw's 5,700+ skills without modification.** This is Capabot's primary competitive advantage — no other alternative has attempted skill-format compatibility.

### OpenClaw Skill Format (what we must support)
OpenClaw skills are directories containing a `SKILL.md` with YAML frontmatter (name, description, tools, triggers, requires) and markdown instructions. Skills can optionally include `index.ts`/`index.py`/`index.go` code modules, `config.json`, and `package.json`. The frontmatter declares required binaries, env vars, and install commands.

Capabot's skill loader must:
1. **Parse `SKILL.md` frontmatter with a forgiving parser** — OpenClaw's 5,700+ skills were written by thousands of authors. Expect malformed YAML, missing `---` delimiters, and non-standard Markdown. Use `yuin/goldmark` with custom AST walkers for instruction extraction, and `gopkg.in/yaml.v3` in lenient mode for frontmatter. Build a `capabot skill lint` command that reports parse errors without failing, so users can triage broken skills on import rather than at runtime.
2. Inject skill instructions into the agent's system prompt when active
3. Map OpenClaw tool names to Capabot tool names (e.g., `system.run` → `shell_exec`)
4. Evaluate `requires.bins` against the host PATH (fixing OpenClaw's container-mismatch bug)
5. Support skill precedence: workspace > user > bundled (same as OpenClaw)

### Three-Tier Execution Model

**Tier 1: Markdown-only skills (OpenClaw compatible)**
- Pure instruction injection — the LLM follows them using available tools
- This covers the majority of OpenClaw's skill catalog
- Zero code execution, zero security surface

**Tier 2: Native Go skills**
- Implement the `Tool` interface directly in Go
- Used for core/built-in tools (web search, file ops, shell exec, memory)
- Compiled into the binary

**Tier 3: WASM sandboxed skills (community/third-party code)**
- `wazero` runtime (pure Go, no CGo)
- For skills that need custom code execution beyond what tools provide
- Strict sandbox: no filesystem, no network unless explicitly granted via host functions
- Host function ABI for controlled access to: HTTP, filesystem, memory store
- Hot-loadable without recompiling the main binary

### Skill Registry & Import
- Local registry in `~/.capabot/skills/`
- `capabot skill import <openclaw-skill-dir>` — copies and validates OpenClaw skills
- `capabot skill install <url>` — install from ClawHub-compatible registry
- Security: hash verification, permission manifest, no implicit shell access
- Tool name mapping table for OpenClaw→Capabot translation

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
