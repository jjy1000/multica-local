import type { Issue, IssueMetadata, IssueStatus, IssueStatusCategory, IssuePriority, IssueAssigneeType } from "./issue";
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
  /** Mythos swarm run mode; only meaningful when lab_source="mythos_swarm".
   *  Kept off the create payload pre-0.3.55 meant the dialog's enhancer
   *  choice was silently dropped (server defaulted to "sole") while the
   *  assignee was preserved — landing on the server mutex 400. */
  lab_mode?: "sole" | "enhancer";
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
  /** Multi-value table facet. OR within the field. */
  statuses?: IssueStatus[];
  /**
   * Filter by status CATEGORY rather than by exact key, so one bucket holds a
   * category's canonical status plus every custom status that inherits it.
   * This is what keeps the board's fan-out fixed at 7 requests however many
   * custom statuses a workspace defines. (MUL-6243)
   */
  status_category?: IssueStatusCategory;
  /** Multi-value form of `status_category`. OR within the field. */
  status_categories?: IssueStatusCategory[];
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

// Server-authoritative Table query contract. Membership, grouping and counts
// are evaluated against the complete result set; the browser only owns view
// state such as collapsed groups/parents.
export type IssueTableScope =
  | { kind: "workspace"; assignee_types?: IssueAssigneeType[] }
  | { kind: "project"; project_id: string; assignee_types?: IssueAssigneeType[] }
  | { kind: "assignee"; actor: IssueActorRef }
  | { kind: "creator"; actor: IssueActorRef }
  | { kind: "my"; relation: "assigned" | "created" | "involved" | "any" };

export interface IssueTableFilters {
  statuses?: IssueStatus[];
  priorities?: IssuePriority[];
  assignees?: IssueActorRef[];
  include_no_assignee?: boolean;
  creators?: IssueActorRef[];
  project_ids?: string[];
  include_no_project?: boolean;
  label_ids?: string[];
  properties?: Record<string, string[]>;
  date?: {
    field: "created_at" | "updated_at";
    start: string;
    end: string;
  };
  working_only?: boolean;
  /** Match the running-task issue projection returned by
   *  `/api/working-agents`. An explicit empty list matches nothing. */
  working_issue_ids?: string[];
  include_sub_issues?: boolean;
}

export type IssueTableSortField =
  | "position"
  | "status"
  | "priority"
  | "title"
  | "created_at"
  | "updated_at"
  | "start_date"
  | "due_date"
  | `property:${string}`;

export interface IssueTableQuerySpec {
  scope: IssueTableScope;
  filters: IssueTableFilters;
  search?: string;
  sort: {
    field: IssueTableSortField;
    direction: "asc" | "desc";
  };
}

export type IssueTableGroupSpec =
  | { kind: "none" }
  | { kind: "status" }
  /**
   * Group by the CATEGORY a status behaves as, not by the status key.
   *
   * Board columns, list sections and swimlane cells are categories, so a custom
   * status folds into the column it behaves as instead of getting one of its
   * own — which is what keeps the surface's fan-out pinned at 7 no matter how
   * many statuses a workspace defines. The descriptor still reports
   * `value.kind === "status"` because a category's value IS its canonical
   * status key; the group KEY is what distinguishes the two contracts.
   * (MUL-6243)
   */
  | { kind: "status_category" }
  | { kind: "assignee" }
  | { kind: "project" }
  | { kind: "parent" }
  | {
      kind: "compound";
      primary: "assignee" | "project" | "parent";
      /** `status_category` folds custom statuses into their category's cell. */
      secondary: "status" | "status_category";
      /** Optional visible secondary buckets. When present, the server pages
       * only primary groups that contain at least one matching card and
       * returns `total` for that complete visible result set. */
      secondary_values?: IssueStatus[] | IssueStatusCategory[];
    }
  | { kind: "property"; property_id: string; include_empty?: boolean };

/** Response-side actor reference. Kept open for forward compatibility: an
 * installed desktop client may receive a new actor kind from a newer server. */
export interface IssueTableActorRef {
  type: string;
  id: string;
}

export interface IssueTableParentRef {
  id: string;
  number: number;
  identifier: string;
  title: string;
  status: string;
}

export type IssueTableGroupValue =
  | { kind: "status"; status: string }
  | { kind: "assignee"; actor: IssueTableActorRef | null }
  | { kind: "project"; project_id: string | null }
  | {
      kind: "parent";
      parent_id: string | null;
      parent: IssueTableParentRef | null;
      value_state: "value" | "unavailable" | "unset";
    }
  | {
      kind: "property";
      property_id: string;
      value?: string | boolean | null;
      value_state: "value" | "unavailable" | "unset";
    };

export interface IssueTableGroupDescriptor {
  key: string;
  value: IssueTableGroupValue;
  count: number;
  /** Present for compound groups. These opaque keys can be passed straight
   * back to `/table/rows`; clients must not reconstruct them. */
  secondary_groups?: IssueTableGroupDescriptor[];
}

export interface IssueTablePageRequest {
  limit?: number;
  cursor?: string | null;
}

export interface IssueTableGroupsRequest {
  query: IssueTableQuerySpec;
  group: Exclude<IssueTableGroupSpec, { kind: "none" }>;
  page?: IssueTablePageRequest;
}

export interface IssueTableGroupsResponse {
  query_fingerprint: string;
  total: number;
  groups: IssueTableGroupDescriptor[];
  next_cursor: string | null;
}

export interface IssueTableRowsRequest {
  query: IssueTableQuerySpec;
  group: IssueTableGroupSpec;
  group_key: string | null;
  hierarchy: { enabled: boolean };
  parent_id: string | null;
  page?: IssueTablePageRequest;
}

export interface IssueTableRow {
  issue: Issue;
  direct_child_count: number;
}

export interface IssueTableRowsResponse {
  query_fingerprint: string;
  group_key: string | null;
  parent_id: string | null;
  total: number;
  rows: IssueTableRow[];
  branch_total: number;
  next_cursor: string | null;
}

export type IssueTableFacetSpec =
  | { kind: "status" }
  | { kind: "priority" }
  | { kind: "assignee" }
  | { kind: "creator" }
  | { kind: "project" }
  | { kind: "label" }
  | { kind: "property"; property_id: string }
  /** Agents running issue work inside this surface. `key` is the agent id,
   *  `count` its running-task count. Evaluated against the surface's own scope
   *  and filters, so the header chip counts the same rows the list shows. */
  | { kind: "working_agents" };

export interface IssueTableFacetsRequest {
  query: IssueTableQuerySpec;
  facets: IssueTableFacetSpec[];
  /** Existing callers default to true. Count-only UIs can skip the extra scan. */
  include_total?: boolean;
}

export interface IssueTableFacetValue {
  key: string;
  count: number;
}

export interface IssueTableFacet {
  kind: IssueTableFacetSpec["kind"];
  property_id?: string;
  values: IssueTableFacetValue[];
}

export interface IssueTableFacetsResponse {
  query_fingerprint: string;
  total: number;
  facets: IssueTableFacet[];
}

/** One agent running issue work inside a single issue surface. Projected from
 *  the `working_agents` facet, so the count is already narrowed by that
 *  surface's scope and every active filter. Name/avatar are resolved from the
 *  workspace agent directory, not carried here. */
export interface WorkingAgentSummary {
  id: string;
  running_task_count: number;
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
  /** Bucketed by status CATEGORY — see PAGINATED_CATEGORIES. (MUL-6243) */
  byStatus: Partial<Record<IssueStatusCategory, IssueStatusBucket>>;
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

// Mythos Swarm durable run + supervise envelopes (0.5.18 M2 LabOutputPanel).
// The durable `GET /api/issues/{id}/mythos-runs` row carries `problem` (the
// run's problem statement) + `coda_conclusions` (structured takeaways) — the
// free-text `coda_summary` is NOT persisted to `mythos_run`; it only exists
// in the ephemeral POST /run response. The supervise envelope mirrors
// `MythosSuperviseStateResponse` in server/internal/handler/mythos_supervise.go.
export interface MythosCodaConclusion {
  key: string;
  value: string;
  confidence?: number | null;
  actionable?: boolean | null;
}

export interface MythosRunSummary {
  run_id: string;
  status: string;
  mode: string;
  started_at: string;
  problem: string;
  iterations: number;
  completed_at?: string | null;
  final_issue_id?: string | null;
  coda_conclusions: MythosCodaConclusion[];
}

export interface MythosSuperviseState {
  run_id: string;
  phase: string;
  started_at?: string;
  last_check_at?: string;
  last_tick_duration_ms: number;
  total_ticks: number;
  sub_tasks_total: number;
  sub_tasks_done: number;
  latest_reflection?: string;
  latest_reflection_iter?: number;
  abort_reason?: string;
}

export interface PythiaForecastEnvelope {
  id: string;
  scenario: string;
  narrative: string;
  probability: number;
  confidence: number;
  horizon: string;
  persona: string;
  lab_source: string;
  synthetic_oracle_failover?: boolean;
}

export interface PythiaForecastRun {
  id: string;
  rounds: number;
  source: string;
  created_at: string;
  envelopes: PythiaForecastEnvelope[];
}

// TimesFM per-issue forecast run (0.5.82 WL2) — mirrors the wire shape of
// GET /api/experimental/timesfm/forecast/issue/runs (a timesfm_forecast_run
// row, migration 275). `result` is the engine's raw JSONB answer; the
// quantile map keys are the engine's band labels.
export interface TimesfmQuantiles {
  lower_90: number[];
  lower_80: number[];
  median: number[];
  upper_80: number[];
  upper_90: number[];
}

export interface TimesfmSeriesPoint {
  point: number[];
  quantiles?: TimesfmQuantiles;
  provenance: string;
  dates?: string[];
}

export interface TimesfmForecastResult {
  series: TimesfmSeriesPoint[];
  provenance: string;
  model_present: boolean;
  horizon: number;
}

export interface TimesfmForecastRun {
  id: string;
  horizons: number;
  provenance: string;
  created_at: string;
  result?: TimesfmForecastResult;
}

export interface CodeCanvasArtifact {
  id: string;
  code: string;
  language: string;
  html: string;
  created_at: string;
}

// Issue causal graph wire shapes (0.5.83 WL3) — mirror the handler
// structs in server/internal/handler/causal_graph.go and the CHECK
// sets in migrations 277/278 (types stay `string` so a future CHECK
// addition renders verbatim).
export interface CausalNode {
  id: string;
  workspace_id: string;
  issue_id: string | null;
  type: string;
  label: string;
  description: string | null;
  metadata: Record<string, unknown>;
  provenance: Record<string, unknown>;
  created_at: string;
  created_by: string | null;
  lab_source: string | null;
  lab_run_id: string | null;
  status: string;
  last_observed_at: string;
}

export interface CausalEdge {
  id: string;
  workspace_id: string;
  from_node_id: string;
  to_node_id: string;
  type: string;
  weight: number | null;
  confidence: number | null;
  metadata: Record<string, unknown>;
  provenance: Record<string, unknown>;
  created_at: string;
  created_by: string | null;
  proposed_by: string | null;
  status: string;
}

export interface CausalSubgraph {
  issue_id: string;
  depth: number;
  nodes: CausalNode[];
  edges: CausalEdge[];
}

export interface CausalPath {
  nodes: CausalNode[];
  edges: CausalEdge[];
}
