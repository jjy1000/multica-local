// claude-brain-derive — pure derivation layer for the Claude Lab brain
// canvas (0.5.132). Maps the issue's AgentTask snapshot rows onto the
// installed lab roster so the neural view shows REAL per-agent state,
// never fabricated progress.
//
// Roster keys are the install-bundle agent names
// (apps/desktop/resources/claude-science/manifest.json: research /
// critique / ml / physics / biology / write). The workspace agents list
// (include_archived) carries the id ↔ name binding; lab agents are
// lab_managed-hidden from selection surfaces but still listed, which is
// exactly what the brain needs.
//
// Phase mapping is a PRESENTATION of which roster role is executing —
// AgentTask has no server-side progress field. Anything richer (per-phase
// completion events) needs a backend contract first.

import type { Agent, AgentTask } from "@multica/core/types/agent";

export type BrainNodeState =
  | "idle"
  | "queued"
  | "running"
  | "done"
  | "failed"
  | "cancelled";

export interface ClaudeBrainRosterEntry {
  /** Install-bundle agent name — also the i18n key suffix. */
  key: string;
  category: "primary" | "domain" | "subagent";
  /** Orbit position in degrees (0 = right, -90 = top). */
  angle: number;
}

export const CLAUDE_LAB_ROSTER: ClaudeBrainRosterEntry[] = [
  { key: "research", category: "primary", angle: -90 },
  { key: "critique", category: "primary", angle: -28 },
  { key: "ml", category: "domain", angle: 34 },
  { key: "physics", category: "domain", angle: 94 },
  { key: "biology", category: "domain", angle: 152 },
  { key: "write", category: "subagent", angle: 212 },
];

const TASK_STATE: Record<AgentTask["status"], BrainNodeState> = {
  queued: "queued",
  dispatched: "queued",
  waiting_local_directory: "queued",
  running: "running",
  completed: "done",
  failed: "failed",
  cancelled: "cancelled",
};

export interface ClaudeBrainNode {
  key: string;
  category: ClaudeBrainRosterEntry["category"];
  /** Resolved workspace agent id; null when the lab roster row is absent. */
  agentId: string | null;
  agentDescription: string;
  state: BrainNodeState;
  /** The agent's most recent task on this issue (null → idle). */
  task: AgentTask | null;
}

export function deriveBrainNodes(
  tasks: AgentTask[],
  agents: Agent[],
): ClaudeBrainNode[] {
  return CLAUDE_LAB_ROSTER.map((entry) => {
    const agent = agents.find((a) => a.name === entry.key) ?? null;
    const task = agent
      ? (tasks.find((t) => t.agent_id === agent.id) ?? null)
      : null;
    return {
      key: entry.key,
      category: entry.category,
      agentId: agent?.id ?? null,
      agentDescription: agent?.description ?? "",
      // Unknown future statuses degrade to idle, not a crash (API-compat
      // default-branch law).
      state: task ? (TASK_STATE[task.status] ?? "idle") : "idle",
      task,
    };
  });
}

export const BRAIN_PHASE_COUNT = 5;

/** role key → pipeline index (see header comment for the honesty note). */
const ROLE_PHASE: Record<string, number> = {
  research: 1,
  ml: 2,
  physics: 2,
  biology: 2,
  critique: 3,
  write: 4,
};

/**
 * -1 no pipeline yet · 0 问题解析 (queued) · 1 文献检索 (research) ·
 * 2 实验复现 (domain experts) · 3 同行评审 (critique) · 4 报告撰写
 * (write / run completed). Failed/cancelled runs freeze the pipeline on
 * the phase whose role failed.
 */
export function deriveBrainPhase(
  nodes: ClaudeBrainNode[],
  status: string,
): number {
  if (status === "running") {
    const running = nodes.find((n) => n.state === "running");
    // running without a roster role (task agent unresolvable) still
    // means work started — park on 问题解析.
    return running ? (ROLE_PHASE[running.key] ?? 0) : 0;
  }
  if (status === "queued") return 0;
  if (status === "completed") return BRAIN_PHASE_COUNT - 1;
  if (status === "failed" || status === "cancelled") {
    const stuck = nodes.find((n) => n.state === "failed" || n.state === "cancelled");
    return stuck ? (ROLE_PHASE[stuck.key] ?? 0) : -1;
  }
  return -1;
}

/** Terminal runs for the workbench history strip, newest first. */
export function deriveRecentRuns(
  tasks: AgentTask[],
  agents: Agent[],
  limit = 4,
): Array<{
  key: string;
  state: BrainNodeState;
  startedAt: string | null;
  completedAt: string | null;
  durationMs: number | null;
  error: string | null;
}> {
  const byId = new Map(agents.map((a) => [a.id, a.name] as const));
  const terminal = tasks.filter((t) =>
    t.status === "completed" || t.status === "failed" || t.status === "cancelled",
  );
  const time = (t: AgentTask) =>
    Date.parse(t.completed_at ?? t.started_at ?? t.created_at ?? "") || 0;
  return terminal
    .sort((a, b) => time(b) - time(a))
    .slice(0, limit)
    .map((t) => ({
      key: byId.get(t.agent_id) ?? t.agent_id,
      state: TASK_STATE[t.status] ?? "idle",
      startedAt: t.started_at,
      completedAt: t.completed_at,
      durationMs:
        t.started_at && t.completed_at
          ? Math.max(0, Date.parse(t.completed_at) - Date.parse(t.started_at))
          : null,
      error: t.error,
    }));
}
