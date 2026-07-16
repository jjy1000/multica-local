"use client";

import { FlaskConical, Network } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import {
  HoverCard,
  HoverCardTrigger,
  HoverCardContent,
} from "@multica/ui/components/ui/hover-card";

export interface LabBadgeProps {
  /** The issue's persisted `lab_source` value, or null. */
  labSource: string | null | undefined;
  className?: string;
}

/**
 * LabBadge — a single, generic chrome element rendered next to the
 * agent activity indicator on issue rows / board cards whenever
 * the bound issue carries a `lab_source` tag.
 *
 * Why a single component for every lab flag (replacing the prior
 * `MythosBoostBadge` which only handled `mythos_swarm`):
 *
 *   1. Persistence wins. The badge fires purely off the persisted
 *      `issue.lab_source` column — NOT off the runtime flag.
 *      A workspace that disables `mythos_swarm` after issuing
 *      tagged issues would otherwise lose every "this is a swarm
 *      run" hint on the list / board, which is a regression of
 *      perceived state. Same contract for pythia / claude-lab /
 *      llm-wiki / code-canvas / etc.
 *
 *   2. Hard rule #1 (flag-off fully bypass) still holds for the
 *      NEW-issue path: the LabPicker refuses to write a flag-off
 *      lab onto a fresh issue (server gate rejects 0/3-way tuples).
 *      This badge just visualises what is already persisted.
 *
 *   3. Adding a new lab means adding one entry to LAB_BADGES below
 *      — no per-flag component, no per-flag hook, no per-flag
 *      catalog wiring.
 */

interface LabBadgeSpec {
  /** Short label rendered inside the pill. */
  label: string;
  /** Tailwind utility prefix bag applied to the pill (border + bg + fg). */
  toneClassName: string;
  /** Dark-mode override applied via the `dark:` variant. */
  toneDarkClassName: string;
  /** Tooltip body explaining what the lab does. Plain Chinese. */
  tooltip: string;
  /** Aria label announced to screen readers. */
  ariaLabel: string;
}

/**
 * Source-of-truth catalogue for lab flag → badge styling.
 *
 * The keys MUST match the `lab_source` column literal that the
 * server-side gate (`experimental.IsKnownKey()`) accepts.
 */
const LAB_BADGES: Record<string, LabBadgeSpec> = {
  mythos_swarm: {
    label: "OpenMythos",
    toneClassName: "border-emerald-300/70 bg-emerald-50/70 text-emerald-700",
    toneDarkClassName:
      "dark:border-emerald-700/60 dark:bg-emerald-950/40 dark:text-emerald-300",
    tooltip:
      "蜂群拓扑已激活。本任务由 prelude / loop / coda 多智能体协作完成,推理深度与自主能力显著增强。",
    ariaLabel: "Mythos 蜂群增强运行中",
  },
  pythia_oracle: {
    label: "Pythia",
    toneClassName: "border-violet-300/70 bg-violet-50/70 text-violet-700",
    toneDarkClassName:
      "dark:border-violet-700/60 dark:bg-violet-950/40 dark:text-violet-300",
    tooltip:
      "Pythia 多视角推演已绑定。本任务接收 10 轮多角色联合预测,生成情景与概率分布。",
    ariaLabel: "Pythia 多视角推演已绑定",
  },
  claude_science_lab: {
    label: "Claude Lab",
    toneClassName: "border-sky-300/70 bg-sky-50/70 text-sky-700",
    toneDarkClassName:
      "dark:border-sky-700/60 dark:bg-sky-950/40 dark:text-sky-300",
    tooltip:
      "Claude 科研实验室已绑定。本任务由科研 agent 在隔离沙箱中执行,支持长链路工具调用。",
    ariaLabel: "Claude 科研实验室已绑定",
  },
  llm_wiki_bridge: {
    label: "LLM Wiki",
    toneClassName: "border-amber-300/70 bg-amber-50/70 text-amber-700",
    toneDarkClassName:
      "dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-300",
    tooltip:
      "LLM Wiki 本地桥接已绑定。本任务的上下文通过本地桥接同步至 LLM Wiki 知识库。",
    ariaLabel: "LLM Wiki 本地桥接已绑定",
  },
  code_canvas: {
    label: "Code Canvas",
    toneClassName: "border-rose-300/70 bg-rose-50/70 text-rose-700",
    toneDarkClassName:
      "dark:border-rose-700/60 dark:bg-rose-950/40 dark:text-rose-300",
    tooltip: "代码画布已绑定。本任务的可视化代码工作区同步到本地画布视图。",
    ariaLabel: "代码画布已绑定",
  },
  agent_self_optimization: {
    label: "Self-Opt",
    toneClassName: "border-teal-300/70 bg-teal-50/70 text-teal-700",
    toneDarkClassName:
      "dark:border-teal-700/60 dark:bg-teal-950/40 dark:text-teal-300",
    tooltip:
      "智能体自优化已绑定。本任务的执行 agent 在运行中持续整定自身的指令与策略。",
    ariaLabel: "智能体自优化已绑定",
  },
  constitution_agent: {
    label: "Constitution",
    toneClassName: "border-indigo-300/70 bg-indigo-50/70 text-indigo-700",
    toneDarkClassName:
      "dark:border-indigo-700/60 dark:bg-indigo-950/40 dark:text-indigo-300",
    tooltip:
      "宪法智能体已绑定。本任务的执行遵循组织宪法约束,并由 CTR/CSIL/TAOL 自动监督。",
    ariaLabel: "宪法智能体已绑定",
  },
  chat_pin_ui: {
    label: "Chat Pin",
    toneClassName: "border-slate-300/70 bg-slate-50/70 text-slate-700",
    toneDarkClassName:
      "dark:border-slate-700/60 dark:bg-slate-950/40 dark:text-slate-300",
    tooltip: "聊天置顶 UI 已绑定。本任务的会话固定在聊天面板顶部。",
    ariaLabel: "聊天置顶 UI 已绑定",
  },
};

/**
 * Pick which Lucide icon to render on the left of the pill.
 * Defaults to `FlaskConical` so even unknown future labs render a
 * placeholder glyph instead of crashing.
 */
function labIcon(key: string) {
  if (key === "mythos_swarm") return Network;
  return FlaskConical;
}

/**
 * LabBadge — see file header. Returns null when the issue has no
 * `lab_source` value, so the regular user experience stays
 * completely free of chrome.
 */
export function LabBadge({ labSource, className }: LabBadgeProps) {
  if (!labSource) return null;
  const spec = LAB_BADGES[labSource];
  // Unknown lab_source: nothing the server should ever send, but
  // we still degrade gracefully (silently skip rendering instead
  // of throwing — keeps the whole row renderable).
  if (!spec) return null;

  const Icon = labIcon(labSource);

  return (
    <HoverCard>
      <HoverCardTrigger>
        <span
          aria-label={spec.ariaLabel}
          className={cn(
            "inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 text-[10px] font-medium",
            spec.toneClassName,
            spec.toneDarkClassName,
            // 0.3.32: keep the breathing halo for mythos only —
            // other labs are static pills to avoid the user's eye
            // chasing a moving target across the issue list.
            labSource === "mythos_swarm" ? "mythos-boost-pulse" : "",
            className,
          )}
        >
          <Icon className="size-3" aria-hidden />
          {spec.label}
        </span>
      </HoverCardTrigger>
      <HoverCardContent
        side="top"
        sideOffset={4}
        className="max-w-xs text-xs leading-relaxed"
      >
        {spec.tooltip}
      </HoverCardContent>
    </HoverCard>
  );
}
