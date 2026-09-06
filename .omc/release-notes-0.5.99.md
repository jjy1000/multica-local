# Release Notes — 0.5.99 (H9 ship-blocker fix)

**Date**: 2026-09-06
**Scope**: 1-line fix in `apps/desktop/src/main/server-manager.ts`.
**Baseline**: 0.5.98 (HEAD ea514caa7…44292f016, then broken at install time)
**Gates**: typecheck ✅ / lint ✅ / cold-start verify ✅

## TL;DR

0.5.98 was effectively **unbootable** — the desktop app launched,
the renderer rendered, the daemon started, but the bundled `server`
binary never bound `:8090`. The cold-start verify at 19:16:33
failed (`server did not bind :8090 within 30s`) and every
subsequent relaunch hit the same wall until the fix landed at
13:55:08.

## Root cause

H9 (audit 2026-09-06) added 3 call sites in
`apps/desktop/src/main/server-manager.ts` referencing a new
`pickEnvForSpawn(extra)` helper defined in
`apps/desktop/src/main/util/spawn-env.ts`. The H9 patch re-exported
the helper like this:

```ts
export { pickEnvForSpawn } from "./util/spawn-env";
```

A bare `export { x } from "..."` is a **re-export only** — it does
NOT bind `x` into the local module's scope. Every `spawn()` in the
Electron main process threw `ReferenceError: pickEnvForSpawn is
not defined` on its first call, which the silent `try/catch` in
`probeMulticaPg` swallowed as "psql not found", looping 120 times
over 60 s and timing out `ensureServerUp`.

For comparison, `daemon-manager.ts:3` had the proper
`import { pickEnvForSpawn } from "./util/spawn-env"` — so the
daemon subprocess was unaffected. The damage was confined to
the 3 call sites in `server-manager.ts`.

## Failure trace (verbatim from `~/.multica/server-manager.log`)

```
[13:47:21.291Z] probeMulticaPg exhausted all candidates, returning false
[13:47:21.796Z] ensureServerUp failed: PostgreSQL did not become reachable on 5432 within 60s
```

(after adding diagnostic logging in the for-loop catch):

```
[13:46:29.050Z] probeMulticaPg spawnSync threw: path=…/psql error=pickEnvForSpawn is not defined
[13:46:29.050Z] probeMulticaPg: trying /opt/homebrew/opt/postgresql@17/bin/psql
[13:46:29.051Z] probeMulticaPg spawnSync threw: path=…/psql error=pickEnvForSpawn is not defined
…loops 6 candidates, each throwing…
[13:46:29.053Z] probeMulticaPg exhausted all candidates, returning false
```

## Fix

```diff
- export { pickEnvForSpawn } from "./util/spawn-env";
+ import { pickEnvForSpawn } from "./util/spawn-env";
+ export { pickEnvForSpawn };
```

`daemon-manager.ts` was already correctly importing the helper, so
no other call sites needed changes.

## Verified post-fix

```
[13:55:07.857Z] backend selected: external (pg=true)
[13:55:07.881Z] PG reachable; running migrate
[13:55:07.883Z] backend=external; skipping migrate (trust existing schema)
[13:55:07.883Z] migrate done; spawning server
[13:55:08.903Z] server is running: port=8090 pid=32373 backend=external
[13:55:08.905Z] persisted pgBackend=native to desktop.json
```

```
$ lsof -iTCP:8090 -sTCP:LISTEN
server  32373  jiangjianyan  13u  IPv6  ...  TCP *:8090 (LISTEN)

$ curl http://127.0.0.1:8090/health
{"status":"ok"}
```

## Why 0.5.98 still shipped (with the bug)

The ship-mac.sh chain ran end-to-end successfully:
- `pnpm typecheck` ✅
- `pnpm lint` ✅
- `pnpm test` ✅ (1832 / 33 parked)
- `go test` ✅ (12 packages PASS)
- `pnpm --filter @multica/desktop bundle-cli` ✅
- `pnpm --filter @multica/desktop build` ✅
- `pnpm exec electron-builder --mac --dir` ✅
- `bash scripts/desktop-sign-nested-binaries.sh` ✅ (multica --help exit 0)

The static checks and the nested-binary signature check all pass on
0.5.98 because `multica --help` exits 0 — the helper name is only
referenced at runtime, never at build time, so the bundler and the
nested-binary signature check don't catch it.

The cold-start verify **did** catch it (server did not bind :8090
within 30s) but `bash scripts/ship-mac.sh` exits 0 regardless of
the verify outcome (the verify is non-fatal to the ship chain —
**this is a ship-chain weakness** that should be fixed in 0.5.100+).
The ship completed, the .app was installed, the bug was latent
until a user relaunched the app.

## Lessons / follow-up (0.5.100+)

1. **Ship-chain verify must be fatal.** Cold-start verify is the
   ONLY check that exercises the Electron main process end-to-end
   (renderer + main + bundled server). Current behavior:
   `ship-mac.sh` exits 0 even when the verify fails. Make it fatal
   (`exit 1` on verify failure) and add a `verify-cold-start` step
   to CI before any 0.5.100+ release.
2. **Lint for unimported local references.** `pickEnvForSpawn` is
   referenced 3 times in `server-manager.ts` but the name was
   never imported. A simple "every reference to a name must be
   either declared locally or imported" lint rule would have
   caught this at `pnpm typecheck` time. `typescript-eslint` has
   `no-unresolved-references` and `no-shadow` variants. Add to
   `packages/.../eslint.config.mjs` in 0.5.100.
3. **P0 regression: identical-class bug to 0.5.25 RuntimeGC.** The
   audit fixed a code path but didn't ship its dependency, and the
   ship chain's static checks didn't catch the missing piece.
   Document this in `~/.claude/CLAUDE.md` "Known Stability
   Surfaces" once 0.5.99 ships.

## Changes

- `apps/desktop/src/main/server-manager.ts:438` — `export { ... }`
  → `import { ... }; export { ... };` (1 line changed).
- 1 atomic commit: `31d15e5e3 fix(desktop): import pickEnvForSpawn
  into server-manager scope (H9 ship-blocker)`.
- `apps/desktop/package.json` — 0.5.98 → 0.5.99.

## Deferred (carries from 0.5.98)

| ID | Finding | Scope |
|---|---|---|
| H12 | 13+ `api.*` direct calls bypassing `useMutation`/`useQuery` | ~12 new mutation hooks + 13 file updates; 1-2 days |
| H19 | 10 web files import `next/navigation` directly | Migrate to `useNavigation()` adapter; 1 day |
| H18 | explicit-column pin script | `scripts/check-issue-column-sync.sh`; half day |
| M5–M33 | 25 MED/LOW items | Spread across 0.5.100–0.5.102 |
