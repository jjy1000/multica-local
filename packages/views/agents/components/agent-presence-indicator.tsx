"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { availabilityConfig, workloadConfig } from "../presence";
import { useT } from "../../i18n";
import { motion, useReducedMotion } from "motion/react";

interface PresenceIndicatorProps {
  // null/undefined = still loading. Caller passes the detail computed at
  // the page level (or via the useAgentPresenceDetail hook for single-agent
  // views). Keeping this as a prop avoids per-row hook subscriptions in
  // long lists.
  detail: AgentPresenceDetail | null | undefined;
  // Compact = dot only, no label / no workload chip. Used in dense rows.
  compact?: boolean;
}

/**
 * Renders an agent's two-dimension presence: an availability dot + an
 * optional workload chip. The dot's colour reads only from the
 * availability dimension (3 colours), so a runtime-healthy agent whose
 * last task failed shows a green dot — workload no longer carries
 * historical state at all.
 *
 * Compact mode collapses to dot-only — used in dense surfaces where the
 * full chip would crowd the row.
 *
 * Pure presentation — takes the already-derived detail object as a prop.
 * The page-level component is responsible for sourcing it (via
 * `useAgentPresenceDetail` for a single agent, or `useWorkspacePresenceMap`
 * for lists).
 */
export function AgentPresenceIndicator({
  detail,
  compact,
}: PresenceIndicatorProps) {
  const { t } = useT("agents");
  // Hooks must run on every render, BEFORE any early return — see
  // rules-of-hooks. The reduced-motion preference is read once at the top of
  // the function and threaded into `shouldPulse` so the Skeleton branch (when
  // `detail` is still resolving) keeps the same hook count as the resolved
  // branch. P0 audit fix (multica-0.3.2 code review): moving the hook below
  // the `if (!detail)` early return would have caused React's "Rendered more
  // hooks than during the previous render" exception every time an agent's
  // presence detail resolved after the row mounted.
  const reducedMotion = useReducedMotion();

  if (!detail) {
    return compact ? (
      <Skeleton className="h-1.5 w-1.5 rounded-full" />
    ) : (
      <Skeleton className="h-3 w-24 rounded" />
    );
  }

  const av = availabilityConfig[detail.availability];
  const wl = workloadConfig[detail.workload];
  const availabilityLabel = t(($) => $.availability[detail.availability]);
  const workloadLabel = t(($) => $.workload[detail.workload]);
  const isWorking = detail.workload === "working";
  const isQueued = detail.workload === "queued";
  const showQueueBadge = isWorking && detail.queuedCount > 0;
  // Queued's amber comes from workloadConfig as the *severe* tone — meant
  // for "stuck on offline runtime", which is the dominant cause. But on a
  // healthy runtime, queued is just a brief race between enqueue and the
  // daemon's claim, and amber there reads as a warning that isn't there.
  // Compose with availability: online ⇒ muted (transient), otherwise ⇒
  // keep amber (genuine stuck signal).
  const queuedTone =
    detail.availability === "online" ? "text-muted-foreground" : wl.textClass;

  // 0.3.2 motion (memory multica-0.3.2): online agents get a subtle
  // pulsing dot so the user can immediately tell "this agent is alive
  // and dispatchable" without watching the WS heartbeat. The pulse is
  // compositor-only (transform + opacity, no layout), respects
  // `prefers-reduced-motion`, and only runs while `availability` is
  // "online" — offline / unstable agents stay static so the user can
  // distinguish a hard failure from a transient blip at a glance.
  const shouldPulse = reducedMotion !== true && detail.availability === "online";

  if (compact) {
    return (
      <span
        className="inline-flex items-center"
        title={`${availabilityLabel}${detail.workload !== "idle" ? ` · ${workloadLabel}` : ""}`}
      >
        <motion.span
          className={`h-1.5 w-1.5 shrink-0 rounded-full ${av.dotClass}`}
          aria-hidden
          animate={
            shouldPulse
              ? { scale: [1, 1.35, 1], opacity: [0.85, 1, 0.85] }
              : { scale: 1, opacity: 1 }
          }
          transition={
            shouldPulse
              ? { duration: 1.6, repeat: Infinity, ease: "easeInOut" }
              : { duration: 0 }
          }
        />
      </span>
    );
  }

  return (
    <span className="inline-flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
      {/* Availability — dot + label. Single dimension, single colour. */}
      <span className="inline-flex items-center gap-1.5">
        <motion.span
          className={`h-1.5 w-1.5 shrink-0 rounded-full ${av.dotClass}`}
          aria-hidden
          animate={
            shouldPulse
              ? { scale: [1, 1.35, 1], opacity: [0.85, 1, 0.85] }
              : { scale: 1, opacity: 1 }
          }
          transition={
            shouldPulse
              ? { duration: 1.6, repeat: Infinity, ease: "easeInOut" }
              : { duration: 0 }
          }
        />
        <span className={`text-xs ${av.textClass}`}>{availabilityLabel}</span>
      </span>

      {/* Workload — separator + label, with counts when working/queued.
          All three workload states render here for symmetry: idle gets
          its own "Idle" label so the difference between "no presence
          data" (no chip at all) and "agent is idle" (explicit Idle chip)
          is visible. Archived agents skip the workload chip entirely —
          "Archived" already says everything; "Archived · Idle" is noise. */}
      {detail.availability !== "archived" && (
      <span className="inline-flex items-center gap-1">
        <span className="text-xs text-muted-foreground">·</span>
        <span
          className={`text-xs ${
            isQueued ? queuedTone : wl.textClass
          }`}
        >
          {workloadLabel}
        </span>
        {isWorking && (
          <span className="font-mono text-xs tabular-nums text-muted-foreground">
            {detail.runningCount} / {detail.capacity}
          </span>
        )}
        {showQueueBadge && (
          <span className="rounded-md bg-muted px-1 py-0 text-xs font-medium text-muted-foreground">
            {t(($) => $.presence.queue_badge, { count: detail.queuedCount })}
          </span>
        )}
        {/* Queued (no running) — show the queued count directly, since
            there's no running/capacity ratio to anchor on. Honestly
            surfaces "stuck" on offline runtimes. */}
        {isQueued && (
          <span className="font-mono text-xs tabular-nums text-muted-foreground">
            {detail.queuedCount}
          </span>
        )}
      </span>
      )}
    </span>
  );
}
