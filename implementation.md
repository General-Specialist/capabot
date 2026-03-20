# Capabot — Implementation Plan

This document is the executable playbook. Each task has a concrete deliverable, dependency chain, estimated line count, and acceptance criteria. Tasks are ordered so every step produces a runnable, testable binary.

**Current state:** 2,032 lines across 11 files. Skill engine (parser, linter, importer, tool name mapping) is complete and validated against 32,814 real ClawHub skills. CLI supports `capabot skill lint` and `capabot skill import`.

---

## Guiding Principles

1. **Always shippable.** Every task ends with `go build && go test ./...` passing.
2. **Test before code.** RED → GREEN → REFACTOR. No exceptions.
3. **One concern per file.** 200-400 lines typical, 800 max.
4. **Zero CGo.** If a dependency needs C, find or write the pure-Go alternative.
5. **Immutable data.** Functions return new values, never mutate arguments.

---

## Sprint 1: Storage + Config (Foundation)

Everything depends on config loading and a working database. This is the critical path.

### T1.1 — Config System
**Package:** `internal/config`
**Files:** `config.go`, `config_test.go`, `defaults.go`
**Est:** ~300 lines

- Define `Config` struct: server addr, database path, provider keys, transport configs, skill dirs, log level
- Load from `~/.capabot/config.yaml` with `gopkg.in/yaml.v3`
- Environment variable overrides (`CAPABOT_DB_PATH`, `CAPABOT_LOG_LEVEL`, etc.)
- Validate at startup: required fields present, paths writable, ports available
- `config.Default()` returns sane defaults for zero-config startup

**Acceptance:**
- [ ] `TestLoadConfig_FromFile` — round-trips a YAML file
- [ ] `TestLoadConfig_EnvOverrides` — env var takes precedence over file
- [ ] `TestLoadConfig_Validation` — missing required field returns error
- [ ] `TestLoadConfig_Defaults` — zero-config produces usable config

**Depends on:** nothing
**Blocks:** everything

---

### T1.2 — SQLite Storage Layer
**Package:** `internal/memory`
**Files:** `store.go`, `store_test.go`, `migrations.go`, `pool.go`, `pool_test.go`
**Est:** ~600 lines

- `Store` struct wrapping `modernc.org/sqlite` with connection pool
- `pool.go`: single-writer / multi-reader pool (Dragon 3 mitigation)
  - Write queue: buffered channel, single goroutine drains and executes writes
  - Read pool: N connections for concurrent reads
  - All connections enforce `PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;`
- Schema migrations via `//go:embed` SQL files:
  - `001_init.sql`: tenants, sessions, messages, memory, skills, tool_executions
  - Migration runner: track applied versions in `schema_versions` table
- Repository methods: `SaveMessage`, `GetSession`, `StoreMemory`, `RecallMemory` (cosine similarity)
- Per-tenant isolation: each tenant gets a separate SQLite file under `~/.capabot/data/<tenant_id>.db`

**Acceptance:**
- [ ] `TestStore_Migration` — applies migrations idempotently
- [ ] `TestStore_CRUD` — basic insert/read/update/delete for sessions and messages
- [ ] `TestStore_ConcurrentWriters` — 10 goroutines writing simultaneously, zero lock errors
- [ ] `TestStore_PerTenantIsolation` — tenant A cannot read tenant B's data
- [ ] `TestStore_CosineSimilarity` — stores embeddings, recalls by similarity, sub-10ms for 5K vectors

**Depends on:** T1.1
**Blocks:** T3.1 (agent core needs storage), T4.1 (orchestrator needs session state)

---

### T1.3 — Structured Logging
**Package:** (configure in `cmd/capabot`)
**Files:** modify `main.go`, add `internal/log/log.go`
**Est:** ~80 lines

- `rs/zerolog` configured from `Config.LogLevel`
- Context-aware: every log line includes tenant ID, session ID, agent ID when available
- JSON output for production, pretty console for development

**Depends on:** T1.1
**Blocks:** nothing (nice-to-have early, everything uses it)

---

## Sprint 2: LLM Provider System

### T2.1 — Provider Interface + Anthropic Provider
**Package:** `internal/llm`
**Files:** `provider.go`, `anthropic.go`, `anthropic_test.go`, `types.go`
**Est:** ~500 lines

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
    Models() []ModelInfo
    Name() string
}
```

- `ChatRequest`: messages, model, tools (JSON schema), temperature, max_tokens
- `ChatResponse`: content, tool_calls, usage (input/output tokens), stop_reason
- `StreamChunk`: delta content, delta tool_call, done flag
- Anthropic provider: Messages API via raw `net/http` (no SDK)
  - Streaming via SSE parsing
  - Tool use: send tool definitions, parse `tool_use` blocks, return `tool_result`
  - Retry on 429 with exponential backoff
  - API key from config

**Acceptance:**
- [ ] `TestAnthropicProvider_Chat` — mock server returns expected response
- [ ] `TestAnthropicProvider_Stream` — mock server streams SSE chunks
- [ ] `TestAnthropicProvider_ToolUse` — round-trip tool call + tool result
- [ ] `TestAnthropicProvider_Retry429` — retries on rate limit, succeeds on second attempt
- [ ] Integration test (skipped in CI): real API call to Claude, verify response

**Depends on:** T1.1 (needs API key from config)
**Blocks:** T3.1 (agent core needs LLM)

---

### T2.2 — OpenAI-Compatible Provider
**Package:** `internal/llm`
**Files:** `openai.go`, `openai_test.go`
**Est:** ~300 lines

- Implements `Provider` against `/v1/chat/completions`
- Works with: OpenAI, OpenRouter, Ollama, Together, Groq, any OpenAI-compat endpoint
- Configurable base URL
- Tool calling via OpenAI function calling format

**Depends on:** T2.1 (shares types)
**Blocks:** nothing (Anthropic is enough to proceed)

---

### T2.3 — Provider Router
**Package:** `internal/llm`
**Files:** `router.go`, `router_test.go`
**Est:** ~200 lines

- Routes requests to providers based on: model name, task tier (fast/balanced/powerful), fallback chain
- Fallback: if primary returns 429/5xx, try next provider in chain
- Token budget tracking per tenant (configurable daily/monthly limits)

**Depends on:** T2.1, T2.2
**Blocks:** T3.1

---

## Sprint 3: Agent Core

### T3.1 — Agent Loop (ReAct Engine)
**Package:** `internal/agent`
**Files:** `agent.go`, `agent_test.go`, `loop.go`, `loop_test.go`, `context.go`
**Est:** ~700 lines

This is the brain. ReAct loop: Observe → Think → Act → Observe.

- `Agent` struct: config, provider, tool registry, memory store, active skills
- `Run(ctx, input Message) (Message, error)` — main entry point
- Loop mechanics:
  1. Compose system prompt (base + active skills' instructions + memory context)
  2. Send to LLM with tool definitions
  3. If response contains tool calls → execute tools → append results → loop
  4. If response is text → return to user
  5. Max iterations guard (configurable, default 25)
- **Context window management (Dragon 4 mitigation):**
  - Track token count per message via provider-reported usage
  - At 80% capacity: summarize older turns via LLM call, replace originals with summary
  - Tool output truncation: cap at 4K tokens, store full output in memory
  - Sliding window: repeated tool calls keep only last result in context

**Acceptance:**
- [ ] `TestAgent_SimpleChat` — text in, text out, no tools
- [ ] `TestAgent_ToolCall` — agent calls a tool, gets result, produces final answer
- [ ] `TestAgent_MultiTool` — agent chains 3 tool calls before answering
- [ ] `TestAgent_MaxIterations` — stops at limit, returns partial result
- [ ] `TestAgent_ContextSummarization` — triggers summarization when token budget exceeded
- [ ] `TestAgent_ToolOutputTruncation` — large tool output gets capped

**Depends on:** T1.2 (memory), T2.1 (LLM provider)
**Blocks:** T4.1 (orchestrator wraps agents), T5.x (transports feed agents)

---

### T3.2 — Built-in Tools
**Package:** `internal/agent/tools`
**Files:** `web_search.go`, `web_fetch.go`, `shell_exec.go`, `file_ops.go`, `memory_ops.go`, each with `_test.go`
**Est:** ~800 lines

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage  // JSON Schema
    Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}
```

- `web_search` — pluggable backend: SearXNG, Brave Search API, Tavily
- `web_fetch` — HTTP GET + readability extraction (strip nav/ads, return markdown)
- `shell_exec` — **security-critical**: `os/exec` with discrete argv, no shell interpretation, allowlist-only binary resolution (Dragon CVE mitigations)
- `file_read` / `file_write` — sandboxed to configurable root directory
- `memory_store` / `memory_recall` — delegate to storage layer

**Acceptance:**
- [ ] `TestShellExec_NoShellInterpolation` — `; rm -rf /` in arg does NOT get interpreted
- [ ] `TestShellExec_AllowlistEnforced` — blocked binary returns error
- [ ] `TestShellExec_WrapperChainResolution` — `env bash -c "cmd"` gets caught
- [ ] `TestFileOps_SandboxEscape` — `../../etc/passwd` blocked
- [ ] `TestWebFetch_ExtractsContent` — returns clean markdown from HTML
- [ ] `TestMemoryOps_StoreAndRecall` — round-trip with vector similarity

**Depends on:** T1.2 (memory tools need storage)
**Blocks:** T3.1 (agent needs tools to dispatch)

---

### T3.3 — Skill Injection into Agent
**Package:** `internal/agent`
**Files:** `skills.go`, `skills_test.go`
**Est:** ~200 lines

- `SkillRegistry` loads skills from `~/.capabot/skills/` using existing parser
- When an agent activates a skill, its instructions are appended to the system prompt
- Skill precedence: workspace > user > bundled
- Tool name rewriting: OpenClaw tool references in skill instructions get mapped to Capabot names at load time (using existing `toolmap.go`)

**Acceptance:**
- [ ] `TestSkillInjection_SystemPrompt` — skill instructions appear in composed prompt
- [ ] `TestSkillInjection_Precedence` — workspace skill overrides bundled skill of same name
- [ ] `TestSkillInjection_ToolNameRewrite` — `exec` in skill text becomes `shell_exec`

**Depends on:** T3.1, existing skill parser
**Blocks:** nothing

---

## Sprint 4: Transport Layer

### T4.1 — Transport Interface + HTTP API
**Package:** `internal/transport`, `internal/transport/http`
**Files:** `transport.go`, `http/adapter.go`, `http/adapter_test.go`, `http/middleware.go`
**Est:** ~500 lines

```go
type Transport interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg OutboundMessage) error
    OnMessage(handler func(ctx context.Context, msg InboundMessage))
    Name() string
}
```

- HTTP adapter using `labstack/echo`:
  - `POST /api/chat` — send message, get response
  - `GET /api/chat/stream` — SSE endpoint for streaming responses
  - `GET /api/sessions` — list sessions
  - API key auth middleware
  - Rate limiting middleware (token bucket per API key)
- This is the first transport because the web UI and CLI will use it

**Acceptance:**
- [ ] `TestHTTPAdapter_Chat` — POST message, get JSON response
- [ ] `TestHTTPAdapter_Stream` — SSE stream delivers chunks
- [ ] `TestHTTPAdapter_Auth` — missing/invalid API key returns 401
- [ ] `TestHTTPAdapter_RateLimit` — exceeding limit returns 429

**Depends on:** T3.1 (routes messages to agent)
**Blocks:** T6.1 (web UI talks to HTTP API)

---

### T4.2 — Telegram Adapter
**Package:** `internal/transport/telegram`
**Files:** `adapter.go`, `adapter_test.go`, `api.go`
**Est:** ~400 lines

- Long-polling mode (dev) + webhook mode (production)
- Raw `net/http` against Bot API (no SDK)
- Markdown formatting, inline keyboards, file uploads
- Maps Telegram user ID → Capabot tenant + session

**Depends on:** T4.1 (shares transport interface)
**Blocks:** nothing

---

### T4.3 — Discord Adapter
**Package:** `internal/transport/discord`
**Files:** `adapter.go`, `adapter_test.go`, `gateway.go`
**Est:** ~450 lines

- WebSocket gateway for real-time events
- Slash command registration on startup
- Thread-aware conversations
- Rich embeds for tool results

**Depends on:** T4.1
**Blocks:** nothing

---

### T4.4 — Slack Adapter
**Package:** `internal/transport/slack`
**Files:** `adapter.go`, `adapter_test.go`, `socketmode.go`
**Est:** ~400 lines

- Socket Mode (no public endpoint)
- Slash commands, thread replies
- App Home tab for status

**Depends on:** T4.1
**Blocks:** nothing

---

## Sprint 5: Router + Multi-Agent

### T5.1 — Router
**Package:** `internal/router`
**Files:** `router.go`, `router_test.go`, `auth.go`, `ratelimit.go`
**Est:** ~400 lines

- Routes inbound messages from any transport to the correct agent
- Auth: API key validation, tenant resolution
- Rate limiting: per-user, per-channel, configurable limits
- Tenant isolation: each request is scoped to a tenant context

**Depends on:** T4.1 (transports feed router)
**Blocks:** T5.2

---

### T5.2 — Orchestrator (Multi-Agent)
**Package:** `internal/orchestrator`
**Files:** `orchestrator.go`, `orchestrator_test.go`, `workflow.go`, `workflow_test.go`
**Est:** ~600 lines

- Agent registry: named agents with distinct configs (model, skills, tools)
- Parent→child delegation: agent can spawn sub-agents via `agent_spawn` tool
- Parallel execution: orchestrator runs child agents as goroutines
- Workflow engine: sequential, parallel, conditional chains
- Session context sharing: child agents inherit parent's conversation context (read-only)

**Acceptance:**
- [ ] `TestOrchestrator_SpawnChild` — parent delegates, child responds, parent synthesizes
- [ ] `TestOrchestrator_ParallelAgents` — 3 agents run concurrently, results collected
- [ ] `TestOrchestrator_Workflow_Sequential` — A → B → C chain executes in order
- [ ] `TestOrchestrator_Workflow_Parallel` — A + B run simultaneously, then C
- [ ] `TestOrchestrator_TenConcurrentAgents` — Dragon 3 stress test, zero lock errors

**Depends on:** T3.1, T1.2 (concurrent DB access)
**Blocks:** nothing

---

## Sprint 6: Web UI

### T6.1 — Web Server + Dashboard
**Package:** `internal/web`
**Files:** `server.go`, `routes.go`, `middleware.go` + `templates/*.templ`
**Est:** ~800 lines (Go) + ~600 lines (templ)

- `templ` templates compiled to Go
- `htmx` for dynamic updates (no JS framework)
- Tailwind CSS compiled at build time
- All static assets embedded via `//go:embed`
- Pages:
  - Dashboard (agent status, recent conversations, health)
  - Conversations (chat history, search, filtering)
  - Skills (browse, install, lint results)
  - Agents (configure model, skills, tools per agent)
  - Providers (manage API keys, routing rules)
  - Settings (tenant config, transport setup)

**Acceptance:**
- [ ] `TestWebServer_DashboardLoads` — GET / returns 200 with agent status
- [ ] `TestWebServer_ChatStreaming` — SSE endpoint streams tokens to browser
- [ ] `TestWebServer_SkillInstall` — POST skill import via web form
- [ ] `TestWebServer_AuthRequired` — unauthenticated requests redirect to login

**Depends on:** T4.1 (HTTP API)
**Blocks:** nothing

---

## Sprint 7: WASM Sandbox (Tier 3 Skills)

### T7.1 — WASM Runtime
**Package:** `pkg/sandbox`
**Files:** `runtime.go`, `runtime_test.go`, `hostfuncs.go`, `hostfuncs_test.go`, `abi.go`
**Est:** ~500 lines

- `wazero` runtime (pure Go, zero CGo)
- Host function ABI:
  - `http_fetch(url, method, headers, body) → response` — controlled HTTP access
  - `fs_read(path) → content` — sandboxed file read (allowlisted paths only)
  - `memory_store(key, value)` / `memory_recall(query)` — memory bridge
  - `log(level, message)` — structured logging
- Resource limits: memory cap, execution timeout, no raw socket/filesystem access
- Hot-loadable: skills can be added/updated without restarting the binary

**Acceptance:**
- [ ] `TestWASM_HelloWorld` — minimal .wasm module executes and returns result
- [ ] `TestWASM_HostFunc_HTTP` — module calls http_fetch, gets mocked response
- [ ] `TestWASM_SandboxEscape` — module attempts filesystem access, gets denied
- [ ] `TestWASM_Timeout` — infinite loop module gets killed after timeout
- [ ] `TestWASM_MemoryLimit` — module exceeding memory cap gets terminated

**Depends on:** T3.2 (host functions bridge to tools)
**Blocks:** nothing (Tier 1+2 skills work without WASM)

---

## Sprint 8: Hardening + Polish

### T8.1 — Security Test Suite
**Est:** ~400 lines of tests

- Fuzz `shell_exec` argument parsing with known injection patterns
- Test all CVE mitigations from plan.md Phase 8 table
- Verify WASM sandbox isolation
- Verify per-tenant data isolation
- Prompt injection detection (basic keyword + pattern matching)

### T8.2 — Graceful Shutdown
**Est:** ~100 lines

- `context.Context` propagation from `main` through all subsystems
- Signal handler: `SIGTERM`/`SIGINT` → cancel context → drain in-flight goroutines → close DB → exit
- Configurable drain timeout (default 30s)

### T8.3 — CLI Polish
**Est:** ~300 lines

- `capabot serve` — start gateway + all configured transports
- `capabot chat` — interactive CLI session (stdin/stdout, no transport needed)
- `capabot skill install <url>` — fetch from URL/registry
- `capabot skill create <name>` — scaffold new skill directory
- `capabot agent list` / `capabot agent create`
- `capabot config set <key> <value>`
- `capabot migrate` — run pending DB migrations

### T8.4 — OpenClaw Migration Tool
**Est:** ~200 lines

- `capabot migrate-from-openclaw ~/.openclaw/` — reads OpenClaw config, converts to Capabot format
- Maps OpenClaw skill directories to `~/.capabot/skills/`
- Converts OpenClaw provider config to Capabot provider config

---

## Dependency Graph

```
T1.1 Config ─────────────────────┬──────────────────────────────────┐
  │                               │                                  │
  v                               v                                  v
T1.2 Storage                   T1.3 Logging                      T2.1 Anthropic Provider
  │                                                                  │
  ├──────────────────────┐                                           v
  v                      v                                        T2.2 OpenAI Provider
T3.2 Built-in Tools    T3.3 Skill Injection                        │
  │                      │                                           v
  v                      v                                        T2.3 Provider Router
T3.1 Agent Core ←────────┘←──────────────────────────────────────────┘
  │
  ├────────────────────┬──────────────────┐
  v                    v                  v
T4.1 HTTP API       T4.2 Telegram     T4.3 Discord    T4.4 Slack
  │
  ├────────────────────┐
  v                    v
T5.1 Router         T6.1 Web UI
  │
  v
T5.2 Orchestrator
                    T7.1 WASM Sandbox (independent, needs T3.2)
                    T8.x Hardening (needs everything above)
```

---

## Estimated Totals

| Sprint | Tasks | New Lines (est.) | Test Lines (est.) |
|--------|-------|-------------------|-------------------|
| 1. Storage + Config | T1.1–T1.3 | ~980 | ~600 |
| 2. LLM Providers | T2.1–T2.3 | ~1,000 | ~500 |
| 3. Agent Core | T3.1–T3.3 | ~1,700 | ~900 |
| 4. Transports | T4.1–T4.4 | ~1,750 | ~800 |
| 5. Router + Multi-Agent | T5.1–T5.2 | ~1,000 | ~600 |
| 6. Web UI | T6.1 | ~1,400 | ~400 |
| 7. WASM Sandbox | T7.1 | ~500 | ~300 |
| 8. Hardening | T8.1–T8.4 | ~1,000 | ~400 |
| **Total** | **19 tasks** | **~9,330** | **~4,500** |

Existing code: 2,032 lines (skill engine). Grand total at completion: ~15,860 lines.

---

## Critical Path

The shortest path to a working end-to-end demo (user sends message → agent thinks → responds):

**T1.1 → T1.2 → T2.1 → T3.2 → T3.1 → T4.1**

Six tasks. After these six, you have: config loading, SQLite storage, Anthropic LLM calls, built-in tools, a ReAct agent loop, and an HTTP API to talk to it. Everything else layers on top.
