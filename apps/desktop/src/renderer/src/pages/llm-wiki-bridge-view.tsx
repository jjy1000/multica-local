import { useEffect, useState } from "react";
import { FlaskConical, Loader2 } from "lucide-react";
import { useExperimentalFlag } from "@multica/core/experimental";
import { api } from "@multica/core/api";

// LLMWikiBridgeView (0.3.19+)
//
// Minimal status page for the LLM Wiki bridge. Shows whether the
// desktop LLM Wiki.app is reachable, a quick-start guide, and the
// available /api/experimental/llm-wiki/* verbs.

interface StatusResponse {
  desktop_api: string;
  reachable: boolean;
  vault_dir: string;
  vault_dirs_found: string[];
  search_total_docs: number;
}

export function LLMWikiBridgeView() {
  // The LLM Wiki bridge is a workspace-level status surface (the
  // stdio subprocess is shared, the inventory of endpoints /
  // vault docs is workspace-wide), so it does not bind to a single
  // issue — there is no issue-scoped view to render here.
  const bridgeEnabled = useExperimentalFlag("llm_wiki_bridge", false);
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!bridgeEnabled) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);

    const loadStatus = async (): Promise<void> => {
      try {
        await window.experimentalAPI.invoke(
          "llm_wiki_bridge",
          "ensure-up",
        );
      } catch {
        // The HTTP bridge can still reach a running LLM Wiki desktop API
        // when the optional stdio MCP process is unavailable.
      }

      try {
        const response = await api.rawRequest("/api/experimental/llm-wiki/status");
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }
        const data = (await response.json()) as StatusResponse;
        if (!cancelled) setStatus(data);
      } catch {
        if (!cancelled) setStatus(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    void loadStatus();
    return () => {
      cancelled = true;
    };
  }, [bridgeEnabled]);

  if (!bridgeEnabled) {
    return (
      <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
        <Header />
        <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
          <Intro />
          <div className="rounded-md border border-amber-200 bg-amber-50/40 p-5 dark:border-amber-800 dark:bg-amber-950/30">
            <p className="text-sm text-amber-800 dark:text-amber-200">
              LLM Wiki Bridge 未启用。请在 设置 → Labs 中打开开关。
            </p>
          </div>
        </main>
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        <Intro />
        <StatusCard status={status} loading={loading} />
        <VerbsCard />
        <SkillUsageCard />
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
        <span>LLM Wiki</span>
      </div>
    </header>
  );
}

function Intro() {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        LLM Wiki 本地桥接
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        将 Multica 智能体桥接到本地的 LLM Wiki.app。检索走 API
        (127.0.0.1:19828)，写入走文件系统
        (~/Documents/llm wiki/)。模型无需额外配置。
      </p>
    </section>
  );
}

function StatusCard({ status, loading }: {
  status: StatusResponse | null;
  loading: boolean;
}) {
  if (loading) {
    return (
      <section className="rounded-xl border border-border bg-card p-5">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" />
          正在连接 LLM Wiki…
        </div>
      </section>
    );
  }
  if (!status) {
    return (
      <section className="rounded-xl border border-destructive/30 bg-destructive/5 p-5">
        <p className="text-sm font-medium text-destructive">无法连接</p>
        <p className="mt-1 text-xs text-muted-foreground">
          请确认 /Applications/LLM Wiki.app 正在运行，然后刷新此页面。
        </p>
      </section>
    );
  }
  return (
    <section className="rounded-xl border border-emerald-200 bg-emerald-50/30 p-5 dark:border-emerald-800 dark:bg-emerald-950/20">
      <p className="text-sm font-medium text-foreground">
        LLM Wiki 已连接
      </p>
      <dl className="mt-3 grid grid-cols-2 gap-3 text-xs">
        <div>
          <dt className="text-muted-foreground">API 端口</dt>
          <dd className="font-mono text-foreground">{status.desktop_api}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">可连接</dt>
          <dd className="font-mono text-emerald-700 dark:text-emerald-300">
            {status.reachable ? "✓ 是" : "✗ 否"}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">仓库目录</dt>
          <dd className="font-mono text-foreground">{status.vault_dir}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">已索引文档</dt>
          <dd className="font-mono text-foreground">{status.search_total_docs}</dd>
        </div>
      </dl>
    </section>
  );
}

function VerbsCard() {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-base font-semibold text-foreground">可用操作</h2>
      <p className="mt-1 text-sm text-muted-foreground">
        Agent 通过 <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">multica-llm-wiki</code>{" "}
        技能调用以下端点。你不需要手动调用它们。
      </p>
      <table className="mt-4 w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
            <th className="pb-2 font-medium">动词</th>
            <th className="pb-2 font-medium">端点</th>
            <th className="pb-2 font-medium">说明</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {[
            ["search", "GET /api/experimental/llm-wiki/search?q=...", "向量 + 关键词混合搜索"],
            ["read", "GET /api/experimental/llm-wiki/files/*", "读取单个文件内容"],
            ["graph", "GET /api/experimental/llm-wiki/graph", "知识图谱查询"],
            ["files", "GET /api/experimental/llm-wiki/files", "列出仓库文件"],
            ["status", "GET /api/experimental/llm-wiki/status", "连接和索引状态"],
            ["write", "POST /api/experimental/llm-wiki/write", "写入文件到仓库"],
          ].map(([verb, endpoint, desc]) => (
            <tr key={verb}>
              <td className="py-2 font-mono text-xs text-foreground">{verb}</td>
              <td className="font-mono text-xs text-muted-foreground">{endpoint}</td>
              <td className="text-xs text-muted-foreground">{desc}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function SkillUsageCard() {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-base font-semibold text-foreground">如何开始</h2>
      <p className="mt-1 text-sm text-muted-foreground">
        1. 打开 /Applications/LLM Wiki.app（确保后台运行）<br />
        2. 在任意 workspace 的 Issue 中 @ multica-llm-wiki 技能<br />
        3. Agent 自动调用检索端点查找你的知识库内容<br />
        4. 写入操作将文件落到 LLM Wiki 仓库目录，由你手工 re-vectorise
      </p>
      <pre className="mt-4 overflow-x-auto rounded-md bg-muted px-4 py-3 text-xs leading-relaxed text-foreground">
{`# Agent 视角：通过 multica-llm-wiki 技能搜索知识库
# 技能会在后台调用 /api/experimental/llm-wiki/search`}
      </pre>
    </section>
  );
}
