import type { CausalEdge, CausalNode } from "@multica/core/types/api";

// buildGraphDigest (0.5.121) — the compression layer of the
// decision-traceability system: the full graph is for humans to stare
// at; agents (and users pasting into a task) want the compressed
// signal. Pure function, exported for pins.
export function buildGraphDigest(nodes: CausalNode[], edges: CausalEdge[]): string {
  const countBy = (values: string[]) => {
    const m = new Map<string, number>();
    for (const v of values) m.set(v, (m.get(v) ?? 0) + 1);
    return m;
  };
  const fmtCounts = (m: Map<string, number>) =>
    [...m.entries()]
      .sort((a, b) => b[1] - a[1])
      .map(([k, n]) => `${k} ${n}`)
      .join(" / ") || "none";

  const nodeTypes = countBy(nodes.map((n) => n.type));
  const edgeTypes = countBy(edges.map(e => e.type));
  const agents = countBy(
    nodes
      .map((n) => (typeof n.metadata?.agent === "string" && n.metadata.agent ? n.metadata.agent : null))
      .filter((a): a is string => Boolean(a)),
  );
  const degree = new Map<string, number>();
  for (const e of edges) {
    degree.set(e.from_node_id, (degree.get(e.from_node_id) ?? 0) + 1);
    degree.set(e.to_node_id, (degree.get(e.to_node_id) ?? 0) + 1);
  }
  const hubs = [...nodes]
    .map((n) => ({ n, d: degree.get(n.id) ?? 0 }))
    .filter((x) => x.d > 0)
    .sort((a, b) => b.d - a.d)
    .slice(0, 5);
  const suggested = edges.filter((e) => e.status === "suggested").length;

  const lines = [
    "## Causal Graph Digest",
    "",
    `- nodes: ${nodes.length} (${fmtCounts(nodeTypes)})`,
    `- edges: ${edges.length} (${fmtCounts(edgeTypes)})${suggested ? ` · ${suggested} suggested (human-gated)` : ""}`,
  ];
  if (agents.size > 0) {
    lines.push(`- agents on record: ${[...agents.entries()].sort((a, b) => b[1] - a[1]).map(([a, n]) => `${a} (${n})`).join(", ")}`);
  }
  if (hubs.length > 0) {
    lines.push(`- key hubs: ${hubs.map((h) => `[${h.n.type}] ${h.n.label} (deg ${h.d})`).join(", ")}`);
  }
  return lines.join("\n");
}

