"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChevronRight, ExternalLink, Loader2, Square, ThumbsDown } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { useExperimentalFlags } from "@multica/core/experimental";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { AppLink } from "../../navigation";
import { LabOutputPanel } from "../../experimental/components/lab-output-panel";
import { LabProgressCard } from "./lab-progress-card";
import { LabLastResultChip } from "./lab-last-result-chip";
import { LabBadge } from "./lab-badge";
import { TerminateTaskConfirmDialog } from "./terminate-task-confirm-dialog";
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
  // 0.5.105 (audit H3): the swarm_topology row was removed with the
  // runtime retirement — legacy swarm-bound issues resolve no route.
  // 0.5.81: semantica issues had NO jump target at all — this map is
  // the single source of truth for the PropRow external-link icon,
  // the create-issue post-create redirect and the trailing timeline
  // summary card, so a missing entry silently killed every one of
  // those affordances for semantica-bound issues. The URL matches
  // routes.tsx (`/experimental/semantica-explorer`, NOT
  // `/experimental/semantica` — that path is the REST proxy).
  semantica: "semantica-explorer",
  // 0.5.82: timesfm — local TimesFM 2.5 forecasting lab. The URL matches
  // routes.tsx (`/experimental/timesfm-lab`, NOT `/experimental/timesfm`
  // — that path is the bare REST proxy the agent subprocess calls; same
  // split as semantica above).
  timesfm: "timesfm-lab",
  // 0.5.83: issue causal graph + hidden agent team. Same routing law as
  // semantica/timesfm above — `/experimental/causal-graph` is the focused
  // view (not the bare `/api/causal-graph/*` REST surface).
  causal_graph: "causal-graph",
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
  const { t } = useT("issues");
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
      // 0.5.105 (audit M1): feedback copy was hardcoded Chinese in this
      // shared component — now resolves through the issues locale.
      setFeedback(
        t(($) => $.lab_section.trust_correct_ok, { score: body.score.toFixed(1) }),
      );
      setNote("");
    },
    onError: (e) => {
      setFeedback(
        t(($) => $.lab_section.trust_correct_failed, {
          msg: e instanceof Error ? e.message : String(e),
        }),
      );
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
        {t(($) => $.lab_section.trust_correct_button)}
      </button>
      {open && (
        <div className="flex flex-col gap-1.5 rounded-md border border-border/60 bg-background/60 p-2">
          <input
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder={t(($) => $.lab_section.trust_correct_note_placeholder)}
            className="rounded-md border border-border bg-background px-2 py-1 text-caption text-foreground placeholder:text-muted-foreground"
          />
          <button
            type="button"
            disabled={correct.isPending}
            onClick={() => correct.mutate()}
            className="inline-flex items-center justify-center gap-1 rounded-md border border-red-500/40 bg-red-500/10 px-2 py-1 text-caption font-medium text-red-700 hover:bg-red-500/20 disabled:opacity-50 dark:text-red-300"
          >
            <ThumbsDown className="size-3" aria-hidden />
            {t(($) => $.lab_section.trust_correct_submit)}
          </button>
          {feedback && <p className="text-[11px] text-foreground/80">{feedback}</p>}
        </div>
      )}
    </div>
  );
}

// 0.5.105 (audit H3): the SwarmRunStatusPill (0.5.22) and its
// /api/issues/{id}/swarm-runs reverse-lookup query were removed with
// the swarm_topology runtime retirement — the endpoint no longer
// exists, and legacy swarm-bound issues simply render no pill.

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
  const reduceMotion = useReducedMotion() ?? false;

  const flagTitle = useMemo(() => {
    const f = (flags ?? []).find((flag) => flag.key === labSource);
    if (!f) return null;
    return f.title.zh || f.title.en;
  }, [flags, labSource]);

  const suffix = labSourceRouteSuffix(labSource);
  const labEnabled = (flags ?? []).some((f) => f.key === labSource && f.enabled);
  // 0.5.103: the "open panel" link is gated on `suffix && labEnabled` only.
  // The historical `hides_deliverable_in_issue_timeline` lookup (0.3.51)
  // stood in for "ships a workspace view", but the field actually means
  // "the deliverable posts to the issue timeline" — false for pythia_oracle
  // BY DESIGN, which suppressed the panel link for pythia-bound issues and
  // dropped the section into the "lab not enabled" fallback copy while the
  // flag was on. Route membership is the honest signal.

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

  // 0.5.103: surface a stop control next to the running indicator. Before
  // this the only terminate affordance lived in the execution log, which
  // readers of the labs section (e.g. a multi-round Pythia run) never find —
  // the run looks unstoppable until it finishes on its own. Same API the
  // execution log uses; cancelling the agent task aborts its in-flight
  // forecast request, whose round loop listens on the request context.
  const [confirmTerminate, setConfirmTerminate] = useState(false);
  const [terminating, setTerminating] = useState(false);
  const runningTaskId = useMemo(
    () =>
      snapshot.find((task) => task.issue_id === issueId && task.status === "running")?.id ?? null,
    [snapshot, issueId],
  );
  const terminateTask = useMutation({
    mutationFn: (taskId: string) => api.cancelTask(issueId, taskId),
    onSuccess: () => {
      setConfirmTerminate(false);
      setTerminating(false);
    },
    onError: (e) => {
      setTerminating(false);
      toast.error(e instanceof Error ? e.message : t(($) => $.execution_log.cancel_failed));
    },
  });

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
      {/* Animated collapse (0.5.86 polish): the body fades + slides via a
          height:auto animation instead of popping in/out (which shifted the
          sidebar layout). Reduced motion collapses instantly — the mounted
          motion.div keeps the DOM shape but runs the transition at duration
          0, mirroring the causal-canvas reduced-motion treatment. */}
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            key="issue-labs-section-body"
            className="space-y-1.5 overflow-hidden pl-2"
            initial={reduceMotion ? false : { height: 0, opacity: 0 }}
            animate={{
              height: "auto",
              opacity: 1,
              transition: {
                duration: reduceMotion ? 0 : UI_MOTION_DURATION.standard,
                ease: UI_EASE_OUT,
              },
            }}
            exit={{
              height: 0,
              opacity: 0,
              transition: {
                duration: reduceMotion ? 0 : UI_MOTION_DURATION.fast,
                ease: UI_EASE_OUT,
              },
            }}
          >
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-caption text-foreground/90">
              {flagTitle ?? labSource}
            </span>
            <div className="flex shrink-0 items-center gap-1">
            {live.running && runningTaskId && (
              <button
                type="button"
                disabled={terminating}
                aria-label={t(($) => $.lab_section.terminate_running)}
                title={t(($) => $.lab_section.terminate_running)}
                onClick={() => setConfirmTerminate(true)}
                className="flex items-center justify-center rounded p-1 text-destructive transition-colors hover:bg-destructive/10 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {terminating ? (
                  <Loader2 className="size-3 animate-spin" />
                ) : (
                  <Square className="size-3" />
                )}
              </button>
            )}
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
          </div>

          {/* 0.5.86: per-lab progress card. Generalizes the swarm-only
              status pill above into every run-capable lab bound to the
              issue (pythia / timesfm / mythos / claude_science_lab):
              idle / running / done / failed / engine-down + a deep link
              into the lab view (推演动画 / 进度 / 最终显示). Auxiliary
              labs (causal_graph, llm_wiki_bridge, semantica) render a
              muted no-run row instead. swarm_topology keeps the pill (it
              already carries status + click-through) and code_canvas
              keeps the panel — the card renders null for both so their
              surfaces are never duplicated. */}
          <LabProgressCard
            issueId={issueId}
            workspaceId={wsId}
            labSource={labSource}
            flagEnabled={labEnabled}
            live={live}
          />

          {/* 0.5.81: glanceable "latest result" affordance that lives one
              row above the LabOutputPanel reader. Shares the panel's
              TanStack Query cache key so the network round-trip is
              amortised. Augments — never replaces — the panel below.

              0.5.81 audit P3: mounted ONLY for claude_science_lab — that is
              the sole A-class lab with a real LabContext implementation;
              mounting for the other four silently rendered a null component
              every render. Widen this condition alongside new readers in
              LabLastResultChip itself, never here alone. */}
          {labSource === "claude_science_lab" && (
            <LabLastResultChip
              wsId={wsId}
              issueId={issueId}
              labSource={labSource}
            />
          )}

          {/* 0.5.18 M1: converged read-side output panel for the
              A-class issue-bound labs. Only claude_science_lab has a
              real LabContext reader; pythia/mythos/code-canvas/swarm
              have dedicated readers, and 0.5.82 adds the timesfm
              compact records reader. */}
          {(labSource === "claude_science_lab" ||
            labSource === "pythia_oracle" ||
            labSource === "mythos_swarm" ||
            labSource === "code_canvas" ||
            labSource === "swarm_topology" ||
            labSource === "timesfm") && (
            <LabOutputPanel
              wsId={wsId}
              issueId={issueId}
              labSource={labSource}
              labMode={issueLabMode ?? undefined}
            />
          )}

          {suffix && labEnabled ? (
            // 0.3.35: include ?issue=<id> so the lab panel opens
            // pre-scoped to this issue (ClaudeLabView / PythiaView /
            // MythosView all read the search param). Without it the
            // user lands on the workspace-scoped empty state and has
            // to re-pick the issue in the picker.
            //
            // 0.5.81: user plugins (user_*) never set
            // hides_deliverable_in_issue_timeline (plugin_scanner.go
            // leaves it false — sandbox runs are not issue-bound), so
            // gating on that field suppressed this link for every
            // plugin issue AND for built-ins like pythia_oracle whose
            // deliverable posts to the issue timeline (hides_deliverable
            // = false by design). The route map above only contains
            // flags that ship a real workspace-scoped view, so
            // `suffix && labEnabled` is the honest gate: an enabled lab
            // with a known route always gets its way back into the
            // panel. (0.5.103: pythia-bound issues showed the
            // "lab not enabled" fallback copy while the flag was ON —
            // the direct cause of the "did it even start?" confusion.)
            <AppLink
              href={`/experimental/${suffix}?issue=${encodeURIComponent(issueId)}`}
              className="inline-flex items-center gap-1 text-caption text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300 transition-colors"
            >
              <ExternalLink className="size-3" />
              {t(($) => $.lab_section.open_panel)}
            </AppLink>
          ) : !labEnabled ? (
            // Only claim "not enabled" when the flag actually is. An
            // enabled lab with no route suffix is a developer error
            // pinned by the FLAG_ROUTE_SUFFIX mapping test (Active
            // Contract #9) — render nothing rather than a lie.
            <div className="rounded-md border border-dashed border-border/60 px-2 py-1.5 text-[11px] text-muted-foreground">
              <p className="font-medium text-foreground/80">
                {t(($) => $.lab_section.no_flag_title)}
              </p>
              <p className="mt-0.5 leading-snug">{t(($) => $.lab_section.no_flag_hint)}</p>
            </div>
          ) : null}
          <TerminateTaskConfirmDialog
            open={confirmTerminate}
            onOpenChange={setConfirmTerminate}
            onConfirm={() => {
              if (!runningTaskId) return;
              setTerminating(true);
              terminateTask.mutate(runningTaskId);
            }}
            showRunningNote
          />
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
