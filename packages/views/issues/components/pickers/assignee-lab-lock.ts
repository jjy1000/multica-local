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
