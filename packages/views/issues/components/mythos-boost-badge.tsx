"use client";

import { Network } from "lucide-react";
import { useExperimentalFlag } from "@multica/core/experimental";
import {
  HoverCard,
  HoverCardTrigger,
  HoverCardContent,
} from "@multica/ui/components/ui/hover-card";
import { cn } from "@multica/ui/lib/utils";

interface MythosBoostBadgeProps {
  className?: string;
  /**
   * The issue's own `lab_source` field. When non-null and equal to
   * `"mythos_swarm"`, the badge renders regardless of whether the
   * `mythos_swarm` Labs flag is currently enabled — so historical
   * issues tagged before the flag was flipped off (or never enabled
   * in this workspace) still surface their swarm-affiliation badge
   * on list rows / board cards.
   *
   * When the prop is omitted / null, the component falls back to the
   * flag check (the original 0.3.22 behaviour): hide entirely until
   * the flag is on, so non-mythos-flagged users see no extra chrome.
   */
  issueLabSource?: string | null;
}

/**
 * "Mythos Boost" badge — shown next to the agent activity indicator
 * on issue rows / board cards whenever this issue was tagged with
 * the `mythos_swarm` lab source, OR when the `mythos_swarm` Labs flag
 * is currently on (covers the workspace-level "every new issue may
 * run on swarm" affordance).
 *
 * Visual language: a small Network glyph with a breathing emerald
 * halo + the literal label "OpenMythos" so users immediately
 * recognise that this issue is being processed by the RDT three-
 * stage runner (prelude → loop agents → coda) instead of the
 * single-agent path.
 *
 * The badge itself is purely a UI affordance — Mythos activation per
 * issue is decided server-side by the multica-mythos Skill adapter
 * (see server/internal/service/builtin_skills/multica-mythos/SKILL.md).
 * This component just makes the flag's effect (or this specific
 * issue's lab tag) visible to the user so they understand why a task
 * suddenly has multiple agents on it.
 *
 * Hard rules:
 *
 *   1. Flag-gated for NEW (no `issueLabSource`) reads — returns
 *      null when the flag is off AND the issue carries no lab tag.
 *      Same bypass contract as the rest of the Labs framework
 *      (0.3.6 hard rule #1).
 *
 *   2. Historical / persisted lab tags win — an issue with
 *      `lab_source = "mythos_swarm"` always shows the badge even
 *      when the flag is off, so users can still see "this is a
 *      swarm run" at a glance on historical issue lists.
 *
 *   3. No async loading. The flag is in-memory; no spinner / skeleton.
 *      The badge appears instantly when the flag flips on.
 *
 *   4. Tooltip is the discoverability surface. The label "OpenMythos"
 *      alone is opaque to first-time users; the tooltip explains in
 *      one sentence what changes.
 */
export function MythosBoostBadge({ className, issueLabSource }: MythosBoostBadgeProps) {
  const flagEnabled = useExperimentalFlag("mythos_swarm", false);
  // 0.3.32: historical issue wins. If the issue row carries a
  // `lab_source === "mythos_swarm"` tag from an earlier enabled
  // window, we want the badge to keep showing on list rows / board
  // cards even if the user has flipped the flag off since. Without
  // this branch, de-flagging the workspace would silently hide the
  // swarm-affiliation chrome for every pre-existing tagged issue,
  // which is a regression: the issue IS still a swarm run; only
  // NEW tagging is disabled.
  const tagged = issueLabSource === "mythos_swarm";
  if (!flagEnabled && !tagged) return null;

  return (
    <HoverCard>
      <HoverCardTrigger>
        <span
          aria-label="OpenMythos 增强运行中"
          className={cn(
            "inline-flex items-center gap-1 rounded-full border border-emerald-300/70 bg-emerald-50/70 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700",
            "dark:border-emerald-700/60 dark:bg-emerald-950/40 dark:text-emerald-300",
            "mythos-boost-pulse",
            className,
          )}
        >
          <Network className="size-3" aria-hidden />
          OpenMythos
        </span>
      </HoverCardTrigger>
      <HoverCardContent side="top" sideOffset={4} className="max-w-xs text-xs leading-relaxed">
        蜂群拓扑已激活。本任务由 prelude / loop / coda 多智能体协作完成,推理深度与自主能力显著增强。
      </HoverCardContent>
    </HoverCard>
  );
}