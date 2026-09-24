package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// ---------------------------------------------------------------------------
// causal — 0.5.119 知识图谱决策追溯自动系统 (causal_graph lab) consumption
// surface for agents.
//
// The graph is recorded automatically (Tier A task-seam hooks: enqueue /
// complete / sub-issue split, all agent-stamped) and evolved nightly
// (Tier D LLM proposals behind the human-confirm gate). Until 0.5.119
// the only agent-facing read path was the passive claim-time briefing
// injected by the daemon; this verb lets ANY agent — an optimizer
// auditing another agent's decision trail, a historian reconstructing a
// task fission — pull the trace on demand.
//
// `multica causal subgraph --issue <ref>` walks the active causal graph
// BFS-style around the issue and prints the trace as markdown (nodes
// with agent attribution + typed edges). `--output json` returns the
// raw endpoint shape for programmatic consumers.
// ---------------------------------------------------------------------------

var causalCmd = &cobra.Command{
	Use:   "causal",
	Short: "Query the decision-traceability knowledge graph (Labs: causal_graph)",
}

var causalSubgraphCmd = &cobra.Command{
	Use:   "subgraph --issue <id-or-key>",
	Short: "Trace the causal subgraph around an issue (nodes + typed edges)",
	RunE:  runCausalSubgraph,
}

var causalSubgraphIssue string
var causalSubgraphDepth int

func init() {
	causalSubgraphCmd.Flags().StringVar(&causalSubgraphIssue, "issue", "", "Issue id, key (e.g. JYF-123), or unambiguous id prefix")
	causalSubgraphCmd.Flags().IntVar(&causalSubgraphDepth, "depth", 2, "BFS depth 1-4 (default 2)")
	_ = causalSubgraphCmd.MarkFlagRequired("issue")
	causalCmd.AddCommand(causalSubgraphCmd)
}

type causalNodePayload struct {
	ID        string          `json:"id"`
	IssueID   *string         `json:"issue_id"`
	Type      string          `json:"type"`
	Label     string          `json:"label"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt string          `json:"created_at"`
	Status    string          `json:"status"`
}

type causalEdgePayload struct {
	FromNodeID string   `json:"from_node_id"`
	ToNodeID   string   `json:"to_node_id"`
	Type       string   `json:"type"`
	Confidence *float64 `json:"confidence"`
	Status     string   `json:"status"`
}

func runCausalSubgraph(cmd *cobra.Command, args []string) error {
	if causalSubgraphDepth < 1 || causalSubgraphDepth > 4 {
		return fmt.Errorf("--depth must be between 1 and 4, got %d", causalSubgraphDepth)
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, causalSubgraphIssue)
	if err != nil {
		return fmt.Errorf("resolve issue: %w", err)
	}

	path := "/api/causal-graph/subgraph?issue_id=" + url.PathEscape(issueRef.ID) +
		"&depth=" + strconv.Itoa(causalSubgraphDepth)
	var result struct {
		IssueID string              `json:"issue_id"`
		Depth   int                 `json:"depth"`
		Nodes   []causalNodePayload `json:"nodes"`
		Edges   []causalEdgePayload `json:"edges"`
	}
	if err := client.GetJSON(ctx, path, &result); err != nil {
		return fmt.Errorf("query causal subgraph: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}

	renderCausalSubgraphText(result)
	return nil
}

// renderCausalSubgraphText prints the agent-facing markdown trace. The
// shape mirrors the claim-time brief (claim_brief.go) so agents see one
// dialect in both the passive and the on-demand path: type:label node
// lines with agent attribution, then typed edges by label.
func renderCausalSubgraphText(r struct {
	IssueID string              `json:"issue_id"`
	Depth   int                 `json:"depth"`
	Nodes   []causalNodePayload `json:"nodes"`
	Edges   []causalEdgePayload `json:"edges"`
}) {
	if len(r.Nodes) == 0 {
		fmt.Println("No causal nodes recorded for this issue yet. The graph fills in as tasks enqueue, complete, and split on it.")
		return
	}

	labels := make(map[string]string, len(r.Nodes))
	fmt.Printf("## Causal Trace (issue %s, depth %d)\n\n", r.IssueID, r.Depth)
	fmt.Printf("%d node(s) recorded by prior runs. Treat them as ground truth — do not redo work that already produced a recorded outcome.\n\n### Nodes\n", len(r.Nodes))
	for _, n := range r.Nodes {
		line := n.Type + ":" + truncateCausalLabel(n.Label, 120)
		if agent := causalMetadataAgent(n.Metadata); agent != "" {
			line += " (by " + agent + ")"
		}
		labels[n.ID] = line
		fmt.Println("- " + line)
	}

	if len(r.Edges) == 0 {
		return
	}
	fmt.Println("\n### Edges")
	for _, e := range r.Edges {
		from := labels[e.FromNodeID]
		if from == "" {
			from = e.FromNodeID
		}
		to := labels[e.ToNodeID]
		if to == "" {
			to = e.ToNodeID
		}
		line := fmt.Sprintf("- %s --%s--> %s", from, e.Type, to)
		if e.Confidence != nil && *e.Confidence < 1.0 {
			line += fmt.Sprintf(" (conf=%.2f)", *e.Confidence)
		}
		fmt.Println(line)
	}
}

// causalMetadataAgent extracts the agent display name from a node's
// metadata JSON (stamped by the recorder since 0.5.119).
func causalMetadataAgent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if s, ok := m["agent"].(string); ok {
		return s
	}
	return ""
}

func truncateCausalLabel(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
