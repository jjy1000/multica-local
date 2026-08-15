---
name: multica-creating-swarms
description: "Use when the user has picked 'swarm topology' for an issue (issue.lab_source='swarm_topology') and the orchestrator needs to author the persistent role-agents + skills + coordinating squad for that swarm. Walks through the three-phase lifecycle: bootstrap (author N role-agents + M skills + 1 squad + bind via squad_member), execute (the orchestrator runs the 5-phase machine research → design → implement → review → done), cleanup (delete role-agent rows + skill rows + squad + visibility rows + archive swarm_run when terminal). Not for claude_science_lab (5-tab view) or single-task issue assignment."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Creating Multica swarms (swarm topology)

This is the lifecycle contract for a swarm topology instance. A swarm is
NOT a single agent — it is a self-organising multi-agent system that the
orchestrator bootstraps on demand, runs through a 5-phase machine, and
tears down when the run reaches terminal status. The role-agents and
skills created during bootstrap are scoped to the swarm — they are
deleted when the swarm ends (mirror of runtime_gc.go's sentinel pattern,
adapted to role rows).

The contract is split into three phases matching the orchestrator's
state machine (`swarm_run.status`):

- **Phase 1 — Bootstrap** (`status='preparing' → 'planning'`): author
  the role roster + skills + coordinating squad + visibility rows. All
  rows are tagged so cleanup can find them later. Idempotent via UPSERT
  on `swarm_run.id` + `swarm_role.role_name` (re-running bootstrap on
  the same swarm resumes without duplicating rows).
- **Phase 2 — Execute** (`status='planning' → 'running' → 'monitoring'`):
  the orchestrator walks the DAG topologically, enqueues each role's
  work as an `agent_task_queue` row, and advances the phase machine when
  all roles in the current phase hit `'completed'`. Human interrupts
  arrive as comments and are handled by the existing MUL-4304 reconcile
  path — no new IPC.
- **Phase 3 — Cleanup** (`status IN ('completed','aborted','failed')`):
  archive the run via `tarGz`, soft-delete role-agent rows, hard-delete
  the coordinating squad, remove `experimental_resource_visibility`
  rows. Triggered by `swarm_gc` after the 7-day archive TTL (mirrors
  `runtime_gc.go` retention ladder).

Source-traced facts land in `references/creating-swarms-source-map.md`
once the orchestrator + install handler ship. This skill is the
authoritative contract; the source-map file is evidence-layer.

## When to invoke this skill

You should invoke (or be invoked on) this skill when:

- `issue.lab_source='swarm_topology'` AND `issue.lab_mode='sole'` for a
  workspace with the swarm orchestrator installed.
- The user explicitly says "use swarm topology", "create a swarm",
  "self-organising agents", or names a task that should run as a
  persistent multi-agent system (large-scale, high-complexity, long
  duration).

Do NOT invoke this skill for:

- Single-agent task assignment (`issue.assignee_type='agent'`).
- Chat / comment operations (use `multica-mentioning`).
- claude_science_lab 5-tab view (use `multica-claude-science`).
- Mythos Swarm (sole/enhancer dual mode — use `multica-mythos`).

## Phase 1 — Bootstrap (atomic role-agents + skills + squad)

The leader is the swarm coordinator agent (auto-provisioned per
workspace, role_name = `swarm_coordinator`). On bootstrap, it must:

1. **Survey the workspace** (mirrors `multica-lab-builder` Step 0):
   ```bash
   multica experimental flags list --output json    # confirm swarm_topology enabled
   multica agent list --output json | jq '[.[] | select(.role_name == "swarm_coordinator")]'
   ```
   If `swarm_coordinator` is missing, stop and tell the user the
   orchestrator is not installed in this workspace.

2. **Create the swarm_run row** via SQL:
   ```sql
   INSERT INTO swarm_run (workspace_id, creator_user_id, root_issue_id, problem, topology_spec)
   VALUES ($1, $2, $3, $4, '{}'::jsonb);
   ```
   `topology_spec` is empty here; the leader fills it during the
   `planning` phase after reading the issue body.

3. **Author N role-agents** (cap: `MaxSwarmRoles=6`, anti-pattern #1).
   For each role (researcher / coder / reviewer / tester / domain-1 /
   domain-2), call `multica agent create`:
   ```bash
   multica agent create \
     --name "swarm_${swarm_run_id}_researcher" \
     --description "Researcher role in swarm ${swarm_run_id}" \
     --instructions "You are the researcher role for swarm ${swarm_run_id}. ..." \
     --runtime-id "${runtime_id}" \
     --output json
   ```
   Then INSERT a `swarm_role` row referencing the new agent_id:
   ```sql
   INSERT INTO swarm_role (swarm_run_id, agent_id, role_name, role_instructions, parent_role_id, depends_on)
   VALUES ($1, $2, 'researcher', $3, NULL, '[]'::jsonb);
   ```
   Reuse `multica-creating-agents` for the per-agent field contract.

4. **Author M skills** (1–3 typically; mirrors the phases). For each
   skill, call `multica skill create` with a scoped name:
   ```bash
   multica skill create \
     --name "swarm_${swarm_run_id}_research_protocol" \
     --description "Research-phase protocol for swarm ${swarm_run_id}" \
     --instructions "..." \
     --output json
   ```
   Then bind via `multica skill binding add <agent-id> <skill-name>`
   so each role-agent sees the right skills at claim time
   (`LoadAgentSkillsForClaim` reads the bindings).

5. **Create the coordinating squad**:
   ```bash
   multica squad create \
     --name "swarm_${swarm_run_id}" \
     --description "Coordinating squad for swarm ${swarm_run_id}" \
     --leader-agent-id "${coordinator_agent_id}" \
     --output json
   ```
   Then `multica squad member add <squad-id> <agent-id> --role researcher`
   for each role-agent. Members carry `role_name` so the orchestrator
   can resolve "researcher" → agent_id at execution time.

6. **Seed visibility rows** (so the role-agents + squad do not appear
   in regular pickers while the swarm runs):
   ```sql
   INSERT INTO experimental_resource_visibility (workspace_id, flag_key, kind, target_id)
   VALUES ($1, 'swarm_topology', 'agent', $2), ($1, 'swarm_topology', 'squad', $3)
   ON CONFLICT DO NOTHING;
   ```
   Mirrors `install_mythos.go:180` `upsertMythosVisibility` pattern.

7. **Claim the resource lock** so concurrent swarms in the same
   workspace do not race on the lock table:
   ```sql
   INSERT INTO experimental_resource_lock (workspace_id, experimental_source, resource_kind, resource_id)
   VALUES ($1, 'swarm_topology', 'swarm_run', $2);
   ```

8. **Flip status** `preparing → planning` and `current_phase` to the
   first phase (typically `research`).

Bootstrap is idempotent: re-running on the same `swarm_run.id` is a no-op
(`ON CONFLICT DO NOTHING` on role-name uniqueness + UPSERT on the
swarm_run row).

## Phase 2 — Execute (5-phase machine)

The orchestrator ticks every 30 s (mirrors `mythos/supervise.go:115`).
On each tick:

1. **Read ready roles**:
   ```sql
   SELECT * FROM swarm_role
   WHERE swarm_run_id = $1 AND status IN ('ready','running')
   ORDER BY created_at ASC;
   ```

2. **Topological walk**: for each role, check `parent_role_id` status.
   If parent is `'completed'`, enqueue an `agent_task_queue` row via
   `multica issue assign <issue-id> <role-agent-id>` (the same path as
   manual assignment).

3. **Heartbeat touch** on each `running` role every tick:
   ```sql
   UPDATE swarm_role SET last_heartbeat_at = now() WHERE id = $1;
   ```

4. **Phase advance gate** (mirrors mythos supervise completion
   determination, 2026-07-28 audit):
   ```sql
   SELECT COUNT(*) FROM swarm_role WHERE swarm_run_id = $1 AND status = 'completed';
   ```
   When `count == total_roles_in_phase`, advance `current_phase` to the
   next phase + reset all roles to `'ready'`. When `current_phase` hits
   `'done'`, set `status='completed'` + `completed_at=now()`.

5. **Human interrupt handling** (MUL-4304 reconcile, no new IPC):
   ```sql
   SELECT * FROM swarm_role_message
   WHERE swarm_run_id = $1 AND type='human_interrupt' AND read_by::text NOT LIKE '%${coordinator_role_id}%';
   ```
   When a new human_interrupt message is found, the orchestrator
   consumes it on the next tick (writes a `redirect`-kind
   `swarm_interrupt` row + re-prompts the in-flight role-agents).

6. **Max runtime hard-cap** (72 h): if `now() - started_at >
   max_runtime_hours`, set `status='failed'` + `interrupt_reason='runtime_cap_exceeded'`.

## Phase 3 — Cleanup (after terminal status + 7-day archive TTL)

Triggered by `swarm_gc` (parallel to `runtime_gc.go`). Sweep cadence:
every 6 h. For each `swarm_run` with `status IN ('completed','aborted','failed')`
AND `completed_at < now() - INTERVAL '7 days'`:

1. **Archive** (sentinel pattern, mirrors `runtime_gc.archiveOne`):
   ```bash
   mkdir -p ~/.multica/swarm/<YYYY-MM>/<swarm_run_id>/
   touch ~/.multica/swarm/<YYYY-MM>/<swarm_run_id>/.archiving
   # move any artifact dir (sub-task outputs) into the archive target
   touch ~/.multica/swarm/<YYYY-MM>/<swarm_run_id>/.finished
   rm ~/.multica/swarm/<YYYY-MM>/<swarm_run_id>/.archiving
   ```

2. **Soft-delete role-agent rows**:
   ```sql
   UPDATE agent SET archived_at = now()
   WHERE id IN (SELECT agent_id FROM swarm_role WHERE swarm_run_id = $1);
   UPDATE swarm_role SET status = 'archived' WHERE swarm_run_id = $1;
   ```

3. **Hard-delete the coordinating squad**:
   ```bash
   multica squad delete <squad-id> --yes
   ```

4. **Remove visibility rows**:
   ```sql
   DELETE FROM experimental_resource_visibility
   WHERE workspace_id = $1 AND flag_key = 'swarm_topology'
     AND target_id IN (SELECT agent_id FROM swarm_role WHERE swarm_run_id = $2);
   ```

5. **Release the resource lock**:
   ```sql
   DELETE FROM experimental_resource_lock
   WHERE experimental_source = 'swarm_topology' AND resource_id = $1;
   ```

6. **Archive `swarm_role_message` rows older than 30 days** (separate
   TTL from the run TTL — messages are lower-cardinality audit data
   that the orchestrator may want to keep longer for retrospective
   analysis):
   ```sql
   DELETE FROM swarm_role_message
   WHERE swarm_run_id = $1 AND created_at < now() - INTERVAL '30 days';
   ```

7. **Final unlink** (`swarm_gc.trashSweep`):
   ```bash
   tarGz ~/.multica/swarm/<YYYY-MM>/<swarm_run_id>/ ~/.multica/swarm/.trash/<swarm_run_id>-<stamp>.tar.gz
   ```

The leader agent never runs cleanup itself — it just flips status and
relies on the GC. This mirrors the `runtime_gc` contract.

## Hard constraints (do NOT violate)

1. **Max 6 roles per swarm** (`MaxSwarmRoles=6`). The orchestrator
   rejects bootstrap attempts with `> 6` roles. Anti-pattern #1
   (Anthropic Jun 2025 — spawn-50 antipattern).
2. **No cross-swarm role reuse**. Each role-agent is owned by exactly
   one swarm_run. Reuse would create attribution ambiguity in the
   trust-score ledger.
3. **Coordinator agent is fixed** at `role_name='swarm_coordinator'` for
   the swarm's lifetime. The user CANNOT swap the coordinator (Active
   Contract #5: lock to coordinator).
4. **Pause ≠ cancel**. `pause` stops new task enqueues but lets
   in-flight roles complete. `cancel` is destructive — sets
   `status='aborted'` immediately + cancels all `agent_task_queue`
   rows for this swarm.
5. **Cleanup is asynchronous** — the leader does not wait for GC.
   Visible teardown may take up to 6 h (the GC tick interval). The UI
   shows the swarm as `archived` immediately, the actual file deletes
   happen later.

## Anti-patterns explicitly avoided

1. **Spawn-50-subagents** (Anthropic Jun 2025): capped at 6 roles.
2. **Last-handoff-wins race** (OpenAI Swarm pitfall): every state
   transition is a SQL row write.
3. **Hierarchical-manager-self-delegation loop** (CrewAI documented
   failure): `parent_role_id` is one-way; orchestrator walks DAG
   top-down.
4. **Group-chat deadlock** (AutoGen): explicit phase machine + terminal
   status, never "wait for someone to say done".
5. **Checkpoint bloat** (LangGraph): checkpoint only at phase
   boundaries, not every tool call. Persisted as
   `swarm_run.current_phase` + `swarm_role.last_heartbeat_at`.
6. **Memory poisoning across swarms** (CrewAI pitfall): per-role
   memory is scoped to `swarm_run_id`, never bleeds across.

## See also

- `multica-creating-agents` — per-agent field contract (Phase 1 step 3).
- `multica-creating-agents/references/creating-agents-source-map.md` —
  evidence layer for the per-agent contract.
- `multica-mythos` — Mythos Swarm (sole/enhancer dual mode), the
  5-agent RDT orchestration template.
- `multica-squads` — squad CRUD + member binding contract.
- `multica-claude-science-runtime` — Skill adapter HTTP surface (used
  by role-agents to call into Claude Science tools).
- `multica-lab-builder` — survey-before-author discipline (Phase 1
  step 1).
- `multica-mentioning` — comment-trigger semantics for human interrupts
  (Phase 2 step 5).