// Package causalgraph — claim-time briefing read path (0.5.85 P1).
//
// Until 0.5.85 the causal graph was write-only: Tier A recorders
// (recorder.go) + Tier B semantica mirrors + Tier D curator/evolver
// suggestions all land on the graph, but nothing on the read side
// lets the working agent see what is already known about its issue.
// That gap is the "agent cannot see the graph" line in the 0.5.83
// post-ship audit (cooperation ≈ 40-45% with read side = 0%).
//
// BuildClaimSubgraph closes the gap for the daemon-claim hot path:
// when a task is claimed for an issue, the read path queries the
// active subgraph (BFS up to depth 2 from the issue's nodes), filters
// it down to high-signal Tier A/B edges, and returns a compact
// markdown section the daemon injects into the agent's
// Instructions. Agents working on the issue then see "## Prior Causal
// Context (read-only)" with the actions, outcomes, decisions, and
// constraints already on the graph instead of rediscovering them.
//
// Lifecycle: 200ms ctx.WithTimeout parent deadline. Silent fallback
// on every error path — a broken causal read must NEVER block the
// claim hot path, so every non-nil error returns ("", nil) and the
// caller concatenates an empty string. The flag gate lives at the
// caller (daemon.go ClaimTaskByRuntime → experimental.DefaultFor) so
// off-flag installs skip the call entirely and pay zero overhead.
//
// Filter ladder (applied post-BFS in Go; sqlc has no combined query
// that covers all four):
//
//   - status='active' on both nodes and edges (the sqlc queries
//     ListCausalNodesByIssue / ListActiveEdgesTouching already filter
//     on status='active' server-side; ListCausalNodesByIDs does NOT,
//     so we re-filter here to keep the slice honest).
//   - node type ∉ {'assumption','evidence'} (first-cut noise filter
//     from the audit; constraint / decision / action / outcome stay).
//   - edge type ∉ {'blocks','contradicts'} (audit noise filter;
//     causes / supports / depends_on / enables stay).
//   - edge confidence ≥ 0.6 OR NULL (NULL counts as 1.0; Tier A
//     recorders don't set confidence, so a recorders-only graph
//     would otherwise vanish).
//   - top-20 nodes (by confidence DESC, created_at DESC) + top-20
//     edges (by confidence DESC, created_at DESC) post-filter.
//
// Hard cap: 16 000 chars ≈ 4 000 tokens. Beyond the cap the renderer
// drops the oldest edges first (lowest information value) and
// appends a "…(truncated, N more edges)" tail so the agent knows
// the slice is incomplete. The cap is the single riskiest design
// knob — measure on real data before locking it.
package causalgraph

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Brief-formatter knobs (audit-recommended starting values).
const (
	// claimBriefDepth caps the BFS hops from the issue's seed
	// nodes. The audit asks for ≤2 so a depth-2 slice from an
	// issue's outcome nodes still reaches the actions that
	// enabled them, without straying into the full workspace
	// graph.
	claimBriefDepth = 2

	// maxClaimBriefNodes / maxClaimBriefEdges bound the slice size
	// injected into the prompt. 20/20 mirrors the audit's "top-20"
	// recommendation; a long-lived issue could otherwise balloon
	// the section past the 4000-token cap.
	maxClaimBriefNodes = 20
	maxClaimBriefEdges = 20

	// minClaimEdgeConfidence is the audit-recommended confidence
	// floor. NULL confidence counts as 1.0 (Tier A recorders do
	// not write confidence, so the floor would otherwise zero out
	// the entire recorders-only subgraph).
	minClaimEdgeConfidence = 0.6

	// maxClaimBriefChars caps the rendered section. 16 000 chars
	// ≈ 4 000 tokens is the audit recommendation; the rendered
	// heading + node list + edge list should fit comfortably
	// inside one LLM prompt section. Beyond the cap we drop the
	// oldest edges first and append a "…(truncated)" tail.
	maxClaimBriefChars = 16000

	// claimBriefTimeout bounds the worst-case latency of the
	// read path on a slow DB. Anything inside the budget ships;
	// anything past it returns ("", nil) silently. The deadline
	// rides on the claim HTTP request scope so claim cancellation
	// propagates to the read path.
	claimBriefTimeout = 200 * time.Millisecond
)

// excludedClaimNodeTypes are dropped from the rendered slice.
// Assumption / evidence are noisy first-cut — they are real graph
// data but rarely load-bearing for a working agent, and including
// them tends to dominate the section budget on long-lived issues.
var excludedClaimNodeTypes = map[string]struct{}{
	"assumption": {},
	"evidence":   {},
}

// excludedClaimEdgeTypes are dropped from the rendered slice.
// blocks / contradicts are typically signals the agent should
// infer from the inverse relation (depends_on) rather than carry
// as a separate edge.
var excludedClaimEdgeTypes = map[string]struct{}{
	"blocks":      {},
	"contradicts": {},
}

// BuildClaimSubgraph returns the markdown briefing section for the
// given issue's active causal subgraph, or "" on any error path.
// The function is the read-side counterpart to Recorder.RefreshForIssue
// and is safe to call on every daemon claim — broken reads are
// silent fallbacks (the audit's "never block the claim" law).
//
// Signature mirrors Recorder's read-side: db.Queries is sufficient,
// no Pool required (the BFS rides the same sqlc surface as the
// HTTP handler's causalSubgraph endpoint). The workspaceID parameter
// is reserved for future cross-workspace defense-in-depth checks
// (today every node fetched is already workspace-scoped via the
// issue_id seed); nil is allowed and treated as "skip the check".
//
// Caller contract: the daemon.go ClaimTaskByRuntime path consults
// experimental.DefaultFor("causal_graph") BEFORE invoking this
// function so off-flag installs pay zero overhead. BuildClaimSubgraph
// does NOT consult the flag itself — defence-in-depth noise the
// audit explicitly rejected.
func BuildClaimSubgraph(ctx context.Context, q *db.Queries, workspaceID, issueID pgtype.UUID) (string, error) {
	// Nil-safety: tests and minimal builds may pass nil; a nil
	// Queries means the helper cannot run, so return the empty
	// default rather than panic.
	if q == nil {
		return "", nil
	}
	if !issueID.Valid {
		return "", nil
	}

	// 200ms budget. Wrap a child of the parent so claim
	// cancellation propagates — we don't want to outlive the
	// request scope on a slow DB.
	timeoutCtx, cancel := context.WithTimeout(ctx, claimBriefTimeout)
	defer cancel()

	nodes, edges, err := bfsClaimSubgraph(timeoutCtx, q, issueID)
	if err != nil {
		// WRN — silent to the caller (empty + nil), but the log
		// lets ops correlate "claim was slow + no causal
		// briefing" without polluting the response shape.
		slog.Warn("claim causal subgraph read failed",
			"issue_id", util.UUIDToString(issueID),
			"workspace_id", util.UUIDToString(workspaceID),
			"error", err,
		)
		return "", nil
	}
	if len(nodes) == 0 {
		return "", nil
	}

	// Build a node lookup so the edge renderer can resolve
	// endpoints to labels without re-querying.
	nodesByID := make(map[pgtype.UUID]db.CausalNode, len(nodes))
	for _, n := range nodes {
		nodesByID[n.ID] = n
	}

	rankedNodes := filterAndRankClaimNodes(nodes)
	rankedEdges := filterAndRankClaimEdges(edges)

	if len(rankedNodes) == 0 {
		return "", nil
	}

	return renderClaimBriefMarkdown(rankedNodes, rankedEdges, nodesByID), nil
}

// bfsClaimSubgraph walks the active causal graph up to claimBriefDepth
// hops from the issue's seed nodes. Mirrors the BFS shape of
// handler.causalSubgraph (undirected, status='active' via sqlc
// filter) but caps the depth at claimBriefDepth and re-filters
// nodes by status to defend against ListCausalNodesByIDs (which
// does NOT pre-filter).
func bfsClaimSubgraph(ctx context.Context, q *db.Queries, issueID pgtype.UUID) ([]db.CausalNode, []db.CausalEdge, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	seed, err := q.ListCausalNodesByIssue(ctx, issueID)
	if err != nil {
		return nil, nil, fmt.Errorf("seed subgraph: %w", err)
	}
	if len(seed) == 0 {
		return nil, nil, nil
	}

	visited := make(map[pgtype.UUID]struct{}, len(seed))
	frontier := make([]pgtype.UUID, 0, len(seed))
	nodesByID := make(map[pgtype.UUID]db.CausalNode, len(seed))
	for _, n := range seed {
		if n.Status != "active" {
			continue
		}
		visited[n.ID] = struct{}{}
		frontier = append(frontier, n.ID)
		nodesByID[n.ID] = n
	}
	if len(frontier) == 0 {
		return nil, nil, nil
	}

	edgesOut := make(map[pgtype.UUID]db.CausalEdge)
	for d := 0; d < claimBriefDepth && len(frontier) > 0; d++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		touching, err := q.ListActiveEdgesTouching(ctx, frontier)
		if err != nil {
			return nil, nil, fmt.Errorf("expand depth %d: %w", d, err)
		}
		var nextIDs []pgtype.UUID
		for _, e := range touching {
			if e.Status != "active" {
				continue
			}
			edgesOut[e.ID] = e
			for _, endpoint := range []pgtype.UUID{e.FromNodeID, e.ToNodeID} {
				if _, ok := visited[endpoint]; ok {
					continue
				}
				visited[endpoint] = struct{}{}
				nextIDs = append(nextIDs, endpoint)
			}
		}
		frontier = frontier[:0]
		if len(nextIDs) > 0 {
			neighborNodes, err := q.ListCausalNodesByIDs(ctx, nextIDs)
			if err != nil {
				return nil, nil, fmt.Errorf("expand depth %d materialise: %w", d, err)
			}
			for _, n := range neighborNodes {
				if n.Status != "active" {
					continue
				}
				nodesByID[n.ID] = n
				frontier = append(frontier, n.ID)
			}
		}
	}

	nodes := make([]db.CausalNode, 0, len(nodesByID))
	for _, n := range nodesByID {
		nodes = append(nodes, n)
	}
	edges := make([]db.CausalEdge, 0, len(edgesOut))
	for _, e := range edgesOut {
		edges = append(edges, e)
	}
	return nodes, edges, nil
}

// filterAndRankClaimNodes drops excluded node types (assumption /
// evidence) and ranks by created_at DESC. Nodes carry no confidence
// column (CausalNode.Confidence does not exist — only edges do),
// so the rank key is creation recency, which mirrors the audit's
// "what is most recent first" intuition for the briefing. The
// top-N slice is what the renderer consumes; the function never
// reads the DB.
func filterAndRankClaimNodes(nodes []db.CausalNode) []db.CausalNode {
	filtered := make([]db.CausalNode, 0, len(nodes))
	for _, n := range nodes {
		if _, excluded := excludedClaimNodeTypes[n.Type]; excluded {
			continue
		}
		filtered = append(filtered, n)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.Time.After(filtered[j].CreatedAt.Time)
	})
	if len(filtered) > maxClaimBriefNodes {
		filtered = filtered[:maxClaimBriefNodes]
	}
	return filtered
}

// filterAndRankClaimEdges drops excluded edge types (blocks /
// contradicts) and edges below the confidence floor, then ranks
// by confidence DESC then created_at DESC. The top-N slice is
// what the renderer consumes; the function never reads the DB.
//
// Edges whose endpoints are NOT in the rendered (top-20) node
// slice still render — the renderer prints "unknown" for them
// rather than dropping the edge, because an edge to a dropped
// node is still informative and pointing at an unknown node is
// honest about the slice limit (the audit's "first cut" verdict
// on noise filter semantics).
func filterAndRankClaimEdges(edges []db.CausalEdge) []db.CausalEdge {
	filtered := make([]db.CausalEdge, 0, len(edges))
	for _, e := range edges {
		if _, excluded := excludedClaimEdgeTypes[e.Type]; excluded {
			continue
		}
		if e.Confidence.Valid {
			conf := confidenceForSort(e.Confidence)
			if conf < minClaimEdgeConfidence {
				continue
			}
		}
		filtered = append(filtered, e)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		ci, cj := confidenceForSort(filtered[i].Confidence), confidenceForSort(filtered[j].Confidence)
		if ci != cj {
			return ci > cj
		}
		return filtered[i].CreatedAt.Time.After(filtered[j].CreatedAt.Time)
	})
	if len(filtered) > maxClaimBriefEdges {
		filtered = filtered[:maxClaimBriefEdges]
	}
	return filtered
}

// confidenceForSort returns the confidence as a float64 sortable
// across valid + invalid (NULL) values. NULL confidence counts as
// 1.0 so the Tier A recorder-only slice ranks ahead of low-conf
// Tier D suggestions — the audit's confidence-floor design verdict.
func confidenceForSort(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 1.0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 1.0
	}
	return f.Float64
}

// renderClaimBriefMarkdown assembles the final markdown section
// and applies the 16 000-char hard cap. Beyond the cap we drop
// the oldest edges (lowest information value) and append a
// "…(truncated, N more edges)" tail.
func renderClaimBriefMarkdown(nodes []db.CausalNode, edges []db.CausalEdge, allNodes map[pgtype.UUID]db.CausalNode) string {
	if len(nodes) == 0 {
		return ""
	}

	// Materialise the node slice the edge renderer can resolve.
	// Edges whose endpoints are NOT in this slice render with
	// an "unknown" label rather than dropping them — the slice
	// already passed the top-20 cap so we keep the most
	// informative edges visible.
	renderableNodes := make(map[pgtype.UUID]struct{}, len(nodes))
	for _, n := range nodes {
		renderableNodes[n.ID] = struct{}{}
	}

	var sb strings.Builder
	sb.WriteString("## Prior Causal Context (read-only)\n\n")
	sb.WriteString("This issue already has ")
	fmt.Fprintf(&sb, "%d active causal node(s) and %d edge(s) recorded by prior runs. ", len(nodes), len(edges))
	sb.WriteString("Treat them as ground truth — do not redo work that has already produced a recorded outcome.\n\n")

	sb.WriteString("### Nodes\n")
	for _, n := range nodes {
		fmt.Fprintf(&sb, "- [%s] %s", n.Type, truncateBriefLabel(n.Label))
		if n.Description.Valid && n.Description.String != "" {
			fmt.Fprintf(&sb, " — %s", truncateBriefLabel(n.Description.String))
		}
		sb.WriteString("\n")
	}

	if len(edges) > 0 {
		sb.WriteString("\n### Edges\n")
		dropped := 0
		for _, e := range edges {
			if sb.Len() >= maxClaimBriefChars {
				// Drop the oldest edge from the visible slice
				// (the slice is already sorted by confidence
				// DESC then created_at DESC, so the tail is
				// the least informative).
				dropped++
				continue
			}
			fromLabel := nodeLabel(e.FromNodeID, allNodes, renderableNodes)
			toLabel := nodeLabel(e.ToNodeID, allNodes, renderableNodes)
			fmt.Fprintf(&sb, "- %s --%s--> %s", fromLabel, e.Type, toLabel)
			if conf := confidenceForSort(e.Confidence); e.Confidence.Valid && conf < 1.0 {
				fmt.Fprintf(&sb, " (conf=%.2f)", conf)
			}
			sb.WriteString("\n")
		}
		if dropped > 0 {
			fmt.Fprintf(&sb, "\n…(truncated, %d more edge(s) omitted; raise %d-char cap to see more)\n", dropped, maxClaimBriefChars)
		}
	}

	return sb.String()
}

// nodeLabel resolves an edge endpoint to a "type:label" string for
// the rendered slice. If the endpoint is not in the rendered
// (top-20) node slice we render "unknown" rather than dropping the
// edge — an edge to a dropped node is still informative and
// pointing at an unknown node is honest about the slice limit.
func nodeLabel(id pgtype.UUID, allNodes map[pgtype.UUID]db.CausalNode, renderable map[pgtype.UUID]struct{}) string {
	if _, ok := renderable[id]; !ok {
		return "unknown"
	}
	n, ok := allNodes[id]
	if !ok {
		return "unknown"
	}
	return n.Type + ":" + truncateBriefLabel(n.Label)
}

// truncateBriefLabel keeps the rendered section well within the
// per-line budget. 120 chars matches recorder.truncateLabel so
// the rendered label matches the stored label length policy.
func truncateBriefLabel(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
