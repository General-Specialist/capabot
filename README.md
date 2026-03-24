# capabot

OpenClaw, but a single binary. 20x faster start, 9x lower idle memory, 67x less code — and every ClawHub skill works out of the box.

**Questions, feedback, or just want to hang?** [Join the Discord](https://discord.gg/ktAy8fZH)

## vs OpenClaw

| | OpenClaw | capabot | |
|---|---|---|---|
| Cold start | 2–5s | <100ms | **20x faster** |
| Idle memory | ~200MB | ~23MB | **~9x lower** |
| Codebase | ~1.2M lines JS | ~25K lines Go+TS | **48x less code** |
| Install | npm + runtime + deps | single binary | |
| Skills | 30K+ on ClawHub | 30K+ on ClawHub | |
| Providers | 25+ | 4 (Anthropic, OpenAI, Gemini, OpenRouter) | |
| Web UI | yes | yes | |
| Multi-agent | yes | yes | |
| WASM sandbox | no | yes | |

## Quick start

```bash
go install github.com/air-verse/air@latest   # Go hot-reload

cp config.example.yaml ~/.capabot/config.yaml
# add your API key in config.yaml
```

Then two terminals:

```bash
# terminal 1 — backend (http://localhost:9090)
cd capabot && air

# terminal 2 — frontend (http://localhost:5173)
cd capabot/web && bun install && bun run dev
```

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
capabot skill install code-reviewer   # Install from ClawHub
capabot skill search "git"            # Search ClawHub
capabot skill create my-skill         # Create a new skill
capabot skill lint ./my-skill/SKILL.md
```

Skills live in `~/.capabot/skills/<name>/SKILL.md`.

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
| `browser` | Playwright-based browser automation |
| `web_search` | DuckDuckGo / Brave / SearXNG search |
| `web_fetch` | Fetch and extract text from URLs |
| `image_read` | Read images (JPEG, PNG, GIF, WEBP) for vision models |
| `pdf_read` | Pass PDFs directly to the model (up to 32 MB) |
| `notebook` | Read and edit Jupyter `.ipynb` notebooks |
| `memory_store` | Persistent key-value memory |
| `memory_recall` | Retrieve stored memory |
| `todo` | Session-scoped task list with status tracking |
| `schedule` | Delay execution (for chaining time-sensitive actions) |

## Automations

Schedule agent prompts to run on a recurring schedule from the web UI (`/automations`). Supports manual trigger, live streaming of agent traces, run history, and stop-in-flight.

## CLI

```
capabot serve      Start the API server
capabot dev        Hot-reload mode for skill development
capabot chat       Interactive terminal chat
capabot skill      Manage skills (install, search, create, lint)
capabot agent      List configured agents
capabot migrate    Run database migrations
```

## Development

See Quick start above. `air` watches for Go changes and rebuilds automatically; Vite handles frontend HMR.

## Architecture

```
cmd/capabot/         CLI entrypoint
internal/
  agent/             ReAct loop — observe, think, act
  llm/               Provider abstraction (Anthropic, OpenAI, Gemini, OpenRouter)
  skill/             SKILL.md parser, registry, WASM runner
  memory/            SQLite storage — sessions, messages, vector recall
  tools/             Built-in tools (web, files, shell, browser, memory)
  transport/         Channel adapters (HTTP, Telegram, Discord, Slack)
  api/               REST API + SSE streaming
  cron/              Automation scheduler
  orchestrator/      Multi-agent coordination
web/                 React + Vite frontend
```

## Security

- Shell execution uses direct `os/exec` argv — no shell interpretation, no injection
- Binary paths resolved via `exec.LookPath` against an allowlist
- WASM skills run in a fully isolated sandbox (wazero, pure Go)
- Content filtering on all incoming messages
- Rate limiting, bearer token auth, per-tenant data isolation

## License

MIT
