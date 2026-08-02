"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, ExternalLink, FlaskConical, Loader2, RefreshCw, CheckCircle2, AlertTriangle, XCircle, ThumbsDown } from "lucide-react";
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
// settings surface. This is the single source of truth for the
// sidebar status section (this file), the inline PropRow in
// issue-detail.tsx, and the create-issue redirect (both import
// `labSourceRouteSuffix` from here). Update this map when adding
// a new lab view.
//
// (0.3.68: chat_pin_ui entry removed — routes.tsx never shipped an
// `experimental/chat-pin` view, so the mapping produced dead links.)
export const FLAG_ROUTE_SUFFIX: Record<string, string> = {
  claude_science_lab: "claude-lab",
  pythia_oracle: "pythia",
  mythos_swarm: "mythos",
  llm_wiki_bridge: "llm-wiki",
  code_canvas: "code-canvas",
  // 0.5.5.3: agent_self_optimization no longer maps to a dedicated
  // experimental view — the feature is product-level (control via
  // the autopilot row's `enabled` field, not a Labs-tab toggle).
  // The SelfOptView page still exists as a documentation-style
  // overview at /experimental/agent-self-optimization, but the
  // issue-detail "open panel" link no longer renders for it
  // (matches the 0.5.5 removal of agent_creation_studio's mapping).
  // (0.3.57: constitution_agent entry removed alongside the lab
  // retirement in migration 165.)
};

/**
 * Public helper consumed by issue-detail.tsx and create-issue.tsx.
 * Returns the route suffix (`/experimental/<suffix>`) for a given
 * `lab_source`, or `undefined` if the lab has no dedicated view.
 *
 * User plugins (`user_*` lab sources, 0.3.60+) route to the generic
 * plugin shell (`experimental/plugin/:pluginSlug` in routes.tsx).
 */
export function labSourceRouteSuffix(
  labSource: string | null | undefined,
): string | undefined {
  if (!labSource) return undefined;
  if (labSource.startsWith("user_")) {
    const slug = labSource.slice(5); // strip "user_" prefix
    return slug ? `plugin/${slug}` : undefined;
  }
  return FLAG_ROUTE_SUFFIX[labSource];
}

// AgentTrustCorrectButton (0.5.2) — issue-detail correction entry for the
// agent_self_optimization trust score. The user flags the assigned agent's
// work as wrong; the server applies -0.5 and records a 'correction' event
// that the self-opt runner learns from. Rendered only when the issue has an
// agent assignee AND the flag is enabled (the trust surface is opt-in).
export function AgentTrustCorrectButton({
  wsId,
  agentId,
  issueId,
}: {
  wsId: string;
  agentId: string;
  issueId: string;
}) {
  const [note, setNote] = useState("");
  const [feedback, setFeedback] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  const correct = useMutation({
    mutationFn: async () => {
      const res = await api.rawRequest(
        `/api/experimental/trust/${encodeURIComponent(agentId)}/correct`,
        {
          method: "POST",
          body: JSON.stringify({ workspace_id: wsId, issue_id: issueId, note }),
          headers: { "content-type": "application/json" },
        },
      );
      if (!res.ok) throw new Error(`correct failed: ${res.status}`);
      return res.json() as Promise<{ score: number }>;
    },
    onSuccess: (body) => {
      setFeedback(`已纠正,当前信任分 ${body.score.toFixed(1)}(-0.5)`);
      setNote("");
    },
    onError: (e) => {
      setFeedback(`纠正失败:${e instanceof Error ? e.message : String(e)}`);
    },
  });

  return (
    <div className="flex flex-col gap-1">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1 text-caption text-red-600 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300 transition-colors"
      >
        <ThumbsDown className="size-3" aria-hidden />
        纠正智能体
      </button>
      {open && (
        <div className="flex flex-col gap-1.5 rounded-md border border-border/60 bg-background/60 p-2">
          <input
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="备注:哪里错了(可选)"
            className="rounded-md border border-border bg-background px-2 py-1 text-caption text-foreground placeholder:text-muted-foreground"
          />
          <button
            type="button"
            disabled={correct.isPending}
            onClick={() => correct.mutate()}
            className="inline-flex items-center justify-center gap-1 rounded-md border border-red-500/40 bg-red-500/10 px-2 py-1 text-caption font-medium text-red-700 hover:bg-red-500/20 disabled:opacity-50 dark:text-red-300"
          >
            <ThumbsDown className="size-3" aria-hidden />
            扣分纠正(-0.5)
          </button>
          {feedback && <p className="text-[11px] text-foreground/80">{feedback}</p>}
        </div>
      )}
    </div>
  );
}

/**
 * Sidebar "Labs" section for issues that were tagged with a lab source.
 */export function IssueLabsSection({
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

  const suffix = labSourceRouteSuffix(labSource);
  const labEnabled = (flags ?? []).some((f) => f.key === labSource && f.enabled);
  // 0.3.51: the catalog's `hides_deliverable_in_issue_timeline` field
  // is the single source-of-truth for "does this lab ship a
  // workspace-scoped workbench view?". The 0.3.49.1 ship wired this
  // lookup; the 0.3.50 ship replaced the historical VIEW_LAB_SOURCES
  // const entirely, so this is now the only place the field is read.
  const hidesDeliverable = (flags ?? []).some(
    (f) => f.key === labSource && f.hides_deliverable_in_issue_timeline === true,
  );
  const hasWorkspaceView = hidesDeliverable;

  // 0.3.54: track the full live lifecycle, not just running/queued.
  // Background agent_task rows surface in the renderer through
  // `getAgentTaskSnapshot()`. A flip to `failed` or `cancelled`
  // was previously invisible — the user saw the badge vanish and
  // assumed the work was still in flight. The B-class automation
  // flags (agent_self_optimization,
  // llm_wiki_bridge) drive their work entirely outside of an
  // explicit task per issue, so the absence of any task should
  // surface as an "auto-running" hint rather than disappear.
  const live = useMemo(() => {
    let running = false;
    let queued = false;
    let failed = false;
    let cancelled = false;
    for (const task of snapshot) {
      if (task.issue_id !== issueId) continue;
      if (task.status === "running") running = true;
      else if (
        task.status === "queued" ||
        task.status === "dispatched" ||
        task.status === "waiting_local_directory"
      ) {
        queued = true;
      } else if (task.status === "failed") {
        failed = true;
      } else if (task.status === "cancelled") {
        cancelled = true;
      }
    }
    return { running, queued, failed, cancelled };
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
      : live.failed
        ? {
            tone: "failed" as const,
            label: t(($) => $.lab_section.failed_indicator),
            title: t(($) => $.lab_section.failed_tooltip),
          }
        : live.cancelled
          ? {
              tone: "cancelled" as const,
              label: t(($) => $.lab_section.cancelled_indicator),
              title: t(($) => $.lab_section.cancelled_tooltip),
            }
          : null;

  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen((v) => !v)}
      >
        <LabBadge labSource={labSource} />
        <span>{t(($) => $.lab_section.section_title)}</span>
        <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
      </button>
      {open && (
        <div className="space-y-1.5 pl-2">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-caption text-foreground/90">
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
              className="inline-flex items-center gap-1 text-caption text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300 transition-colors"
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
