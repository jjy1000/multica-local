# multica-timesfm — Source Map

Every contract the SKILL.md teaches, traced to the code that enforces it.
Fork paths are relative to the repository root. If a line anchor drifts,
trust the symbol name — update this file rather than deleting the
reference.

## Fork: server side

| Contract | Source |
| --- | --- |
| `POST /api/experimental/timesfm/forecast/issue` — request shape (`issue_id`, `series` number[]/number[][], `horizon` default 24 clamp [1,256], `dates`), engine-down 503, provenance collapse, `run_id`-carrying response | `server/internal/handler/timesfm_forecast.go` (`timesfmForecastRequest`, `normalizeTimesfmSeries`, `clampTimesfmHorizon`, `timesfmIssueForecast`) |
| `GET /api/experimental/timesfm/forecast/issue/runs?issue_id=&limit=` — newest-first `{id, horizons, provenance, created_at, result}`, default limit 10, engine-down tolerant | `server/internal/handler/timesfm_forecast.go` (`timesfmIssueForecastRuns`) |
| Flag gate: uniform 404 when the `timesfm` flag is off | `RequireExperimentalFlag("timesfm")` at the route registration site (router.go), `server/internal/handler/experimental_guard.go` |
| Persistence table `timesfm_forecast_run` (migration 275), provenance CHECK `model\|seasonal_naive\|mixed` | `server/migrations/275_timesfm_forecast_run*.sql` |
| Leader agent `timesfm_oracle` bound to this skill; purge-before-seed visibility | `server/internal/handler/install_timesfm.go` |

## Fork: desktop / engine

| Contract | Source |
| --- | --- |
| Engine subprocess: FastAPI loopback (`POST /forecast`, `GET /health`, `GET /info`), Tier-0 seasonal-naive fallback, RAM preflight, offline env (`HF_HUB_OFFLINE=1`, local-dir weights from `~/.multica/models/timesfm/model.safetensors`) | `apps/desktop/vendor/timesfm-src/run_loopback.py`, started via `resources/timesfm/run.sh` |
| Vendored torch-stack-only TimesFM 2.5 source | `apps/desktop/vendor/timesfm-src/` (upstream `__init__` guards optional imports) |
| Wheelhouse build (offline `pip --no-index --find-links`), arm64-first | `apps/desktop/vendor/timesfm-src/scripts/build-timesfm-wheelhouse.sh` (build-machine only; wheelhouse gitignored) |
| Loopback registration → `LoopbackService "timesfm"` in the catalog manifest; manager spawn | `apps/desktop/src/main/experimental/` (generic manifest-driven manager) |

## Client (records surfaces)

| Contract | Source |
| --- | --- |
| Route suffix `timesfm-lab` (bare `/experimental/timesfm` is the REST proxy — never route the view there) | `packages/views/issues/components/issue-labs-section.tsx` (`FLAG_ROUTE_SUFFIX`), `apps/desktop/src/renderer/src/routes.tsx` |
| Records view (ICP-2 breadcrumb, ICP-3 `?run=` receiver, quantile-band SVG, fallback banner) | `apps/desktop/src/renderer/src/pages/timesfm-view.tsx` |
| Issue-side compact reader + deep link | `packages/views/experimental/components/lab-output-panel.tsx` (`TimesfmPanel`) |
| Read hook (5s poll, 404→[], zod fallback) | `packages/core/experimental/timesfm-queries.ts` |

## Upstream derivation

- Model & quality guidance: **timesfm-master / timesfm-forecasting**
  (SKILL.md by Clayton Young @borealBytes, Apache-2.0) — the ≥32-point
  context floor, quantile-index semantics, anomaly-interval framing, and
  2.5 model numbers derive from that skill. Fork changes: agents call the
  REST endpoint instead of importing `timesfm` (the subprocess env is
  offline and wheelhouse-managed), XReg covariates are not exposed, and
  the preflight checker is the engine's own RAM guard, not a user script.
- TimesFM 2.5 checkpoint: `google/timesfm-2.5-200m-pytorch` (weights are
  NOT in this repository; seed `~/.multica/models/timesfm/model.safetensors`
  to upgrade provenance from `seasonal_naive` to `model`).
