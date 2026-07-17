# Multica 0.3.10 — 2026-07-13

## Summary

P1 hotfix for the main-process spawn ENOENT crash that 0.3.9 exposed
when a user enabled `claude_science` while the OpenScience binary was
not yet vendored. The crash surfaced as a modal NSAlert with a stack
trace the user could not act on. This release adds a pre-flight
existence check to `BaseExperimentalManager.start()` and a structured
`code = "BINARY_NOT_BUNDLED"` error that the renderer translates into
"service not bundled" copy.

## Root cause

`apps/desktop/src/main/experimental/manager-template.ts` (shipped in
0.3.8) called `spawn(bin, args, …)` directly without first checking
that the bundled binary exists at the resolved resource path. Node's
`spawn()` does not throw on ENOENT — it emits an asynchronous
`'error'` event on the returned `ChildProcess` and silently leaks the
child object. The error escaped to Node's default
unhandled-rejection path; in Electron's main process that surfaced as
a modal NSAlert the user could not act on (memory: `multica-fork-vs-
upstream-divergence-map.md` flagged this kind of UX).

The 0.3.9 ship expected `bundle-cli` to ship a real
`apps/desktop/vendor/openscience-bin/openscience` binary, but that
vendor path is still empty (the 0.3.8 caveat documented in the
release notes). 0.3.9 wiring finally exposed the missing binary to
the user, and the missing pre-check turned "service not bundled"
into "main process crash".

## Fix (3 files + 1 new test file)

1. `apps/desktop/src/main/experimental/manager-template.ts`:
   - Added `existsSync(bin)` pre-flight check before `pickFreePort()`.
     When the binary is missing, `start()` rejects synchronously with
     a structured error `code = "BINARY_NOT_BUNDLED"` and a message
     that names the expected path AND the fix command.
   - Added `child.once("error", …)` listener to capture async
     spawn errors (EACCES, EPERM, signal-killed etc.) and race the
     health-probe promise against the spawn-failure deferred so a
     failed spawn returns immediately instead of waiting
     `readyTimeoutMs`.
   - Without this fix, missing binaries also produced silent port
     leaks (the picked port was never bound). The pre-check short-
     circuits before `pickFreePort()` so no port is allocated.

2. `apps/desktop/src/renderer/src/pages/claude-science-view.tsx`:
   - New `humanizeBootError()` helper translates the structured
     error into a Labs-user-readable message that names the exact
     vendor path AND the exact `bundle-cli && package` command to
     ship a real binary.

3. `apps/desktop/src/renderer/src/pages/pythia-view.tsx`:
   - Mirror helper for the Pythia manager (Python engine
     under `apps/desktop/vendor/pythia-src/engine/`).

4. `apps/desktop/src/main/experimental/manager-template.test.ts`
   (new) — 3 vitest cases pinning the fix:
   - REGRESSION GUARD: rejects with `code = "BINARY_NOT_BUNDLED"`
     when the binary path is absent.
   - REGRESSION GUARD: does NOT call `pickFreePort()` when binary is
     missing (so no port leak).
   - Passes the existence check when binary IS staged (the
     health-probe loop then fails at "did not become healthy",
     which is the expected and documented behavior of the dummy
     shell stub).

## Verification

- `pnpm --filter @multica/desktop test` — **274 / 274 pass** (was
  271). The 3 new cases cover the regression.
- `pnpm typecheck` — 0 errors.
- Cold-start 3-check on 0.3.10 DMG:
  - 5432 LISTEN (postgres) within 8s.
  - 8090 LISTEN (server) within 8s.
  - GUI window appears.
- Row parity preserved: `workspace=1 / issue=162 / agent=80 /
  schema_migrations=184`. No SQL migrations touched.

## User-visible change

Before 0.3.10: enabling `claude_science` and clicking the sidebar
entry triggered a modal NSAlert with `spawn … ENOENT` stack trace
the user could not act on.

After 0.3.10: enabling `claude_science` and clicking the sidebar
entry shows an inline panel:

> Claude Science unavailable
>
> Claude Science 的二进制还没有被打包进桌面 app。请把 openscience 的
> Bun-built native 二进制放到 apps/desktop/vendor/openscience-bin/
> openscience,然后跑 `pnpm --filter @multica/desktop bundle-cli &&
> pnpm --filter @multica/desktop package` 重新打包。当前 Labs flag
> 已启用,但 service not bundled。

The same pattern applies to `pythia_oracle` (Pythia Python engine).

## Known caveats (unchanged from 0.3.9)

- DMG hand-built via `create-dmg` (memory 0.3.4 workaround).
- Vendor paths still empty: enabling either flag shows the new
  structured "service not bundled" panel. Populate the vendor
  paths and re-run `bundle-cli && package` to ship a 0.3.10.x
  patch that activates both flags end-to-end.

## Rollback

`git revert` the 4 file changes above + restore `package.json`
version 0.3.9. No data concerns — schema migration count unchanged.

## By the numbers

- 3 files modified, 1 new test file.
- ~75 LOC added, ~5 LOC removed.
- 0 new SQL migrations.
- 0 breaking changes to wire format.