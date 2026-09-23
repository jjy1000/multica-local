"use client";

// pythia-panel — the issue-side Pythia lab panel (0.5.111). Replaces the
// 0.5.18 spinner-only reader with the SocialSim-aligned four-tab surface:
//
//   实时 (live)    — round timeline + probability trajectory + council
//                    vote sheet of the round in flight, the stop button,
//                    and the 继续推演 form (variables + optional rounds +
//                    parent run).
//   报告 (report)  — the LLM-synthesized conclusion report (live as it
//                    lands; the mechanical digest is the honest fallback).
//   历史 (history) — every persisted run with lineage badges; click one
//                    to open the replay player.
//   追问 (chat)    — Q&A against the deliberation, optionally in one
//                    council persona's voice.
//
// Flag-off, loading, and error states mirror the 0.5.104 contract (a
// disabled lab says so plainly; a 404 runs poll degrades to []).

import { useState } from "react";
import { History, Loader2, MessageCircle, Play, RefreshCw, ScrollText, Square, Waves } from "lucide-react";
import { useExperimentalFlags } from "@multica/core/experimental";
import type { PythiaForecastRun } from "@multica/core/types/api";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { usePythiaIssueLab } from "../../hooks/use-pythia-issue-lab";
import { useT } from "../../../i18n";
import { PythiaRoundTimeline, PythiaTrajectory } from "./pythia-round-view";
import { PythiaReportView, PythiaReplayPlayer } from "./pythia-report-view";
import { PythiaFollowUpChat } from "./pythia-followup-chat";

type PanelTab = "live" | "report" | "history" | "chat";

function TabButton({
  active,
  onClick,
  label,
  children,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium transition-colors ${
        active
          ? "bg-purple-500/15 text-purple-700 dark:text-purple-300"
          : "text-muted-foreground hover:bg-muted hover:text-foreground"
      }`}
    >
      {children}
      {label}
    </button>
  );
}

/** 继续推演 form: variables textarea + optional round pin + parent-run
 *  start. Shown on the live tab; disabled while a run is in flight. */
function ContinueForm({
  disabled,
  busy,
  onSubmit,
}: {
  disabled: boolean;
  busy: boolean;
  onSubmit: (input: { variables: string; rounds?: number; parentRunId?: string }) => void;
}) {
  const { t } = useT("experimental");
  const [variables, setVariables] = useState("");
  const [roundsText, setRoundsText] = useState("");
  const [parentRunId, setParentRunId] = useState("");

  const submit = () => {
    const parsed = Number.parseInt(roundsText, 10);
    onSubmit({
      variables: variables.trim(),
      rounds: Number.isFinite(parsed) && parsed > 0 ? parsed : undefined,
      parentRunId: parentRunId.trim() || undefined,
    });
    setVariables("");
    setRoundsText("");
  };

  return (
    <div className="space-y-1.5 rounded-md border border-border bg-card/50 p-2" data-testid="pythia-continue-form">
      <label
        htmlFor="pythia-continue-variables"
        className="block text-[11px] font-medium text-muted-foreground"
      >
        {t(($) => $.pythia_lab.continue_title)}
      </label>
      <textarea
        id="pythia-continue-variables"
        value={variables}
        onChange={(e) => setVariables(e.target.value)}
        rows={2}
        placeholder={t(($) => $.pythia_lab.continue_placeholder)}
        disabled={disabled}
        className="w-full resize-y rounded-md border border-border bg-background p-2 text-[11px] text-foreground outline-none focus:border-primary disabled:opacity-50"
      />
      <div className="flex items-center gap-1.5">
        <input
          type="text"
          value={parentRunId}
          onChange={(e) => setParentRunId(e.target.value)}
          placeholder={t(($) => $.pythia_lab.continue_parent_placeholder)}
          disabled={disabled}
          className="h-7 min-w-0 flex-1 rounded-md border border-border bg-background px-1.5 font-mono text-[10px] text-foreground outline-none focus:border-primary disabled:opacity-50"
        />
        <input
          type="number"
          min={1}
          max={10}
          value={roundsText}
          onChange={(e) => setRoundsText(e.target.value)}
          placeholder={t(($) => $.pythia_lab.continue_rounds_placeholder)}
          disabled={disabled}
          aria-label={t(($) => $.pythia_lab.continue_rounds_placeholder)}
          className="h-7 w-14 rounded-md border border-border bg-background px-1.5 text-[10px] text-foreground outline-none focus:border-primary disabled:opacity-50"
        />
        <button
          type="button"
          onClick={submit}
          disabled={disabled || busy || !variables.trim()}
          className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md bg-purple-600 px-2 text-[11px] font-medium text-white hover:bg-purple-500 disabled:opacity-50"
        >
          {busy ? <Loader2 className="size-3 animate-spin" aria-hidden /> : <Play className="size-3" aria-hidden />}
          {t(($) => $.pythia_lab.continue_submit)}
        </button>
      </div>
      <p className="text-[9px] leading-snug text-muted-foreground">
        {t(($) => $.pythia_lab.continue_hint)}
      </p>
    </div>
  );
}

export function PythiaPanel({
  wsId,
  issueId,
}: {
  wsId: string;
  issueId: string;
  // 0.5.112: the old labViewHref prop is GONE — the /experimental/pythia
  // page is a passive monitor, so the "view in lab" jump no longer exists.
}) {
  const { t } = useT("experimental");
  const [tab, setTab] = useState<PanelTab>("live");
  const [replayRunId, setReplayRunId] = useState<string | null>(null);
  const [startError, setStartError] = useState(false);

  // 0.5.104 flag-off honesty (unchanged contract).
  const { data: flags } = useExperimentalFlags();
  const flagsLoaded = flags != null;
  const flagEnabled = (flags ?? []).some((f) => f.key === "pythia_oracle" && f.enabled);

  const lab = usePythiaIssueLab(wsId, issueId);
  const { stream, runs, hasLiveRun } = lab;

  const latestRun = runs[0] ?? null;
  const streamRun = stream.runId ? runs.find((r) => r.id === stream.runId) ?? null : null;
  const reportRun: PythiaForecastRun | null = streamRun ?? latestRun;
  const replayRun = replayRunId ? runs.find((r) => r.id === replayRunId) ?? null : null;

  const startRun = async (input: { variables?: string; rounds?: number; parentRunId?: string }) => {
    setStartError(false);
    const res = await lab.start(input);
    if (!res) setStartError(true);
    else setTab("live");
  };

  if (flagsLoaded && !flagEnabled) {
    return (
      <div
        className="space-y-1 rounded-md border border-dashed border-border/60 px-2 py-1.5"
        data-testid="lab-output-panel-pythia-flag-off"
      >
        <p className="text-[11px] font-medium text-muted-foreground">
          {t(($) => $.lab_output_panel.pythia_flag_off)}
        </p>
        <p className="text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.lab_output_panel.pythia_flag_off_hint)}
        </p>
      </div>
    );
  }

  if (lab.isLoading) {
    return (
      <div className="space-y-2" data-testid="lab-output-panel-loading">
        <Skeleton className="h-3 w-1/2" />
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-2/3" />
      </div>
    );
  }

  if (lab.isError) {
    return (
      <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1.5">
        <p className="text-[11px] text-destructive">{t(($) => $.lab_output_panel.error)}</p>
        <button
          type="button"
          onClick={lab.refetch}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.lab_output_panel.retry)}
        </button>
      </div>
    );
  }

  const empty = runs.length === 0 && stream.status === "idle";

  return (
    <div className="space-y-2" data-testid="lab-output-panel-pythia">
      <div className="flex flex-wrap items-center justify-between gap-1.5">
        <div className="flex items-center gap-0.5">
          <TabButton active={tab === "live"} onClick={() => setTab("live")} label={t(($) => $.pythia_lab.tab_live)}>
            <Waves className="size-3" aria-hidden />
          </TabButton>
          <TabButton active={tab === "report"} onClick={() => setTab("report")} label={t(($) => $.pythia_lab.tab_report)}>
            <ScrollText className="size-3" aria-hidden />
          </TabButton>
          <TabButton active={tab === "history"} onClick={() => setTab("history")} label={t(($) => $.pythia_lab.tab_history)}>
            <History className="size-3" aria-hidden />
            {runs.length > 0 && <span className="font-mono">{runs.length}</span>}
          </TabButton>
          <TabButton active={tab === "chat"} onClick={() => setTab("chat")} label={t(($) => $.pythia_lab.tab_chat)}>
            <MessageCircle className="size-3" aria-hidden />
          </TabButton>
        </div>
        <div className="flex items-center gap-1.5">
          {hasLiveRun && (
            <span className="flex items-center gap-1 text-[10px] font-medium text-purple-700 dark:text-purple-300">
              <Loader2 className="size-3 animate-spin" aria-hidden />
              {t(($) => $.pythia_lab.live_badge)}
            </span>
          )}
          {hasLiveRun && (
            <button
              type="button"
              aria-label={t(($) => $.lab_output_panel.pythia_stop_forecast)}
              data-testid="lab-output-panel-pythia-stop"
              onClick={lab.cancel}
              className="rounded p-1 text-purple-700 transition-colors hover:bg-purple-500/10 dark:text-purple-300"
            >
              <Square className="size-3" aria-hidden />
            </button>
          )}
        </div>
      </div>

      {tab === "live" && (
        <div className="space-y-2">
          <PythiaRoundTimeline
            envelopes={stream.status !== "idle" ? stream.envelopes : latestRun?.envelopes ?? []}
            totalRounds={stream.status !== "idle" ? stream.totalRounds : latestRun?.rounds ?? 0}
            running={hasLiveRun}
          />
          <PythiaTrajectory
            envelopes={stream.status !== "idle" ? stream.envelopes : latestRun?.envelopes ?? []}
            totalRounds={stream.status !== "idle" ? stream.totalRounds : latestRun?.rounds ?? 0}
          />
          {!hasLiveRun && (
            <ContinueForm disabled={false} busy={false} onSubmit={startRun} />
          )}
          {startError && (
            <p className="text-[10px] text-destructive">
              {t(($) => $.pythia_lab.start_failed)}
            </p>
          )}
          {hasLiveRun && stream.status === "running" && (
            <button
              type="button"
              onClick={() => setTab("report")}
              className="w-full rounded-md border border-purple-500/40 bg-purple-500/5 px-2 py-1 text-[11px] font-medium text-purple-700 hover:bg-purple-500/10 dark:text-purple-300"
            >
              {t(($) => $.pythia_lab.watch_report_hint)}
            </button>
          )}
        </div>
      )}

      {tab === "report" && (
        <PythiaReportView
          run={reportRun}
          liveReport={stream.status !== "idle" ? stream.report : undefined}
        />
      )}

      {tab === "history" && (
        <div className="space-y-1.5" data-testid="pythia-history">
          {replayRun ? (
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <p className="text-[11px] font-medium text-foreground/85">
                  {t(($) => $.pythia_lab.replay_title, {
                    time: replayRun.created_at,
                  })}
                </p>
                <button
                  type="button"
                  onClick={() => setReplayRunId(null)}
                  className="text-[10px] text-muted-foreground hover:text-foreground"
                >
                  {t(($) => $.pythia_lab.replay_close)}
                </button>
              </div>
              <PythiaReplayPlayer envelopes={replayRun.envelopes} />
            </div>
          ) : runs.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t(($) => $.lab_output_panel.empty)}</p>
          ) : (
            runs.map((run) => (
              <button
                key={run.id}
                type="button"
                onClick={() => setReplayRunId(run.id)}
                className="w-full rounded-md border border-border px-2 py-1.5 text-left transition-colors hover:bg-muted/50"
                data-testid="pythia-history-row"
              >
                <div className="flex items-center justify-between gap-2 text-[10px]">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="rounded bg-muted px-1 py-0.5 font-mono">
                      {run.run_kind === "continuation"
                        ? t(($) => $.pythia_lab.kind_continuation)
                        : t(($) => $.pythia_lab.kind_initial)}
                    </span>
                    <span
                      className={
                        run.status === "completed"
                          ? "text-emerald-600 dark:text-emerald-400"
                          : run.status === "running"
                            ? "text-purple-600 dark:text-purple-300"
                            : "text-muted-foreground"
                      }
                    >
                      {run.status}
                    </span>
                    <span className="truncate font-mono text-muted-foreground">
                      {run.created_at}
                    </span>
                  </span>
                  <span className="shrink-0 font-mono text-muted-foreground">
                    {run.rounds} · {run.source}
                  </span>
                </div>
                {run.variables && (
                  <p className="mt-0.5 line-clamp-1 text-[10px] text-foreground/70">
                    {t(($) => $.pythia_lab.variables_label)}: {run.variables}
                  </p>
                )}
              </button>
            ))
          )}
          {!replayRun && runs.length > 0 && (
            <button
              type="button"
              disabled={lab.isLoading}
              onClick={() => startRun({ variables: "" })}
              className="inline-flex items-center gap-1 rounded-md border border-purple-500/40 bg-purple-500/5 px-2 py-1 text-[11px] font-medium text-purple-700 hover:bg-purple-500/10 disabled:opacity-50 dark:text-purple-300"
            >
              <RefreshCw className="size-3" aria-hidden />
              {t(($) => $.lab_output_panel.pythia_start, { rounds: 3 })}
            </button>
          )}
        </div>
      )}

      {tab === "chat" && <PythiaFollowUpChat issueId={issueId} />}

      {empty && tab === "live" && (
        <p className="text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.pythia_lab.empty_hint)}
        </p>
      )}
    </div>
  );
}
