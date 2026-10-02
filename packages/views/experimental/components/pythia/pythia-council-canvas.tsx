"use client";

// pythia-council-canvas — the Pythia "council chamber" visual (0.5.132),
// ported from the approved .omc/prototypes/pythia-council-visual.html.
// A consensus dial (needle + spread band + round badge) over the engine's
// real council: the four swarm personas sit around it, each seat showing
// its latest vote; the per-round trajectory rides beneath. Everything is
// a projection of the envelopes the parent already holds — this file
// never subscribes (usePythiaIssueLab's SSE bus is single-owner).
//
// Theming/animation discipline identical to ClaudeBrainCanvas: scoped
// --council-* custom properties with a .dark override, CSS keyframe
// loops behind one prefers-reduced-motion gate, motion/react entrances
// under useReducedMotion, zero rAF dependence for correctness.

import { useMemo, useState } from "react";
import type { TFunction } from "i18next";
import { motion, useReducedMotion } from "motion/react";
import { ChevronDown, Users } from "lucide-react";
import type { PythiaForecastEnvelope } from "@multica/core/types/api";
import { useT } from "../../../i18n";
import {
  deriveConsensusTrajectory,
  deriveCouncilSeats,
  deriveSpreadBand,
  deriveVerdict,
  idleCouncilSeats,
  latestCouncil,
} from "./pythia-council-derive";

const VIEW_W = 760;
const VIEW_H = 300;
const DIAL = { cx: 380, cy: 168, r: 96 };
const SEAT_R = 21;
const DIAL_ARC = Math.PI * DIAL.r;

const COUNCIL_STYLE = `
.council-chamber{
  --council-track:rgba(9,9,11,.13);
  --council-sub:#71717a;
  --council-node:#ffffff;
}
.dark .council-chamber{
  --council-track:rgba(250,250,250,.13);
  --council-sub:#8e8e96;
  --council-node:#131316;
}
@keyframes council-dashflow{to{stroke-dashoffset:-28}}
.council-vote.speaking{stroke-dasharray:6 8;animation:council-dashflow .7s linear infinite}
@keyframes council-halo{0%{transform:scale(1);opacity:.7}100%{transform:scale(1.8);opacity:0}}
.council-halo{transform-box:fill-box;transform-origin:center;opacity:.85;animation:council-halo 1.6s ease-out infinite}
@keyframes council-spin{to{transform:rotate(360deg)}}
.council-lock{transform-box:fill-box;transform-origin:center;animation:council-spin 14s linear infinite}
.council-standby{transform-box:fill-box;transform-origin:center;animation:council-spin 30s linear infinite}
@keyframes council-blink{50%{opacity:.3}}
.council-qmark{animation:council-blink 1.6s infinite}
@keyframes council-idle-breathe{50%{opacity:.5}}
.council-seat-idle-ring{animation:council-idle-breathe 3.4s ease-in-out infinite}
@media (prefers-reduced-motion: reduce){
  .council-vote.speaking{animation:none;stroke-dasharray:6 8}
  .council-halo{display:none}
  .council-lock{animation:none}
  .council-standby{animation:none}
  .council-qmark{animation:none}
  .council-seat-idle-ring{animation:none}
}`;

function pol(t: number, r: number) {
  const a = Math.PI * (1 - t);
  return { x: DIAL.cx + Math.cos(a) * r, y: DIAL.cy - Math.sin(a) * r };
}
function arcD(t0: number, t1: number, r: number) {
  const p0 = pol(t0, r), p1 = pol(t1, r);
  return `M ${p0.x.toFixed(1)} ${p0.y.toFixed(1)} A ${r} ${r} 0 0 1 ${p1.x.toFixed(1)} ${p1.y.toFixed(1)}`;
}

type BrainT = TFunction<"experimental">;

function verdictLabel(t: BrainT, v: ReturnType<typeof deriveVerdict>): string {
  switch (v) {
    case "split": return t(($) => $.pythia_lab.council_verdict_split);
    case "likely": return t(($) => $.pythia_lab.council_verdict_likely);
    case "unlikely": return t(($) => $.pythia_lab.council_verdict_unlikely);
    case "pending": return t(($) => $.pythia_lab.council_verdict_pending);
  }
}

export function PythiaCouncilCanvas({
  envelopes,
  totalRounds,
  running,
  defaultOpen = true,
}: {
  envelopes: PythiaForecastEnvelope[];
  totalRounds: number;
  running: boolean;
  defaultOpen?: boolean;
}) {
  const { t } = useT("experimental");
  const reduceMotion = useReducedMotion() ?? false;
  const [open, setOpen] = useState(defaultOpen);

  // 0.5.133: idle chamber still seats the fixed engine roster — an empty
  // stage read as "not implemented"; a waiting council reads as live.
  const seats = useMemo(() => {
    const dataSeats = deriveCouncilSeats(envelopes);
    return dataSeats.length > 0 ? dataSeats : idleCouncilSeats();
  }, [envelopes]);
  const latest = useMemo(() => latestCouncil(envelopes), [envelopes]);
  const consensus =
    latest?.council.consensus ??
    (envelopes.length ? (envelopes[envelopes.length - 1]!.probability) : null);
  const band = useMemo(() => deriveSpreadBand(latest?.council ?? null), [latest]);
  const verdict = deriveVerdict(consensus, band);
  const round = envelopes.length;
  const planned = Math.max(totalRounds, round);

  // seat geometry: spread along a shallow arc under the dial
  const seatPos = useMemo(() => {
    const n = Math.max(seats.length, 1);
    return seats.map((_, i) => ({
      x: 130 + (i * (VIEW_W - 260)) / Math.max(n - 1, 1),
      y: 252 + Math.sin(Math.PI * (n === 1 ? 0.5 : i / (n - 1))) * 14,
    }));
  }, [seats]);

  // the newest seat with a vote is "speaking" (its comet rides the edge)
  const speakingIdx = seats.length
    ? seats.reduce((acc, s, i) => (s.probability != null ? i : acc), -1)
    : -1;

  const verdictColor =
    verdict === "split"
      ? "text-amber-600 dark:text-amber-400"
      : verdict === "likely"
        ? "text-emerald-600 dark:text-emerald-400"
        : verdict === "unlikely"
          ? "text-slate-500 dark:text-slate-400"
          : "text-muted-foreground";

  return (
    <div
      className="council-chamber overflow-hidden rounded-lg border border-border bg-card/40"
      data-testid="pythia-council-canvas"
      data-running={running ? "true" : "false"}
      data-verdict={verdict}
    >
      <style>{COUNCIL_STYLE}</style>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        data-testid="pythia-council-toggle"
        className="flex w-full items-center gap-2 border-b border-border px-3 py-1.5 text-left hover:bg-muted/40"
      >
        <Users className="size-3.5 shrink-0 text-purple-600 dark:text-purple-300" aria-hidden />
        <span className="text-xs font-medium text-foreground">
          {t(($) => $.pythia_lab.council_title)}
        </span>
        {running && (
          <span className="flex items-center gap-1 font-mono text-[10px] text-purple-700 dark:text-purple-300">
            <span className="council-qmark size-1.5 rounded-full bg-purple-500" aria-hidden />
            R{round}/{planned}
          </span>
        )}
        {!running && consensus != null && (
          <span className={`font-mono text-[10px] font-semibold ${verdictColor}`}>
            {(consensus * 100).toFixed(0)}%
          </span>
        )}
        <ChevronDown
          className={`ml-auto size-3.5 shrink-0 text-muted-foreground transition-transform ${open ? "" : "-rotate-90"}`}
          aria-hidden
        />
      </button>

      {open && (
        <div className="relative" style={{ height: 262 }} data-testid="pythia-council-stage">
          <svg viewBox={`0 0 ${VIEW_W} ${VIEW_H}`} className="h-full w-full" role="img"
              aria-label={t(($) => $.pythia_lab.council_title)}>
            {/* vote edges + comets */}
            {seats.map((seat, i) => {
              const p = seatPos[i]!;
              const dx = DIAL.cx - p.x, dy = DIAL.cy - p.y, L = Math.hypot(dx, dy) || 1;
              const speaking = running && i === speakingIdx;
              const d = `M ${p.x.toFixed(1)} ${(p.y - SEAT_R - 4).toFixed(1)} Q ${((p.x + DIAL.cx) / 2 + 20).toFixed(1)} ${((p.y + DIAL.cy) / 2 - 30).toFixed(1)} ${(DIAL.cx + (dx / L) * 34).toFixed(1)} ${(DIAL.cy + (dy / L) * 34 - 8).toFixed(1)}`;
              return (
                <g key={seat.persona} className={seat.hue}>
                  <path
                    id={`council-vote-${i}`}
                    className={`council-vote${speaking ? " speaking" : ""}`}
                    style={{
                      fill: "none",
                      stroke: speaking ? "currentColor" : "var(--council-track)",
                      strokeWidth: speaking ? 2 : 1.4,
                    }}
                    d={d}
                  />
                  {speaking && !reduceMotion && (
                    <circle r={3.2} fill="currentColor" opacity={0.95}>
                      <animateMotion dur="1.1s" repeatCount="indefinite">
                        <mpath href={`#council-vote-${i}`} />
                      </animateMotion>
                    </circle>
                  )}
                </g>
              );
            })}

            {/* consensus dial */}
            <g data-testid="pythia-council-dial" data-consensus={consensus ?? ""}>
              {/* standby ring: slow dashed orbit while no votes have landed —
                  the "in waiting" state stays visibly alive */}
              {consensus == null && (
                <circle
                  cx={DIAL.cx} cy={DIAL.cy} r={DIAL.r + 22}
                  fill="none" stroke="currentColor" strokeWidth={1.5}
                  strokeDasharray="3 9" opacity={0.45}
                  className="council-standby text-purple-500"
                />
              )}
              {!running && consensus != null && (
                <circle
                  cx={DIAL.cx} cy={DIAL.cy} r={DIAL.r + 22}
                  fill="none" stroke="currentColor" strokeWidth={2}
                  strokeDasharray="4 7" className="council-lock text-emerald-600 dark:text-emerald-400"
                />
              )}
              <path d={arcD(0, 1, DIAL.r)} fill="none" stroke="var(--council-track)" strokeWidth={9} strokeLinecap="round" />
              <path
                d={arcD(0, 1, DIAL.r)}
                fill="none"
                className="stroke-purple-500"
                strokeWidth={9}
                strokeLinecap="round"
                strokeDasharray={DIAL_ARC}
                strokeDashoffset={DIAL_ARC * (1 - (consensus ?? 0))}
                style={{ transition: "stroke-dashoffset .9s cubic-bezier(.4,0,.2,1)" }}
              />
              {band && (
                <path
                  d={arcD(band.lo, band.hi, DIAL.r + 14)}
                  fill="none"
                  stroke="rgba(168,85,247,.5)"
                  strokeWidth={7}
                  opacity={band.split ? 0.9 : 0.4}
                  style={{ transition: "opacity .5s" }}
                />
              )}
              {/* needle */}
              <line
                x1={DIAL.cx} y1={DIAL.cy}
                x2={DIAL.cx} y2={DIAL.cy - DIAL.r + 18}
                stroke="currentColor" strokeWidth={2.5} strokeLinecap="round"
                className="text-foreground"
                style={{
                  transition: "all .9s cubic-bezier(.34,1.3,.5,1)",
                  transformBox: "fill-box",
                  transformOrigin: "center bottom",
                  transform: `rotate(${((consensus ?? 0) - 0.5) * 180}deg)`,
                }}
              />
              <text x={DIAL.cx} y={DIAL.cy - 26} textAnchor="middle" fontSize={26} fontWeight={700}
                    className="fill-foreground font-mono">
                {consensus == null ? "—" : `${(consensus * 100).toFixed(0)}%`}
              </text>
              <text x={DIAL.cx} y={DIAL.cy - 10} textAnchor="middle" fontSize={9.5} fill="var(--council-sub)">
                {t(($) => $.pythia_lab.council_consensus)}
              </text>
              <text x={DIAL.cx} y={DIAL.cy + 40} textAnchor="middle" fontSize={10} fill="var(--council-sub)" className="font-mono">
                {t(($) => $.pythia_lab.council_round, { round: String(round), total: String(planned) })}
              </text>
              {!running && consensus != null && (
                <text x={DIAL.cx} y={DIAL.cy + 58} textAnchor="middle" fontSize={11.5} fontWeight={650} className={verdictColor}>
                  {verdictLabel(t, verdict)}
                </text>
              )}
            </g>

            {/* persona seats */}
            {seats.map((seat, i) => {
              const p = seatPos[i]!;
              const speaking = running && i === speakingIdx;
              return (
                <g
                  key={seat.persona}
                  className={seat.hue}
                  transform={`translate(${p.x.toFixed(1)} ${p.y.toFixed(1)})`}
                  data-testid="pythia-council-seat"
                  data-persona={seat.persona}
                >
                  <motion.g
                    initial={reduceMotion ? false : { opacity: 0 }}
                    animate={{ opacity: 1 }}
                    transition={{ delay: Math.min(i * 0.07, 0.3), duration: 0.35 }}
                  >
                    {speaking && !reduceMotion && (
                      <circle className="council-halo" r={SEAT_R + 3} fill="none" stroke="currentColor" strokeWidth={2} />
                    )}
                    <circle
                      className={!speaking && seat.probability == null && !reduceMotion ? "council-seat-idle-ring" : undefined}
                      r={SEAT_R} fill="var(--council-node)" stroke="currentColor"
                      strokeWidth={speaking ? 2.5 : 1.8} opacity={seat.probability == null ? 0.55 : 1}
                    />
                    <text y={-SEAT_R - 7} textAnchor="middle" fontSize={10.5} fontWeight={650}
                          className="fill-foreground font-mono">
                      {seat.probability == null ? "—" : `${(seat.probability * 100).toFixed(0)}%`}
                    </text>
                    <text y={4} textAnchor="middle" fontSize={9} fill="var(--council-sub)">
                      {seat.persona.slice(0, 8)}
                    </text>
                  </motion.g>
                </g>
              );
            })}
          </svg>

          {/* per-round trajectory strip (bottom) */}
          <TrajectoryStrip envelopes={envelopes} planned={planned} />
        </div>
      )}
    </div>
  );
}

function TrajectoryStrip({
  envelopes,
  planned,
}: {
  envelopes: PythiaForecastEnvelope[];
  planned: number;
}) {
  const { t } = useT("experimental");
  const traj = useMemo(() => deriveConsensusTrajectory(envelopes), [envelopes]);
  const W = 700, H = 46;
  if (traj.length === 0) return null;
  const px = (i: number) => 14 + (i * (W - 28)) / Math.max(planned - 1, 1);
  const py = (v: number) => 10 + (1 - v) * (H - 20);
  const pts = traj.map((v, i) => `${px(i).toFixed(1)},${py(v).toFixed(1)}`).join(" ");
  return (
    <div className="border-t border-border px-3 py-1" data-testid="pythia-council-traj">
      <svg viewBox={`0 0 ${W} ${H}`} className="h-9 w-full" role="img"
          aria-label={t(($) => $.pythia_lab.council_trajectory)}>
        <polyline points={pts} fill="none" className="stroke-purple-500" strokeWidth={1.8}
                  strokeLinejoin="round" strokeLinecap="round" />
        {traj.map((v, i) => (
          <circle key={i} cx={px(i).toFixed(1)} cy={py(v).toFixed(1)}
                  r={i === traj.length - 1 ? 4 : 2.6} className="fill-purple-500" />
        ))}
      </svg>
    </div>
  );
}
