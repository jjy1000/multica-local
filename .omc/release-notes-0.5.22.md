# Release Notes — 0.5.22

**Ship date:** 2026-08-15
**Branch:** `epic/0.5.13-integration`
**Baseline:** 0.5.21 (commit `9d706a956`, packaged 2026-08-14)

---

## Headline: Swarm Topology (top-level task mode parallel to `claude_science_lab`)

The flagship of this ship is the new **swarm topology** feature: a self-organising multi-agent system that the user picks at issue creation (`issue.lab_source='swarm_topology'` + `lab_mode='sole'`). The orchestrator authors N role-agents + M skills + 1 coordinating squad on bootstrap, runs a 5-phase machine (research → design → implement → review → done), and tears everything down via `swarm_gc` on terminal status. Mirrors the OSS patterns from OpenAI Swarm / CrewAI / MetaGPT / LangGraph / ChatDev with the Anthropic Jun 2025 "Built a multi-agent research system" lessons applied (≤6 roles, end-state eval at phase boundaries, external memory via per-role DB rows).

---

## What's new

### Schema (migration 241)

Four new tables — forward-only additive, idempotent + nullable defaults throughout:

- **`swarm_run`** — persistent swarm instance (parallel to `mythos_run` mig 149). Carries 5-phase machine status (`preparing` → `planning` → `running` → `monitoring` → `completed`/`aborted`/`failed`) + `topology_spec` JSONB + 72h runtime cap + interrupt tracking. UNIQUE on `root_issue_id` (one swarm per issue).
- **`swarm_role`** — per-role-agent within a swarm. Reuses `agent` table for runtime; this table owns the DAG topology (`parent_role_id` + `depends_on` JSONB) + per-role lifecycle state.
- **`swarm_role_message`** — inter-role + role↔human messages. NULL `from_role_id` = human-originated via MUL-4304 reconcile; NULL `to_role_id` = broadcast.
- **`swarm_interrupt`** — user-initiated interrupt audit log (pause / cancel / redirect / inject_message).
- Plus `experimental_resource_lock` CHECK widened to include `swarm_topology` source.

### Backend

- `server/internal/service/swarm/orchestrator.go` — per-run goroutine + 30s tick + 72h cap + 5-phase machine + role DAG walk + heartbeat/idle detection.
- `server/internal/service/swarm/types.go` — `MaxSwarmRoles=6` cap, `HeartbeatTimeout=5min`, `RoleIdleTickLimit=10`, `ArchiveTTL=7d`, `TrashTTL=90d`, `MessageTTL=30d`, `PhaseOrder` slice + `NextPhase` helper, `ValidateTopologySpec` (caps + DAG check).
- `server/internal/handler/swarm_run.go` — 4 endpoints:
  - `POST   /api/experimental/swarm-topology/runs` (bootstrap, idempotent on `root_issue_id`)
  - `POST   /api/experimental/swarm-topology/runs/{id}/interrupt` (cancel is sync status flip; others async via 30s tick)
  - `GET    /api/experimental/swarm-topology/runs/{id}/state` (live status + phase + role set + counters)
  - `GET    /api/issues/{id}/swarm-runs` (reverse lookup)
- `server/internal/handler/swarm_routes.go` — `RegisterSwarmRoutes` helper.
- `server/internal/experimental/swarm_gc.go` — `SwarmGC` cleanup loop mirroring `runtime_gc` sentinel + atomic tarGz archive. 7d archive + 90d trash retention.
- `server/internal/experimental/lock.go` — new `SourceSwarmTopology` constant + `AllSources` entry.

### Built-in skill

- `server/internal/service/builtin_skills/multica-creating-swarms/SKILL.md` — three-phase lifecycle (bootstrap / execute / cleanup) for the leader agent. Step 0 surveys workspace; Phase 1 authors role-agents + skills + squad + visibility rows + lock claim; Phase 2 walks the DAG topologically with human interrupts via MUL-4304; Phase 3 tears down on terminal.

### Active Contracts §5 (CLAUDE.md)

5 new contracts document the hard invariants:
- **#5** Lock to coordinator, no manual assignee override (extended mutex in `issue.go:2222-2254`)
- **#6** 5-phase machine is the only state transition
- **#7** Max 6 roles per swarm
- **#8** Human interrupt via comment (zero new IPC)
- **#9** Cleanup is async (mirrors `runtime_gc` retention)

### Frontend

- `packages/views/experimental/components/swarm-topology-graph.tsx` — pure-SVG node-edge renderer (no external graph lib). Kahn's algorithm topological layout + 7 status colors + curved bezier edges + arrowhead markers.
- `packages/views/experimental/components/swarm-interrupt-bar.tsx` — sticky bottom bar with Pause / Cancel / Inject Message buttons + inline prompt expand.
- `apps/desktop/src/renderer/src/pages/swarm-topology-view.tsx` — pre-workspace lab view with 5 sections (Header / TopologyGraph / RoleList table / InterruptBar / PastRunsPanel). 5s polling refetch per Active Contract #1 Mode B.
- `apps/desktop/src/renderer/src/routes.tsx` — `/experimental/swarm-topology` route registered.
- `packages/views/issues/components/issue-labs-section.tsx` — `FLAG_ROUTE_SUFFIX` extended with `swarm_topology`; `LabOutputPanel` condition extended.

### Tests

- 18 unit tests pinning Phase machine + `MaxSwarmRoles` + `ValidateTopologySpec` + `TopologySpecFromJSON` + `isTerminal`. All pass in 0.4s.
- `TestBuiltinSkillsConformToTemplate` continues to pass for the new `multica-creating-swarms` skill (auto-loaded via `embed.FS`).

### Docs

- `CLAUDE.md` — `Active Contracts §5` (5 new swarm contracts) + `C1 line refresh` (claude-lab-view.tsx 1319→1344, 1554→1585).
- `multica-creating-swarms/SKILL.md` is the source of truth for the orchestrator's lifecycle contract.

---

## Self-determined design answers (per user "自我确定后实施")

| Q | Question | Answer |
|---|---|---|
| Q1 | role-agent = `agent` row or separate `swarm_role` table? | **Hybrid** — `swarm_role.agent_id` reuses `agent` table; `swarm_role` carries topology |
| Q2 | built-in flag vs `user_swarm_topology` plugin? | **Built-in flag** — top-level task mode (not a lab sub-feature) |
| Q3 | "pause" = daemon stop or status flip? | **Status flip** — orchestrator stops enqueueing, in-flight roles complete |
| Q4 | Lab leader rewrite contract for swarm? | **Lock to coordinator** — no manual assignee override (extended mutex) |
| Q5 | Cleanup TTL? | **7d archive + 90d trash** (mirror `runtime_gc`, slightly more generous) |

---

## Risks (per `multica-version-upgrade-compat` + ship-pre-flight)

- **Migration 241 is forward-only additive** — no destructive changes. `experimental_resource_lock` CHECK widened (preserves all 6 prior source values per the migration 237 "do not tidy retired flags" convention).
- **Coordinator agent is runtime-derived** — not seeded by the migration. Initial swarm bootstrap creates `swarm_coordinator`; until then, the issue has no assignee (the mutex permits this). Subsequent PATCH triggers the 4-case leader-rewrite path.
- **`MaxSwarmRoles=6` cap is enforced at install + runtime bootstrap** — both validator (`ValidateTopologySpec`) + leader-side rejection in the skill.
- **Cleanup is async** — visible teardown may take up to 6h (the GC tick interval). The UI shows `archived` immediately.

---

## Anti-patterns explicitly avoided (per OSS survey + Anthropic Jun 2025)

1. No spawn-50 antipattern — `MaxSwarmRoles=6` cap.
2. No last-handoff-wins race (OpenAI Swarm pitfall) — every state transition is a SQL row write.
3. No manager self-delegation loop — `parent_role_id` is one-way.
4. No group-chat deadlock — explicit phase machine + terminal status.
5. No checkpoint bloat — checkpoint only at phase boundaries.
6. No memory poisoning across swarms — per-swarm scoping, GC reclaims.

---

## What's NOT in this ship

- **No packaging** — per user instruction. `pnpm --filter @multica/desktop bundle-cli` + `electron-builder --mac --dir` not run. `apps/desktop/package.json` version bumped to `0.5.22` but the `.app` is still 0.5.21 until the next packaging pass.
- **`multica lab delegate` CLI for swarm_topology** — not added yet. Planned for 0.5.23.
- **Web sidebar entry** — only the desktop `/experimental/swarm-topology` route is wired. Web `apps/web/app/[workspaceSlug]/(dashboard)/experimental/swarm-topology/page.tsx` stub can follow in 0.5.23.
- **i18n** — only English strings. zh-Hans / ja / ko translations follow in a P1 cleanup.

---

## Files changed (10 commits, ~3,500 LOC added)

```
server/migrations/241_swarm_topology.up.sql            (+250)
server/migrations/241_swarm_topology.down.sql          (+30)
server/pkg/db/queries/swarm_run.sql                    (+140)
server/pkg/db/generated/swarm_run.sql.go               (sqlc gen, ~23 funcs)
server/pkg/db/generated/models.go                       (sqlc gen, +4 structs)
server/internal/service/builtin_skills/multica-creating-swarms/SKILL.md  (+296)
server/internal/service/swarm/orchestrator.go          (+400)
server/internal/service/swarm/types.go                 (+210)
server/internal/service/swarm/orchestrator_test.go     (+210, 18 tests)
server/internal/experimental/swarm_gc.go               (+380)
server/internal/experimental/lock.go                   (+12, SourceSwarmTopology + AllSources)
server/internal/experimental/runtime_gc.go             (export writeAtomic + renameCrossDevice)
server/internal/handler/swarm_run.go                   (+400)
server/internal/handler/swarm_routes.go                (+45)
server/internal/handler/handler.go                     (+8, SwarmService field)
server/internal/handler/issue.go                       (mutex extension, ~20 lines)
server/cmd/server/router.go                             (SwarmService + swarm_gc boot wire, ~50 lines)
server/cmd/server/main.go                              (SwarmService.Stop in shutdown, ~8 lines)
packages/views/experimental/components/swarm-topology-graph.tsx           (+280)
packages/views/experimental/components/swarm-interrupt-bar.tsx            (+100)
packages/views/experimental/components/index.ts         (+9, exports)
packages/views/experimental/index.ts                    (+9, exports)
apps/desktop/src/renderer/src/pages/swarm-topology-view.tsx              (+370)
apps/desktop/src/renderer/src/routes.tsx                (+7, route registration)
packages/views/issues/components/issue-labs-section.tsx  (+5, FLAG_ROUTE_SUFFIX + LabOutputPanel)
CLAUDE.md                                              (Active Contracts §5 + C1 line refresh)
apps/desktop/package.json                              (0.5.21 → 0.5.22)
```

---

## Next ship (0.5.23 candidate)

- `multica lab delegate <swarm_id> "<task>"` CLI verb (like Mythos `lab delegate`)
- Web `apps/web/.../experimental/swarm-topology/page.tsx` sidebar stub
- i18n 4-locale translation pass
- `multica swarm list / pause / cancel / inspect` for direct CLI access to running swarms
- Optional: Anthropic research "spawn-50 antipattern" guardrail — log warning when role count approaches 6
---

## Addendum — closed-loop audit fix (2026-08-16)

**Post-ship multilens audit** (4 lenses × adversarial verify, 37 agents)
found the feature shipped but never actually ran — every bootstrap
500'd. 13 P0 + 12 P1 + 13 P2 closed-loop breakages fixed across 10
atomic commits + migration 244. See the commit messages (`85799b54e` →
`4be708847`) for the per-fix detail; summary table:

| # | Defect | Effect |
|---|---|---|
| P0-1 | `ResourceType("swarm_run")` not in lock enum | every bootstrap 500 |
| P0-2 | `TopologySpec` nil → SQL NULL vs NOT NULL | every bootstrap 23502 |
| P0-3/4 | two `*Service` instances + non-atomic lazy-init | double orchestrator goroutine per run |
| P0-5 | role-agent `runtime_id` = zero UUID | dispatch loop never closed |
| P0-6 | empty roles → `0 < 0` gate skip | false-complete in ~30s |
| P0-7/8 | missing `drainTasks` on terminal exits | daemons poll dead tasks |
| P0-9 | drain missed `waiting_local_directory` | ghost tasks claimable |
| P0-10 | coda `AuthorID` NULL vs NOT NULL | summary silently dropped |
| P0-11 | `SwarmStateResponse` missing `is_paused` | Pause label permanently wrong |
| P0-12/13 | GC visibility + lock cleanup stubs | unbounded row leaks |

Migration **244** (`swarm_run_resource_type`) widens
`experimental_resource_lock.resource_type` to include `swarm_run`.

**Verification:** `go build ./...` clean · `go test -race -short
./internal/service/swarm/` ok · `go test -race -short
./internal/experimental/` ok · `pnpm typecheck` 6/6 · `migrate up`
applied 243 + 244 against live DB.

---

## Semantica × Multica Phase 2 (2026-08-16)

Second feature shipping in 0.5.22: the **Semantica lab plugin** — a
local knowledge-graph + decision-record subprocess (`python -m
semantica.explorer`) bridged into Multica as a Labs flag. Brings
ontology / SPARQL / causal-chain / precedent search to agent roles,
with an end-to-end decision-sync loop: a Multica issue reaching a
terminal status is POSTed to Semantica's `/api/decisions` as an
idempotent `multica_<uuid>` record, so later lab-bound issues can
query prior decisions as precedents.

### What's new (14 new + 11 modified files)

- **Catalog + manifest** — `semantica` Flag (`catalog.go:353`,
  `HidesDeliverableInIssueTimeline: true`), `SourceSemantica`
  (`lock.go:119`), `experiments/semantica/manifest.json`,
  `vendor/semantica/run.sh` + `requirements.txt` (FastAPI + uvicorn +
  pydantic), migration **242** (CHECK widened to `semantica`).
- **Install handler** — `install_semantica.go` upserts the
  `semantica_decision_advisor` leader agent, claims the lock, seeds
  visibility rows, heals `runtime_id`. Workspace-scoped (matches
  `install_pythia`).
- **Leader wiring** — `defaultLabLeaderForKey` (`handler/issue.go:3043`)
  + `defaultLeaderAgentForLab` (`service/issue.go:385`) map
  `lab_source='semantica'` → `semantica_decision_advisor`.
- **Decision sync loop** — `cmd/server/decision_sync_listeners.go`
  subscribes to `EventIssueUpdated` + `EventTaskCompleted/Failed/Cancelled`,
  dedupes via `recentSyncDedup` (5-min TTL), re-reads the `db.Issue`
  row (never trusting the payload's heterogeneous `issue` field), and
  fires `handler.SyncIssueDecisionToSemantica` → `POST /api/decisions`
  in a fire-and-forget goroutine (10s timeout, never panics).
- **Skills** — `multica-semantica-decision-advisor` (delegation
  specialist; `SKILL.md` + `references/api-source-map.md`) +
  `multica-semantica` (explorer curl skill).
- **UI** — `semantica-explorer-view.tsx` (Labs-tab iframe,
  `sandbox="allow-scripts"` only, `referrerPolicy="no-referrer"`,
  opaque origin — no `allow-same-origin` per CLAUDE.md hard rule).
  Route `/experimental/semantica-explorer` (NOT `/experimental/semantica`
  — that prefix is reserved for the REST proxy).
- **CLI** — `multica lab delegate semantica "<task>"` via the general
  built-in-key pass-through (`resolveLabFlagKey`); no new endpoint.
- **i18n** — 4-locale `experimental_semantica` sidebar label +
  `experimental.json` semantica block.

### Review fixes (multilens audit: 3 reviewers + adversarial verify, 29 findings → 6 HIGH fixed)

| # | Severity | Defect | Fix |
|---|---|---|---|
| 1 | HIGH | UTF-8 byte-slice truncated CJK descriptions mid-codepoint | rune-aware `[]rune` slice + `utf8.RuneCountInString` |
| 2 | HIGH | `recentSyncDedup` unbounded growth (~25 MB/yr) | janitor goroutine (`evictStaleSyncEntries`, ticks every TTL) |
| 3 | MED | Load+Store race → up to 4 concurrent POSTs | `sync.Map.LoadOrStore` atomic dedup |
| 4 | **NEW** | `extractIssueID` panics on nil `*IssueResponse` | nil-pointer guard (found by the new test, not the verifier) |
| 5 | LOW | empty `row.Title` → unsearchable decision record | UUID-based `"Untitled issue <uuid>"` fallback |

19 new tests pin the contracts: dedup TTL + 100-goroutine concurrency,
`syncIssueRow` four-case predicate, goroutine-offload non-blocking
(httptest slow-upstream, assert caller <100 ms), end-to-end sync 6
paths (happy / subprocess-down / 5xx / flag-off / nil-registry).

### Verification

```
gofmt: clean
go vet:  clean
go test -race ./internal/handler/           → ok (18.1s)
go test -race ./internal/experimental/      → ok (1.6s)
go test -race ./cmd/server/ (semantica tests) → ok (1.9s)
```

### Deferred to 0.5.23

- **MED F8/F9** — `X-API-Key` injection for `SEMANTICA_REQUIRE_AUTH=1`
  mode (fork default is anonymous, so not a live bug).
- **MED F10** — `workspace_id` partitioning on the Semantica corpus
  (INCONCLUSIVE — needs the Semantica Python source to confirm).
- **LOW/NIT** — `http.DefaultClient` pool sharing, `--task` length
  cap in `cmd_lab.go`, iframe `src` double-slash.

### Files changed (Semantica)

```
apps/desktop/resources/experiments/semantica/manifest.json      (new)
apps/desktop/vendor/semantica/run.sh + requirements.txt         (new)
apps/desktop/src/renderer/src/pages/semantica-explorer-view.tsx (new)
apps/desktop/src/renderer/src/routes.tsx                        (+7)
apps/desktop/src/main/experimental/manager-factory.ts           (+8)
server/cmd/server/decision_sync_listeners.go + _test.go         (new)
server/internal/handler/decision_sync.go + _test.go             (new)
server/internal/handler/install_semantica.go + _test.go         (new)
server/internal/service/builtin_skills/multica-semantica-decision-advisor/*  (new)
server/internal/service/builtin_skills/multica-semantica/*      (new)
server/migrations/242_semantica_visibility_seed.{up,down}.sql   (new)
server/internal/handler/issue.go                                (+10, defaultLabLeaderForKey)
server/cmd/server/main.go                                       (+8, registerDecisionSyncListeners)
server/cmd/multica/cmd_lab.go + _test.go                        (built-in key pass-through)
packages/views/locales/{en,zh-Hans,ja,ko}/layout.json           (experimental_semantica)
```
