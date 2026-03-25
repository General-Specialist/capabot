# capabot

Self-hosted AI agent management platform. Connect your LLM providers, install skills, and run agents from a web UI, CLI, or chat platforms like Discord and Telegram.

**Questions?** [Join the Discord](https://discord.gg/ktAy8fZH)

## Quick start

**Prerequisites:** [Docker](https://www.docker.com/get-started/)

```bash
git clone https://github.com/General-Specialist/capabot.git
cd capabot
cp config.example.yaml ~/.capabot/config.yaml
# add at least one API key in config.yaml
docker compose up --build
```

Backend: http://localhost:9090 | Frontend: http://localhost:5173

## Development

**Prerequisites:** [Go](https://go.dev/dl/), [Bun](https://bun.sh/), [Air](https://github.com/air-verse/air)

```bash
docker compose up postgres -d
air                                   # backend hot-reload on :9090
cd web && bun install && bun run dev  # frontend HMR on :5173
```

## Key features

**Skills & Plugins** — Extend the agent with skills from [ClawHub](https://clawhub.ai), the community skill registry with 30K+ skills. Any OpenClaw `SKILL.md` works out of the box. Skills are just Markdown — write instructions and the agent follows them. For real computation, write a plugin in TypeScript, JavaScript, or Python using the OpenClaw `register(api)` protocol — or write native Go.

```bash
capabot skill search "code review"      # search ClawHub
capabot skill install code-reviewer     # install from ClawHub
capabot skill create my-skill           # scaffold a new skill
capabot skill init --plugin my-plugin   # scaffold a TS plugin
```

Plugins can register tools, hooks (pre/post tool execution), HTTP routes, LLM providers, commands, and services. OpenClaw's `definePluginEntry` import works out of the box.

**Built-in tools** — file read/write/edit, shell exec (allowlisted), browser automation via Playwright, web search, web fetch, persistent memory, and more.

**Automations** — Schedule agent prompts on a recurring schedule from the web UI. Live-stream agent traces, view run history, stop runs in-flight.

**Multi-provider** — Anthropic, OpenAI, Gemini, OpenRouter. Switch models per-message with `@model-name`. Falls back to next provider on rate limits or errors.

**Personas** — Give the agent different names, prompts, and avatars. Route Discord/Slack channels to specific personas or tags. Mention with `@persona-name` to address one directly.

**Transports** — Discord, Telegram, Slack, or plain HTTP. All route to the same agent core.

## License

MIT
