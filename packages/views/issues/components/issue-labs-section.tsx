"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ChevronRight, ExternalLink, Loader2, ThumbsDown } from "lucide-react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { AppLink } from "../../navigation";
import { LabOutputPanel } from "../../experimental/components/lab-output-panel";
import { LabLastResultChip } from "./lab-last-result-chip";
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
  // 0.5.21: swarm topology — top-level task mode parallel to
  // claude_science_lab. Per-issue swarm view (IssueLabsSection) +
  // dedicated /experimental/swarm-topology view (sidebar entry).
  swarm_topology: "swarm-topology",
  // 0.5.81: semantica issues had NO jump target at all — this map is
  // the single source of truth for the PropRow external-link icon,
  // the create-issue post-create redirect and the trailing timeline
  // summary card, so a missing entry silently killed every one of
  // those affordances for semantica-bound issues. The URL matches
  // routes.tsx (`/experimental/semantica-explorer`, NOT
  // `/experimental/semantica` — that path is the REST proxy).
  semantica: "semantica-explorer",
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

// Swarm run status pill (0.5.22 FIX 2). Reads the issue-side swarm_run
// reverse lookup (GET /api/issues/{id}/swarm-runs) and renders a compact
// status pill next to the LabBadge when lab_source === "swarm_topology".
// The endpoint returns a single object (UNIQUE on root_issue_id) or 404;
// 404 (no run, or flag-off via RequireExperimentalFlag) maps to `null`
// and hides the pill. Status/phase are raw server enum strings — the
// same values the desktop swarm-topology view renders without i18n.
interface SwarmRunStatus {
  id: string;
  status: string;
  current_phase: string;
}

const SWARM_STATUS_TONES: Record<string, string> = {
  preparing: "bg-slate-100 text-slate-700",
  planning: "bg-blue-100 text-blue-700",
  running: "bg-blue-200 text-blue-900",
  monitoring: "bg-amber-100 text-amber-700",
  completed: "bg-emerald-100 text-emerald-700",
  aborted: "bg-slate-200 text-slate-700",
  failed: "bg-red-100 text-red-700",
};

// 0.5.81: the pill carries ?issue=<id> so the swarm-topology view can
// resume the bound run on arrival (swarm-topology-view fetchRunByIssue).
// The previous bare href landed on the empty BootstrapForm even when a
// run existed — the "click the result, nothing delivers" bug class.
function SwarmRunStatusPill({ run, issueId }: { run: SwarmRunStatus; issueId: string }) {
  const tone = SWARM_STATUS_TONES[run.status] ?? "bg-slate-100 text-slate-700";
  return (
    <AppLink
      href={`/experimental/swarm-topology?issue=${encodeURIComponent(issueId)}`}
      className="shrink-0"
      aria-label={`swarm run: ${run.status}`}
    >
      <span
        className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${tone}`}
        data-swarm-status={run.status}
      >
        {run.status}
        {run.current_phase ? (
          <span className="opacity-70">· {run.current_phase}</span>
        ) : null}
      </span>
    </AppLink>
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

  // Swarm run status (0.5.22 FIX 2). Poll the issue-side reverse lookup
  // so the pill tracks the orchestrator's live status/phase. Mode B
  // refetch (Active Contract #1): 5s while a run is non-terminal, 30s
  // once terminal or absent (404 → null).
  const swarmRun = useQuery({
    queryKey: ["swarm-run", issueId],
    queryFn: async (): Promise<SwarmRunStatus | null> => {
      const res = await api.rawRequest(
        `/api/issues/${encodeURIComponent(issueId)}/swarm-runs`,
      );
      if (res.status === 404) return null;
      if (!res.ok) throw new Error(`swarm run fetch failed: ${res.status}`);
      return res.json();
    },
    enabled: labSource === "swarm_topology",
    staleTime: 5_000,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (data && !["completed", "aborted", "failed"].includes(data.status)) {
        return 5_000;
      }
      return 30_000;
    },
  });

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
            {/* Swarm run status pill (0.5.22). Renders only when the
                issue is bound to a swarm_topology run AND the run
                exists. 0.5.60 (audit hole #6): once the lookup has
                resolved to 404, render a muted "not started" pill
                instead of nothing — with every run table empty on a
                fresh install, silence read as "broken". */}
            {swarmRun.data ? (
              <SwarmRunStatusPill run={swarmRun.data} issueId={issueId} />
            ) : swarmRun.isSuccess && !swarmRun.data ? (
              <AppLink
                href={`/experimental/swarm-topology?issue=${encodeURIComponent(issueId)}`}
                className="shrink-0"
              >
                <span
                  className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-[10px] font-medium text-slate-500"
                  data-swarm-status="never_started"
                >
                  {t(($) => $.lab_section.swarm_never_started)}
                </span>
              </AppLink>
            ) : null}
          </div>

          {/* 0.5.81: glanceable "latest result" affordance that lives one
              row above the LabOutputPanel reader. Shares the panel's
              TanStack Query cache key so the network round-trip is
              amortised. Augments — never replaces — the panel below. */}
          {(labSource === "claude_science_lab" ||
            labSource === "pythia_oracle" ||
            labSource === "mythos_swarm" ||
            labSource === "code_canvas" ||
            labSource === "swarm_topology") && (
            <LabLastResultChip
              wsId={wsId}
              issueId={issueId}
              labSource={labSource}
            />
          )}

          {/* 0.5.18 M1: converged read-side output panel for the four
              A-class issue-bound labs. Only claude_science_lab has a
              real reader in M1; the other three render a placeholder. */}
          {(labSource === "claude_science_lab" ||
            labSource === "pythia_oracle" ||
            labSource === "mythos_swarm" ||
            labSource === "code_canvas" ||
            labSource === "swarm_topology") && (
            <LabOutputPanel
              wsId={wsId}
              issueId={issueId}
              labSource={labSource}
              labMode={issueLabMode ?? undefined}
            />
          )}

          {suffix && labEnabled && (hasWorkspaceView || labSource.startsWith("user_")) ? (
            // 0.3.35: include ?issue=<id> so the lab panel opens
            // pre-scoped to this issue (ClaudeLabView / PythiaView /
            // MythosView all read the search param). Without it the
            // user lands on the workspace-scoped empty state and has
            // to re-pick the issue in the picker.
            //
            // 0.5.81: user plugins (user_*) never set
            // hides_deliverable_in_issue_timeline (plugin_scanner.go
            // leaves it false — sandbox runs are not issue-bound), so
            // hasWorkspaceView was false forever and this "open lab
            // panel" affordance was suppressed for every plugin issue:
            // created from a task, yet no way back into its surface.
            // The plugin shell exists and is usable (run / artifacts /
            // chat) even though it can't issue-scope artifacts yet, so
            // let user plugins through.
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
        </div>
      )}
    </div>
  );
}
