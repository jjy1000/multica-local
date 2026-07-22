import { FlaskConical } from "lucide-react";
import { useExperimentalFlag } from "@multica/core/experimental";

// AgentSelfOptimizationView (0.3.20 placeholder)
//
// This flag exposes the 智能体优化专家 agent, its 2 autopilots
// (SkillOpt-Multica daily self-evolution loop + per-3-workday bulk
// optimization), and the skillopt-multica Skill as opt-in plugins.
//
// The flag is a knowledge / automation surface — NOT a user-facing
// configuration panel. Toggling it on reveals the hidden agent /
// autopilots / skill in their normal listings; nothing here needs to
// be clicked. The view simply confirms flag state and tells the user
// where the surfaced items will appear.

export function AgentSelfOptimizationView() {
  const enabled = useExperimentalFlag("agent_self_optimization", false);
  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        <Intro />
        <StatusCard enabled={enabled} />
        <WhereItShowsUpCard />
        <CrossLinkSection />
        <WarningCard />
      </main>
    </div>
  );
}

function Header() {
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">试验性功能</span>
        <span className="text-muted-foreground/60">/</span>
        <span>智能体自优化</span>
      </div>
    </header>
  );
}

function Intro() {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        智能体自优化循环
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        将「智能体优化专家」智能体 + 2 条 autopilot（SkillOpt-Multica 每日自进化 +
        每 3 工作日批量优化）+ skillopt-multica 技能作为可选用插件暴露。开启后这些
        智能体会自动修改工作区内的 agent / skill / autopilot — 请在明确接受的前提下启用。
      </p>
    </section>
  );
}

function StatusCard({ enabled }: { enabled: boolean }) {
  return (
    <section
      className={
        enabled
          ? "rounded-xl border border-emerald-200 bg-emerald-50/30 p-5 dark:border-emerald-800 dark:bg-emerald-950/20"
          : "rounded-xl border border-border bg-card p-5"
      }
    >
      <p
        className={
          enabled
            ? "text-sm font-medium text-emerald-800 dark:text-emerald-200"
            : "text-sm font-medium text-foreground"
        }
      >
        {enabled ? "已启用 · 插件已挂载" : "未启用 · 插件已隐藏"}
      </p>
      <p className="mt-1 text-xs text-muted-foreground">
        {enabled
          ? "智能体 / autopilot / skill 的可见性表行已激活。下次进入工作区时它们会出现在常规列表里。"
          : "智能体 / autopilot / skill 的可见性表行已停用。常规列表里看不到它们，但数据 / 资源并未删除 — 重新开启即恢复。"}
      </p>
    </section>
  );
}

function WhereItShowsUpCard() {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-base font-semibold text-foreground">开启后会在哪里出现</h2>
      <ul className="mt-3 flex flex-col gap-2 text-sm text-foreground">
        <li>• <strong>智能体</strong>：工作区 → 智能体列表里多出「智能体优化专家」一行（保留在 squad 内供 leader brief 引用）</li>
        <li>• <strong>Autopilot</strong>：设置 → Autopilot 列表里多出 2 条（每 3 工作日批量优化 + SkillOpt-Multica 每日循环）</li>
        <li>• <strong>Skill</strong>：智能体详情页的可用技能列表里出现 skillopt-multica</li>
      </ul>
    </section>
  );
}

function WarningCard() {
  return (
    <section className="rounded-xl border border-destructive/30 bg-destructive/5 p-6">
      <h2 className="text-base font-semibold text-destructive">⚠ 自动修改范围</h2>
      <p className="mt-2 text-sm text-foreground">
        启用后，SkillOpt-Multica 会在每天的低峰时段对工作区内的所有 agent / skill / autopilot
        做一次自评估；批量优化 autopilot 每 3 个工作日触发一次跨工作区优化。所有改动会在 autopilot
        详情页留有 diff 历史，但不会要求人工确认。如果你希望保留完全的人工控制权，请保持该 flag 关闭。
      </p>
    </section>
  );
}

// CrossLinkSection was retired in 0.3.57 alongside the
// constitution_agent lab (migration 165). The reverse direction
// previously lived in constitution-agent-view.tsx.
function CrossLinkSection() {
  return null;
}
