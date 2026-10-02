"use client";

// use-claude-lab-issue — the single subscription source for the issue-
// first Claude Lab surfaces (0.5.114): header pill + main-pane embed,
// mirroring the usePythiaIssueLab contract (Active Contract #10 shape,
// pythia analogue). Unlike pythia there is no SSE bus: the run
// lifecycle IS the standard agent task queue, so the workspace
// AgentTaskSnapshot (5s poll, Active Contract #1) is the stream, and
// the by-issue artifact listing rides the same live/idle cadence.
// Both query keys are workspace-shared — pill, embed, and the existing
// LabProgressCard amortise the same round-trips.

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { api } from "@multica/core/api";
import type { AgentTask } from "@multica/core/types/agent";

export type ClaudeLabRunStatus =
  | "running"
  | "queued"
  | "completed"
  | "failed"
  | "cancelled"
  | "idle";

const ACTIVE_STATES = new Set(["running", "queued", "dispatched", "waiting_local_directory"]);

function taskTime(t: AgentTask): number {
  return Date.parse(t.completed_at ?? t.started_at ?? t.created_at ?? "") || 0;
}

export function useClaudeLabIssue(wsId: string, issueId: string) {
  const snapshotQuery = useQuery(agentTaskSnapshotOptions(wsId));
  const { data: snapshot = [] } = snapshotQuery;

  const tasks = useMemo(
    () => snapshot.filter((task) => task.issue_id === issueId),
    [snapshot, issueId],
  );

  const liveTask = useMemo<AgentTask | null>(() => {
    let best: AgentTask | null = null;
    for (const task of tasks) {
      if (task.status !== "running" && task.status !== "waiting_local_directory") continue;
      if (!best || taskTime(task) > taskTime(best)) best = task;
    }
    return best;
  }, [tasks]);

  const queued = useMemo(
    () => tasks.some((t) => t.status === "queued" || t.status === "dispatched"),
    [tasks],
  );

  // Latest task by terminal/started time — the pill keeps the last
  // outcome visible after the run lands (snapshot retains each agent's
  // most recent terminal task).
  const latest = useMemo<AgentTask | null>(() => {
    let best: AgentTask | null = null;
    for (const task of tasks) {
      if (ACTIVE_STATES.has(task.status)) continue;
      if (!best || taskTime(task) > taskTime(best)) best = task;
    }
    return best;
  }, [tasks]);

  const status: ClaudeLabRunStatus = liveTask
    ? "running"
    : queued
      ? "queued"
      : latest
        ? (latest.status as ClaudeLabRunStatus)
        : "idle";

  const artifacts = useQuery({
    queryKey: ["claude-lab", "artifacts", wsId, issueId],
    queryFn: () => api.listClaudeScienceArtifactsByIssue(issueId, wsId),
    enabled: Boolean(wsId && issueId),
    staleTime: 30_000,
    refetchInterval: status === "running" || status === "queued" ? 5_000 : 60_000,
  });

  return {
    tasks,
    latest,
    liveTask,
    status,
    hasLive: status === "running" || status === "queued",
    artifacts: artifacts.data ?? [],
    // 0.5.131: a failed snapshot/artifacts fetch used to render
    // identically to "no runs" (the pill vanished, the embed showed the
    // empty hint). Surface the outage so the embed can offer a retry.
    isError: snapshotQuery.isError || artifacts.isError,
    refetch: () => {
      void snapshotQuery.refetch();
      void artifacts.refetch();
    },
  };
}
