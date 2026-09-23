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

import { useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Loader2, Sparkles, ChevronDown } from "lucide-react";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { usePythiaIssueLab } from "../../hooks/use-pythia-issue-lab";
import { PythiaRoundTimeline } from "./pythia-round-view";
import { useT } from "../../../i18n";

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
  const streamTarget = stream.runId ? runs.find((r) => r.id === stream.runId) : null;
  const useStream = streamTarget != null;

  const showLive = hasLiveRun && useStream;
  // 0.5.114: when a run completes the report is delivered to the issue
  // timeline as a `pythia_runtime` comment (writeback path in
  // pythiaWritebackReport). Rendering the full markdown inside the embed
  // duplicates that comment — readers see the same content twice and the
  // second copy fights the first for attention. Collapse the embed to
  // null at that point: the comment owns the report, the header pill
  // keeps the status indicator alive, the history fold stays open only
  // when there is a previously-completed run the user can re-inspect.
  const latestCompletedRun =
    !showLive && runs.length > 0 ? runs[0] ?? null : null;
  const showHistory = !showLive && latestCompletedRun != null;

  // No run + no live → don't render anything (collapse to null).
  if (!showLive && !showHistory) return null;

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
        ) : null}
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
  // When a new envelope lands the timeline needs to react to its insertion.
  // We key the children by an incrementing id derived from the last envelope
  // so AnimatePresence picks the change up cleanly.
  const lastKey = envelopes.at(-1)?.id ?? "init";
  return (
    <div className="space-y-2" data-testid="pythia-embed-live">
      <PythiaRoundTimeline
        envelopes={envelopes}
        totalRounds={Math.max(totalRounds, envelopes.length)}
        running
      />
      {envelopes.length === 0 && (
        <p className="text-[11px] text-muted-foreground">{emptyLabel}</p>
      )}
      <AnimatePresence initial={false}>
        <motion.div
          key={lastKey}
          initial={reduceMotion ? false : { opacity: 0, scale: 0.98 }}
          animate={{
            opacity: 1,
            scale: 1,
            transition: {
              duration: UI_MOTION_DURATION.standard,
              ease: UI_EASE_OUT,
            },
          }}
          className="text-[10px] text-purple-700/70 dark:text-purple-300/70"
        >
          </motion.div>
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
  const [open, setOpen] = useState(false);
  const envelopes = run.envelopes ?? [];
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
        <span className="truncate">{t(($) => $.pythia_lab.embed_history_toggle, { time: run.created_at })}</span>
      </button>
      {open && envelopes.length > 0 && (
        <PythiaRoundTimeline envelopes={envelopes} totalRounds={run.rounds || envelopes.length} running={false} />
      )}
    </div>
  );
}