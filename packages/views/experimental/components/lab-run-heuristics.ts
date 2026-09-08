// Lab run "is it working right now" heuristics (0.5.86 lab-progress-card).
//
// The forecast labs (pythia_oracle, timesfm) have NO status column on their
// run rows — a persisted row only appears once the synchronous engine call
// resolves (timesfm_forecast.go persists AFTER the engine answers; pythia's
// SSE loop writes the row when the deliberation completes). "Running" can
// therefore only be derived from side-channel signals:
//
//   - pythia: a sessionStorage trigger timestamp written by LabOutputPanel's
//     trigger mutation (0.5.59). Fresh trigger + no rows yet → in progress;
//     stale trigger + still no rows → stuck (engine likely down).
//   - timesfm: no trigger timestamp exists anywhere (the lab view POSTs and
//     awaits), so the only honest signal is recency of the latest persisted
//     row — a run younger than the window means the lab is actively being
//     used on this issue.
//
// Extracted from LabOutputPanel's PythiaPanel (0.5.59 inline logic) so the
// issue-side LabProgressCard reads the SAME signal instead of duplicating
// the constants — a drift between the two surfaces would render
// contradictory states for the same run.

/** Pythia: a real LLM round takes 25–60s through the bridge (0.5.104
 *  live measurement) and the default deliberation is 3 rounds, so the
 *  trigger window has to span ~3 minutes. 5 minutes total before
 *  "triggered but nothing landed" flips to stuck. (Was 90s for the
 *  retired near-instant engine path.) */
export const PYTHIA_IN_PROGRESS_WINDOW_MS = 300_000;

/** Timesfm: a persisted run younger than this reads as "just ran" — the
 *  lab was active on this issue within the last two minutes. */
export const TIMESFM_RECENT_RUN_WINDOW_MS = 120_000;

/** sessionStorage key for the pythia trigger timestamp. MUST stay
 *  byte-identical to the 0.5.59 key — older tabs wrote under this name. */
export function pythiaTriggerKey(wsId: string, issueId: string): string {
  return `pythia-triggered-${wsId}-${issueId}`;
}

/** Read the last pythia trigger timestamp for this workspace+issue pair,
 *  or null when absent/unparseable (SSR, fresh session, corrupted entry). */
export function readPythiaTriggeredAt(
  wsId: string,
  issueId: string,
): number | null {
  if (typeof window === "undefined") return null;
  const raw = window.sessionStorage.getItem(pythiaTriggerKey(wsId, issueId));
  const n = raw ? Number.parseInt(raw, 10) : NaN;
  return Number.isFinite(n) ? n : null;
}

/** Record "a forecast was just triggered". Fire-and-forget — callers then
 *  re-read the timestamp. */
export function markPythiaTriggered(
  wsId: string,
  issueId: string,
  now: number = Date.now(),
): void {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(pythiaTriggerKey(wsId, issueId), String(now));
}

/** Clear the trigger timestamp — used when the user explicitly cancels an
 *  in-flight forecast (0.5.104). Without this the "推演中..." heuristic
 *  keeps spinning for its full 90s window after the run was already
 *  stopped. */
export function clearPythiaTriggered(wsId: string, issueId: string): void {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(pythiaTriggerKey(wsId, issueId));
}

export type PythiaTriggerState = "idle" | "in_progress" | "stuck";

/** Derive the trigger-side state. `hasRuns` short-circuits to idle — once
 *  rows arrive the timestamp is ignored (we have data). */
export function derivePythiaTriggerState(
  triggeredAt: number | null,
  hasRuns: boolean,
  now: number = Date.now(),
): PythiaTriggerState {
  if (triggeredAt == null || hasRuns) return "idle";
  if (now - triggeredAt < PYTHIA_IN_PROGRESS_WINDOW_MS) return "in_progress";
  return "stuck";
}

/** Timesfm recency: true when the latest persisted run was created within
 *  the recent-run window. Rows carry RFC3339 `created_at` (server formats
 *  with time.RFC3339); an unparseable/absent timestamp is never recent. */
export function isTimesfmRunRecent(
  createdAt: string | null | undefined,
  now: number = Date.now(),
): boolean {
  if (!createdAt) return false;
  const t = Date.parse(createdAt);
  if (!Number.isFinite(t)) return false;
  return now - t < TIMESFM_RECENT_RUN_WINDOW_MS && now - t >= -60_000;
}
