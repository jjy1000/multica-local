import type { Label } from "./label";

/**
 * A status CATEGORY — the behavior equivalence class an issue's status belongs
 * to. There are exactly 7, and each is also the key of the built-in status that
 * defines it, which is why this stayed a closed union while `Issue.status`
 * became open. Board columns, filters and the presentation config are all keyed
 * off categories, so their shape is fixed no matter how many custom statuses a
 * workspace defines. (MUL-6243)
 */
export type IssueStatusCategory =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";

/**
 * A status KEY as stored on the issue: one of the 7 built-ins, or a custom key
 * an admin defined for this workspace.
 *
 * OPEN by design. `(string & {})` keeps editor autocomplete for the 7 built-ins
 * while accepting any catalog key, which is what the server has always been
 * able to send. Anything that needs presentation (label, colour, board column)
 * must resolve the key to its CATEGORY first — `useIssueStatuses(wsId)` in a
 * component, `statusCategoryOfKey` in a pure path. (MUL-6243)
 */
export type IssueStatus = IssueStatusCategory | (string & {});

export type IssuePriority = "urgent" | "high" | "medium" | "low" | "none";

export type IssueAssigneeType = "member" | "agent" | "squad";

export interface IssueReaction {
  id: string;
  issue_id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
  created_at: string;
}

/**
 * Per-issue metadata is a flat KV map agents use to record pipeline state
 * (PR number, pipeline_status, waiting_on, ...). Values are primitives only —
 * string / number / bool — enforced by both the API and the DB. Always
 * present in responses (empty object when unset) so reads don't need a
 * nil guard on the parent field.
 */
export type IssueMetadataValue = string | number | boolean;
export type IssueMetadata = Record<string, IssueMetadataValue>;

export interface Issue {
  id: string;
  workspace_id: string;
  number: number;
  identifier: string;
  title: string;
  description: string | null;
  status: IssueStatus;
  /**
   * The category `status` belongs to, when the endpoint resolved it. Optional
   * because a BUILT-IN status is its own category and needs no resolution —
   * use `issueStatusCategory(issue)` rather than reading this directly.
   * (MUL-6243)
   */
  status_category?: IssueStatusCategory;
  /**
   * A CUSTOM status's display name, carried beside the key. Empty for the 7
   * built-ins, which are localized from the key — prefer `useStatusLabel`,
   * which handles both and stays correct when an admin renames a status.
   *
   * Optional only for compatibility with a server that predates it; a current
   * server always sends the field. (MUL-6749)
   */
  status_name?: string;
  priority: IssuePriority;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  creator_type: IssueAssigneeType;
  creator_id: string;
  parent_issue_id: string | null;
  project_id: string | null;
  position: number;
  // Ordered barrier group among sibling sub-issues (null = unstaged). The
  // parent assignee is notified/woken only when every sub-issue in a stage
  // finishes; see server/internal/handler/issue_child_done.go.
  stage: number | null;
  // Experimental lab association — the flag key from the Labs catalog.
  // Null means the issue is not associated with any lab.
  lab_source?: string | null;
  // 0.3.31 — mythos_swarm dual-mode flag. Only meaningful when
  // lab_source === "mythos_swarm". Renderer uses this to swap the
  // IssueLabsSection chrome between the sole-mode and enhancer-mode
  // supervise panel.
  lab_mode?: "sole" | "enhancer" | null;
  // Calendar days as date-only "YYYY-MM-DD" (no time, no timezone). Use the
  // helpers in @multica/core/issues/date to format/compare — never `new Date()`
  // + local formatting, which shifts the day by the viewer's offset.
  start_date: string | null;
  due_date: string | null;
  metadata: IssueMetadata;
  reactions?: IssueReaction[];
  labels?: Label[];
  created_at: string;
  updated_at: string;
}
