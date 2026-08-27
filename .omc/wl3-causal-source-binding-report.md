# WL3 Causal Source Binding — Backend Research Report

created: 2026-08-27 · author: backend architecture research pass
scope: grounding for Work Line 3 (issue causal graph, roadmap
`.omc/plans/0.5.81-0.5.83-labs-evolution-roadmap.md` §3 / ICP-1..6)
worktree: `multica-exploration-dev-0581` (WL1 branch). All paths relative to repo root.
Migration head verified: **274** (`server/migrations/274_cleanup_orphan_experimental_resources.up.sql`);
267 `.up.sql` files exist (some numbers skipped upstream-side) → WL3 starts at **275**.

---

## 1. TIER A — Native provenance capture points

### 1.1 Enqueue seam A1: create/assign path

`IssueService.maybeEnqueueOnAssign(ctx context.Context, issue db.Issue, creatorType, actorID string)` — `server/internal/service/issue.go:624`.

- Gate: `if issue.LabSource.Valid && !experimental.AutoDispatch(issue.LabSource.String) { return }` — `issue.go:636-638`.
- Enqueue: `s.TaskService.EnqueueTaskForIssue(ctx, issue)` — `issue.go:640` (errors logged, never fatal).
- Squad variant follows via `shouldEnqueueSquadLeaderOnAssign` → `enqueueSquadLeaderTask` — `issue.go:646-648`.
- Readiness predicates: `shouldEnqueueAgentTask` skips backlog (`issuestatus.Effective(...)== "backlog"`) — `issue.go:656-661`; agent must have valid runtime + not archived — `issue.go:663-672`.

### 1.2 Enqueue seam A2: update/batch-update chokepoint

`IssueService.WillEnqueueRun(ctx context.Context, in IssueTriggerInput, probe IssueTriggerProbe) (IssueRunTrigger, bool)` — `server/internal/service/issue_trigger.go:84`.

- Same AutoDispatch gate single-sourced here for both write paths — `issue_trigger.go:97-99`.
- Normalized status transition decision (assign vs backlog→active promote) — `issue_trigger.go:111-130`; pending-run dedup via the `(issue_id, agent_id)` unique slot — `hasPendingRun` at `issue_trigger.go:186-195`.
- HTTP side: `Handler.UpdateIssue` `server/internal/handler/issue.go:2826`; lab_source flip triggers leader rewrite `assignDefaultLabAgentOnUpdate` — `issue.go:3209-3221` (func at `issue.go:3329`); `statusChanged` calc — `issue.go:3235`; broadcast with per-field flags — `issue.go:3254-3268`. Batch: `BatchUpdateIssues` `issue.go:3707`, publish `:4127-4146`.
- Comment-trigger third family (not an assign change): `Handler.shouldEnqueueOnComment` — `handler/issue.go:3590`; it funnels into the mention enqueues below.

### 1.3 The three enqueue functions (roadmap cites task.go:528/632/657)

All in `server/internal/service/task.go`:

| Function | Line | Signature core | Used by |
|---|---|---|---|
| `EnqueueTaskForIssue` | `task.go:528` | `(ctx, issue db.Issue, triggerCommentID ...pgtype.UUID)` → `(db.AgentTaskQueue, error)` | A1/A2 seams; manual rerun variants |
| `EnqueueTaskForMention` | `task.go:632` | `(ctx, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID)` | @mention comment trigger |
| `EnqueueTaskForSquadLeader` | `task.go:657` | `(ctx, issue db.Issue, leaderID, squadID pgtype.UUID, triggerCommentID pgtype.UUID)` | squad dispatch |

Shared impls: `enqueueIssueTask` `task.go:569`, `enqueueMentionTask` `task.go:676`
(withOriginator/handoff/fresh-session variants wrap them). Both write **exactly one row**
via `Queries.CreateAgentTask` (`task.go:589-601`, `task.go:691-705`) then
`broadcastTaskEvent(EventTaskQueued)` + `NotifyTaskEnqueued` (`task.go:620-621`, `713-714`).

### 1.4 Where issue_id lands on tasks — already queryable

`agent_task_queue.issue_id` is a **first-class FK column**, NOT a JSON payload:

- DDL: `issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE` — `server/migrations/001_init.up.sql:127-140` (made nullable by mig 033 only to admit chat tasks: `033_chat.up.sql:33`).
- INSERT params carry it explicitly: `CreateAgentTaskParams{ IssueID: issue.ID, ... }` — `task.go:592`, `task.go:694`.
- Context JSONB column (`003_task_context.up.sql`) is used ONLY by quick_create/chat task kinds (`QuickCreateContext` struct `task.go:734-752`). Issue-bound tasks store everything queryable in typed columns.

**Verdict**: `{issue_id, trigger_comment_id, originator_user_id, status timestamps}` is fully
queryable today; Tier A needs **no new column on agent_task_queue**.

Current `agent_task_queue` column inventory (provenance-relevant subset):

```
001: id, agent_id, issue_id, status CHECK('queued','dispatched','running','completed',
     'failed','cancelled' [+ 'waiting_local_directory' mig 109]), priority,
     dispatched_at, started_at, completed_at, result JSONB, error TEXT, created_at
003: context JSONB · 004: runtime_id · 020: session_id, work_dir
028: trigger_comment_id · 042: autopilot_run_id · 061: trigger_summary TEXT
066: force_fresh_session BOOL · 090: is_leader_task BOOL · 109: wait_reason TEXT
122: handoff_note TEXT · 127: squad_id UUID · 141: delivered_comment_ids
146: coalesced_comment_ids · 240: originator_user_id, accountable_user_id
227: retired_session_id · 269: branch_name TEXT        (+ lease cols migs 055/124)
keyset indexes 270 (runtime_id), 271 (agent_id), 272 (issue_id); pending unique 037
```

### 1.5 Completion / terminal-state write sites

Service layer (`server/internal/service/task.go`):

- `CompleteTask(ctx, taskID pgtype.UUID, result []byte, sessionID, workDir string)` — `task.go:1453`. Tx: `qtx.CompleteAgentTask` (`task.go:1456`) + chat resume-pointer pin. Idempotent under parallel races (`UPDATE … WHERE status='running'` no-row ⇒ success, `task.go:1489-1500`). **The ≥1-agent-comment invariant** (synthesizes fallback comment from result payload) lives at `task.go:1522-1559`.
- `FailTask(ctx, taskID, errMsg, sessionID, workDir, failureReason string)` — `task.go:1677` → `qtx.FailAgentTask` `task.go:1697`.
- Other transitions: `ClaimTask :1165`, `StartTask :1387` (broadcasts EventTaskRunning), `CancelTaskWithResult :1078`, `CancelTasksForIssue :941`, `MarkTaskWaitingLocalDirectory :1425`.

HTTP callers (terminal endpoints): `h.TaskService.CompleteTask` — `server/internal/handler/daemon.go:2575`; `h.TaskService.FailTask` — `daemon.go:3067`.

Daemon client side drives those endpoints from `server/internal/daemon/daemon.go`: FailTask at `:3054/:3120/:3131/:3213/:3265/:3288`, CompleteTask at `:3236`.

Analytics mirrors of the full lifecycle (natural precedent for a "write and forget" provenance hook): `captureTaskQueued/Dispatched/Started/Completed/Failed/Cancelled` — `task.go:233/240/251/258/265/274`.

**Issue-status transitions persist through the handler UpdateIssue SQL** (`handler/issue.go:2826`, prior-row diff `:3235`) and are *observed* terminal by `decision_sync_listeners.isTerminalStatus` — `cmd/server/decision_sync_listeners.go:261`. Note the semantic gap the roadmap exploits: an agent can deliver its final comment BEFORE the issue flips to done/cancelled (documented as the daemon.go:2583-2595 gap in `decision_sync_listeners.go:17-19`).

### 1.6 issue_dependency — dormant since 001; reuse recommendation

DDL (`server/migrations/001_init.up.sql:88-94`):

```sql
CREATE TABLE issue_dependency (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    depends_on_issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('blocks', 'blocked_by', 'related'))
);
-- NO created_at, NO actor columns, NO workspace_id (workspace scope via join),
-- NO visibility rows referencing it. Zero Go/sqlc consumers today (grep across
-- server/**.go returns nothing outside migrations).
```

**Recommendation (matches the user's decided "mixed" schema)**:

- **Revive** `issue_dependency` strictly for user-visible issue↔issue dependency edges (UI + `/api/issues/{id}/dependencies` CRUD) — it costs nothing, matches ICP-1's issues-first contract, and its three legacy type values map cleanly onto edge semantics (`blocked_by` ≈ `depends_on`, `blocks`, `related`).
- **New tables** `causal_node` / `causal_edge` for heterogeneous nodes and trust-classed edges (Tier B/C/D rows never belong in `issue_dependency`).
- **Bridge**, don't merge: when a `causal_edge(type='depends_on')` connects two issue-provenance nodes, the curator may mirror it into `issue_dependency('blocks'|'blocked_by')` with `created_by='agent'` so plain issue UI shows it; direction stays canonical in causal_edge.

Forward-only ALTER sketch for mig 275 (append-only; no drops/renames):

```sql
ALTER TABLE issue_dependency ADD COLUMN IF NOT EXISTS evidence_comment_id UUID NULL REFERENCES comment(id) ON DELETE SET NULL;
ALTER TABLE issue_dependency ADD COLUMN IF NOT EXISTS created_by  TEXT NULL DEFAULT 'user';
ALTER TABLE issue_dependency ADD COLUMN IF NOT EXISTS created_at  TIMESTAMPTZ NULL DEFAULT now();
ALTER TABLE issue_dependency ADD COLUMN IF NOT EXISTS updated_at  TIMESTAMPTZ NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS idx_issue_dependency_issue ON issue_dependency(issue_id);
CREATE INDEX IF NOT EXISTS idx_issue_dependency_depends ON issue_dependency(depends_on_issue_id);
```

(Workspace scoping rides the existing `JOIN issue` pattern — do not add workspace_id now.)

**Where stamps land (Tier A)**: all native stamps go into the new tables, keyed off live
columns — never mutate the hot queue table:
- `causal_node.provenance` JSONB: `{system:'multica_task_enqueue'|'multica_task_complete', task_id, issue_id, agent_id, trigger_comment_id}`
- recommend two **generated columns** (`source_system TEXT GENERATED ALWAYS AS (provenance->>'system') STORED`, same for record_id) + partial UNIQUE `(source_system, source_record_id)` so every emitter de-dups against origin without touching upstream tables.
- enqueue stamp source values: `queued` event row = task_id + trigger_comment_id (task.go:589-601 has them); outcome stamp source = CompleteTask result JSONB (`payload.Output`, task.go:1546-1553) + completed_at/status.

### 1.7 Emitter wiring choice (Tier A capture mechanics)

Two options verified against precedent:

1. **Bus listeners (recommended)**: mirror `decision_sync_listeners.go` — subscribe
   `EventIssueUpdated(status_changed)` + `EventTaskCompleted/Failed/Cancelled`
   (`decision_sync_listeners.go:145-213`; dedup TTL machinery `:55-125`). Pros: zero risk
   to enqueue latency, symmetric with the existing listener file the roadmap names;
   events bus is synchronous but listeners offload into goroutines.
2. Direct calls inside `enqueueIssueTask`/`CompleteTask` — tx-coupled ordering guarantees
   but couples the hot path to causal writes and duplicates the analytics-capture style
   (`captureTask*` sites above) into schema-writes.

Take bus listeners; recover ordering loss via `(source_system, source_record_id)` dedup
(same belt-and-braces philosophy as decision sync: listener dedup TTL + upstream idempotency).

---

## 2. HIDDEN AGENT TEAM MACHINERY (highest priority)

### 2.1 The table + filter mechanics (exact behavior!)

Table `experimental_resource_visibility` — `server/migrations/150_experimental_resource_visibility.up.sql`:

```
id, flag_key, resource_type CHECK IN ('agent','autopilot','skill')  [widened + 'squad' in mig 157],
resource_id UUID (**NO FK by design**), hidden BOOL DEFAULT TRUE,
UNIQUE (flag_key, resource_type, resource_id)
```

Direction clarification demanded by the brief ("fail-open = hide fails OPEN?"):

- `filterLabsHiddenByDefault[T]` — `server/internal/handler/labs_visibility_filter.go:70-128`.
  - Flag per `experimental.DefaultFor(flagKey)` == true → return slice UNFILTERED (`:79-83`).
  - Unknown key → unfiltered short-circuit (`:84-94`).
  - Lookup error → **log warn + return UNFILTERED = hidden agents APPEAR**. This is what
    "fail-open" means: the hide mechanism fails toward *visibility*, not invisibility
    (`labs_visibility_filter.go:96-103`). Hence: UX-only affordance; NEVER use this path
    for authz/admission (roadmap risk line agrees).
  - Rows apply only while their flag_key's `hidden=TRUE` (`ListHiddenResourceIDs` SQL filters `hidden = TRUE` — `server/pkg/db/queries/experimental_resource_visibility.sql:18-20`).
- Critical nuance pinned by code: `DefaultFor` (`server/internal/experimental/catalog.go:529-547`)
  reads **only the catalog literal + broken-flag blacklist**, NOT the user's experimental_pref
  (same confirmation comment at `handler/decision_sync.go:198-204`). Every built-in lab flag
  ships `DefaultVal=false`, so for ALL built-ins the "flag on ⇒ show everything" branch is dead
  code — **seeded rows are filtered out of rosters regardless of whether the user enabled the
  lab**. Practical consequence for WL3: seeding 3 agents under `flag_key='causal_graph'`
  hides them from ListAgents rosters permanently (as long as the catalog default stays false),
  which is exactly what ICP-6 wants; the "show when on" branch exists for hypothetical
  default-true keys only.
- `lab_managed` DTO stamp: `labManagedSet` — `labs_visibility_filter.go:142-162`;
  `ListLabManagedResourceIDs` deliberately ignores BOTH flag state AND the hidden value —
  **row EXISTENCE ⇒ infrastructure** (`experimental_resource_visibility.sql:32-41` call sites:
  `handler/agent.go:726-735`, `:774`; squads `handler/squad.go:251/:360`). Used so the UI can grey/exclude from pickers while name/avatar still resolve by id. A stale orphaned row keeps greying the user's own resources after teardown (the reason purge contracts exist; 0.5.60 audit found 378 orphaned rows).

Where filtering applies to member/assignee listing: `ListAgents` iterates **every** catalog key defensively — `handler/agent.go:711-719`; skills `handler/skill.go:350`; autopilots `handler/autopilot.go:373`; squads `handler/squad.go:227`.

### 2.2 Seeder precedents (idempotent INSERT conventions)

All use `Queries.InsertExperimentalResourceVisibility` whose SQL is
`INSERT … VALUES ($1,$2,$3,TRUE) ON CONFLICT (flag_key, resource_type, resource_id) DO NOTHING`
(`experimental_resource_visibility.sql:22-30`) — re-runs are no-ops.

- Single leader (semantica): upsert-by-name agent creation `GetAgentByWorkspaceAndName` → early-return id (`install_semantica.go:88-94`), else `CreateAgent` (`install_semantica.go:101-128`: RuntimeMode local, runtime resolution may be NULL with warn-log fallback `:95-100`, `PermissionMode:"public_to"`, `MaxConcurrentTasks:1`, `CustomArgs []byte("[]")` JSON-array gotcha documented `:117-120`); lock claim `experimental.Claim(LockAgent)` `install_semantica.go:72`; visibility seed `upsertSemanticaVisibility` `install_semantica.go:137-146`; runtime heal `rebindLabAgentsToOnlineRuntime` `:84`.
- Pythia forward-guard double-seed rationale (lock + visibility both seeded so future picker rewrites cannot silently re-leak): `install_pythia.go:60-68`, `upsertPythiaVisibility` `install_pythia.go:124-133`, agent `install_pythia.go:75-117`.
- Multi-agent + squad (mythos): seeds 5 named agents + the squad; IDs are runtime-derived at install time hence seeded by the install handler NOT migration — `install_mythos.go:151-160`, `upsertMythosVisibility` `install_mythos.go:180-211`; synthetic offline runtime row `"Status:"offline"` to satisfy runtime NOT NULL without faking liveness — `install_mythos.go:213-234`.
- Static-ID alternative: migration-seeded constants mirrored in Go (`visibility.go:77-113` + mig 150 seed block) guarded by catalog tests — use only if you accept hard-coded UUIDs; runtime-derived IDs force the install-handler pattern (mythos rationale, `install_mythos.go:165-179`).

User-plugin flavor (generalized manifest-driven): `seedPluginVisibility` — `handler/user_plugins.go` (~`:515+`), includes **purge-before-seed** `DeletePluginResourceVisibilityByFlagKey` (~`:538-545`).

### 2.3 Purge-on-delete parity (contract references)

- Plugin delete: tombstone → `UnregisterUserPlugin` → **purge visibility by flag_key** (`user_plugins.go:451-459`, best-effort) → clear pref. Stated rationale: lab_managed is stamped from row existence, so leftovers leak greyed-out agents indefinitely.
- Swarm GC role-agent removal: archive roles, then delete visibility rows per agent id before CASCADE run deletion — `server/internal/experimental/swarm_gc.go:288-315` (`DeleteExperimentalResourceVisibilityByResourceID`, query `experimental_resource_visibility.sql:47-53`; this step was itself a 0.5.22 audit fix).
- Orphan sweep companion: `DeleteOrphanResourceVisibilityRows` (`experimental_resource_visibility.sql:56-62`).

### 2.4 GC/runtime gotcha — Start() takes NO context (confirmed)

- `SwarmGC.Start()` signature has no ctx, wired with an explicit comment recording the
  **0.5.39 regression**: passing bootCtx made Run exit ~8ms after router boot (ctx.Done at setup completion), silently disabling cleanup forever — `server/cmd/server/router.go:777-785`.
- Reference loop shape: `SemanticaGC.Start()` launches own goroutine behind an atomic CAS — `experimental/semantica_gc.go:86-91`; idempotent Stop via `sync.Once` + stopped channel `:95-105`; each sweep builds its own `context.WithTimeout(context.Background(), 30s)` `:126-129`. Siblings: `RuntimeGC`/`AuthTokenGC`/ACLReconciler Start()s — `experimental/runtime_gc.go:114`, `auth_token_gc.go:100`, `semantica_acl_reconciler.go:84`.
- Shutdown order law: HTTP drain FIRST, scheduler/GC stop AFTER (`cmd/server/main.go:545-556` comment block), GC Stop() calls are held on the Handler (`main.go:583-601`). Long-lived loops therefore never accept request contexts; they own `context.Background()` internally or receive fresh per-tick timeouts.
- Scheduled-job alternative (lease-backed, durable): JobSpec + Register — `scheduler.NewManager(pool)` + `Register(TaskUsageHourlyJob)` + `Register(AutopilotScheduleDispatchJob)` — `main.go:500-513` (catch-up latest-only semantics noted there). The nightly evolver should choose JobSpec; the subgraph-stale-marking ticker should be a SemanticaGC-style goroutine.

### 2.5 Concrete recommendation — the 因果图谱演化团队

Catalog: add flag key literal `causal_graph` (register beside the 8 existing keys — `catalog.go:179/198/225/267/299/322/349/380`; none is timesfm/causal_graph today, confirming the roadmap table).

Team composition (3 hidden agents + 1 autopilot, matching the user's decision matrix):

| Identity (name-based upsert) | Role | Drives |
|---|---|---|
| `causal_graph_curator` | LLM triple extraction over new comments/transitions | Tier D writes, gated `status='suggested'` |
| `causal_evidence_auditor` | validates Tier B/C cross-refs before promotion; confirms suggested edges on evidence match | confirm/reject support |
| `causal_graph_synthesizer` | path-gap analysis; proposes missing enabling edges | nightly suggested proposals |
| autopilot `causal_graph_nightly_evolver` | cron wrapper around synthesizer + maintenance sweep | nightly cadence |

plus a **maintenance ticker goroutine** (not an agent) mirroring `SemanticaGC` for orphan reconcile/stale marking (>30d last_observed_at), started after HTTP wiring, stored on Handler for main.go Stop(). Execution identity: these agents CAN be dispatched real tasks through the normal queue when LLM work is needed (runtime binding via `resolveWorkspaceOnlineRuntime`; NULL-runtime cold-start tolerated with the semantica/pythia warn-log pattern) — pure mechanical sweeps (GC) stay in-process goroutines, honoring ICP-6's "admission relies on enabled flags + AgentReadiness, not the hidden list".

Seeding recipe (`install_causal_graph.go`, one commit-scoped installer):

1. Per agent: `GetAgentByWorkspaceAndName` → early-return; else CreateAgent with same field conventions as `install_semantica.go:101-128` (public_to, MaxConcurrentTasks=1, CustomArgs `[]`, instructions bilingual like the semantica leader).
2. `experimental.Claim(LockAgent)` per agent; register LockAutopilot claim for the autopilot row (precedent: semantica `install_semantica.go:72`).
3. Visibility seeds: `InsertExperimentalResourceVisibility{FlagKey:"causal_graph", ResourceType:"agent"}` ×3 (+ HideAutopilot row ×1). Idempotent by construction.
4. Purge parity: on feature teardown/delete-reseed invoke `DeletePluginResourceVisibilityByFlagKey("causal_graph")` (builtin labs currently lack a delete UI; still wire the function into any uninstall/reseed path and into the installer head as purge-before-seed, mirroring `user_plugins.go:538-544`).

UX safety envelope (all directions re-derived from code above):
- Rosters/@mention/pickers hide the team whenever `hidden=TRUE` rows exist and the flag's catalog default is false — always, in practice (`labs_visibility_filter.go:79-103` + `catalog.go:529-547` + `decision_sync.go:199-204`).
- DB faults flip hiding OFF momentarily (fail-open; acceptable UX-over-authz) — never gate privileges or the autopilot admission through this filter.
- All infra writers stamp `lab_managed` independently, so even during fail-open blips the picker-grey stays correct (`ListLabManagedResourceIDs` existence-only SQL).
- Deletion hygiene: rely on purge-by-flag_key + periodic orphan sweep; document that forgetting the purge recreates the 0.5.78 stale-lab-managed bug class.

---

## 3. TIER B/C — pointer-level tracing

### 3.1 Semantica DecisionRecords (Tier B)

Export (Multica → Semantica): `Handler.SyncIssueDecisionToSemantica(row, status, actorType, actorID)` — `server/internal/handler/decision_sync.go:163-173` → goroutine body `postDecisionSync` `:186-304`.

- Envelope `semanticaDecision` — `decision_sync.go:120-136`; built by `buildSemanticaDecision` `:310-349`: **`id = "multica_" + <issue_uuid>`** (upstream upsert-on-id dedupe), `title/description(≤2000 runes)/status/outcome/tags(["multica","lab:semantica"])/visibility`.
- Provenance block: `{source:"multica", issue_id, workspace_id, actor_type ('system'|'user'|'agent'), actor_id?, occurred_at RFC3339}` — `:340-347`. This is the direct column-mapping surface the roadmap claims.
- Endpoint: POST upstream `{LoopbackURL}/api/decisions` with `X-Multica-Embedded: 1` — `decision_sync.go:96, 238-249`; gating lesson: do NOT re-check `DefaultFor` inside emitters (reads catalog-default only) — `decision_sync.go:198-204`.
- Listener wiring: `cmd/server/decision_sync_listeners.go` — subscribes `EventIssueUpdated` + `EventTaskCompleted/Failed/Cancelled` (`:171-233`); **fires only when terminal AND `row.LabSource=="semantica"`** (`syncIssueRow` `:132-143`). For WL3 the causal mirror must either relax this guard or use a second listener keyed on the new flag.

Storage truth:

- Upstream subprocess persists decisions inside its own rdflib graph per workspace file `~/.multica/workspaces/<wsId>/semantica-graph.json`; canonical model classes `semantica/context/decision_models.py` (Decision/Precedent/…) — header notes `decision_sync.go:49-70`. No fork-side DecisionRecord body table exists.
- Fork-side index only: `semantica_local_decision_acl` (mig 273 — `273_semantica_local_decision_acl.up.sql:22-31`): `decision_id TEXT PK (=multica_<issue_uuid>), workspace_id, actor_type CHECK('system','user','agent','team'), actor_id TEXT, visibility CHECK('team','individual_private','shared_team')`. Write-through at `decision_sync.go:287-303`; read endpoint GET `/api/experimental/semantica/decisions` — `router.go:923` + `handler/semantica_decisions.go`.
- Decision↔issue link TODAY: implicit only — `decision_id` embeds the issue uuid and ACL rows carry workspace but NOT issue_id. **Tier B adds the explicit mapping**, see §3.3.

### 3.2 Pythia forecast ledger (Tier C)

Engine (vendored): `apps/desktop/vendor/pythia-src/engine/ledger.py`

- `Ledger` class `ledger.py:32`; append-only `runs/ledger.jsonl` writer `_append` `:62-67`.
- Forecast record shape — `record_forecasts` `ledger.py:70-92`: `{kind:"forecast", id, statement, horizon, probability, base_probability, location, reasoning, lat/lng, split, ts, resolve_after, agents:[{name,probability,model}], brief_id}`.
- Resolution record — `resolve(fid, verdict, outcome, evidence)` `ledger.py:98-102`: `{kind:"resolution", id, verdict, outcome (1.0|0.0|null), evidence (≤400 chars), resolved_ms}`; verdict domain `yes/no/unresolved` from the closer `engine/loop.py:67/81/83` (window-expired ⇒ "unresolved").
- Scorecard/Brier — `scorecard()` `ledger.py:155-220`: per-record and per-persona/per-model mean Brier `(probability - outcome)^2`.
- Server-side persistence: `pythia_forecast_run` (mig 164 — `164_pythia_forecast_run.up.sql:19-43`): `{id, workspace_id, issue_id NOT NULL FK CASCADE, rounds INT, source CHECK('oracle','synthetic','synthetic_oracle_failover','mixed'), envelopes JSONB (full round array, round order), created_at}` — one row per completed deliberation, listed via `GET /api/experimental/pythia-oracle/forecast/issue/runs?issue_id=&limit=` (`handler/forecast_issue.go:78`, handler `:411-437`, sqlc `ListPythiaForecastRunsByIssue`). Routes sit behind `RequireExperimentalFlag("pythia_oracle")` — `router.go:1093-1095`; middleware impl `handler/experimental_guard.go:44`.
- Resolution closure caveat: verdict/evidence records live ONLY in the engine jsonl (no PG copy). WL3 needs either (a) an engine-proxy passthrough (`GET /scorecard` / runs retrieval) computed at sync time — recommend, zero Python changes; or (b) teaching the proxy to expose `history_for/open_recent/due` (`ledger.py:104-138`) so the Go side can pair forecasts ↔ resolutions by shared `id`.

### 3.3 Bi-directional mapping recommendation

Single fork-owned map keyed by origin identity — foreign systems stay untouched:

```
causal_node.provenance = { system: 'semantica_decision' | 'pythia_forecast' | 'pythia_resolution'
                                | 'multica_task_enqueue' | 'multica_task_complete'
                                | 'issue_terminal_status',
                           record_id: '<native id>', extra… }
+ generated columns source_system/source_record_id + UNIQUE(source_system, source_record_id)
```

- Semantica side needs nothing new: `decision.id` (`multica_<issue_uuid>`) IS the join key, and its own `provenance.issue_id` gives reverse navigation back to the issue without any vendor edit. "Bi-directional" is achieved purely fork-side: import creates decision-node; exports keep using `SyncIssueDecisionToSemantica`; never append foreign ids INTO upstream envelopes beyond the provenance block they already carry.
- Pythia side likewise untouched: pair `forecast{id}` ↔ `resolution{id==forecast id}` natively; persist to causal_node/causal_edge only pairs where both halves exist plus a matching `pythia_forecast_run.envelopes` anchor row (or direct engine fetch if we adopt (b)); confidence seeded from scorecard Brier snapshot read once per pairing run.
- De-dup law across all tiers: emitters fire repeatedly (decision-sync's 4-event fan-out proved idempotency engineering matters); the partial-unique index makes every tier-A..C writer an UPSERT/ON-CONFLICT-noop.

---

## 4. OPEN QUESTIONS (ranked; need human/product decision)

1. **Who executes Tier-D curator work: dispatched agent tasks vs in-process service LLM calls?**
   Hidden agents with real runtimes make the curator visible in ExecutionLog/task history
   (auditable, ICP-consistent) but adds queue latency and session cost; in-process calls in
   `service/causal_graph/curator.go` are cheap/invisible but bypass the agent-platform story.
   Roadmap §3.3 lists BOTH (curator service item 13 + team seeding item 11) without deciding
   which one actually performs writes.
2. **Nightly evolver vehicle: lease-scheduler JobSpec vs Autopilot vs GC-style ticker.**
   JobSpec (main.go:502-513) brings crash recovery/idempotency/lease theft but needs sys_cron
   plumbing; Autopilot requires exposing a visible-or-hidden automation row (and quota tests);
   SemanticaGC-style tickers are simplest but latest-only catch-up must be hand-rolled.
   Pick exactly one to avoid duplicate scheduling paths writing suggested edges.
3. **Suggested-edge confirmation UX scope**: does `POST /api/causal-graph/edges/{id}/confirm|reject`
   (roadmap item 9) auto-promote auditor-verified Tier-B/C conflicts, or is human confirm
   mandatory for anything with confidence ≤ some floor? Defines the trust ladder boundary.
4. **Backfill policy**: historical issues/tasks/comments produce empty graphs on day one.
   Lazy backfill job (flag-gated daemon pass over completed tasks) vs strict "from install onward".
   Migration-time inserts would violate the "migrations are structural" house rule and block
   cold starts on big histories — recommend lazy, but confirm appetite.
5. **`causal_edge` uniqueness shape**: `UNIQUE(from,to,type) WHERE status='active'` conflicts
   with temporal re-evaluation (an old contradiction superseded by a newer supporting edge).
   Decide supersede-vs-reactivate semantics BEFORE mig 277 locks the index (trigger bumping
   `last_observed_at` per roadmap item §3.1.4 vs app-level).
6. **Pythia confidence source**: live passthrough scorecard (fresh numbers, more moving parts,
   engine must be up) vs snapshot-at-pairing persisted into edge.confidence (stable, auditable).
   Recommend snapshot + `provenance.brier_snapshot_ms`.
7. **Semantica listener relaxation**: current terminal-sync fires ONLY for
   `lab_source='semantica'` issues (`decision_sync_listeners.go:132-143`); the causal mirror
   wants ALL terminal issues when the flag is on. Confirm whether decision-sync gains the same
   wider scope (changes volume POSTed to the subprocess) or stays scoped.
8. **Hidden-team naming/language**: agent names/descriptions ship bilingual Chinese today
   (`install_semantica.go:104/115`); confirm the 因果图谱 identities follow that convention and
   locale-parity rules for their instruction strings.

## Appendix — verified constant inventory for implementation tickets

- Flags today (8): chat_pin_ui, claude_science_lab, pythia_oracle, mythos_swarm, swarm_topology, llm_wiki_bridge, code_canvas, semantica (`catalog.go` Key literals).
- Status enums: task lifecycle `agent_task_queue.status` (incl. waiting_local_directory, mig 109); hideable surfaces `'agent','autopilot','skill','squad'` (mig 150 + 157).
- Trust-ladder precedent for suggested-gating: `agent_self_optimization::edits.go` style per roadmap; not re-audited here (out of scope pass).
- Router gating pattern reference: `r.Use(h.RequireExperimentalFlag(key))` — `router.go:1093-1095`; middleware `handler/experimental_guard.go:44`.
