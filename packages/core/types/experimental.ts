// Experimental / Labs flag type. Mirrors the server-side
// `ExperimentalFlagResponse` struct in
// server/internal/handler/experimental_flags.go.
//
// The catalog lives in code (server/internal/experimental/catalog.go);
// the labs UI only renders whatever the server returns. Users cannot add
// new flags at runtime — see the plan at .omc/plan-0.3.6-experimental-flags.md
// for the user constraints that drive this shape.

export interface LocalizedString {
  en: string;
  zh: string;
}

// 0.3.20: sidebar entry point declared in the experiment manifest.
// Carried on every flag's wire shape so the renderer's nav hook can
// render the "Experimental" sidebar group from the same payload
// without a second round-trip or a hard-coded STATIC_NAV list.
export interface ExperimentalSidebarEntry {
  /** Stable row key (must match across en/zh locales). */
  key: string;
  /** Flag key this entry points to (== top-level `key` today; left
   *  as a separate field so future flags can expose multiple entries). */
  flag_key?: string;
  /** i18n key the renderer resolves with its locale table. */
  label_key: string;
  /** Renderer route — typically under /experimental/<slug>. */
  route: string;
}

// PR 7: install manifest embedded in a flag's GET response. Shape
// mirrors ExperimentalResourcesManifest on the server (counts are
// duplicated per resource_type so the renderer side panel can phrase
// "已装载 292 skills (280 可见)" without a second round-trip).
export interface ExperimentalFlagInstallation {
  source: string;
  installed: boolean;
  hidden: boolean;
  counts: Array<{
    resource_type: string;
    total: number;
    visible: number;
  }>;
  recent_activity?: {
    installed_workspace_slug?: string;
    tasks_last_24h: number;
    agent_runs_last_24h: number;
    last_activity_at?: string;
  };
}

export interface ExperimentalFlag {
  /** Stable identifier; matches `experimental_pref.flag_key`. */
  key: string;
  /** Current effective state — merges catalog default + user override. */
  enabled: boolean;
  /** Catalog default; rendered as the "off by default — enable to try" hint. */
  default_enabled: boolean;
  /** User-visible title, server-supplied bilingual. */
  title: LocalizedString;
  /** User-visible description, server-supplied bilingual. */
  description: LocalizedString;
  /** 0.3.20: catalog RuntimeKind — none / inline / subprocess / headless. */
  runtime_kind?: string;
  /** 0.3.20: manifest entry_points.sidebar rows. Empty array means
   *  the flag has no sidebar entry (visible only in the Labs tab). */
  sidebar_entries?: ExperimentalSidebarEntry[];
  /** 0.3.45.8: when true, the issue-detail LabPicker does NOT offer
   *  this flag as a per-issue "实验插件" choice. Reserved for
   *  infrastructure / self-driven labs whose effect is global once
   *  enabled (llm_wiki_bridge, agent_self_optimization). The flag is
   *  still rendered in the Labs settings tab where the user flips the
   *  toggle on. Absent or false = picker offers the flag as usual. */
  hide_from_issue_lab_picker?: boolean;
  /** 0.5.3: when true, the issue-detail LabPicker shows this flag EVEN
   *  when it is not enabled. Reserved for action-type labs whose entry
   *  is a user action reachable without a Labs opt-in
   *  (agent_creation_studio). Absent or false = the picker applies the
   *  enabled filter as usual. Mutually exclusive with
   *  hide_from_issue_lab_picker. */
  always_show_in_lab_picker?: boolean;
  /** 0.3.49.1: when true, the lab owns a workspace-scoped view where
   *  the agent deliverable belongs, and `issue-detail.tsx` filters the
   *  deliverable thread out of the plain issue timeline. Pre-0.3.49.1
   *  this was a renderer-side hardcoded `VIEW_LAB_SOURCES` Set;
   *  the migration moved the source of truth into the catalog so a
   *  future flag declares "I own a workbench view" once and the
   *  consumers (`issue-detail.tsx`, `issue-labs-section.tsx`) inherit
   *  the choice for free. Absent or false = deliverable comments
   *  remain visible in the plain timeline. */
  hides_deliverable_in_issue_timeline?: boolean;
  /** PR 7: present when the flag has installable backing resources. */
  installation?: ExperimentalFlagInstallation;
  /** 0.3.60: true when the flag originates from a user-created plugin
   *  rather than the developer catalog. The Labs tab renders user
   *  plugins in a separate section below the catalog flags. */
  is_user_plugin?: boolean;
  /** 0.3.65: the workspace agent name this lab auto-assigns as the issue
   *  owner when picked (the "实验室测试智能体" shown under the locked
   *  assignee). Mirrors the server-side leader-rewrite table. Empty /
   *  absent means the lab owns no single agent (mythos_swarm runs via its
   *  squad roster; llm_wiki_bridge / chat_pin_ui have no per-issue agent)
   *  — the renderer then shows a generic "lab owns the roster" hint. */
  leader_agent?: string;
  /** 0.5.86: how the lab participates on a bound issue.
   *  - "assignee" (独立工作型): the lab's leader agent owns the issue's
   *    assignee slot — the AssigneePicker locks to it and the server
   *    400s any non-leader assignee on create/update.
   *  - "auxiliary" (辅助协作型): the lab works alongside the normal
   *    agents for tracing/visualization and is never an assignee.
   *  Absent means legacy/unclassified — no lock applies
   *  (chat_pin_ui, code_canvas, user plugins). Mirrors the Go
   *  Flag.InteractionModel (server/internal/experimental/catalog.go). */
  interaction_model?: "assignee" | "auxiliary";
  /** 0.5.86: when true the lab is frozen — kept toggleable for existing
   *  installs but superseded by another lab (e.g. swarm_topology →
   *  mythos_swarm). The Labs settings tab renders a muted banner naming
   *  the successor. Absent/false = not frozen. Sent by newer servers;
   *  absent on older payloads — the client defaults gracefully. */
  frozen?: boolean;
  /** 0.5.86: flag key of the lab that supersedes this one when
   *  `frozen` is true. Resolved against the same flags list for a
   *  display label (raw key as fallback). */
  successor_key?: string;
}

export interface ExperimentalFlagsList {
  flags: ExperimentalFlag[];
}

// 0.5.88 P4: the interaction-model contract block of a user-plugin
// manifest. Mirrors the server-side parser
// (server/internal/experimental/plugin_scanner.go
// ParseUserPluginContract) and the 0.5.86 built-in taxonomy
// (ExperimentalFlag.interaction_model):
//   - "assignee" (独立工作型): the plugin's leader agent owns the bound
//     issue's assignee slot — binding locks the assignee.
//   - "auxiliary" (辅助协作型): assists other agents; never locks.
// Absent interaction_model defaults to "auxiliary" (behavior-
// preserving: unclassified plugins never locked). leader_agent is
// REQUIRED and non-empty when interaction_model is "assignee"
// (server rejects the create/update otherwise). The index signature
// keeps every other manifest key (capabilities, ui, …) addressable.
export interface UserPluginManifest {
  interaction_model?: "assignee" | "auxiliary";
  leader_agent?: string;
  [key: string]: unknown;
}

// 0.3.60 Labs sandbox: user-created plugin wire shape. Mirrors the
// server-side UserPluginResponse struct. CRUD endpoints live at
// /api/user-plugins; the GET /api/experimental-flags list merges
// active user plugins with is_user_plugin: true.
export interface UserPluginResponse {
  id: string;
  slug: string;
  flag_key: string;
  title: LocalizedString;
  description: LocalizedString;
  trigger_mode: "auto" | "issue_select";
  runtime_kind: "none" | "inline" | "subprocess";
  status: "active" | "disabled" | "deleted";
  manifest?: UserPluginManifest;
  created_at: string;
  updated_at: string;
}

// 0.3.60 Labs sandbox: a single artifact produced by a user plugin run.
// Mirrors the server-side artifact metadata wire shape returned by
// GET /api/user-plugins/:slug/artifacts. The renderer dispatches on
// `type` to pick a viewer (image / chart / table / html / code / file /
// text); `data` carries inline payloads (chart/table/code/text) while
// `url` points at binary blobs (image/file) served by the backend.
export interface ArtifactMeta {
  id: string;
  type: "image" | "chart" | "table" | "html" | "code" | "file" | "text";
  title: string;
  mime_type?: string;
  size?: number;
  data?: unknown;
  url?: string;
  created_at: string;
}