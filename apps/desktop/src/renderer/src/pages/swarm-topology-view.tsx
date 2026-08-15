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

import { useCallback, useState } from "react";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";

import { SwarmInterruptBar, SwarmTopologyGraph, type SwarmRole } from "@multica/views/experimental";

const API_BASE = "/api/experimental/swarm-topology";

interface SwarmRunState {
  run_id: string;
  status: string;
  current_phase: string;
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
  kind: "pause" | "cancel" | "redirect" | "inject_message",
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

export interface SwarmTopologyViewProps {
  initialRunId?: string;
  workspaceId?: string;
}

export function SwarmTopologyView({ initialRunId, workspaceId }: SwarmTopologyViewProps) {
  const queryClient = useQueryClient();
  const [runId, setRunId] = useState<string | undefined>(initialRunId);

  // Past runs (Mode B polling per Active Contract #1: 30s idle).
  const pastRuns = useQuery({
    queryKey: ["swarm-topology", "past-runs", workspaceId],
    queryFn: async () => {
      // Server has no /runs list endpoint yet — uses /issues/{id}/swarm-runs
      // per-run. For the panel we render "no past runs" until a list
      // endpoint ships. (Future: add GET /api/experimental/swarm-topology/runs)
      return [] as SwarmRun[];
    },
    enabled: Boolean(workspaceId),
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
    mutationFn: (args: { kind: "pause" | "cancel" | "inject_message"; payload?: string }) =>
      postInterrupt(runId!, args.kind, args.payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["swarm-topology", "state", runId] });
      queryClient.invalidateQueries({ queryKey: ["swarm-topology", "past-runs", workspaceId] });
    },
  });

  const onInterrupt = useCallback(
    async (kind: "pause" | "cancel" | "inject_message", payload?: string) => {
      await interrupt.mutateAsync({ kind, payload });
    },
    [interrupt],
  );

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-6 py-8" data-testid="swarm-topology-view">
      <Header />
      {runId ? (
        <ActiveRun runId={runId} state={state.data} loading={state.isLoading} />
      ) : (
        <BootstrapForm workspaceId={workspaceId} onBootstrapped={(run) => setRunId(run.id)} />
      )}
      {runId ? (
        <RoleList roles={state.data?.roles ?? []} />
      ) : null}
      {runId ? (
        <SwarmInterruptBar
          runId={runId}
          status={state.data?.status ?? "preparing"}
          onInterrupt={onInterrupt}
        />
      ) : null}
      <PastRunsPanel runs={pastRuns.data ?? []} loading={pastRuns.isLoading} />
    </div>
  );
}

function Header() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Swarm Topology</CardTitle>
        <CardDescription>
          Self-organising multi-agent system. The leader authors role-agents +
          skills + a coordinating squad on bootstrap, then walks a 5-phase
          machine (research → design → implement → review → done). Each
          role runs as an independent agent; the orchestrator ticks every
          30s and tears everything down via swarm_gc on terminal status.
        </CardDescription>
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
  return (
    <Card data-testid="swarm-topology-active">
      <CardHeader>
        <CardTitle className="flex items-center justify-between">
          <span>Live topology</span>
          <span className="flex items-center gap-2 text-sm font-normal">
            <StatusBadge status={state?.status ?? "loading"} />
            <span className="text-muted-foreground">
              phase: <strong>{state?.current_phase ?? "—"}</strong>
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
          <Counter label="Active roles" value={state?.active_role_count ?? 0} />
          <Counter label="Completed roles" value={state?.completed_role_count ?? 0} />
          <Counter label="Run id" value={runId.slice(0, 8) + "…"} mono />
        </div>
      </CardContent>
    </Card>
  );
}

function RoleList({ roles }: { roles: SwarmRole[] }) {
  if (roles.length === 0) return null;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Roles</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-left text-muted-foreground">
                <th className="py-2 pr-3">Name</th>
                <th className="py-2 pr-3">Status</th>
                <th className="py-2 pr-3">Current step</th>
                <th className="py-2 pr-3">Last heartbeat</th>
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
  return (
    <Card>
      <CardHeader>
        <CardTitle>Past runs</CardTitle>
        <CardDescription>
          Historical swarm_run rows in this workspace. Older runs are
          archived by swarm_gc after 7 days (mirrors runtime_gc retention).
        </CardDescription>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-20 w-full" />
        ) : runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">No past runs yet.</p>
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
  const [issueId, setIssueId] = useState("");
  const [problem, setProblem] = useState("");
  const [maxHours, setMaxHours] = useState(72);

  const bootstrap = useMutation({
    mutationFn: bootstrapSwarm,
    onSuccess: onBootstrapped,
  });

  return (
    <Card data-testid="swarm-topology-bootstrap">
      <CardHeader>
        <CardTitle>Bootstrap a swarm</CardTitle>
        <CardDescription>
          Pick an issue and describe the problem. The leader will author
          role-agents + skills + a coordinating squad on bootstrap.
        </CardDescription>
      </CardHeader>
      <CardContent>
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
            <Label htmlFor="swarm-issue-id">Issue ID</Label>
            <Input
              id="swarm-issue-id"
              type="text"
              value={issueId}
              onChange={(e) => setIssueId(e.target.value)}
              placeholder="e.g. 11fc7289-a895-4d4c-bed8-b241edbaa7ac"
              required
              disabled={bootstrap.isPending}
              data-testid="swarm-bootstrap-issue"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="swarm-problem">Problem statement</Label>
            <Input
              id="swarm-problem"
              type="text"
              value={problem}
              onChange={(e) => setProblem(e.target.value)}
              placeholder="e.g. Build a multi-module webapp with persistence, auth, and tests"
              required
              disabled={bootstrap.isPending}
              data-testid="swarm-bootstrap-problem"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="swarm-max-hours">Max runtime hours</Label>
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
              Default 72h. Hard cap 168h (1 week). Orchestrator flips
              status='failed' with reason 'max_lifetime' on overrun.
            </p>
          </div>
          {workspaceId ? (
            <p className="text-xs text-muted-foreground">
              Workspace: <span className="font-mono">{workspaceId}</span>
            </p>
          ) : null}
          {bootstrap.error ? (
            <p className="text-sm text-destructive">{String(bootstrap.error)}</p>
          ) : null}
          <Button
            type="submit"
            disabled={bootstrap.isPending || !issueId || !problem}
            data-testid="swarm-bootstrap-submit"
          >
            {bootstrap.isPending ? "Bootstrapping…" : "Bootstrap swarm"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

function StatusBadge({ status }: { status: string }) {
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
  return (
    <span
      className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${cls}`}
      data-status={status}
    >
      {status}
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