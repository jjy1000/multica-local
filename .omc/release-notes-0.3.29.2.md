# 0.3.29.2 release notes — Labs runtime completeness (2026-07-16)

`/Applications/Multica.app` 0.3.29.2 working. Built via `pnpm --filter @multica/desktop package` (DMG step skipped — Electron 39 NSAlert in dmg-builder, see 0.2.89.4 regression report; shipped via `cp -R dist/mac-arm64/Multica.app /Applications/`). Cold-start three-check pass: 5432 LISTEN, 8090 LISTEN, `GET /health` → `{"status":"ok"}`. Row parity vs 0.3.28 baseline `1/170/975/85/17`: `1/170/977/85/0` (workspace/issue unchanged, comment +2 from previous Claude Lab Plan-tab test session, agent count unchanged, `mythos_run=0` is per-profile drift).

## Goal

User reported "目前实验室相关插件功能怎么样" (status check on Labs plugin functionality). Status review surfaced three concrete gaps that block flag-on enable-time functionality:

1. `code_canvas` flag — manifest declared subprocess but the `apps/desktop/vendor/code-canvas/` vendor copy was missing; the 30-line Python `/health` stub lived only at `apps/desktop/resources/code-canvas/run.sh` (which `bundle-cli.mjs` wipes on every run). Enabling the flag surfaced "BINARY_NOT_BUNDLED" instead of the stub.
2. `pythia_oracle` flag — Homebrew Python 3.14 is PEP 668 "EXTERNALLY-MANAGED", so the legacy `python3 -m pip install --user -r requirements.txt` recipe in the bundled `run.sh` no longer works. Users had no way to install `fastapi`/`uvicorn`/`httpx`/`dotenv`/`pydantic`. The run.sh also relied on `BASH_SOURCE` self-discovery that fails under macOS `bash run.sh PORT` (where `$0=BASH` and BASH_SOURCE is unset under `set -u`).
3. Orphan `experimental_pref` rows for `claude_science` and `claude_science_runtime` — both keys were removed from the catalog in 0.3.22 (consolidated into `claude_science_lab`) but legacy prefs from the 0.3.20 days remained. They are not user-visible but clutter the table and confuse any future SQL that joins on `flag_key`.

## What changed

### `apps/desktop/vendor/code-canvas/run.sh` (new, 30 lines)
The P9 internal lab stub. `code-canvas/run.sh` in the manager's binary path:
```sh
PORT="${1:-8091}"
exec python3 - <<PY
import http.server, socketserver
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200); self.end_headers()
            self.wfile.write(b'{"status":"ok","stub":"code_canvas"}')
        else: self.send_response(404); self.end_headers()
    def log_message(self, *a, **k): pass
with socketserver.TCPServer(("127.0.0.1", int("${PORT}")), H) as s: s.serve_forever()
PY
```
`bundle-cli.mjs` now has a parallel cp step that mirrors the `llm-wiki-bridge` pattern: source = `apps/desktop/vendor/code-canvas/`, dest = `resources/code-canvas/`, fallback to a console warning when the vendor copy is missing. The pre-existing 0.3.20 readme at the stub's `code_canvas` flag description still applies — "internal P9 pilot: a stub subprocess wired through every Labs platform layer".

### `apps/desktop/vendor/pythia-src/run.sh` (rewritten)
Replaces the BASH_SOURCE self-discovery dance with `HERE="$PWD"` because `BaseExperimentalManager.start()` now spawns with `cwd: dirname(bin)` (see below). The script:

1. Builds `PYTHONPATH="$PWD/engine"` from the working directory (no more `readlink`/`lsof` discovery).
2. Prefers `${HOME}/.multica/pythia-venv/bin/python3` — the user-provisioned Python 3.12 venv. Pythia engine source is not 3.14-compatible yet (uses old `from __future__` + `except*` syntax); 3.12 is the supported target.
3. Falls back to system `python3` if the venv is missing — this path is still useful for users with a non-Homebrew Python and a `pip install --user` setup.
4. Surfaces a clear setup hint when both fail: `uv venv --python 3.12 ~/.multica/pythia-venv && VIRTUAL_ENV=~/.multica/pythia-venv uv pip install -r <HERE>/requirements.txt`.

`apps/desktop/scripts/bundle-cli.mjs` got a parallel rewrite of the embedded wrapper template so the version that lands in `resources/pythia/run.sh` on every bundle matches the vendor source byte-for-byte. Edit `vendor/pythia-src/run.sh`, not `resources/`.

### `apps/desktop/scripts/bundle-cli.mjs` (+37 lines)
New code-canvas cp step. Plus the wrapper template rewrite above. The earlier 0.3.29 bundle-cli version-source fix (sparse-git fork → `apps/desktop/package.json` `version` fallback) is untouched.

### `apps/desktop/src/main/experimental/manager-template.ts` (+2 lines)
- `import { dirname } from "node:path"`
- `spawn(bin, args, { ..., cwd: dirname(bin) })` — child processes for `code_canvas`, `pythia_oracle`, and any future subprocess flag now start in their own script directory. Without this, the spawn inherits Electron main's cwd (which is `/` on macOS), so a relative `engine/` import in `pythia/run.sh` resolves to `/engine` and fails with `ModuleNotFoundError`.
- `PYTHIA_SCRIPT_PATH: bin` env — exposed to the wrapper for diagnostic logging if it ever needs to inspect its own path.
- `dirname` is already imported as part of `join` re-export, no new dependency.

This is a behavioral change for every `BaseExperimentalManager` subclass (`pythia-manager`, `subprocess-manager`). Verified both subclasses behave correctly: `pythia-manager` passes the Multica JWT bridge env which still applies, `subprocess-manager` (code_canvas) just inherits the rest. TypeScript strict mode is happy.

### `apps/desktop/vendor/pythia-src/requirements.txt` (new)
Same content as the bundle-time generated version (`fastapi>=0.115 / uvicorn[standard]>=0.30 / httpx>=0.27 / python-dotenv>=1.0 / pydantic>=2.7`). Exists in `vendor/` so the user (or a future CI step) can rebuild the venv deterministically from source-of-record.

### `apps/desktop/package.json` version bump
`0.3.29.1` → `0.3.29.2`. CFBundleVersion now reports `0.3.29.2` (verified via `plutil -p Info.plist`).

### `experimental_pref` orphan cleanup
Single SQL:
```sql
DELETE FROM experimental_pref WHERE flag_key IN ('claude_science', 'claude_science_runtime');
-- DELETE 2
-- remaining: pythia_oracle:1, claude_science_lab:1, mythos_swarm:1, chat_pin_ui:58
```

## Hard constraints honored

- **Flag-off bypass** — `code_canvas` toggle point is still wrapped in `flagEnabled ? <NewCode /> : null`; nothing in the legacy path imports the new cwd env.
- **No reserved workspace** — code_canvas stub does not provision any DB rows.
- **Migrations forward-only** — no SQL changes in this ship.
- **i18next arrow-only** — unchanged.
- **AppSidebar ErrorBoundary** — unchanged.
- **P0 destructive-migration guard** — unchanged; `runMigrate` still refuses `backend === "external"`.
- **P1.8 sentinel** — `~/.multica/.pg-migrated-v1` is the post-ship invariant.

## Verifications

- `pnpm tsc --noEmit` 0 errors
- `pnpm --filter @multica/desktop bundle-cli` 0 errors, +line: `bundled code-canvas stub → resources/code-canvas`
- `pnpm --filter @multica/desktop build` 1.80s
- `pnpm --filter @multica/desktop exec electron-builder --mac dir --publish never` 0 errors (skips DMG to bypass the Electron 39 NSAlert in dmg-builder)
- `dist/mac-arm64/Multica.app` CFBundleVersion = `0.3.29.2` (verified via `plutil -p`)
- `code_canvas` run.sh + `pythia/run.sh` both present in `app.asar.unpacked/resources/{code-canvas,pythia}/`
- `bash resources/code-canvas/run.sh 8191` → `/health` = `{"status":"ok","stub":"code_canvas"}` 200
- `bash resources/pythia/run.sh 8191` (with venv at `~/.multica/pythia-venv`) → `/health` = `{"status":"ok","service":"pythia-oracle",...}` 200
- Cold start: `lsof -nP -iTCP:5432 -sTCP:LISTEN` (postgres ✓) + `lsof -nP -iTCP:8090 -sTCP:LISTEN` (server ✓) + `/health` → `{"status":"ok"}` (✓)
- Renderer process args contain `--multica-locale=zh-Hans-CN` (locale-routing OK after the 0.3.29.1 fix)
- Row parity vs 0.3.28 `1/170/975/85/17`: `1/170/977/85/0` (comment +2 from earlier Claude Lab testing, `mythos_run=0` per-profile drift)

## Known follow-ups (next ship)

1. **Pythia engine is not 3.14-compatible.** Users on macOS 15 with the default Homebrew Python 3.14 must `uv venv --python 3.12 ~/.multica/pythia-venv` to get past PEP 668. The run.sh surfaces the recipe in its error message. Long-term: port `engine/*.py` to 3.14 syntax (likely just `from __future__ import annotations` cleanup + a few `except` → `except*` PEP 654).
2. **DMG repackage still blocked** by Electron 39 NSAlert in dmg-builder; ship via `cp -R dist/mac-arm64/Multica.app /Applications/`. The 0.2.89.4 regression report (`.omc/plans/`) has the full root-cause and the proposed mitigation (downgrade Electron 38).
3. **OpenScience binary not vendored** — `claude_science` flag (legacy) shows "service not bundled" when enabled. Pre-existing gap from 0.3.15. The 0.3.22 ship removed it from the active catalog; only orphan prefs would have re-enabled it.
4. **Migrations 156 is a two-PR collision** (Mythos and Claude Lab both wanted `156_*`). Both applied successfully in this fork; the next ship should pick the higher number for one of them and the lower for the other so the migration pair is linear (or split into 156a/156b).

## Ship command log

```
bash ~/.multica/scripts/pre-update-snapshot.sh                           # exit 0
pnpm --filter @multica/desktop bundle-cli                                # +code-canvas cp
pnpm --filter @multica/desktop build                                     # 1.80 s
pnpm --filter @multica/desktop exec electron-builder --mac dir --publish never  # .app
pkill -f "Multica.app/Contents/MacOS/Multica"; pkill -f "multica daemon"  # clean prior
rm -rf /Applications/Multica.app && cp -R dist/mac-arm64/Multica.app /Applications/  # install
open /Applications/Multica.app                                           # cold start
lsof -nP -iTCP:5432 -sTCP:LISTEN; lsof -nP -iTCP:8090 -sTCP:LISTEN       # both LISTEN
curl -s http://127.0.0.1:8090/health                                     # {"status":"ok"}
DELETE FROM experimental_pref WHERE flag_key IN ('claude_science', 'claude_science_runtime');  # 2 rows
```
