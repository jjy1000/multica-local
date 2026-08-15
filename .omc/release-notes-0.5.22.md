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