# 0.5.30 — Semantica E2E smoke runbook (R4 assertions flip to PASS)

> Re-runs `scripts/semantica-e2e-smoke.sh` against a 0.5.30 install to
> confirm the 4 R4 reinforcement assertions that were `DEFERRED` on
> 0.5.28 now flip to PASS post-P0-2 (workspace-scoped manager) + P1-1
> (X-API-Key inside auth chain). Closes the loop on the R6 verifier's
> INCONCLUSIVE P0-1 verdict (cold-launch now lands on real hardware).
>
> Source-of-truth script: `scripts/semantica-e2e-smoke.sh` (201 LoC).
> Deferred assertions live at lines 165-191; current-cycle assertions
> at lines 88-163.

## 1. Prerequisites

| Var | Required | How to set |
|---|---|---|
| `SEMANTICA_REPO_PATH` | YES | `export SEMANTICA_REPO_PATH=/Users/jiangjianyan/semantica` (cloned semantica repo; script validates `semantica/explorer` subdir at line 69-72) |
| `MULTICA_API_URL` | YES | `export MULTICA_API_URL=http://127.0.0.1:8090` (default Multica backend; verified by `curl /health` 200) |
| `MULTICA_API_TOKEN` | YES | `mat_…` token from the active profile |
| `python3` | YES (unless `--no-pip`) | `command -v python3`; script line 94 falls back to `MULTICA_PYTHON` if set |

### Extract `MULTICA_API_TOKEN` from the active profile

```bash
# 1) Find the active profile (default: desktop-localhost-8090)
PROFILE=~/.multica/profiles/desktop-localhost-8090/config.json

# 2) jq path
MULTICA_API_TOKEN="$(jq -r '.token // .api_token // .jwt' "$PROFILE")"

# 3) python3 fallback (handles nested / non-standard keys)
MULTICA_API_TOKEN="$(python3 -c '
import json, sys
d = json.load(open("'"$PROFILE"'"))
for k in ("token","api_token","jwt","multica_api_token"):
    if k in d: print(d[k]); sys.exit(0)
sys.exit(1)
')"

# 4) Sanity-check: must start with `mat_`
[[ "$MULTICA_API_TOKEN" == mat_* ]] || { echo "bad token shape"; exit 2; }

export MULTICA_API_TOKEN
```

### `vendor/semantica/run.sh` must be executable

```bash
ls -l apps/desktop/vendor/semantica/run.sh        # mode bits must include +x
chmod +x apps/desktop/vendor/semantica/run.sh    # the 0.5.29 ship already flipped this, but re-verify after a clean checkout
git update-index --chmod=+x apps/desktop/vendor/semantica/run.sh
```

The script does **not** invoke `run.sh` directly — but the spawn path
does (`subprocess-manager.ts:153` via `READY_TIMEOUT_MS`), and a
non-executable `run.sh` returns `exit 126` on ensure-up → smoke exits
with code 2 at Stage 2 (script line 116).

## 2. Pre-run checklist

| # | Check | How |
|---|---|---|
| 1 | `Multica.app` is running on `:8090` | `curl -fsS http://127.0.0.1:8090/health` returns `{"status":"ok"}` |
| 2 | 0.5.30 ship is installed at `/Applications/Multica.app` | `defaults read /Applications/Multica.app/Contents/Info CFBundleShortVersionString` prints `0.5.30` |
| 3 | `semantica` flag enabled in **Settings → Labs** | `multica experimental list` shows `semantica: enabled` |
| 5 | Workspace WS-A (personal) + WS-B (client) exist | `curl -H "Authorization: Bearer $MULTICA_API_TOKEN" -H "X-Workspace-ID: <idA>" $MULTICA_API_URL/api/workspaces/current` returns 200 for both |
| 6 | `~/.multica/workspaces/` dir layout exists (post-0.5.29) | `ls -d ~/.multica/workspaces/*/semantica-graph.json` shows ≥1 entry; pre-0.5.29 you'd see a single legacy `~/.multica/semantica-graph.json` only |
| 7 | `apps/desktop/vendor/semantica/run.sh` is `+x` (see §1) | `test -x apps/desktop/vendor/semantica/run.sh && echo OK` |
| 8 | `--no-pip` if venv cached | `ls -d ~/.multica/semantica-venv` → if exists, pass `--no-pip` (saves ~30s) |

### Dual-workspace assertion setup (R4 P0-2a)

The script's Stage-5 `find` count (line 156) and R4 (iii) assertion
require **two workspaces** to have each spawned at least one
Semantica subprocess. If you have only one workspace, the assertion
will report DEFERRED — that's expected, not a regression.

To create WS-B without polluting WS-A:

```bash
# Create workspace
WS_B_ID="$(curl -sS -X POST "$MULTICA_API_URL/api/workspaces" \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"slug":"smoke-ws-b","title":"Semantica Smoke WS-B"}' \
  | jq -r '.workspace.id')"

# Spawn ensure-up twice, once per workspace, to populate both per-wsId managers
for WS in "$WS_A_ID" "$WS_B_ID"; do
  curl -sS -X POST "$MULTICA_API_URL/api/experimental/semantica/ensure-up" \
    -H "Authorization: Bearer $MULTICA_API_TOKEN" \
    -H "X-Workspace-ID: $WS" >/dev/null
done
```

## 3. What to watch in output

The script (line 56-65) prints one line per assertion:

```
[smoke]   ✓ PASS: <label>
[smoke]   ✗ FAIL: <label>      # → stderr + script exits 1
[smoke]   → DEFERRED (0.5.29): <label>
```

On a green 0.5.30 run, **all 4 R4 deferred assertions must flip to
PASS** (or report data-dependent PASS like `.provenance`):

| # | Assertion (script line) | 0.5.28 baseline | 0.5.30 expected |
|---|---|---|---|
| (i) | unproxied caller with `X-API-Key: deliberately-wrong` → 401 (line 169-172) | DEFERRED (returns 200/302 — proxy forwards key verbatim) | **PASS**: returns `401` (P1-1 fix — `MountExperimentalProxies` now inside `r.Group(middleware.Auth(...))` + unconditional `req.Header.Del("X-API-Key")`) |
| (ii) | `wsId='../foo'` rejected by subprocess-manager (line 176) | DEFERRED (path-traversal accepted → RCE risk) | **PASS**: `subprocess-manager.ts` regex `^[0-9a-f]{8}-…-[0-9a-f]{12}$` rejects; `console.warn` logged; spawn returns `null` |
| (iii) | two workspaces spawn two distinct `BaseExperimentalManager`s (line 180) | DEFERRED (singleton keyed on flagKey → WS-B inherits WS-A's process + port + graph) | **PASS**: per-`(flagKey, wsId)` cache key → `find ~/.multica/workspaces -name semantica-graph.json | wc -l` returns **≥ 2** |
| (iv) | `.provenance` not orphaned (line 185-191) | DEFERRED (cp-only migration abandoned `.provenance` at legacy global path) | **PASS** (after first `decision_sync` fires): `find ~/.multica -name semantica-graph.json.provenance | wc -l` ≥ 1 at the workspace-scoped path |

Plus **R6 P0-1 cold-launch** closes its INCONCLUSIVE verdict:

- Stage 2 `multica experimental semantica ensure-up` must return 0
  within `READY_TIMEOUT_MS` (default 180000 ms = 180 s; bump to 240000
  if cold-start genuinely exceeds 180 s on this hardware — record
  actual `READY_TIMEOUT_MS` value in the report).

### Quick visual sanity

```bash
bash scripts/semantica-e2e-smoke.sh 2>&1 | tee /tmp/semantica-smoke.log
# 0.5.30-green tail looks like:
#
# [smoke] R4 reinforcement assertions (DEFERRED — P0-2 + P1-1 ship 0.5.29):
# [smoke]   ✓ PASS: unproxied caller (X-API-Key invalid) → 401
# [smoke]   ✓ PASS: wsId='../foo' rejected by subprocess-manager
# [smoke]   ✓ PASS: two workspaces spawn two distinct BaseExperimentalManagers
# [smoke]   ✓ PASS: .provenance exists alongside graph.json (no orphan)
# [smoke] ===== Summary =====
# [smoke] PASS: 9        (5 current-cycle + 4 newly-passing R4)
# [smoke] FAIL: 0
# [smoke] DEFERRED (0.5.29): 0
# [smoke] Current-cycle verdict: GREEN
```

## 4. Expected exit code

| Outcome | Exit code | Reason |
|---|---|---|
| All current-cycle assertions green, all 4 R4 flipped to PASS | **0** | Script line 201: `[ "$FAIL" = 0 ] && exit 0` |
| Any current-cycle assertion fails (`/health`, `/graph`, header, per-workspace) | **1** | Script line 127 / 141 / 147 / 161 |
| Prereq missing (`SEMANTICA_REPO_PATH` unset, multica CLI absent, python3 missing on cold install, `/health` 502 within timeout) | **2** | Script lines 71 / 84 / 96 / 116 |
| Unknown arg passed | **1** | Script line 52 (`exit 1`) |

`DEFERRED` does **not** flip the exit code — only `FAIL` does (line 199).

## 5. Failure mode triage (top 5)

| # | Symptom | Root cause | One-line fix |
|---|---|---|---|
| 1 | Stage 3 `/api/health` returns 502 within 180 s | Cold-start genuinely exceeded `READY_TIMEOUT_MS=180000` (manifest line 36) on this hardware | `READY_TIMEOUT_MS=240000 multica experimental semantica ensure-up`; if it still fails, bump manifest line 36 to `240000` and re-run |
| 2 | Stage 4 `X-Multica-Embedded: 1` header missing | Pre-0.5.28 fork-local middleware never installed or got reverted during 0.5.29 rebase | `grep -rn "X-Multica-Embedded" apps/desktop/src/main/experimental/` and re-apply the belt-and-braces header writer |
| 3 | R4 (i) returns 200 instead of 401 | `MountExperimentalProxies` was moved **back outside** the auth chain (e.g. an accidental revert in `server/cmd/server/router.go`) | `grep -n "MountExperimentalProxies" server/cmd/server/router.go` — must be inside the `r.Group(middleware.Auth(...))` block; revert to the 0.5.29 commit |
| 4 | R4 (iii) reports only 1 `semantica-graph.json` even with two workspaces | Per-`(flagKey, wsId)` cache key regressed (singleton by flagKey) | `grep -n "managerCache\|cacheKey" apps/desktop/src/main/experimental/subprocess-manager.ts`; key must include `${flagKey}:${wsId}`; ensure both `WS_A_ID` and `WS_B_ID` were passed via `X-Workspace-ID` on the ensure-up POST |
| 5 | Stage 5 `find` returns 0 `semantica-graph.json` | `SEMANTICA_KG_PATH` env unset AND `decision_sync` never fired AND `workspaces/<wsId>/` dir not yet created on a fresh 0.5.30 install | Trigger one decision: `multica experimental semantica ensure-up` then `curl -X POST $MULTICA_API_URL/api/experimental/semantica/api/decisions -d '{"text":"smoke seed"}' -H "Authorization: Bearer $MULTICA_API_TOKEN"` — re-run after the first sync |

### Diagnostic one-liners

```bash
# Tail server log for the active profile
tail -f ~/.multica/profiles/localhost-8090/server.log | grep -i semantica

# Inspect spawned manager state per workspace
lsof -nP -iTCP -sTCP:LISTEN | grep -E 'semantica|pythia'

# Snapshot the per-wsId managers that subprocess-manager.ts holds
ps -axo pid,command | grep -E 'semantica.*run\.sh' | grep -v grep
```

## 6. Re-runnable (`--no-pip`)

The script (line 46, 89-106) caches the venv at
`~/.multica/semantica-venv`. After the first cold install, pass
`--no-pip` to skip the 30-120 s `pip install -e $SEMANTICA_REPO_PATH`
step:

```bash
bash scripts/semantica-e2e-smoke.sh --no-pip
# Combined with --quiet to suppress per-stage info logging:
bash scripts/semantica-e2e-smoke.sh --no-pip --quiet
```

Re-runnable indefinitely — the script does not mutate state outside
its own `/tmp/multica-semantica-*.{json,log}` scratch files and the
`~/.multica/semantica-venv` venv.

## 7. Reporting

When the run finishes, paste this summary into **#semantica-audit**:

```
[0.5.30 Semantica D-smoke @ <YYYY-MM-DDTHH:MM:SSZ>]
READY_TIMEOUT_MS: <180000|240000|custom>
Workspaces tested: WS-A=<idA slug>  WS-B=<idB slug or "single-ws">
Current-cycle: PASS=<n> FAIL=<n>
R4 reinforcement (formerly DEFERRED on 0.5.28):
  (i) unproxied 401            → <PASS|FAIL> (HTTP <code>)
  (ii) wsId='../foo' rejected  → <PASS|FAIL>
  (iii) two workspaces managers → <PASS|FAIL> (graph.json count: <n>)
  (iv) .provenance not orphaned → <PASS|FAIL> (.provenance count: <n>)
Cold-launch P0-1 measurement: ensure-up returned in <Ns>
Exit code: <0|1|2>
Script log: /tmp/semantica-smoke.log
```

If any R4 item stays `FAIL` post-0.5.30, escalate to
`0.5.30-semantica-retention-2026-08-17.md` owner and attach the full
`/tmp/semantica-smoke.log` — **do not** treat a single rerun as
dispositive; rerun twice to rule out the `TestRuntimeGC_RunSweepsBeforeExit`
-class timing flake documented in
`.omc/release-notes-0.5.28-semantica-patch.md` §Ship gate verification.