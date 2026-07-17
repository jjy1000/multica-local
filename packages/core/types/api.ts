import type { Issue, IssueMetadata, IssueStatus, IssuePriority, IssueAssigneeType } from "./issue";
import type { MemberRole } from "./workspace";
import type { Project } from "./project";

// Issue API
export interface CreateIssueRequest {
  title: string;
  description?: string;
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_type?: IssueAssigneeType;
  assignee_id?: string;
  parent_issue_id?: string;
  project_id?: string;
  /** Ordered stage (>= 1) grouping this sub-issue under its parent. */
  stage?: number;
  /** Associate this issue with an experimental lab (flag key). */
  lab_source?: string;
  start_date?: string;
  due_date?: string;
  attachment_ids?: string[];
}

export interface UpdateIssueRequest {
  title?: string;
  description?: string;
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_type?: IssueAssigneeType | null;
  assignee_id?: string | null;
  position?: number;
  start_date?: string | null;
  due_date?: string | null;
  parent_issue_id?: string | null;
  project_id?: string | null;
  /** Ordered stage (>= 1); null clears it (unstaged). */
  stage?: number | null;
  /** Associate/clear experimental lab (flag key). Null clears. */
  lab_source?: string | null;
  /** 0.3.31 — mythos_swarm dual-mode. "sole" = mythos owns the
   *  issue end-to-end; "enhancer" = mythos preludes + supervises,
   *  the user-picked assignee executes. Empty/null clears. Only
   *  meaningful when lab_source="mythos_swarm"; the backend
   *  rejects mismatches with 400. */
  lab_mode?: "sole" | "enhancer" | null;
  /** Attachment IDs to bind to this issue alongside the description update.
   *  Used by the description editor to register newly uploaded files so they
   *  surface in `issueAttachments` and keep their preview Eye on refresh. */
  attachment_ids?: string[];
  /** Skip starting the agent run this write would trigger ("暂时不启动",
   *  MUL-3375). The assignee/status change still applies. Control field —
   *  strip from optimistic cache patches; never written onto the Issue. */
  suppress_run?: boolean;
  /** Free-text handoff instruction injected into the started run's opening
   *  context (MUL-3375). Only consumed when a run actually starts. Control
   *  field — strip from optimistic cache patches. */
  handoff_note?: string;
}

/** Inputs to `POST /api/issues/preview-trigger`. A nil prospective field means
 *  "leave unchanged"; `isCreate` previews a not-yet-persisted issue. */
export interface IssueTriggerPreviewParams {
  issueIds?: string[];
  isCreate?: boolean;
  assigneeType?: IssueAssigneeType | null;
  assigneeId?: string | null;
  status?: IssueStatus;
}

/** One issue that WILL start a run under the prospective write. `agent_id` is
 *  the runnable agent (squad leader for squads). `handoff_supported` is the
 *  soft-gate signal: false when the target runtime is too old to render a
 *  handoff note (gray the note box; the assignment still works). */
export interface IssueTriggerPreviewItem {
  issue_id: string;
  agent_id: string;
  source: string;
  handoff_supported: boolean;
}

export interface IssueTriggerPreview {
  triggers: IssueTriggerPreviewItem[];
  total_count: number;
}

export interface ListIssuesParams {
  limit?: number;
  offset?: number;
  workspace_id?: string;
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_id?: string;
  assignee_ids?: string[];
  creator_id?: string;
  project_id?: string;
  /**
   * Widen the assignee filter to issues where the user is the *indirect*
   * assignee — assignee is one of the user's owned agents, or a squad that
   * involves the user (human member / leader-via-owned-agent / agent member
   * owned by the user). Direct member assignment is intentionally excluded:
   * `involves_user_id` and `assignee_id=<user>` (tab "Assigned to me") produce
   * disjoint result sets by construction.
   */
  involves_user_id?: string;
  /** JSONB containment filter on `issue.metadata`. AND across keys. */
  metadata?: IssueMetadata;
  open_only?: boolean;
  /**
   * Restrict the result to issues with at least one of `start_date` /
   * `due_date` set. Used by the Project Gantt view so it doesn't have to
   * page through every issue on the project just to discard the unscheduled
   * majority on the client.
   */
  scheduled?: boolean;
  date_field?: "created_at" | "updated_at";
  date_start?: string;
  date_end?: string;
  sort_by?: "position" | "priority" | "title" | "created_at" | "start_date" | "due_date";
  sort_direction?: "asc" | "desc";
  /**
   * 0.3.33: when true, lab-bound issues (lab_source IS NOT NULL) are
   * hidden from the result. Defaults to false (0.3.37): lab issues
   * are first-class tasks and surface in the main list. The list
   * toolbar's "hide experimental lab tasks" toggle flips this on
   * to opt out.
   */
  exclude_lab?: boolean;
}

export interface IssueActorRef {
  type: IssueAssigneeType;
  id: string;
}

export interface ListGroupedIssuesParams {
  group_by: "assignee";
  limit?: number;
  offset?: number;
  workspace_id?: string;
  statuses?: IssueStatus[];
  priorities?: IssuePriority[];
  assignee_types?: IssueAssigneeType[];
  assignee_id?: string;
  assignee_ids?: string[];
  creator_id?: string;
  project_id?: string;
  /** See `ListIssuesParams.involves_user_id` — same semantics. */
  involves_user_id?: string;
  /** JSONB containment filter on `issue.metadata`. AND across keys. */
  metadata?: IssueMetadata;
  assignee_filters?: IssueActorRef[];
  include_no_assignee?: boolean;
  creator_filters?: IssueActorRef[];
  project_ids?: string[];
  include_no_project?: boolean;
  label_ids?: string[];
  group_assignee_type?: IssueAssigneeType | "none";
  group_assignee_id?: string;
  date_field?: "created_at" | "updated_at";
  date_start?: string;
  date_end?: string;
  sort_by?: "position" | "priority" | "title" | "created_at" | "start_date" | "due_date";
  sort_direction?: "asc" | "desc";
}

/** Raw backend response shape for `GET /api/issues`. */
export interface ListIssuesResponse {
  issues: Issue[];
  total: number;
}

export interface IssueAssigneeGroup {
  id: string;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  issues: Issue[];
  total: number;
}

/** Raw backend response shape for `GET /api/issues/grouped?group_by=assignee`. */
export interface GroupedIssuesResponse {
  groups: IssueAssigneeGroup[];
}

/** Per-status bucket in the paginated issue cache. `total` is the server count (all pages), not the length of `issues`. */
export interface IssueStatusBucket {
  issues: Issue[];
  total: number;
}

/**
 * Frontend cache shape for the issue list. Data is bucketed by status so
 * each column can paginate independently. Assembled from per-status
 * `api.listIssues` responses by the query functions in `issues/queries.ts`.
 */
export interface ListIssuesCache {
  byStatus: Partial<Record<IssueStatus, IssueStatusBucket>>;
}

export interface SearchIssueResult extends Issue {
  match_source: "title" | "description" | "comment";
  matched_snippet?: string;
  matched_description_snippet?: string;
  matched_comment_snippet?: string;
}

export interface SearchIssuesResponse {
  issues: SearchIssueResult[];
  total: number;
}

export interface SearchProjectResult extends Project {
  match_source: "title" | "description";
  matched_snippet?: string;
}

export interface SearchProjectsResponse {
  projects: SearchProjectResult[];
  total: number;
}

export interface UpdateMeRequest {
  name?: string;
  avatar_url?: string;
  language?: string;
  /** Free-form self-description (max 2000 chars). Pass "" to clear. */
  profile_description?: string;
  /** IANA tz to pin; "" clears back to browser-tz; undefined leaves untouched. */
  timezone?: string;
}

export interface CreateMemberRequest {
  email: string;
  role?: MemberRole;
}

export interface UpdateMemberRequest {
  role: MemberRole;
}

// Personal Access Tokens
export interface PersonalAccessToken {
  id: string;
  name: string;
  token_prefix: string;
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface CreatePersonalAccessTokenRequest {
  name: string;
  expires_in_days?: number;
}

export interface CreatePersonalAccessTokenResponse extends PersonalAccessToken {
  token: string;
}

// Pagination
export interface PaginationParams {
  limit?: number;
  offset?: number;
}

// ---------------------------------------------------------------------------
// Claude Lab workbench (0.3.40)
// ---------------------------------------------------------------------------
// Wire shape of GET /api/experimental/claude-science-lab/issues/{id}/context.
// Slimmed-down from the full DB rows so the workbench doesn't pull MCP /
// runtime config blobs it never renders. The renderer keys off `lab_seq` for
// the progress badge, `tasks[].status` for the live timeline indicator, and
// `chat_session_id` to bind the right-hand LabChatPanel to the user's
// existing chat session (MUL-4351, chat_input_task_id).
export interface LabContext {
  issue: LabIssueBrief;
  agent: LabAgentBrief | null;
  tasks: LabTaskBrief[];
  comments: LabCommentBrief[];
  chat_session_id: string | null;
  lab_seq: number;
  server_time: string;
}

export interface LabIssueBrief {
  id: string;
  workspace_id: string;
  title: string;
  description: string | null;
  status: string;
  lab_source: string;
  lab_mode: "sole" | "enhancer" | null;
  assignee_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface LabAgentBrief {
  id: string;
  name: string;
  description: string;
  status: string;
}

export interface LabTaskBrief {
  id: string;
  status:
    | "queued"
    | "dispatched"
    | "running"
    | "preparing"
    | "waiting_local_directory"
    | "completed"
    | "failed"
    | "cancelled"
    | "deferred";
  trigger_summary: string | null;
  error: string | null;
  failure_reason: string | null;
  result_summary: string | null;
  // 0.3.40 v2: structured deliverables extracted from the agent's
  // result jsonb. The agent prompt (multica-claude-science SKILL.md +
  // leader agent `instructions`) instructs the agent to emit these
  // envelopes so the workbench can render charts / images / code
  // snippets inline rather than burying them in markdown.
  result_attachments?: LabAttachment[];
  result_predictions?: LabPrediction[];
  result_code_blocks?: LabCodeBlock[];
  created_at: string;
  dispatched_at: string | null;
  started_at: string | null;
  completed_at: string | null;
  duration_ms: number | null;
}

// LabAttachment is a single inline deliverable produced by the
// agent. `kind` mirrors the artifact-view taxonomy: `png` / `svg` /
// `html` / `interactive-chart` / `md` / `csv` / `json` / `txt` /
// `log`. Either `data` (inline payload) or `url` (server-stored
// reference) is populated; the renderer picks accordingly.
export interface LabAttachment {
  kind: string;
  name?: string;
  mime?: string;
  data?: unknown;
  url?: string;
  bytes?: number;
}

// LabPrediction is one row of the agent's probabilistic forecast.
// Renders as a single point on the Forecast tab probability chart.
export interface LabPrediction {
  round: number;
  scenario: string;
  narrative?: string;
  probability: number;
  confidence?: number;
  horizon?: string;
  persona?: string;
}

// LabCodeBlock is a fenced code snippet the agent wants to surface
// in the Code tab. `language` drives syntax highlighting; `code` is
// the raw source.
export interface LabCodeBlock {
  language: string;
  filename?: string;
  code: string;
}

export interface LabCommentBrief {
  id: string;
  author_type: "member" | "agent" | "system";
  content: string;
  created_at: string;
}
