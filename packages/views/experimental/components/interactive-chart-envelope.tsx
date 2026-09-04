"use client";

// InteractiveChartEnvelope — shared renderer for the `interactive-chart`
// attachment envelope ({schema, data}). Originally lived only in the
// desktop claude-lab-view (0.3.40 v2); promoted to the shared views
// package so the issue-side result renderers (LabTaskResultView /
// LabOutputPanel) draw the same real charts instead of degrading the
// envelope to a JSON code dump.
//
// The envelope is agent-emitted and schema-light: every field is treated
// as unknown and coerced defensively — a malformed payload renders the
// empty-chart placeholder, never throws.

import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Scatter,
  ScatterChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { useT } from "../../i18n";

export interface ChartEnvelope {
  schema?: {
    type: "line" | "bar" | "scatter" | "heatmap";
    x: { field: string; label?: string };
    y: { field: string; label?: string };
    color?: { field: string; label?: string };
    bins?: number;
  };
  data?: unknown[];
}

export function InteractiveChartEnvelope({
  data,
  heightClass = "h-44",
}: {
  data: unknown;
  // Container height — the workbench result panel uses a taller canvas
  // than the compact timeline/issue-side cards.
  heightClass?: string;
}) {
  const { t } = useT("experimental");
  const envelope = (data ?? {}) as ChartEnvelope;
  const schema = envelope?.schema;
  const points = Array.isArray(envelope?.data) ? envelope.data : [];
  if (
    !schema ||
    !schema.x ||
    !schema.y ||
    points.length === 0 ||
    points.some((p) => p == null || typeof p !== "object")
  ) {
    return (
      <div className="flex h-32 items-center justify-center text-xs text-muted-foreground">
        {t(($) => $.lab_output_panel.chart_empty)}
      </div>
    );
  }
  const W = 320;
  const H = 160;
  const chart = (() => {
    if (schema.type === "line") {
      return (
        <LineChart data={points} width={W} height={H}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
          <XAxis dataKey={schema.x.field} tick={{ fontSize: 10 }} />
          <YAxis tick={{ fontSize: 10 }} />
          <Tooltip />
          <Line
            type="monotone"
            dataKey={schema.y.field}
            stroke="var(--primary)"
            dot={false}
          />
        </LineChart>
      );
    }
    if (schema.type === "bar") {
      return (
        <BarChart data={points} width={W} height={H}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
          <XAxis dataKey={schema.x.field} tick={{ fontSize: 10 }} />
          <YAxis tick={{ fontSize: 10 }} />
          <Tooltip />
          <Bar dataKey={schema.y.field} fill="var(--primary)" />
        </BarChart>
      );
    }
    if (schema.type === "scatter") {
      return (
        <ScatterChart width={W} height={H}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
          <XAxis dataKey={schema.x.field} tick={{ fontSize: 10 }} />
          <YAxis dataKey={schema.y.field} tick={{ fontSize: 10 }} />
          <Tooltip />
          <Scatter data={points} fill="var(--primary)" />
        </ScatterChart>
      );
    }
    return null;
  })();
  if (!chart) {
    return (
      <div className="flex h-32 items-center justify-center text-xs text-muted-foreground">
        {t(($) => $.lab_output_panel.chart_unsupported_type, { type: schema.type })}
      </div>
    );
  }
  return (
    <div className={`${heightClass} w-full p-2`}>
      <ResponsiveContainer width="100%" height="100%">
        {chart}
      </ResponsiveContainer>
    </div>
  );
}
