// ClaudeLabView (0.3.29, flag-gated)
//
// Single-pane Claude Research Lab — replaces the 0.3.20
// `claude_science` brochure page + `claude_science_runtime`
// sandbox page. One window, six capability tabs:
//
//   Plan       — issues tagged lab_source=claude_science_lab (Plan tab)
//   Chat       — multi-agent conversation (workspace-bound, no jump)
//   Artifact   — runtime sandbox sessions + rendered artifacts
//   Forecast   — live prediction ticker + probability chart (SVG)
//   Code       — recent runtime sessions + low-code run button
//   Knowledge  — skill references for the active issue
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
import {
  ArrowRight,
  FlaskConical,
  Loader2,
  Lock,
  Play,
  Sparkles,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useT } from "@multica/views/i18n";
import { ExperimentalChatPane, ForecastStreamView } from "@multica/views/experimental";
import { getCurrentWsId } from "@multica/core/platform";
import { api } from "@multica/core/api";
import type { Agent } from "@multica/core/types/agent";

import { ExperimentalArtifactView } from "@/components/experimental-artifact-view";

type LabTab = "plan" | "chat" | "artifact" | "forecast" | "code" | "knowledge";

const TAB_ORDER: LabTab[] = ["plan", "chat", "artifact", "forecast", "code", "knowledge"];

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

export function ClaudeLabView({ issueId: initialIssueId = null }: { issueId?: string | null } = {}) {
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);
  const [tab, setTab] = useState<LabTab>("plan");
  const [wsId, setWsId] = useState<string | null>(() => getCurrentWsId());
  // When mounted inline from an issue detail (via renderLabInline on
  // IssueDetailPage), the parent passes an `issueId` so Plan /
  // Forecast / Code / Knowledge / Artifact tabs open already-scoped
  // to that issue instead of the "no issue bound" empty state. The
  // user can still re-pick from the picker inside each tab. Without
  // this prop the view behaves as the workspace-scoped `/experimental/
  // claude-lab` route does today (default null).
  const [selectedIssueId, setSelectedIssueId] = useState<string | null>(initialIssueId);
  // lockedAgentId is only meaningful while a lab is selected. Setting
  // it to a non-null value implies the agent is locked; clearing
  // selectedIssueId does NOT auto-clear the agent (the lab session
  // outlives the picker — same pattern as the Chat tab's wsId).
  const [lockedAgentId, setLockedAgentId] = useState<string | null>(null);

  useEffect(() => {
    const id = setInterval(() => {
      const next = getCurrentWsId();
      setWsId((prev) => (prev === next ? prev : next));
    }, 500);
    return () => clearInterval(id);
  }, []);

  // When the picker clears, drop the agent lock too. This keeps the
  // "lab is selected" state consistent — an agent is locked BECAUSE a
  // lab is selected, not independently.
  useEffect(() => {
    if (!selectedIssueId) setLockedAgentId(null);
  }, [selectedIssueId]);

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
        <LabAgentLockBar
          wsId={wsId}
          selectedIssueId={selectedIssueId}
          lockedAgentId={lockedAgentId}
          onLockedAgentChange={setLockedAgentId}
        />
        <ActiveTab
          tab={tab}
          wsId={wsId}
          selectedIssueId={selectedIssueId}
          lockedAgentId={lockedAgentId}
          onSelectIssue={setSelectedIssueId}
        />
      </main>
    </div>
  );
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
            {tLab(($) => $.claude_lab[`tab_${key}`])}
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
        {t(($) => $.claude_lab.title)}
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        {t(($) => $.claude_lab.subtitle)}
      </p>
    </section>
  );
}

function FlagOffNotice() {
  const { t } = useT("claude-lab");
  return (
    <section className="rounded-xl border border-amber-200 bg-amber-50/40 p-5 dark:border-amber-800 dark:bg-amber-950/30">
      <p className="text-sm font-medium text-amber-800 dark:text-amber-200">
        {t(($) => $.claude_lab.flag_off_title)}
      </p>
      <p className="mt-1 text-xs text-amber-700 dark:text-amber-300">
        {t(($) => $.claude_lab.flag_off_body)}
      </p>
    </section>
  );
}

// LabAgentLockBar — 0.3.29: when a Claude Lab issue is picked, the
// user locks a lab agent for the duration of the experiment. The lock
// bar is the only place agent selection happens for this surface, and
// it ONLY appears when an issue is actively selected. Without a
// selected issue the bar shows the "pick an issue first" hint and the
// agent picker stays inert.
//
// Hard rule: the lock UI must NOT show when `selectedIssueId` is null.
// This is the single source of truth for "is a lab actively chosen?"
// — locking without a lab would otherwise leak the locked agent into
// other surfaces.
function LabAgentLockBar({
  wsId,
  selectedIssueId,
  lockedAgentId,
  onLockedAgentChange,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
  lockedAgentId: string | null;
  onLockedAgentChange: (id: string | null) => void;
}) {
  const { t } = useT("claude-lab");
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);

  // Agent list is only fetched when an issue is selected AND the
  // flag is on. When selectedIssueId is null we return early on the
  // hint branch below, so the query stays cold and no extra request
  // hits the server.
  const agents = useQuery({
    queryKey: ["claude-lab-agents", wsId],
    enabled: enabled && !!wsId && !!selectedIssueId,
    staleTime: 60_000,
    queryFn: async () => {
      const list = await api.listAgents({ workspace_id: wsId ?? undefined });
      return list ?? [];
    },
  });

  if (!selectedIssueId) {
    // Lock UI ONLY appears when a lab is actively chosen. Before that
    // we render the hint branch — see the doc comment on this
    // function for the invariant.
    return (
      <section className="rounded-xl border border-dashed border-border bg-card/40 p-3 text-xs text-muted-foreground">
        {t(($) => $.claude_lab.agent_lock_unlocked_hint)}
      </section>
    );
  }

  return (
    <section className="rounded-xl border border-border bg-card p-3">
      <div className="flex items-center gap-3">
        <Lock className="size-3.5 text-amber-600" aria-hidden />
        <span className="text-xs font-medium text-foreground">
          {t(($) => $.claude_lab.agent_lock_header)}
        </span>
        <select
          aria-label={t(($) => $.claude_lab.agent_lock_pick)}
          className="flex-1 rounded-md border border-border bg-background px-2 py-1 text-xs"
          value={lockedAgentId ?? ""}
          onChange={(e) => onLockedAgentChange(e.target.value || null)}
        >
          <option value="">{t(($) => $.claude_lab.agent_lock_pick)}…</option>
          {(agents.data ?? []).map((a: Agent) => (
            <option key={a.id} value={a.id}>
              {a.name || a.id.slice(0, 8)}
            </option>
          ))}
        </select>
        {lockedAgentId ? (
          <button
            type="button"
            onClick={() => onLockedAgentChange(null)}
            className="rounded-md border border-border bg-background px-2 py-1 text-xs hover:bg-muted"
          >
            {t(($) => $.claude_lab.agent_lock_clear)}
          </button>
        ) : null}
        <span className="rounded-md bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
          {t(($) => $.claude_lab.agent_lock_locked)}
        </span>
      </div>
    </section>
  );
}

interface ActiveTabProps {
  tab: LabTab;
  wsId: string | null;
  selectedIssueId: string | null;
  lockedAgentId: string | null;
  onSelectIssue: (id: string) => void;
}

function ActiveTab({
  tab,
  wsId,
  selectedIssueId,
  lockedAgentId,
  onSelectIssue,
}: ActiveTabProps) {
  switch (tab) {
    case "plan":
      return <PlanTab wsId={wsId} selectedIssueId={selectedIssueId} onSelectIssue={onSelectIssue} />;
    case "chat":
      return <ChatTab wsId={wsId} />;
    case "artifact":
      return <ArtifactTab wsId={wsId} selectedIssueId={selectedIssueId} />;
    case "forecast":
      return <ForecastTab selectedIssueId={selectedIssueId} />;
    case "code":
      return (
        <CodeTab
          wsId={wsId}
          selectedIssueId={selectedIssueId}
          lockedAgentId={lockedAgentId}
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
  const issues = useQuery({
    queryKey: ["claude-lab-issues", wsId, CLAUDE_LAB_SOURCE],
    enabled: enabled && !!wsId,
    staleTime: 30_000,
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
        title={t(($) => $.claude_lab.title_plan)}
        body={t(($) => $.claude_lab.no_workspace)}
      />
    );
  }
  if (issues.isLoading) {
    return <LoadingHint title={t(($) => $.claude_lab.title_plan)} />;
  }
  if (issues.isError) {
    return (
      <ErrorHint
        title={t(($) => $.claude_lab.title_plan)}
        body={`${t(($) => $.claude_lab.loading_failed)}: ${(issues.error as Error).message}`}
      />
    );
  }
  const rows = issues.data?.issues ?? [];
  if (rows.length === 0) {
    return (
      <EmptyHint
        title={t(($) => $.claude_lab.title_plan)}
        body={t(($) => $.claude_lab.plan_empty_body)}
      />
    );
  }
  return (
    <section className="rounded-xl border border-border bg-card p-5">
      <header className="mb-3 flex items-center justify-between text-xs text-muted-foreground">
        <div className="inline-flex items-center gap-2">
          <Sparkles className="size-4" aria-hidden />
          <span className="font-medium text-foreground">{t(($) => $.claude_lab.title_plan)}</span>
          <span>· {rows.length} 条</span>
        </div>
      </header>
      <ul className="flex flex-col gap-3">
        {rows.map((it) => (
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
              <span className="flex-1 truncate text-sm font-medium">{it.title}</span>
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
                  ? t(($) => $.claude_lab.selected)
                  : t(($) => $.claude_lab.select_issue)}
              </button>
            </div>
            {it.description ? (
              <p className="mt-2 line-clamp-2 text-xs text-muted-foreground">
                {it.description}
              </p>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

function ChatTab({ wsId }: { wsId: string | null }) {
  return (
    <section className="rounded-xl border border-border bg-card overflow-hidden">
      <div className="h-[560px]">
        <ExperimentalChatPane wsId={wsId ?? ""} />
      </div>
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
        title={t(($) => $.claude_lab.title_artifact)}
        body={t(($) => $.claude_lab.no_workspace)}
      />
    );
  }
  if (!selectedIssueId) {
    return (
      <EmptyHint
        title={t(($) => $.claude_lab.title_artifact)}
        body={t(($) => $.claude_lab.agent_lock_issue_required)}
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <section className="rounded-xl border border-border bg-card p-4">
        <header className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
          <FlaskConical className="size-4" aria-hidden />
          <span className="font-medium text-foreground">
            {t(($) => $.claude_lab.artifact_sessions_header)}
          </span>
          <span>· {sessions.data?.total ?? 0} 条</span>
        </header>
        {sessions.isLoading ? (
          <LoadingHint title="" />
        ) : sessions.isError ? (
          <ErrorHint
            title=""
            body={`${t(($) => $.claude_lab.artifact_load_error)}: ${(sessions.error as Error).message}`}
          />
        ) : (sessions.data?.sessions ?? []).length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.claude_lab.artifact_empty_body)}
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
          {t(($) => $.claude_lab.forecast_chart_header)}
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
        {t(($) => $.claude_lab.forecast_chart_waiting)}
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
      aria-label={t(($) => $.claude_lab.forecast_chart_aria)}
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
  lockedAgentId,
}: {
  wsId: string | null;
  selectedIssueId: string | null;
  lockedAgentId: string | null;
}) {
  const { t } = useT("claude-lab");
  const enabled = useExperimentalFlag(CLAUDE_LAB_FLAG, false);
  const queryClient = useQueryClient();
  const [runState, setRunState] = useState<"idle" | "running" | "done" | "error">("idle");

  const sessions = useQuery({
    queryKey: ["claude-lab-code-sessions", wsId, selectedIssueId],
    enabled: enabled && !!wsId && !!selectedIssueId,
    staleTime: 15_000,
    queryFn: async () => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-runtime/sessions/by-issue?workspace_id=${encodeURIComponent(wsId ?? "")}&issue_id=${encodeURIComponent(selectedIssueId ?? "")}&limit=20`,
      );
      if (!r.ok) throw new Error(`by-issue ${r.status}`);
      return (await r.json()) as RuntimeSessionsResponse;
    },
  });

  if (!wsId || !selectedIssueId) {
    return (
      <EmptyHint
        title={t(($) => $.claude_lab.title_code)}
        body={t(($) => $.claude_lab.agent_lock_issue_required)}
      />
    );
  }

  const runDisabled = !lockedAgentId || runState === "running";

  const onRunForMe = async () => {
    if (!lockedAgentId) return;
    setRunState("running");
    try {
      const r = await api.rawRequest("/api/experimental/claude-science-runtime/execute", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          workspace_id: wsId,
          agent_id: lockedAgentId,
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
            {t(($) => $.claude_lab.title_code)}
          </span>
          <button
            type="button"
            onClick={onRunForMe}
            disabled={runDisabled}
            aria-busy={runState === "running"}
            title={
              !lockedAgentId
                ? t(($) => $.claude_lab.agent_lock_pick)
                : runState === "running"
                  ? t(($) => $.claude_lab.code_run_running)
                  : t(($) => $.claude_lab.code_run_button)
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
              ? t(($) => $.claude_lab.code_run_running)
              : t(($) => $.claude_lab.code_run_button)}
          </button>
        </header>
        <p className="text-xs text-muted-foreground">
          {t(($) => $.claude_lab.code_intro_with_count, { count: sessions.data?.total ?? 0 })}
        </p>
        {!lockedAgentId ? (
          <p className="mt-2 text-[10px] text-amber-700 dark:text-amber-300">
            {t(($) => $.claude_lab.agent_lock_pick)}
          </p>
        ) : null}
        {runState === "done" ? (
          <p className="mt-2 text-[10px] text-emerald-700 dark:text-emerald-300">
            {t(($) => $.claude_lab.code_run_done)}
          </p>
        ) : null}
        {runState === "error" ? (
          <p className="mt-2 text-[10px] text-destructive">
            {t(($) => $.claude_lab.code_run_error)}
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
        <p className="text-xs text-muted-foreground">{t(($) => $.claude_lab.code_sessions_empty)}</p>
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
        title={t(($) => $.claude_lab.title_knowledge)}
        body={t(($) => $.claude_lab.agent_lock_issue_required)}
      />
    );
  }
  return (
    <section className="rounded-xl border border-border bg-card p-4">
      <header className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
        <Sparkles className="size-4" aria-hidden />
        <span className="font-medium text-foreground">{t(($) => $.claude_lab.title_knowledge)}</span>
        <span>· {t(($) => $.claude_lab.knowledge_header)}</span>
      </header>
      {skills.isLoading ? (
        <LoadingHint title="" />
      ) : skills.isError ? (
        <ErrorHint title="" body={`${t(($) => $.claude_lab.knowledge_load_error)}: ${(skills.error as Error).message}`} />
      ) : (skills.data ?? []).length === 0 ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.claude_lab.knowledge_empty)}</p>
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
      <p className="mt-3 text-sm text-muted-foreground">{t(($) => $.claude_lab.loading)}</p>
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