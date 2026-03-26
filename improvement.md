# GoStaff Improvement Plan

Focus: reducing code, removing debt, simplifying. Keeping it maintainable by a tiny team.

---

## 1. Token pricing table is hardcoded in agent.go

**Problem:** `agent.go` has a `tokenPricing` map with hardcoded model prices. This will need manual updates every time a model is added or pricing changes. It's also business logic living inside the agent loop.

**Fix:** Move the pricing table to its own file `internal/agent/pricing.go` or even to `internal/llm/pricing.go` (since it's provider knowledge). Not a big code reduction, but better cohesion. Consider loading from config or an external source long-term.

---

## 2. `install.sh` and `install.ps1` — verify they're maintained

**Problem:** These install scripts exist but may be out of date with the current release process (goreleaser).

**Fix:** If goreleaser handles distribution, consider whether these manual install scripts are still needed. If they're just fallbacks, verify they point to the correct release URLs.

---

## ~~3. Frontend: silent error swallowing~~ ✓ Done

Added `AlertProvider` (toast + confirm dialogs) wrapping the app. SkillsPage and AutomationsPage now surface load/action errors as toasts. Settings has a "Silence error notifications" toggle for users who prefer quiet failures.

---

## 3. Frontend: no test files

**Problem:** The entire `web/src/` directory has zero test files. The frontend is untested.

**Fix:** Not urgent for a tiny team, but at minimum add tests for `lib/api.ts` (the fetch wrapper) since it's the boundary between frontend and backend. Consider Vitest since it's already a Vite project.

---

## Priority Order

Next up:
1. **#1** Move token pricing out of agent.go
2. **#2** Verify install scripts are still needed
3. **#3** Frontend: add tests for `lib/api.ts`
