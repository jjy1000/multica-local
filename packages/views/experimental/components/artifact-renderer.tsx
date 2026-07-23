"use client";

import { useState } from "react";
import { Download, FileText, ExternalLink } from "lucide-react";
import { api } from "@multica/core/api";

// 0.3.60 Labs sandbox — generic artifact renderer for user plugins.
//
// Renders a single plugin artifact by type. Kept dependency-free on
// purpose: charts are drawn with inline SVG (no Recharts) so the shell
// adds zero bundle weight and works in the packaged desktop renderer.
//
// v1: hardcoded Chinese labels; i18n keys deferred.

export interface Artifact {
  id: string;
  type: "image" | "chart" | "table" | "html" | "code" | "file" | "text";
  title: string;
  mime_type?: string;
  size?: number;
  /** Inline data for chart / table / code / text. */
  data?: unknown;
  /** Download URL for image / file / html. Relative to the API host. */
  url?: string;
  created_at: string;
}

export interface ArtifactRendererProps {
  artifact: Artifact;
  pluginSlug: string;
}

/** Resolve an artifact-relative URL against the configured API host. */
function resolveUrl(url: string): string {
  if (/^https?:\/\//i.test(url) || url.startsWith("data:")) return url;
  return `${api.getBaseUrl()}${url}`;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

// ---- chart ----

interface ChartDataset {
  label: string;
  values: number[];
}

interface ChartData {
  chart_type: "line" | "bar" | "scatter";
  labels: string[];
  datasets: ChartDataset[];
}

const CHART_COLORS = ["#3b82f6", "#10b981", "#f59e0b", "#ef4444", "#8b5cf6", "#ec4899"];

function isChartData(data: unknown): data is ChartData {
  if (typeof data !== "object" || data === null) return false;
  const d = data as Record<string, unknown>;
  return Array.isArray(d.labels) && Array.isArray(d.datasets);
}

function ChartSvg({ data }: { data: ChartData }) {
  const W = 480;
  const H = 240;
  const PAD = 32;
  const labels = data.labels;
  const datasets = data.datasets;
  const chartType = data.chart_type ?? "line";

  const allValues = datasets.flatMap((ds) => ds.values.map((v) => Number(v) || 0));
  const maxVal = Math.max(1, ...allValues);
  const minVal = Math.min(0, ...allValues);
  const range = maxVal - minVal || 1;

  const innerW = W - PAD * 2;
  const innerH = H - PAD * 2;
  const stepX = labels.length > 1 ? innerW / (labels.length - 1) : innerW;

  const x = (i: number) => PAD + (labels.length > 1 ? i * stepX : innerW / 2);
  const y = (v: number) => PAD + innerH - ((Number(v) || 0) - minVal) / range * innerH;

  // Horizontal grid lines + y-axis labels (4 ticks).
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((t) => minVal + t * range);

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      className="w-full h-auto"
      role="img"
      aria-label="chart"
    >
      {ticks.map((t, i) => (
        <g key={i}>
          <line
            x1={PAD}
            x2={W - PAD}
            y1={y(t)}
            y2={y(t)}
            className="stroke-muted"
            strokeWidth={1}
          />
          <text
            x={PAD - 6}
            y={y(t) + 3}
            textAnchor="end"
            className="fill-muted-foreground"
            fontSize={9}
          >
            {Math.round(t)}
          </text>
        </g>
      ))}

      {datasets.map((ds, di) => {
        const color = CHART_COLORS[di % CHART_COLORS.length];
        const pts = ds.values.map((v, i) => `${x(i)},${y(v)}`);

        if (chartType === "bar") {
          const groupW = stepX * 0.7;
          const barW = groupW / datasets.length;
          return (
            <g key={di}>
              {ds.values.map((v, i) => {
                const bx = x(i) - groupW / 2 + di * barW;
                const by = y(v);
                return (
                  <rect
                    key={i}
                    x={bx}
                    y={by}
                    width={Math.max(1, barW - 1)}
                    height={Math.max(0, PAD + innerH - by)}
                    fill={color}
                    rx={1}
                  />
                );
              })}
            </g>
          );
        }

        if (chartType === "scatter") {
          return (
            <g key={di}>
              {ds.values.map((v, i) => (
                <circle key={i} cx={x(i)} cy={y(v)} r={3} fill={color} />
              ))}
            </g>
          );
        }

        // line (default)
        return (
          <g key={di}>
            <polyline
              points={pts.join(" ")}
              fill="none"
              stroke={color}
              strokeWidth={2}
              strokeLinejoin="round"
              strokeLinecap="round"
            />
            {ds.values.map((v, i) => (
              <circle key={i} cx={x(i)} cy={y(v)} r={2.5} fill={color} />
            ))}
          </g>
        );
      })}

      {labels.map((lbl, i) => (
        <text
          key={i}
          x={x(i)}
          y={H - PAD + 14}
          textAnchor="middle"
          className="fill-muted-foreground"
          fontSize={9}
        >
          {String(lbl).slice(0, 12)}
        </text>
      ))}
    </svg>
  );
}

// ---- table ----

interface TableData {
  columns: string[];
  rows: string[][];
}

function isTableData(data: unknown): data is TableData {
  if (typeof data !== "object" || data === null) return false;
  const d = data as Record<string, unknown>;
  return Array.isArray(d.columns) && Array.isArray(d.rows);
}

// ---- component ----

export function ArtifactRenderer({ artifact, pluginSlug: _pluginSlug }: ArtifactRendererProps) {
  const [zoomed, setZoomed] = useState(false);

  switch (artifact.type) {
    case "image": {
      if (!artifact.url) {
        return <p className="text-sm text-muted-foreground">图片缺少 URL。</p>;
      }
      const src = resolveUrl(artifact.url);
      return (
        <div className="space-y-2">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={src}
            alt={artifact.title}
            onClick={() => setZoomed((z) => !z)}
            className={
              zoomed
                ? "w-full rounded-lg border border-border cursor-zoom-out"
                : "max-w-full rounded-lg border border-border cursor-zoom-in"
            }
          />
          <p className="text-xs text-muted-foreground">点击图片{zoomed ? "还原" : "放大"}</p>
        </div>
      );
    }

    case "chart": {
      if (!isChartData(artifact.data)) {
        return <p className="text-sm text-muted-foreground">图表数据格式无效。</p>;
      }
      return (
        <div className="rounded-lg border border-border bg-background p-3">
          <ChartSvg data={artifact.data} />
          {artifact.data.datasets.length > 1 && (
            <div className="mt-2 flex flex-wrap gap-3">
              {artifact.data.datasets.map((ds, i) => (
                <span key={i} className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                  <span
                    className="inline-block h-2 w-2 rounded-full"
                    style={{ backgroundColor: CHART_COLORS[i % CHART_COLORS.length] }}
                  />
                  {ds.label}
                </span>
              ))}
            </div>
          )}
        </div>
      );
    }

    case "table": {
      if (!isTableData(artifact.data)) {
        return <p className="text-sm text-muted-foreground">表格数据格式无效。</p>;
      }
      const { columns, rows } = artifact.data;
      return (
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/50">
                {columns.map((col, i) => (
                  <th
                    key={i}
                    className="px-3 py-2 text-left font-medium text-foreground whitespace-nowrap"
                  >
                    {col}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, ri) => (
                <tr key={ri} className="border-b border-border last:border-b-0">
                  {row.map((cell, ci) => (
                    <td key={ci} className="px-3 py-2 text-muted-foreground">
                      {cell}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    }

    case "code": {
      const code =
        typeof artifact.data === "string"
          ? artifact.data
          : artifact.data != null
            ? JSON.stringify(artifact.data, null, 2)
            : "";
      return (
        <pre className="overflow-x-auto rounded-lg border border-border bg-muted/40 p-3 text-xs">
          <code className="font-mono text-foreground">{code}</code>
        </pre>
      );
    }

    case "text": {
      const text = typeof artifact.data === "string" ? artifact.data : String(artifact.data ?? "");
      return (
        <div className="space-y-2">
          {text.split(/\n{2,}/).map((para, i) => (
            <p key={i} className="text-sm leading-relaxed text-foreground whitespace-pre-wrap">
              {para}
            </p>
          ))}
        </div>
      );
    }

    case "html": {
      const srcDoc = typeof artifact.data === "string" ? artifact.data : undefined;
      const src = artifact.url ? resolveUrl(artifact.url) : undefined;
      return (
        <iframe
          title={artifact.title}
          sandbox="allow-scripts"
          {...(srcDoc ? { srcDoc } : { src })}
          className="h-[420px] w-full rounded-lg border border-border bg-background"
        />
      );
    }

    case "file": {
      const href = artifact.url ? resolveUrl(artifact.url) : undefined;
      return (
        <div className="flex items-center gap-3 rounded-lg border border-border bg-background p-4">
          <FileText className="h-8 w-8 shrink-0 text-muted-foreground" aria-hidden />
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium text-foreground">{artifact.title}</p>
            <p className="text-xs text-muted-foreground">
              {[artifact.mime_type, artifact.size != null ? formatBytes(artifact.size) : null]
                .filter(Boolean)
                .join(" · ") || "文件"}
            </p>
          </div>
          {href && (
            <a
              href={href}
              download
              className="inline-flex items-center gap-1.5 rounded-md border border-input bg-background px-3 py-1.5 text-xs font-medium text-foreground hover:bg-muted"
            >
              <Download className="h-3.5 w-3.5" aria-hidden />
              下载
            </a>
          )}
        </div>
      );
    }

    default:
      return (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <ExternalLink className="h-4 w-4" aria-hidden />
          不支持的产物类型
        </div>
      );
  }
}
