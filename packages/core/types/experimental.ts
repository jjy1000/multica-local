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
  /** PR 7: present when the flag has installable backing resources. */
  installation?: ExperimentalFlagInstallation;
}

export interface ExperimentalFlagsList {
  flags: ExperimentalFlag[];
}