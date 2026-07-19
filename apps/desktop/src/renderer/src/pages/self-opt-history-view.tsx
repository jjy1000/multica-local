import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FlaskConical, RefreshCw, PlayCircle, Clock, FileText, AlertCircle } from "lucide-react";
import { api } from "@multica/core/api";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useT } from "@multica/views/i18n";
import { AppLink } from "@multica/views/navigation";
import { useWorkspaceId } from "@multica/core/hooks";

// SelfOptHistoryView (0.3.45.1) — run history for the
// agent_self_optimization lab. Reads from
// GET /api/experimental/self-opt/runs (the new server endpoints
// wired in 0.3.45.1) and renders:
//
//  1. Header: page title + last-refreshed + "立即运行" button
//  2. Run list: rows ordered newest-first, each with status / trigger /
//     time / source issue count / confidence headline
//  3. Run detail panel (right side / bottom on mobile): the selected
//     run's report markdown rendered as preformatted text + a
//     prompt_suggestions bullet list with confidence badges
//
// 0.3.45.1 hard contract:
//
//   - Uses api.rawRequest (per CLAUDE.md "Experimental tab network
//     calls (0.3.30)") so the desktop loopback origin + bearer
//     headers work without rewriting the baseUrl.
//   - Flag-gated server-side (returns 404 when the flag is OFF); the
//     UI shows a "flag disabled" placeholder when the request 404s.
//   - Manual trigger fires POST /api/experimental/self-opt/runs; the
//     request returns 202 with a run_id and we re-list after a short
//     delay so the new row appears.
//   - No LLM call in this view — the report is markdown text only.

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

// Terminal run statuses — anything else (running, or the default
// "等待" / queued state) is treated as in-flight by the list poll.
// Mirrors the StatusBadge branch set below.
const TERMINAL_SELF_OPT_STATUSES = new Set(["done", "failed", "cancelled"]);

interface PromptSuggestion {
  agent_name?: string;
  agent_id?: string;
  suggested_change?: string;
  rationale?: string;
  confidence?: number;
  backing_issue_ids?: string[];
}

export function SelfOptHistoryView() {
  // 0.3.45.2 bug fix (P1#8): the only gate preventing the flag-off
  // view from rendering was the server returning 404 on the runs
  // endpoint, which we silently swallowed into an empty list. Users
  // who landed here via the Labs sidebar (which only renders when
  // the flag is on) would see the chrome but no data; users who
  // typed the URL with the flag off would also see the chrome.
  // Add a real flag gate so the flag-off state shows a single
  // "请先在设置 → 试验性功能中启用" placeholder.
  useT("experimental");
  const enabled = useExperimentalFlag("agent_self_optimization", false);
  if (!enabled) {
    return <FlagOffPlaceholder />;
  }
  return <SelfOptHistoryViewBody />;
}

function FlagOffPlaceholder() {
  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-5xl flex-col gap-4 px-6 py-12 text-sm text-muted-foreground">
        <p>智能体自优化未启用。请先在「设置 → 试验性功能」中打开「智能体自优化」开关,再返回此处查看历史。</p>
      </main>
    </div>
  );
}

function SelfOptHistoryViewBody() {
  // useT reserved for future i18n coverage; the MVP renders Chinese
  // strings inline (matches the other Labs views). Linter requires
  // us to drop the variable rather than hold it dead.
  useT("experimental");
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
    // 0.3.45.8 (P0#3.7 sibling): self-opt runs have no WS event at all
    // (unlike agent tasks / autopilot), so this poll is the ONLY freshness
    // signal. The flat 60s interval left a run's status badge stale for up
    // to a minute as it moved running → done. Poll every 5s while any run is
    // still in flight (anything not done/failed/cancelled), and fall back to
    // a 60s idle beat so externally-triggered runs still surface.
    refetchInterval: (query) => {
      const runs = query.state.data?.runs ?? [];
      const active = runs.some(
        (r) => !TERMINAL_SELF_OPT_STATUSES.has(r.status),
      );
      return active ? 5_000 : 60_000;
    },
    staleTime: 30_000,
  });

  const trigger = useMutationLite(() => listQuery.refetch());

  const runs = listQuery.data?.runs ?? [];
  const selected = runs.find((r) => r.id === selectedRunID) ?? runs[0];

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-6 py-8">
        <Intro />
        <Toolbar
          loading={listQuery.isLoading}
          onRefresh={() => listQuery.refetch()}
          onTrigger={async () => {
            if (!wsId) return;
            const res = await api.rawRequest(
              "/api/experimental/self-opt/runs",
              {
                method: "POST",
                body: JSON.stringify({ workspace_id: wsId }),
                headers: { "content-type": "application/json" },
              },
            );
            if (res.ok) {
              await new Promise((r) => setTimeout(r, 1500));
              await listQuery.refetch();
            } else {
              throw new Error(`trigger failed: ${res.status}`);
            }
          }}
          triggerPending={trigger.pending}
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
      </main>
    </div>
  );
}

// Minimal mutation-style hook that wraps a callback with pending
// state. We don't use useMutation from react-query here because the
// trigger side-effect is a refresh + a brief wait, not a typical
// mutation pattern.
function useMutationLite(_fn: () => Promise<unknown>) {
  const [pending, setPending] = useState(false);
  return {
    pending,
    mutate: async (cb: () => Promise<unknown>) => {
      setPending(true);
      try {
        await cb();
      } finally {
        setPending(false);
      }
    },
  };
}

function Header() {
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">试验性功能</span>
        <span className="text-muted-foreground/60">/</span>
        <span>智能体自优化历史</span>
      </div>
    </header>
  );
}

function Intro() {
  return (
    <section className="flex flex-col gap-2">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        智能体自优化历史
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        定时每四天一次扫描已完结任务(非实验性 agent),派生 prompt 改进建议。每条 run
        自动创建一条带实验室图标的问题,在主面板按「实验性任务」过滤后展示;报告全文见右侧面板。
      </p>
    </section>
  );
}

interface ToolbarProps {
  loading: boolean;
  onRefresh: () => void;
  onTrigger: () => Promise<void>;
  triggerPending: boolean;
}

function Toolbar({ loading, onRefresh, onTrigger, triggerPending }: ToolbarProps) {
  return (
    <div className="flex items-center justify-between gap-2 rounded-lg border border-border bg-card px-4 py-2 text-xs">
      <div className="flex items-center gap-2 text-muted-foreground">
        <Clock className="size-3.5" aria-hidden />
        <span>每 4 天一次 · 工作日 10:00(本地时区)</span>
      </div>
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onRefresh}
          disabled={loading}
          className="inline-flex items-center gap-1 rounded-md border border-border/60 px-2.5 py-1 text-xs font-medium hover:bg-accent disabled:opacity-50"
        >
          <RefreshCw className={`size-3 ${loading ? "animate-spin" : ""}`} aria-hidden />
          刷新
        </button>
        <button
          type="button"
          onClick={onTrigger}
          disabled={triggerPending}
          className="inline-flex items-center gap-1 rounded-md border border-purple-500/40 bg-purple-500/10 px-2.5 py-1 text-xs font-medium text-purple-700 hover:bg-purple-500/20 disabled:opacity-50 dark:text-purple-300"
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
      <aside className="rounded-lg border border-border bg-card p-4 text-xs text-muted-foreground">
        加载中…
      </aside>
    );
  }
  if (runs.length === 0) {
    return (
      <aside className="rounded-lg border border-dashed border-border bg-card/40 p-4 text-xs text-muted-foreground">
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
      className={`flex w-full flex-col gap-1 rounded-md px-2.5 py-2 text-left text-xs transition-colors ${
        selected
          ? "bg-purple-500/15 ring-1 ring-purple-500/40"
          : "hover:bg-accent/60"
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-foreground">
          {startedLocal || "—"}
        </span>
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
      <section className="rounded-lg border border-dashed border-border bg-card/40 p-6 text-sm text-muted-foreground">
        选择左侧的 run 查看报告全文与建议清单。
      </section>
    );
  }
  const suggestions = (run.prompt_suggestions as PromptSuggestion[]) ?? [];
  return (
    <section className="flex flex-col gap-4 rounded-lg border border-border bg-card p-5">
      <header className="flex items-center justify-between gap-2">
        <h2 className="text-base font-semibold text-foreground">报告详情</h2>
        <span className="text-[11px] text-muted-foreground">
          {run.started_at} → {run.finished_at ?? "—"}
        </span>
      </header>
      {run.error_message && (
        <div className="flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2 text-xs text-red-700 dark:text-red-300">
          <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          <span>{run.error_message}</span>
        </div>
      )}
      {run.kb_appendix_path && (
        <div className="flex items-start gap-2 rounded-md border border-border/60 bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          <FileText className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          <span className="truncate">KB 写入: {run.kb_appendix_path}</span>
        </div>
      )}
      {suggestions.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-sm font-medium text-foreground">建议清单</h3>
          <ul className="flex flex-col gap-2">
            {suggestions.map((s, i) => (
              <li
                key={i}
                className="rounded-md border border-border/60 bg-background/40 px-3 py-2 text-xs"
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
          <summary className="cursor-pointer text-xs font-medium text-foreground">
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
          className="inline-flex w-fit items-center gap-1 text-xs text-purple-600 hover:underline dark:text-purple-400"
        >
          打开对应 issue →
        </AppLink>
      )}
    </section>
  );
}