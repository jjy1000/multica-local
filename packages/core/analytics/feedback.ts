/**
 * Feedback funnel instrumentation.
 *
 * Pairs with the backend's `feedback_submitted` event (emitted from
 * `CreateFeedback` after a successful insert) so we can compute a
 * completion rate: users who open the help-menu feedback entry →
 * users who actually send. The message content itself is never sent
 * to PostHog; see docs/analytics.md and the backend `FeedbackSubmitted`
 * helper for the PII contract.
 *
 * Note: in this fork analytics are no-ops (`captureEvent` writes to
 * the noop client), but the funnel probes are kept wired so the wiring
 * stays observable when telemetry is re-enabled.
 */

import { captureEvent } from "./index";

/**
 * Entry point the user took to reach the feedback surface. Typed union
 * so future surfaces (keyboard shortcut, error-toast CTA, sidebar menu
 * item) have to extend this list explicitly rather than drift.
 */
export type FeedbackOpenedSource = "help_menu";

/**
 * Fires once when the feedback entry point is opened. Workspace id is
 * attached when the entry point opens inside a workspace; pre-workspace
 * surfaces (e.g. inbox, onboarding transitions) omit it rather than
 * sending an empty string.
 */
export function captureFeedbackOpened(
  source: FeedbackOpenedSource,
  workspaceId?: string,
): void {
  captureEvent("feedback_opened", {
    source,
    ...(workspaceId ? { workspace_id: workspaceId } : {}),
  });
}
