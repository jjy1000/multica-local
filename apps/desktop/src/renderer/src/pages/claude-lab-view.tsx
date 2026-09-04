// ClaudeLabView (0.3.29, flag-gated)
//
// Single-pane Claude Research Lab — replaces the 0.3.20
// `claude_science` brochure page + `claude_science_runtime`
// sandbox page. One window, five capability tabs:
//
//   Plan       — issues tagged lab_source=claude_science_lab (Plan tab)
//   Artifact   — runtime sandbox sessions + rendered artifacts
//   Forecast   — live prediction ticker + probability chart (SVG)
//   Code       — recent runtime sessions + low-code run button
//   Knowledge  — skill references for the active issue
//
// Chat (formerly a sixth tab) was removed in 0.3.36 — conversations
// live on the workspace-level ChatWindow side panel keyed by
// `chat_input_task_id` (MUL-4351); the row title still jumps to the
// issue detail page where that conversation is mounted. The 0.5.97
// cycle removed the per-row "open chat" button (redundant with the
// title jump) and made selection result-first: picking an issue in the
// Plan tab mounts the workbench whose LatestResultPanel renders the
// experiment's newest structured result (charts / predictions / code)
// above the full run history.
//
// Hard rules:
//
//   1. Flag-gated via `useExperimentalFlag("claude_science_lab", false)`.
//      When the flag is off the entire view short-circuits to an
//      "enable in Labs" placeholder, so the experimental branch is
//      fully bypassed per the 0.3.6 framework constraint.
//   2. No external navigation. Tabs swap an internal state, never
//      `push('/claude-science/issues')`. The lab is its own surface.
//   3. No reserved workspace. Issue/Squad/Member artifacts created
//      inside the lab live on the user's currently active workspace,
//      tagged via `issue.lab_source`.
//   4. i18n selectors MUST be arrow expressions, never block bodies,
//      per the 2026-07-14 AppSidebar incident (see CLAUDE.md).
//
// 0.3.29 changes vs 0.3.27:
//   - Plan tab now lists real issues (lab_source=claude_science_lab)
//     from the active workspace, with per-issue Run / Open actions.
//   - Artifact tab mounts the real `<ExperimentalArtifactView>` so
//     users see sandbox sessions + rendered PNG / SVG / HTML / chart
//     artifacts. Sessions filter by issue via the by-issue endpoint.
//   - Forecast tab layers a probability SVG chart above the live
//     ticker so the user gets a quick "scenario vs probability" read.
//   - Code tab lists recent runtime sessions for the active issue
//     with snippet preview + status badges.
//   - Knowledge tab shows research-skill references from the bundled
//     claude-science catalogue so the user can pick a Skill to invoke.
//   - All tabs share a `selectedIssueId` so picking an issue in Plan
//     narrows Artifact / Code / Knowledge in one click.

import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  ArrowRight,
  FlaskConical,
  Loader2,
  Lock,
  Play,
  Sparkles,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useT } from "@multica/views/i18n";
import { ForecastStreamView, LabChatPanel, labChatPanelPropsFromContext } from "@multica/views/experimental";
import {
  LabTaskResultView,
  useDeepLinkRun,
} from "@multica/views/experimental/components";
import { getCurrentSlug, getCurrentWsId } from "@multica/core/platform";
import { paths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";
import { api } from "@multica/core/api";

import { ExperimentalArtifactView } from "@/components/experimental-artifact-view";

type LabTab = "plan" | "artifact" | "forecast" | "code" | "knowledge";

const TAB_ORDER: LabTab[] = ["plan", "artifact", "forecast", "code", "knowledge"];

interface LabIssue {
  id: string;
  workspace_id: string;
  title: string;
  description: string | null;
  status: string;
  priority: string | null;
  number: number;
  lab_source: string | null;
  created_at: string;
  updated_at: string;
}

interface LabIssuesResponse {
  issues: LabIssue[];
  total: number;
  lab: string;
}

interface RuntimeSession {
  id: string;
  workspace_id: string;
  agent_id: string;
  issue_id: string | null;
  language: string;
  status: string;
  exit_code: number | null;
  duration_ms: number | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  stdout: string | null;
  stderr: string | null;
  lab_source: string | null;
}

interface RuntimeSessionsResponse {
  sessions: RuntimeSession[];
  total: number;
}

const CLAUDE_LAB_FLAG = "claude_science_lab";
const CLAUDE_LAB_SOURCE = "claude_science_lab";

// 0.3.45.8 (P0#3.7 sibling): the three lab list queries below
// (`claude-lab-issues` / `claude-lab-runtime-sessions` /
// `claude-lab-code-sessions`) use custom query keys that are NOT covered
// by use-realtime-sync's WS invalidation (which only touches the standard
// issueKeys.all / runtimeKeys.all / … keys). They previously had a
// staleTime but no refetchInterval, so a status badge only refreshed on
// mount or window refocus — the same stale-status failure mode fixed for
// agentTaskSnapshot in 0.3.45.7, but worse here because there is no WS
// push to fall back on. Poll every 5s while any row is still in flight
// and fall back to the original staleTime cadence as an idle baseline
// (never `false`, precisely because there is no WS signal to catch
// externally-triggered changes or newly created issues).
//
// Live issue statuses: the badge can still flip while the lab works the
// issue — typically in_review → done when the agent finishes (see
// daemon.go CompleteTask). done / todo / backlog / cancelled are treated
// as idle by the poll.
const LIVE_LAB_ISSUE_STATUSES = new Set(["in_progress", "in_review"]);

// Live runtime/code session statuses: a session's status badge flips from
// running → finished/failed. Anything not in this set is terminal-ish and
// polled at the idle baseline.
const LIVE_LAB_SESSION_STATUSES = new Set([
  "running",
  "pending",
  "queued",
  "starting",
]);

export function ClaudeLabView({ issueId: initialIssueId = null }: { issueId?: string | null } = {}) {
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);
  const { t } = useT("claude-lab");
  const [tab, setTab] = useState<LabTab>("plan");
  const [wsId, setWsId] = useState<string | null>(() => getCurrentWsId());
  // URL `?issue=<id>` lets CreateIssueDialog land the user back in the
  // lab panel pre-scoped to the just-created issue, instead of jumping
  // to the issue detail page. The prop wins when both are set
  // (inline-render from IssueDetailPage). Without `?issue=` the view
  // behaves like the workspace-scoped `/experimental/claude-lab` route
  // did before 0.3.35 (selectedIssueId starts null).
  const [searchParams] = useSearchParams();
  const urlIssueId = searchParams.get("issue");
  const [selectedIssueId, setSelectedIssueId] = useState<string | null>(
    initialIssueId ?? urlIssueId,
  );
  // 0.3.43: sync subsequent URL changes back into selectedIssueId.
  // Without this useEffect, navigating /experimental/claude-lab?issue=A
  // -> ?issue=B (e.g. CreateIssueDialog onCreate completion, or
  // back/forward through router history) leaves the workbench strip
  // (LabWorkbenchSection + PlanTimeline + IssueContextBar +
  // LabChatPanelContainer) bound to A — same regression class as
  // mythos-view.tsx RunForm `rootIssueId` before its 0.3.43
  // useEffect was added. We also re-derive from initialIssueId so
  // the inline-render path (IssueDetailPage -> ClaudeLabView with
  // a prop) rebinds on prop change too.
  useEffect(() => {
    const next = initialIssueId ?? urlIssueId ?? null;
    setSelectedIssueId((prev) => (prev === next ? prev : next));
  }, [initialIssueId, urlIssueId]);
  // 0.3.45.8: the lab's running agent is no longer a separate piece
  // of state. It comes from the bound issue's assignee (issue.assignee_id
  // is set when the user picks the lab in IssueDetail's PropRow — see
  // packages/views/issues/components/pickers/lab-picker.tsx). The lab
  // panel renders that as a read-only strip and the Code tab pulls it
  // from the same `api.getIssue(id)` query. Dropping the dedicated
  // state removes a class of bugs where the lock leaked across
  // surfaces (set agent in one tab, see it bind to a different issue).
  useEffect(() => {
    const id = setInterval(() => {
      const next = getCurrentWsId();
      setWsId((prev) => (prev === next ? prev : next));
    }, 500);
    return () => clearInterval(id);
  }, []);

  if (!enabled) {
    return (
      <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
        <Header active="plan" onTabChange={setTab} />
        <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
          <Intro />
          <FlagOffNotice />
        </main>
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header active={tab} onTabChange={setTab} />
      <main className="mx-auto flex w-full max-w-6xl flex-col gap-6 px-6 py-6">
        <Intro />
        {selectedIssueId ? (
          <div className="-mb-2 flex items-center justify-end gap-1.5 text-[10px] text-muted-foreground">
            <span className="rounded border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-foreground/80">
              {selectedIssueId.slice(0, 8)}…
            </span>
            <button
              type="button"
              onClick={() => setSelectedIssueId(null)}
              aria-label={t(($) => $.agent_lock_clear)}
              className="inline-flex size-4 items-center justify-center rounded text-xs hover:bg-muted hover:text-foreground"
            >
              ×
            </button>
          </div>
        ) : null}
        <LabAgentFromIssue
          wsId={wsId}
          selectedIssueId={selectedIssueId}
        />
        <ActiveTab
          tab={tab}
          wsId={wsId}
          selectedIssueId={selectedIssueId}
          onSelectIssue={setSelectedIssueId}
        />
        {/* 0.3.40 Claude Lab workbench: appears below the active tab
            whenever an issue is selected. The Plan/Artifact/Forecast/
            Code/Knowledge tabs above remain the workspace-scoped
            browse surface; this strip is the per-issue workbench
            that ties together agent runs + chat + the chat session
            that owns them. Flag-off never reaches this branch
            because the parent <ClaudeLabView /> short-circuits on
            flag-disabled. */}
        <LabWorkbenchSection
          wsId={wsId}
          selectedIssueId={selectedIssueId}
        />
      </main>
    </div>
  );
}

// LabWorkbenchSection (0.3.40)
//
// Renders the per-issue workbench strip below the active tab when
// an issue has been selected. Layout: 12-col grid on lg+ (timeline
// 8 / chat panel 4), stacks vertically on smaller viewports. The
// component is intentionally decoupled from the active tab — the
// user can flip between Plan / Artifact / Forecast / Code /
// Knowledge without losing their workbench context.
//
// Data flow:
//
//   1. Single GET to /api/experimental/claude-science-lab/issues/
//      {id}/context — see server/internal/handler/lab.go. Polled
//      every 5 s while the strip is visible so a fresh agent run
//      shows up without manual refresh; the chat panel on the
//      right polls its own messages every 3 s.
//
//   2. The LabChatPanel inside is bound to chat_session_id
//      resolved by the workbench (server looks up the most recent
//      task's chat_input_task_id). When chat_session_id is null
//      (a brand-new lab issue) the chat panel renders a
//      "send the first message to start" hint and lazy-creates
//      a session on first send.
//
//   3. Tasks are rendered as a vertical timeline — terminal runs
//      (completed/failed/cancelled) at the top sorted DESC by
//      created_at, in-flight runs at the bottom. The lab_seq
//      badge in the issue context bar counts terminal runs.
//
// Flag-off: the parent <ClaudeLabView /> short-circuits before
// this component is ever mounted, so no double-gate is needed.
function LabWorkbenchSection({
  wsId,
  selectedIssueId,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
}) {
  if (!wsId || !selectedIssueId) return null;

  return (
    <section
      aria-label="Lab workbench"
      className="grid grid-cols-1 gap-4 lg:grid-cols-12"
    >
      <div className="lg:col-span-8">
        <IssueContextBar
          wsId={wsId}
          selectedIssueId={selectedIssueId}
        />
        <LatestResultPanel
          wsId={wsId}
          selectedIssueId={selectedIssueId}
        />
        <PlanTimeline
          wsId={wsId}
          selectedIssueId={selectedIssueId}
        />
      </div>
      <div className="lg:col-span-4">
        <LabChatPanelContainer
          wsId={wsId}
          selectedIssueId={selectedIssueId}
        />
      </div>
    </section>
  );
}

// LabChatPanelContainer — thin wrapper that ties the chat panel
// to the workbench's LabContext. We render the panel as a fixed
// 480-px-ish column on desktop; on narrow viewports it stacks
// below the timeline (handled by the parent grid above). The panel
// reads its agent from the bound issue's assignee — 0.3.45.8 dropped
// the separate lab-agent lock state, so there is nothing to inherit
// here; the chat and the lab are bound to the same issue and the
// same agent row.
function LabChatPanelContainer({
  wsId,
  selectedIssueId,
}: {
  wsId: string;
  selectedIssueId: string;
}) {
  const { t } = useT("claude-lab");
  const ctx = useLabWorkbenchContext(wsId, selectedIssueId);
  if (!ctx.data) {
    return (
      <div className="flex h-[480px] items-center justify-center rounded-xl border border-dashed border-border bg-card/40 text-xs text-muted-foreground">
        {ctx.isLoading ? t(($) => $.loading) : t(($) => $.loading_failed)}
      </div>
    );
  }
  const props = labChatPanelPropsFromContext(ctx.data, wsId, selectedIssueId);
  if (!props) {
    return (
      <div className="flex h-[480px] items-center justify-center rounded-xl border border-dashed border-border bg-card/40 p-4 text-center text-xs text-muted-foreground">
        {t(($) => $.agent_lock_issue_required)}
      </div>
    );
  }
  return (
    <div className="h-[600px]">
      <LabChatPanel {...props} />
    </div>
  );
}

// useLabWorkbenchContext — single-query bootstrap for the
// workbench strip. Polled every 5 s so a fresh agent run shows up
// without manual refresh; the chat panel on the right polls its
// own messages at a tighter 3-s cadence.
function useLabWorkbenchContext(wsId: string, issueId: string) {
  return useQuery({
    queryKey: ["claude-lab-workbench-context", wsId, issueId],
    enabled: !!wsId && !!issueId,
    staleTime: 5_000,
    refetchInterval: 5_000,
    queryFn: () => api.getLabContext(issueId, wsId),
  });
}

// IssueContextBar (0.3.40)
//
// Header strip for the selected lab issue: title + status badge +
// lab_seq progress counter. The bar is the only place the user can
// see at a glance how many runs the lab has produced and where the
// current issue stands. Click on the title jumps to the main
// issue detail page (matches the Plan row title behavior, 0.3.38).
function IssueContextBar({
  wsId,
  selectedIssueId,
}: {
  wsId: string;
  selectedIssueId: string;
}) {
  const { t } = useT("claude-lab");
  const router = useNavigation();
  const slug = getCurrentSlug();
  const queryClient = useQueryClient();
  const ctx = useLabWorkbenchContext(wsId, selectedIssueId);
  const issue = ctx.data?.issue;
  const labSeq = ctx.data?.lab_seq ?? 0;
  const status = issue?.status ?? "";
  const title = issue?.title ?? "";

  const onTitleClick = () => {
    if (!slug) return;
    router.push(paths.workspace(slug).issueDetail(selectedIssueId));
  };

  // 0.5.22: manual "Run research" trigger. claude_science_lab opts out
  // of auto-dispatch (the leader-rewrite still applies, but no
  // agent_task_queue row is created until the user clicks). Hide the
  // button while a task is in flight so the user can't double-enqueue.
  const tasks = ctx.data?.tasks ?? [];
  const inFlight = tasks.some(
    (task) =>
      task.status === "queued" ||
      task.status === "running" ||
      task.status === "preparing" ||
      task.status === "dispatched" ||
      task.status === "waiting_local_directory" ||
      task.status === "deferred",
  );
  const runResearch = useMutation({
    mutationFn: async () => {
      const resp = await api.rawRequest(
        `/api/experimental/claude-science/issues/${selectedIssueId}/run`,
        { method: "POST" },
      );
      if (!resp.ok) {
        const text = await resp.text();
        throw new Error(text || `HTTP ${resp.status}`);
      }
      return (await resp.json()) as { task_id: string };
    },
    onSuccess: () => {
      // The lab context query carries the new task row; refetch so the
      // PlanTimeline + lab_seq refresh immediately instead of waiting
      // for the 5s polling beat.
      queryClient.invalidateQueries({
        queryKey: ["claude-lab-context", selectedIssueId],
      });
    },
  });

  return (
    <div
      aria-label={t(($) => $.issue_context_aria)}
      className="mb-3 flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-2"
    >
      <button
        type="button"
        onClick={onTitleClick}
        disabled={!slug}
        className={
          "min-w-0 flex-1 truncate text-left text-sm font-medium " +
          (slug ? "hover:underline" : "cursor-not-allowed")
        }
        title={title}
      >
        {ctx.isLoading ? t(($) => $.loading) : title || "—"}
      </button>
      <span className="rounded-md bg-secondary px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-secondary-foreground">
        {t(($) => $.issue_context_status)}: {status || "—"}
      </span>
      <span className="rounded-md bg-primary/10 px-2 py-0.5 text-[10px] font-medium text-primary">
        {t(($) => $.plan_lab_seq_label, { seq: labSeq })}
      </span>
      {issue?.lab_source === "claude_science_lab" && !inFlight && (
        <button
          type="button"
          onClick={() => runResearch.mutate()}
          disabled={runResearch.isPending}
          className="rounded-md bg-primary px-2.5 py-1 text-[11px] font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-60"
          title={t(($) => $.dispatch_manually_hint)}
        >
          {runResearch.isPending
            ? t(($) => $.loading)
            : t(($) => $.run_research_button)}
        </button>
      )}
      {runResearch.isError && (
        <span className="text-[10px] text-destructive">
          {t(($) => $.loading_failed)}
        </span>
      )}
    </div>
  );
}

// LatestResultPanel (0.5.97)
//
// Result-first card for the selected issue: renders the newest run that
// actually produced output (charts / predictions / code / summary) above
// the PlanTimeline history. This is what makes Plan-tab selection an
// "open the experiment" action — clicking 选中 mounts the workbench with
// the experiment's rendered result at the top, instead of making the
// user scroll a timeline to find it. Rendered deliverables go through
// the shared LabTaskResultView (same renderer as the issue-side summary
// card), with a taller chart canvas since the workbench has the room.
function LatestResultPanel({
  wsId,
  selectedIssueId,
}: {
  wsId: string;
  selectedIssueId: string;
}) {
  const { t } = useT("claude-lab");
  const ctx = useLabWorkbenchContext(wsId, selectedIssueId);
  if (!ctx.data) return null;
  const tasks = [...ctx.data.tasks].sort((a, b) =>
    a.created_at < b.created_at ? 1 : -1,
  );
  if (tasks.length === 0) return null;

  // Newest run with real output — an in-flight newest run must not hide
  // the previous result (same contract as LabOutputPanel's ClaudePanel).
  const outputTask =
    tasks.find(
      (task) =>
        task.result_summary ||
        (task.result_attachments?.length ?? 0) > 0 ||
        (task.result_predictions?.length ?? 0) > 0 ||
        (task.result_code_blocks?.length ?? 0) > 0,
    ) ?? null;
  if (!outputTask) return null;

  const newest = tasks[0];
  const newestInFlight =
    newest.id !== outputTask.id &&
    newest.status !== "completed" &&
    newest.status !== "failed" &&
    newest.status !== "cancelled";

  // Run seq counts terminal runs DESC (same numbering as PlanTimeline).
  const terminal = tasks.filter(
    (task) =>
      task.status === "completed" ||
      task.status === "failed" ||
      task.status === "cancelled",
  );
  const terminalIdx = terminal.findIndex((task) => task.id === outputTask.id);
  const seq = terminalIdx >= 0 ? terminal.length - terminalIdx : 0;

  const renderStatus = () => {
    switch (outputTask.status) {
      case "queued":
        return t(($) => $.plan_timeline_status_queued);
      case "dispatched":
        return t(($) => $.plan_timeline_status_dispatched);
      case "running":
        return t(($) => $.plan_timeline_status_running);
      case "preparing":
        return t(($) => $.plan_timeline_status_preparing);
      case "waiting_local_directory":
        return t(($) => $.plan_timeline_status_waiting_local_directory);
      case "completed":
        return t(($) => $.plan_timeline_status_completed);
      case "failed":
        return t(($) => $.plan_timeline_status_failed);
      case "cancelled":
        return t(($) => $.plan_timeline_status_cancelled);
      case "deferred":
        return t(($) => $.plan_timeline_status_deferred);
      default:
        return outputTask.status;
    }
  };

  return (
    <section
      aria-label={t(($) => $.latest_result_header)}
      data-testid="claude-lab-latest-result"
      className="mb-3 rounded-xl border border-primary/25 bg-card p-4"
    >
      <header className="mb-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Sparkles className="size-3.5 text-primary" aria-hidden />
        <span className="font-medium text-foreground">
          {t(($) => $.latest_result_header)}
        </span>
        {seq > 0 ? (
          <span className="font-mono text-[10px]">
            {t(($) => $.plan_timeline_run_label, { seq })}
          </span>
        ) : null}
        <span
          className={
            "rounded px-1.5 py-0.5 text-[10px] font-medium " +
            statusBadgeClass(outputTask.status)
          }
        >
          {renderStatus()}
        </span>
        {outputTask.duration_ms !== null ? (
          <span className="text-[10px]">
            {t(($) => $.plan_timeline_run_duration)}: {formatMs(outputTask.duration_ms)}
          </span>
        ) : null}
        {newestInFlight ? (
          <span className="ml-auto inline-flex items-center gap-1 text-[10px]">
            <Loader2 className="size-3 animate-spin" aria-hidden />
            {t(($) => $.plan_timeline_status_running)}
          </span>
        ) : null}
      </header>
      <LabTaskResultView task={outputTask} chartHeightClass="h-64" />
    </section>
  );
}

// PlanTimeline (0.3.40)
//
// Renders agent_task_queue rows for the selected issue as a
// vertical timeline. Terminal runs (completed/failed/cancelled)
// appear sorted DESC by created_at; in-flight runs (queued/
// running/preparing/dispatched) appear at the bottom and pulse.
//
// Why a separate component: keeps the workbench strip file
// readable, and lets us hand off the timeline shape to a future
// v2 Storybook / Playwright spec without touching the rest of
// the view.
function PlanTimeline({
  wsId,
  selectedIssueId,
}: {
  wsId: string;
  selectedIssueId: string;
}) {
  const { t } = useT("claude-lab");
  const ctx = useLabWorkbenchContext(wsId, selectedIssueId);
  // 0.5.81 audit P1: both issue-side emitters (LabLastResultChip and
  // ClaudePanel's "view full record" link) point ?issue=&run=<task id> at
  // this view, so PlanTimeline is the run receiver for the ICP-3 contract —
  // hooks must sit above the early returns below (rules-of-hooks).
  const deepLink = useDeepLinkRun<HTMLLIElement>();
  if (!ctx.data) {
    return (
      <div className="rounded-xl border border-dashed border-border bg-card/40 p-4 text-xs text-muted-foreground">
        {ctx.isLoading ? t(($) => $.loading) : t(($) => $.loading_failed)}
      </div>
    );
  }
  const tasks = ctx.data.tasks;
  if (tasks.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-border bg-card/40 p-6 text-center text-xs text-muted-foreground">
        {t(($) => $.plan_timeline_empty)}
      </div>
    );
  }
  // Partition: terminal runs first (DESC), then in-flight. Both
  // buckets render with the same row shape — only the status pill
  // color differs. We keep the partition explicit so the visual
  // hierarchy matches the user's mental model.
  const terminal: typeof tasks = [];
  const inflight: typeof tasks = [];
  for (const task of tasks) {
    if (
      task.status === "completed" ||
      task.status === "failed" ||
      task.status === "cancelled"
    ) {
      terminal.push(task);
    } else {
      inflight.push(task);
    }
  }
  // Switch on the LabTaskBrief.status enum (per Go-side
  // server/internal/handler/lab.go). The previous
  // `t((s) => statusKey.split('.').reduce(...))` selector walked
  // the resources proxy by a closure-local dynamic key — when the
  // leaf is a translated string (i18next v22+) the selector
  // returns a plain string instead of the proxy, keysFromSelector
  // reads [PATH_KEY] from a string → undefined, and the very next
  // line in i18next's internals throws TypeError. The error
  // escapes React's render pass and unmounts the surrounding tree
  // (the same failure mode documented in the 2026-07-14
  // AppSidebar incident). Switch + explicit literal t() calls
  // sidesteps the dynamic-key contract entirely. Every status in
  // the LabTaskBrief union is enumerated; a future addition must
  // add a case here AND a locale key in all 4 locales.
  const renderRow = (task: (typeof tasks)[number], seq: number) => {
    const renderStatus = () => {
      switch (task.status) {
        case "queued":
          return t(($) => $.plan_timeline_status_queued);
        case "dispatched":
          return t(($) => $.plan_timeline_status_dispatched);
        case "running":
          return t(($) => $.plan_timeline_status_running);
        case "preparing":
          return t(($) => $.plan_timeline_status_preparing);
        case "waiting_local_directory":
          return t(($) => $.plan_timeline_status_waiting_local_directory);
        case "completed":
          return t(($) => $.plan_timeline_status_completed);
        case "failed":
          return t(($) => $.plan_timeline_status_failed);
        case "cancelled":
          return t(($) => $.plan_timeline_status_cancelled);
        case "deferred":
          return t(($) => $.plan_timeline_status_deferred);
        default:
          return task.status;
      }
    };
    return (
      <li
        key={task.id}
        ref={deepLink.rowRef(task.id)}
        className={
          "flex flex-col gap-1 rounded-md border border-border bg-background/40 px-3 py-2 text-xs" +
          (deepLink.isDeepLinked(task.id)
            ? " bg-primary/5 ring-1 ring-primary/30"
            : "")
        }
      >
        <div className="flex items-center gap-2">
          <span className="font-mono text-[10px] text-muted-foreground">
            {t(($) => $.plan_timeline_run_label, { seq })}
          </span>
          <span
            className={
              "rounded px-1.5 py-0.5 text-[10px] font-medium " +
              statusBadgeClass(task.status)
            }
          >
            {renderStatus()}
          </span>
          {task.duration_ms !== null ? (
            <span className="text-[10px] text-muted-foreground">
              {t(($) => $.plan_timeline_run_duration)}: {formatMs(task.duration_ms)}
            </span>
          ) : null}
          <span className="ml-auto text-[10px] text-muted-foreground">
            {new Date(task.created_at).toLocaleString()}
          </span>
        </div>
        {task.trigger_summary ? (
          <div className="text-[11px] text-muted-foreground">
            <span className="font-medium">
              {t(($) => $.plan_timeline_run_trigger)}:{" "}
            </span>
            {task.trigger_summary}
          </div>
        ) : null}
        {/* 0.5.97: the structured result body (charts / predictions /
            code / summary) is rendered by the shared LabTaskResultView —
            the same renderer the issue-side summary card and the
            latest-result panel use, so every surface draws a run's
            deliverables identically. */}
        <LabTaskResultView task={task} />
      </li>
    );
  };

  return (
    <section className="rounded-xl border border-border bg-card p-3">
      <header className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">
          {t(($) => $.plan_timeline_header)}
        </span>
      </header>
      <ul className="flex flex-col gap-2">
        {terminal.map((task, idx) => renderRow(task, terminal.length - idx))}
        {inflight.length > 0 ? (
          <>
            <li className="px-3 py-1 text-[10px] uppercase tracking-wide text-muted-foreground">
              ·· {t(($) => $.plan_timeline_status_running)} ··
            </li>
            {inflight.map((task) => renderRow(task, 0))}
          </>
        ) : null}
      </ul>
    </section>
  );
}

function statusBadgeClass(status: string): string {
  switch (status) {
    case "completed":
      return "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200";
    case "failed":
      return "bg-destructive/15 text-destructive";
    case "cancelled":
      return "bg-muted text-muted-foreground";
    case "running":
    case "dispatched":
    case "preparing":
      return "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200";
    default:
      return "bg-secondary text-secondary-foreground";
  }
}

function formatMs(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  const rs = s % 60;
  return `${m}m${rs.toString().padStart(2, "0")}s`;
}

function Header({
  active,
  onTabChange,
}: {
  active: LabTab;
  onTabChange: (next: LabTab) => void;
}) {
  const { t } = useT("layout");
  const { t: tLab } = useT("claude-lab");
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">{t(($) => $.sidebar.experimental_group)}</span>
        <span className="text-muted-foreground/60">/</span>
        <span>{t(($) => $.sidebar.experimental_claude_science_lab)}</span>
      </div>
      <nav className="ml-6 flex items-center gap-1">
        {TAB_ORDER.map((key) => (
          <button
            key={key}
            type="button"
            onClick={() => onTabChange(key)}
            className={
              "rounded-md px-2.5 py-1 text-xs transition-colors " +
              (active === key
                ? "bg-accent text-accent-foreground"
                : "text-muted-foreground hover:bg-accent/40 hover:text-foreground")
            }
            aria-pressed={active === key}
          >
            {(tLab as unknown as (k: string) => string)(`tab_${key}`)}
          </button>
        ))}
      </nav>
    </header>
  );
}

function Intro() {
  const { t } = useT("claude-lab");
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        {t(($) => $.title)}
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        {t(($) => $.subtitle)}
      </p>
    </section>
  );
}

function FlagOffNotice() {
  const { t } = useT("claude-lab");
  return (
    <section className="rounded-xl border border-amber-200 bg-amber-50/40 p-5 dark:border-amber-800 dark:bg-amber-950/30">
      <p className="text-sm font-medium text-amber-800 dark:text-amber-200">
        {t(($) => $.flag_off_title)}
      </p>
      <p className="mt-1 text-xs text-amber-700 dark:text-amber-300">
        {t(($) => $.flag_off_body)}
      </p>
    </section>
  );
}

// LabAgentFromIssue — 0.3.45.8: replaces the old LabAgentLockBar picker.
// The lab's running agent is now derived from the issue itself: an
// issue bound to claude_science_lab has `assignee_type='agent'` +
// `assignee_id=<leader>`, set when the user picked the lab in the issue
// detail PropRow (see packages/views/issues/components/pickers/lab-picker.tsx).
// That agent IS the lab's leader; the user does not pick a separate lab
// agent inside the lab panel — that was the 0.3.29 design but it let
// users bind an unrelated agent to an issue without going through the
// normal IssueDetail flow, which made the lab-agent lock leak across
// surfaces and confused the per-issue task panel.
//
// Contract:
//   - When selectedIssueId is null → render the existing hint (no lab).
//   - When selectedIssueId is set → render a read-only "由 {agentName}
//     驱动" strip sourced from `api.getIssue(id).assignee_id`.
//   - No `<select>`, no agent list, no clear button.
//   - The Code tab pulls agent_id from this same query rather than
//     from a separate piece of state.
function LabAgentFromIssue({
  wsId,
  selectedIssueId,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
}) {
  const { t } = useT("claude-lab");
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);

  const issueQ = useQuery({
    queryKey: ["claude-lab-issue", wsId, selectedIssueId] as const,
    enabled: enabled && !!wsId && !!selectedIssueId,
    staleTime: 30_000,
    queryFn: async () => {
      const r = await api.getIssue(selectedIssueId as string);
      return r;
    },
  });

  const issue = issueQ.data;
  const agentId = issue?.assignee_type === "agent" ? issue.assignee_id : null;
  const agentLabel = agentId?.slice(0, 8) ?? "—";

  // 0.3.45.8: pull the agent's display name from the cached workspace
  // agent list so the strip shows "由 research 驱动" instead of the
  // truncated UUID. Falls back to the truncated ID while the list is
  // loading (first paint).
  //
  // 0.3.66 (rules-of-hooks): this second useQuery MUST run on every render,
  // so it is declared BEFORE the `!selectedIssueId` early return below.
  // Pre-0.3.66 the early return sat between the two queries, so a
  // selectedIssueId that flipped null↔set within one mount changed the hook
  // count and crashed React ("Rendered fewer hooks than expected"). Both
  // queries stay disabled when they shouldn't fire (issueQ on
  // `!!selectedIssueId`, agentName on `!!agentId`), so behaviour is unchanged.
  const agentName = useQuery({
    queryKey: ["claude-lab-agents-lookup", wsId, agentId] as const,
    enabled: !!wsId && !!agentId,
    staleTime: 60_000,
    queryFn: async () => {
      const list = await api.listAgents({ workspace_id: wsId ?? undefined });
      return list ?? [];
    },
  });
  const resolvedName = agentName.data?.find((a) => a.id === agentId)?.name ?? agentLabel;

  if (!selectedIssueId) {
    return (
      <section className="rounded-xl border border-dashed border-border bg-card/40 p-3 text-xs text-muted-foreground">
        {t(($) => $.agent_lock_unlocked_hint)}
      </section>
    );
  }

  return (
    <section className="rounded-xl border border-border bg-card p-3">
      <div className="flex items-center gap-3">
        <Lock className="size-3.5 text-amber-600" aria-hidden />
        <span className="text-xs font-medium text-foreground">
          {t(($) => $.agent_lock_header)}
        </span>
        <span
          className="flex-1 rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground/90"
          data-testid="claude-lab-agent-display"
        >
          {agentId ? (
            <span className="font-medium">{resolvedName}</span>
          ) : (
            <span className="text-muted-foreground">
              {t(($) => $.agent_lock_no_assignee_hint) ?? "请先在 issue 面板选择「实验插件」以指派实验 leader"}
            </span>
          )}
        </span>
        <span className="rounded-md bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
          {t(($) => $.agent_lock_locked)}
        </span>
      </div>
    </section>
  );
}

interface ActiveTabProps {
  tab: LabTab;
  wsId: string | null;
  selectedIssueId: string | null;
  onSelectIssue: (id: string) => void;
}

function ActiveTab({
  tab,
  wsId,
  selectedIssueId,
  onSelectIssue,
}: ActiveTabProps) {
  switch (tab) {
    case "plan":
      return <PlanTab wsId={wsId} selectedIssueId={selectedIssueId} onSelectIssue={onSelectIssue} />;
    case "artifact":
      return <ArtifactTab wsId={wsId} selectedIssueId={selectedIssueId} />;
    case "forecast":
      return <ForecastTab selectedIssueId={selectedIssueId} />;
    case "code":
      return (
        <CodeTab
          wsId={wsId}
          selectedIssueId={selectedIssueId}
        />
      );
    case "knowledge":
      return <KnowledgeTab wsId={wsId} selectedIssueId={selectedIssueId} />;
  }
}

function PlanTab({
  wsId,
  selectedIssueId,
  onSelectIssue,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
  onSelectIssue: (id: string) => void;
}) {
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);
  const { t } = useT("claude-lab");
  const router = useNavigation();
  // 0.3.37: clicking a plan row should open the main issue detail
  // (the same row IS a regular `issue` row tagged lab_source=
  // claude_science_lab). The "select_issue" button still binds the
  // row to the lab's Forecast/Artifact/Knowledge tabs in-place;
  // the row's title now doubles as the jump link to the issue
  // detail page so users get the same UX they get from the main
  // task list — click the title to read the full description,
  // click the side button to scope lab tabs to that issue.
  // 0.3.36: hide audit / verification rows by default. Titles wrapped
  // in `[...]` (the convention used by the 0.3.33 audit + per-ship
  // smoke tests) are noise in the Plan tab — the user is here to do
  // research, not inspect test fixtures. A checkbox exposes them
  // when needed.
  const [showAudit, setShowAudit] = useState(false);
  const issues = useQuery({
    queryKey: ["claude-lab-issues", wsId, CLAUDE_LAB_SOURCE],
    enabled: enabled && !!wsId,
    staleTime: 30_000,
    // 0.3.45.8 (P0#3.7 sibling): the Plan tab renders each row's issue
    // status badge ({it.status}). Poll every 5s while any issue is still
    // in flight so an in_review → done flip surfaces promptly; otherwise
    // fall back to the 30s idle beat. No WS invalidation reaches this
    // custom key, so idle must still poll (not `false`).
    refetchInterval: (query) =>
      (query.state.data?.issues ?? []).some((it) =>
        LIVE_LAB_ISSUE_STATUSES.has(it.status),
      )
        ? 5_000
        : 30_000,
    queryFn: async () => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-lab/issues?workspace_id=${encodeURIComponent(wsId ?? "")}&lab=${encodeURIComponent(CLAUDE_LAB_SOURCE)}`,
      );
      if (!r.ok) throw new Error(`issues ${r.status}`);
      return (await r.json()) as LabIssuesResponse;
    },
  });

  if (!wsId) {
    return (
      <EmptyHint
        title={t(($) => $.title_plan)}
        body={t(($) => $.no_workspace)}
      />
    );
  }
  if (issues.isLoading) {
    return <LoadingHint title={t(($) => $.title_plan)} />;
  }
  if (issues.isError) {
    return (
      <ErrorHint
        title={t(($) => $.title_plan)}
        body={`${t(($) => $.loading_failed)}: ${(issues.error as Error).message}`}
      />
    );
  }
  const allRows = issues.data?.issues ?? [];
  const rows = showAudit
    ? allRows
    : allRows.filter((it) => !/^\s*\[(audit|test|verify|smoke)/i.test(it.title));
  const auditCount = allRows.length - rows.length;
  if (rows.length === 0) {
    return (
      <EmptyHint
        title={t(($) => $.title_plan)}
        body={t(($) => $.plan_empty_body)}
      />
    );
  }
  return (
    <section className="rounded-xl border border-border bg-card p-5">
      <header className="mb-3 flex items-center justify-between text-xs text-muted-foreground">
        <div className="inline-flex items-center gap-2">
          <Sparkles className="size-4" aria-hidden />
          <span className="font-medium text-foreground">{t(($) => $.title_plan)}</span>
          <span>· {rows.length} 条</span>
          {auditCount > 0 && (
            <label className="ml-2 inline-flex cursor-pointer items-center gap-1 text-[10px]">
              <input
                type="checkbox"
                checked={showAudit}
                onChange={(e) => setShowAudit(e.target.checked)}
                className="size-3 accent-primary"
              />
              <span>含 {auditCount} 条 audit</span>
            </label>
          )}
        </div>
      </header>
      <ul className="flex flex-col gap-3">
        {rows.map((it) => {
          // 0.3.37: clicking the row title jumps to the main issue
          // detail page (the same row IS a regular `issue` tagged
          // lab_source=claude_science_lab). The "select_issue" button
          // still binds the row to the lab's Forecast/Artifact/
          // Knowledge tabs in-place. getCurrentSlug is the platform-
          // side source of truth for the active workspace slug;
          // pre-workspace fallback routes to /experimental/claude-lab
          // (the current behavior).
          const slug = getCurrentSlug();
          const issueHref =
            slug != null
              ? paths.workspace(slug).issueDetail(it.id)
              : `/experimental/claude-lab?issue=${encodeURIComponent(it.id)}`;
          return (
          <li
            key={it.id}
            className={
              "rounded-lg border bg-background/40 p-3 " +
              (selectedIssueId === it.id ? "border-primary" : "border-border")
            }
          >
            <div className="flex items-center gap-3">
              <span className="font-mono text-[10px] text-muted-foreground">
                #{it.number}
              </span>
              <button
                type="button"
                onClick={() => router.push(issueHref)}
                className="flex-1 cursor-pointer truncate text-left text-sm font-medium text-foreground hover:underline"
                aria-label={t(($) => $.title_plan)}
              >
                {it.title}
              </button>
              <span className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                {it.status}
              </span>
              <button
                type="button"
                onClick={() => onSelectIssue(it.id)}
                className={
                  "rounded-md px-2 py-1 text-xs " +
                  (selectedIssueId === it.id
                    ? "bg-primary text-primary-foreground"
                    : "border border-border bg-background hover:bg-muted")
                }
              >
                {selectedIssueId === it.id
                  ? t(($) => $.selected)
                  : t(($) => $.select_issue)}
              </button>
            </div>
            {it.description ? (
              <p className="mt-2 line-clamp-2 text-xs text-muted-foreground">
                {it.description}
              </p>
            ) : null}
          </li>
          );
        })}
      </ul>
    </section>
  );
}

function ArtifactTab({
  wsId,
  selectedIssueId,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
}) {
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);
  const { t } = useT("claude-lab");
  const sessions = useQuery({
    queryKey: ["claude-lab-runtime-sessions", wsId, selectedIssueId],
    enabled: enabled && !!wsId && !!selectedIssueId,
    staleTime: 15_000,
    // 0.3.45.8 (P0#3.7 sibling): the Artifact tab shows per-session status
    // badges. Poll every 5s while a session is still running; fall back to
    // the 15s idle beat when all sessions are terminal. No WS push reaches
    // this custom key, so idle keeps a baseline poll rather than `false`.
    //
    // 0.3.49 (Mode B variant): the canonical Mode B idle cadence from
    // the 0.3.45.9 lineage is `30_000` for "no WS + tab-cross" keys.
    // Claude Lab session queries (`runtime-sessions` and `code-sessions`,
    // both below) deliberately use a tighter `15_000` because they are
    // per-issue scoped — when the user is actively viewing the Artifact
    // or Code tab they need new session rows to surface within a single
    // reading beat. Mode B `30_000` would still be correct under
    // tab-cross semantics (i.e. users opening one issue, switching
    // away, coming back), but Claude Lab's UX assumption is "you are
    // here for this issue right now". If that assumption ever
    // changes, switch to `30_000` and update the comment block below.
    refetchInterval: (query) =>
      (query.state.data?.sessions ?? []).some((s) =>
        LIVE_LAB_SESSION_STATUSES.has(s.status),
      )
        ? 5_000
        : 15_000,
    queryFn: async () => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-runtime/sessions/by-issue?workspace_id=${encodeURIComponent(wsId ?? "")}&issue_id=${encodeURIComponent(selectedIssueId ?? "")}&limit=20`,
      );
      if (!r.ok) throw new Error(`by-issue ${r.status}`);
      return (await r.json()) as RuntimeSessionsResponse;
    },
  });

  if (!wsId) {
    return (
      <EmptyHint
        title={t(($) => $.title_artifact)}
        body={t(($) => $.no_workspace)}
      />
    );
  }
  if (!selectedIssueId) {
    return (
      <EmptyHint
        title={t(($) => $.title_artifact)}
        body={t(($) => $.agent_lock_issue_required)}
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <section className="rounded-xl border border-border bg-card p-4">
        <header className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
          <FlaskConical className="size-4" aria-hidden />
          <span className="font-medium text-foreground">
            {t(($) => $.artifact_sessions_header)}
          </span>
          <span>· {sessions.data?.total ?? 0} 条</span>
        </header>
        {sessions.isLoading ? (
          <LoadingHint title="" />
        ) : sessions.isError ? (
          <ErrorHint
            title=""
            body={`${t(($) => $.artifact_load_error)}: ${(sessions.error as Error).message}`}
          />
        ) : (sessions.data?.sessions ?? []).length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.artifact_empty_body)}
          </p>
        ) : (
          <ul className="flex flex-col gap-2">
            {(sessions.data?.sessions ?? []).map((s) => (
              <li
                key={s.id}
                className="flex items-center gap-3 rounded-md border border-border bg-background/40 px-3 py-2 text-xs"
              >
                <span className="font-mono text-[10px] text-muted-foreground">
                  {s.id.slice(0, 8)}
                </span>
                <span className="flex-1 truncate font-medium">{s.status}</span>
                {s.exit_code !== null ? (
                  <span className="text-muted-foreground">exit {s.exit_code}</span>
                ) : null}
                {s.duration_ms !== null ? (
                  <span className="text-muted-foreground">{s.duration_ms} ms</span>
                ) : null}
                <span className="text-muted-foreground">
                  {new Date(s.created_at).toLocaleString()}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <ExperimentalArtifactView workspaceId={wsId} />
    </div>
  );
}

function ForecastTab({ selectedIssueId: _selectedIssueId }: { selectedIssueId: string | null }) {
  // 0.3.29: selectedIssueId is intentionally captured in the tab
  // signature so the Forecast tab data flow can be re-pointed at
  // issue-scoped predictions without changing the call site. Today
  // the chart still reads the lab-level SSE stream; the per-issue
  // channel ships in 0.3.30 alongside issue-scoped oracle calls.
  const forecastUrl = "/api/experimental/claude-science-lab/forecast/stream";
  const { t } = useT("claude-lab");
  return (
    <div className="flex flex-col gap-4">
      <section className="rounded-xl border border-border bg-card overflow-hidden">
        <header className="border-b border-border bg-background/40 px-4 py-2 text-xs text-muted-foreground">
          {t(($) => $.forecast_chart_header)}
        </header>
        <div className="h-[180px] p-3">
          <ForecastProbabilityChart url={forecastUrl} />
        </div>
      </section>
      <section className="rounded-xl border border-border bg-card overflow-hidden">
        <div className="h-[480px]">
          <ForecastStreamView url={forecastUrl} />
        </div>
      </section>
    </div>
  );
}

// ForecastProbabilityChart — 0.3.29 SVG line chart of the live
// forecast stream's probability field. The forecast SSE consumer
// updates the underlying state; we read it via a small adapter hook
// (`useForecastFrames`) so this chart shares the same wire parser as
// `<ForecastStreamView>` without duplicating the SSE plumbing.
function ForecastProbabilityChart({ url }: { url: string }) {
  const frames = useForecastFrames(url);
  const data = useMemo(() => frames.slice(0, 20).reverse(), [frames]);
  const { t } = useT("claude-lab");
  if (data.length < 2) {
    return (
      <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
        {t(($) => $.forecast_chart_waiting)}
      </div>
    );
  }
  const W = 600;
  const H = 140;
  const padX = 12;
  const padY = 8;
  const xs = data.map((_, i) => padX + (i * (W - padX * 2)) / Math.max(1, data.length - 1));
  const ys = data.map((p) => padY + (1 - Math.max(0, Math.min(1, p.probability))) * (H - padY * 2));
  const points = xs.map((x, i) => `${x.toFixed(1)},${ys[i].toFixed(1)}`).join(" ");
  const last = data[data.length - 1];
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      className="h-full w-full"
      role="img"
      aria-label={t(($) => $.forecast_chart_aria)}
    >
      <rect x={0} y={0} width={W} height={H} fill="transparent" />
      <line x1={padX} y1={H - padY} x2={W - padX} y2={H - padY} stroke="var(--border)" strokeDasharray="3 3" />
      <line x1={padX} y1={padY} x2={W - padX} y2={padY} stroke="var(--border)" strokeDasharray="3 3" />
      <polyline
        points={points}
        fill="none"
        stroke="var(--primary)"
        strokeWidth={2}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
      {xs.map((x, i) => (
        <circle key={i} cx={x} cy={ys[i]} r={2.4} fill="var(--primary)" />
      ))}
      <text x={padX} y={padY + 8} fontSize={9} fill="var(--muted-foreground)">
        {Math.round(last.probability * 100)}%
      </text>
    </svg>
  );
}

// useForecastFrames — duplicate SSE parser shared with
// <ForecastStreamView>. We keep this hook intentionally minimal (no
// keep-alive handling, no fetch-error surfacing) because the chart
// only needs to render; the ticker UI is the canonical reader.
function useForecastFrames(url: string): { id: string; probability: number; createdAt: string }[] {
  const [frames, setFrames] = useState<{ id: string; probability: number; createdAt: string }[]>([]);
  useEffect(() => {
    if (!url) return;
    const controller = new AbortController();
    let cancelled = false;
    (async () => {
      try {
        const resp = await api.rawRequest(url, {
          signal: controller.signal,
          cache: "no-store",
          headers: { Accept: "text/event-stream" },
        });
        if (!resp.ok || !resp.body) return;
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";
        while (!cancelled) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let idx;
          while ((idx = buf.indexOf("\n\n")) !== -1) {
            const block = buf.slice(0, idx);
            buf = buf.slice(idx + 2);
            const dataLine = block.split("\n").find((l) => l.startsWith("data:"));
            if (!dataLine) continue;
            try {
              const env = JSON.parse(dataLine.slice(5).trim()) as { id: string; probability: number; createdAt: string };
              if (typeof env?.id === "string") {
                setFrames((prev) => [env, ...prev].slice(0, 50));
              }
            } catch {
              // ignore keep-alive / non-JSON
            }
          }
        }
      } catch {
        // Chart is best-effort; the ticker surfaces errors.
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [url]);
  return frames;
}

function CodeTab({
  wsId,
  selectedIssueId,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
}) {
  const { t } = useT("claude-lab");
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);
  const queryClient = useQueryClient();
  const [runState, setRunState] = useState<"idle" | "running" | "done" | "error">("idle");

  const sessions = useQuery({
    queryKey: ["claude-lab-code-sessions", wsId, selectedIssueId],
    enabled: enabled && !!wsId && !!selectedIssueId,
    staleTime: 15_000,
    // 0.3.45.8 (P0#3.7 sibling): the Code tab shows per-session status
    // badges. Poll every 5s while a session is still running; fall back to
    // the 15s idle beat when all sessions are terminal. No WS push reaches
    // this custom key, so idle keeps a baseline poll rather than `false`.
    //
    // 0.3.49 (Mode B variant): mirrors `claude-lab-runtime-sessions`
    // above — both per-issue scoped Claude Lab session keys use a
    // deliberate `15_000` instead of the canonical Mode B `30_000`.
    // See the longer rationale at the runtime-sessions block.
    refetchInterval: (query) =>
      (query.state.data?.sessions ?? []).some((s) =>
        LIVE_LAB_SESSION_STATUSES.has(s.status),
      )
        ? 5_000
        : 15_000,
    queryFn: async () => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-runtime/sessions/by-issue?workspace_id=${encodeURIComponent(wsId ?? "")}&issue_id=${encodeURIComponent(selectedIssueId ?? "")}&limit=20`,
      );
      if (!r.ok) throw new Error(`by-issue ${r.status}`);
      return (await r.json()) as RuntimeSessionsResponse;
    },
  });

  // 0.3.45.8: the running agent is sourced from the bound issue's
  // assignee, NOT from a separate lab-agent lock state. This guarantees
  // the lab's "Run for me" call executes on the exact agent the user
  // bound via IssueDetail's LabPicker — no second source of truth.
  const issueQ = useQuery({
    queryKey: ["claude-lab-issue", wsId, selectedIssueId] as const,
    enabled: enabled && !!wsId && !!selectedIssueId,
    staleTime: 30_000,
    queryFn: async () => api.getIssue(selectedIssueId as string),
  });
  const agentId =
    issueQ.data?.assignee_type === "agent" ? issueQ.data.assignee_id : null;

  if (!wsId || !selectedIssueId) {
    return (
      <EmptyHint
        title={t(($) => $.title_code)}
        body={t(($) => $.agent_lock_issue_required)}
      />
    );
  }

  const runDisabled = !agentId || runState === "running";

  const onRunForMe = async () => {
    if (!agentId) return;
    setRunState("running");
    try {
      const r = await api.rawRequest("/api/experimental/claude-science-runtime/execute", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          workspace_id: wsId,
          agent_id: agentId,
          issue_id: selectedIssueId,
          language: "python",
          // 0.3.29 "Run for me" — emits a self-describing JSON blob so
          // the resulting stdout + artifact trace show the issue the
          // run was bound to without any plumbing on the caller side.
          code:
            "import json, sys\n" +
            "payload = {'hello': 'claude-lab', 'issue_id': '" + selectedIssueId + "'}\n" +
            "print(json.dumps(payload, ensure_ascii=False))\n",
        }),
      });
      if (!r.ok) {
        setRunState("error");
        return;
      }
      setRunState("done");
      void queryClient.invalidateQueries({
        queryKey: ["claude-lab-code-sessions", wsId, selectedIssueId],
      });
      void queryClient.invalidateQueries({
        queryKey: ["claude-lab-runtime-sessions", wsId, selectedIssueId],
      });
    } catch {
      setRunState("error");
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <section className="rounded-xl border border-border bg-card p-4">
        <header className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
          <span className="font-medium text-foreground">
            {t(($) => $.title_code)}
          </span>
          <button
            type="button"
            onClick={onRunForMe}
            disabled={runDisabled}
            aria-busy={runState === "running"}
            title={
              !agentId
                ? (t(($) => $.agent_lock_no_assignee_hint) ??
                  "请先在 issue 面板选择「实验插件」以指派实验 leader")
                : runState === "running"
                  ? t(($) => $.code_run_running)
                  : t(($) => $.code_run_button)
            }
            className={
              "inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs " +
              (runDisabled
                ? "cursor-not-allowed border border-border bg-muted text-muted-foreground"
                : "border border-border bg-background hover:bg-muted")
            }
          >
            {runState === "running" ? (
              <Loader2 className="size-3 animate-spin" aria-hidden />
            ) : (
              <Play className="size-3" aria-hidden />
            )}
            {runState === "running"
              ? t(($) => $.code_run_running)
              : t(($) => $.code_run_button)}
          </button>
        </header>
        <p className="text-xs text-muted-foreground">
          {t(($) => $.code_intro_with_count, { count: sessions.data?.total ?? 0 })}
        </p>
        {!agentId ? (
          <p className="mt-2 text-[10px] text-amber-700 dark:text-amber-300">
            {t(($) => $.agent_lock_no_assignee_hint) ??
              "请先在 issue 面板选择「实验插件」以指派实验 leader"}
          </p>
        ) : null}
        {runState === "done" ? (
          <p className="mt-2 text-[10px] text-emerald-700 dark:text-emerald-300">
            {t(($) => $.code_run_done)}
          </p>
        ) : null}
        {runState === "error" ? (
          <p className="mt-2 text-[10px] text-destructive">
            {t(($) => $.code_run_error)}
          </p>
        ) : null}
      </section>
      {sessions.data?.sessions && sessions.data.sessions.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {sessions.data.sessions.map((s) => (
            <li
              key={s.id}
              className="rounded-md border border-border bg-background/40 p-3 text-xs"
            >
              <div className="mb-1 flex items-center gap-2">
                <span className="font-mono text-[10px] text-muted-foreground">
                  {s.id.slice(0, 8)}
                </span>
                <span className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[10px]">
                  {s.language}
                </span>
                <span className="text-muted-foreground">{s.status}</span>
                {s.duration_ms !== null ? (
                  <span className="text-muted-foreground">{s.duration_ms} ms</span>
                ) : null}
              </div>
              {s.stdout ? (
                <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 p-2 font-mono text-[11px]">
                  {s.stdout.slice(0, 2000)}
                </pre>
              ) : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted-foreground">{t(($) => $.code_sessions_empty)}</p>
      )}
    </div>
  );
}

function KnowledgeTab({
  wsId,
  selectedIssueId,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
}) {
  const { t } = useT("claude-lab");
  // 0.3.29 ships a lightweight knowledge surface: the bundled
  // claude-science skill catalogue is filtered for entries relevant
  // to a Claude Lab issue. The full 5-connector literature search
  // (PubMed / arXiv / Crossref / OpenAlex / Semantic Scholar) is
  // tracked for 0.3.30.
  const skills = useQuery({
    queryKey: ["claude-lab-skills", selectedIssueId],
    enabled: !!selectedIssueId,
    staleTime: 60_000,
    queryFn: async () => {
      const r = await api.rawRequest("/api/experimental/claude-science/skills");
      if (!r.ok) throw new Error(`skills ${r.status}`);
      const data = (await r.json()) as { skills: { name: string; description?: string }[] };
      return data.skills ?? [];
    },
  });

  if (!wsId || !selectedIssueId) {
    return (
      <EmptyHint
        title={t(($) => $.title_knowledge)}
        body={t(($) => $.agent_lock_issue_required)}
      />
    );
  }
  return (
    <section className="rounded-xl border border-border bg-card p-4">
      <header className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
        <Sparkles className="size-4" aria-hidden />
        <span className="font-medium text-foreground">{t(($) => $.title_knowledge)}</span>
        <span>· {t(($) => $.knowledge_header)}</span>
      </header>
      {skills.isLoading ? (
        <LoadingHint title="" />
      ) : skills.isError ? (
        <ErrorHint title="" body={`${t(($) => $.knowledge_load_error)}: ${(skills.error as Error).message}`} />
      ) : (skills.data ?? []).length === 0 ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.knowledge_empty)}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {(skills.data ?? []).slice(0, 10).map((s) => (
            <li
              key={s.name}
              className="rounded-md border border-border bg-background/40 p-3 text-xs"
            >
              <div className="font-medium">{s.name}</div>
              {s.description ? (
                <p className="mt-1 text-muted-foreground">{s.description}</p>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function LoadingHint({ title }: { title: string }) {
  const { t } = useT("claude-lab");
  return (
    <section className="rounded-xl border border-border bg-card p-8">
      <div className="flex items-center gap-2 text-foreground">
        <Loader2 className="size-5 animate-spin" aria-hidden />
        {title ? <h2 className="text-base font-semibold">{title}</h2> : null}
      </div>
      <p className="mt-3 text-sm text-muted-foreground">{t(($) => $.loading)}</p>
    </section>
  );
}

function ErrorHint({ title, body }: { title: string; body: string }) {
  return (
    <section className="rounded-xl border border-destructive bg-destructive/5 p-5">
      <div className="flex items-center gap-2 text-foreground">
        <FlaskConical className="size-5 text-destructive" aria-hidden />
        {title ? <h2 className="text-base font-semibold">{title}</h2> : null}
      </div>
      <p className="mt-3 text-sm text-destructive">{body}</p>
    </section>
  );
}

function EmptyHint({ title, body }: { title: string; body: string }) {
  return (
    <section className="rounded-xl border border-dashed border-border bg-card p-8">
      <div className="flex items-center gap-2 text-foreground">
        <ArrowRight className="size-5 text-muted-foreground" aria-hidden />
        <h2 className="text-base font-semibold">{title}</h2>
      </div>
      <p className="mt-3 text-sm text-muted-foreground">{body}</p>
    </section>
  );
}

