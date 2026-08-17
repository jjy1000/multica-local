---
name: release-notes-0.5.28
created: 2026-08-17T08:43:52Z
updated: 2026-08-17T08:43:52Z
---

# 0.5.28 Release Notes — PORT/URL Env Leak Residual Fix (2026-08-17)

Urgent patch on top of 0.5.27: completes the daemon-port-env-leak fix started in commit `e4a69d314` and adds a parallel fix for the server's persisted `.env` PORT.

## Highlights

### e4a69d314 — `fix(desktop)` Pass `--server-url` when spawning daemon (carried from 0.5.27)
The GUI's `daemon-manager.ts::startDaemon` now appends `--server-url <targetApiBaseUrl>` (F-027-allowlisted URL from `desktop.json::apiUrl`) so the daemon's URL resolution does not fall back to `ws://localhost:8080/ws` (DefaultServerURL) when the launching shell leaks `PORT=8080`. The user's dev shell had `PORT=8080`; on this machine that port hosts SearXNG, so the GUI's daemon crashed in 1s and every `agent_task_queue` row stayed `queued` until `~/.multica/scripts/multica-spawn-daemon.zsh` was run manually.

### 0ff8c64a4 — `fix(desktop)` Seed `targetApiBaseUrl` for main-process auto-start
The 0.5.27 fix was conditional (`if (targetApiBaseUrl)`), but `targetApiBaseUrl` is null on the main-process auto-start path (`bootstrapCli` → `tryAutoStartFromMain`, fired at `app.whenReady()` before the renderer IPC). New `initTargetApiUrl(url)` export (F-027-allowlisted) seeded from `desktop.json::apiUrl` at `index.ts:706` immediately before `setupDaemonManager(...)`. Renderer IPC still wins (sets it later), so behavior is unchanged once the renderer mounts. Cold-launch with `prefs.autoStart: true` now uses the canonical URL from boot.

### 7b5c3322f — `fix(server)` Pin `PORT` in `buildServerEnv`
`apps/desktop/src/main/server-manager.ts::buildServerEnv` parsed `~/.multica/profiles/<p>/.env` without reconciling the `PORT` line against the freshly-derived port. A missing or stale `PORT` in the persisted `.env` would let the server bind Go's `"8080"` default while the renderer/GUI polls 8090 — fails loudly (timeout) but the same root pattern as the daemon bug. Now after `parseEnvFile`, `env.PORT` is pinned to `String(port)` whenever the file existed.

## Verification
- `pnpm typecheck --force` 6/6 (0 cached, 38s — after all 3 fixes)
- Ship chain: snapshot → migrate (no new) → bundle → electron-vite build → electron-builder `--dir` → install + re-sign (nested `multica` exit 0) → cold-start 6s, Server PID 15979, Info.plist 0.5.28
- /Applications/Multica.app = 0.5.28

## Deferred to 0.5.29+
- **stopDaemon env symmetry** (analyst 3.2, P2) — `apps/desktop/src/main/daemon-manager.ts:998` currently has no `env:` at all on the stop path; `MULTICA_LAUNCHED_BY` is missing. Becomes uniform after 3.1 lands.
- **DATABASE_URL leak on first-launch** (analyst 3.3 second half, P1) — `pgProbeUrl()` at `server-manager.ts:229-234` reads `process.env["DATABASE_URL"]` and persists into `.env` on first launch. P0 `runMigrate(backend==="external")` refusal already limits blast radius; the actual fix is gating this behind an explicit `MULTICA_DEV_DATABASE_URL` opt-in.
- **`vendor/code-canvas/run.sh` hardening** (analyst 3.4, P3) — add a comment above the `if [ -n "$1" ]` line documenting that `PORT` must be *assigned*, never `${PORT:-…}`-defaulted, since the manager's spawn env inherits the parent GUI's `PORT`.