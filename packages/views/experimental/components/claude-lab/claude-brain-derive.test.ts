/**
 * @vitest-environment node
 */
import { describe, expect, it } from "vitest";
import type { Agent, AgentTask } from "@multica/core/types/agent";
import {
  BRAIN_PHASE_COUNT,
  CLAUDE_LAB_ROSTER,
  deriveBrainNodes,
  deriveBrainPhase,
  deriveRecentRuns,
} from "./claude-brain-derive";

function agent(name: string, id = `agent-${name}`): Agent {
  return {
    id,
    name,
    description: `${name} description`,
  } as unknown as Agent;
}

function task(
  agentId: string,
  status: AgentTask["status"],
  over: Partial<AgentTask> = {},
): AgentTask {
  return {
    id: `task-${agentId}-${status}`,
    agent_id: agentId,
    status,
    created_at: "2026-10-03T10:00:00Z",
    started_at: "2026-10-03T10:00:02Z",
    completed_at: null,
    error: null,
    trigger_summary: "trigger",
    ...over,
  } as unknown as AgentTask;
}

describe("deriveBrainNodes — roster × AgentTaskSnapshot mapping", () => {
  it("always returns the six roster slots in roster order", () => {
    const nodes = deriveBrainNodes([], []);
    expect(nodes.map((n) => n.key)).toEqual(CLAUDE_LAB_ROSTER.map((r) => r.key));
    expect(nodes.every((n) => n.state === "idle" && n.agentId === null)).toBe(true);
  });

  it("maps a running task on the research agent to its roster node", () => {
    const agents = [agent("research", "r-1"), agent("ml", "r-2")];
    const tasks = [task("r-1", "running")];
    const nodes = deriveBrainNodes(tasks, agents);
    const byKey = Object.fromEntries(nodes.map((n) => [n.key, n]));
    expect(byKey.research!.state).toBe("running");
    expect(byKey.research!.agentId).toBe("r-1");
    // agent exists but has no task on this issue → idle, still resolved
    expect(byKey.ml!.state).toBe("idle");
    expect(byKey.ml!.agentId).toBe("r-2");
    // agent row absent (lab rolled back) → unresolved + idle
    expect(byKey.critique!.agentId).toBeNull();
    expect(byKey.critique!.state).toBe("idle");
  });

  it("folds every active/terminal task status into a node state", () => {
    // one status per roster slot (six) — a 7th task would out-range the
    // roster and silently test the unresolved-agent branch instead
    const cases: Array<[AgentTask["status"], string]> = [
      ["queued", "queued"],
      ["waiting_local_directory", "queued"],
      ["running", "running"],
      ["completed", "done"],
      ["failed", "failed"],
      ["cancelled", "cancelled"],
    ];
    const agents = CLAUDE_LAB_ROSTER.map((r, i) => agent(r.key, `a-${i}`));
    const tasks = cases.map(([status], i) => task(`a-${i}`, status));
    const states = deriveBrainNodes(tasks, agents).map((n) => n.state);
    expect(states).toEqual(cases.map(([, nodeState]) => nodeState));
    // dispatched folds to queued like the other pre-run holds
    const dispatched = deriveBrainNodes([task("a-0", "dispatched")], [agent("research", "a-0")]);
    expect(dispatched[0]!.state).toBe("queued");
  });
});

describe("deriveBrainPhase — role → pipeline index", () => {
  const agents = CLAUDE_LAB_ROSTER.map((r, i) => agent(r.key, `a-${i}`));
  const phaseFor = (key: string, taskStatus: AgentTask["status"], overall: string) =>
    deriveBrainPhase(
      deriveBrainNodes([task(`a-${CLAUDE_LAB_ROSTER.findIndex((r) => r.key === key)}`, taskStatus)], agents),
      overall,
    );

  it("running: research → literature search, domain experts → reproduction, critique → review, write → report", () => {
    expect(phaseFor("research", "running", "running")).toBe(1);
    expect(phaseFor("ml", "running", "running")).toBe(2);
    expect(phaseFor("physics", "running", "running")).toBe(2);
    expect(phaseFor("biology", "running", "running")).toBe(2);
    expect(phaseFor("critique", "running", "running")).toBe(3);
    expect(phaseFor("write", "running", "running")).toBe(4);
  });

  it("queued parks on phase 0, completed saturates the pipeline, idle clears it", () => {
    expect(phaseFor("research", "queued", "queued")).toBe(0);
    expect(phaseFor("write", "completed", "completed")).toBe(BRAIN_PHASE_COUNT - 1);
    expect(deriveBrainPhase(deriveBrainNodes([], []), "idle")).toBe(-1);
  });

  it("a failed run freezes the pipeline on the failed role's phase", () => {
    expect(phaseFor("ml", "failed", "failed")).toBe(2);
    expect(phaseFor("critique", "cancelled", "cancelled")).toBe(3);
  });
});

describe("deriveRecentRuns — workbench history strip", () => {
  it("lists terminal runs newest-first with duration, skipping active tasks", () => {
    const agents = [agent("research", "r"), agent("write", "w")];
    const t1 = {
      ...task("r", "completed"),
      completed_at: "2026-10-03T10:05:00Z", // 298s run
    } as AgentTask;
    const t2 = {
      ...task("w", "failed", { error: "boom" }),
      completed_at: "2026-10-03T11:00:00Z",
    } as AgentTask;
    const t3 = task("r", "running"); // active — excluded
    const runs = deriveRecentRuns([t2, t3, t1], agents);
    expect(runs.map((r) => r.key)).toEqual(["write", "research"]);
    expect(runs[0]!.state).toBe("failed");
    expect(runs[0]!.error).toBe("boom");
    expect(runs[1]!.durationMs).toBe(298_000);
  });

  it("falls back to the raw agent id when the agent row is missing", () => {
    const orphan = task("ghost-id", "completed");
    expect(deriveRecentRuns([orphan], [])[0]!.key).toBe("ghost-id");
  });
});
