# Upstream Sync Ledger — 2026-10-02 (mini-batch, v0.6.1 window)

- Base: upstream `e31da86c9` (2026-09-30 batch end) → `2ea01ae4e` = **v0.6.1** (2026-10-01), **7 commits** (6 substantive + changelog).
- Conflict scan: no posthog / electron-updater / OAuth / CloudFront / billing hits in the window (the two perf commits touch fork-absent surfaces outright).
- Note: v0.6.1's changelog entry is **cumulative** — its features/fixes also sweep in items the previous batch already triaged SKIP (run-chart pointer + cumulative cost = MUL-7758/7780 runs-timeline line; Windows junction workdirs = MUL-7801; chat workspace switch = MUL-7732-class chat-page). Do not re-triage those from the changelog; the commit window is the truth.

## Triage (6 substantive)

| Commit | What | Verdict | Reason |
| --- | --- | --- | --- |
| `262dcb5fa` MUL-7816 | 403 usage limit classified as quota, not auth | **PORT — `4215ce3fe`** | Fork's classify.go has the exact bug shape (bare "403" auth rule at priority 3 beats "usage limit" quota rule at priority 4; access-token variant lands in context_overflow via token+limit). Fork simplification: no concurrent-request case, no `NormalizeDaemonReason` (both are part of the 384-line divergence), so the witness became the first switch case. 4 ordering tests; mutation-verified (3 cases red with rule disabled). |
| `19c5a23c2` | release hidden floating composer focus | **PORT — `4bf97dbe6`** | Fork chat-window stays mounted when closed (opacity + pointerEvents:none), and fork's blur only fires on send — the stuck-focus hazard is identical. Adapted: fork never had `use-chat-input-focus.ts` (no focus-request machinery), so only the release half was ported, extracted as `use-release-focus-when-hidden.ts` hook + `inert`/`aria-hidden` on the container (React 19 native inert). 3 hook tests; mutation-verified. |
| `43b0571f9` MUL-7821 | CLI `--duplicate-of` for issue status/update | **SKIP-defer — needs project** | Prerequisite chain never ported: MUL-7349 family is 6 commits (server duplicate column `b7cdd76ad` w/ migs 536-537 + handler + sqlc, FE status-picker trio, review follow-ups) — all landed upstream **before** the 2026-09-17 base, so they predate even the last two sync batches. `duplicate_of_issue_id` absent from fork migrations AND core schemas; CLI alone would silently no-op. Full chain = medium project (mig 536/537 → fork 294/295). |
| `d36ef1d1b` MUL-7838 PR1 | scope working-agent facet tasks by visible agents | **SKIP** | `issue_table_facets.go` + the whole issue-table facets system absent in fork. |
| `da540ea6e` MUL-7838 PR2 | limit working-agent refresh queries | **SKIP** | Meat sits in `issues/surface/` + `use-issue-surface-controller.ts` (absent) and the use-realtime-sync hunk is pure issue-table-facets plumbing (`tableAll` / `IssueTableQuerySpec` zero hits in fork's realtime sync). |
| `ba30324e1` MUL-7834 | GPT-6.1 Sol fallback + current model pricing | **SKIP — model-catalog family stays deferred (adjudicated)** | Machine has **no codex CLI installed** (claude 2.1.287 only); fork's codexStaticModels stops at the gpt-5.5 generation (models.go ~1875 lines behind, pricing.go has no gpt-6 rows). gpt-6* pricing/alias rows would be permanently dormant here. Revisit only if the codex model catalog gets re-synced wholesale (own mini-project). |
| `2ea01ae4e` | changelog v0.6.1 | **SKIP** | Would document unported features as shipped. |

## Gates (final, all green)

- `pnpm typecheck` — 6/6 turbo tasks.
- `cd server && go test -count=1 ./internal/... ./pkg/agent/... ./pkg/taskfailure/...` (DATABASE_URL exported from repo root, len=65 confirmed) — **rc=0**.
- views vitest — **1918 passed / 33 skipped** (in-case skips; net +3 from the chat hook tests). Core vitest not run — core untouched by this batch.

## Concurrent-work note

During this session, 7 files outside the batch scope showed working-tree modifications that were NOT made by this session (pythia-view, 3 pythia experimental components, use-pythia-issue-lab, forecast_issue{,_test}, forecast_run_runner — a coherent "forecast rounds = planned total" change set). They are **excluded from all three commits above** and left untouched in the working tree. Gates ran against the tree including them (their tests passed at run time), but the commits are file-scoped.
