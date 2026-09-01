import { useEffect, useState, type ReactNode } from "react";
import { FlaskConical, KeyRound, Loader2 } from "lucide-react";
import { useExperimentalFlag } from "@multica/core/experimental";
import {
  api,
  parseWithFallback,
  LLMWikiStatusResponseSchema,
  EMPTY_LLM_WIKI_STATUS_RESPONSE,
  LLMWikiTokenResponseSchema,
  EMPTY_LLM_WIKI_TOKEN_RESPONSE,
  type LLMWikiStatusResponse,
  type LLMWikiTokenResponse,
} from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { IssueBreadcrumb } from "@multica/views/experimental/components";

// LLMWikiBridgeView (0.3.19+)
//
// Minimal status page for the LLM Wiki bridge. Shows whether the
// desktop LLM Wiki.app is reachable, a quick-start guide, and the
// available /api/experimental/llm-wiki/* verbs.
//
// 0.5.92: the status card warns precisely instead of one amber
// "unreachable" blob — client missing (not_installed), installed
// but not launched (not_running), running but key rejected
// (unauthorized) — and a new API-key card lets the user paste the
// token generated in LLM Wiki.app (Settings → API + MCP) straight
// into Multica's own store (POST /api/experimental/llm-wiki/token).

type StatusResponse = LLMWikiStatusResponse;
type TokenResponse = LLMWikiTokenResponse;

export function LLMWikiBridgeView() {
  // The LLM Wiki bridge is a workspace-level status surface (the
  // stdio subprocess is shared, the inventory of endpoints /
  // vault docs is workspace-wide), so it does not bind to a single
  // issue — there is no issue-scoped view to render here.
  const bridgeEnabled = useExperimentalFlag("llm_wiki_bridge", false);
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [loading, setLoading] = useState(true);
  // Bumped by the TokenCard after a save/clear so the status card
  // re-probes (a saved key can turn unauthorized → ok immediately).
  const [reloadKey, setReloadKey] = useState(0);

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
        const raw = (await response.json()) as unknown;
        const data = parseWithFallback<StatusResponse>(
          raw,
          LLMWikiStatusResponseSchema,
          EMPTY_LLM_WIKI_STATUS_RESPONSE,
          { endpoint: "/api/experimental/llm-wiki/status" },
        );
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
  }, [bridgeEnabled, reloadKey]);

  if (!bridgeEnabled) {
    return (
      <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
        <Header />
        <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
          {/* 0.5.81: LLM Wiki is workspace-level (no issue binding),
              so the breadcrumb renders the unbound-hint variant. */}
          <IssueBreadcrumb infoHintWhenUnbound />
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
        {/* 0.5.81: workspace-level surface — show the unbound hint. */}
        <IssueBreadcrumb infoHintWhenUnbound />
        <Intro />
        <StatusCard status={status} loading={loading} />
        <TokenCard onChanged={() => setReloadKey((k) => k + 1)} />
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
  // Health is a free-form object from the LLM Wiki.app /api/v1/health
  // endpoint. We peek at the two fields the bridge guarantees (`ok`) plus
  // a couple of common additive fields (`version`, `uptime`) — anything
  // else the desktop app may surface is intentionally ignored to keep
  // the schema `.loose()` on the server side.
  const healthVersion =
    typeof status.health?.version === "string" ? status.health.version : null;
  const healthUptime =
    typeof status.health?.uptime === "number" ? status.health.uptime : null;
  const healthSummary =
    healthVersion ?? (healthUptime != null ? `uptime ${healthUptime}s` : "—");
  const apiLabel = status.desktop_api ?? "—";

  if (!status.ok) {
    // 0.5.92: three distinct warning states keyed off the server's
    // `failure` code — install guidance, launch guidance, or key
    // guidance. Older servers (no `failure` field) degrade to the
    // not-running card via the fallback.
    const failure =
      status.failure || "not_running";
    if (failure === "not_installed") {
      return (
        <WarningCard
          title="未检测到 LLM Wiki 客户端"
          body={
            <>
              /Applications/LLM Wiki.app 不存在。请先安装 LLM Wiki
              客户端，然后打开它的 <b>设置 → API + MCP</b>，启用本地 API
              （建议同时生成 API 密钥），再回到此页面刷新。
            </>
          }
          hint={status.hint}
          reason={status.reason}
        />
      );
    }
    if (failure === "unauthorized") {
      return (
        <WarningCard
          title="API 鉴权失败"
          body={
            <>
              LLM Wiki 正在运行，但拒绝了桥接请求 —— 需要有效的 API
              密钥。请在 LLM Wiki.app 的 <b>设置 → API + MCP</b>{" "}
              生成密钥，然后在下方「API 密钥」卡片中粘贴并保存。
            </>
          }
          hint={status.hint}
          reason={status.reason}
        />
      );
    }
    return (
      <WarningCard
        title="LLM Wiki 未运行"
        body={
          status.installed === false ? (
            <>未检测到客户端；请安装 /Applications/LLM Wiki.app。</>
          ) : (
            <>
              LLM Wiki 已安装但没有在运行。请启动 /Applications/LLM
              Wiki.app 并保持后台运行，然后刷新此页面。
            </>
          )
        }
        hint={status.hint}
        reason={status.reason}
      />
    );
  }

  return (
    <section className="rounded-xl border border-emerald-200 bg-emerald-50/30 p-5 dark:border-emerald-800 dark:bg-emerald-950/20">
      <p className="text-sm font-medium text-foreground">LLM Wiki 已连接</p>
      <dl className="mt-3 grid grid-cols-2 gap-3 text-xs">
        <div>
          <dt className="text-muted-foreground">API 端口</dt>
          <dd className="font-mono text-foreground">{apiLabel}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">可连接</dt>
          <dd className="font-mono text-emerald-700 dark:text-emerald-300">✓ 是</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">仓库目录</dt>
          <dd className="font-mono text-foreground break-all">
            {status.vault_root || "—"}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">健康状态</dt>
          <dd className="font-mono text-foreground">{healthSummary}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">API 密钥</dt>
          <dd
            className={
              status.token_configured
                ? "font-mono text-emerald-700 dark:text-emerald-300"
                : "font-mono text-amber-700 dark:text-amber-300"
            }
          >
            {status.token_configured ? "已配置" : "未配置（密钥启用后必需）"}
          </dd>
        </div>
      </dl>
    </section>
  );
}

// WarningCard is the shared amber shell for the three 0.5.92
// failure states. `hint` is the server's English remediation line
// (shown small, for power users); `reason` is the raw probe error.
function WarningCard({ title, body, hint, reason }: {
  title: string;
  body: ReactNode;
  hint?: string;
  reason?: string;
}) {
  return (
    <section className="rounded-xl border border-amber-200 bg-amber-50/40 p-5 dark:border-amber-800 dark:bg-amber-950/30">
      <p className="text-sm font-medium text-amber-900 dark:text-amber-100">{title}</p>
      <p className="mt-1 text-xs leading-relaxed text-amber-800 dark:text-amber-200">{body}</p>
      {hint ? (
        <p className="mt-2 rounded-md border border-dashed border-amber-300/60 px-2 py-1 font-mono text-[10px] text-amber-700 dark:border-amber-700/60 dark:text-amber-300">
          {hint}
        </p>
      ) : null}
      {reason && reason !== hint ? (
        <p className="mt-1 text-[10px] text-muted-foreground">{reason}</p>
      ) : null}
    </section>
  );
}

// TokenCard (0.5.92) — paste the API key generated in LLM Wiki.app
// (Settings → API + MCP) into Multica's own store. The bridge reads
// this store first, so a paste here always wins; the endpoint only
// ever reports configured/source, never the token itself.
function TokenCard({ onChanged }: { onChanged: () => void }) {
  const [tokenState, setTokenState] = useState<TokenResponse | null>(null);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  const loadTokenState = async (): Promise<void> => {
    try {
      const response = await api.rawRequest("/api/experimental/llm-wiki/token");
      const raw = (await response.json()) as unknown;
      setTokenState(
        parseWithFallback<TokenResponse>(
          raw,
          LLMWikiTokenResponseSchema,
          EMPTY_LLM_WIKI_TOKEN_RESPONSE,
          { endpoint: "/api/experimental/llm-wiki/token" },
        ),
      );
    } catch {
      setTokenState(null);
    }
  };

  useEffect(() => {
    void loadTokenState();
  }, []);

  const save = async (): Promise<void> => {
    if (!value.trim() || busy) return;
    setBusy(true);
    setMessage("");
    try {
      const response = await api.rawRequest("/api/experimental/llm-wiki/token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token: value.trim() }),
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      setValue("");
      setMessage("已保存。桥接将优先使用此密钥。");
      onChanged();
    } catch {
      setMessage("保存失败，请重试。");
    } finally {
      setBusy(false);
      void loadTokenState();
    }
  };

  const clear = async (): Promise<void> => {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      const response = await api.rawRequest("/api/experimental/llm-wiki/token", {
        method: "DELETE",
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      setMessage("已清除。将回退到 LLM Wiki.app 自身的密钥。");
      onChanged();
    } catch {
      setMessage("清除失败，请重试。");
    } finally {
      setBusy(false);
      void loadTokenState();
    }
  };

  const sourceLabels: Record<string, string> = {
    user: "已粘贴（Multica 存储，优先级最高）",
    env: "环境变量 LLM_WIKI_API_TOKEN",
    app: "自动读取 LLM Wiki.app 的本地状态",
    legacy: "旧版 auth.json 布局",
    none: "未配置",
  };
  const source = tokenState?.source ?? "none";

  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="flex items-center gap-2 text-base font-semibold text-foreground">
        <KeyRound className="size-4" aria-hidden />
        API 密钥
      </h2>
      <p className="mt-1 text-sm text-muted-foreground">
        在 LLM Wiki.app 的 <b>设置 → API + MCP</b> 中生成密钥后粘贴到此处。
        当前状态：
        <span
          className={
            tokenState?.configured
              ? "ml-1 font-medium text-emerald-700 dark:text-emerald-300"
              : "ml-1 font-medium text-amber-700 dark:text-amber-300"
          }
        >
          {sourceLabels[source] ?? source}
        </span>
      </p>
      <div className="mt-3 flex items-center gap-2">
        <Input
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="粘贴 API 密钥（llm wiki 客户端生成）"
          className="max-w-sm font-mono text-xs"
          disabled={busy}
        />
        <Button size="sm" onClick={() => void save()} disabled={busy || !value.trim()}>
          保存
        </Button>
        <Button
          size="sm"
          variant="outline"
          onClick={() => void clear()}
          disabled={busy || source !== "user"}
        >
          清除
        </Button>
      </div>
      {message ? <p className="mt-2 text-xs text-muted-foreground">{message}</p> : null}
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
            ["search", "POST /api/experimental/llm-wiki/search", "向量 + 关键词混合搜索"],
            ["read", "GET /api/experimental/llm-wiki/read?path=...", "读取单个文件内容"],
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
