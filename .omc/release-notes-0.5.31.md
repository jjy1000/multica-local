---
name: release-notes-0.5.31
created: 2026-08-17T08:45:00Z
updated: 2026-08-17T08:45:00Z
---

# 0.5.31 Release Notes — AuthTokenGC + Auth Cleanup + Deferred Ports (2026-08-17)

Closes 3 dormant `expires_at` ladders (same bug class as 0.5.25 RuntimeGC), truncates 2 abandoned auth tables, and lands the last 2 deferred audit items.

## Highlights

### AuthTokenGC — `feat(experimental)` `993aa72e9`
Unified background GC for the three auth-token tables with `expires_at` but no working retention:
- **task_token** (migration 108) — one row per agent spawn, highest volume
- **workspace_invitation** (migration 041) — 7d TTL
- **daemon_token** (migration 029) — long-lived, one per daemon install

The `DeleteExpiredTaskTokens` + `DeleteExpiredDaemonTokens` queries ALREADY existed in generated code with **zero callers** — the migration documented the ladder, nothing ever swept. `DeleteExpiredWorkspaceInvitations` added to invitation.sql. Lifecycle mirrors RuntimeGC/SwarmGC/SemanticaGC: Start at boot (router.go), Stop in main.go shutdown, `sweepCount atomic.Uint64` + `TestAuthTokenGC_RunSweepsBeforeExit` regression pin (fails on eager-close with "got 0 sweeps"). 6h default interval, 15s per-table sub-context timeout, nil-Queries guard for test builds. Zero migrations.

### Migration 249 — `chore(migrations)` `641e785ed` + `fix(migrations)` `7a564dd3b`
TRUNCATE `verification_code` + `personal_access_token` (both have no insert path in the fork — SendCode/VerifyCode/GoogleLogin return 410 Gone, cloud PAT removed). **Audit table-name fix**: migration 011 creates `personal_access_token` (SINGULAR) — the audit's plural broke the first apply (SQLSTATE 42P01); corrected + re-applied, verified both tables empty.

### pgProbeUrl loopback-only guard — `fix(server)` `b50166858`
Closes the deferred 3.3 second half: `process.env.DATABASE_URL` is now accepted ONLY for localhost/127.0.0.1/::1. Any other host falls back to the loopback default — the value is persisted verbatim to the profile `.env` at first launch, so a foreign DATABASE_URL would have been baked into the server config. Exported for tests; 4-case test suite pins all behaviors.

### Deferred port items
- **3.2 P2** `da0edc327` — `stopDaemon` execFile now passes `env: desktopSpawnEnv()` (MULTICA_LAUNCHED_BY symmetry with start path)
- **3.4 P3** `471edadb5` — `vendor/code-canvas/run.sh` PORT-assignment invariant documented

## Verification
- `pnpm typecheck --force` 6/6 (0 cached, 35s)
- `go build ./...` exit 0
- `go test ./internal/experimental/` ok (AuthTokenGC test green)
- Migration 249 applied: `verification_code=0`, `personal_access_token=0`
- Ship chain: snapshot → migrate (249 applied) → bundle → **4a/7 integrity check PASS (714 bytes, '<')** → electron-builder `--dir` → install + re-sign → **cold-start PASS, server 0.5.31**
- /Applications/Multica.app = 0.5.31
- Daemon: `--server-url http://localhost:8090 --profile desktop-localhost-8090`

## Remaining deferred (tracked, no urgency)
- MUL-5991 pair (jcode ACP effort) — user skip
- 0c69f1f95 (Hermes resume-auth) — user defer
- 7 MUL-6254 tests — need fork-idiom fixtures (username-only, no cloud)