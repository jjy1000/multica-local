"use client";

// pythia-panel — the issue-side Pythia panel (0.5.111 + 0.5.113).
// 0.5.111 introduced 4 tabs (live / report / history / chat). 0.5.113 moves
// live + report to the issue-main-pane embed (`pythia-issue-embed.tsx`)
// + report-as-comment writeback; only three affordances stay on the
// property panel:
//   继续推演 — ContinueForm: variables + parent-run + rounds
//   历史·回放 — PythiaReplayPlayer: replay a finished run round-by-round
//   追问 — PythiaFollowUpChat: persona Q&A grounded in the latest run

import { useState } from "react";
import { History, MessageCircle, Play } from "lucide-react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { usePythiaIssueLab } from "../../hooks/use-pythia-issue-lab";
import { useT } from "../../../i18n";
import { PythiaReplayPlayer } from "./pythia-report-view";
import { PythiaFollowUpChat } from "./pythia-followup-chat";

type PanelTab = "continue" | "history" | "chat";

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
          {busy ? (
            <span className="size-3 animate-spin rounded-full border-2 border-white border-t-transparent" />
          ) : (
            <Play className="size-3" aria-hidden />
          )}
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
}) {
  const { t } = useT("experimental");
  const [tab, setTab] = useState<PanelTab>("continue");
  const [replayRunId, setReplayRunId] = useState<string | null>(null);
  const [startError, setStartError] = useState(false);

  // 0.5.104 flag-off honesty (unchanged contract).
  const { data: flags } = useExperimentalFlags();
  const flagsLoaded = flags != null;
  const flagEnabled = (flags ?? []).some((f) => f.key === "pythia_oracle" && f.enabled);

  const lab = usePythiaIssueLab(wsId, issueId);
  const { runs, hasLiveRun } = lab;
  const [starting, setStarting] = useState(false);

  const startRun = async (input: { variables?: string; rounds?: number; parentRunId?: string }) => {
    setStartError(false);
    setStarting(true);
    try {
      const res = await lab.start(input);
      if (!res) setStartError(true);
      else setTab("continue");
    } finally {
      setStarting(false);
    }
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

  const replayRun = replayRunId ? runs.find((r) => r.id === replayRunId) ?? null : null;

  return (
    <div className="space-y-2" data-testid="lab-output-panel-pythia">
      <div className="flex flex-wrap items-center justify-between gap-1.5">
        <div className="flex items-center gap-0.5">
          <TabButton
            active={tab === "continue"}
            onClick={() => setTab("continue")}
            label={t(($) => $.pythia_lab.tab_continue)}
          >
            <Play className="size-3" aria-hidden />
          </TabButton>
          <TabButton
            active={tab === "history"}
            onClick={() => setTab("history")}
            label={t(($) => $.pythia_lab.tab_history)}
          >
            <History className="size-3" aria-hidden />
            {runs.length > 0 && <span className="font-mono">{runs.length}</span>}
          </TabButton>
          <TabButton
            active={tab === "chat"}
            onClick={() => setTab("chat")}
            label={t(($) => $.pythia_lab.tab_chat)}
          >
            <MessageCircle className="size-3" aria-hidden />
          </TabButton>
        </div>
        {hasLiveRun && (
          <button
            type="button"
            aria-label={t(($) => $.lab_output_panel.pythia_stop_forecast)}
            data-testid="lab-output-panel-pythia-stop"
            onClick={lab.cancel}
            className="rounded p-1 text-purple-700 transition-colors hover:bg-purple-500/10 dark:text-purple-300"
          >
            <span className="text-[10px] font-medium">
              {t(($) => $.lab_output_panel.pythia_stop_forecast)}
            </span>
          </button>
        )}
      </div>

      {tab === "continue" && (
        <ContinueForm disabled={hasLiveRun} busy={starting} onSubmit={startRun} />
      )}
      {startError && tab === "continue" && (
        <p className="text-[10px] text-destructive">{t(($) => $.pythia_lab.start_failed)}</p>
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
        </div>
      )}

      {tab === "chat" && <PythiaFollowUpChat issueId={issueId} />}
    </div>
  );
}