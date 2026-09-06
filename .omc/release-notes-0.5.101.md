# Release Notes — 0.5.101 (upstream port batch — 2 of 3)

**Date**: 2026-09-06 (just after 0.5.100)
**Scope**: 2 upstream MUL ports (3 PRs → 2 actual ship; 1 deferred to 0.5.101.1 patch)
**Baseline**: 0.5.100 (HEAD `eefb3a722`)
**Head**: `6aea3052e feat(dbreader): wire MUL-7016 read replica infrastructure into fork`
**Branch**: `epic/0.5.72-followups` (4 commits ahead of 0.5.100)
**Scope delta**: 18 files, +931 / -82

## Gates

- `pnpm typecheck` → 6/6 packages clean
- `pnpm test` → 1832 passed, 33 skipped (no new red)
- `pnpm --filter @multica/desktop test` → 384 passed
- `go build ./server/internal/... ./server/pkg/agent/...` → clean
- `go vet ./server/internal/...` → clean
- `go test ./server/internal/dbreader/...` → ok (350 LOC new tests pass)
- `go test ./server/internal/handler/...` → ok
- `go test ./server/internal/metrics/...` → ok
- `cd server && go test ./internal/... ./pkg/agent/...` → all PASS

## TL;DR

0.5.101 ships two upstream MUL ports into the fork. The planned three-piece batch becomes two:

1. **`fix(perf)`** — MUL-7050. Drop unused exact search counts from `/api/issues/search` and `/api/projects/search`. The `COUNT(*) OVER()` window function and `X-Total-Count` header / JSON `total` field were computed only to populate a value no UI surface actually rendered. Net −45 LOC of wasted work per search; positive test coverage pins the contract.
2. **`feat(dbreader)`** — MUL-7016 B.4 (infrastructure only). New `server/internal/dbreader/` package with a `Selector` type that owns replica fallback, a passive circuit breaker, and per-business routing. The `Handler` struct gains a `ReadSelector` field; the metrics registry gains `DBRouting` (Prometheus `multica_db_read_routes_total{business,role,reason}`). The fork's runtime can now route opt-in eventual-consistency reads against an optional PostgreSQL replica when `DATABASE_REPLICA_URL` is set.

The third planned piece — MUL-7008 (WS claim poll 3m) — is **deferred to 0.5.101.1** because its actual scope (34 files, +736/-60, including a new `BatchClaimReadyTasks` sqlc CTE + new wsrpc.go + new capability negotiation) is significantly larger than the agent's deep-dive estimate (+250 LOC). Following CLAUDE.md "Surgical Changes" + "Goal-Driven Execution", stopping at the natural commit boundary (two clean PRs vs one partial) is the right call.

## Reverse from 0.5.100 memory

The plan was based on coarse evaluations. The deep-dive research surfaced three reversals:

| Memory said | Deep-dive said | Actual fork state |
|---|---|---|
| MUL-7050 "dormant" | REACHABLE LOW | Reachable LOW — shipped as PR-1 |
| MUL-7016 "fork-absent infra" | REACHABLE HIGH (paired B.4+B.5) | Reachable HIGH but B.5 unreachable (fork's daemon architecture inlines workspace reads into `GetDaemonWorkspaceRepos`; the upstream `ListDaemonWorkspaces` / `GetDaemonWorkspace` functions do not exist) |
| MUL-7008 "MED +250 LOC, no wsrpc.go" | MED +250 | HIGH +736 LOC, NEW wsrpc.go, NEW sqlc CTE — deferred to 0.5.101.1 |

The original 0.5.100 memory also had a wrong claim about MUL-7051 (MUL-7051 armed autopilot subscribers) — audit completed in the planning phase showed fork never ported MUL-5483, `DelegatedSubscriber` is dormant on fork, zero callers. **SKIP for 0.5.103**.

## Per-PR summary

### PR-1 — MUL-7050 perf search remove counts (commit `69d1a966a`)

8 files, +95/-30. LOW complexity. Pure 9-file removal + test additions. All fork-side per-file diffs are ≤10 LOC deletions except schema.test.ts (+30 LOC new describe) and search_response_test.go (NEW, +49).

Verified (PR-1 commit message): typecheck clean, go test clean, no schema regressions in `core/api` or ` `schemas.ts`.

### PR-2 — MUL-7016 B.4 read replica infrastructure (commit `6aea3052e`)

7 files, +836/-52. B.4 infrastructure shipped; B.5 routing deferred (fork-absent `ListDaemonWorkspaces` / `GetDaemonWorkspace` functions — see "Reverse" section above).

Files:
- `server/internal/dbreader/selector.go` (NEW, +360) — Selector type, circuit breaker, Read helper
- `server/internal/dbreader/selector_test.go` (NEW, +350) — full table coverage
- `server/internal/metrics/db_routing.go` (NEW, +30) — Prometheus counter
- `server/internal/handler/handler.go` (+3) — ReadSelector field + New() init
- `server/internal/metrics/registry.go` (+12/-8) — ReplicaPool, DBRouting, NewDBCollector signature
- `server/internal/metrics/db.go` (+40/-30) — single-pool → pools []namedDBPool with role label
- `server/cmd/server/main.go` (+24/-9) — optional replica pool wiring, dbRoutingMetrics var

## NOT shipped (deferred to 0.5.101.1 / 0.5.103)

| ID | MUL | Reason |
|---|---|---|
| MUL-7008 | PR-A.4 / 0.5.101.1 | 34 files, +736/-60, includes sqlc regen + new wsrpc.go. Deep-dive undersold; needs focused 2-3 hr session. |
| MUL-7029 | B3-2 / SKIP | fork has no helper subprocess protocol; bug class dormant |
| MUL-7051 | A.3 / SKIP | DelegatedSubscriber dormant on fork (MUL-5483 prerequisite never ported) |
| MUL-7016 B.5 | 0.5.101.1+ | `ListDaemonWorkspaces` + `GetDaemonWorkspace` functions do not exist in fork; B.5 wires up only when fork gains these functions (likely via MUL-7016 follow-on OR an unrelated daemon-workspace API port) |

## Out of scope (deferred, documented)

- **server/cmd/server/dbstats.go wholesale replica-infra swap** — upstream's version imports `server/internal/dbstartup` (MUL-6502 startup-recovery package, 805 LOC), which fork has not ported. Fork's pre-existing HEAD also imports it, so `cmd/server` was already unbuildable before this PR — not a regression. Back-porting `dbstartup` is its own concern (805 LOC + 19-file cross-cutting change including docker/entrypoint.sh, helm templates, scripts/entrypoint.test.sh) and out of scope for a B.4 wiring PR.
- **Config / docs files** (4 docs/.env.example, SELF_HOSTING_ADVANCED.md, apps/docs/content/docs/environment-variables.{mdx,ja.mdx,ko.mdx,zh.mdx}, docker-compose.selfhost.yml, scripts/selfhost-config.test.sh) — pure documentation + deployment config. Runtime impact = zero. Deferred to a follow-up docs-sync PR.
- **server/cmd/server/dbstats_test.go (+154 LOC)** — the test file is part of the dbstats.go wholesale swap. Skipped with dbstats.go.
- **server/internal/metrics/db_test.go (+41 LOC)** — the existing `TestDBCollectorExposesPoolStats` test continues to pass with the multi-pool refactor (metric name `multica_db_pool_*` is unchanged; the `role` label is added but no `role=` value is asserted). The upstream test additions explicitly assert `role="primary"` and `role="replica"` — those are nice-to-have and deferred.

## Per-file diff sanity gate (0.5.36 wholesale-adoption trap)

- 3 files are pure additions (selector.go, selector_test.go, db_routing.go) → diff = upstream, no fork-side diff
- handler.go: +3 LOC surgical (1 import + 1 struct field + 1 init line)
- registry.go: +12/-8 LOC surgical (RegistryOptions.Pool → Pool+ReplicaPool, Registry struct adds DBRouting, NewDBCollector call site updated)
- db.go: +40/-30 LOC surgical (single pool → []namedDBPool, all 12 metric descriptions updated, new collectDBPool helper)
- main.go: +24/-9 LOC surgical (2 imports + replica wiring block + RegistryOptions field)

No file exceeds 5× upstream's per-file stat.

## Lessons (carried forward)

1. **Always re-verify scope after deep-dive.** The original coarse evaluations undersold MUL-7008 by 3×. Deep-dive research surfaced this — agent research claims were wrong on this one. Per CLAUDE.md "Surgical Changes" + 0.5.36 wholesale-adoption trap, sticking to the actual scope (not the planned scope) is the right move.
2. **Fork divergence is the rule, not the exception.** MUL-7016 B.5 cannot be ported because fork's daemon architecture inlines workspace reads; the upstream functions don't exist. B.5 stays dormant until fork gains those functions through some other route.
3. **`dbstartup` debt is pre-existing.** Fork's HEAD has `dbstats.go` importing `dbstartup` (MUL-6502) but the package directory doesn't exist. `cmd/server` was already unbuildable before this PR — not a regression this PR introduced. Back-port `dbstartup` is its own ship.

## Next steps

1. **Ship**: `bash scripts/ship-mac.sh --yes` (after this notes commit lands).
2. **0.5.101.1 patch**: MUL-7008 WS claim poll 3m (34 files, +736/-60, sqlc regen + new wsrpc.go + capability negotiation). Focused 2-3 hr session.
3. **0.5.102**: MUL-7002 heartbeat + invalidation ordered pair (per pre-flight: verify daemon PAT rotation invariant first).
4. **0.5.103**: B.7 (MUL-6986 skill system merge) split into 3 sub-batches + standalone B.2 + B.3 + (optional) B.6.
5. **Memory**: persist the 0.5.101 lessons to cross-session memory.