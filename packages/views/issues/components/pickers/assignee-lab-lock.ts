import type { ExperimentalFlag } from "@multica/core/types/experimental";

/**
 * 0.5.86 assignee-lock (独立工作型) — client mirror of the server's
 * `assigneeLabLockError` gate (server/internal/handler/issue.go).
 *
 * Labs classified `interaction_model: "assignee"` own the bound
 * issue's assignee slot: the AssigneePicker locks and picking the lab
 * clears any manual assignee (the server's leader-rewrite then fills
 * the lab leader). `auxiliary` labs (causal_graph, llm_wiki_bridge)
 * never lock. Unclassified flags keep the legacy coexist behavior.
 *
 * Enhancer mode always unlocks — the target assignee is the very
 * field enhancer exists to populate.
 *
 * Legacy fallback: a server predating `interaction_model` omits the
 * field entirely; the 0.3.33 hardcoded mutex pair
 * (mythos_swarm / swarm_topology) still applied there, so the lock
 * stays on for those two keys when the field is absent.
 */
export function isAssigneeLabLocked(
  flags: ExperimentalFlag[] | undefined | null,
  labSource: string | null | undefined,
  labMode?: string | null,
): boolean {
  if (!labSource || labMode === "enhancer") return false;
  const flag = flags?.find((f) => f.key === labSource);
  if (!flag) return false;
  if (flag.interaction_model === "assignee") return true;
  if (flag.interaction_model === "auxiliary") return false;
  // Legacy server (no interaction_model in the payload): keep the
  // 0.3.33 hardcoded mutex behavior.
  return labSource === "mythos_swarm" || labSource === "swarm_topology";
}

/**
 * 0.5.86: detect the server's assignee-lab-lock 400 (issue create/update)
 * in a thrown error and extract its structured parts, so the renderer can
 * show a dedicated guided toast instead of the raw English message.
 *
 * Stable-substring match on the three deterministic messages emitted by
 * `assigneeLabLockError` (server/internal/handler/issue.go):
 *   1. "lab_source=<key> requires the lab to own the assignee (...)"
 *      — leader not resolvable from the roster table.
 *   2. "lab_source=<key> locks the assignee to the lab agent (<leader>); ..."
 *      — non-agent assignee, or a different agent than the leader.
 *   3. "lab_source=<key> locks the assignee to its lab agent (<leader>),
 *      but that agent is not installed yet; ..."
 *      — leader name known but the agent row is missing.
 *
 * Returns null for any other error.
 */
export interface AssigneeLabLockErrorInfo {
  /** Lab flag key parsed from the message (`lab_source=<key>`). */
  labSource: string;
  /** Leader agent name when the message carries one, else null. */
  leaderName: string | null;
  /**
   * true  = leader row exists (case 2),
   * false = leader name known but agent not installed (case 3),
   * null  = no resolvable leader at all (case 1).
   */
  leaderInstalled: boolean | null;
}

export function matchAssigneeLabLockError(err: unknown): AssigneeLabLockErrorInfo | null {
  const message =
    err instanceof Error
      ? err.message
      : typeof err === "string"
        ? err
        : "";
  if (!message) return null;

  // Case 1 — no leader resolvable.
  const requires = message.match(
    /lab_source=([A-Za-z0-9_]+) requires the lab to own the assignee/,
  );
  if (requires) {
    return { labSource: requires[1]!, leaderName: null, leaderInstalled: null };
  }

  // Cases 2 & 3 — leader named in the message.
  const locks = message.match(
    /lab_source=([A-Za-z0-9_]+) locks the assignee to (?:the|its) lab agent \(([^)]*)\)/,
  );
  if (locks) {
    return {
      labSource: locks[1]!,
      leaderName: locks[2] || null,
      // Case 3's "not installed yet" clause is the only divergence.
      leaderInstalled: !message.includes("not installed yet"),
    };
  }

  return null;
}

/**
 * Display label for a lab flag key: localized title when the flag row is
 * in the payload, the raw key otherwise. Mirrors the `title.zh || title.en`
 * convention used across the issue-detail surfaces.
 */
export function labLockLabel(
  flags: ExperimentalFlag[] | undefined | null,
  labSource: string | null | undefined,
): string {
  if (!labSource) return "";
  const flag = flags?.find((f) => f.key === labSource);
  return flag ? flag.title.zh || flag.title.en || labSource : labSource;
}
