# Improvements

---

## `internal/cron/parser.go` — reimplemented RRule

**Issue**: Custom RRule parser that only supports `DAILY/WEEKLY/MONTHLY/YEARLY` + `INTERVAL` + `BYDAY`. No `BYHOUR`, `BYMINUTE`, no time-of-day support.

**Where**: `cron/parser.go`

**Fix**: Use an existing Go RRule library (e.g. `teambition/rrule-go`) which handles the full RFC 5545 spec including time-of-day, DTSTART, etc. The current implementation can't schedule automations at a specific time of day.

---

## `internal/transport/discord.go` — Discord Gateway implemented from scratch

**Issue**: The Discord transport is a full Gateway WebSocket client implementation (~400+ lines) including heartbeat, resume, reconnect, opcode handling. This is a lot of infrastructure to maintain.

**Where**: `transport/discord.go`

**Fix**: Consider `bwmarrin/discordgo` which handles all this. The current implementation is fine but any Discord API changes require manual updates here.

---

## `cmd/capabot/serve.go` — `makeMessageHandler` / `resolvePersonas` is large

**Issue**: `makeMessageHandler` and the persona routing logic in `serve.go` is long and handles many cases inline. Discord role sync at startup is also inline in `runServe`.

**Where**: `serve.go:465-640`

**Fix**: Extract persona routing logic into a `personaRouter` type. Extract Discord startup sync into a helper function. The function currently does startup sync AND defines message handler — two different responsibilities.

---

## `internal/api/server.go` — `handleChat` / `handleChatStream` likely duplicated

**Issue**: Both `handleChat` (sync) and `handleChatStream` (SSE) run an agent. The non-streaming path is just the streaming path without SSE headers. This often leads to divergence in logic.

**Where**: `api/server.go` (chat handlers)

**Suggestion**: One `runChatRequest` helper that both handlers call, with SSE opt-in as a parameter.

---

