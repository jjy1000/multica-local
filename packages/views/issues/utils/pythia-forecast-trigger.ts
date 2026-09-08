// Shared trigger + cancel control for issue-bound Pythia forecast runs
// (0.5.104).
//
// Before this module the three auto-launch sites (create-issue, the
// update-path lab-parity branch in use-issue-actions, and PythiaPanel's
// retry/start buttons) each fired their own fire-and-forget rawRequest
// with no AbortController — once a deliberation started there was NO
// way to stop it from the UI, and the run looked unstoppable until it
// finished on its own. The 0.5.103 stop button in IssueLabsSection only
// covers agent-task-backed labs; the pythia forecast never creates an
// agent task row (AutoDispatch=false), so that button never appears for
// it.
//
// The server side already honours client disconnects — the SSE round
// loop selects on r.Context().Done() between rounds and the deferred
// persist writes whatever rounds actually landed — so aborting the
// fetch here stops the burn end-to-end and keeps the partial result.

import { api } from "@multica/core/api";
import {
  clearPythiaTriggered,
  markPythiaTriggered,
} from "../../experimental/components/lab-run-heuristics";

const PYTHIA_FORECAST_ENDPOINT = "/api/experimental/pythia-oracle/forecast/issue";

// One in-flight forecast per workspace+issue. A fresh trigger for the
// same issue aborts the previous run — stacking a second SSE loop on
// top of the first double-spends rounds and interleaves two report
// comments, which is never what the user meant.
const activeControllers = new Map<string, AbortController>();

function controllerKey(wsId: string, issueId: string): string {
  return `${wsId}:${issueId}`;
}

/**
 * Fire-and-forget a deliberation for the issue. Stamps the sessionStorage
 * trigger first so every mounted surface (PythiaPanel, LabProgressCard)
 * flips into "推演中..." immediately. Never throws — a forecast failure
 * must not surface as a create/update error; the runs poll shows what
 * actually landed.
 */
export function startPythiaIssueForecast(opts: {
  wsId: string;
  issueId: string;
  rounds: number;
}): void {
  const { wsId, issueId, rounds } = opts;
  markPythiaTriggered(wsId, issueId);

  const key = controllerKey(wsId, issueId);
  activeControllers.get(key)?.abort();
  const controller = new AbortController();
  activeControllers.set(key, controller);

  void api
    .rawRequest(PYTHIA_FORECAST_ENDPOINT, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ issue_id: issueId, rounds }),
      signal: controller.signal,
    })
    .catch(() => {
      // Aborted (user cancel) or transport failure — both are surfaced by
      // the runs poll (partial rounds persist server-side on abort), not
      // by toasts here.
    })
    .finally(() => {
      if (activeControllers.get(key) === controller) {
        activeControllers.delete(key);
      }
    });
}

/**
 * Abort the in-flight forecast for this issue, if any. Also clears the
 * sessionStorage trigger so the "推演中..." heuristic doesn't keep
 * spinning for its 90s window after the user explicitly stopped the
 * run. Returns true when a run was actually aborted.
 */
export function cancelPythiaIssueForecast(wsId: string, issueId: string): boolean {
  const key = controllerKey(wsId, issueId);
  const controller = activeControllers.get(key);
  clearPythiaTriggered(wsId, issueId);
  if (!controller) return false;
  controller.abort();
  return true;
}

/**
 * Register an externally-owned controller (PythiaPanel's useMutation
 * path) so the shared cancel button can abort it too.
 */
export function registerPythiaForecastController(
  wsId: string,
  issueId: string,
  controller: AbortController,
): void {
  const key = controllerKey(wsId, issueId);
  activeControllers.get(key)?.abort();
  activeControllers.set(key, controller);
}

export function unregisterPythiaForecastController(
  wsId: string,
  issueId: string,
  controller: AbortController,
): void {
  const key = controllerKey(wsId, issueId);
  if (activeControllers.get(key) === controller) {
    activeControllers.delete(key);
  }
}
