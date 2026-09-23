"use client";

// pythia-report-view — the report + replay pieces of the Pythia issue
// panel (0.5.111, SocialSim alignment):
//
//   PythiaReportView   — renders the LLM-synthesized conclusion report
//     (a small markdown subset renderer: ##/### headings, lists, bold,
//     paragraphs — no HTML sink, so no sanitizer needed), with the
//     injected-variables echo and the mechanical per-round digest as
//     fallback when synthesis failed.
//   PythiaReplayPlayer — the DeductionReplayViewer counterpart: steps a
//     finished run's envelopes round by round with play/pause/speed/
//     step/slider controls (pure state + CSS transitions).

import { useEffect, useMemo, useRef, useState } from "react";
import { Pause, Play, RotateCcw, SkipBack, SkipForward } from "lucide-react";
import type { PythiaForecastEnvelope, PythiaForecastRun } from "@multica/core/types/api";
import { useT } from "../../../i18n";
import { PythiaRoundTimeline } from "./pythia-round-view";

/** Minimal markdown-subset renderer for the synthesized report. Block
 *  level: headings (##/###), unordered/ordered list items, paragraphs.
 *  Inline: **bold** only. Text is rendered as plain text nodes — the
 *  report is model output but never HTML. */
export function renderReportMarkdown(md: string): React.ReactNode[] {
  const lines = md.split("\n");
  const out: React.ReactNode[] = [];
  let list: { ordered: boolean; items: string[] } | null = null;

  const flushList = (key: string) => {
    if (!list) return;
    const Tag = list.ordered ? "ol" : "ul";
    out.push(
      <Tag key={key} className="my-1 ml-4 list-disc space-y-0.5 text-xs leading-relaxed text-foreground/85">
        {list.items.map((item, i) => (
          <li key={i}>{renderInline(item)}</li>
        ))}
      </Tag>,
    );
    list = null;
  };

  lines.forEach((raw, i) => {
    const line = raw.trimEnd();
    const heading = /^(#{1,4})\s+(.*)$/.exec(line);
    const bullet = /^[-*]\s+(.*)$/.exec(line.trim());
    const ordered = /^(\d+)[.、)]\s+(.*)$/.exec(line.trim());
    if (heading && heading[2] != null) {
      flushList(`l${i}`);
      const level = heading[1]?.length ?? 2;
      out.push(
        level <= 2 ? (
          <h4 key={i} className="mt-2 text-xs font-semibold text-foreground">
            {renderInline(heading[2])}
          </h4>
        ) : (
          <h5 key={i} className="mt-1.5 text-[11px] font-semibold text-foreground/90">
            {renderInline(heading[2])}
          </h5>
        ),
      );
    } else if (bullet && bullet[1] != null) {
      if (!list || list.ordered) {
        flushList(`l${i}`);
        list = { ordered: false, items: [] };
      }
      list.items.push(bullet[1]);
    } else if (ordered && ordered[2] != null) {
      if (!list || !list.ordered) {
        flushList(`l${i}`);
        list = { ordered: true, items: [] };
      }
      list.items.push(ordered[2]);
    } else if (line.trim() === "") {
      flushList(`l${i}`);
    } else {
      flushList(`l${i}`);
      out.push(
        <p key={i} className="my-0.5 text-xs leading-relaxed text-foreground/85">
          {renderInline(line)}
        </p>,
      );
    }
  });
  flushList("l-end");
  return out;
}

function renderInline(text: string): React.ReactNode[] {
  const parts = text.split(/(\*\*[^*]+\*\*)/g);
  return parts.map((part, i) => {
    const bold = /^\*\*([^*]+)\*\*$/.exec(part);
    if (bold) {
      return (
        <strong key={i} className="font-semibold text-foreground">
          {bold[1]}
        </strong>
      );
    }
    return <span key={i}>{part}</span>;
  });
}

export function PythiaReportView({
  run,
  liveReport,
}: {
  run: PythiaForecastRun | null;
  /** A report that arrived over the live stream before the run row
   *  refetched — preferred when present. */
  liveReport?: string;
}) {
  const { t } = useT("experimental");
  const report = (liveReport || run?.report || "").trim();
  if (report) {
    return (
      <div className="space-y-2" data-testid="pythia-report">
        {run?.variables && (
          <p className="rounded-md border border-purple-500/30 bg-purple-500/5 px-2 py-1 text-[11px] text-foreground/85">
            <span className="font-medium">{t(($) => $.pythia_lab.variables_label)}</span>{" "}
            {run.variables}
          </p>
        )}
        <div className="rounded-md border border-border px-2 py-1.5">{renderReportMarkdown(report)}</div>
      </div>
    );
  }
  // Synthesis fell back (engine down) — show the mechanical digest so the
  // tab is never empty, with an honest hint.
  return (
    <div className="space-y-2" data-testid="pythia-report-fallback">
      {run && run.envelopes.length > 0 ? (
        <PythiaRoundTimeline envelopes={run.envelopes} totalRounds={run.rounds} running={false} />
      ) : (
        <p className="text-xs text-muted-foreground">{t(($) => $.lab_output_panel.empty)}</p>
      )}
    </div>
  );
}

const REPLAY_BASE_MS = 1400;

export function PythiaReplayPlayer({ envelopes }: { envelopes: PythiaForecastEnvelope[] }) {
  const { t } = useT("experimental");
  const [index, setIndex] = useState(Math.max(0, envelopes.length - 1));
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(1);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    if (timer.current) clearInterval(timer.current);
    timer.current = null;
    if (!playing || envelopes.length === 0) return;
    timer.current = setInterval(() => {
      setIndex((i) => {
        if (i >= envelopes.length - 1) {
          setPlaying(false);
          return i;
        }
        return i + 1;
      });
    }, REPLAY_BASE_MS / speed);
    return () => {
      if (timer.current) clearInterval(timer.current);
    };
  }, [playing, speed, envelopes.length]);

  const visible = useMemo(() => envelopes.slice(0, index + 1), [envelopes, index]);
  const atEnd = index >= envelopes.length - 1;

  if (envelopes.length === 0) {
    return <p className="text-xs text-muted-foreground">{t(($) => $.lab_output_panel.empty)}</p>;
  }

  return (
    <div className="space-y-2" data-testid="pythia-replay">
      <div className="flex items-center gap-1.5">
        <button
          type="button"
          aria-label={t(($) => $.pythia_lab.replay_step_back)}
          onClick={() => {
            setPlaying(false);
            setIndex((i) => Math.max(0, i - 1));
          }}
          className="rounded p-1 text-foreground/70 hover:bg-muted disabled:opacity-40"
          disabled={index === 0}
        >
          <SkipBack className="size-3.5" aria-hidden />
        </button>
        <button
          type="button"
          aria-label={playing ? t(($) => $.pythia_lab.replay_pause) : t(($) => $.pythia_lab.replay_play)}
          onClick={() => {
            if (atEnd) setIndex(0);
            setPlaying((v) => !v);
          }}
          className="rounded bg-purple-500/10 p-1 text-purple-600 hover:bg-purple-500/20 dark:text-purple-300"
        >
          {playing ? <Pause className="size-3.5" aria-hidden /> : <Play className="size-3.5" aria-hidden />}
        </button>
        <button
          type="button"
          aria-label={t(($) => $.pythia_lab.replay_step_forward)}
          onClick={() => {
            setPlaying(false);
            setIndex((i) => Math.min(envelopes.length - 1, i + 1));
          }}
          className="rounded p-1 text-foreground/70 hover:bg-muted disabled:opacity-40"
          disabled={atEnd}
        >
          <SkipForward className="size-3.5" aria-hidden />
        </button>
        <button
          type="button"
          aria-label={t(($) => $.pythia_lab.replay_restart)}
          onClick={() => {
            setPlaying(false);
            setIndex(0);
          }}
          className="rounded p-1 text-foreground/70 hover:bg-muted"
        >
          <RotateCcw className="size-3.5" aria-hidden />
        </button>
        <span className="ml-auto font-mono text-[10px] text-muted-foreground">
          {index + 1}/{envelopes.length}
        </span>
        <select
          aria-label={t(($) => $.pythia_lab.replay_speed)}
          value={speed}
          onChange={(e) => setSpeed(Number(e.target.value))}
          className="rounded border border-border bg-background px-1 py-0.5 text-[10px] text-foreground"
        >
          {[1, 2, 4].map((s) => (
            <option key={s} value={s}>{`${s}×`}</option>
          ))}
        </select>
      </div>
      <input
        type="range"
        min={0}
        max={envelopes.length - 1}
        value={index}
        aria-label={t(($) => $.pythia_lab.replay_slider)}
        onChange={(e) => {
          setPlaying(false);
          setIndex(Number(e.target.value));
        }}
        className="w-full accent-purple-500"
      />
      <PythiaRoundTimeline envelopes={visible} totalRounds={envelopes.length} running={false} />
    </div>
  );
}
