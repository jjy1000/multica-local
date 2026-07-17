---
name: release-notes-0.3.1
created: 2026-07-03T13:30:00Z
updated: 2026-07-03T13:30:00Z
version: 0.3.1
type: release-notes
---

# Multica 0.3.1 — Stability Patch (4-P1)

Released: 2026-07-03
DMG: `apps/desktop/dist/multica-desktop-0.3.1-mac-arm64.dmg` (240 MB)
Replaces: 0.3.0

## What's in this release

Patched 4 high-severity stability bugs in the 0.3.0 standalone
implementation. No user-facing feature changes. No data-affecting
logic changed — only safety + reliability guarantees tightened.

## The 4 fixes

### P1.1 — `ensureServerUp` in-flight coalescing

Two concurrent IPC calls (e.g. renderer 2s status poll racing with
a manual retry) previously both ran the full server-start sequence,
orphaning the first child. Now coalesces into a single in-flight
Promise.

### P1.2 — stop vs in-flight race

`stopServerManager` and an in-flight `ensureServerUp` no longer race
on the `currentState` field. The new `stopping` flag prevents
in-flight ensures from writing `"running"` for a server the caller
is about to kill.

### P1.6 — probeMulticaPg false positive

The PG-identity probe now also checks for the `schema_migrations`
table (in `information_schema.tables`). A user's brew PG with the
same `multica` user/db/pgcrypto no longer satisfies the probe —
Multica won't accidentally point at a foreign database.

### P1.8 — migration sentinel atomicity

`runMigrationFlow` now creates an `.pg-migrating-v1` sentinel with
`O_EXCL` BEFORE the destructive `pg_restore`. On success it is
atomically renamed to `.pg-migrated-v1`. A SIGKILL between
restore-success and rename leaves the in-progress file; the next
launch refuses to retry automatically (the user can remove the
file manually after investigation). This prevents the
silent-data-loss scenario where a successful migration was
re-attempted and the catch block wiped already-populated pgdata.

## Verification

| Check | Result |
|---|---|
| typecheck | 0 errors |
| vitest | 30 files / **269 tests** (was 268) |
| go test | all green |
| Cold start | <5s to /health 200 |
| Data parity | 100% preserved (workspace=1 / issue=87 / comment=496 / agent=38) |
| Daemon | cli_version=0.3.1, 3 agents online |
| Hot quit | 5432 + 8090 freed cleanly |

## Risks

None new. The 0.3.0 P0 data-safety line is unchanged.

## Known issues carried over (not regressed)

- **P1 daemon-orphan on quit**: `multica daemon` wrapper process
  survives `Cmd+Q` (PPID=1). Manual cleanup:
  `pkill -f "multica daemon"`. Tracked in
  `multica-0.3.0-stability-2026-07-03.md`. Fix pending separate PR.
- **Electron 39 BrowserHungDetector 1.5s NSAlert risk**: mitigated
  by `build.modulePreload: false` from 0.2.89.5.1. Boot <5s gives
  safe margin.

## Deferred to 0.3.2

- 5 remaining P1 (MIGRATION_TABLES set, hdiutil detach leak,
  pg_ctl SIGTERM target, pgrep -f multi-install, daemon start race)
- All P2/P3 cosmetic items

Plan: `.omc/plans/0.3.1-stability-fixes.md`
Memory: `.claude/projects/-Users-jiangjianyan-jjy-multica-main/memory/multica-0.3.1-stability-fix-2026-07-03.md`