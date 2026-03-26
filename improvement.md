# GoStaff Improvement Plan

Focus: reducing code, removing debt, simplifying. Keeping it maintainable by a tiny team.

## Architecture Improvements

### 7. Split `server.go` handler methods into logical files

**Problem:** `internal/api/server.go` is 964 lines with route registration + all handler implementations in one file. Finding a handler requires scrolling through 50+ methods.

Some handlers are already extracted (`automations.go`, `people.go`, `modes.go`, `config_keys.go`, `memory_handlers.go`, `skills_create.go`, `execute_fallback.go`, `usage.go`), but chat, conversations, settings, logs, health, and avatar handlers are still in `server.go`.

**Fix:** Extract remaining handlers:
- `handlers_chat.go` -- chat + chat/stream handlers
- `handlers_conversations.go` -- conversation list/get/delete
- `handlers_settings.go` -- shell mode, execute fallback, shell approved
- Keep route registration in `server.go`

**Lines saved from server.go:** ~400 (moved, not deleted -- but server.go drops to ~500 lines)

---

### 9. OpenClaw plugin streaming not supported

**Problem:** `openclaw.go` line ~195: `Stream()` returns `"does not support streaming"`. OpenClaw plugins that register LLM providers can't stream responses. This means any plugin-provided model falls back to request-response only, which is noticeably slower for chat UIs.

**Fix:** Add streaming support to the JSON-line IPC protocol. The host sends `stream` instead of `chat`, and the plugin sends back multiple `chunk` messages before a final `done`. This is the biggest gap vs OpenClaw for users who want plugin-provided models.

**Lines added:** ~100-150 in plugin.go + host.mjs

---

### 10. Plugin config schemas accepted but ignored

**Problem:** OpenClaw plugins can declare `configSchema` via `definePluginEntry()`. GoStaff accepts this in `host.mjs` but never surfaces it to the user or validates against it. Plugins that depend on user configuration (API keys, preferences) silently fail.

**Fix:** Store config schemas in the skill registry. Surface them in the SkillsPage UI so users can fill in required config. Pass the config to the plugin on `invoke`. This is important for OpenClaw conversion -- many popular skills need config.

---

## What OpenClaw Has That We Don't

These are features in OpenClaw that GoStaff does not implement. Ordered by impact on converting OpenClaw users:

### High Impact (would lose users without)

1. **Plugin streaming** -- covered in #9 above. Users expect real-time token streaming from custom models.

2. **Plugin configuration UI** -- covered in #10 above. Many ClawHub skills require API keys or settings that users configure through OpenClaw's UI.

3. **Multi-tenant channels** -- OpenClaw has `registerChannel()` for per-channel isolation (different skills, memory, system prompts per channel). GoStaff has `channel_bindings` for people routing but not full channel-scoped configuration. Important for Discord servers with multiple use cases.

### Medium Impact (nice to have for power users)

4. **Speech/voice providers** -- `registerSpeechProvider()` for voice input/output. Growing use case with Discord voice channels.

5. **Image generation providers** -- `registerImageGenerationProvider()`. Popular for creative/art Discord bots.

6. **Context engines** -- `registerContextEngine()` for custom RAG/retrieval. OpenClaw power users build custom memory systems. GoStaff has key-value memory which covers 90% of cases.

7. **Interactive handlers** -- `registerInteractiveHandler()` for multi-step wizard-style interactions (buttons, dropdowns in Discord). GoStaff only does text-based interaction.

### Low Impact (niche/advanced)

8. **Gateway methods** -- `registerGatewayMethod()` for custom API endpoints at the gateway level.

9. **Media understanding providers** -- `registerMediaUnderstandingProvider()`. GoStaff already handles images/PDFs natively via the LLM providers.

10. **CLI command registration** -- `registerCli()` separate from tools. GoStaff converts commands to tools (`cmd_` prefix) which works for most cases.

---

## Priority Order

### Immediate (code debt)
1. **#7** Split server.go handlers (~1 hour, pure cleanup)

### OpenClaw conversion (feature work)
2. **#9** Plugin streaming support
3. **#10** Plugin config schema UI
4. **OpenClaw gap** Multi-tenant channel configuration

### Backlog
5. Frontend test coverage (at minimum `lib/api.ts`)
