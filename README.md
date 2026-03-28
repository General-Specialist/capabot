Go rewrite of OpenClaw — 100x lighter (~17MB vs 1GB+ RAM), more capable for 99% of use cases. Same skill ecosystem, same plugin protocol. Clean, easy-to-use web UI.

**Questions?** [Join the Discord](https://discord.gg/DY6pb9WvZ3) | **Architecture deep dive:** [context.md](context.md)

## Quick start

**Prerequisites:** [Go](https://go.dev/dl/), [Bun](https://bun.sh/), [PostgreSQL 17](https://www.postgresql.org/download/)

```bash
git clone https://github.com/General-Specialist/gostaff.git
cd gostaff

# create the database (after installing postgres for your OS)
createuser -s gostaff 2>/dev/null; createdb -O gostaff gostaff 2>/dev/null

cd web && bun install && bun run dev  # frontend on :5173
air                                   # backend on :8080
# then add an API key via Settings (recommended) or config.yaml
```

## Features

**Skills & Plugins** — 3-tier system. Tier 1: markdown instructions. Tier 2: native Go (compiles + hot-reloads from the web UI). Tier 3: OpenClaw-compatible TS/JS/Python plugins. Install from [ClawHub](https://clawhub.ai) (30K+ skills), GitHub, or create your own.

**Built-in tools** — file ops, shell exec (allowlisted), browser automation (Playwright), web search/fetch, persistent key-value memory, notebook execution, and a meta-tool (`use_tool`) for extended tools.

**Automations** — Schedule agent prompts with RRULE from the web UI. Live-stream traces, view run history, stop runs mid-flight.

**Multi-provider LLM** — Anthropic, OpenAI, Gemini, OpenRouter. Switch per-message with `@model-name`. Auto-fallback on rate limits.

**People** — Bot personas with custom names, prompts, and avatars. Route Discord/Slack channels to specific people or tags.

**Transports** — Discord, Telegram, Slack, HTTP. All route to the same ReAct agent core.

## Configuration

`config.yaml` in the project root, with env var overrides (`GOSTAFF_DATABASE_URL`, `GOSTAFF_ANTHROPIC_API_KEY`, etc). See `config.example.yaml` for all options.

## License

MIT
