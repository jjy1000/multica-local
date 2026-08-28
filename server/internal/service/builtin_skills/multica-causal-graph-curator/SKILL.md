---
name: multica-causal-graph-curator
description: "Use when maintaining the workspace's issue causal graph: after runs land on an issue, scan the recent comments/status transitions, extract (subject, predicate, object) causal relations between existing graph nodes, and POST them as SUGGESTED edges for human confirmation via /api/causal-graph/suggestions. Requires the `causal_graph` Labs flag enabled. Suggestions never auto-apply — a human confirms or rejects every one. Do not use it for normal issue work, forecasting, or platform operations."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Causal Graph Curator (0.5.83+)

You maintain the workspace's issue causal graph (decision tracking).
The graph's edges carry a trust ladder; yours is the WEAKEST tier:

| Tier | Source | Trust |
| --- | --- | --- |
| A | native task hooks (enqueue/complete) | machine-native |
| B | Semantica decision mirrors | machine-native |
| C | Pythia hypothesis closure | calibrated |
| **D** | **you (curated triples)** | **LLM — suggested-only, confidence ≤ 0.5, human-gated** |

Everything you write lands `status='suggested'` and is INVISIBLE to
the graph reads until a human confirms it. This is by design — never
attempt to bypass it (no direct active-edge writes, no re-using the
manual endpoint).

## Hard rule — flag check before any call

```sh
multica experimental flags list 2>/dev/null | grep -E '^causal_graph\s+(true|enabled)'
```

If the flag is off, **refuse**:

> 因果图谱未启用。请在 Settings → Labs 打开「causal_graph」开关。

## The loop

### Step 1 — read the window

Pull the issue's recent activity since your last observation (the
daemon hands you the issue id):

```sh
multica issue get <issue-id> --output json
multica issue comment list <issue-id> --output json
```

### Step 2 — read the existing nodes

You may only link nodes that already exist (Tier A/B write actions,
decisions, outcomes; you do NOT invent entities):

```sh
curl -sS -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  "$MULTICA_API_URL/api/causal-graph/nodes?workspace_id=$WORKSPACE_ID&issue_id=<issue-id>"
```

If fewer than two nodes exist, stop — there is nothing to relate.

### Step 3 — extract triples, sparingly

From the comment/status window, extract at most **3** relations per
scan, only when the evidence is explicit ("X failed so we switched to
Y", "this blocks the rollout"). Each triple:

- `from_node_id` / `to_node_id` — existing node ids (Step 2)
- `type` — one of `causes | supports | contradicts | depends_on |
  enables | blocks`
- `confidence` — your honest 0..1 estimate (the server halves it;
  suggested edges cap at 0.5)
- `rationale` — REQUIRED, one sentence quoting the evidence

### Step 4 — propose

```sh
curl -sS -X POST \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "from_node_id": "<id>",
    "to_node_id": "<id>",
    "type": "causes",
    "confidence": 0.8,
    "rationale": "Comment 2026-08-27: the migration failure forced the rollback."
  }' \
  "$MULTICA_API_URL/api/causal-graph/suggestions?workspace_id=$WORKSPACE_ID"
```

201 = the suggestion is queued for human review. You will not learn
the verdict here — the user confirms/rejects in the graph view. Never
re-propose the same (from, to, type) triple; 409 means it already
exists.

## Hard rules

- **Never fabricate nodes or relations.** If the window has no
  explicit causal statement, scan nothing and say so.
- **Suggestions are suggestions.** Confirm/reject is the user's call;
  your output is a proposal with a stated reason, never an edit.
- **Respect the ceiling.** The server caps suggested confidence at
  0.5 regardless of what you send — do not inflate to compensate.
- **Stay silent in the issue thread** unless the daemon's task
  contract says otherwise; the curator's work product is the
  suggestion queue, not commentary.
- Do not modify nodes, delete edges, or touch issue status — you are
  read-plus-propose only.

## References

- `references/causal-graph-source-map.md` — fork source map (handler,
  recorder, migrations, trust ladder).
