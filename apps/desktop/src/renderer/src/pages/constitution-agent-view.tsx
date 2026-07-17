import { FlaskConical } from "lucide-react";
import { useExperimentalFlag } from "@multica/core/experimental";

// ConstitutionAgentView (0.3.20 placeholder)
//
// This flag exposes the 宪法智能体 (Charter Guardian) agent and its
// 3 autopilots (CTR 三周评审 / CSIL 宪章自优化 / TAOL 任务-智能体优化)
// as opt-in plugins. The agent enforces the workspace constitution
// (charter v6, CSIL + CTR + TAOL tracks) and runs autonomous review
// cycles — the flag exists to keep it out of the default agent team
// until the user explicitly opts in.
//
// The flag is a knowledge / automation surface, not a configuration
// panel. There is nothing for the user to click here; the view just
// reports flag state and the surfaced locations.

export function ConstitutionAgentView() {
  const enabled = useExperimentalFlag("constitution_agent", false);
  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        <Intro />
        <StatusCard enabled={enabled} />
        <TracksCard />
        <WhereItShowsUpCard />
        <CrossLinkSection />
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
        <span>宪法智能体</span>
      </div>
    </header>
  );
}

function Intro() {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        宪法智能体（宪章守护）
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        将「宪法智能体」与其 3 条 autopilot（CTR 三周评审 / CSIL 宪章自优化 /
        TAOL 任务-智能体优化）以及配套 skill 作为可选用插件暴露。该智能体负责执行工作区
        宪章（charter v6，CSIL + CTR + TAOL 三轨制）并周期性自动评审。默认关闭 —
        请在明确接受宪章约束后再启用。
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
        {enabled ? "已启用 · 宪章已生效" : "未启用 · 宪章已隐藏"}
      </p>
      <p className="mt-1 text-xs text-muted-foreground">
        {enabled
          ? "智能体 / 3 条 autopilot / skill 已挂载。CSIL 循环会在每日低峰时段发起一次宪章自检。"
          : "智能体 / autopilot / skill 仍保留在 DB 与 resources 中，启用 flag 即恢复，不重新创建。"}
      </p>
    </section>
  );
}

function TracksCard() {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-base font-semibold text-foreground">三轨制</h2>
      <dl className="mt-3 flex flex-col gap-3 text-sm">
        <div>
          <dt className="font-medium text-foreground">CTR — 三周评审</dt>
          <dd className="text-xs text-muted-foreground">每 3 周对工作区宪章做一次全面 review，结果写入 charter diff 通道。</dd>
        </div>
        <div>
          <dt className="font-medium text-foreground">CSIL — 宪章自优化</dt>
          <dd className="text-xs text-muted-foreground">每日低峰时段根据最近一周的 issue / comment 数据微调宪章条目。</dd>
        </div>
        <div>
          <dt className="font-medium text-foreground">TAOL — 任务-智能体优化</dt>
          <dd className="text-xs text-muted-foreground">跨工作区审视任务分配是否与宪章一致，发现错配时向 owner 提建议。</dd>
        </div>
      </dl>
    </section>
  );
}

function WhereItShowsUpCard() {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-base font-semibold text-foreground">开启后会在哪里出现</h2>
      <ul className="mt-3 flex flex-col gap-2 text-sm text-foreground">
        <li>• <strong>智能体</strong>：工作区 → 智能体列表里多出「宪法智能体」一行</li>
        <li>• <strong>Autopilot</strong>：设置 → Autopilot 列表里多出 CTR / CSIL / TAOL 共 3 条</li>
        <li>• <strong>Skill</strong>：智能体详情页的可用技能列表里出现 multica-constitution-agent</li>
      </ul>
    </section>
  );
}

// CrossLinkSection — when this flag is on, mention the linked
// `agent_self_optimization` lab. The two are intentionally split: the
// self-optimization loop proposes skill / agent edits, and the
// constitution agent is the reviewer that catches regressions. This
// card shows that contract so the user understands why both flags
// make sense together.
function CrossLinkSection() {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-base font-semibold text-foreground">与「智能体自优化」的关系</h2>
      <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
        智能体自优化（<code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">agent_self_optimization</code>）让智能体提议 skill / agent 改动；宪法智能体是它的 reviewer —
        每次 SkillOpt-Multica 提出修改，CSIL 都会核对是否违反宪章。两个 flag 同时启用时形成「提议 + 审查」的闭环。
      </p>
    </section>
  );
}
