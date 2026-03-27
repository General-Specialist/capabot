Go rewrite of OpenClaw — 100x lighter (~17MB vs 1GB+ RAM), more capable for 99% of use cases. Same skill ecosystem, same plugin protocol. Clean, easy-to-use web UI.

**Questions?** [Join the Discord](https://discord.gg/ktAy8fZH)

## Quick start

**Prerequisites:** [Go](https://go.dev/dl/), [Bun](https://bun.sh/), [PostgreSQL 17](https://www.postgresql.org/download/)

```bash
git clone https://github.com/General-Specialist/gostaff.git
cd gostaff

# create the database (after installing postgres for your OS)
createuser -s gostaff 2>/dev/null; createdb -O gostaff gostaff 2>/dev/null

cp config.example.yaml ~/.gostaff/config.yaml
# add at least one API key in config.yaml
air                                   # backend on :9090
cd web && bun install && bun run dev  # frontend on :5173
```

Backend: http://localhost:9090 | Frontend: http://localhost:5173

## Key features

**Skills** — Markdown instructions that shape the agent's behavior. Create your own from the web UI or CLI, or install from [ClawHub](https://clawhub.ai) (30K+ community skills). Any OpenClaw `SKILL.md` works out of the box. The web UI separates custom skills from ClawHub-installed ones.

**Plugins** — Executable extensions for real computation. Write a native Go plugin (Tier 2) from the web UI — it compiles and hot-reloads instantly. Or install OpenClaw TS/JS/Python plugins (Tier 3) from ClawHub. The agent can also create, edit, delete, and search for skills/plugins on its own via built-in tools.

```bash
gostaff skill search "code review"      # search ClawHub
gostaff skill install code-reviewer     # install from ClawHub
gostaff skill install owner/repo        # install from GitHub
gostaff skill create my-skill           # scaffold a new skill
```

The Go SDK (`internal/sdk`) is the plugin system. Plugins implement `sdk.Plugin` and register tools, hooks, HTTP routes, and LLM providers in-process — no subprocess overhead. OpenClaw TS/JS/Python plugins from ClawHub are automatically wrapped via an adapter and work transparently. New skills are hot-reloaded into the running server.

**Built-in tools** — file read/write/edit, shell exec (allowlisted), browser automation via Playwright, web search, web fetch, persistent memory, and more.

**Automations** — Schedule agent prompts on a recurring schedule from the web UI. Live-stream agent traces, view run history, stop runs in-flight.

**Multi-provider** — Anthropic, OpenAI, Gemini, OpenRouter. Switch models per-message with `@model-name`. Falls back to next provider on rate limits or errors.

**People** — Give the agent different names, prompts, and avatars. Route Discord/Slack channels to specific people or tags. Mention with `@person-name` to address one directly.

**Transports** — Discord, Telegram, Slack, or plain HTTP. All route to the same agent core.

## License

MIT
