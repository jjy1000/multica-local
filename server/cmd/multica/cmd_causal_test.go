package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderCausalSubgraphText_AgentAttribution pins the 0.5.119
// on-demand trace dialect: type:label node lines with the recorder's
// "(by <agent>)" attribution suffix, typed edge lines, and the honest
// empty-graph message.
func TestRenderCausalSubgraphText_AgentAttribution(t *testing.T) {
	payload := struct {
		IssueID string              `json:"issue_id"`
		Depth   int                 `json:"depth"`
		Nodes   []causalNodePayload `json:"nodes"`
		Edges   []causalEdgePayload `json:"edges"`
	}{
		IssueID: "abc-123",
		Depth:   2,
		Nodes: []causalNodePayload{
			{ID: "n1", Type: "constraint", Label: "root issue"},
			{ID: "n2", Type: "action", Label: "ran the analysis", Metadata: json.RawMessage(`{"agent":"research"}`)},
			{ID: "n3", Type: "outcome", Label: "report delivered"},
		},
		Edges: []causalEdgePayload{
			{FromNodeID: "n1", ToNodeID: "n2", Type: "enables"},
			{FromNodeID: "n2", ToNodeID: "n3", Type: "causes", Confidence: floatPtr(0.5)},
		},
	}

	out, err := captureStdout(t, func() error { renderCausalSubgraphText(payload); return nil })
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, want := range []string{
		"## Causal Trace (issue abc-123, depth 2)",
		"- constraint:root issue",
		"- action:ran the analysis (by research)",
		"- outcome:report delivered",
		"- constraint:root issue --enables--> action:ran the analysis (by research)",
		"- action:ran the analysis (by research) --causes--> outcome:report delivered (conf=0.50)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered trace missing %q, got:\n%s", want, out)
		}
	}
}

// TestRenderCausalSubgraphText_Empty pins the empty-graph message — an
// agent querying an issue with no recorded trace gets guidance, not an
// empty screen.
func TestRenderCausalSubgraphText_Empty(t *testing.T) {
	payload := struct {
		IssueID string              `json:"issue_id"`
		Depth   int                 `json:"depth"`
		Nodes   []causalNodePayload `json:"nodes"`
		Edges   []causalEdgePayload `json:"edges"`
	}{IssueID: "abc-123", Depth: 2}

	out, err := captureStdout(t, func() error { renderCausalSubgraphText(payload); return nil })
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "No causal nodes recorded") {
		t.Errorf("empty-graph message missing, got: %q", out)
	}
}

func floatPtr(v float64) *float64 { return &v }
