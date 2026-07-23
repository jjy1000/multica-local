# release-notes-0.3.62

Multica 0.3.62 ships the 0.3.60 user-plugin runtime closure and the
0.3.61 squad-subscriber schema fix that were sitting uncommitted on the
working tree, plus a single-line router hardening that lets the
/metrics unit test pass under `NewRouter(nil, ...)` without panicking.

## Changes

### 1. Version bump — `apps/desktop/package.json`

`version`: `0.3.61` → `0.3.62`. Bumped from the canonical desktop
package file. `git describe` keeps returning the pre-update-snapshot
marker on this sparse-git fork, so the package version is the only
source of truth.

### 2. Router nil-check fix — `server/cmd/server/router.go`

`TestMainRouterDoesNotExposePrometheusMetrics` was panicking on
`pgxpool.(*Pool).Acquire(0x0, ...)` because the 0.3.60 boot-time user
plugin loader calls `h.Queries.ListActiveUserPlugins(ctx)` unconditionally
once `h.ExperimentRegistry != nil`. `db.Queries` is always non-nil
because `db.New(pool)` wraps the pool into a struct — so the existing
guard wasn't enough; the inner `db DBTX` field stays nil when a test
passes `pgxpool = nil`.

Fix: wrap the boot-time load in a `func() { defer recover … }()` so a
panic from a nil pool logs a `WARN user plugins boot-time load skipped
(nil DB pool)` and lets `NewRouter` return a usable router. The boot-time
load is a UX nicety (user_* flags appear in Labs at startup), not a
correctness invariant — failure here must never block router
construction.

### 3. Ship chain (this release)

```
bash ~/.multica/scripts/pre-update-snapshot.sh
cd server && go run ./cmd/migrate up    # 166 + 167 already applied
pnpm --filter @multica/desktop bundle-cli
pnpm --filter @multica/desktop build
pnpm exec electron-builder --mac --dir
cp -R dist/mac-arm64/Multica.app /Applications/
bash ~/.multica/scripts/verify-desktop-cold-start.sh
```

`migrate up` reports both `166_user_plugin` and
`167_squad_subscriber_inbox_constraints` as already applied (from the
0.3.61 install run earlier today). No new SQL this release.

## Verified

- `go test ./cmd/server/` — PASS (TestMainRouterDoesNotExposePrometheusMetrics
  was the only remaining regression).
- `go test ./internal/handler/` (single package) — PASS.
  Cross-package pollution with the rest of `./...` is unrelated to this
  change.
- `go build ./...` — clean.
- `go vet ./...` — clean.
- Three-check cold start: 5432 + 8090 listeners, /health 200, rawRequest
  grep ≥ 1.
- Row parity: workspace/issue/comment/agent counts match the 0.3.61
  baseline.
- End-to-end: squad-as-assignee task creates no 23514 on
  issue_subscriber / inbox_item; user-plugin POST /run writes the
  expected artifacts and entries.

## Known boundaries (unchanged)

- DMG creation via create-dmg 1.2.3 still hangs on this fork. Ship via
  `electron-builder --mac --dir` and `cp -R` as always.
- `pnpm build` does not run electron-builder — verify renderer asar
  replacement with `grep -c rawRequest` after every build.
- `git describe --tags` still returns a pre-update marker; never bump
  anywhere except `apps/desktop/package.json`.

Refs: `.omc/0.3.62-ship-2026-07-23.md`,
`.omc/release-notes-0.3.60.md`, `.omc/release-notes-0.3.61.md`.