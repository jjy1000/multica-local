---
name: multica-causal-trace
description: "Use when you need to trace WHY something happened in this workspace: reconstruct a task's decision chain, see how a parent issue split into sub-issues, find which agent did what, or audit a previous run's outcomes before redoing work. Backed by the causal knowledge graph (causal_graph lab), which records every task enqueue/complete/sub-issue split automatically. Do not use it for normal issue work, commenting, or platform operations."
user-invocable: true
allowed-tools: Bash(multica *)
---

# Causal Trace (0.5.119+)

The workspace maintains a typed causal knowledge graph — the
知识图谱决策追溯自动系统 (Knowledge Graph Decision Traceability).
It records AUTOMATICALLY, with no issue binding and no manual step:

| Event | What lands on the graph |
| --- | --- |
| task enqueued | `[action]` node stamped with the assigned agent's name |
| task completed/failed | `[outcome]` node, linked `--causes-->` from the action |
| issue split into a sub-issue | parent root `--depends_on-->` child root |
| lab delegation (`multica lab delegate --parent`) | same split edge |
| nightly LLM pass | `[decision]`/`[evidence]` triples as SUGGESTED edges (human-gated, confidence ≤ 0.5) |

Trust ladder: native task hooks > Semantica mirrors > Pythia closures
> LLM-curated suggestions. Only `active` edges are shown; suggested
ones stay invisible until a human confirms them.

## When to trace

- **Before redoing work** — the graph is ground truth about what prior
  runs already produced. Do not repeat a run that has a recorded
  outcome.
- **After a task fission** — see which child issues a parent split
  into and which agent owns each piece.
- **When auditing or optimizing another agent** — the `(by <agent>)`
  attributions on action/outcome nodes give the per-agent decision
  trail. Feed it into self-optimization reviews, trust corrections,
  or skill-improvement proposals.
- **When you inherit a long-running issue** — the chain explains how
  the current state was reached.

## How to trace

```sh
# Markdown trace around an issue (nodes + typed edges, BFS depth 2)
multica causal subgraph --issue "$ISSUE_REF"

# Wider or narrower trace (depth 1-4)
multica causal subgraph --issue "$ISSUE_REF" --depth 3

# Raw JSON for programmatic consumption
multica causal subgraph --issue "$ISSUE_REF" --output json
```

`$ISSUE_REF` accepts a UUID, an issue key (`JYF-123`), or an
unambiguous id prefix. The lab is on by default; if the command 404s,
the flag was turned off (Settings → Labs → 知识图谱决策追溯自动系统).

## Reading the trace

- `- [action] <label> (by <agent>)` — a run an agent performed; the
  attribution is stamped at enqueue time.
- `- [outcome] <label>` — what that run produced (label carved from
  the run's final output; the status lives in provenance).
- `- [constraint] <label>` — the issue's root node (its title).
- Edges: `--enables-->` (trigger → run), `--causes-->` (run →
  outcome), `--depends_on-->` (parent split → child split).
- `(conf=0.42)` — low-confidence edges; treat as hints, not facts.

## Hard rules

- **The trace is read-only for you.** Creating edges or nodes is the
  curator agent's job (human-gated); you consume, you do not write.
- **Do not treat a sparse graph as "nothing happened".** Nodes appear
  only from 0.5.119 onward (plus anything the nightly pass proposed
  and a human confirmed). Older history lives in the issue timeline.
- **Cite the node when you act on the trace.** If a trace changes your
  plan (skip a redo, pick a different approach), reference the node
  label in your comment so the reasoning stays auditable.
