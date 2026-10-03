"use client";

// claude-brain-canvas — the Claude Lab "brain" neural view (0.5.132),
// ported from the approved .omc/prototypes/claude-brain-visual.html
// design. A central orchestrator core with the six installed lab agents
// on an orbit; connection edges animate while their agent runs. Every
// node state comes from the issue's AgentTaskSnapshot rows via
// useClaudeLabIssue + the workspace agents list (name → roster slot) —
// no fabricated progress: a running node shows an indeterminate arc and
// the live elapsed time, because AgentTask has no percentage field.
//
// Variants:
//   embed     — compact, mounted at the top of ClaudeIssueEmbed inside
//               the issue main pane. Collapsible; defaults open while a
//               run is live.
//   workbench — taller canvas plus a recent-runs strip + artifact count,
//               mounted in the desktop lab page's workbench section.
//
// Theming: semantic tokens for chrome, component-scoped CSS custom
// properties for SVG internals (--brain-*) with a `.dark` override —
// the same CSS-only dual-theme contract the Shiki block in
// packages/ui/styles/base.css uses. Loop animations live in the mounted
// <style> block with a single prefers-reduced-motion gate; entrances go
// through motion/react gated by useReducedMotion.
//
// Host-hardening carried over from the prototype: nothing depends on
// requestAnimationFrame for correctness — loops are CSS keyframes and
// the elapsed ticker is a 1s setInterval (an IAB-class host can report
// "visible" while never scheduling a frame).

import { useEffect, useMemo, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useQuery } from "@tanstack/react-query";
import {
  Atom,
  Brain as BrainIcon,
  ChevronDown,
  Dna,
  LineChart,
  Microscope,
  PenLine,
  Scale,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { agentListOptions } from "@multica/core/workspace/queries";
import { formatElapsedSecs } from "../../../chat/lib/format";
import { useClaudeLabIssue } from "../../hooks/use-claude-lab-issue";
import { useT } from "../../../i18n";
import {
  BRAIN_PHASE_COUNT,
  CLAUDE_LAB_ROSTER,
  deriveBrainNodes,
  deriveBrainPhase,
  deriveRecentRuns,
  type BrainNodeState,
  type ClaudeBrainNode,
} from "./claude-brain-derive";

// --- geometry (viewBox units) -------------------------------------------
const VIEW_W = 900;
const VIEW_H = 430;
const CORE = { x: VIEW_W / 2, y: 212, r: 56 };
const NODE_R = 24;
const ORBIT = { rx: 300, ry: 148 };
const ARC_LEN = 2 * Math.PI * NODE_R;

const ROLE_ICON: Record<string, LucideIcon> = {
  research: Microscope,
  critique: Scale,
  ml: LineChart,
  physics: Atom,
  biology: Dna,
  write: PenLine,
};

const ROLE_HUE: Record<string, string> = {
  research: "text-emerald-600 dark:text-emerald-400",
  critique: "text-amber-600 dark:text-amber-400",
  ml: "text-violet-600 dark:text-violet-400",
  physics: "text-sky-600 dark:text-sky-400",
  biology: "text-teal-600 dark:text-teal-400",
  write: "text-rose-600 dark:text-rose-400",
};

function nodePosition(angleDeg: number) {
  const rad = (angleDeg * Math.PI) / 180;
  return {
    x: CORE.x + Math.cos(rad) * ORBIT.rx,
    y: CORE.y + Math.sin(rad) * ORBIT.ry,
  };
}

function edgePath(angleDeg: number, bendSign: number) {
  const p = nodePosition(angleDeg);
  const dx = p.x - CORE.x;
  const dy = p.y - CORE.y;
  const len = Math.hypot(dx, dy) || 1;
  const sx = CORE.x + (dx / len) * (CORE.r + 6);
  const sy = CORE.y + (dy / len) * (CORE.r + 6);
  const ex = p.x - (dx / len) * (NODE_R + 8);
  const ey = p.y - (dy / len) * (NODE_R + 8);
  const bend = 26 * bendSign;
  const mx = (sx + ex) / 2 - (dy / len) * bend;
  const my = (sy + ey) / 2 + (dx / len) * bend;
  return `M ${sx.toFixed(1)} ${sy.toFixed(1)} Q ${mx.toFixed(1)} ${my.toFixed(1)} ${ex.toFixed(1)} ${ey.toFixed(1)}`;
}

// All SVG-internal colors flow through these; light values here,
// dark overrides below. Chrome (card, borders, text) uses semantic
// Tailwind tokens directly.
const BRAIN_STYLE = `
.claude-brain{
  --brain-line:rgba(9,9,11,.16);
  --brain-node-fill:#ffffff;
  --brain-node-line:rgba(9,9,11,.22);
  --brain-label:#3f3f46;
  --brain-sub:#71717a;
  --brain-grid:rgba(9,9,11,.055);
  --brain-core-fill:#f4f6f5;
  --brain-glow:rgba(16,185,129,.06);
}
.dark .claude-brain{
  --brain-line:rgba(250,250,250,.15);
  --brain-node-fill:#131316;
  --brain-node-line:rgba(250,250,250,.22);
  --brain-label:#d4d4d8;
  --brain-sub:#8e8e96;
  --brain-grid:rgba(250,250,250,.05);
  --brain-core-fill:#101215;
  --brain-glow:rgba(16,185,129,.05);
}
@keyframes brain-dashflow{to{stroke-dashoffset:-32}}
.brain-edge-active{stroke-dasharray:7 9;stroke-width:2;animation:brain-dashflow .9s linear infinite}
.brain-edge-done{stroke-dasharray:none;opacity:.45}
.brain-edge-idle{stroke-dasharray:3 6;opacity:.8;animation:brain-dashflow 2.6s linear infinite}
.brain-edge-ping{stroke-dasharray:5 7;stroke-width:2;animation:brain-dashflow .5s linear infinite}
.brain-core-hot{filter:drop-shadow(0 0 18px rgba(16,185,129,.45))}
@keyframes brain-bob{50%{transform:translateY(-2px)}}
.brain-icon-bob{animation:brain-bob 1.6s ease-in-out infinite}
@keyframes brain-shake{25%{transform:translateX(-3px)}75%{transform:translateX(3px)}}
.brain-shake{animation:brain-shake .4s ease-in-out}
@keyframes brain-idle-breathe{50%{opacity:.45}}
.brain-node-idle-ring{animation:brain-idle-breathe 3.2s ease-in-out infinite}
@keyframes brain-halo{0%{transform:scale(1);opacity:.7}100%{transform:scale(1.85);opacity:0}}
.brain-halo{transform-box:fill-box;transform-origin:center;opacity:.8;animation:brain-halo 2s ease-out infinite}
.brain-halo.h2{animation-delay:1s}
@keyframes brain-arcspin{to{transform:rotate(360deg)}}
.brain-arcspin{transform-box:fill-box;transform-origin:center;animation:brain-arcspin 1.6s linear infinite}
@keyframes brain-pop{0%{transform:scale(.35);opacity:0}100%{transform:scale(1);opacity:1}}
.brain-pop{transform-box:fill-box;transform-origin:center;animation:brain-pop .38s cubic-bezier(.2,1.6,.4,1)}
@keyframes brain-breathe{50%{transform:scale(1.06)}}
.brain-breathe{transform-box:fill-box;transform-origin:center;animation:brain-breathe 3.2s ease-in-out infinite}
@keyframes brain-spin{to{transform:rotate(360deg)}}
.brain-spin{transform-box:fill-box;transform-origin:center;animation:brain-spin 26s linear infinite}
@keyframes brain-ripple{0%{transform:scale(1);opacity:.85}100%{transform:scale(3.4);opacity:0}}
.brain-ripple{transform-box:fill-box;transform-origin:center;animation:brain-ripple 1.5s ease-out forwards}
@media (prefers-reduced-motion: reduce){
  .brain-edge-active{animation:none;stroke-dasharray:7 9}
  .brain-edge-idle{animation:none}
  .brain-edge-ping{animation:none;stroke-dasharray:5 7}
  .brain-node-idle-ring{animation:none}
  .brain-halo{display:none}
  .brain-arcspin{display:none}
  .brain-pop{animation:none;opacity:1}
  .brain-breathe{animation:none}
  .brain-spin{animation:none}
  .brain-ripple{display:none}
  .brain-icon-bob{animation:none}
  .brain-shake{animation:none}
}`;

// monotonic id for transient ripple elements (animation keying only)
let rippleSeq = 0;

// Dynamic i18n keys go through explicit switch helpers, NOT template
// literal selectors — `$[`brain_agent_${key}`]` compiles but defeats both
// key typing and the arrow-expression proxy contract (the 0.5.131
// tabLabel lesson; see apps/desktop claude-lab-view.tsx). Non-roster
// keys (a raw agent id in the recent-runs strip) fall back to the id.
type BrainT = TFunction<"claude-lab">;

function brainRoleLabel(t: BrainT, key: string): string {
  switch (key) {
    case "research": return t(($) => $.brain_agent_research);
    case "critique": return t(($) => $.brain_agent_critique);
    case "ml": return t(($) => $.brain_agent_ml);
    case "physics": return t(($) => $.brain_agent_physics);
    case "biology": return t(($) => $.brain_agent_biology);
    case "write": return t(($) => $.brain_agent_write);
    default: return key;
  }
}

function brainStateLabel(t: BrainT, state: BrainNodeState): string {
  switch (state) {
    case "idle": return t(($) => $.brain_state_idle);
    case "queued": return t(($) => $.brain_state_queued);
    case "running": return t(($) => $.brain_state_running);
    case "done": return t(($) => $.brain_state_done);
    case "failed": return t(($) => $.brain_state_failed);
    case "cancelled": return t(($) => $.brain_state_cancelled);
  }
}

function brainPhaseLabel(t: BrainT, idx: number): string {
  switch (idx) {
    case 0: return t(($) => $.brain_phase_0);
    case 1: return t(($) => $.brain_phase_1);
    case 2: return t(($) => $.brain_phase_2);
    case 3: return t(($) => $.brain_phase_3);
    case 4: return t(($) => $.brain_phase_4);
    default: return String(idx);
  }
}

function BrainNodeView({
  node,
  index,
  runningElapsed,
  selected,
  onSelect,
}: {
  node: ClaudeBrainNode;
  index: number;
  runningElapsed: string;
  selected: boolean;
  onSelect: (key: string) => void;
}) {
  const { t } = useT("claude-lab");
  const reduceMotion = useReducedMotion() ?? false;
  const entry = CLAUDE_LAB_ROSTER.find((r) => r.key === node.key)!;
  const pos = nodePosition(entry.angle);
  const Icon = ROLE_ICON[node.key] ?? BrainIcon;
  const hue = ROLE_HUE[node.key];
  const running = node.state === "running";
  const dimmed = node.state === "idle" || node.state === "queued";
  const failed = node.state === "failed" || node.state === "cancelled";

  return (
    <g
      className={hue}
      style={{ cursor: "pointer" }}
      transform={`translate(${pos.x.toFixed(1)} ${pos.y.toFixed(1)})`}
      data-testid="claude-brain-node"
      data-key={node.key}
      data-state={node.state}
      role="button"
      tabIndex={0}
      aria-label={`${brainRoleLabel(t, node.key)}: ${brainStateLabel(t, node.state)}`}
      onClick={() => onSelect(node.key)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") onSelect(node.key);
      }}
    >
      {/* outer <g> owns the attribute translate; the entrance animation
          lives on an inner motion.g because motion writes style.transform,
          which would override the positioning attribute (0.5.132 port
          trap — nodes would all fly to the viewBox origin). The shake
          wrapper sits one level deeper for the same reason: its CSS
          keyframe transform must not fight the positioning attribute. */}
      <motion.g
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ delay: Math.min(index * 0.06, 0.3), duration: 0.35 }}
      >
      <g className={failed && !reduceMotion ? "brain-shake" : undefined}>
      {running && (
        <>
          <circle className="brain-halo" r={NODE_R + 3} fill="none" stroke="currentColor" strokeWidth={2} />
          <circle className="brain-halo h2" r={NODE_R + 3} fill="none" stroke="currentColor" strokeWidth={2} />
        </>
      )}
      <circle
        className={dimmed && !reduceMotion ? "brain-node-idle-ring" : undefined}
        r={NODE_R}
        fill="var(--brain-node-fill)"
        stroke={
          selected || running || node.state === "done"
            ? "currentColor"
            : "var(--brain-node-line)"
        }
        strokeWidth={selected ? 3 : 2.5}
        strokeDasharray={node.state === "queued" ? "4 4" : undefined}
        opacity={dimmed ? 0.75 : 1}
      />
      {running && (
        // indeterminate progress arc — AgentTask has no % field, so the
        // arc spins instead of pretending to know completion
        <circle
          className="brain-arcspin"
          r={NODE_R + 6}
          fill="none"
          stroke="currentColor"
          strokeWidth={2.5}
          strokeLinecap="round"
          strokeDasharray={`${ARC_LEN * 0.28} ${ARC_LEN}`}
        />
      )}
      {node.state === "done" ? (
        <g className="brain-pop">
          <circle r={NODE_R - 4} fill="currentColor" />
          <path
            d="M -8 1 L -2 7 L 9 -6"
            fill="none"
            stroke="var(--brain-node-fill)"
            strokeWidth={3}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </g>
      ) : failed ? (
        <g stroke="currentColor" strokeWidth={2.5} strokeLinecap="round">
          <path d="M -7 -7 L 7 7" />
          <path d="M 7 -7 L -7 7" />
        </g>
      ) : (
        <g className={running && !reduceMotion ? "brain-icon-bob" : undefined}>
          <g transform="translate(-8 -8) scale(0.67)" opacity={dimmed ? 0.45 : 1}>
            <Icon width={24} height={24} strokeWidth={1.8} />
          </g>
        </g>
      )}
      <text
        y={NODE_R + 20}
        textAnchor="middle"
        fontSize={12}
        fontWeight={550}
        fill="var(--brain-label)"
      >
        {brainRoleLabel(t, node.key)}
      </text>
      <text
        y={NODE_R + 34}
        textAnchor="middle"
        fontSize={10}
        fill="var(--brain-sub)"
        className="font-mono"
        data-testid="claude-brain-node-status"
      >
        {running
          ? `${brainStateLabel(t, "running")} ${runningElapsed}`
          : brainStateLabel(t, node.state)}
      </text>
      </g>
      </motion.g>
    </g>
  );
}

function BrainEdge({
  node,
  ping,
  reduceMotion,
}: {
  node: ClaudeBrainNode;
  ping: boolean;
  reduceMotion: boolean;
}) {
  const { t } = useT("claude-lab");
  const entry = CLAUDE_LAB_ROSTER.find((r) => r.key === node.key)!;
  const bendSign = CLAUDE_LAB_ROSTER.indexOf(entry) % 2 === 0 ? -1 : 1;
  const running = node.state === "running";
  const cls = running
    ? "brain-edge-active"
    : ping
      ? "brain-edge-ping"
      : node.state === "done"
        ? "brain-edge-done"
        : "brain-edge-idle";
  const edgeId = `brain-edge-${node.key}`;
  return (
    <g
      className={ROLE_HUE[node.key]}
      data-testid="claude-brain-edge"
      data-key={node.key}
      data-state={node.state}
      data-ping={ping ? "true" : "false"}
      aria-label={`${brainRoleLabel(t, node.key)}: ${brainStateLabel(t, node.state)}`}
    >
      <path
        id={edgeId}
        className={cls}
        style={{
          fill: "none",
          // inline style beats the stylesheet base, keyed by state (the
          // presentation stroke attribute would lose to any CSS rule)
          stroke:
            running || node.state === "done" || ping
              ? "currentColor"
              : "var(--brain-line)",
          strokeWidth: 1.5,
        }}
        d={edgePath(entry.angle, bendSign)}
      />
      {/* signal comet: rides a running edge at full strength, or the
          pinged idle edge as a soft ambient pulse (prototype .comet) */}
      {(running || ping) && !reduceMotion && (
        <circle r={running ? 3 : 2.3} fill="currentColor" opacity={running ? 0.95 : 0.55}>
          <animateMotion dur={running ? "1.15s" : "1.4s"} repeatCount="indefinite">
            <mpath href={`#${edgeId}`} />
          </animateMotion>
        </circle>
      )}
    </g>
  );
}

function PhasePipeline({ phase, failed }: { phase: number; failed: boolean }) {
  const { t } = useT("claude-lab");
  return (
    <ol
      className="flex items-center px-3 pt-1"
      data-testid="claude-brain-phases"
      data-phase={phase}
    >
      {Array.from({ length: BRAIN_PHASE_COUNT }, (_, i) => {
        const done = i < phase;
        const act = i === phase;
        return (
          <li key={i} className="relative flex flex-1 flex-col items-center gap-1">
            {i > 0 && (
              <span
                className={`absolute top-[4.5px] left-[-50%] h-px w-full ${
                  done || act ? "bg-emerald-500/60" : "bg-border"
                }`}
                aria-hidden
              />
            )}
            <span
              className={`relative z-[1] size-2.5 rounded-full transition-colors ${
                act && failed
                  ? "bg-red-500"
                  : act
                    ? "animate-pulse bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,.8)]"
                    : done
                      ? "bg-emerald-500/80"
                      : "bg-muted-foreground/30"
              }`}
              data-testid="claude-brain-phase"
              data-idx={i}
            />
            <span
              className={`whitespace-nowrap text-[10px] ${
                act ? "font-medium text-foreground" : "text-muted-foreground"
              }`}
            >
              {brainPhaseLabel(t, i)}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function DetailCard({
  node,
  onClose,
}: {
  node: ClaudeBrainNode;
  onClose: () => void;
}) {
  const { t } = useT("claude-lab");
  const task = node.task;
  const startedMs = Date.parse(task?.started_at ?? "") || 0;
  const completedMs = Date.parse(task?.completed_at ?? "") || 0;
  const durationMs = startedMs && completedMs ? completedMs - startedMs : null;
  return (
    <motion.div
      initial={{ opacity: 0, height: 0 }}
      animate={{ opacity: 1, height: "auto" }}
      exit={{ opacity: 0, height: 0 }}
      transition={{ duration: 0.22 }}
      className="overflow-hidden"
      data-testid="claude-brain-detail"
      data-key={node.key}
    >
      <div className="m-2 rounded-lg border border-border bg-background/80 p-2.5">
        <div className="flex items-center gap-2">
          <span className={`size-2 rounded-full ${ROLE_HUE[node.key]} bg-current`} aria-hidden />
          <span className="text-xs font-medium text-foreground">
            {brainRoleLabel(t, node.key)}
          </span>
          <span className="font-mono text-[10px] text-muted-foreground">{node.key}</span>
          <span
            className={`ml-auto rounded px-1.5 py-0.5 text-[10px] ${
              node.state === "running"
                ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                : node.state === "failed"
                  ? "bg-red-500/10 text-red-700 dark:text-red-300"
                  : "bg-muted text-muted-foreground"
            }`}
          >
            {brainStateLabel(t, node.state)}
          </span>
          <button
            type="button"
            onClick={onClose}
            aria-label={t(($) => $.brain_detail_close)}
            className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <ChevronDown className="size-3.5" aria-hidden />
          </button>
        </div>
        {node.agentDescription ? (
          <p className="mt-1.5 line-clamp-2 text-[11px] leading-relaxed text-muted-foreground">
            {node.agentDescription}
          </p>
        ) : null}
        {task ? (
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[11px]">
            <dt className="text-muted-foreground">{t(($) => $.brain_detail_trigger)}</dt>
            <dd className="truncate text-foreground" title={task.trigger_summary ?? ""}>
              {task.trigger_summary || "—"}
            </dd>
            {durationMs != null ? (
              <>
                <dt className="text-muted-foreground">{t(($) => $.brain_detail_duration)}</dt>
                <dd className="tabular-nums text-foreground">
                  {formatElapsedSecs(Math.round(durationMs / 1000))}
                </dd>
              </>
            ) : null}
            {(task.attempt ?? 1) > 1 ? (
              <>
                <dt className="text-muted-foreground">{t(($) => $.brain_detail_attempt)}</dt>
                <dd className="tabular-nums text-foreground">{task.attempt}</dd>
              </>
            ) : null}
            {task.error ? (
              <>
                <dt className="text-muted-foreground">{t(($) => $.brain_detail_error)}</dt>
                <dd className="truncate text-red-600 dark:text-red-400" title={task.error}>
                  {task.error}
                </dd>
              </>
            ) : null}
          </dl>
        ) : (
          <p className="mt-2 text-[11px] text-muted-foreground">
            {t(($) => $.brain_detail_none)}
          </p>
        )}
      </div>
    </motion.div>
  );
}

export function ClaudeBrainCanvas({
  wsId,
  issueId,
  variant = "embed",
  defaultOpen,
  className,
}: {
  wsId: string;
  issueId: string;
  variant?: "embed" | "workbench";
  defaultOpen?: boolean;
  className?: string;
}) {
  const { t } = useT("claude-lab");
  const reduceMotion = useReducedMotion() ?? false;
  const { tasks, liveTask, status, hasLive, artifacts } = useClaudeLabIssue(wsId, issueId);
  const agentsQuery = useQuery(agentListOptions(wsId));
  const agents = useMemo(() => agentsQuery.data ?? [], [agentsQuery.data]);

  const nodes = useMemo(() => deriveBrainNodes(tasks, agents), [tasks, agents]);
  const phase = useMemo(() => deriveBrainPhase(nodes, status), [nodes, status]);
  const recentRuns = useMemo(
    () => (variant === "workbench" ? deriveRecentRuns(tasks, agents) : []),
    [variant, tasks, agents],
  );
  const failedRun = status === "failed" || status === "cancelled";

  // 0.5.133: always expanded by default — the idle brain IS the visual
  // (slow edge drift + breathing nodes), collapsing it on idle made the
  // panel look unimplemented. Users can still fold it per session.
  const [open, setOpen] = useState(defaultOpen ?? true);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const selected = nodes.find((n) => n.key === selectedKey) ?? null;

  // 1s elapsed ticker while a run is live (state changes still come from
  // the 5s snapshot poll — this only moves the clock).
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!hasLive) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [hasLive]);
  const startedMs = Date.parse(liveTask?.started_at ?? liveTask?.created_at ?? "") || 0;
  const elapsed = hasLive && startedMs ? formatElapsedSecs(Math.max(0, Math.round((now - startedMs) / 1000))) : "";

  // Completion ripple: when a node leaves "running" for a terminal
  // state, pulse a ring from the core once. Skipped entirely under
  // reduced motion (the CSS layer hides the element anyway — this just
  // avoids mounting it).
  const prevStates = useRef<Record<string, BrainNodeState>>({});
  const [ripples, setRipples] = useState<Array<{ id: number; at: number }>>([]);
  useEffect(() => {
    const next: Record<string, BrainNodeState> = {};
    for (const n of nodes) {
      next[n.key] = n.state;
      const prev = prevStates.current[n.key];
      if (prev === "running" && n.state !== "running" && !reduceMotion) {
        const id = ++rippleSeq;
        setRipples((rs) => [...rs, { id, at: Date.now() }]);
        setTimeout(() => setRipples((rs) => rs.filter((r) => r.id !== id)), 1600);
      }
    }
    prevStates.current = next;
  }, [nodes, reduceMotion]);

  // 0.5.136 ambient ping: every few seconds one non-running edge lights
  // up with a soft comet for ~1.6s — the idle brain keeps transmitting
  // (the prototype's .edge.ping layer). Decorative only: it never alters
  // node state or emits data. Zero rAF — a setTimeout chain over a nodes
  // ref (so the 5s snapshot poll does not restart the rhythm).
  const nodesRef = useRef(nodes);
  nodesRef.current = nodes;
  const [pingKey, setPingKey] = useState<string | null>(null);
  useEffect(() => {
    if (reduceMotion) return;
    let alive = true;
    let offTimer: ReturnType<typeof setTimeout> | null = null;
    let nextTimer: ReturnType<typeof setTimeout> | null = null;
    const tick = () => {
      if (!alive) return;
      const candidates = nodesRef.current.filter(
        (n) => n.state === "idle" || n.state === "queued" || n.state === "done",
      );
      if (candidates.length > 0) {
        const pick = candidates[Math.floor(Math.random() * candidates.length)]!;
        setPingKey(pick.key);
        offTimer = setTimeout(() => {
          if (alive) setPingKey(null);
        }, 1600);
      }
      nextTimer = setTimeout(tick, 3400 + Math.random() * 1500);
    };
    nextTimer = setTimeout(tick, 1200);
    return () => {
      alive = false;
      if (offTimer) clearTimeout(offTimer);
      if (nextTimer) clearTimeout(nextTimer);
    };
  }, [reduceMotion]);

  const coreLabel =
    status === "running"
      ? t(($) => $.brain_core_running)
      : status === "queued"
        ? t(($) => $.brain_core_queued)
        : status === "completed"
          ? t(($) => $.brain_core_done)
          : status === "failed"
            ? t(($) => $.brain_core_failed)
            : t(($) => $.brain_core_idle);

  return (
    <div
      className={`claude-brain overflow-hidden rounded-lg border border-border bg-card/40 ${className ?? ""}`}
      data-testid="claude-brain-canvas"
      data-variant={variant}
      data-status={status}
    >
      <style>{BRAIN_STYLE}</style>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 border-b border-border px-3 py-1.5 text-left hover:bg-muted/40"
        data-testid="claude-brain-toggle"
      >
        <BrainIcon className="size-3.5 shrink-0 text-sky-600 dark:text-sky-300" aria-hidden />
        <span className="text-xs font-medium text-foreground">{t(($) => $.brain_title)}</span>
        {hasLive && (
          <span className="flex items-center gap-1 text-[10px] font-medium text-emerald-700 dark:text-emerald-300">
            <span className="size-1.5 animate-pulse rounded-full bg-emerald-500" aria-hidden />
            {elapsed}
          </span>
        )}
        {variant === "workbench" && artifacts.length > 0 && (
          <span className="ml-1 rounded border border-border px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
            {t(($) => $.brain_artifacts, { n: artifacts.length })}
          </span>
        )}
        <ChevronDown
          className={`ml-auto size-3.5 shrink-0 text-muted-foreground transition-transform ${open ? "" : "-rotate-90"}`}
          aria-hidden
        />
      </button>

      {open && (
        <div>
          <div
            className="relative"
            style={{
              height: variant === "workbench" ? 330 : 250,
              // 0.5.136: prototype-grade ambience — an emerald radial glow
              // wash under the grid (the prototype's .brain-wrap layer),
              // both tracking the theme through --brain-glow.
              backgroundImage: `radial-gradient(closest-side at 50% 46%, var(--brain-glow), transparent 72%), linear-gradient(var(--brain-grid) 1px, transparent 1px), linear-gradient(90deg, var(--brain-grid) 1px, transparent 1px)`,
              backgroundSize: "100% 100%, 42px 42px, 42px 42px",
            }}
            data-testid="claude-brain-stage"
          >
            <svg
              viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
              className="h-full w-full"
              preserveAspectRatio="xMidYMid meet"
              role="img"
              aria-label={t(($) => $.brain_title)}
            >
              <g>
                {nodes.map((n) => (
                  <BrainEdge
                    key={`edge-${n.key}`}
                    node={n}
                    ping={pingKey === n.key}
                    reduceMotion={reduceMotion}
                  />
                ))}
              </g>
              {/* orchestrator core */}
              <g
                transform={`translate(${CORE.x} ${CORE.y})`}
                className={hasLive ? "text-emerald-600 dark:text-emerald-400" : "text-muted-foreground"}
                data-testid="claude-brain-core"
                data-live={hasLive ? "true" : "false"}
              >
                {ripples.map((r) => (
                  <circle key={r.id} className="brain-ripple" r={CORE.r + 10} fill="none" stroke="currentColor" strokeWidth={2} />
                ))}
                <circle className="brain-spin" r={CORE.r + 30} fill="none" stroke="currentColor" strokeWidth={1} strokeDasharray="3 12" opacity={0.5} />
                <circle className="brain-breathe" r={CORE.r + 14} fill="none" stroke="currentColor" strokeWidth={1.4} opacity={0.35} />
                <circle r={CORE.r} fill="var(--brain-core-fill)" stroke="currentColor" strokeWidth={1.5} opacity={0.9} className={hasLive ? "brain-core-hot" : undefined} />
                <g transform="translate(-11 -20)" opacity={0.9}>
                  <BrainIcon width={22} height={22} strokeWidth={1.8} />
                </g>
                <text y={16} textAnchor="middle" fontSize={12.5} fontWeight={600} fill="var(--brain-label)">
                  {coreLabel}
                </text>
                {hasLive && elapsed ? (
                  <text y={33} textAnchor="middle" fontSize={10} fill="var(--brain-sub)" className="font-mono">
                    {elapsed}
                  </text>
                ) : null}
              </g>
              <g>
                {nodes.map((n, i) => (
                  <BrainNodeView
                    key={n.key}
                    node={n}
                    index={i}
                    runningElapsed={elapsed}
                    selected={selectedKey === n.key}
                    onSelect={(k) => setSelectedKey((cur) => (cur === k ? null : k))}
                  />
                ))}
              </g>
            </svg>
          </div>

          <PhasePipeline phase={phase} failed={failedRun} />

          <AnimatePresence initial={false}>
            {selected && (
              <DetailCard node={selected} onClose={() => setSelectedKey(null)} />
            )}
          </AnimatePresence>

          {variant === "workbench" && recentRuns.length > 0 && (
            <ul
              className="flex flex-wrap gap-1.5 border-t border-border px-3 py-2"
              data-testid="claude-brain-recent-runs"
            >
              {recentRuns.map((r, i) => (
                <li
                  key={`${r.key}-${i}`}
                  className="flex items-center gap-1.5 rounded border border-border bg-background/60 px-2 py-1 text-[10px]"
                  title={r.error ?? undefined}
                >
                  <span
                    className={`size-1.5 rounded-full ${
                      r.state === "done"
                        ? "bg-emerald-500"
                        : r.state === "failed"
                          ? "bg-red-500"
                          : "bg-muted-foreground"
                    }`}
                    aria-hidden
                  />
                  <span className="text-muted-foreground">
                    {brainRoleLabel(t, r.key)}
                  </span>
                  {r.durationMs != null && (
                    <span className="font-mono tabular-nums text-muted-foreground">
                      {formatElapsedSecs(Math.round(r.durationMs / 1000))}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
