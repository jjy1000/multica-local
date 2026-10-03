"use client";

// causal-constellation-canvas — the causal-decision "star map" (0.5.132),
// ported from the approved .omc/prototypes/causal-constellation-visual.html.
// A focus-centric projection of the REAL workspace graph: upstream causes
// on the left arc, downstream effects on the right; active edges carry
// confidence-paced flow particles; the impact cone (BFS over active
// edges, per-hop ripple) is the signature interaction — click 推演影响.
//
// Edge hues ride the existing --causal-edge-* custom properties from
// packages/ui/styles/tokens.css (light + dark values), so the map tracks
// the app theme with zero per-component color work; suggested edges get
// the "?" pulse and rejected/superseded edges fade to ghost tones —
// same status semantics as the full graph canvas.
//
// Props-driven (no queries): the causal-graph page already owns
// useCausalWorkspaceGraph; confirm/reject actions stay in the page's
// SuggestedQueue — this canvas surfaces the pending count only.

import { useEffect, useMemo, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { motion, useReducedMotion } from "motion/react";
import { ChevronDown, Compass, Zap } from "lucide-react";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";
import { useT } from "../../i18n";
import {
  projectConstellation,
  type ConstellationStar,
} from "./causal-constellation-derive";

const VIEW_W = 760;
const VIEW_H = 300;
const FOCUS = { x: 380, y: 148, r: 26 };

// status-agnostic edge tone: hue by type var, weight/opacity by status
function edgeStroke(edge: CausalEdge): { color: string; dashed: boolean; opacity: number } {
  if (edge.status === "rejected" || edge.status === "superseded") {
    return { color: "var(--causal-edge-fallback)", dashed: true, opacity: 0.22 };
  }
  const color = `var(--causal-edge-${edge.type.replace(/_/g, "-")}, var(--causal-edge-fallback))`;
  return {
    color,
    dashed: edge.status === "suggested",
    opacity: edge.status === "suggested" ? 0.55 : 0.85,
  };
}

const CONSTELLATION_STYLE = `
.constellation-map{
  --const-node:#ffffff;
  --const-sub:#71717a;
  --const-label:#3f3f46;
  --const-star:#52525b;
  --const-glow:rgba(20,184,166,.08);
  --const-grid:rgba(9,9,11,.06);
}
.dark .constellation-map{
  --const-node:#12161a;
  --const-sub:#8e8e96;
  --const-label:#d4d4d8;
  --const-star:#d4d4d8;
  --const-glow:rgba(20,184,166,.06);
  --const-grid:rgba(250,250,250,.045);
}
@keyframes const-dashflow{to{stroke-dashoffset:-26}}
.const-flow-line{animation:const-dashflow 1s linear infinite}
.const-flow-line.slow{animation-duration:1.7s}
@keyframes const-wave{0%{transform:scale(.6);opacity:.85}100%{transform:scale(2.6);opacity:0}}
.const-wave{transform-box:fill-box;transform-origin:center;animation:const-wave 1.3s ease-out forwards}
@keyframes const-qblink{50%{opacity:.25}}
.const-qmark{animation:const-qblink 1.6s infinite}
@keyframes const-spin{to{transform:rotate(360deg)}}
.const-orbit{transform-box:fill-box;transform-origin:center;animation:const-spin 26s linear infinite}
@keyframes const-twinkle{50%{opacity:.08}}
.const-twinkle{animation:const-twinkle 3.6s ease-in-out infinite}
.const-focus-glow{filter:drop-shadow(0 0 14px rgba(20,184,166,.7))}
.const-star-lit{filter:drop-shadow(0 0 10px rgba(20,184,166,.5))}
@media (prefers-reduced-motion: reduce){
  .const-flow-line{animation:none}
  .const-wave{display:none}
  .const-qmark{animation:none}
  .const-orbit{animation:none}
  .const-twinkle{animation:none}
}`;

// deterministic background starfield: FNV-1a seed over the focus id →
// xorshift stream, so the same graph always paints the same sky and a
// focus change re-rolls it exactly once (never per-render jitter from
// the graph poll).
function backgroundStars(seed: string, count = 26): Array<{ x: number; y: number; r: number; delay: number; base: number }> {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  const next = () => {
    h ^= h << 13;
    h ^= h >>> 17;
    h ^= h << 5;
    return ((h >>> 0) % 10000) / 10000;
  };
  const stars: Array<{ x: number; y: number; r: number; delay: number; base: number }> = [];
  for (let i = 0; i < count; i++) {
    stars.push({
      x: 18 + next() * (VIEW_W - 36),
      y: 14 + next() * (VIEW_H - 28),
      r: 0.7 + next() * 0.9,
      delay: next() * 3.2,
      base: 0.25 + next() * 0.35,
    });
  }
  return stars;
}

type CausalT = TFunction<"causal-graph">;

function tierLabel(t: CausalT, tier: "confirmed" | "suggested" | "rejected"): string {
  switch (tier) {
    case "confirmed": return t(($) => $.constellation_tier_confirmed);
    case "suggested": return t(($) => $.constellation_tier_suggested);
    case "rejected": return t(($) => $.constellation_tier_rejected);
  }
}

function starPosition(star: ConstellationStar, index: number, sideCount: number) {
  // left arc for upstream, right arc for downstream, spread vertically
  const row = sideCount === 1 ? 0.5 : index / (sideCount - 1);
  const x = star.side === "up" ? 96 + row * 18 : VIEW_W - 96 - row * 18;
  const y = 52 + row * (VIEW_H - 118);
  return { x, y };
}

export function CausalConstellationCanvas({
  nodes,
  edges,
  focusId,
  className,
}: {
  nodes: CausalNode[];
  edges: CausalEdge[];
  focusId: string | null;
  className?: string;
}) {
  const { t } = useT("causal-graph");
  const reduceMotion = useReducedMotion() ?? false;
  const [open, setOpen] = useState(true);

  const projection = useMemo(
    () => projectConstellation(nodes, edges, focusId),
    [nodes, edges, focusId],
  );

  // starfield seed: the focus star's id (or the first node when no focus)
  // — stable across graph polls, re-rolled only when the focus moves.
  const starSeed = projection.focus?.id ?? nodes[0]?.id ?? "empty";
  const starfield = useMemo(() => backgroundStars(starSeed), [starSeed]);

  // star geometry (per side)
  const positions = useMemo(() => {
    const up = projection.stars.filter((s) => s.side === "up");
    const down = projection.stars.filter((s) => s.side === "down");
    const map = new Map<string, { x: number; y: number }>();
    up.forEach((s, i) => map.set(s.node.id, starPosition(s, i, up.length)));
    down.forEach((s, i) => map.set(s.node.id, starPosition(s, i, down.length)));
    return map;
  }, [projection.stars]);

  // impact cone: waveSeq retrigger + per-hop glow timers
  const [waveHop, setWaveHop] = useState(0);
  const timers = useRef<Array<ReturnType<typeof setTimeout>>>([]);
  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  function runWave() {
    timers.current.forEach(clearTimeout);
    timers.current = [];
    setWaveHop(0);
    if (reduceMotion) {
      setWaveHop(projection.layers.length ? projection.layers.length + 1 : 1);
      return;
    }
    projection.layers.forEach((_, i) => {
      timers.current.push(setTimeout(() => setWaveHop(i + 1), (i + 1) * 480));
    });
    timers.current.push(setTimeout(() => setWaveHop(0), projection.layers.length * 480 + 1600));
  }
  const hopNodeIds = useMemo(() => {
    const ids = new Set<string>();
    for (let i = 0; i < waveHop && i < projection.layers.length; i++) {
      for (const id of projection.layers[i]!.nodeIds) ids.add(id);
    }
    return ids;
  }, [waveHop, projection.layers]);

  const suggestedCount = projection.tiers.find((x) => x.tier === "suggested")?.count ?? 0;

  return (
    <div
      className={`constellation-map overflow-hidden rounded-lg border border-border bg-card/40 ${className ?? ""}`}
      data-testid="causal-constellation-canvas"
      data-stars={projection.stars.length}
    >
      <style>{CONSTELLATION_STYLE}</style>
      <div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          data-testid="causal-constellation-toggle"
          className="flex min-w-0 flex-1 items-center gap-2 text-left hover:opacity-80"
        >
          <Compass className="size-3.5 shrink-0 text-teal-600 dark:text-teal-300" aria-hidden />
          <span className="text-xs font-medium text-foreground">
            {t(($) => $.constellation_title)}
          </span>
          <span className="truncate font-mono text-[10px] text-muted-foreground">
            {projection.focus ? projection.focus.label : t(($) => $.constellation_no_focus)}
          </span>
          <ChevronDown
            className={`ml-auto size-3.5 shrink-0 text-muted-foreground transition-transform ${open ? "" : "-rotate-90"}`}
            aria-hidden
          />
        </button>
        {suggestedCount > 0 && (
          <span
            className="shrink-0 rounded border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 font-mono text-[10px] text-amber-600 dark:text-amber-300"
            data-testid="causal-constellation-suggested"
          >
            {t(($) => $.constellation_suggested_n, { n: String(suggestedCount) })}
          </span>
        )}
        <button
          type="button"
          onClick={runWave}
          disabled={!projection.focus}
          data-testid="causal-constellation-wave-btn"
          className="inline-flex shrink-0 items-center gap-1 rounded-md border border-teal-500/40 bg-teal-500/10 px-2 py-0.5 text-[11px] font-medium text-teal-700 hover:bg-teal-500/20 disabled:opacity-40 dark:text-teal-300"
        >
          <Zap className="size-3" aria-hidden />
          {t(($) => $.constellation_wave)}
        </button>
      </div>

      {open && (
        <div
          className="relative"
          style={{
            height: 280,
            // 0.5.136: prototype-grade ambience — a teal radial glow wash
            // under a faint grid (the prototype's --canvas-glow layer),
            // both tracking the theme through --const-glow/--const-grid.
            backgroundImage:
              "radial-gradient(closest-side at 50% 46%, var(--const-glow), transparent 72%), linear-gradient(var(--const-grid) 1px, transparent 1px), linear-gradient(90deg, var(--const-grid) 1px, transparent 1px)",
            backgroundSize: "100% 100%, 42px 42px, 42px 42px",
          }}
          data-testid="causal-constellation-stage"
        >
            <svg viewBox={`0 0 ${VIEW_W} ${VIEW_H}`} className="h-full w-full" role="img"
                aria-label={t(($) => $.constellation_title)}>
            {/* background starfield: faint deterministic stars with a slow
                twinkle — the "sky" the constellation hangs in. Present in
                the empty state too, so a fresh workspace reads as night
                sky, not a blank card. */}
            <g data-testid="causal-constellation-starfield" aria-hidden>
              {starfield.map((s, i) => (
                <circle
                  key={i}
                  cx={s.x.toFixed(1)}
                  cy={s.y.toFixed(1)}
                  r={s.r.toFixed(2)}
                  fill="var(--const-star)"
                  opacity={s.base.toFixed(2)}
                  className={reduceMotion ? undefined : "const-twinkle"}
                  style={{ animationDelay: `${s.delay.toFixed(2)}s` }}
                />
              ))}
            </g>
            {/* 0.5.133 empty state: an animated placeholder (slowly orbiting
                dashed ring + hint) so a fresh workspace still reads as a
                live map rather than a blank box */}
            {!projection.focus && (
              <g data-testid="causal-constellation-empty">
                <circle
                  cx={VIEW_W / 2} cy={VIEW_H / 2} r={34}
                  fill="none" stroke="var(--causal-edge-causes)" strokeWidth={1.5}
                  strokeDasharray="3 9" opacity={0.5}
                  className={reduceMotion ? undefined : "const-orbit"}
                />
                <text x={VIEW_W / 2} y={VIEW_H / 2 + 66} textAnchor="middle" fontSize={11}
                      fill="var(--const-sub)">
                  {t(($) => $.constellation_empty)}
                </text>
              </g>
            )}
            {/* edges focus<->stars (+ star<->star within kept set) */}
            {projection.edges.map(({ edge, fromId, toId }) => {
              const a = fromId === projection.focus?.id
                ? { x: FOCUS.x, y: FOCUS.y }
                : positions.get(fromId);
              const b = toId === projection.focus?.id
                ? { x: FOCUS.x, y: FOCUS.y }
                : positions.get(toId);
              // only draw edges that touch a positioned star or the focus
              if (!a || !b) return null;
              const tone = edgeStroke(edge);
              const dx = b.x - a.x, dy = b.y - a.y, L = Math.hypot(dx, dy) || 1;
              const ra = fromId === projection.focus?.id ? FOCUS.r + 5 : 15;
              const rb = toId === projection.focus?.id ? FOCUS.r + 5 : 15;
              const x1 = a.x + (dx / L) * ra, y1 = a.y + (dy / L) * ra;
              const x2 = b.x - (dx / L) * rb, y2 = b.y - (dy / L) * rb;
              const mx = (a.x + b.x) / 2 - (dy / L) * 14, my = (a.y + b.y) / 2 + (dx / L) * 14;
              const id = `const-e-${edge.id}`;
              return (
                <g key={edge.id} data-testid="causal-constellation-edge" data-status={edge.status} data-type={edge.type}>
                  <path
                    id={id}
                    d={`M ${x1.toFixed(1)} ${y1.toFixed(1)} Q ${mx.toFixed(1)} ${my.toFixed(1)} ${x2.toFixed(1)} ${y2.toFixed(1)}`}
                    fill="none"
                    stroke={tone.color}
                    strokeWidth={(1.1 + (edge.confidence ?? 0.3) * 2).toFixed(1)}
                    strokeDasharray={tone.dashed ? "6 7" : undefined}
                    opacity={tone.opacity}
                  />
                  {edge.status === "active" && !reduceMotion && (
                    <circle r={2.4} fill={tone.color} opacity={0.9} className="const-flow-line">
                      <animateMotion dur={`${(1.9 - (edge.confidence ?? 0.4) * 0.8).toFixed(2)}s`} repeatCount="indefinite">
                        <mpath href={`#${id}`} />
                      </animateMotion>
                    </circle>
                  )}
                  {edge.status === "suggested" && (
                    <text
                      x={mx.toFixed(1)} y={(my - 6).toFixed(1)}
                      textAnchor="middle" fontSize={11} fontWeight={700}
                      className="const-qmark fill-amber-500"
                    >?</text>
                  )}
                </g>
              );
            })}

            {/* focus star + impact ripples */}
            {projection.focus && (
              <g transform={`translate(${FOCUS.x} ${FOCUS.y})`} data-testid="causal-constellation-focus">
                {waveHop > 0 && !reduceMotion && (
                  <circle className="const-wave" r={FOCUS.r + 8} fill="none" stroke="var(--causal-edge-causes)" strokeWidth={2} />
                )}
                <circle
                  r={FOCUS.r + 12} fill="none" stroke="rgba(20,184,166,.35)" strokeWidth={1}
                  strokeDasharray="2 8" className={reduceMotion ? undefined : "const-orbit"}
                />
                <circle r={FOCUS.r} fill="var(--const-node)" stroke="var(--causal-edge-causes)" strokeWidth={2} className="const-focus-glow" />
                <g transform="translate(-9 -9)">
                  <Compass width={18} height={18} strokeWidth={1.8} className="text-teal-600 dark:text-teal-300" />
                </g>
                <text y={FOCUS.r + 20} textAnchor="middle" fontSize={11.5} fontWeight={600} fill="var(--const-label)">
                  {projection.focus.label}
                </text>
                <text y={FOCUS.r + 33} textAnchor="middle" fontSize={9.5} fill="var(--const-sub)" className="font-mono">
                  {t(($) => $.constellation_impacted, { n: String(projection.impactedCount) })}
                </text>
              </g>
            )}

            {/* upstream / downstream stars */}
            {projection.stars.map((star, i) => {
              const p = positions.get(star.node.id)!;
              const r = 14 + Math.min(star.degree, 4);
              const lit = hopNodeIds.has(star.node.id);
              return (
                <g
                  key={star.node.id}
                  transform={`translate(${p.x.toFixed(1)} ${p.y.toFixed(1)})`}
                  data-testid="causal-constellation-star"
                  data-side={star.side}
                  data-id={star.node.id}
                >
                  <motion.g
                    initial={reduceMotion ? false : { opacity: 0 }}
                    animate={{ opacity: 1 }}
                    transition={{ delay: Math.min(i * 0.06, 0.3), duration: 0.35 }}
                  >
                    {lit && !reduceMotion && (
                      <circle className="const-wave" r={r + 4} fill="none" stroke="var(--causal-edge-causes)" strokeWidth={2} />
                    )}
                    <circle
                      r={r}
                      fill="var(--const-node)"
                      stroke={lit ? "var(--causal-edge-causes)" : "var(--const-line, rgba(148,163,184,.45))"}
                      strokeWidth={lit ? 2.4 : 1.6}
                      className={lit ? "const-star-lit" : undefined}
                    />
                    <text y={4} textAnchor="middle" fontSize={10} fill="var(--const-sub)">
                      {star.side === "up" ? "◈" : "◇"}
                    </text>
                    <text y={r + 14} textAnchor="middle" fontSize={10} fontWeight={550} fill="var(--const-label)">
                      {star.node.label.length > 9 ? `${star.node.label.slice(0, 8)}…` : star.node.label}
                    </text>
                    <text y={r + 26} textAnchor="middle" fontSize={9} fill="var(--const-sub)" className="font-mono">
                      {star.degree}{star.confidence > 0 ? ` · ${(star.confidence * 100).toFixed(0)}%` : ""}
                    </text>
                  </motion.g>
                </g>
              );
            })}
          </svg>
        </div>
      )}

      {/* trust ladder chips (status-honest; Tier D law in the title attr) */}
      <div className="flex flex-wrap items-center gap-1.5 border-t border-border px-3 py-1.5"
          data-testid="causal-constellation-tiers"
          title={t(($) => $.constellation_tier_note)}>
        {projection.tiers.map((tier) => (
          <span
            key={tier.tier}
            className={`rounded border px-1.5 py-0.5 font-mono text-[10px] ${
              tier.tier === "confirmed"
                ? "border-emerald-500/40 text-emerald-600 dark:text-emerald-300"
                : tier.tier === "suggested"
                  ? "border-amber-500/40 text-amber-600 dark:text-amber-300"
                  : "border-border text-muted-foreground"
            }`}
          >
            {tierLabel(t, tier.tier)} ×{tier.count}
          </span>
        ))}
      </div>
    </div>
  );
}
