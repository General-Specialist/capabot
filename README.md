# capabot

OpenClaw, but a single binary. 20x faster start, 9x lower idle memory, 67x less code — and every ClawHub skill works out of the box.

```bash
git clone https://github.com/General-Specialist/capabot.git && cd capabot && make build
cp config.example.yaml ~/.capabot/config.yaml  # add your API key
./bin/capabot serve                             # http://localhost:9090
```

**Questions, feedback, or just want to hang?** I respond basically ASAP — [join the Discord](https://discord.gg/ktAy8fZH)

## vs OpenClaw

| | OpenClaw | capabot | |
|---|---|---|---|
| Cold start | 2–5s | <100ms | **20x faster** |
| Idle memory | ~200MB | ~23MB | **~9x lower** |
| Codebase | ~1.2M lines JS | ~18K lines Go | **67x less code** |
| Install | npm + runtime + deps | single binary | |
| Skills | 30K+ on ClawHub | 30K+ on ClawHub | |
| Providers | 25+ | 4 (Anthropic, OpenAI, Gemini, OpenRouter) | |
| Transports | WhatsApp, Telegram, Slack, Discord, Signal, iMessage | Telegram, Discord, Slack, HTTP | |
| Web UI | yes | yes (embedded, no separate deploy) | |
| Multi-agent | yes | yes | |
| WASM sandbox | no | yes | |

## Quick start

```bash
# Copy the example config
cp config.example.yaml ~/.capabot/config.yaml

# Add your API key
# providers.anthropic.api_key / providers.gemini.api_key / etc.

# Start the server
capabot serve

# Or chat directly in the terminal
capabot chat
```

The web UI is available at `http://localhost:9090`.

## Configuration

```yaml
server:
  addr: :9090

providers:
  anthropic:
    api_key: sk-ant-...
    model: claude-sonnet-4-6-20250514
  gemini:
    api_key: AIza...
    model: gemini-2.0-flash
  openai:
    api_key: sk-...
  openrouter:
    api_key: sk-or-...

security:
  api_key: ""                        # Bearer token for the HTTP API (optional)
  shell_allowlist: [ls, cat, grep]   # Commands the agent can run

skills:
  dirs:
    - ~/.capabot/skills
```

All values can be overridden with environment variables: `CAPABOT_ANTHROPIC_API_KEY`, etc.

## Skills

Skills are Markdown files that describe what an agent can do. Any OpenClaw `SKILL.md` works out of the box.

```bash
# Install a skill from ClawHub
capabot skill install code-reviewer

# Search ClawHub
capabot skill search "git"

# Create a new skill
capabot skill create my-skill

# Lint for compatibility
capabot skill lint ./my-skill/SKILL.md
```

Skills live in `~/.capabot/skills/<name>/SKILL.md` and are hot-reloaded in dev mode:

```bash
capabot dev   # watches for skill changes, auto-lints
```

For skills that need real computation, compile to WASM and drop `skill.wasm` alongside `SKILL.md`. The runtime is fully sandboxed (no filesystem, no network).

## Built-in tools

| Tool | Description |
|---|---|
| `file_read` | Read files with optional line ranges |
| `file_write` | Write/append files, auto-creates parent dirs |
| `file_edit` | Exact string replacement in files |
| `glob` | Recursive file pattern matching (`**/*.go`) |
| `grep` | Regex content search — files, content, or count mode |
| `shell_exec` | Run allowlisted shell commands |
| `web_search` | DuckDuckGo / Brave / SearXNG search |
| `web_fetch` | Fetch and extract text from URLs |
| `image_read` | Read images (JPEG, PNG, GIF, WEBP) for vision models |
| `pdf_read` | Pass PDFs directly to the model (up to 32 MB) |
| `notebook` | Read and edit Jupyter `.ipynb` notebooks |
| `memory_store` | Persistent key-value memory |
| `memory_recall` | Retrieve stored memory |
| `todo` | Session-scoped task list with status tracking |
| `schedule` | Delay execution (for chaining time-sensitive actions) |

Vision and document tools work natively with each provider — Anthropic receives `document`/`image` blocks, OpenAI receives `image_url`/`file` parts, Gemini receives `inlineData` blobs.

## Automations

Schedule agent prompts to run on a cron schedule from the web UI (`/automations`) or config. Supports manual trigger, run history, and enable/disable per automation.

## CLI

```
capabot serve      Start the API server and configured transports
capabot dev        Hot-reload mode for skill development
capabot chat       Interactive terminal chat
capabot skill      Manage skills (install, search, create, lint, import)
capabot agent      List configured agents
capabot migrate    Run database migrations
```

## Building

```bash
make build          # ./bin/capabot
make build-linux    # Linux amd64
make build-arm      # Linux arm64
make test
make test-cover
```

Requires Go 1.22+. `CGO_ENABLED=0` — compiles anywhere, runs anywhere.

## Architecture

```
cmd/capabot/         CLI entrypoint
internal/
  agent/             ReAct loop — observe, think, act
  llm/               Provider abstraction (Anthropic, OpenAI, Gemini, OpenRouter)
  skill/             SKILL.md parser, registry, WASM runner
  memory/            SQLite storage — sessions, messages, vector recall
  tools/             Built-in tools (web, files, shell, memory, schedule)
  transport/         Channel adapters (HTTP, Telegram, Discord, Slack)
  api/               REST API + SSE streaming
  orchestrator/      Multi-agent coordination
web/                 React SPA (embedded in binary)
```

The agent loop is a standard ReAct cycle: the LLM observes context, picks a tool, capabot runs it, feeds the result back, repeat. Max 25 iterations by default.

## Security

- Shell execution uses direct `os/exec` argv — no shell interpretation, no injection
- Binary paths resolved via `exec.LookPath` against an allowlist
- WASM skills run in a fully isolated sandbox (wazero, pure Go)
- Content filtering on all incoming messages (20+ prompt injection patterns)
- Rate limiting, bearer token auth, per-tenant data isolation

## Status

All core features are complete. The binary is self-contained and production-ready for single-node deployments.

Planned but not yet implemented: additional transports (WhatsApp, Matrix, Teams), horizontal scaling (Postgres backend), mobile apps.

## License

MIT
