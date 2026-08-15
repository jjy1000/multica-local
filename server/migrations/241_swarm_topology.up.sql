-- 0.5.21 swarm topology: persistent multi-agent system parallel to
-- claude_science_lab. When user picks "swarm topology" for an issue, this
-- becomes the issue-bound lab (issue.lab_source='swarm_topology',
-- lab_mode='sole'). Self-organizing: leader agents author N role-agents +
-- M skills + 1 coordinating squad on bootstrap, run a 5-phase machine
-- (research → design → implement → review → done), and tear everything
-- down via swarm_gc on terminal status.
--
-- Design lineage (per CLAUDE.md "Active Contracts (0.3.45.7+)" §5):
-- - Mythos dual-mode (sole/enhancer + 30s tick + 24h cap) is the
--   orchestration template — the supervisor goroutine pattern at
--   server/internal/service/mythos/supervise.go:115 ports 1:1.
-- - multica-creating-agents skill authors agent/skill/squad rows — swarm
--   uses the same path (leader runs multica-creating-swarms to author
--   role-agents on bootstrap).
-- - MUL-4304 comment reconcile is the human-in-the-loop medium (zero
--   new IPC): user comment w/ @swarm_coordinator mention wakes leader
--   per the same trigger path as agent→agent @mentions.
--
-- Idempotent + additive (CLAUDE.md: forward-only additive migrations).
-- 0.5.21 dev baseline — this migration introduces the four tables and
-- widens experimental_resource_lock CHECK to include swarm_topology so
-- swarm-managed rows can be claimed by the lock helper.
--
-- Anti-patterns explicitly avoided (per OSS survey + Anthropic Jun 2025
-- "Built a multi-agent research system"):
-- 1. No spawn-50 antipattern: MaxSwarmRoles=6 cap is enforced in the
--    orchestrator (not SQL), but we document it inline so the schema
--    reader understands why role_count<=6 is the expected shape.
-- 2. No last-handoff-wins race (OpenAI Swarm pitfall): every state
--    transition is a SQL row write, never a return-value handoff.
-- 3. No manager self-delegation loop: parent_role_id is one-way; the
--    orchestrator walks the DAG top-down each tick.
-- 4. No group-chat deadlock: explicit phase machine + terminal status
--    (mirrors mythos_run CHECK), never "wait for someone to say done".

CREATE TABLE swarm_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- caller (user who picked swarm_topology for the issue); the swarm
    -- coordinator agent is NOT the creator — this stays the human.
    creator_user_id UUID NOT NULL REFERENCES "user"(id),
    -- the lab-bound issue. UNIQUE per workspace: one swarm per issue
    -- (a re-pick on the same issue resurrects the existing run).
    root_issue_id UUID NOT NULL REFERENCES issue(id),
    problem TEXT NOT NULL,
    -- preparing (leader about to bootstrap)
    -- planning (leader analyzing + drafting role plan)
    -- running (role-agents active, orchestrator ticking)
    -- monitoring (phase transition in progress, awaiting checkpoints)
    -- completed (all phases done, awaiting GC)
    -- aborted (user cancelled)
    -- failed (orchestrator error or invariant violation)
    status TEXT NOT NULL DEFAULT 'preparing'
        CHECK (status IN ('preparing','planning','running','monitoring','completed','aborted','failed')),
    -- research / design / implement / review / done
    current_phase TEXT NOT NULL DEFAULT 'research'
        CHECK (current_phase IN ('research','design','implement','review','done')),
    -- topology spec authored by the leader on bootstrap:
    --   {roles: [{name, role_instructions, model, ...}],
    --    edges: [{from_role, to_role, type: 'sequential'|'parallel'}],
    --    phases: ['research','design','implement','review']}
    -- Mirrors Anthropic Jun 2025 "objective + output format + tool
    -- guidance per subagent" — the spec is the persisted contract.
    topology_spec JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- 72h default (mirrors Mythos 24h cap + a 3-day tail for
    -- long-horizon tasks). Orchestrator hard-cancels on overrun.
    max_runtime_hours INT NOT NULL DEFAULT 72,
    -- last user interrupt timestamp + reason. Mirrors the
    -- Mythos supervise_state JSONB shape but flatter (single
    -- most-recent interrupt is enough for the UI pill).
    interrupted_at TIMESTAMPTZ,
    interrupt_reason TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_swarm_run_by_workspace
    ON swarm_run(workspace_id, started_at DESC);
CREATE INDEX idx_swarm_run_active
    ON swarm_run(workspace_id)
    WHERE status IN ('preparing','planning','running','monitoring');
-- Reverse lookup for issue detail page (one swarm per issue).
CREATE UNIQUE INDEX idx_swarm_run_by_root_issue
    ON swarm_run(root_issue_id);

-- swarm_role: per-role-agent within a swarm. Reuses agent table for
-- the runtime identity (multica-creating-agents skill still authors
-- agent rows via multica-creating-swarms SKILL.md); this table owns
-- the topology structure (parent_role_id + depends_on edges) and the
-- role-level lifecycle state (separate from agent.runtime_status).
CREATE TABLE swarm_role (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    swarm_run_id UUID NOT NULL REFERENCES swarm_run(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id),
    -- "researcher" / "coder" / "reviewer" / "tester" / "<domain>".
    -- Matches the leader-authored role_name in topology_spec.
    role_name TEXT NOT NULL,
    -- Per-role instructions (subset of the agent's full instructions,
    -- focused on the swarm context). Daemon injects at claim time
    -- alongside the agent's base instructions.
    role_instructions TEXT NOT NULL,
    -- DAG edge: this role's work depends on parent_role completing
    -- first. NULL = top of the DAG (no parent). Used by orchestrator
    -- to schedule role-agents in topological order.
    parent_role_id UUID REFERENCES swarm_role(id),
    -- depends_on: [{role_id, type: 'sequential'|'parallel'}]
    -- Sequential = hard predecessor; Parallel = may overlap if idle.
    -- Stored as JSONB for flexibility (MUL-4525 may add new edge
    -- types — additive, no migration needed).
    depends_on JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- created (row written, agent may not exist yet)
    -- ready (agent exists, daemon can claim)
    -- running (agent_task_queue row claimed, executing)
    -- idle (waiting on parent_role)
    -- completed (finished, output persisted)
    -- failed (orchestrator error or role error)
    -- archived (GC swept — agent row also soft-deleted)
    status TEXT NOT NULL DEFAULT 'created'
        CHECK (status IN ('created','ready','running','idle','completed','failed','archived')),
    -- Orchestrator's idle-detection signal. The orchestrator marks a
    -- role 'idle' if no agent_task_queue row has progressed in 5 min
    -- (mirrors Mythos supervise 30s tick + 24h cap, but per-role).
    last_heartbeat_at TIMESTAMPTZ,
    -- Free-form human-readable step description (e.g. "drafting
    -- module spec"); surfaced in the topology graph tooltip.
    current_step TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_swarm_role_by_run
    ON swarm_role(swarm_run_id, status);
CREATE INDEX idx_swarm_role_by_agent
    ON swarm_role(agent_id);
-- Orchestrator's "what's ready to claim" query.
CREATE INDEX idx_swarm_role_ready
    ON swarm_role(swarm_run_id)
    WHERE status IN ('ready','running');

-- swarm_role_message: inter-role + role↔human messages.
-- NULL from_role_id = human-originated (via MUL-4304 reconcile).
-- NULL to_role_id = broadcast (orchestrator picks up).
CREATE TABLE swarm_role_message (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    swarm_run_id UUID NOT NULL REFERENCES swarm_run(id) ON DELETE CASCADE,
    from_role_id UUID REFERENCES swarm_role(id),
    to_role_id UUID REFERENCES swarm_role(id),
    -- instruction (leader → role)
    -- progress (role → broadcast: "drafted module X")
    -- request_help (role → leader: "stuck on Y")
    -- human_interrupt (user → leader: comment w/ @swarm_coordinator)
    -- completion (role → broadcast: done)
    -- error (role → leader: failed)
    content TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN (
        'instruction','progress','request_help','human_interrupt','completion','error'
    )),
    -- [{role_id, read_at}] for per-role read state. Lets the leader
    -- track which roles have seen which broadcasts (avoids the
    -- "everyone says done before anyone heard" race).
    read_by JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_swarm_role_message_by_run
    ON swarm_role_message(swarm_run_id, created_at DESC);

-- swarm_interrupt: user-initiated interrupt audit log.
-- Distinct from swarm_role_message.human_interrupt (which is a
-- per-event message); this table is the canonical "what did the user
-- tell the swarm to do" trail for retrospective analysis.
CREATE TABLE swarm_interrupt (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    swarm_run_id UUID NOT NULL REFERENCES swarm_run(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id),
    -- pause (orchestrator stops assigning new agent_task_queue rows
    --        but lets in-flight roles complete)
    -- cancel (orchestrator cancels all agent_task_queue rows + sets
    --         swarm_run.status='aborted' immediately)
    -- redirect (user supplies new instructions via payload; leader
    --           re-prompts on next tick)
    -- inject_message (user comment w/ @swarm_coordinator; recorded
    --                  here for audit + mirrors in swarm_role_message
    --                  for the leader's working memory)
    kind TEXT NOT NULL CHECK (kind IN ('pause','cancel','redirect','inject_message')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_swarm_interrupt_by_run
    ON swarm_interrupt(swarm_run_id, created_at DESC);

-- experimental_resource_lock CHECK widened to include swarm_topology.
-- Without this widening, the swarm install handler's INSERT into
-- experimental_resource_lock would reject the swarm_topology source
-- value. Mirrors mig 149's mythos_swarm widening + mig 154's
-- claude_science_lab widening.
--
-- Full list is preserved (claude_science / mythos_swarm / swarm_topology
-- / agent_self_optimization / agent_creation_studio / constitution_agent)
-- even though agent_self_optimization + agent_creation_studio +
-- constitution_agent are retired (0.5.6 / 0.3.57) — per CLAUDE.md
-- "experimental_resource_lock CHECK constraints outlive retired flags —
-- and that's expected", the CHECK is forward-only.
ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check,
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
        CHECK (experimental_source IN (
            'claude_science','mythos_swarm','swarm_topology',
            'agent_self_optimization','agent_creation_studio','constitution_agent'
        ));