"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, ExternalLink, FlaskConical, Loader2, RefreshCw, CheckCircle2, AlertTriangle, XCircle } from "lucide-react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { AppLink } from "../../navigation";
import { LabBadge } from "./lab-badge";
import { useT } from "../../i18n";

// Hard-coded mapping from experimental flag key to its experimental
// view route suffix. Mirrors the `path: "experimental/<suffix>"`
// entries in apps/desktop/src/renderer/src/routes.tsx. Kept local
// because the wire shape (`ExperimentalFlag`) intentionally
// doesn't carry the route — sidebar entries already hold it, but
// the flag object's surface is trimmed for the LabPicker /
// settings surface. This is the single source of truth for both
// the sidebar status section (this file) and the inline PropRow
// in issue-detail.tsx (which imports `labSourceRouteSuffix`
// from here). Update this map when adding a new lab view.
export const FLAG_ROUTE_SUFFIX: Record<string, string> = {
  claude_science_lab: "claude-lab",
  pythia_oracle: "pythia",
  mythos_swarm: "mythos",
  llm_wiki_bridge: "llm-wiki",
  code_canvas: "code-canvas",
  agent_self_optimization: "agent-self-optimization",
  constitution_agent: "constitution-agent",
  chat_pin_ui: "chat-pin",
};

/**
 * Public helper consumed by issue-detail.tsx. Returns the route
 * suffix (`/experimental/<suffix>`) for a given `lab_source`,
 * or `undefined` if the lab has no dedicated view.
 */
export function labSourceRouteSuffix(
  labSource: string | null | undefined,
): string | undefined {
  if (!labSource) return undefined;
  return FLAG_ROUTE_SUFFIX[labSource];
}

/**
 * @deprecated 0.3.49.1: the renderer-side hardcoded `VIEW_LAB_SOURCES`
 * set has been folded into the server catalog as
 * `Flag.HidesDeliverableInIssueTimeline` (mirrored on the wire as
 * `hides_deliverable_in_issue_timeline`). The two consumer sites
 * (`IssueLabsSection` for the open-panel link, `IssueDetail` for the
 * timeline comment filter) now read the field via `useExperimentalFlags`.
 * The constant is kept exported only so older renderers don't fail to
 * typecheck during the rollout window; new code MUST NOT reference it.
 *
 * Lab sources that own a dedicated workspace-scoped view (workbench).
 * For these labs the agent's substantive output is delivered inside the
 * lab panel (via GetClaudeLabContext), so issue-detail.tsx hides the
 * agent's deliverable comments from the plain issue timeline — the
 * results belong in the lab, not the issue task.
 *
 * 0.3.49: noted as a 6-element subset that overlaps with the catalog's
 * `HideFromIssueLabPicker` field. The two encode different semantics:
 *  - `VIEW_LAB_SOURCES` answers "does this lab ship a workspace-scoped
 *    view?" (used by issue-detail.tsx to gate the timeline deliverable
 *    thread).
 *  - `HideFromIssueLabPicker` answers "should this lab be hidden from
 *    the per-issue LabPicker popover?" (used by `lab-picker.tsx` to
 *    suppress infrastructure / self-driven labs from manual selection).
 *
 * They happen to share 6 keys today, but the lists can diverge:
 * `chat_pin_ui` and `agent_creation_studio` are NOT in
 * `VIEW_LAB_SOURCES` (neither owns a workspace-scoped view); the
 * `HideFromIssueLabPicker:true` set (`llm_wiki_bridge` +
 * `agent_self_optimization`) intentionally stays small. The hardcoded
 * set is maintained as the canonical "workspace-scoped view" anchor;
 * the server-driven `HideFromIssueLabPicker` is independently toggled
 * in `server/internal/experimental/catalog.go`.
 */
export const VIEW_LAB_SOURCES: ReadonlySet<string> = new Set([
  "claude_science_lab",
  "pythia_oracle",
  "mythos_swarm",
  "llm_wiki_bridge",
  "code_canvas",
  "agent_self_optimization",
  "constitution_agent",
]);

/**
 * Sidebar "Labs" section for issues that were tagged with a lab source.
 */
export function IssueLabsSection({
  issueId,
  labSource,
  issueLabMode = null,
}: {
  issueId: string;
  labSource: string;
  issueLabMode?: "sole" | "enhancer" | null;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { data: flags } = useExperimentalFlags();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const [open, setOpen] = useState(true);

  const flagTitle = useMemo(() => {
    const f = (flags ?? []).find((flag) => flag.key === labSource);
    if (!f) return null;
    return f.title.zh || f.title.en;
  }, [flags, labSource]);

  const suffix = FLAG_ROUTE_SUFFIX[labSource];
  const labEnabled = (flags ?? []).some((f) => f.key === labSource && f.enabled);
  // 0.3.49.1: read the "owns a workbench view" flag from the catalog
  // rather than the deprecated VIEW_LAB_SOURCES const. The semantics
  // are identical (the const was seeded with the same 7 flags the
  // catalog now declares `hides_deliverable_in_issue_timeline: true`)
  // but going through the server keeps the consumer in sync with
  // future flag additions or removals without a TS release.
  const hidesDeliverable = (flags ?? []).some(
    (f) => f.key === labSource && f.hides_deliverable_in_issue_timeline === true,
  );
  const hasWorkspaceView = hidesDeliverable;

  const live = useMemo(() => {
    let running = false;
    let queued = false;
    for (const task of snapshot) {
      if (task.issue_id !== issueId) continue;
      if (task.status === "running") running = true;
      else if (
        task.status === "queued" ||
        task.status === "dispatched" ||
        task.status === "waiting_local_directory"
      ) {
        queued = true;
      }
    }
    return { running, queued };
  }, [snapshot, issueId]);

  const indicator = live.running
    ? {
        tone: "running" as const,
        label: t(($) => $.lab_section.running_indicator),
        title: t(($) => $.lab_section.running_tooltip),
      }
    : live.queued
      ? {
          tone: "queued" as const,
          label: t(($) => $.lab_section.queued_indicator),
          title: t(($) => $.lab_section.queued_tooltip),
        }
      : null;

  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen((v) => !v)}
      >
        <LabBadge labSource={labSource} />
        <span>{t(($) => $.lab_section.section_title)}</span>
        <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
      </button>
      {open && (
        <div className="space-y-1.5 pl-2">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-xs text-foreground/90">
              {flagTitle ?? labSource}
            </span>
            {indicator && (
              <span
                className="inline-flex shrink-0 items-center gap-1 rounded-full bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700 dark:text-emerald-300"
                title={indicator.title}
              >
                {indicator.tone === "running" ? (
                  <Loader2 className="size-2.5 animate-spin" aria-hidden />
                ) : (
                  <span className="size-1.5 rounded-full bg-emerald-500" aria-hidden />
                )}
                {indicator.label}
              </span>
            )}
          </div>
          {suffix && labEnabled && hasWorkspaceView ? (
            // 0.3.35: include ?issue=<id> so the lab panel opens
            // pre-scoped to this issue (ClaudeLabView / PythiaView /
            // MythosView all read the search param). Without it the
            // user lands on the workspace-scoped empty state and has
            // to re-pick the issue in the picker.
            <AppLink
              href={`/experimental/${suffix}?issue=${encodeURIComponent(issueId)}`}
              className="inline-flex items-center gap-1 text-xs text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300 transition-colors"
            >
              <ExternalLink className="size-3" />
              {t(($) => $.lab_section.open_panel)}
            </AppLink>
          ) : (
            <div className="rounded-md border border-dashed border-border/60 px-2 py-1.5 text-[11px] text-muted-foreground">
              <p className="font-medium text-foreground/80">
                {t(($) => $.lab_section.no_flag_title)}
              </p>
              <p className="mt-0.5 leading-snug">{t(($) => $.lab_section.no_flag_hint)}</p>
            </div>
          )}

          {/* 0.3.31: enhancer-mode supervise panel */}
          {labSource === "mythos_swarm" && issueLabMode === "enhancer" && (
            <MythosEnhancerSupervisePanel issueId={issueId} enhancerMode />
          )}
        </div>
      )}
    </div>
  );
}

// ── 0.3.31 enhancer supervise panel ────────────────────────────────────────

interface SuperviseStateSnapshot {
  run_id: string;
  phase: string;
  sub_tasks_total: number;
  sub_tasks_done: number;
  total_ticks: number;
  last_tick_duration_ms: number;
  latest_reflection?: string;
  latest_reflection_iter?: number;
  abort_reason?: string;
}

// Mythos supervise phases that need sub-minute polling. The server-side
// `superviseLoop` (server/internal/service/mythos/supervise.go) ticks
// every 30 s; without `isLive`-driven 5 s polling the renderer can
// race the server tick and miss an entire supervision snapshot per
// fetch. Terminal phases (`done` / `aborted` / `degraded`) fall back to
// 60 s idle — the panel is mounted only while `enhancerMode` is true,
// so the existence of this set is the only signal we have for "is this
// run still progressing?".
const LIVE_MYTHOS_PHASES = new Set<SuperviseStateSnapshot["phase"]>([
  "preparing",
  "planning",
  "supervising",
]);

function MythosEnhancerSupervisePanel({
  issueId,
  enhancerMode,
}: {
  issueId: string;
  enhancerMode: boolean;
}) {
  const { t } = useT("issues");
  const qc = useQueryClient();

  const wsId = useWorkspaceId();

  // `mythos-runs-for-issue` polls once a run exists for the issue.
  // Before a run shows up (preparing / still spinning up) the panel
  // renders the empty-state copy and the query backs off to 60 s so
  // we're not hammering the server. Once a run ID materialises the
  // companion `mythos-supervise-state` query (below) takes over at 5 s.
  const runsQuery = useQuery({
    queryKey: ["mythos-runs-for-issue", issueId, wsId] as const,
    queryFn: async () => {
      const res = await api.rawRequest(
        `/api/issues/${issueId}/mythos-runs?workspace_id=${encodeURIComponent(wsId)}`,
        { method: "GET" },
      );
      if (!res.ok) {
        if (res.status === 404) return [];
        throw new Error(`mythos runs fetch failed: ${res.status}`);
      }
      return res.json() as Promise<{ run_id: string }[]>;
    },
    enabled: enhancerMode,
    refetchInterval: (query) =>
      query.state.data && query.state.data.length > 0 ? 5_000 : 60_000,
    staleTime: 10_000,
  });

  const runID = runsQuery.data?.[0]?.run_id;

  const stateQuery = useQuery({
    queryKey: ["mythos-supervise-state", runID] as const,
    queryFn: async () => {
      if (!runID) return null;
      const res = await api.rawRequest(
        `/api/experimental/mythos-swarm/supervise/${runID}`,
        { method: "GET" },
      );
      if (!res.ok) {
        if (res.status === 404) return null;
        throw new Error(`supervise state fetch failed: ${res.status}`);
      }
      return res.json() as Promise<SuperviseStateSnapshot>;
    },
    enabled: !!runID,
    refetchInterval: (query) => {
      const snap = query.state.data;
      if (!snap) return 60_000;
      return LIVE_MYTHOS_PHASES.has(snap.phase) ? 5_000 : 60_000;
    },
    staleTime: 5_000,
  });

  const tick = useMutation({
    mutationFn: async () => {
      if (!runID) throw new Error("no run id");
      const res = await api.rawRequest(
        `/api/experimental/mythos-swarm/supervise/${runID}/tick`,
        { method: "POST" },
      );
      if (!res.ok) throw new Error(`tick failed: ${res.status}`);
      return res.json() as Promise<SuperviseStateSnapshot>;
    },
    onSuccess: (snapshot) => {
      qc.setQueryData(["mythos-supervise-state", runID], snapshot);
    },
  });

  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const phase: string = stateQuery.data?.phase ?? "preparing";

  // 0.3.48: removed the `useState(30_000) + setTimeout(10_000)` manual
  // poll-boost that used to ride on `tick.isSuccess`. The `tick`
  // mutation's `onSuccess` already seeds `stateQuery` via
  // `qc.setQueryData`, and the `refetchInterval` above now keys off the
  // snapshot's phase, so the bespoke 10-second boost is superseded by
  // the canonical `isLive(...) ? 5_000 : 60_000` shape (see
  // 0.3.45.7 → 0.3.45.9 lineage).

  if (!enhancerMode) return null;
  if (!runID) {
    return (
      <div className="mt-1.5 rounded-md border border-dashed border-purple-500/30 bg-purple-500/5 px-2 py-1.5 text-[11px] text-muted-foreground">
        <p className="text-foreground/80">
          {t(($) => $.lab_section.mythos_enhancer_phase_preparing)}
        </p>
      </div>
    );
  }
  // both enhancerMode === true and runID is non-null here
  return (
    <div className="mt-1.5 space-y-1 rounded-md border border-purple-500/30 bg-purple-500/5 px-2 py-1.5 text-[11px]">
      <div className="flex items-center justify-between gap-2">
        <PhaseIcon phase={phase} />
        <span className="truncate font-medium text-foreground/90">{phase}</span>
      </div>

      {stateQuery.data && (
        <div className="space-y-0.5 text-muted-foreground">
          {stateQuery.data.sub_tasks_total > 0 && (
            <div>
              {`${stateQuery.data.sub_tasks_done}/${stateQuery.data.sub_tasks_total}`}
            </div>
          )}
          {stateQuery.data.total_ticks > 0 && (
            <div className="text-[10px]">
              {`${stateQuery.data.total_ticks} ticks · ${stateQuery.data.last_tick_duration_ms}ms`}
            </div>
          )}
          {stateQuery.data.latest_reflection && (
            <div className="mt-1 line-clamp-2 text-foreground/70 italic">
              {stateQuery.data.latest_reflection}
            </div>
          )}
        </div>
      )}

      <button
        type="button"
        onClick={() => tick.mutate()}
        disabled={tick.isPending}
        className="inline-flex items-center gap-1 rounded-md border border-border/60 bg-background/80 px-2 py-0.5 text-[10px] font-medium text-foreground/80 transition-colors hover:bg-accent disabled:opacity-50"
      >
        <RefreshCw className={`size-2.5 ${tick.isPending ? "animate-spin" : ""}`} aria-hidden />
        {t(($) => $.lab_section.mythos_enhancer_check_now) ?? "立即检查"}
      </button>
    </div>
  );
}

function PhaseIcon({ phase }: { phase: string }) {
  const cls = "size-3 shrink-0";
  switch (phase) {
    case "preparing":
    case "planning":
      return <Loader2 className={`${cls} animate-spin text-purple-500`} aria-hidden />;
    case "supervising":
      return <Loader2 className={`${cls} animate-spin text-emerald-500`} aria-hidden />;
    case "done":
      return <CheckCircle2 className={`${cls} text-emerald-500`} aria-hidden />;
    case "aborted":
      return <XCircle className={`${cls} text-red-500`} aria-hidden />;
    case "degraded":
      return <AlertTriangle className={`${cls} text-amber-500`} aria-hidden />;
    default:
      return <FlaskConical className={`${cls} text-purple-500`} aria-hidden />;
  }
}
