"use client";

// LabRunLink — shared "view full record in lab" helper (0.5.81, ICP-3).
//
// Two pieces:
//   1. labRunHref(labSource, issueId, runId) → returns the canonical URL
//      for jumping into the lab view with a specific run pre-scoped, or
//      `undefined` when the lab has no dedicated view (LLM Wiki / Pythia
//      shell scenarios). Built on top of labSourceRouteSuffix so user_*
//      plugin keys route to /experimental/plugin/<slug>?issue=...&run=...
//      and built-in labs route to their FLAG_ROUTE_SUFFIX-mapped paths.
//   2. <LabRunLink flagKey issueId runId /> renders the URL as an AppLink
//      with the standard "在实验室查看完整记录" affordance. Honours the
//      locale strings (4-locale parity maintained in this commit).
//
// Each receiving lab view's history list reads `?run=<id>` from the
// search params (via useSearchParams) and uses it to scroll/highlight
// the matching record. Wired scope (this commit):
//   - pythia-view, mythos-view, code-canvas-view, plugin-shell-view
//   - swarm-topology-view (already accepted ?issue= + ?run= via
//     PastRunsPanel; the search-param hook stays compatible).
//
// Out of scope for this commit (other labs follow their panel
// availability in future work):
//   - semantica-explorer-view (no persisted runs today)
//   - llm-wiki-bridge-view (workspace-level, no per-task runs)
//
// Click target semantics (matches 0.5.80 release-guard contract):
//   useNavigation().push() inherits the workspace-singleton release
//   guard arming from the navigation adapter — navigating from an
//   issue detail page into a lab view cannot accidentally trigger
//   the workspace singleton release.

import { ExternalLink } from "lucide-react";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { labSourceRouteSuffix } from "../../issues/components/issue-labs-section";

/**
 * Returns the canonical `/experimental/<suffix>?issue=<id>&run=<runId>` URL
 * for a given `lab_source` + bound issue + run id, or `undefined` if the
 * lab has no dedicated view (lab source null, unknown flag key, or
 * legacy retired key without a suffix entry).
 *
 * Public so other consumers (IssueLabsSection "open panel" link,
 * comment-thread jumps, completion notifications) can build the same
 * URL without re-deriving the route map.
 */
export function labRunHref(
  labSource: string | null | undefined,
  issueId: string,
  runId: string,
): string | undefined {
  const suffix = labSourceRouteSuffix(labSource);
  if (!suffix) return undefined;
  const params = new URLSearchParams();
  params.set("issue", issueId);
  params.set("run", runId);
  return `/experimental/${suffix}?${params.toString()}`;
}

export interface LabRunLinkProps {
  /** Lab source key (e.g. "claude_science_lab", "user_my_plugin"). */
  flagKey: string | null | undefined;
  /** Bound issue id — must match ?issue= on the receiving lab view. */
  issueId: string;
  /** Run id (task id for AgentTask-based labs; lab-specific id for
   *  Pythia forecast runs / Mythos swarm runs / etc.). */
  runId: string;
  /** Override the link label — defaults to "在实验室查看完整记录" / i18n
   *  equivalent. */
  label?: string;
  /** Override className (the base styling is the standard small purple
   *  jump-link used elsewhere on this surface). */
  className?: string;
}

/**
 * Renders an AppLink to the lab view pre-scoped to the bound issue +
 * run id. Returns `null` when the lab has no dedicated view (so
 * callers can drop it in unconditionally).
 */
export function LabRunLink({
  flagKey,
  issueId,
  runId,
  label,
  className,
}: LabRunLinkProps) {
  const { t } = useT("experimental");
  const href = labRunHref(flagKey, issueId, runId);
  if (!href) return null;
  const text = label ?? t(($) => $.lab_output_panel.run_link_view_in_lab);
  return (
    <AppLink
      href={href}
      className={
        "inline-flex items-center gap-1 text-caption text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300 transition-colors " +
        (className ?? "")
      }
    >
      <ExternalLink className="size-3" aria-hidden />
      {text}
    </AppLink>
  );
}