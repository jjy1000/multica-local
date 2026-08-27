---
name: release-notes-0.5.82
created: 2026-08-27T23:10:00+08:00
updated: 2026-08-27T23:10:00+08:00
---

# 0.5.82 — TimesFM forecasting lab (WL2: local time-series foundation model)

**Branch:** `epic/0.5.82-timesfm` → merged `epic/0.5.72-followups`
**Trigger:** labs evolution roadmap §2 (`.omc/plans/0.5.81-0.5.83-labs-evolution-roadmap.md`), OSS origin `timesfm-master/timesfm-forecasting`

## TL;DR

WL2 lands the second roadmap wave: a **local TimesFM 2.5 (200M, torch stack) forecasting lab**, issue-first per ICP-1..4. An issue assigned to the hidden `timesfm_oracle` agent extracts a numeric series from issue context and POSTs it to `POST /api/experimental/timesfm/forecast/issue`; the server forwards to the desktop-spawned FastAPI loopback engine and persists every answer into `timesfm_forecast_run` (migration 275). The lab view `/experimental/timesfm-lab` is a **records-only** surface (no manual trigger — ICP-1) that lists runs newest-first, renders a plain-SVG quantile-band chart (90%/80% bands + median), and deep-links `?issue=&run=` bidirectionally (ICP-3). Engine-down never blocks history (GET /runs is a pure DB read, ICP-2); a `seasonal_naive` / `model_present=false` provenance raises an honest fallback banner instead of passing naive numbers off as model forecasts.

## Ship shape

| Layer | What landed |
| --- | --- |
| Vendor | `apps/desktop/vendor/timesfm-src/` — torch-stack-only source trim of TimesFM 2.5 (upstream `__init__` already guards optional imports; zero source edits), `run_loopback.py` (POST /forecast, GET /health,/info; Tier-0 seasonal-naive fallback; env-before-import; RAM preflight; `HF_HUB_OFFLINE=1`, local-dir weights from `~/.multica/models/timesfm/model.safetensors`), `run.sh` (`--no-index --find-links wheelhouse`), arm64-first wheelhouse builder (`scripts/build-timesfm-wheelhouse.sh`, build-machine only — NOT executed during this ship; without a seeded wheelhouse/weights the engine runs Tier-0) |
| Catalog | `timesfm` flag (AutoDispatch=false, pinned by `TestCatalogAutoDispatchContract`), manifest `apps/desktop/resources/experiments/timesfm/manifest.json` (LoopbackService "timesfm"), generic manager descriptor in `manager-factory.ts`, `experimental_resource_lock` CHECK widened + `SourceTimesfm` (else `Claim("timesfm")` would hang on 23514) |
| DB | Migration `275_timesfm_forecast_run` (id/workspace_id/issue_id FK CASCADE/horizons/provenance CHECK model\|seasonal_naive\|mixed/result JSONB/created_at + idx (issue_id, created_at DESC)) + sqlc; static pin test; applied post-backup (`~/.multica/backups/db-pre-wl2-mig-20260827T140339Z.sql`, 105MB) |
| Server | `handler/timesfm_forecast.go` — POST (series number[]/number[][], horizon default 24 clamp [1,256], dates echo; engine-down honest 503, no synthetic envelope; persistence non-fatal, run_id rides the response for deep links) + GET /runs (newest-first, default limit 10, engine-down tolerant); `install_timesfm.go` — leader `timesfm_oracle` bound to `multica-timesfm`, purge-before-seed visibility (0.5.78 contract) |
| Client | `packages/core` zod schemas + `useTimesfmForecastRuns` (5s poll, 404→[] flag-off degradation); desktop route `/experimental/timesfm-lab` (bare `/experimental/timesfm` stays the REST proxy — semantica split precedent) + `TimesfmView` (IssueBreadcrumb, `useDeepLinkRun` receiver with aria-current highlight + auto-expand, plain-SVG quantile band, fallback banner, click-to-expand ledger contract); `LabOutputPanel` compact `TimesfmPanel` reader (run count, provenance badge, LabRunLink newest run, no-trigger pinned by test); locales ×4 (new `timesfm` ns + 3 `lab_output_panel.timesfm_*` keys) + i18n registry |
| Skill | `multica-timesfm` builtin (user-invocable:false, Bash(multica *)) adapted from the OSS skill to REST-endpoint conventions + `references/timesfm-source-map.md` + `TestTimesfmSkillCoversForecastContract`; builtin inventory now 20 |

## Law compliance (ICP-1..4)

- **ICP-1 issue-first**: runs fire ONLY from issue assignment / comment ask; view and panel have no trigger (pinned by `queryByRole(button)`).
- **ICP-2 records engine-down**: view + panel read persisted rows; 503 only on the POST path.
- **ICP-3 deep links**: `labRunHref("timesfm", issueId, runId)` → `/experimental/timesfm-lab?issue=&run=`; the run list is a wired receiver (roster comment updated); the panel's LabRunLink emits pre-scoped jumps; `run_id` rides the POST response.
- **ICP-4 append-only history**: mig 275 is forward-only; runs are immutable records.
- **Release-guard**: no new arming needed — `isLabsRoute` arms on the `/experimental/*` prefix for every adapter push, so the new route is covered by construction.

## Gates

`pnpm typecheck` 6/6 · core vitest 816/819 (3 ledgered baseline) · views vitest 1746/1788 (42 ledgered baseline, exact 9-file match) · desktop vitest **370/370** (47 files) · `go build ./...` · full `go test -count=1 -p 1` with DATABASE_URL — all ok · go vet (branch) clean.

## Known limits

- Model weights + wheelhouse are NOT shipped in-repo (gitignored, ~800MB). First-class `model` provenance requires seeding `~/.multica/models/timesfm/model.safetensors` and building the wheelhouse on an arm64 Mac; until then the engine answers `seasonal_naive` (or is down → 503 POST / records-only GET).
- XReg covariates are vendored but not exposed through the API.
