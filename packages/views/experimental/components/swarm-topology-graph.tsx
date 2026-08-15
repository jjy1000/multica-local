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

import { useMemo } from "react";

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

const STATUS_COLOR: Record<string, { fill: string; stroke: string }> = {
  created:   { fill: "#f1f5f9", stroke: "#94a3b8" },
  ready:     { fill: "#dbeafe", stroke: "#3b82f6" },
  running:   { fill: "#bfdbfe", stroke: "#1d4ed8" },
  idle:      { fill: "#fef3c7", stroke: "#d97706" },
  completed: { fill: "#d1fae5", stroke: "#059669" },
  failed:    { fill: "#fee2e2", stroke: "#dc2626" },
  archived:  { fill: "#e5e7eb", stroke: "#6b7280" },
};

export function SwarmTopologyGraph({ roles, className }: SwarmTopologyGraphProps) {
  // Topological layout: compute levels (depth from any root), then
  // position each role in its level's row.
  const layout = useMemo(() => computeLayout(roles), [roles]);

  const totalW = layout.width + 2 * PADDING;
  const totalH = layout.height + 2 * PADDING;

  if (roles.length === 0) {
    return (
      <div className={className} data-testid="swarm-topology-graph-empty">
        <div className="rounded-md border border-dashed border-muted p-6 text-center text-sm text-muted-foreground">
          No roles yet. The leader will author role-agents during the
          bootstrap phase (research → design → implement → review).
        </div>
      </div>
    );
  }

  return (
    <div
      className={className}
      data-testid="swarm-topology-graph"
      role="img"
      aria-label={`Swarm topology with ${roles.length} role-agents`}
    >
      <svg
        width={totalW}
        height={totalH}
        viewBox={`0 0 ${totalW} ${totalH}`}
        xmlns="http://www.w3.org/2000/svg"
        className="font-sans text-xs"
      >
        <title>Swarm topology</title>
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
          const colors = STATUS_COLOR[node.status] ?? STATUS_COLOR.created;
          if (!colors) return null;
          return (
            <g
              key={node.id}
              transform={`translate(${node.x + PADDING}, ${node.y + PADDING})`}
              data-role-id={node.id}
              data-role-name={node.role_name}
              data-status={node.status}
            >
              <rect
                width={NODE_W}
                height={NODE_H}
                rx={6}
                fill={colors.fill}
                stroke={colors.stroke}
                strokeWidth={node.status === "running" ? 2.5 : 1.5}
              />
              <text
                x={NODE_W / 2}
                y={20}
                textAnchor="middle"
                fill="#0f172a"
                fontWeight={600}
              >
                {node.role_name}
              </text>
              <text
                x={NODE_W / 2}
                y={38}
                textAnchor="middle"
                fill="#475569"
                fontSize={10}
              >
                {node.status}
                {node.current_step ? ` · ${truncate(node.current_step, 12)}` : ""}
              </text>
            </g>
          );
        })}
      </svg>

      {/* Legend. */}
      <div className="mt-3 flex flex-wrap gap-2 text-xs">
        {Object.entries(STATUS_COLOR).map(([status, colors]) => (
          <span key={status} className="inline-flex items-center gap-1.5">
            <span
              className="inline-block h-3 w-3 rounded-sm"
              style={{ background: colors.fill, borderColor: colors.stroke, borderWidth: 1 }}
            />
            <span className="text-muted-foreground">{status}</span>
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