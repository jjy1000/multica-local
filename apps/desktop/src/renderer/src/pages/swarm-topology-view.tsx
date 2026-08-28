// Swarm topology view — pre-workspace lab surface.
//
// Pattern mirrors packages/views/experimental/components/mythos-view.tsx
// + packages/views/experimental/components/plugin-shell-view.tsx. The
// user lands here from the Labs sidebar (entry: `/experimental/swarm-topology`)
// or from the issue detail page's swarm badge (entry:
// `<SwarmIssueLabsSection />` on the issue detail page).
//
// Five sections:
//   - Header         : title + status pill + phase indicator
//   - TopologyGraph  : node-edge diagram of role-agents + depends_on
//                      edges (live, polled via 5s refetchInterval
//                      mirroring Active Contract #1 Mode B)
//   - RoleList       : table view of role-agents + heartbeat + current step
//   - PastRunsPanel  : historical swarm_run rows for the workspace
//   - SwarmInterruptBar : sticky bottom pause/cancel/inject controls
//
// Two modes:
//   - "bootstrap" (no existing run for the workspace): show a RunForm
//     with workspace + problem + max_runtime_hours. POST bootstraps a
//     new swarm_run, then re-fetches state.
//   - "active" (existing run): show the full layout above.

import { useCallback, useEffect, useMemo, useState } from "react";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { useSearchParams } from "react-router-dom";

import { api } from "@multica/core/api";
import { getCurrentWsId } from "@multica/core/platform";
import type { Issue } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";

import { SwarmInterruptBar, SwarmTopologyGraph, IssueBreadcrumb, type SwarmRole } from "@multica/views/experimental";

import { useT } from "@multica/views/i18n";

const API_BASE = "/api/experimental/swarm-topology";

// 0.5.86 swarm consolidation notice text (kept as a tiny component so
// the hook ordering of the main view is untouched).
function BannerT() {
  const { t } = useT("swarm");
  return <>{t(($) => $.consolidation_notice)}</>;
}

interface SwarmRunState {
  run_id: string;
  status: string;
  current_phase: string;
  is_paused?: boolean;
  roles: SwarmRole[];
  active_role_count: number;
  completed_role_count: number;
}

interface SwarmRun {
  id: string;
  root_issue_id: string;
  status: string;
  current_phase: string;
  max_runtime_hours: number;
  started_at: string;
  completed_at?: string;
  interrupted_at?: string;
  interrupt_reason?: string;
  roles?: SwarmRole[];
}

interface BootstrapRequest {
  root_issue_id: string;
  problem: string;
  max_runtime_hours?: number;
}

async function bootstrapSwarm(req: BootstrapRequest): Promise<SwarmRun> {
  const resp = await api.rawRequest(`${API_BASE}/runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!resp.ok) throw new Error(`bootstrap failed: ${resp.status}`);
  return resp.json();
}

async function fetchSwarmState(runId: string): Promise<SwarmRunState> {
  const resp = await api.rawRequest(`${API_BASE}/runs/${runId}/state`);
  if (!resp.ok) throw new Error(`fetch state failed: ${resp.status}`);
  return resp.json();
}

async function postInterrupt(
  runId: string,
  kind: "pause" | "resume" | "cancel" | "redirect" | "inject_message",
  payload?: unknown,
): Promise<unknown> {
  const resp = await api.rawRequest(`${API_BASE}/runs/${runId}/interrupt`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ kind, payload }),
  });
  if (!resp.ok) throw new Error(`interrupt failed: ${resp.status}`);
  return resp.json();
}

// fetchPastSwarmRuns hits GET /api/experimental/swarm-topology/runs
// for the PastRunsPanel. Server enforces the swarm_topology flag gate;
// flag-off callers get a 404 from the experimental middleware (see
// server/cmd/server/router.go::RequireExperimentalFlag), and the renderer
// surfaces an empty list (defensive — the panel hides when the flag is
// off in the parent LabPicker anyway).
async function fetchPastSwarmRuns(workspaceId: string): Promise<SwarmRun[]> {
  const resp = await api.rawRequest(`${API_BASE}/runs?workspace_id=${encodeURIComponent(workspaceId)}`);
  if (resp.status === 404) return [];
  if (!resp.ok) throw new Error(`fetch past runs failed: ${resp.status}`);
  return resp.json();
}

// Issue-side reverse lookup: GET /api/issues/{id}/swarm-runs returns the
// single swarm_run bound to the issue (UNIQUE on root_issue_id) or 404.
// The same shape the issue-detail status pill consumes
// (issue-labs-section.tsx SwarmRunStatusPill).
interface SwarmRunSummary {
  id: string;
  status: string;
  current_phase: string;
}

async function fetchRunByIssue(issueId: string): Promise<SwarmRunSummary | null> {
  const resp = await api.rawRequest(`/api/issues/${encodeURIComponent(issueId)}/swarm-runs`);
  if (resp.status === 404) return null;
  if (!resp.ok) throw new Error(`swarm run fetch failed: ${resp.status}`);
  return resp.json();
}

export interface SwarmTopologyViewProps {
  initialRunId?: string;
  workspaceId?: string;
}

// 0.5.81 fix: the standalone sidebar entry renders <SwarmTopologyView />
// bare (routes.tsx passes no props), so `workspaceId` stayed undefined
// forever — PastRunsPanel never fetched and the BootstrapForm submit sat
// disabled behind "请先选择或创建一个工作区" even with a live workspace
// singleton. Mirror claude-lab-view.tsx / plugin-shell-view.tsx: poll
// getCurrentWsId() at 500ms so a pre-workspace lab surface tracks the
// active workspace without unmounting. The explicit prop still wins.
function useCurrentWsIdPoll(fallback?: string): string | undefined {
  const [polled, setPolled] = useState<string | null>(() => getCurrentWsId());
  useEffect(() => {
    const id = setInterval(() => {
      setPolled((prev) => {
        const next = getCurrentWsId();
        return prev === next ? prev : next;
      });
    }, 500);
    return () => clearInterval(id);
  }, []);
  return fallback ?? polled ?? undefined;
}

export function SwarmTopologyView({ initialRunId, workspaceId }: SwarmTopologyViewProps) {
  const queryClient = useQueryClient();
  const [searchParams] = useSearchParams();
  const urlIssueId = searchParams.get("issue");
  const effectiveWorkspaceId = useCurrentWsIdPoll(workspaceId);

  // Explicit selection wins: an initialRunId prop or a bootstrap started in
  // this mount must beat any issue-bound run below.
  const [explicitRunId, setExplicitRunId] = useState<string | undefined>(initialRunId);

  // 0.5.81 resume-on-arrival: clicking the issue-detail swarm status pill /
  // "never started" pill lands here from an issue; without issue binding the
  // page showed the empty BootstrapForm even though the run existed — the
  // "run completed in the task but the lab delivers nothing" bug class.
  // Derived (not effect-copied) so navigating ?issue=A → ?issue=B rebinds,
  // mirroring the claude-lab-view 0.3.43 sync-fix semantics.
  const boundRun = useQuery({
    queryKey: ["swarm-topology", "run-by-issue", urlIssueId],
    queryFn: () => fetchRunByIssue(urlIssueId!),
    enabled: Boolean(urlIssueId),
    staleTime: 5_000,
  });
  const resumedRunId =
    urlIssueId && boundRun.data && !boundRun.isError ? boundRun.data.id : undefined;
  const runId = explicitRunId ?? resumedRunId;

  // Past runs (Mode B polling per Active Contract #1: 30s idle).
  // Server endpoint: GET /api/experimental/swarm-topology/runs?workspace_id=
  // (handler/swarm_run.go::GetSwarmRunsByWorkspace, flag-gated by
  // router.go::RequireExperimentalFlag). Server returns the newest 100
  // runs (capped server-side); polling idle at 30s keeps the panel
  // fresh without thrashing the workspace-scoped query.
  const pastRuns = useQuery({
    queryKey: ["swarm-topology", "past-runs", effectiveWorkspaceId],
    queryFn: () => fetchPastSwarmRuns(effectiveWorkspaceId!),
    enabled: Boolean(effectiveWorkspaceId),
    refetchInterval: (query) => (query.state.data ? 30_000 : false),
    staleTime: 30_000,
  });

  // Active state (Mode B + tighter for in-flight topology).
  const state = useQuery({
    queryKey: ["swarm-topology", "state", runId],
    queryFn: () => fetchSwarmState(runId!),
    enabled: Boolean(runId),
    refetchInterval: (query) => {
      const data = query.state.data as SwarmRunState | undefined;
      if (!data) return 5_000;
      if (data.status === "completed" || data.status === "aborted" || data.status === "failed") {
        return 30_000;
      }
      return 5_000;
    },
    staleTime: 5_000,
  });

  const interrupt = useMutation({
    mutationFn: (args: { kind: "pause" | "resume" | "cancel" | "inject_message"; payload?: string }) =>
      postInterrupt(runId!, args.kind, args.payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["swarm-topology", "state", runId] });
      queryClient.invalidateQueries({ queryKey: ["swarm-topology", "past-runs", effectiveWorkspaceId] });
    },
  });

  const onInterrupt = useCallback(
    async (kind: "pause" | "resume" | "cancel" | "inject_message", payload?: string) => {
      await interrupt.mutateAsync({ kind, payload });
    },
    [interrupt],
  );

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-6 py-8" data-testid="swarm-topology-view">
      {/* 0.5.86 swarm consolidation: deprecation banner. The sidebar entry
          point was removed from the manifest and new issue bindings are
          frozen (HideFromIssueLabPicker) — mythos_swarm is the single
          蜂群 lab. This view stays mounted (forward-only law) so the
          historical runs remain readable. */}
      <div
        className="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-300"
        data-testid="swarm-consolidation-notice"
      >
        <BannerT />
      </div>
      {/* 0.5.81: back-link strip mounted at top of view. Reads ?issue= so
          it tracks whatever binding drove the navigation. */}
      <IssueBreadcrumb />
      <Header />
      {runId ? (
        <ActiveRun runId={runId} state={state.data} loading={state.isLoading} />
      ) : (
        <BootstrapForm
          workspaceId={effectiveWorkspaceId}
          onBootstrapped={(run) => {
            setExplicitRunId(run.id);
            void queryClient.invalidateQueries({
              queryKey: ["swarm-topology", "past-runs", effectiveWorkspaceId],
            });
          }}
        />
      )}
      {runId ? (
        <RoleList roles={state.data?.roles ?? []} />
      ) : null}
      {runId ? (
        <SwarmInterruptBar
          runId={runId}
          status={state.data?.status ?? "preparing"}
          isPaused={state.data?.is_paused ?? false}
          onInterrupt={onInterrupt}
        />
      ) : null}
      <PastRunsPanel runs={pastRuns.data ?? []} loading={pastRuns.isLoading} />
    </div>
  );
}

function Header() {
  const { t } = useT("swarm");
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t(($) => $.title)}</CardTitle>
        <CardDescription>{t(($) => $.description)}</CardDescription>
      </CardHeader>
    </Card>
  );
}

function ActiveRun({
  runId,
  state,
  loading,
}: {
  runId: string;
  state: SwarmRunState | undefined;
  loading: boolean;
}) {
  const { t } = useT("swarm");
  return (
    <Card data-testid="swarm-topology-active">
      <CardHeader>
        <CardTitle className="flex items-center justify-between">
          <span>{t(($) => $.live.title)}</span>
          <span className="flex items-center gap-2 text-sm font-normal">
            <StatusBadge status={state?.status ?? "loading"} />
            <span className="text-muted-foreground">
              {t(($) => $.live.phase)}: <strong>{state?.current_phase ?? "—"}</strong>
            </span>
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-32 w-full" />
        ) : (
          <SwarmTopologyGraph roles={state?.roles ?? []} />
        )}
        <div className="mt-4 grid grid-cols-3 gap-4 text-sm">
          <Counter label={t(($) => $.live.active_roles)} value={state?.active_role_count ?? 0} />
          <Counter label={t(($) => $.live.completed_roles)} value={state?.completed_role_count ?? 0} />
          <Counter label={t(($) => $.live.run_id)} value={runId.slice(0, 8) + "…"} mono />
        </div>
      </CardContent>
    </Card>
  );
}

function RoleList({ roles }: { roles: SwarmRole[] }) {
  const { t } = useT("swarm");
  if (roles.length === 0) return null;
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t(($) => $.roles.title)}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-left text-muted-foreground">
                <th className="py-2 pr-3">{t(($) => $.roles.name)}</th>
                <th className="py-2 pr-3">{t(($) => $.roles.status)}</th>
                <th className="py-2 pr-3">{t(($) => $.roles.current_step)}</th>
                <th className="py-2 pr-3">{t(($) => $.roles.last_heartbeat)}</th>
              </tr>
            </thead>
            <tbody>
              {roles.map((r) => (
                <tr key={r.id} className="border-b last:border-0">
                  <td className="py-2 pr-3 font-medium">{r.role_name}</td>
                  <td className="py-2 pr-3">
                    <StatusBadge status={r.status} />
                  </td>
                  <td className="py-2 pr-3 text-muted-foreground">
                    {r.current_step ?? "—"}
                  </td>
                  <td className="py-2 pr-3 text-muted-foreground">
                    {r.last_heartbeat_at
                      ? new Date(r.last_heartbeat_at).toLocaleString()
                      : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );
}

function PastRunsPanel({ runs, loading }: { runs: SwarmRun[]; loading: boolean }) {
  const { t } = useT("swarm");
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t(($) => $.past_runs.title)}</CardTitle>
        <CardDescription>{t(($) => $.past_runs.description)}</CardDescription>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-20 w-full" />
        ) : runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t(($) => $.past_runs.empty)}</p>
        ) : (
          <ul className="space-y-2 text-sm">
            {runs.map((r) => (
              <li key={r.id} className="flex items-center justify-between border-b pb-2 last:border-0">
                <span className="font-mono text-xs">{r.id.slice(0, 8)}…</span>
                <StatusBadge status={r.status} />
                <span className="text-muted-foreground">{new Date(r.started_at).toLocaleString()}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function BootstrapForm({
  workspaceId,
  onBootstrapped,
}: {
  workspaceId?: string;
  onBootstrapped: (run: SwarmRun) => void;
}) {
  const { t } = useT("swarm");
  const { t: tExp } = useT("experimental");
  const [searchParams] = useSearchParams();
  const urlIssueId = searchParams.get("issue");
  const [issueId, setIssueId] = useState("");
  // Pre-bind the issue picker from `?issue=<id>` when the user lands here
  // from an issue-detail "view in lab" jump (LabOutputPanel produces the
  // href). We never clobber a value the user already picked.
  useEffect(() => {
    if (urlIssueId && !issueId) setIssueId(urlIssueId);
  }, [urlIssueId, issueId]);
  const [problem, setProblem] = useState("");
  const [maxHours, setMaxHours] = useState(72);

  const bootstrap = useMutation({
    mutationFn: bootstrapSwarm,
    onSuccess: onBootstrapped,
  });

  return (
    <Card data-testid="swarm-topology-bootstrap">
      <CardHeader>
        <CardTitle>{t(($) => $.bootstrap.label)}</CardTitle>
        <CardDescription>{t(($) => $.bootstrap.description)}</CardDescription>
      </CardHeader>
      <CardContent>
        {issueId ? (
          <div className="mb-3 flex items-center justify-end gap-1.5 text-[10px] text-muted-foreground">
            <span className="rounded border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-foreground/80">
              {issueId.slice(0, 8)}…
            </span>
            <button
              type="button"
              onClick={() => setIssueId("")}
              aria-label={tExp(($) => $.back)}
              className="inline-flex size-4 items-center justify-center rounded text-xs hover:bg-muted hover:text-foreground"
            >
              ×
            </button>
          </div>
        ) : null}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            bootstrap.mutate({
              root_issue_id: issueId,
              problem,
              max_runtime_hours: maxHours,
            });
          }}
          className="space-y-4"
        >
          <div className="space-y-2">
            <Label htmlFor="swarm-issue-id">{t(($) => $.bootstrap.issue_label)}</Label>
            <IssuePicker
              value={issueId}
              onChange={setIssueId}
              disabled={bootstrap.isPending}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.bootstrap.issue_helper)}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="swarm-problem">{t(($) => $.bootstrap.problem_label)}</Label>
            <Input
              id="swarm-problem"
              type="text"
              value={problem}
              onChange={(e) => setProblem(e.target.value)}
              placeholder={t(($) => $.bootstrap.problem_placeholder)}
              required
              disabled={bootstrap.isPending}
              data-testid="swarm-bootstrap-problem"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="swarm-max-hours">{t(($) => $.bootstrap.max_hours_label)}</Label>
            <Input
              id="swarm-max-hours"
              type="number"
              min={1}
              max={168}
              value={maxHours}
              onChange={(e) => setMaxHours(Number(e.target.value))}
              disabled={bootstrap.isPending}
              data-testid="swarm-bootstrap-max-hours"
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.bootstrap.max_hours_helper)}
            </p>
          </div>
          {workspaceId ? (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.bootstrap.workspace_label)}: <span className="font-mono">{workspaceId}</span>
            </p>
          ) : null}
          {bootstrap.error ? (
            <p className="text-sm text-destructive">{String(bootstrap.error)}</p>
          ) : null}
          <Button
            type="submit"
            disabled={bootstrap.isPending || !issueId || !problem || !workspaceId}
            data-testid="swarm-bootstrap-submit"
          >
            {bootstrap.isPending ? t(($) => $.bootstrap.bootstrapping) : t(($) => $.bootstrap.submit)}
          </Button>
          {/* 0.5.22 audit fix (P2-8): workspaceId undefined guard.
              The BootstrapForm previously assumed workspaceId was set
              (non-null assertion on line 131); when undefined the
              submit would 403 server-side with a misleading error.
              The disable above + this hint surfaces the missing-workspace
              state directly. */}
          {!workspaceId ? (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.bootstrap.workspace_label)}:{" "}
              <span className="italic">请先选择或创建一个工作区</span>
            </p>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}

function StatusBadge({ status }: { status: string }) {
  const { t } = useT("swarm");
  const tAny = t as unknown as (sel: (res: any) => string) => string;
  const colors: Record<string, string> = {
    preparing: "bg-slate-100 text-slate-700",
    planning: "bg-blue-100 text-blue-700",
    running: "bg-blue-200 text-blue-900",
    monitoring: "bg-amber-100 text-amber-700",
    completed: "bg-emerald-100 text-emerald-700",
    aborted: "bg-slate-200 text-slate-700",
    failed: "bg-red-100 text-red-700",
    created: "bg-slate-100 text-slate-600",
    ready: "bg-blue-50 text-blue-700",
    idle: "bg-amber-50 text-amber-700",
    archived: "bg-slate-200 text-slate-500",
  };
  const cls = colors[status] ?? "bg-slate-100 text-slate-700";
  // Use i18n label if available (handles zh/ja/ko fallback to status enum
  // string); falls back to the raw enum so unknown statuses don't 404.
  const label = tAny(($) => $.status[status]) ?? status;
  return (
    <span
      className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${cls}`}
      data-status={status}
    >
      {label}
    </span>
  );
}

function Counter({
  label,
  value,
  mono,
}: {
  label: string;
  value: number | string;
  mono?: boolean;
}) {
  return (
    <div className="rounded-md border bg-muted/30 p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`mt-1 text-lg font-semibold ${mono ? "font-mono text-sm" : ""}`}>{value}</div>
    </div>
  );
}

// IssuePicker — debounced autocomplete over GET /api/issues/search.
// Filters out issues already bound to a swarm_topology run (UNIQUE on
// root_issue_id) and the currently selected issue so a user can't pick
// it twice. Workspace scope is enforced server-side via the X-Workspace-ID
// header that api.rawRequest injects.
const ISSUE_PICKER_LIMIT = 10;

function IssuePicker({
  value,
  onChange,
  disabled,
}: {
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);

  const search = useQuery({
    queryKey: ["swarm-bootstrap-issue-search", query],
    enabled: query.trim().length > 0,
    queryFn: ({ signal }) =>
      api.searchIssues({
        q: query.trim(),
        limit: ISSUE_PICKER_LIMIT,
        include_closed: false,
        signal,
      }),
    staleTime: 5_000,
  });

  const selectedIssue = useQuery({
    queryKey: ["swarm-bootstrap-issue-selected", value],
    enabled: Boolean(value),
    queryFn: () => api.getIssue(value),
    staleTime: Infinity,
  });

  const filtered = useMemo(() => {
    const issues: Issue[] = search.data?.issues ?? [];
    // 0.5.22 audit fix (P2-14): broader filter. The previous
    // implementation only excluded swarm_topology-labelled issues;
    // any other lab (mythos_swarm, claude_science_lab, pythia_oracle,
    // etc.) would slip through and the server's PostSwarmRun would
    // 400 because the issue is already lab-bound. Now: exclude ANY
    // issue that has a non-null lab_source (mirrors the
    // i.lab_source == null || i.lab_source === '' shape used by
    // LabPicker in mythos-view.tsx). The UNIQUE idx on
    // swarm_run.root_issue_id is the server-side safety net for
    // issues already bootstrapped — the filter is purely UX.
    return issues.filter(
      (i) => i.id !== value && (!i.lab_source || i.lab_source === ""),
    );
  }, [search.data, value]);

  const selected = selectedIssue.data;
  const displayValue = selected
    ? `${selected.identifier} — ${selected.title}`
    : query;

  return (
    <div className="relative" data-testid="swarm-bootstrap-issue-picker">
      <Input
        type="text"
        value={displayValue}
        onChange={(e) => {
          setQuery(e.target.value);
          onChange("");
        }}
        placeholder="Search issues by title or identifier…"
        disabled={disabled}
        data-testid="swarm-bootstrap-issue"
        autoComplete="off"
        onFocus={() => filtered.length > 0 && setOpen(true)}
        onBlur={() => {
          // Delay close so a click on a row registers first.
          setTimeout(() => setOpen(false), 150);
        }}
      />
      {open && filtered.length > 0 ? (
        <ul
          role="listbox"
          className="absolute z-20 mt-1 max-h-64 w-full overflow-auto rounded-md border bg-popover p-1 text-sm shadow-md"
          data-testid="swarm-bootstrap-issue-results"
        >
          {filtered.map((i) => (
            <li
              key={i.id}
              role="option"
              aria-selected={false}
              className="cursor-pointer rounded px-2 py-1.5 hover:bg-accent"
              onMouseDown={(e) => {
                e.preventDefault();
                onChange(i.id);
                setQuery("");
                setOpen(false);
              }}
              data-testid="swarm-bootstrap-issue-option"
              data-issue-id={i.id}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="truncate font-medium">{i.title}</span>
                <span className="shrink-0 font-mono text-xs text-muted-foreground">
                  {i.identifier}
                </span>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}