-- 292_causal_graph_read_receipt.up.sql
-- 0.5.121 (知识图谱决策追溯自动系统): read receipts — WHO has read the
-- causal graph of an issue. The claim-time briefing
-- (service/causal_graph/claim_brief.go → handler/daemon.go) is the
-- primary "an agent is reading this trace" signal; each injection of a
-- non-empty brief records one row so the issue-side graph preview can
-- show "these agents have been tracing this issue's causal analysis"
-- (the user-facing ask: 知晓当前任务 agent 正在追溯和读取因果分析).
--
-- One row per read event (a resume re-claim is a fresh read and records
-- again); the listing endpoint caps at the most recent few. `source`
-- names the read channel ('claim_brief' today; the REST subgraph path
-- may join later). Forward-only additive — no existing objects touched.

CREATE TABLE IF NOT EXISTS causal_graph_read_receipt (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    agent_id UUID NULL REFERENCES agent(id) ON DELETE SET NULL,
    task_id UUID NULL,
    source TEXT NOT NULL DEFAULT 'claim_brief',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_causal_reads_issue_time
    ON causal_graph_read_receipt (issue_id, created_at DESC);
