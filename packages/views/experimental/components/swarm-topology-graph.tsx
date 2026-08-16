// Swarm topology graph — pure-SVG node-edge renderer.
//
// Self-organising multi-agent system visualised as a DAG of role-agents
// with parent_role_id + depends_on edges. Pure SVG (no external graph
// library) to keep within the design-system constraints (4 colors /
// 1 font / light + dark) per CLAUDE.md "UI Rules" + "CSS" sections.
//
// Read-only: the interrupt action is a separate <SwarmInterruptBar />
// button, NOT a graph gesture. The graph itself never mutates state —
// it just reflects swarm_role rows fetched from /api/experimental/
// swarm-topology/runs/{id}/state.
//
// a11y: each node has a <title> for native SVG tooltip AND a visually-
// hidden <ul> for screen readers. Status is conveyed by a glyph (not
// color alone) for colorblind accessibility — per design-quality.md.

import { useMemo } from "react";

import { useT } from "../../i18n";

export interface SwarmRole {
  id: string;
  role_name: string;
  parent_role_id?: string;
  status: string;
  current_step?: string;
  last_heartbeat_at?: string;
}

export interface SwarmTopologyGraphProps {
  roles: SwarmRole[];
  className?: string;
}

// Layout constants. Kept inline (no design tokens) because the graph
// is a self-contained visualization, not part of the standard UI
// surface.
const NODE_W = 120;
const NODE_H = 56;
const NODE_GAP_X = 40;
const NODE_GAP_Y = 30;
const PADDING = 16;

// Status → { fill, stroke, glyph }.
// Glyph ensures colorblind users can distinguish statuses without relying
// on hue (per design-quality.md "Color used semantically").
const STATUS_VISUAL: Record<
  string,
  { fill: string; stroke: string; glyph: string }
> = {
  created:   { fill: "#f1f5f9", stroke: "#94a3b8", glyph: "○" },
  ready:     { fill: "#dbeafe", stroke: "#3b82f6", glyph: "●" },
  running:   { fill: "#bfdbfe", stroke: "#1d4ed8", glyph: "◐" },
  idle:      { fill: "#fef3c7", stroke: "#d97706", glyph: "◐" },
  completed: { fill: "#d1fae5", stroke: "#059669", glyph: "✓" },
  failed:    { fill: "#fee2e2", stroke: "#dc2626", glyph: "✗" },
  archived:  { fill: "#e5e7eb", stroke: "#6b7280", glyph: "▣" },
};

export function SwarmTopologyGraph({ roles, className }: SwarmTopologyGraphProps) {
  const { t } = useT("swarm");
  // useT is typed against the closed enum of status keys. Unknown
  // statuses (forward-compatible enum additions, server-side drift)
  // fall through to the raw enum string. Cast to bypass the closed
  // union when indexing by an arbitrary string.
  const tAny = t as unknown as (sel: (res: any) => string) => string;
  // Topological layout: compute levels (depth from any root), then
  // position each role in its level's row.
  const layout = useMemo(() => computeLayout(roles), [roles]);

  const totalW = layout.width + 2 * PADDING;
  const totalH = layout.height + 2 * PADDING;

  if (roles.length === 0) {
    return (
      <div className={className} data-testid="swarm-topology-graph-empty">
        <div className="rounded-md border border-dashed border-muted p-6 text-center text-sm text-muted-foreground">
          {t(($) => $.graph.empty)}
        </div>
      </div>
    );
  }

  return (
    <div
      className={className}
      data-testid="swarm-topology-graph"
      role="img"
      aria-label={t(($) => $.graph.aria, { count: roles.length })}
    >
      <svg
        width={totalW}
        height={totalH}
        viewBox={`0 0 ${totalW} ${totalH}`}
        xmlns="http://www.w3.org/2000/svg"
        className="font-sans text-xs"
      >
        <title>{t(($) => $.title)}</title>
        {/* Edges first so nodes render on top. */}
        {layout.edges.map((edge, i) => {
          const from = layout.nodes.find((n) => n.id === edge.from);
          const to = layout.nodes.find((n) => n.id === edge.to);
          if (!from || !to) return null;
          const fx = from.x + NODE_W / 2;
          const fy = from.y + NODE_H;
          const tx = to.x + NODE_W / 2;
          const ty = to.y;
          const my = (fy + ty) / 2;
          const path = `M ${fx} ${fy} C ${fx} ${my}, ${tx} ${my}, ${tx} ${ty}`;
          return (
            <path
              key={i}
              d={path}
              stroke="#64748b"
              strokeWidth={1.5}
              fill="none"
              markerEnd="url(#arrowhead)"
              data-from={edge.from}
              data-to={edge.to}
            />
          );
        })}

        {/* Arrowhead marker. */}
        <defs>
          <marker
            id="arrowhead"
            markerWidth="8"
            markerHeight="8"
            refX="6"
            refY="4"
            orient="auto"
          >
            <path d="M 0 0 L 8 4 L 0 8 z" fill="#64748b" />
          </marker>
        </defs>

        {/* Nodes. */}
        {layout.nodes.map((node) => {
          const visual = STATUS_VISUAL[node.status] ?? STATUS_VISUAL.created;
          if (!visual) return null;
          const statusLabel = tAny(($) => $.status[node.status]) || node.status;
          return (
            <g
              key={node.id}
              transform={`translate(${node.x + PADDING}, ${node.y + PADDING})`}
              data-role-id={node.id}
              data-role-name={node.role_name}
              data-status={node.status}
            >
              {/* Native SVG tooltip for sighted mouse hover */}
              <title>
                {node.role_name} — {statusLabel}
                {node.current_step ? ` · ${node.current_step}` : ""}
              </title>
              <rect
                width={NODE_W}
                height={NODE_H}
                rx={6}
                fill={visual.fill}
                stroke={visual.stroke}
                strokeWidth={node.status === "running" ? 2.5 : 1.5}
              />
              <text
                x={NODE_W / 2}
                y={18}
                textAnchor="middle"
                fill="#0f172a"
                fontWeight={600}
                fontSize={14}
                aria-hidden="true"
              >
                {visual.glyph}
              </text>
              <text
                x={NODE_W / 2}
                y={36}
                textAnchor="middle"
                fill="#0f172a"
                fontWeight={600}
              >
                {node.role_name}
              </text>
              <text
                x={NODE_W / 2}
                y={50}
                textAnchor="middle"
                fill="#475569"
                fontSize={10}
              >
                {statusLabel}
                {node.current_step ? ` · ${truncate(node.current_step, 10)}` : ""}
              </text>
            </g>
          );
        })}
      </svg>

      {/* Visually-hidden role list for screen readers (the SVG itself
          is not readable by SR without alt text per node). */}
      <ul className="sr-only" aria-label={t(($) => $.graph.aria, { count: roles.length })}>
        {layout.nodes.map((node) => (
          <li key={node.id}>
            {node.role_name} —
            {tAny(($) => $.status[node.status]) || node.status}
            {node.current_step ? ` — ${node.current_step}` : ""}
          </li>
        ))}
      </ul>

      {/* Legend. */}
      <div className="mt-3 flex flex-wrap gap-2 text-xs">
        <span className="font-medium text-muted-foreground mr-1">
          {t(($) => $.graph.legend)}
        </span>
        {Object.entries(STATUS_VISUAL).map(([status, visual]) => (
          <span key={status} className="inline-flex items-center gap-1.5">
            <span
              className="inline-flex items-center justify-center h-3 w-3 rounded-sm text-[10px]"
              style={{
                background: visual.fill,
                borderColor: visual.stroke,
                borderWidth: 1,
              }}
              aria-hidden="true"
            >
              {visual.glyph}
            </span>
            <span className="text-muted-foreground">
              {tAny(($) => $.status[status]) || status}
            </span>
          </span>
        ))}
      </div>
    </div>
  );
}

interface LayoutNode {
  id: string;
  role_name: string;
  status: string;
  current_step?: string;
  x: number;
  y: number;
  level: number;
}

interface LayoutEdge {
  from: string;
  to: string;
}

interface Layout {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  width: number;
  height: number;
}

// computeLayout does a Kahn-style topological sort to assign each role
// a depth level, then positions them row-by-row. Falls back to a
// flat row if the DAG has cycles (defensive — ValidateTopologySpec
// already rejects cycles, but the UI shouldn't crash if one slips
// through).
function computeLayout(roles: SwarmRole[]): Layout {
  const byId = new Map<string, SwarmRole>(roles.map((r) => [r.id, r]));
  const children = new Map<string, string[]>();
  const indegree = new Map<string, number>();

  for (const r of roles) {
    children.set(r.id, []);
    indegree.set(r.id, 0);
  }
  for (const r of roles) {
    if (r.parent_role_id && byId.has(r.parent_role_id)) {
      children.get(r.parent_role_id)!.push(r.id);
      indegree.set(r.id, (indegree.get(r.id) ?? 0) + 1);
    }
  }

  // Kahn's algorithm: walk levels left-to-right.
  const levels: string[][] = [];
  let frontier = roles.filter((r) => (indegree.get(r.id) ?? 0) === 0).map((r) => r.id);
  const visited = new Set<string>();

  while (frontier.length > 0) {
    levels.push(frontier);
    for (const id of frontier) visited.add(id);
    const next: string[] = [];
    for (const id of frontier) {
      for (const child of children.get(id) ?? []) {
        indegree.set(child, (indegree.get(child) ?? 0) - 1);
        if ((indegree.get(child) ?? 0) === 0 && !visited.has(child)) {
          next.push(child);
        }
      }
    }
    frontier = next;
  }

  // Any unvisited nodes (cycle, shouldn't happen) → flat row at the bottom.
  const orphans = roles.filter((r) => !visited.has(r.id)).map((r) => r.id);
  if (orphans.length > 0) levels.push(orphans);

  // Compute pixel positions.
  const nodes: LayoutNode[] = [];
  let maxRowWidth = 0;
  for (let levelIdx = 0; levelIdx < levels.length; levelIdx++) {
    const row = levels[levelIdx];
    if (!row) continue;
    const rowWidth = row.length * NODE_W + (row.length - 1) * NODE_GAP_X;
    if (rowWidth > maxRowWidth) maxRowWidth = rowWidth;
  }
  for (let levelIdx = 0; levelIdx < levels.length; levelIdx++) {
    const row = levels[levelIdx];
    if (!row) continue;
    const rowWidth = row.length * NODE_W + (row.length - 1) * NODE_GAP_X;
    const xOffset = (maxRowWidth - rowWidth) / 2;
    for (let colIdx = 0; colIdx < row.length; colIdx++) {
      const id = row[colIdx];
      if (!id) continue;
      const role = byId.get(id);
      if (!role) continue;
      nodes.push({
        id,
        role_name: role.role_name,
        status: role.status,
        current_step: role.current_step,
        x: xOffset + colIdx * (NODE_W + NODE_GAP_X),
        y: levelIdx * (NODE_H + NODE_GAP_Y),
        level: levelIdx,
      });
    }
  }

  // Edges.
  const edges: LayoutEdge[] = [];
  for (const r of roles) {
    if (r.parent_role_id && byId.has(r.parent_role_id)) {
      edges.push({ from: r.parent_role_id, to: r.id });
    }
  }

  return {
    nodes,
    edges,
    width: maxRowWidth,
    height: levels.length * NODE_H + (levels.length - 1) * NODE_GAP_Y,
  };
}

function truncate(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max - 1) + "…";
}