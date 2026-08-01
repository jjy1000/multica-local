import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  FlaskConical,
  RefreshCw,
  PlayCircle,
  Clock,
  FileText,
  AlertCircle,
  ShieldCheck,
  History,
  ThumbsDown,
  CheckCircle2,
  XCircle,
  Inbox,
} from "lucide-react";
import { api } from "@multica/core/api";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useT } from "@multica/views/i18n";
import { AppLink } from "@multica/views/navigation";
import { useWorkspaceId } from "@multica/core/hooks";

// SelfOptView (0.5.2) — the merged view for the agent_self_optimization
// lab. Replaces the 0.3.20 placeholder (agent-self-optimization-view.tsx)
// and the 0.3.45.1 history-only view (self-opt-history-view.tsx).
//
// Tabs:
//   1. 循环概览 — what the loop is, the trust score contract, the
//      ablation principle, and a manual "立即运行" trigger.
//   2. 运行历史 — run list + report detail (former SelfOptHistoryView).
//   3. 信任评分 — trust leaderboard (agent_trust_profile rows).
//   4. 事件时间线 — corrections / reviews (agent_trust_event rows) plus a
//      "纠正智能体" entry (score -0.5, the correction trigger).
//
// Network contract: every call goes through api.rawRequest (per CLAUDE.md
// "Experimental tab network calls (0.3.30)") — bare fetch() silently fails
// in the packaged desktop app.
//
// Language contract (user-approved): UI copy is Chinese-first with English
// for terms; all LLM-facing text (review prompt) is English.

interface SelfOptRunDTO {
  id: string;
  workspace_id: string;
  status: string;
  trigger_kind: string;
  started_at: string;
  finished_at?: string;
  source_issue_count: number;
  prompt_suggestions: unknown[];
  report_md?: string;
  kb_appendix_path?: string;
  error_message?: string;
  created_issue_id?: string;
}

interface SelfOptRunListResponse {
  runs: SelfOptRunDTO[];
  has_more: boolean;
}

const TERMINAL_SELF_OPT_STATUSES = new Set(["done", "failed", "cancelled", "deferred"]);

interface PromptSuggestion {
  agent_name?: string;
  agent_id?: string;
  suggested_change?: string;
  rationale?: string;
  confidence?: number;
  backing_issue_ids?: string[];
}

interface TrustProfile {
  agent_id: string;
  workspace_id: string;
  score: number;
  review_threshold: number;
  review_requested_count: number;
  review_pass_count: number;
  review_fail_count: number;
  correction_count: number;
}

interface TrustEvent {
  id: string;
  agent_id: string;
  event_type: string;
  score_delta: number;
  score_before?: number;
  score_after?: number;
  task_id?: string;
  issue_id?: string;
  note?: string;
  created_at: string;
}

type TabKey = "overview" | "history" | "trust" | "events" | "suggestions";

export function SelfOptView() {
  useT("experimental");
  const enabled = useExperimentalFlag("agent_self_optimization", false);
  const [tab, setTab] = useState<TabKey>("overview");
  if (!enabled) {
    return <FlagOffPlaceholder />;
  }
  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-6 py-8">
        <Intro />
        <TabBar tab={tab} onTab={setTab} />
        {tab === "overview" && <OverviewTab />}
        {tab === "history" && <HistoryTab />}
        {tab === "trust" && <TrustTab />}
        {tab === "events" && <EventsTab />}
        {tab === "suggestions" && <SuggestionsTab />}
      </main>
    </div>
  );
}

function FlagOffPlaceholder() {
  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-5xl flex-col gap-4 px-6 py-12 text-body text-muted-foreground">
        <p>智能体自优化未启用。请先在「设置 → 试验性功能」中打开「智能体自优化」开关,再返回此处查看循环、历史与信任评分。</p>
      </main>
    </div>
  );
}

function Header() {
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-caption text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">试验性功能</span>
        <span className="text-muted-foreground/60">/</span>
        <span>智能体自优化循环</span>
      </div>
    </header>
  );
}

function Intro() {
  return (
    <section className="flex flex-col gap-2">
      <h1 className="text-display-sm font-semibold tracking-tight text-foreground">
        智能体自优化循环
      </h1>
      <p className="text-body leading-relaxed text-muted-foreground">
        每周一上午 10 点扫描已完结任务(非实验性 agent)与信任评分账本,参考 SkillOpt 的「文本空间优化器」思路:
        把智能体 / 技能 / 团队 / 自动化的指令文本当作可训练状态,基于信任评分账本与已完成任务提出 add/delete/replace 编辑,
        经过验证门控后写回 — 经验被智能体自己学习。信任 ≥ 8 的智能体进入保留机制(不再提议修改);
        信任偏低且有纠正记录的智能体会被优先自动优化。若错过(电脑关机 / 未开 app),下次启动自动补跑;
        历史数据不足时自动排队待数据充足后优化。
      </p>
    </section>
  );
}

const TABS: { key: TabKey; label: string }[] = [
  { key: "overview", label: "循环概览" },
  { key: "suggestions", label: "待确认建议" },
  { key: "history", label: "运行历史" },
  { key: "trust", label: "信任评分" },
  { key: "events", label: "事件时间线" },
];

function TabBar({ tab, onTab }: { tab: TabKey; onTab: (k: TabKey) => void }) {
  return (
    <div className="flex items-center gap-1 border-b border-border pb-2">
      {TABS.map((t) => (
        <button
          key={t.key}
          type="button"
          onClick={() => onTab(t.key)}
          className={`rounded-md px-3 py-1.5 text-caption font-medium transition-colors ${
            tab === t.key
              ? "bg-purple-500/15 text-purple-700 dark:text-purple-300"
              : "text-muted-foreground hover:bg-accent/60"
          }`}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

// ---- Tab 1: overview ----

function OverviewTab() {
  const wsId = useWorkspaceId();
  const [triggerPending, setTriggerPending] = useState(false);
  return (
    <div className="flex flex-col gap-4">
      <section className="rounded-xl border border-border bg-card p-5">
        <h2 className="text-title-sm font-semibold text-foreground">循环如何工作</h2>
        <ol className="mt-3 flex list-inside list-decimal flex-col gap-2 text-body text-foreground/90">
          <li>每周一 10:00 扫描已完结的主工作区任务与信任评分账本</li>
          <li>SkillOpt 式优化:对每个智能体的 instructions 提出 add/delete/replace 编辑(证据驱动)</li>
          <li>验证门控:第二 LLM 评估候选指令集,严格改善才写回 agent.instructions(经验自我学习)</li>
          <li>错过自动补跑:上次成功运行超过 7 天,下次打开 app 1 分钟内自动触发</li>
          <li>数据不足自动排队:已完结任务 &lt; 5 且信任事件 &lt; 3 时标记「数据不足」,24 小时后重试</li>
          <li>删除闭环:智能体被归档/删除时,其信任评分与优化记录一并清除</li>
        </ol>
      </section>

      <section className="rounded-xl border border-border bg-card p-5">
        <h2 className="flex items-center gap-2 text-title-sm font-semibold text-foreground">
          <ShieldCheck className="size-4 text-emerald-600 dark:text-emerald-400" aria-hidden />
          信任评分与自我审核
        </h2>
        <ul className="mt-3 flex flex-col gap-2 text-body text-foreground/90">
          <li>• 每个智能体初始 <strong>5.0</strong> 分,上限 <strong>10.0</strong></li>
          <li>• 你纠正一次产出 → <strong>-0.5</strong> 分(在 issue 详情或本页事件 tab 触发)</li>
          <li>• 分数 &lt; <strong>7.0</strong> 时,子 agent 完成任务后自动自我审核(LLM 复核产出)</li>
          <li>• 审核通过 → <strong>+0.2</strong> 恢复;审核不通过 → <strong>-0.5</strong> 并记录失败</li>
          <li>• 主 agent 依据评分决定是否信任子 agent 的产出:低分产出先审后用</li>
        </ul>
      </section>

      <section className="rounded-xl border border-amber-500/30 bg-amber-500/5 p-5">
        <h2 className="text-title-sm font-semibold text-amber-700 dark:text-amber-400">
          消融原则(Ablation)
        </h2>
        <p className="mt-2 text-body leading-relaxed text-foreground/90">
          借鉴 Boris Cherny 对 Claude Code 的建议:每 6 个月删掉你的 CLAUDE.md / skills / hooks,
          一行行加回并测试每一行的实际影响。本循环的每一次纠正都是一次「删除→加回」的验证:
          建议会在下一轮以可测试的形式给出,而不是一次堆一整套改动。
        </p>
      </section>

      <section className="rounded-xl border border-border bg-card p-5">
        <h2 className="text-title-sm font-semibold text-foreground">手动触发</h2>
        <div className="mt-3 flex items-center gap-3">
          <button
            type="button"
            disabled={triggerPending}
            onClick={async () => {
              if (!wsId) return;
              setTriggerPending(true);
              try {
                const res = await api.rawRequest("/api/experimental/self-opt/runs", {
                  method: "POST",
                  body: JSON.stringify({ workspace_id: wsId }),
                  headers: { "content-type": "application/json" },
                });
                if (!res.ok) throw new Error(`trigger failed: ${res.status}`);
              } finally {
                setTriggerPending(false);
              }
            }}
            className="inline-flex items-center gap-1 rounded-md border border-purple-500/40 bg-purple-500/10 px-3 py-1.5 text-caption font-medium text-purple-700 hover:bg-purple-500/20 disabled:opacity-50 dark:text-purple-300"
          >
            <PlayCircle className="size-3.5" aria-hidden />
            立即运行
          </button>
          <span className="text-caption text-muted-foreground">
            触发后约 1-2 分钟出现在「运行历史」tab
          </span>
        </div>
      </section>
    </div>
  );
}

// ---- Tab 2: history (former SelfOptHistoryView) ----

function HistoryTab() {
  const wsId = useWorkspaceId();
  const [selectedRunID, setSelectedRunID] = useState<string | null>(null);

  const listQuery = useQuery({
    queryKey: ["self-opt-runs", "list", wsId] as const,
    queryFn: async (): Promise<SelfOptRunListResponse> => {
      const res = await api.rawRequest(
        `/api/experimental/self-opt/runs?workspace_id=${encodeURIComponent(wsId ?? "")}`,
        { method: "GET" },
      );
      if (!res.ok) {
        if (res.status === 404) {
          return { runs: [], has_more: false };
        }
        throw new Error(`list self-opt runs failed: ${res.status}`);
      }
      return res.json() as Promise<SelfOptRunListResponse>;
    },
    refetchInterval: (query) => {
      const runs = query.state.data?.runs ?? [];
      const active = runs.some((r) => !TERMINAL_SELF_OPT_STATUSES.has(r.status));
      return active ? 5_000 : 60_000;
    },
    staleTime: 60_000,
  });

  const runs = listQuery.data?.runs ?? [];
  const selected = runs.find((r) => r.id === selectedRunID) ?? runs[0];

  return (
    <div className="flex flex-col gap-4">
      <Toolbar
        loading={listQuery.isLoading}
        onRefresh={() => listQuery.refetch()}
        onTrigger={async () => {
          if (!wsId) return;
          const res = await api.rawRequest("/api/experimental/self-opt/runs", {
            method: "POST",
            body: JSON.stringify({ workspace_id: wsId }),
            headers: { "content-type": "application/json" },
          });
          if (res.ok) {
            await new Promise((r) => setTimeout(r, 1500));
            await listQuery.refetch();
          } else {
            throw new Error(`trigger failed: ${res.status}`);
          }
        }}
      />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[320px_1fr]">
        <RunList
          runs={runs}
          loading={listQuery.isLoading}
          selectedID={selected?.id ?? null}
          onSelect={(id) => setSelectedRunID(id)}
        />
        <RunDetail run={selected ?? null} />
      </div>
    </div>
  );
}

function Toolbar({
  loading,
  onRefresh,
  onTrigger,
}: {
  loading: boolean;
  onRefresh: () => void;
  onTrigger: () => Promise<void>;
}) {
  return (
    <div className="flex items-center justify-between gap-2 rounded-lg border border-border bg-card px-4 py-2 text-caption">
      <div className="flex items-center gap-2 text-muted-foreground">
        <Clock className="size-3.5" aria-hidden />
        <span>每周一次 · 工作日 10:00(错过自动补跑)</span>
      </div>
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onRefresh}
          disabled={loading}
          className="inline-flex items-center gap-1 rounded-md border border-border/60 px-2.5 py-1 text-caption font-medium hover:bg-accent disabled:opacity-50"
        >
          <RefreshCw className={`size-3 ${loading ? "animate-spin" : ""}`} aria-hidden />
          刷新
        </button>
        <button
          type="button"
          onClick={onTrigger}
          className="inline-flex items-center gap-1 rounded-md border border-purple-500/40 bg-purple-500/10 px-2.5 py-1 text-caption font-medium text-purple-700 hover:bg-purple-500/20 dark:text-purple-300"
        >
          <PlayCircle className="size-3" aria-hidden />
          立即运行
        </button>
      </div>
    </div>
  );
}

function RunList({
  runs,
  loading,
  selectedID,
  onSelect,
}: {
  runs: SelfOptRunDTO[];
  loading: boolean;
  selectedID: string | null;
  onSelect: (id: string) => void;
}) {
  if (loading) {
    return (
      <aside className="rounded-lg border border-border bg-card p-4 text-caption text-muted-foreground">
        加载中…
      </aside>
    );
  }
  if (runs.length === 0) {
    return (
      <aside className="rounded-lg border border-dashed border-border bg-card/40 p-4 text-caption text-muted-foreground">
        暂无自优化 run。点击「立即运行」手动触发一次,或等待下个工作日 10:00 自动触发。
      </aside>
    );
  }
  return (
    <aside className="flex flex-col gap-1.5 overflow-y-auto rounded-lg border border-border bg-card p-2">
      {runs.map((r) => (
        <RunListItem
          key={r.id}
          run={r}
          selected={r.id === selectedID}
          onSelect={() => onSelect(r.id)}
        />
      ))}
    </aside>
  );
}

function RunListItem({
  run,
  selected,
  onSelect,
}: {
  run: SelfOptRunDTO;
  selected: boolean;
  onSelect: () => void;
}) {
  const startedLocal = run.started_at ? new Date(run.started_at).toLocaleString() : "";
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`flex w-full flex-col gap-1 rounded-md px-2.5 py-2 text-left text-caption transition-colors ${
        selected ? "bg-purple-500/15 ring-1 ring-purple-500/40" : "hover:bg-accent/60"
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-foreground">{startedLocal || "—"}</span>
        <StatusBadge status={run.status} trigger={run.trigger_kind} />
      </div>
      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <span>源 issue {run.source_issue_count}</span>
        {run.prompt_suggestions.length > 0 && (
          <span>{run.prompt_suggestions.length} 条建议</span>
        )}
      </div>
    </button>
  );
}

function StatusBadge({ status, trigger }: { status: string; trigger: string }) {
  const cls =
    status === "done"
      ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
      : status === "running"
        ? "bg-blue-500/15 text-blue-700 dark:text-blue-300"
        : status === "failed"
          ? "bg-red-500/15 text-red-700 dark:text-red-300"
          : status === "deferred"
            ? "bg-amber-500/15 text-amber-700 dark:text-amber-300"
            : "bg-muted text-muted-foreground";
  const label =
    status === "done"
      ? trigger === "manual"
        ? "手动"
        : "完成"
      : status === "running"
        ? "运行中"
        : status === "failed"
          ? "失败"
          : status === "cancelled"
            ? "已取消"
            : status === "deferred"
              ? "数据不足"
              : "等待";
  return (
    <span className={`inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium ${cls}`}>
      {label}
    </span>
  );
}

function RunDetail({ run }: { run: SelfOptRunDTO | null }) {
  if (!run) {
    return (
      <section className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-body text-muted-foreground">
        选择左侧的 run 查看报告全文与建议清单。
      </section>
    );
  }
  const suggestions = (run.prompt_suggestions as PromptSuggestion[]) ?? [];
  return (
    <section className="flex flex-col gap-4 rounded-lg border border-border bg-card p-5">
      <header className="flex items-center justify-between gap-2">
        <h2 className="text-title-sm font-semibold text-foreground">报告详情</h2>
        <span className="text-[11px] text-muted-foreground">
          {run.started_at} → {run.finished_at ?? "—"}
        </span>
      </header>
      {run.error_message && (
        <div className="flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2 text-caption text-red-700 dark:text-red-300">
          <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          <span>{run.error_message}</span>
        </div>
      )}
      {run.kb_appendix_path && (
        <div className="flex items-start gap-2 rounded-md border border-border/60 bg-muted/40 px-3 py-2 text-caption text-muted-foreground">
          <FileText className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          <span className="truncate">KB 写入: {run.kb_appendix_path}</span>
        </div>
      )}
      {suggestions.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-body font-medium text-foreground">建议清单</h3>
          <ul className="flex flex-col gap-2">
            {suggestions.map((s, i) => (
              <li
                key={i}
                className="rounded-md border border-border/60 bg-background/40 px-3 py-2 text-caption"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="font-medium text-foreground">
                    {s.agent_name ?? "未命名 agent"}
                  </span>
                  {typeof s.confidence === "number" && (
                    <span className="rounded-full bg-purple-500/10 px-1.5 py-0.5 text-[10px] font-medium text-purple-700 dark:text-purple-300">
                      置信度 {Math.round(s.confidence * 100)}%
                    </span>
                  )}
                </div>
                {s.suggested_change && (
                  <p className="mt-1 leading-relaxed text-foreground/90">{s.suggested_change}</p>
                )}
                {s.rationale && (
                  <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">
                    {s.rationale}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
      {run.report_md && (
        <details className="rounded-md border border-border/60 bg-background/40 px-3 py-2">
          <summary className="cursor-pointer text-caption font-medium text-foreground">
            完整 Markdown 报告
          </summary>
          <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap text-[11px] leading-relaxed text-foreground/80">
            {run.report_md}
          </pre>
        </details>
      )}
      {run.created_issue_id && (
        <AppLink
          href={`/issues/${run.created_issue_id}`}
          className="inline-flex w-fit items-center gap-1 text-caption text-purple-600 hover:underline dark:text-purple-400"
        >
          打开对应 issue →
        </AppLink>
      )}
    </section>
  );
}

// ---- Tab 3: trust leaderboard ----

function TrustTab() {
  const wsId = useWorkspaceId();
  const query = useQuery({
    queryKey: ["self-opt-trust", "profiles", wsId] as const,
    queryFn: async (): Promise<TrustProfile[]> => {
      const res = await api.rawRequest(
        `/api/experimental/trust/profiles?workspace_id=${encodeURIComponent(wsId ?? "")}`,
        { method: "GET" },
      );
      if (!res.ok) {
        if (res.status === 404) return [];
        throw new Error(`list trust profiles failed: ${res.status}`);
      }
      const body = (await res.json()) as { profiles: TrustProfile[] };
      return body.profiles ?? [];
    },
    refetchInterval: 60_000,
    staleTime: 60_000,
  });

  const profiles = query.data ?? [];
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h2 className="text-title-sm font-semibold text-foreground">信任评分排行榜</h2>
        <span className="text-[11px] text-muted-foreground">
          初始 5.0 · 纠正 -0.5 · 审核通过 +0.2 · 审核失败 -0.5 · 上限 10.0
        </span>
      </div>
      {profiles.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-body text-muted-foreground">
          暂无信任评分数据。当子 agent 完成任务且你纠正其产出后,这里会出现它的评分。
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          <table className="w-full text-left text-caption">
            <thead className="border-b border-border bg-muted/40 text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">Agent</th>
                <th className="px-3 py-2 font-medium">信任分</th>
                <th className="px-3 py-2 font-medium">纠正</th>
                <th className="px-3 py-2 font-medium">审核请求</th>
                <th className="px-3 py-2 font-medium">通过/失败</th>
              </tr>
            </thead>
            <tbody>
              {profiles.map((p) => (
                <tr key={p.agent_id} className="border-b border-border/50 last:border-0">
                  <td className="max-w-[220px] truncate px-3 py-2 font-medium text-foreground">
                    {p.agent_id.slice(0, 8)}
                  </td>
                  <td className="px-3 py-2">
                    <ScoreBadge score={p.score} threshold={p.review_threshold} />
                  </td>
                  <td className="px-3 py-2 text-foreground/80">{p.correction_count}</td>
                  <td className="px-3 py-2 text-foreground/80">{p.review_requested_count}</td>
                  <td className="px-3 py-2 text-foreground/80">
                    {p.review_pass_count} / {p.review_fail_count}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="text-[11px] text-muted-foreground">
        分数 &lt; 7.0 的 agent 产出在完成任务后会自动触发自我审核(LLM 复核),通过 +0.2 / 失败 -0.5。
      </p>
    </section>
  );
}

function ScoreBadge({ score, threshold }: { score: number; threshold: number }) {
  const low = score < threshold;
  const cls = low
    ? "bg-red-500/15 text-red-700 dark:text-red-300"
    : score >= 8
      ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
      : "bg-amber-500/15 text-amber-700 dark:text-amber-300";
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-semibold ${cls}`}>
      {score.toFixed(1)}
      {low && <span className="ml-1 text-[10px] font-normal">低信任</span>}
    </span>
  );
}

// ---- Tab 4: event timeline + correction entry ----

function EventsTab() {
  const wsId = useWorkspaceId();
  const [agents, setAgents] = useState<{ id: string; name: string }[]>([]);
  const [selAgent, setSelAgent] = useState("");
  const [note, setNote] = useState("");
  const [correcting, setCorrecting] = useState(false);
  const [feedback, setFeedback] = useState<string | null>(null);

  const query = useQuery({
    queryKey: ["self-opt-trust", "events", wsId] as const,
    queryFn: async (): Promise<TrustEvent[]> => {
      const res = await api.rawRequest(
        `/api/experimental/trust/events?workspace_id=${encodeURIComponent(wsId ?? "")}`,
        { method: "GET" },
      );
      if (!res.ok) {
        if (res.status === 404) return [];
        throw new Error(`list trust events failed: ${res.status}`);
      }
      const body = (await res.json()) as { events: TrustEvent[] };
      return body.events ?? [];
    },
    refetchInterval: 30_000,
    staleTime: 30_000,
  });

  const events = query.data ?? [];

  const loadAgents = async () => {
    if (agents.length > 0 || !wsId) return;
    try {
      const res = await api.rawRequest(
        `/api/agents?workspace_id=${encodeURIComponent(wsId)}&with_archived=true`,
        { method: "GET" },
      );
      if (res.ok) {
        const body = (await res.json()) as { agents?: { id: string; name: string }[] };
        setAgents(body.agents ?? []);
      }
    } catch {
      // agent list is best-effort; correction still works via raw id
    }
  };
  void loadAgents();

  const correct = async () => {
    if (!wsId || !selAgent) return;
    setCorrecting(true);
    setFeedback(null);
    try {
      const res = await api.rawRequest(
        `/api/experimental/trust/${encodeURIComponent(selAgent)}/correct`,
        {
          method: "POST",
          body: JSON.stringify({ workspace_id: wsId, note }),
          headers: { "content-type": "application/json" },
        },
      );
      if (!res.ok) throw new Error(`correct failed: ${res.status}`);
      const body = (await res.json()) as { score: number };
      setFeedback(`已纠正,当前信任分 ${body.score.toFixed(1)}(-0.5)`);
      setNote("");
      await query.refetch();
    } catch (e) {
      setFeedback(`纠正失败:${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setCorrecting(false);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <section className="rounded-xl border border-border bg-card p-5">
        <h2 className="flex items-center gap-2 text-title-sm font-semibold text-foreground">
          <ThumbsDown className="size-4 text-red-500 dark:text-red-400" aria-hidden />
          纠正智能体(触发 -0.5 扣分)
        </h2>
        <div className="mt-3 flex flex-col gap-2 sm:flex-row">
          <select
            value={selAgent}
            onChange={(e) => setSelAgent(e.target.value)}
            className="rounded-md border border-border bg-background px-2.5 py-1.5 text-caption text-foreground"
          >
            <option value="">选择智能体…</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
          <input
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="备注:哪里错了(可选,会进入学习报告)"
            className="flex-1 rounded-md border border-border bg-background px-2.5 py-1.5 text-caption text-foreground placeholder:text-muted-foreground"
          />
          <button
            type="button"
            disabled={correcting || !selAgent}
            onClick={correct}
            className="inline-flex items-center gap-1 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-1.5 text-caption font-medium text-red-700 hover:bg-red-500/20 disabled:opacity-50 dark:text-red-300"
          >
            扣分纠正
          </button>
        </div>
        {feedback && <p className="mt-2 text-caption text-foreground/80">{feedback}</p>}
      </section>

      <section className="flex flex-col gap-2">
        <h2 className="flex items-center gap-2 text-title-sm font-semibold text-foreground">
          <History className="size-4 text-muted-foreground" aria-hidden />
          事件时间线
        </h2>
        {events.length === 0 ? (
          <div className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-body text-muted-foreground">
            暂无信任事件。纠正或自我审核发生后,这里会记录每次 -0.5 / +0.2 的变动。
          </div>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {events.map((ev) => (
              <EventRow key={ev.id} ev={ev} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function EventRow({ ev }: { ev: TrustEvent }) {
  const t = ev.event_type;
  const icon =
    t === "correction" ? (
      <ThumbsDown className="size-3.5 text-red-500 dark:text-red-400" aria-hidden />
    ) : t === "review_pass" ? (
      <CheckCircle2 className="size-3.5 text-emerald-600 dark:text-emerald-400" aria-hidden />
    ) : t === "review_fail" ? (
      <XCircle className="size-3.5 text-red-500 dark:text-red-400" aria-hidden />
    ) : (
      <ShieldCheck className="size-3.5 text-muted-foreground" aria-hidden />
    );
  const label =
    t === "correction"
      ? "用户纠正"
      : t === "review_requested"
        ? "自我审核"
        : t === "review_pass"
          ? "审核通过"
          : t === "review_fail"
            ? "审核失败"
            : "审核跳过";
  const delta =
    ev.score_delta > 0 ? `+${ev.score_delta.toFixed(1)}` : ev.score_delta.toFixed(1);
  const when = ev.created_at ? new Date(ev.created_at).toLocaleString() : "";
  return (
    <li className="flex items-center gap-3 rounded-md border border-border/60 bg-card/50 px-3 py-2 text-caption">
      {icon}
      <span className="font-medium text-foreground">{label}</span>
      <span className="text-muted-foreground">agent {ev.agent_id.slice(0, 8)}</span>
      {ev.note && <span className="truncate text-muted-foreground">— {ev.note}</span>}
      <span className="ml-auto shrink-0 font-mono text-[11px] text-foreground/80">
        {delta}
      </span>
      <span className="shrink-0 text-[11px] text-muted-foreground">{when}</span>
    </li>
  );
}

// ---- Tab 5: 待确认建议 (design-review §4: human-confirm tier) ----

interface SelfOptEditDTO {
  id: string;
  agent_id?: string;
  agent_name?: string;
  /** 0.5.3: optimizable subject kind — agent | skill | squad | autopilot */
  target_type?: string;
  target_id?: string;
  edit_type: string;
  before_text: string;
  after_text: string;
  rationale?: string;
  application: string;
  validation_score?: number;
  validation_reason?: string;
  created_at: string;
}

/** 0.5.3: human-readable subject kind label + badge class. */
const SUBJECT_LABELS: Record<string, { label: string; cls: string }> = {
  agent: { label: "智能体", cls: "bg-purple-500/15 text-purple-700 dark:text-purple-300" },
  skill: { label: "技能", cls: "bg-sky-500/15 text-sky-700 dark:text-sky-300" },
  squad: { label: "团队", cls: "bg-orange-500/15 text-orange-700 dark:text-orange-300" },
  autopilot: { label: "自动化", cls: "bg-teal-500/15 text-teal-700 dark:text-teal-300" },
};

function SuggestionsTab() {
  const wsId = useWorkspaceId();
  const [feedback, setFeedback] = useState<string | null>(null);

  const listQuery = useQuery({
    queryKey: ["self-opt-edits", "suggested", wsId] as const,
    queryFn: async (): Promise<SelfOptEditDTO[]> => {
      const res = await api.rawRequest(
        `/api/experimental/self-opt/edits?workspace_id=${encodeURIComponent(wsId ?? "")}`,
        { method: "GET" },
      );
      if (!res.ok) {
        if (res.status === 404) return [];
        throw new Error(`list edits failed: ${res.status}`);
      }
      const body = (await res.json()) as { edits: SelfOptEditDTO[] };
      return body.edits ?? [];
    },
    refetchInterval: 30_000,
    staleTime: 30_000,
  });

  const edits = listQuery.data ?? [];
  const pending = edits.filter((e) => e.application === "suggested");

  const act = async (id: string, verb: "apply" | "reject" | "ignore") => {
    if (!wsId) return;
    try {
      const res = await api.rawRequest(`/api/experimental/self-opt/edits/${id}/${verb}`, {
        method: "POST",
        body: JSON.stringify({ workspace_id: wsId }),
        headers: { "content-type": "application/json" },
      });
      if (!res.ok) throw new Error(`${verb} failed: ${res.status}`);
      setFeedback(`${verb === "apply" ? "已应用" : verb === "reject" ? "已拒绝" : "已忽略"}`);
      await listQuery.refetch();
    } catch (e) {
      setFeedback(`${verb} 失败:${e instanceof Error ? e.message : String(e)}`);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <section className="rounded-xl border border-border bg-card p-5">
        <h2 className="flex items-center gap-2 text-title-sm font-semibold text-foreground">
          <Inbox className="size-4 text-purple-500" aria-hidden />
          待确认建议
          {pending.length > 0 && (
            <span className="rounded-full bg-purple-500/15 px-2 py-0.5 text-caption font-medium text-purple-700 dark:text-purple-300">
              {pending.length} 条
            </span>
          )}
        </h2>
        <p className="mt-1 text-body text-muted-foreground">
          验证分未达自动应用阈值(或为删除/替换类编辑)的改进建议,等待你确认。自动应用仅对
          「新增」且分数 ≥ 90、有纠正背书、信任分 ≥ 8 的编辑生效。
        </p>
        {feedback && <p className="mt-2 text-caption text-foreground/80">{feedback}</p>}
      </section>

      {pending.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-body text-muted-foreground">
          暂无待确认建议。每周运行后,未达自动应用阈值的编辑会出现在这里。
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {pending.map((e) => (
            <EditCard key={e.id} edit={e} onAction={act} />
          ))}
        </div>
      )}
    </div>
  );
}

function EditCard({
  edit,
  onAction,
}: {
  edit: SelfOptEditDTO;
  onAction: (id: string, verb: "apply" | "reject" | "ignore") => void;
}) {
  const typeLabel =
    edit.edit_type === "add"
      ? "新增"
      : edit.edit_type === "delete"
        ? "删除"
        : "替换";
  const typeCls =
    edit.edit_type === "add"
      ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
      : edit.edit_type === "delete"
        ? "bg-red-500/15 text-red-700 dark:text-red-300"
        : "bg-amber-500/15 text-amber-700 dark:text-amber-300";
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border bg-card p-4">
      <div className="flex items-center gap-2 text-caption">
        <span className={`rounded-full px-2 py-0.5 font-medium ${typeCls}`}>{typeLabel}</span>
        <span
          className={`rounded-full px-2 py-0.5 font-medium ${
            SUBJECT_LABELS[edit.target_type ?? "agent"]?.cls ?? "bg-muted text-muted-foreground"
          }`}
        >
          {SUBJECT_LABELS[edit.target_type ?? "agent"]?.label ?? edit.target_type ?? "智能体"}
        </span>
        <span className="font-medium text-foreground">
          {edit.agent_name ?? edit.target_id?.slice(0, 8) ?? "未知对象"}
        </span>
        {typeof edit.validation_score === "number" && (
          <span className="ml-auto font-mono text-[11px] text-foreground/80">
            评分 {edit.validation_score.toFixed(0)}
          </span>
        )}
      </div>

      {edit.edit_type === "add" ? (
        <div className="rounded-md border border-emerald-500/20 bg-emerald-500/5 px-3 py-2 text-caption text-foreground/90">
          <span className="text-emerald-600 dark:text-emerald-400">+</span> {edit.after_text}
        </div>
      ) : edit.edit_type === "delete" ? (
        <div className="rounded-md border border-red-500/20 bg-red-500/5 px-3 py-2 text-caption text-foreground/90">
          <span className="text-red-500">−</span> {edit.before_text}
        </div>
      ) : (
        <div className="flex flex-col gap-1">
          <div className="rounded-md border border-red-500/20 bg-red-500/5 px-3 py-2 text-caption text-foreground/90">
            <span className="text-red-500">−</span> {edit.before_text}
          </div>
          <div className="rounded-md border border-emerald-500/20 bg-emerald-500/5 px-3 py-2 text-caption text-foreground/90">
            <span className="text-emerald-600 dark:text-emerald-400">+</span> {edit.after_text}
          </div>
        </div>
      )}

      {edit.rationale && (
        <p className="text-caption text-muted-foreground">
          <span className="font-medium text-foreground/80">依据:</span> {edit.rationale}
        </p>
      )}
      {edit.validation_reason && (
        <p className="text-caption text-muted-foreground">
          <span className="font-medium text-foreground/80">验证:</span> {edit.validation_reason}
        </p>
      )}

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => onAction(edit.id, "apply")}
          className="inline-flex items-center gap-1 rounded-md border border-emerald-500/40 bg-emerald-500/10 px-2.5 py-1 text-caption font-medium text-emerald-700 hover:bg-emerald-500/20 dark:text-emerald-300"
        >
          应用
        </button>
        <button
          type="button"
          onClick={() => onAction(edit.id, "ignore")}
          className="inline-flex items-center gap-1 rounded-md border border-border/60 px-2.5 py-1 text-caption font-medium text-foreground/80 hover:bg-accent"
        >
          忽略
        </button>
        <button
          type="button"
          onClick={() => onAction(edit.id, "reject")}
          className="inline-flex items-center gap-1 rounded-md border border-red-500/40 bg-red-500/10 px-2.5 py-1 text-caption font-medium text-red-700 hover:bg-red-500/20 dark:text-red-300"
        >
          拒绝
        </button>
      </div>
    </div>
  );
}
