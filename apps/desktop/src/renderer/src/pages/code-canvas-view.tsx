import { FlaskConical } from "lucide-react";
import { useExperimentalFlag } from "@multica/core/experimental";

// CodeCanvasView (0.3.20 placeholder; 0.3.54 onboarding hints)
//
// Internal P9 lab: a stub subprocess that exists to verify the
// manifest → catalog → registry → IPC chain end-to-end. It is not a
// product surface — there is no real binary, no UI, and nothing for
// the user to do here. The view exists only so the sidebar link does
// not 404 and so operators can see "this is a real, registered lab"
// when triaging.
//
// 0.3.54: rendered a 0.3.54 install handler that provisions a
// `code_canvas_worker` leader agent the daemon can dispatch to, so
// the user can pick "Code Canvas" in an issue's LabPicker and have
// the lab auto-assign. The view below mirrors that: the StatusCard
// tells the user "you can pick me as a lab on any issue" so the
// stub-ness of the underlying subprocess doesn't read as a bug.
//
// Keep this view dead-simple. The 0.3.20 stub manager (apps/desktop/
// src/main/experimental/manager-factory.ts) reports status="idle" and
// get-url()=null for code_canvas; the page mirrors that truthfully.

export function CodeCanvasView() {
  const enabled = useExperimentalFlag("code_canvas", false);
  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        <Intro />
        {enabled && <OnboardingHint />}
        <StatusCard enabled={enabled} />
        <PurposeCard />
      </main>
    </div>
  );
}

function OnboardingHint() {
  return (
    <section className="rounded-xl border border-amber-200/60 bg-amber-50/40 p-5 text-sm dark:border-amber-800 dark:bg-amber-950/30">
      <p className="font-medium text-amber-900 dark:text-amber-200">
        怎么开始用 code_canvas
      </p>
      <ol className="mt-2 list-decimal space-y-1 pl-5 text-xs leading-relaxed text-amber-900/90 dark:text-amber-100/90">
        <li>到主 issue 列表点「创建」</li>
        <li>
          在「分配给」一栏切到 <span className="font-mono">code_canvas</span> 实验室 →
          你的 issue 绑定完成,multica 后台会自动派单到
          <span className="font-mono">code_canvas_worker</span>
        </li>
        <li>工作进度在 issue 详情页右栏「正在本 issue 上运行的实验室」可见</li>
        <li>完成后产物(viewable HTML / generated 图)会贴回 issue 评论里</li>
      </ol>
    </section>
  );
}

function Header() {
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">试验性功能</span>
        <span className="text-muted-foreground/60">/</span>
        <span>Code Canvas</span>
      </div>
    </header>
  );
}

function Intro() {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        代码画布（内部实验）
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        内部 P9 试点：用于端到端验证 Labs 平台每一层（manifest → catalog → registry → IPC）
        都能正确联通的 stub 子进程。它不是面向用户的功能 — 也没有可点的操作。
      </p>
    </section>
  );
}

function StatusCard({ enabled }: { enabled: boolean }) {
  return (
    <section
      className={
        enabled
          ? "rounded-xl border border-amber-200 bg-amber-50/40 p-5 dark:border-amber-800 dark:bg-amber-950/30"
          : "rounded-xl border border-border bg-card p-5"
      }
    >
      <p
        className={
          enabled
            ? "text-sm font-medium text-amber-800 dark:text-amber-200"
            : "text-sm font-medium text-foreground"
        }
      >
        {enabled ? "已注册 · 子进程未就绪" : "未启用"}
      </p>
      <p className="mt-1 text-xs text-muted-foreground">
        {enabled
          ? "Manager 报告 status=idle、url=null。代理层会返回 502 — 这是预期行为：code_canvas 没有真实二进制。未来的实验替换 stub 时会出现在这里。"
          : "在 设置 → Labs 打开后，本页仅作为该 flag 已被注册的可视证据 — 不会有可见变化。"}
      </p>
    </section>
  );
}

function PurposeCard() {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-base font-semibold text-foreground">为什么这个 flag 存在</h2>
      <p className="mt-2 text-sm text-muted-foreground">
        新增一个 subprocess 类实验时，需要保证「启动 → 注册 loopback URL → 反向代理 → 智能体可调用」
        整条链路都能工作。code_canvas 是一个刻意保留的空 stub，专门用来在每次平台重构后做一次冒烟测试。
        真实实验会替换掉它。
      </p>
    </section>
  );
}
