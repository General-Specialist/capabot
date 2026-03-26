# GoStaff Improvement Plan

Focus: reducing code, removing debt, simplifying. Keeping it maintainable by a tiny team.

---

## 1. `serve.go` is too big (~1160 lines)

**Problem:** `serve.go` is the wiring layer for the entire application. It defines:
- All `init*` functions (store, router, tools, skills)
- `makeMessageHandler` + all message routing logic (~200 lines)
- `resolvePeople`, `extractModelTag`, `isApprovalResponse` (~100 lines)
- `handleDefaultRoleCmd`, `handleModeCmd` (~120 lines)
- `syncDiscordPeopleRoles`, `avatarToDataURI` (~60 lines)
- `registerNativeSkills`, `registerSDKPlugins` (~100 lines)
- Agent runner closure (`runAgent`)

**Fix:** Extract into focused files:
- `cmd/gostaff/init.go` — `initStore`, `initRouter`, `initToolRegistry`, `initSkillRegistry`, `registerNativeSkills`, `registerSDKPlugins` (pure setup, no business logic)
- `cmd/gostaff/transport_handler.go` — `makeMessageHandler`, `resolvePeople`, `extractModelTag`, `isApprovalResponse`, `handleDefaultRoleCmd`, `handleModeCmd`, `avatarToDataURI`, `syncDiscordPeopleRoles`
- `serve.go` stays as the orchestrator that calls everything in order

This brings serve.go down to ~200 lines (just the boot sequence) and makes each concern independently testable.

---

## 2. Token pricing table is hardcoded in agent.go

**Problem:** `agent.go` has a `tokenPricing` map with hardcoded model prices. This will need manual updates every time a model is added or pricing changes. It's also business logic living inside the agent loop.

**Fix:** Move the pricing table to its own file `internal/agent/pricing.go` or even to `internal/llm/pricing.go` (since it's provider knowledge). Not a big code reduction, but better cohesion. Consider loading from config or an external source long-term.

---

## 3. OpenClaw compatibility layer adds complexity

**Problem:** The skill system carries a lot of OpenClaw compatibility:
- `types.go` has `SkillMetadata` with three alias keys (`openclaw`, `clawdbot`, `clawdis`) and `Resolved()` method
- `toolmap.go` maps OpenClaw tool names to GoStaff equivalents
- `importer.go` handles OpenClaw-specific import logic
- `sdk/openclaw.go` wraps OpenClaw plugins via a shim layer
- `internal/skill/shim/` has Node.js shim files for OpenClaw plugin protocol

If the goal is a clean, independent product, consider whether this compatibility layer is earning its keep. If few or no users are importing OpenClaw skills, this is dead weight.

**Fix:** Audit whether ClawHub/OpenClaw skills are actually being used. If not, remove:
- `SkillMetadata`/`SkillMetadataInner`/`Resolved()` from types.go
- `toolmap.go` entirely
- OpenClaw import logic from `importer.go`
- `sdk/openclaw.go`
- `internal/skill/shim/` directory

---

## 4. Discord-specific logic leaks into serve.go

**Problem:** `serve.go` has Discord-specific concerns mixed into the general wiring:
- `syncDiscordPeopleRoles` — creates Discord roles for all people/tags at startup
- `avatarToDataURI` — reads avatar files and converts to Discord webhook format
- `handleDefaultRoleCmd` — handles Discord role mention format `<@&ID>`

**Fix:** Move these into `internal/transport/discord_*.go` or a new `internal/transport/discord_setup.go`. The serve.go startup should just call `discord.SyncRoles(store)` if Discord is configured.

---

## 5. `install.sh` and `install.ps1` — verify they're maintained

**Problem:** These install scripts exist but may be out of date with the current release process (goreleaser).

**Fix:** If goreleaser handles distribution, consider whether these manual install scripts are still needed. If they're just fallbacks, verify they point to the correct release URLs.

---

## 7. Frontend: silent error swallowing

**Problem:** Several pages (AutomationsPage, SkillsPage, MemoryPage) catch fetch errors and silently drop them. The user sees no feedback when an API call fails.

**Fix:** Add a simple toast/notification pattern. Even `console.error` + a state-driven error banner would be better than silent failure.

---

## 8. Frontend: no test files

**Problem:** The entire `web/src/` directory has zero test files. The frontend is untested.

**Fix:** Not urgent for a tiny team, but at minimum add tests for `lib/api.ts` (the fetch wrapper) since it's the boundary between frontend and backend. Consider Vitest since it's already a Vite project.

---

## Priority Order

Medium effort, high impact:
1. **#1** Split `serve.go` into focused files (30 min)
2. **#4** Move Discord logic out of serve.go (20 min)

Larger decisions (need user input):
3. **#3** Audit and potentially remove OpenClaw compatibility layer
4. **#2** Move token pricing out of agent.go
