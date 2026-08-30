"use client";

// IssueOpenMythosIcon (0.5.90) — the OpenMythos outer-loop affordance on
// the issue header, mounted beside the causal-graph icon. It marks
// "this issue runs the OpenMythos outer loop" and offers the run
// surface: latest run state, the converged strategy, and a one-click
// start/re-run.
//
// ICP-5 (passive): the icon renders ONLY when the issue is bound to
// mythos_swarm — it never nags, never blocks, and never asks for
// attention on unbound issues. Binding happens through the LabPicker
// (which since 0.5.90 always binds enhancer mode); this icon is the
// indicator + run control.
//
// Enhancer-only contract (0.5.90): the run targets the issue's agent /
// squad assignee (the server defaults the target to the root assignee),
// so the start button is disabled with a hint when the issue has no
// agent/squad assignee.

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Orbit, Play } from "lucide-react";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { Popover, PopoverTrigger, PopoverContent } from "@multica/ui/components/ui/popover";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

const TERMINAL_STATUSES = new Set(["completed", "aborted", "failed"]);
const POLL_MS = 3000;
const IDLE_MS = 15000;

interface MythosRunLite {
  run_id?: string;
  status?: string;
  iterations_run?: number;
  current_loop?: number;
  coda_summary?: string;
  coda_conclusions?: string[] | string;
}

function codaText(run: MythosRunLite | undefined): string {
  if (!run?.coda_conclusions) return run?.coda_summary ?? "";
  const raw = run.coda_conclusions;
  if (Array.isArray(raw)) return raw.join("\n\n");
  return String(raw);
}

export function IssueOpenMythosIcon({
  issueId,
  labSource,
  issueTitle,
  issueDescription,
  assigneeType,
}: {
  issueId: string;
  labSource: string | null | undefined;
  issueTitle?: string;
  issueDescription?: string | null;
  assigneeType?: string | null;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);

  const enabled = labSource === "mythos_swarm";

  const runsQuery = useQuery({
    queryKey: ["issue-openmythos-runs", wsId, issueId],
    enabled: enabled && !!wsId && open,
    queryFn: async (): Promise<MythosRunLite[]> => {
      const r = await api.rawRequest(
        `/api/issues/${encodeURIComponent(issueId)}/mythos-runs?workspace_id=${encodeURIComponent(wsId)}`,
      );
      if (r.status === 404) return [];
      if (!r.ok) throw new Error(`mythos-runs ${r.status}`);
      const raw: unknown = await r.json();
      return Array.isArray(raw) ? (raw as MythosRunLite[]) : [];
    },
    refetchInterval: (query) => {
      const latest = query.state.data?.[0];
      if (!latest) return IDLE_MS;
      return TERMINAL_STATUSES.has(latest.status ?? "") ? IDLE_MS : POLL_MS;
    },
  });

  if (!enabled) return null;

  const targetOk = assigneeType === "agent" || assigneeType === "squad";
  const latest = runsQuery.data?.[0];
  const inFlight = !!latest && !TERMINAL_STATUSES.has(latest.status ?? "");

  const startRun = async () => {
    setStarting(true);
    setStartError(null);
    try {
      const problem = [issueTitle, issueDescription].filter(Boolean).join("\n\n") || issueId;
      const r = await api.rawRequest(
        `/api/experimental/mythos-swarm/run?workspace_id=${encodeURIComponent(wsId)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            problem,
            root_issue_id: issueId,
            mode: "enhancer",
          }),
        },
      );
      if (!r.ok) {
        const body = await r.text();
        setStartError(body.slice(0, 200) || `run ${r.status}`);
      } else {
        await queryClient.invalidateQueries({ queryKey: ["issue-openmythos-runs", wsId, issueId] });
      }
    } catch (e) {
      setStartError(e instanceof Error ? e.message : String(e));
    } finally {
      setStarting(false);
    }
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={(props) => (
          <Button
            {...props}
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground"
            aria-label={t(($) => $.openmythos.icon_tooltip) ?? "OpenMythos outer loop"}
          >
            {inFlight ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <Orbit className="size-4" aria-hidden />
            )}
          </Button>
        )}
      />
      <PopoverContent align="end" className="w-[360px] space-y-2">
        <div className="flex items-center justify-between gap-2">
          <p className="text-xs font-medium text-foreground">
            {t(($) => $.openmythos.panel_title) ?? "OpenMythos outer loop"}
          </p>
          {latest?.status ? (
            <span
              data-openmythos-status={latest.status}
              className="rounded px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground"
            >
              {latest.status}
            </span>
          ) : null}
        </div>
        <p className="text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.openmythos.panel_hint) ??
            "The swarm plans and converges first, then hands the strategy to this issue's assignee."}
        </p>
        {latest ? (
          <div className="space-y-1 rounded-md border border-border/60 bg-muted/30 px-2 py-1.5">
            <p className="text-[10px] text-muted-foreground">
              {t(($) => $.openmythos.iterations, { count: latest.iterations_run ?? latest.current_loop ?? 0 }) ??
                `iterations: ${latest.iterations_run ?? latest.current_loop ?? 0}`}
            </p>
            {codaText(latest) ? (
              <p className="line-clamp-4 whitespace-pre-wrap text-[11px] leading-snug text-foreground">
                {codaText(latest)}
              </p>
            ) : null}
          </div>
        ) : null}
        {startError ? (
          <p className="rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1 text-[10px] text-destructive">
            {startError}
          </p>
        ) : null}
        <div className="flex items-center justify-between gap-2">
          <span className="text-[10px] text-muted-foreground">
            {!targetOk
              ? (t(($) => $.openmythos.target_required) ?? "Assign this issue to an agent or squad first")
              : ""}
          </span>
          <Button
            size="sm"
            variant="outline"
            disabled={!targetOk || starting || inFlight}
            onClick={() => void startRun()}
            className="h-7 gap-1 text-xs"
          >
            {starting ? (
              <Loader2 className="size-3 animate-spin" aria-hidden />
            ) : (
              <Play className="size-3" aria-hidden />
            )}
            {latest
              ? (t(($) => $.openmythos.rerun) ?? "Re-run outer loop")
              : (t(($) => $.openmythos.start) ?? "Start outer loop")}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
