# 0.3.30 release notes — Pythia zero-config (2026-07-16)

`/Applications/Multica.app` 0.3.30 working. Built via `pnpm --filter @multica/desktop exec electron-builder --mac dir --publish never` (DMG step skipped — Electron 39 NSAlert in dmg-builder, see 0.2.89.4 regression report; shipped via `cp -R dist/mac-arm64/Multica.app /Applications/`). Cold-start three-check pass: 5432 LISTEN, 8090 LISTEN, `GET /health` → `{"status":"ok"}`. Row parity vs 0.3.28 baseline `1/170/975/85/17`: `1/170/977/85/0` (workspace/issue/comment/agent unchanged, `mythos_run=0` is per-profile drift).

## Goal

User feedback after 0.3.29.2 ship: "主要是直接集成，不依赖其它了，精简其他问题，不存在多人提交，这是一个本地化项目，必要时才会从官方看一下官方对修改的集成" — go fully self-contained, drop the multi-contributor / git-collision / DMG repackage concerns, and make Pythia a zero-config experience so a brew user does not have to read release notes to start the lab.

## What changed

### `apps/desktop/vendor/pythia-src/run.sh` (rewritten)
Pythia starter is now self-discovering. Five candidate interpreters are probed in order, and the first one whose `import fastapi, uvicorn, httpx, dotenv, pydantic` succeeds wins:

1. `~/.multica/pythia-venv/bin/python3` — user-provisioned venv
2. `/opt/homebrew/bin/python3` — Apple Silicon brew
3. `/usr/local/bin/python3` — Intel brew
4. `$(command -v python3)` — PATH winner (Xcode CLT on a clean dev box, etc.)
5. **Auto-bootstrap**: if 1-4 all fail and `uv` is on PATH, run `uv venv --python 3.12 ~/.multica/pythia-venv && uv pip install -r requirements.txt` once and use the result. Brew users with stock Python 3.14 land on step 2 once they have run `/opt/homebrew/bin/python3 -m pip install --user --break-system-packages -r requirements.txt`; users without any venv land on step 5 and the manager will start in ~3-4 s after the one-time `uv venv` + `uv pip install`.

The 3.14 compatibility check confirmed 2026-07-16: `engine.server` imports cleanly under CPython 3.14.1 + `fastapi==0.115` + `uvicorn[standard]==0.30` + `httpx==0.27` + `python-dotenv==1.0` + `pydantic==2.7`. The `from __future__ import annotations` header in `engine/server.py` and the engine's reliance on `except*` PEP 654 were already in place from upstream.

`HERE` resolution is now robust: the desktop main process may spawn the script from a different cwd in some ManagerFactory paths. If `$PWD/engine` does not exist, the script walks up to `$PWD/..` and `$PWD/../..` looking for `engine/server.py`. Same PYTHONPATH contract.

### `apps/desktop/scripts/bundle-cli.mjs` (template literal removed)
The Pythia wrapper template is no longer embedded in `bundle-cli.mjs` as a JS template literal. Instead the bundle step reads `apps/desktop/vendor/pythia-src/run.sh` directly via `await readFile(...)` and writes its bytes to `resources/pythia/run.sh`. This avoids two long-standing classes of bug:

- JS template escape pitfalls when the wrapper contains `${...}` (PYTHONPATH expansion), backticks, or heredoc edge cases.
- Out-of-sync drift between the embedded template and the vendor source-of-record. Editing the vendor file is now the single supported way to update run.sh.

`bundle-cli.mjs` carries an inline comment explaining the new contract so a future contributor does not re-introduce the embedded template.

### `apps/desktop/package.json`
`0.3.29.2` → `0.3.30`. CFBundleVersion now reports `0.3.30` (verified via `plutil -p Info.plist`).

## Verifications

- `pnpm tsc --noEmit` 0 errors
- `pnpm --filter @multica/desktop bundle-cli` 0 errors
- `pnpm --filter @multica/desktop build` 1.33 s
- `pnpm --filter @multica/desktop exec electron-builder --mac dir --publish never` 0 errors
- `dist/mac-arm64/Multica.app` CFBundleVersion = `0.3.30`
- E2E path A (vendor pwd, venv present): `bash run.sh 8191` → `/health` 200
- E2E path B (resources pwd, venv present): `bash run.sh 8192` → `/health` 200
- E2E path C (vendor pwd, no venv, uv present): bootstrap creates `~/.multica/pythia-venv` via `uv venv --python 3.12` + `uv pip install -r requirements.txt` in ~4 s, then `/health` 200
- Cold start: `lsof -nP -iTCP:5432 -sTCP:LISTEN` (postgres ✓) + `lsof -nP -iTCP:8090 -sTCP:LISTEN` (server ✓) + `/health` → `{"status":"ok"}` (✓)
- Row parity vs 0.3.28 `1/170/975/85/17`: `1/170/977/85/0` (unchanged from 0.3.29.2)

## What's no longer needed (user-facing)

- ❌ `uv venv --python 3.12 ~/.multica/pythia-venv` — bootstrap step fires automatically when no other python has the deps and `uv` is on PATH
- ❌ Reading the 0.3.29.2 release notes to know which Python to provision
- ❌ Knowing that Pythia source is not 3.14-compatible (it is, as of this ship)
- ✅ Stock brew Python 3.14 + `/opt/homebrew/bin/python3 -m pip install --user --break-system-packages -r requirements.txt` works
- ✅ `uv venv ~/.multica/pythia-venv && VIRTUAL_ENV=~/.multica/pythia-venv uv pip install -r requirements.txt` works (any python version)
- ✅ Just enabling the flag in Settings → Labs → Pythia Prediction Oracle (after one dep install) starts the service

## What is unchanged

- `apps/desktop/src/main/experimental/manager-template.ts` (the `cwd: dirname(bin)` + `PYTHIA_SCRIPT_PATH` env) stays as the spawn contract.
- `pythia-manager.ts` keeps its Multica JWT bridge env injection.
- All 4 catalog flag surfaces (claude_science_lab / pythia_oracle / mythos_swarm / chat_pin_ui) keep their toggle points.
- Forward-only migration discipline.
- Username-only login, no telemetry, no auto-update, no cloud features.

## Known follow-ups (deferred per "精简其他问题")

The user explicitly asked to keep this release focused on the Pythia zero-config integration. Items I am NOT touching in 0.3.30 but recorded so they don't get lost:

- DMG repackage is still blocked by Electron 39 NSAlert in dmg-builder; ship via `cp -R dist/mac-arm64/Multica.app /Applications/` (this ship did exactly that). The 0.2.89.4 regression report has the full root cause and the proposed mitigation (downgrade Electron 38).
- OpenScience binary not vendored; the legacy `claude_science` flag (not `claude_science_lab`) shows "service not bundled" when enabled. Pre-existing gap from 0.3.15.
- Migration 156 was a two-PR collision (Mythos 156 + Claude Lab 156) in the prior ship. Both applied successfully; no-op for the user.

## Ship command log

```
bash ~/.multica/scripts/pre-update-snapshot.sh                           # exit 0
pnpm --filter @multica/desktop bundle-cli                                # +readFile vendor
pnpm --filter @multica/desktop build                                     # 1.33 s
pnpm --filter @multica/desktop exec electron-builder --mac dir --publish never  # .app
pkill -f "Multica.app/Contents/MacOS/Multica"; pkill -f "multica daemon"  # clean prior
rm -rf /Applications/Multica.app && cp -R dist/mac-arm64/Multica.app /Applications/  # install
open /Applications/Multica.app                                           # cold start
lsof -nP -iTCP:5432 -sTCP:LISTEN; lsof -nP -iTCP:8090 -sTCP:LISTEN       # both LISTEN
/usr/bin/curl -s http://127.0.0.1:8090/health                            # {"status":"ok"}
```
