"use client";

// pythia-issue-embed — the issue-main-pane Pythia embed (0.5.113, replaces
// the 0.5.111 live/report tabs in the property panel). Mounted by
// issue-detail.tsx INSIDE the main task column, BELOW the comment stream
// and ABOVE the comment input. One component owns:
//   - live rounds + council + trajectory (drives the only visible animation)
//   - terminal report rendered inline (#N + markdown) when the run completes
//   - mount/unmount policy: collapses to nothing when there's no run AND no
//     completed report waiting to surface (i.e. nothing to show → no card)
//
// Shares the existing usePythiaIssueLab hook (SSE bus + run cache). The
// property-panel PythiaPanel now becomes a 3-tab structure (继续推演 /
// 历史·回放 / 追问); the live animation and report view move here.

import { useMemo, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Loader2, Sparkles, ChevronDown } from "lucide-react";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { usePythiaIssueLab } from "../../hooks/use-pythia-issue-lab";
import { PythiaCouncilCanvas } from "./pythia-council-canvas";
import { PythiaRoundTimeline, PythiaTrajectory } from "./pythia-round-view";
import { useLocale, useT } from "../../../i18n";

export function PythiaIssueEmbed({
  wsId,
  issueId,
}: {
  wsId: string;
  issueId: string;
}) {
  const { t } = useT("experimental");
  const reduceMotion = useReducedMotion() ?? false;
  const lab = usePythiaIssueLab(wsId, issueId);
  const { stream, runs, hasLiveRun } = lab;
  const liveEnvelopes = stream.envelopes;
  // Trust the optimistic stream while it says a run is executing: between
  // start() returning and the runs query refetching, the list does not
  // know the run yet, and gating on it made the embed flash to
  // history/null for a beat. A run that truly vanished is cleaned up by
  // the terminal-status effect + the runs poll.
  const streamTarget = stream.runId ? runs.find((r) => r.id === stream.runId) : null;
  const useStream = stream.status === "running" || streamTarget != null;

  const showLive = hasLiveRun && useStream;
  // 0.5.114: when a run completes the report is delivered to the issue
  // timeline as a `pythia_runtime` comment (writeback path in
  // pythiaWritebackReport). Rendering the full markdown inside the embed
  // duplicates that comment — readers see the same content twice and the
  // second copy fights the first for attention. The comment owns the
  // report; the header pill keeps the status indicator alive.
  const latestCompletedRun =
    !showLive && runs.length > 0 ? runs[0] ?? null : null;
  const showHistory = !showLive && latestCompletedRun != null;
  // 0.5.133: the full null-collapse is gone — an idle lab-bound issue
  // (upstream gate: issue.lab_source === "pythia_oracle") now shows the
  // council chamber in its standby state (fixed roster seats, slow
  // standby orbit) instead of nothing, which read as "not implemented".
  const showIdle = !showLive && !showHistory;

  return (
    <div
      className="mt-4 overflow-hidden rounded-lg border border-purple-500/30 bg-purple-500/5"
      data-testid="pythia-issue-embed"
    >
      <div className="flex items-center gap-2 border-b border-purple-500/20 bg-purple-500/10 px-3 py-1.5">
        <Sparkles className="size-3.5 shrink-0 text-purple-600 dark:text-purple-300" aria-hidden />
        <span className="text-xs font-medium text-purple-700 dark:text-purple-300">
          {t(($) => $.pythia_lab.embed_title)}
        </span>
        {showLive && (
          <span className="ml-1 flex items-center gap-1 text-[10px] font-medium text-purple-700 dark:text-purple-300">
            <Loader2 className="size-2.5 animate-spin" aria-hidden />
            {t(($) => $.pythia_lab.embed_running, {
              round: stream.totalRounds > 0 ? stream.totalRounds : liveEnvelopes.length,
            })}
          </span>
        )}
        {showIdle && (
          <span className="ml-1 text-[10px] text-muted-foreground">
            {t(($) => $.pythia_lab.embed_idle)}
          </span>
        )}
      </div>
      <div className="space-y-2 px-3 py-2">
        {showLive ? (
          <LiveEmbed
            envelopes={liveEnvelopes}
            totalRounds={stream.totalRounds}
            reduceMotion={reduceMotion}
            emptyLabel={t(($) => $.pythia_lab.embed_empty)}
          />
        ) : showHistory ? (
          <HistoryEmbed run={latestCompletedRun!} />
        ) : (
          <PythiaCouncilCanvas envelopes={[]} totalRounds={0} running={false} />
        )}
      </div>
    </div>
  );
}

function LiveEmbed({
  envelopes,
  totalRounds,
  reduceMotion,
  emptyLabel,
}: {
  envelopes: ReturnType<typeof usePythiaIssueLab>["stream"]["envelopes"];
  totalRounds: number;
  reduceMotion: boolean;
  emptyLabel: string;
}) {
  return (
    <div className="space-y-2" data-testid="pythia-embed-live">
      {/* 0.5.132 council chamber — the dial/seats/trajectory projection of
          the same envelopes; sits ABOVE the per-round cards so the
          convergence view is the first thing read while a run executes.
          0.5.133: rendered from the first frame (standby seats before the
          first envelope) so the chamber never pops in mid-run. */}
      <PythiaCouncilCanvas
        envelopes={envelopes}
        totalRounds={totalRounds}
        running
      />
      <PythiaRoundTimeline
        envelopes={envelopes}
        totalRounds={Math.max(totalRounds, envelopes.length)}
        running
      />
      {envelopes.length === 0 && (
        <p className="text-[11px] text-muted-foreground">{emptyLabel}</p>
      )}
      {/* Probability/confidence trajectory (0.5.131 — the chart existed
          since 0.5.111 but was never wired in). Fades in ONCE when a
          second round exists, then extends in place; per-round reveal is
          already owned by the RoundCard transitions above. */}
      <AnimatePresence initial={false}>
        {envelopes.length >= 2 && (
          <motion.div
            key="pythia-trajectory"
            initial={reduceMotion ? false : { opacity: 0, y: 4 }}
            animate={{
              opacity: 1,
              y: 0,
              transition: {
                duration: UI_MOTION_DURATION.standard,
                ease: UI_EASE_OUT,
              },
            }}
            exit={{ opacity: 0 }}
          >
            <PythiaTrajectory
              envelopes={envelopes}
              totalRounds={Math.max(totalRounds, envelopes.length)}
            />
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

function HistoryEmbed({
  run,
}: {
  run: NonNullable<ReturnType<typeof usePythiaIssueLab>["runs"][number]>;
}) {
  const { t } = useT("experimental");
  const locale = useLocale();
  const [open, setOpen] = useState(false);
  const envelopes = run.envelopes ?? [];
  const timeLabel = useMemo(() => {
    const ms = Date.parse(run.created_at);
    return Number.isFinite(ms) ? new Date(ms).toLocaleString(locale) : run.created_at;
  }, [run.created_at, locale]);
  return (
    <div className="space-y-1.5" data-testid="pythia-embed-history">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1 text-[11px] font-medium text-foreground/85 hover:text-foreground"
        aria-expanded={open}
      >
        <ChevronDown
          className={`size-3 shrink-0 text-muted-foreground transition-transform ${open ? "" : "-rotate-90"}`}
          aria-hidden
        />
        <span className="truncate">{t(($) => $.pythia_lab.embed_history_toggle, { time: timeLabel })}</span>
      </button>
      {open && envelopes.length > 0 && (
        <div className="space-y-2">
          <PythiaCouncilCanvas
            envelopes={envelopes}
            totalRounds={run.rounds || envelopes.length}
            running={false}
          />
          <PythiaRoundTimeline envelopes={envelopes} totalRounds={run.rounds || envelopes.length} running={false} />
          <PythiaTrajectory envelopes={envelopes} totalRounds={run.rounds || envelopes.length} />
        </div>
      )}
    </div>
  );
}