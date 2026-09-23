// Shared trigger + cancel control for issue-bound Pythia forecast runs
// (0.5.104, async since 0.5.111).
//
// Before 0.5.111 the POST held an SSE loop and cancel meant aborting the
// fetch. The server now runs the deliberation in a DETACHED background
// goroutine: the POST returns {run_id} immediately, rounds stream through
// GET .../runs/{runID}/stream (consumed by use-pythia-issue-lab), and
// cancel is a POST to .../runs/{runID}/cancel — aborting the fetch would
// no longer stop anything.
//
// One in-flight run per workspace+issue is still enforced client-side:
// starting a new run for the same issue best-effort cancels the previous
// one (stacking two full-council runs double-spends LLM rounds and
// interleaves two report comments, which is never what the user meant).

import { api, parseWithFallback } from "@multica/core/api";
import { PythiaForecastStartSchema } from "@multica/core/api/schemas";
import type { PythiaForecastStart } from "@multica/core/types/api";
import {
  clearPythiaTriggered,
  markPythiaTriggered,
} from "../../experimental/components/lab-run-heuristics";

const PYTHIA_FORECAST_ENDPOINT = "/api/experimental/pythia-oracle/forecast/issue";

export interface PythiaForecastStartInput {
  wsId: string;
  issueId: string;
  /** Explicit round count. Omit to let the server resolve
   *  natural-language pins ("推演5轮") or its per-kind default
   *  (initial 3 / continuation 6). */
  rounds?: number;
  /** User-injected continuation variables ("把汇率冲击调高到 20%…"). */
  variables?: string;
  /** Parent run id — turns the request into a CONTINUATION whose
   *  context = 原问题 + 父 run 轮次摘要 + variables. */
  parentRunId?: string;
}

// Last started run id per workspace+issue, so a fresh trigger can cancel
// the previous run server-side (the fetch-abort of 0.5.104 is gone —
// only the server can stop a detached run now).
const lastStartedRuns = new Map<string, string>();

function controllerKey(wsId: string, issueId: string): string {
  return `${wsId}:${issueId}`;
}

/**
 * Start a deliberation for the issue (initial or continuation). Stamps
 * the sessionStorage trigger first so heuristic surfaces (LabProgressCard)
 * flip into "推演中..." immediately, best-effort cancels the previous run
 * for the same issue, and resolves with the server's start payload.
 * Returns null on transport/4xx failure — callers surface that through
 * their own error UI; the runs list shows what actually landed.
 */
export async function startPythiaIssueForecast(
  opts: PythiaForecastStartInput,
): Promise<PythiaForecastStart | null> {
  const { wsId, issueId, rounds, variables, parentRunId } = opts;
  markPythiaTriggered(wsId, issueId);

  const key = controllerKey(wsId, issueId);
  const previous = lastStartedRuns.get(key);
  if (previous && previous !== parentRunId) {
    void cancelPythiaRunById(previous).catch(() => {});
  }

  const body: Record<string, unknown> = { issue_id: issueId };
  if (typeof rounds === "number" && rounds > 0) body.rounds = rounds;
  if (variables && variables.trim()) body.variables = variables.trim();
  if (parentRunId) body.parent_run_id = parentRunId;

  try {
    const r = await api.rawRequest(PYTHIA_FORECAST_ENDPOINT, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!r.ok) return null;
    const raw: unknown = await r.json();
    const start = parseWithFallback<PythiaForecastStart>(
      raw,
      PythiaForecastStartSchema,
      { run_id: "", rounds: 0, status: "running", run_kind: "initial" },
      { endpoint: "POST /api/experimental/pythia-oracle/forecast/issue" },
    );
    if (!start.run_id) return null;
    lastStartedRuns.set(key, start.run_id);
    return start;
  } catch {
    return null;
  }
}

/**
 * Cancel the in-flight forecast for this issue, if we know its run id.
 * Also clears the sessionStorage trigger so the "推演中..." heuristic
 * doesn't keep spinning after the user explicitly stopped the run.
 * Returns true when a cancel request was actually sent.
 */
export function cancelPythiaIssueForecast(
  wsId: string,
  issueId: string,
  runId?: string | null,
): boolean {
  const key = controllerKey(wsId, issueId);
  clearPythiaTriggered(wsId, issueId);
  const target = runId ?? lastStartedRuns.get(key);
  if (!target) return false;
  lastStartedRuns.delete(key);
  void cancelPythiaRunById(target).catch(() => {});
  return true;
}

/** POST .../runs/{runId}/cancel — best-effort, never throws. */
async function cancelPythiaRunById(runId: string): Promise<void> {
  await api.rawRequest(
    `/api/experimental/pythia-oracle/forecast/issue/runs/${encodeURIComponent(runId)}/cancel`,
    { method: "POST" },
  );
}
