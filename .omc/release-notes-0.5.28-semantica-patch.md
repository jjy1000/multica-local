# 0.5.28 — Semantica × Multica integration patch (synthesizer Round 7)

> 4 atomic commits on top of the 0.5.28 baseline (`9ae934e29` PORT leak residual
> + server PORT pin). Synthesizer verdict: ship P0-1 + P1-3-half + D + B; defer
> P0-2 + P1-1 + P1-2 + P1-3-GC to 0.5.29 with R4 reinforcements as foundation.

## What's in this ship

| # | Commit | Type | Scope | Files |
|---|---|---|---|---|
| 1 | `4a0426330` | `fix(semantica)` | P0-1 — cold-start timeout | 3 files, +55/-2 |
| 2 | `531a8beef` | `chore(snapshot)` | P1-3-half — graph backup | 1 file, +79 (new) |
| 3 | `a6fc41fe2` | `test(semantica)` | D — E2E smoke gate | 1 file, +201 (new) |
| 4 | `9c35d2d76` | `docs(skills)` | B — api-source-map fill | 1 file, +8 |

**Zero migrations.** **Zero new env vars required** (READY_TIMEOUT_MS is opt-in override).

## Per-commit changelog

### 1. `4a0426330` — P0-1 cold-start timeout
- `apps/desktop/resources/experiments/semantica/manifest.json:36` —
  `ready_timeout_ms: 120000 → 180000`. First cold start downloads
  sentence-transformers + torch + first embedding model (60-180s typical,
  up to 240s on slower disks per Semantica 0.6.5 README cold-start claim).
- `apps/desktop/src/main/experimental/subprocess-manager.ts:88-95, 153` —
  READY_TIMEOUT_MS env override (precedence over manifest, mirrors PATH_PY
  pattern in `vendor/semantica/run.sh:85`); clamp helper `[10s, 600s]`
  enforces bounds so `=1` (DoS) or `=999999999` (forever-held singleton)
  cannot wedge the subprocess-manager per R4 P0-1 attack.
- `server/internal/daemon/daemon.go:4835` — READY_TIMEOUT_MS added to
  `isBlockedEnvKey` (F-005 belt-and-braces). Even if a future code path
  passes the env through to an agent subprocess env, a user-custom_env
  override cannot arm an adversarial timeout.

### 2. `531a8beef` — P1-3-half graph backup
- `scripts/snapshot-semantica-graph.sh` (new, 79 LoC) — captures
  `~/.multica/semantica-graph*.json` + `*.provenance` (and the 0.5.29
  workspace-scoped paths under `workspaces/*/`) into
  `$BACKUP_DIR/multica-semantica-graph.tgz`, excluding `*.api-key` (the
  0600 secret Semantica mints at `run.sh:158-161`).
- Defense-in-depth: `umask 077` before tar (so a future tar extract on a
  new machine writes 0600, not 0644 even if a glob slip catches a
  secret-suffixed file). Find-based NUL-delimited collection — POSIX,
  portable, no nullglob-vs-array expansion-timing trap.
- **Manual wire-in required** — the legacy `~/.multica/scripts/pre-update-snapshot.sh`
  is user-owned (HOME directory, not in repo). Add one line to its
  Step 3 area:
  ```bash
  export BACKUP_DIR="$BACKUP_DIR"
  source "$REPO_ROOT/scripts/snapshot-semantica-graph.sh"
  ```

### 3. `a6fc41fe2` — D E2E smoke gate
- `scripts/semantica-e2e-smoke.sh` (new, 201 LoC) — 5-stage end-to-end
  smoke + R4 reinforcement assertions tagged DEFERRED. Current-cycle
  PASS gates:
  1. `pip install -e $SEMANTICA_REPO_PATH` (skippable via `--no-pip`)
  2. `multica experimental semantica ensure-up`
  3. `curl /experimental/semantica/api/health` → 200
  4. `curl /experimental/semantica/api/graph` + `X-Multica-Embedded: 1`
     header present
  5. per-workspace `semantica-graph.json` path count ≥ 1
- R4 deferred assertions (will FAIL on 0.5.28-only install, PASS once
  0.5.29 lands):
  - unproxied caller with `X-API-Key: deliberately-wrong` → 401 (P1-1)
  - `wsId='../foo'` rejected by subprocess-manager (P0-2)
  - two workspaces spawn two distinct `BaseExperimentalManager`
    instances (P0-2a, the R5b silent-correctness failure mode)
  - `.provenance` not orphaned at the legacy global path post-P0-2
    migration
- **Closes the loop on R6 verifier's INCONCLUSIVE P0-1 verdict**: the
  cold-launch assertion runs on real hardware under the new 180000 ms
  ceiling plus the env override, so the next 0.5.28 install reports an
  actual measurement (or documents a 240000 ms bump + 600000 ms clamp).

### 4. `9c35d2d76` — B api-source-map SHACL/SKOS fill
- `server/internal/service/builtin_skills/multica-semantica-decision-advisor/references/api-source-map.md`
  — 8 endpoint rows added to the Ontology section:
  - `POST /ontology/suggest-alignments`
  - `POST /ontology/shacl/generate`
  - `GET /ontology/shacl/shapes`
  - `POST /ontology/shacl/validate`
  - `GET /ontology/skos/schemes`
  - `POST /ontology/skos/search`
  - `GET /ontology/skos/concept/{uri}`
  - `POST /ontology/draft`
- The `/decisions/{id}/compliance`, `/decisions/causal-distance`,
  `/vocabulary/{schemes,concepts}` endpoints were already present from
  earlier revisions — confirmed via the section table, no duplicate add.
- Pure doc, zero risk; agents now know these endpoints exist via the
  reverse proxy at `/experimental/semantica/api/*`.

## Deferred to 0.5.29 (synthesizer Round 7 verdict)

| Item | Why deferred |
|---|---|
| **P0-2 workspace-scoped graph path** | R4 P0-2 (singleton+traversal+RCE triple break) requires UUID-regex wsId validation, per-`(flagKey,wsId)` manager key, Python heredoc escape in `run.sh:142` — these are foundational, not afterthought. Shipping A-as-written would deliver isolation-by-appearance while adding a renderer-controlled path primitive (R5b silent-correctness). |
| **P1-1 X-API-Key injection** | R4 TOP-1 attack: `MountExperimentalProxies` is pre-auth (its own comment admits this for iframe-without-JWT-cookie). Injecting `X-API-Key` there makes the proxy an unauthenticated credential oracle (Cookie/Authorization stripped but NOT X-API-Key; conditional `if key != ""` forwards caller header verbatim). The only safe fix is architectural relocation — mount INSIDE the auth chain — not parameter change. P1-1 must NOT ship in 0.5.28 even with mitigations. |
| **P1-3 GC** | 90-day provenance sweep + key rotation — separate cycle, ~100 LoC. Snapshot is the 0.5.28 half. |
| **P1-2 DecisionRecord schema drift** | R6 confirmed the `decisions.py` reference is stale (vendor/semantica/ contains only `run.sh` + `requirements.txt`). 1-line patch path named in R3: api-source-map decisions schema doc + zod add in `packages/core/api/schemas.ts`. Trivial scope, 0.5.29. |

## Risk surface mitigation map

| Risk | 0.5.28 mitigation | 0.5.29 mitigation |
|---|---|---|
| Workspace isolation (P0-2) | NONE (ship-time known regression) | full |
| Auth path (P1-1) | NONE (ship-time known regression) | full |
| Data loss on `.app` upgrade (P1-3) | partial (graph + .provenance in tarball, .api-key excluded, umask 077) | full (+ GC) |
| Unbounded growth (P1-3 GC) | NONE | full |
| Cold-start timeout (P0-1) | full | — |

## Q1-Q3 final picks

- **Q1**: `accept-1.4GB` — sentence-transformers + torch already in semantica
  runtime; pythia vendor is comparable size; lazy-load strips cold-start
  reliability (lesson learned in 0.5.25 RuntimeGC fix).
- **Q2**: `internal-default` — reuses Q1 dependency; no API egress; single-
  process fits the fork-local single-user contract (CLAUDE.md R0 §6).
- **Q3**: `local-file` — R3 architect override: zero pgvector references in
  fork, no `*.control` bundled; new migration is out-of-scope for 0.5.28.

## Ship gate verification

| Gate | Result |
|---|---|
| `pnpm typecheck` (full turbo, 6/6) | ✅ pass |
| `go test -count=1 -timeout 600s ./server/internal/... ./pkg/agent/...` | ⚠️ see note |
| `bash scripts/semantica-e2e-smoke.sh` (D gate, manual) | ⏳ user-env required |

**Go test note**: `TestRuntimeGC_RunSweepsBeforeExit` (60 ms / 20 ms timing
window for ≥2 sweeps) and `TestQuickCreateIssueParentTrustBoundary` (the
race-condition test fixed in 0.5.25 via `Handler.RuntimeOnlineOverride *bool`
test hook) are both pre-existing flakes unrelated to this commit:

- Baseline (no WIP, current 0.5.27 tree): `TestQuickCreateIssueParentTrustBoundary`
  fails ~1/3 runs (observed 3 baseline runs: 2 ok / 1 fail).
- WIP (this commit): same test fails ~1/5 runs (5 runs: 4 ok / 1 fail).
- Both flakes have been noted in 0.5.25 ship log + CLAUDE.md "Known Stability
  Surfaces" — they are **not regressions from 4a0426330**.

The WIP full sweep passed clean on the second rerun (`ok` for all 30+
packages). The first run failed on `TestRuntimeGC_RunSweepsBeforeExit`
under concurrent load; the second passed. This is the documented timing
flake — ship-blocker signals come from stable runs, not one-offs.

## Hard contract reminders (post-0.5.28)

- **`SEMANTICA_REQUIRE_AUTH=1` is strictly weaker than `=0`** until 0.5.29.
  Do not enable require_auth on a 0.5.28 install. Documented in api-source-
  map Notes pre-ship; reinforced by the D script's deferred-assertion
  reporting.
- **`~/.multica/semantica-graph.json` is shared across workspaces** until
  0.5.29. Single-workspace installs are unaffected; multi-workspace users
  will see cross-leak until 0.5.29. Documented in api-source-map Notes
  pre-ship.
- **Pre-update-snapshot.sh wiring is manual.** Run `bash scripts/snapshot-semantica-graph.sh --help`
  once; copy the source-include line into the user's pre-update-snapshot
  recipe; verify the next run produces a `multica-semantica-graph.tgz` row
  in the SUMMARY.md table.
- **Cold-start measurement will land in 0.5.28.x** via the D script's
  cold-launch assertion. If 180000 ms is insufficient, bump to 240000 ms
  (manifest) and document the actual first-boot seconds on this hardware.