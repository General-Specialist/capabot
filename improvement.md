# GoStaff Improvement Plan

Focus: reducing code, removing debt, simplifying. Keeping it maintainable by a tiny team.

---

## Code Debt Removal

### 4. `SkillRequires` parsed but never enforced

**Problem:** `types.go` defines `SkillRequires` (bins, anyBins, env, config) and the parser extracts it from SKILL.md frontmatter, but the runtime never checks if requirements are met before loading a skill. Users declare dependencies that are silently ignored.

**Fix:** Either add a simple check at skill load time (warn if required binaries/env vars are missing) or remove the struct and parsing code entirely. Recommendation: add a warning log, don't block loading.

**Lines saved:** ~0 if adding warnings, ~30 if removing

---

### 6. Error swallowing in API handlers

**Problem:** Several API handlers use `_, _ = expr` to ignore errors from store operations:
```go
_, _ = s.store.SaveMessage(ctx, memory.Message{...})
_ = s.store.UpsertSession(ctx, memory.Session{...})
```

**Fix:** Log errors at warn level. Don't fail the request, but make failures visible. Search for `_ =` in `internal/api/` and add `if err != nil { logger.Warn()... }` where appropriate.

**Lines saved:** ~0 (adds lines, but removes hidden bugs)

---

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

### 8. DatePicker is 546 lines for a single component

**Problem:** `web/src/components/DatePicker.tsx` is 546 LOC. It handles calendar rendering, date selection, time input, and picker state all in one component.

**Fix:** Calendar.tsx already exists separately. Verify DatePicker delegates properly. If it reimplements calendar logic, refactor to compose `Calendar` + a thin `DatePicker` wrapper.

**Lines saved:** ~200 if calendar logic is duplicated

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
1. **#6** Split server.go handlers (~1 hour, pure cleanup)
2. **#4** Decide on SkillRequires: enforce or remove
3. **#5** Fix error swallowing in API handlers

### Soon (architecture)
4. **#7** Audit DatePicker for duplicated calendar logic

### OpenClaw conversion (feature work)
7. **#8** Plugin streaming support
8. **#9** Plugin config schema UI
9. **#3 (OpenClaw gap)** Multi-tenant channel configuration

### Backlog
11. Frontend test coverage (at minimum `lib/api.ts`)
