// ExperimentalArtifactView (0.3.19+)
//
// Renders a runtime session's artifacts (PNG / SVG / HTML / JSON /
// CSV / MD / TXT / LOG) inline. Used by the Claude Science view
// when the `claude_science_runtime` Labs flag is enabled; hidden
// otherwise so the renderer is a no-op when the flag is off.
//
// Hard rules:
//
//   0. 0.3.22 lab consolidation: gated on `claude_science_lab`. The
//      old 0.3.20 `claude_science_runtime` flag has been folded into
//      the lab flag — turning the lab on turns the sandbox on. The
//      runtime HTTP route prefix is unchanged for wire-compat.
//   1. The component returns null when the flag is off; legacy
//      Claude Science behaviour is unchanged.
//   2. Artifact bytes are fetched only when the user opens an
//      artifact — no eager GETs. This keeps the panel cheap when no
//      session has been run yet.
//   3. HTML artifacts are sandboxed via iframe sandbox= so a
//      malicious snippet cannot exfiltrate the desktop session.

import { useEffect, useMemo, useState } from "react";
import { ChevronRight, FlaskConical, Loader2 } from "lucide-react";

import { useExperimentalFlag } from "@multica/core/experimental";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";

interface RuntimeArtifactStub {
  id: string;
  session_id: string;
  name: string;
  kind: "png" | "svg" | "html" | "json" | "csv" | "md" | "txt" | "log" | "interactive-chart";
  bytes: number;
  sha256: string;
  url: string;
}

interface RuntimeSession {
  id: string;
  workspace_id: string;
  agent_id: string;
  issue_id: string | null;
  language: string;
  status: "queued" | "running" | "completed" | "failed" | "expired" | "timeout";
  exit_code: number | null;
  stdout: string | null;
  stderr: string | null;
  duration_ms: number | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

interface SessionsResponse {
  sessions: RuntimeSession[];
  total: number;
}

// 0.3.45.8 (P0#3.7 sibling): statuses where a runtime session is still in
// flight and its status badge (SessionRow, {session.status}) can still
// flip. This custom `["claude-science-runtime", "sessions", …]` query key
// is NOT covered by use-realtime-sync's WS invalidation, so without a
// poll the workspace-wide session list only refreshed on window refocus
// after the 30s staleTime — the same stale-status failure mode fixed for
// the per-issue lists in claude-lab-view.tsx. Poll 5s while any session is
// live, otherwise fall back to a 30s idle beat (never `false`: no WS
// signal exists to surface externally-created sessions).
const LIVE_RUNTIME_SESSION_STATUSES = new Set<RuntimeSession["status"]>([
  "queued",
  "running",
]);

export function ExperimentalArtifactView({ workspaceId }: { workspaceId: string }) {
  const runtimeEnabled = useExperimentalFlag("claude_science_lab", false);
  const qc = useQueryClient();

  const sessions = useQuery({
    queryKey: ["claude-science-runtime", "sessions", workspaceId],
    queryFn: async () => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-runtime/sessions?workspace_id=${encodeURIComponent(workspaceId)}`,
      );
      if (!r.ok) throw new Error(`sessions ${r.status}`);
      return (await r.json()) as SessionsResponse;
    },
    enabled: runtimeEnabled && workspaceId !== "",
    staleTime: 30_000,
    refetchInterval: (query) =>
      (query.state.data?.sessions ?? []).some((s) =>
        LIVE_RUNTIME_SESSION_STATUSES.has(s.status),
      )
        ? 5_000
        : 30_000,
  });

  if (!runtimeEnabled) return null;

  return (
    <section
      aria-label="实验运行时产物"
      className="rounded-2xl border border-border bg-card p-5 shadow-sm"
    >
      <header className="mb-4 flex items-center gap-2">
        <FlaskConical className="size-4 text-primary" />
        <h2 className="text-base font-medium">实验产物</h2>
        <span className="ml-auto text-xs text-muted-foreground">
          {sessions.data ? `${sessions.data.total} sessions` : "loading…"}
        </span>
      </header>
      {sessions.isLoading && (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> 加载 sessions…
        </div>
      )}
      {sessions.isError && (
        <div className="text-sm text-destructive">无法加载实验会话</div>
      )}
      {sessions.data && sessions.data.sessions.length === 0 && (
        <p className="text-sm text-muted-foreground">
          尚无实验会话。让 claude-science 智能体通过
          <code className="mx-1 rounded bg-muted px-1 py-0.5 text-xs">
            multica-claude-science-runtime
          </code>
          技能跑一段代码,产物会出现在这里。
        </p>
      )}
      {sessions.data && sessions.data.sessions.length > 0 && (
        <ul className="flex flex-col gap-3">
          {sessions.data.sessions.slice(0, 8).map((s) => (
            <SessionRow key={s.id} session={s} onInvalidate={() => qc.invalidateQueries({ queryKey: ["claude-science-runtime", "sessions", workspaceId] })} />
          ))}
        </ul>
      )}
    </section>
  );
}

function SessionRow({ session, onInvalidate }: { session: RuntimeSession; onInvalidate: () => void }) {
  const [open, setOpen] = useState(false);
  const artifacts = useQuery({
    queryKey: ["claude-science-runtime", "artifacts", session.id],
    queryFn: async () => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-runtime/sessions/${session.id}/artifacts`,
      );
      if (!r.ok) throw new Error(`artifacts ${r.status}`);
      const data = (await r.json()) as { artifacts: RuntimeArtifactStub[]; total: number };
      return data.artifacts;
    },
    enabled: open,
    staleTime: 60_000,
  });

  const del = useMutation({
    mutationFn: async () => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-runtime/sessions/${session.id}`,
        { method: "DELETE" },
      );
      if (!r.ok && r.status !== 204) throw new Error(`delete ${r.status}`);
    },
    onSettled: onInvalidate,
  });

  const statusColor = useMemo(() => {
    if (session.status === "completed") return "text-emerald-600";
    if (session.status === "failed" || session.status === "timeout") return "text-destructive";
    return "text-muted-foreground";
  }, [session.status]);

  return (
    <li className="rounded-xl border border-border bg-background/40 p-3">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 text-left"
      >
        <ChevronRight className={`size-4 transition-transform ${open ? "rotate-90" : ""}`} />
        <span className="font-mono text-xs">{session.id.slice(0, 8)}</span>
        <span className={`text-xs ${statusColor}`}>{session.status}</span>
        {session.exit_code !== null && (
          <span className="text-xs text-muted-foreground">exit {session.exit_code}</span>
        )}
        {session.duration_ms !== null && (
          <span className="text-xs text-muted-foreground">{session.duration_ms} ms</span>
        )}
        <span className="ml-auto text-xs text-muted-foreground">
          {new Date(session.created_at).toLocaleString()}
        </span>
      </button>
      {open && (
        <div className="mt-3 flex flex-col gap-3">
          {artifacts.isLoading && (
            <div className="text-xs text-muted-foreground">加载产物…</div>
          )}
          {artifacts.data && artifacts.data.length === 0 && (
            <div className="text-xs text-muted-foreground">无产物</div>
          )}
          {artifacts.data && artifacts.data.length > 0 && (
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              {artifacts.data.map((a) => (
                <ArtifactTile key={a.id} artifact={a} />
              ))}
            </div>
          )}
          {session.stdout && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground">stdout</summary>
              <pre className="mt-1 max-h-48 overflow-auto rounded bg-muted/40 p-2 font-mono">{session.stdout}</pre>
            </details>
          )}
          {session.stderr && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground">stderr</summary>
              <pre className="mt-1 max-h-48 overflow-auto rounded bg-muted/40 p-2 font-mono text-destructive">{session.stderr}</pre>
            </details>
          )}
          <button
            type="button"
            onClick={() => del.mutate()}
            disabled={del.isPending}
            className="self-end rounded-md border border-border bg-background/40 px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50"
          >
            {del.isPending ? "删除中…" : "删除 session"}
          </button>
        </div>
      )}
    </li>
  );
}

function ArtifactTile({ artifact }: { artifact: RuntimeArtifactStub }) {
  // NOTE: `href` is consumed by native resource loads (<img src>,
  // <iframe src>, <a href download>) which cannot carry the
  // Authorization: Bearer header the desktop session needs. These
  // still resolve against the renderer origin and will 401/404 on a
  // token-auth desktop build — tracked as a follow-up (needs a blob
  // objectURL fetched via api.rawRequest or a signed artifact URL).
  // The inline readers below (SvgInline / TextFetch /
  // InteractiveChartCard) already fetch through api.rawRequest and
  // work correctly.
  const href = `/api/experimental/claude-science-runtime/artifacts/${artifact.id}`;

  if (artifact.kind === "png") {
    return (
      <figure className="overflow-hidden rounded-md border border-border bg-background/40">
        <img src={href} alt={artifact.name} className="block max-h-72 w-full object-contain" />
        <figcaption className="flex items-center justify-between px-2 py-1 text-xs text-muted-foreground">
          <span>{artifact.name}</span>
          <span>{artifact.bytes} B</span>
        </figcaption>
      </figure>
    );
  }
  if (artifact.kind === "svg") {
    return (
      <figure className="overflow-hidden rounded-md border border-border bg-background/40">
        <SvgInline url={href} />
        <figcaption className="flex items-center justify-between px-2 py-1 text-xs text-muted-foreground">
          <span>{artifact.name}</span>
          <span>{artifact.bytes} B</span>
        </figcaption>
      </figure>
    );
  }
  if (artifact.kind === "html") {
    return (
      <figure className="overflow-hidden rounded-md border border-border bg-background/40">
        <iframe
          title={artifact.name}
          src={href}
          sandbox=""
          className="block h-64 w-full bg-white"
        />
        <figcaption className="flex items-center justify-between px-2 py-1 text-xs text-muted-foreground">
          <span>{artifact.name}</span>
          <span>{artifact.bytes} B</span>
        </figcaption>
      </figure>
    );
  }
  if (artifact.kind === "interactive-chart") {
    return <InteractiveChartCard artifact={artifact} artifactId={artifact.id} />;
  }
  if (artifact.kind === "json" || artifact.kind === "csv" || artifact.kind === "md" || artifact.kind === "txt" || artifact.kind === "log") {
    return (
      <figure className="rounded-md border border-border bg-background/40 p-2">
        <div className="mb-1 flex items-center justify-between text-xs text-muted-foreground">
          <span>{artifact.name}</span>
          <a href={href} className="underline">
            下载
          </a>
        </div>
        <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all font-mono text-xs">
          <TextFetch url={href} />
        </pre>
      </figure>
    );
  }
  return (
    <a href={href} className="rounded-md border border-border bg-background/40 p-2 text-xs underline">
      {artifact.name} ({artifact.bytes} B)
    </a>
  );
}

function SvgInline({ url }: { url: string }) {
  const [markup, setMarkup] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const r = await api.rawRequest(url);
      if (!r.ok) return;
      const text = await r.text();
      if (!cancelled) setMarkup(text);
    })();
    return () => {
      cancelled = true;
    };
  }, [url]);
  if (!markup) {
    return <div className="flex h-32 items-center justify-center text-xs text-muted-foreground">loading svg…</div>;
  }
  return <div className="max-h-72 overflow-auto" dangerouslySetInnerHTML={{ __html: markup }} />;
}

// InteractiveChartCard — 0.3.24+.
//
// Renders an `interactive-chart` artifact as a Recharts visualisation
// driven by a `{ schema, data }` envelope embedded in the artifact
// payload. The shape is a small superset of the pythia / pglite
// chart envelope so the same agent can produce both forecast
// predictions and on-the-fly data tables.
//
// schema (JSON):
//   { "type": "line" | "bar" | "scatter" | "heatmap",
//     "x": { "field": "ts",      "label": "时间" },
//     "y": { "field": "value",    "label": "概率" },
//     "color": { "field": "scenario", "label": "场景" }  // optional, scatter / heatmap
//     "bins": 12  // heatmap only
//   }
//
// data: any[] of records that contain the schema's `field` keys.
//
// Falls back to a JSON dump if the payload is malformed or the
// chart type is unknown — see ExperimentalArtifactView's "no
// renderer" pattern in OpenScience.
function InteractiveChartCard({
  artifact,
  artifactId,
}: {
  artifact: { id: string; name: string; bytes: number };
  artifactId: string;
}) {
  const [payload, setPayload] = useState<{ schema?: ChartSchema; data?: unknown[] } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const r = await api.rawRequest(`/api/experimental/claude-science-runtime/artifacts/${artifactId}`);
        if (!r.ok) {
          if (!cancelled) setError(`HTTP ${r.status}`);
          return;
        }
        const text = await r.text();
        const parsed = JSON.parse(text) as { schema?: ChartSchema; data?: unknown[] };
        if (!cancelled) setPayload(parsed);
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : "无法读取图表");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [artifactId]);

  return (
    <figure className="rounded-md border border-border bg-background/40 p-3">
      <div className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
        <span className="font-medium text-foreground">{artifact.name}</span>
        <span>{artifact.bytes} B</span>
      </div>
      {error ? (
        <p className="text-xs text-destructive">图表加载失败：{error}</p>
      ) : !payload ? (
        <p className="text-xs text-muted-foreground">正在解析图表…</p>
      ) : !payload.schema || !payload.data ? (
        <p className="text-xs text-muted-foreground">缺少 schema 或 data,无法渲染</p>
      ) : (
        <ChartRenderer schema={payload.schema} data={payload.data} />
      )}
    </figure>
  );
}

interface ChartSchema {
  type: "line" | "bar" | "scatter" | "heatmap";
  x: { field: string; label?: string };
  y: { field: string; label?: string };
  color?: { field: string; label?: string };
  bins?: number;
}

function ChartRenderer({ schema, data }: { schema: ChartSchema; data: unknown[] }) {
  // Local Recharts import — desktop renderer only. The shared
  // @multica/ui/chart primitive (packages/ui/components/ui/chart.tsx)
  // is reserved for shadcn-style thuimbnails, not full-bleed
  // interactive plots.
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const recharts = require("recharts") as typeof import("recharts");
  const { ResponsiveContainer, LineChart, Line, BarChart, Bar, ScatterChart, Scatter, XAxis, YAxis, CartesianGrid, Tooltip } = recharts;

  if (!Array.isArray(data) || data.length === 0) {
    return <p className="text-xs text-muted-foreground">数据为空</p>;
  }
  const xKey = schema.x.field;
  const yKey = schema.y.field;

  if (schema.type === "bar") {
    return (
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data as Record<string, unknown>[]}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey={xKey} tick={{ fontSize: 10 }} />
            <YAxis tick={{ fontSize: 10 }} />
            <Tooltip />
            <Bar dataKey={yKey} fill="var(--primary)" />
          </BarChart>
        </ResponsiveContainer>
      </div>
    );
  }
  if (schema.type === "scatter") {
    return (
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <ScatterChart>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey={xKey} tick={{ fontSize: 10 }} />
            <YAxis dataKey={yKey} tick={{ fontSize: 10 }} />
            <Tooltip />
            <Scatter data={data as Record<string, unknown>[]} fill="var(--primary)" />
          </ScatterChart>
        </ResponsiveContainer>
      </div>
    );
  }
  if (schema.type === "heatmap") {
    // Heatmap is not a first-class Recharts primitive; render a
    // simple table as a graceful placeholder until 0.3.25 ships
    // the proper cell grid (see labs-platform 0.3.25 roadmap).
    return (
      <div className="grid max-h-64 grid-cols-6 gap-1 overflow-auto">
        {(data as Record<string, number>[]).slice(0, 36).map((row, i) => (
          <div
            key={i}
            className="aspect-square rounded"
            style={{
              backgroundColor: `color-mix(in oklab, var(--primary) ${Math.min(100, (row[yKey] as number) * 100)}%, var(--muted))`,
            }}
            title={`${row[xKey]}: ${row[yKey]}`}
          />
        ))}
      </div>
    );
  }
  // Default: line
  return (
    <div className="h-64">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data as Record<string, unknown>[]}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
          <XAxis dataKey={xKey} tick={{ fontSize: 10 }} />
          <YAxis tick={{ fontSize: 10 }} />
          <Tooltip />
          <Line type="monotone" dataKey={yKey} stroke="var(--primary)" strokeWidth={2} dot={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}

function TextFetch({ url }: { url: string }) {
  const [text, setText] = useState<string>("loading…");
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const r = await api.rawRequest(url);
      if (!r.ok) {
        if (!cancelled) setText(`# fetch failed: ${r.status}`);
        return;
      }
      const body = await r.text();
      if (!cancelled) setText(body.slice(0, 200_000));
    })();
    return () => {
      cancelled = true;
    };
  }, [url]);
  return <>{text}</>;
}
