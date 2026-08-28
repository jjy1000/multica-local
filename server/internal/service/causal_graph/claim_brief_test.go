// Package causalgraph — claim_brief_test.go (0.5.85 P1)
//
// Regression pins for BuildClaimSubgraph and its helpers. DB-less
// where possible (the package's other test files — maintenance_test.go
// — establish the pattern: nil Queries / nil Pool is a safe no-op so
// the helper-only tests run without DATABASE_URL). DB-backed tests
// for the BFS traversal itself live in handler/causal_graph_claim_inject_test.go
// where the existing testPool fixture is available.
package causalgraph

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// uuidFromString converts a hex string to a valid pgtype.UUID for
// the test fixtures. Panics on bad input (test helper, not prod).
func uuidFromString(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

// numericFromFloat builds a valid pgtype.Numeric carrying the
// given float64 (formatted as a decimal string for the pgx scan,
// which only accepts string / int64 / etc — float64 is rejected).
func numericFromFloat(t *testing.T, v float64) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(strconvFloatToString(v)); err != nil {
		t.Fatalf("scan numeric %v: %v", v, err)
	}
	return n
}

// strconvFloatToString formats v with 3 decimals (the test
// fixtures don't need full precision; 3 decimals survives the
// round-trip the assertions care about).
func strconvFloatToString(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}

// TestBuildClaimSubgraph_NilQueriesSilentFallback pins the
// nil-safety contract. A nil Queries argument must return
// ("", nil) — never panic — so handler call sites can blindly
// pipe through without guarding.
func TestBuildClaimSubgraph_NilQueriesSilentFallback(t *testing.T) {
	got, err := BuildClaimSubgraph(context.Background(), nil, pgtype.UUID{}, pgtype.UUID{Valid: true, Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000001")})
	if err != nil {
		t.Fatalf("expected nil error on nil queries, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string on nil queries, got %q", got)
	}
}

// TestBuildClaimSubgraph_InvalidIssueIDSilentFallback pins that
// an invalid (zero) issueID also returns the empty default. The
// daemon passes the issue UUID only when the task is issue-bound;
// chat / autopilot / quick-create paths have no issue to query, so
// the helper must no-op cleanly.
func TestBuildClaimSubgraph_InvalidIssueIDSilentFallback(t *testing.T) {
	q := &db.Queries{} // not nil, but no real DB either
	got, err := BuildClaimSubgraph(context.Background(), q, pgtype.UUID{}, pgtype.UUID{})
	if err != nil {
		t.Fatalf("expected nil error on invalid issueID, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string on invalid issueID, got %q", got)
	}
}

// TestBuildClaimSubgraph_CancelledContextSilentFallback pins that
// a pre-cancelled context returns the empty default without
// hitting the DB. Mirrors the silent-fallback contract for the
// 200ms deadline path.
func TestBuildClaimSubgraph_CancelledContextSilentFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	q := &db.Queries{}
	got, err := BuildClaimSubgraph(ctx, q, pgtype.UUID{}, pgtype.UUID{Valid: true, Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000001")})
	if err != nil {
		t.Fatalf("expected nil error on cancelled context, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string on cancelled context, got %q", got)
	}
}

// TestFilterAndRankClaimNodes_DropsAssumptionEvidence pins the
// node-type noise filter from the audit. Assumption / evidence
// nodes are dropped; every other type survives.
func TestFilterAndRankClaimNodes_DropsAssumptionEvidence(t *testing.T) {
	nodes := []db.CausalNode{
		newClaimTestNode("decision", "design decision", time.Now()),
		newClaimTestNode("assumption", "noise assumption", time.Now()),
		newClaimTestNode("action", "implemented", time.Now()),
		newClaimTestNode("evidence", "noise evidence", time.Now()),
		newClaimTestNode("outcome", "shipped", time.Now()),
		newClaimTestNode("constraint", "issue root", time.Now()),
	}
	got := filterAndRankClaimNodes(nodes)
	if len(got) != 4 {
		t.Fatalf("expected 4 nodes after filter, got %d (%+v)", len(got), got)
	}
	for _, n := range got {
		if n.Type == "assumption" || n.Type == "evidence" {
			t.Errorf("node type %q should have been filtered out", n.Type)
		}
	}
}

// TestFilterAndRankClaimNodes_OrdersByCreatedAtDesc pins the
// rank key. Newer nodes must come first.
func TestFilterAndRankClaimNodes_OrdersByCreatedAtDesc(t *testing.T) {
	now := time.Now()
	nodes := []db.CausalNode{
		newClaimTestNode("action", "oldest", now.Add(-3*time.Hour)),
		newClaimTestNode("action", "middle", now.Add(-2*time.Hour)),
		newClaimTestNode("action", "newest", now.Add(-1*time.Hour)),
	}
	got := filterAndRankClaimNodes(nodes)
	if len(got) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(got))
	}
	if got[0].Label != "newest" || got[1].Label != "middle" || got[2].Label != "oldest" {
		t.Errorf("expected newest→middle→oldest, got %s→%s→%s", got[0].Label, got[1].Label, got[2].Label)
	}
}

// TestFilterAndRankClaimNodes_TopNCap pins the hard cap. With
// 30 nodes fed in, only the top maxClaimBriefNodes survive.
// The newest N must win.
func TestFilterAndRankClaimNodes_TopNCap(t *testing.T) {
	now := time.Now()
	nodes := make([]db.CausalNode, 0, maxClaimBriefNodes+10)
	for i := 0; i < maxClaimBriefNodes+10; i++ {
		// created_at decreases with index — node[0] is newest.
		nodes = append(nodes, newClaimTestNode("action", labelForIndex(i), now.Add(-time.Duration(i)*time.Minute)))
	}
	got := filterAndRankClaimNodes(nodes)
	if len(got) != maxClaimBriefNodes {
		t.Fatalf("expected %d nodes after cap, got %d", maxClaimBriefNodes, len(got))
	}
	// Newest node (index 0) must be first.
	if got[0].Label != labelForIndex(0) {
		t.Errorf("expected newest node first, got %q", got[0].Label)
	}
}

// TestFilterAndRankClaimEdges_DropsBlocksContradicts pins the
// edge-type noise filter. blocks / contradicts edges are dropped;
// every other type survives.
func TestFilterAndRankClaimEdges_DropsBlocksContradicts(t *testing.T) {
	edges := []db.CausalEdge{
		newClaimTestEdge("causes", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("blocks", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("enables", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("contradicts", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("depends_on", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("supports", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
	}
	got := filterAndRankClaimEdges(edges)
	if len(got) != 4 {
		t.Fatalf("expected 4 edges after filter, got %d", len(got))
	}
	for _, e := range got {
		if e.Type == "blocks" || e.Type == "contradicts" {
			t.Errorf("edge type %q should have been filtered out", e.Type)
		}
	}
}

// TestFilterAndRankClaimEdges_ConfidenceFloor pins the confidence
// floor. Edges with confidence < 0.6 are dropped; edges with
// confidence >= 0.6 OR NULL confidence survive (NULL counts as 1.0).
func TestFilterAndRankClaimEdges_ConfidenceFloor(t *testing.T) {
	edges := []db.CausalEdge{
		newClaimTestEdge("causes", numericOrNil(t, 0.3), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("causes", numericOrNil(t, 0.59), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("causes", numericOrNil(t, 0.6), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("causes", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
		newClaimTestEdge("causes", pgtype.Numeric{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}),
	}
	got := filterAndRankClaimEdges(edges)
	if len(got) != 3 {
		t.Fatalf("expected 3 edges past confidence floor, got %d (%+v)", len(got), got)
	}
}

// TestFilterAndRankClaimEdges_RanksByConfidenceThenCreatedAt pins
// the rank key: confidence DESC then created_at DESC. Higher-
// confidence edges win; ties broken by recency.
func TestFilterAndRankClaimEdges_RanksByConfidenceThenCreatedAt(t *testing.T) {
	now := time.Now()
	edges := []db.CausalEdge{
		newClaimTestEdge("causes", numericOrNil(t, 0.7), pgtype.UUID{}, pgtype.UUID{}, now.Add(-4*time.Hour)),
		newClaimTestEdge("causes", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, now.Add(-2*time.Hour)),
		newClaimTestEdge("causes", numericOrNil(t, 0.9), pgtype.UUID{}, pgtype.UUID{}, now.Add(-1*time.Hour)), // same conf, newer
		newClaimTestEdge("causes", numericOrNil(t, 0.8), pgtype.UUID{}, pgtype.UUID{}, now.Add(-30*time.Minute)),
	}
	got := filterAndRankClaimEdges(edges)
	if len(got) != 4 {
		t.Fatalf("expected 4 edges, got %d", len(got))
	}
	// Order: 0.9/newest, 0.9/older, 0.8/newest, 0.7/oldest.
	if !edgeConfEq(got[0].Confidence, 0.9) || !edgeConfEq(got[1].Confidence, 0.9) {
		t.Errorf("expected first two edges to have 0.9 confidence, got %v / %v", got[0].Confidence, got[1].Confidence)
	}
	if !got[0].CreatedAt.Time.After(got[1].CreatedAt.Time) {
		t.Errorf("expected newer 0.9 edge first")
	}
}

// TestRenderClaimBriefMarkdown_EmptyNodes pins the empty-input
// short-circuit.
func TestRenderClaimBriefMarkdown_EmptyNodes(t *testing.T) {
	got := renderClaimBriefMarkdown(nil, nil, nil)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestRenderClaimBriefMarkdown_HeadingAndNodeList pins the
// markdown shape. The section starts with the literal heading
// from the audit, then the node list, then optionally the edge
// list.
func TestRenderClaimBriefMarkdown_HeadingAndNodeList(t *testing.T) {
	from := uuidFromString(t, "11111111-1111-1111-1111-111111111111")
	to := uuidFromString(t, "22222222-2222-2222-2222-222222222222")
	nodes := []db.CausalNode{
		{
			ID:             from,
			WorkspaceID:    uuidFromStringForTest("ws-test"),
			Type:           "action",
			Label:          "shipped fix",
			Status:         "active",
			CreatedAt:      pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
			LastObservedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
		{
			ID:             to,
			WorkspaceID:    uuidFromStringForTest("ws-test"),
			Type:           "outcome",
			Label:          "tests pass",
			Status:         "active",
			CreatedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
			LastObservedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}
	allNodes := map[pgtype.UUID]db.CausalNode{
		from: nodes[0],
		to:   nodes[1],
	}
	edges := []db.CausalEdge{
		newClaimTestEdge("causes", pgtype.Numeric{}, from, to, time.Time{}),
	}
	got := renderClaimBriefMarkdown(nodes, edges, allNodes)

	if !strings.HasPrefix(got, "## Prior Causal Context (read-only)") {
		t.Errorf("missing audit-mandated heading, got: %q", got)
	}
	if !strings.Contains(got, "### Nodes") {
		t.Errorf("missing ### Nodes section, got: %q", got)
	}
	if !strings.Contains(got, "[action]") || !strings.Contains(got, "[outcome]") {
		t.Errorf("node types missing in rendered section, got: %q", got)
	}
	if !strings.Contains(got, "### Edges") {
		t.Errorf("missing ### Edges section, got: %q", got)
	}
	if !strings.Contains(got, "action:shipped fix --causes--> outcome:tests pass") {
		t.Errorf("edge line missing or malformed, got: %q", got)
	}
}

// TestRenderClaimBriefMarkdown_HardCapTruncatesOldestEdges pins
// the 16 000-char hard cap. With 500 edges fed in, the tail of
// the section carries the "…(truncated, N more edges)" suffix.
func TestRenderClaimBriefMarkdown_HardCapTruncatesOldestEdges(t *testing.T) {
	// Build a node + edge set where the rendered section blows
	// past maxClaimBriefChars. Each self-loop edge is ~70 chars
	// (type:label × 2 + arrow + type + newline), so 500 edges
	// ≈ 35 000 chars — well past the 16 000-char cap.
	nodes := []db.CausalNode{
		newClaimTestNode("action", "single node", time.Now()),
	}
	allNodes := map[pgtype.UUID]db.CausalNode{
		nodes[0].ID: nodes[0],
	}
	var edges []db.CausalEdge
	for i := 0; i < 500; i++ {
		edges = append(edges, newClaimTestEdge("causes", pgtype.Numeric{}, nodes[0].ID, nodes[0].ID, time.Time{}))
	}
	got := renderClaimBriefMarkdown(nodes, edges, allNodes)
	if len(got) <= maxClaimBriefChars {
		t.Fatalf("expected rendered section to exceed %d bytes, got %d", maxClaimBriefChars, len(got))
	}
	if !strings.Contains(got, "…(truncated,") {
		t.Errorf("expected truncation suffix when over cap, got: %q", got)
	}
}

// TestRenderClaimBriefMarkdown_UnknownEndpointLabel pins the
// edge-renderer contract for edges whose endpoint is not in the
// rendered (top-20) node slice. The audit verdict was "render
// 'unknown' rather than dropping the edge".
func TestRenderClaimBriefMarkdown_UnknownEndpointLabel(t *testing.T) {
	nodes := []db.CausalNode{
		newClaimTestNode("action", "rendered node", time.Now()),
	}
	allNodes := map[pgtype.UUID]db.CausalNode{
		nodes[0].ID: nodes[0],
	}
	danglingFrom := uuidFromString(t, "33333333-3333-3333-3333-333333333333")
	danglingTo := uuidFromString(t, "44444444-4444-4444-4444-444444444444")
	edges := []db.CausalEdge{
		newClaimTestEdge("causes", pgtype.Numeric{}, danglingFrom, danglingTo, time.Time{}),
	}
	got := renderClaimBriefMarkdown(nodes, edges, allNodes)
	if !strings.Contains(got, "unknown --causes--> unknown") {
		t.Errorf("expected both endpoints to render as 'unknown', got: %q", got)
	}
}

// TestConfidenceForSort_NullConfidenceIsOne pins the NULL-confidence
// semantics: a NULL confidence counts as 1.0 (the audit's
// "Tier A recorders don't write confidence, so the floor would
// otherwise zero out the entire recorders-only subgraph" verdict).
func TestConfidenceForSort_NullConfidenceIsOne(t *testing.T) {
	got := confidenceForSort(pgtype.Numeric{})
	if got != 1.0 {
		t.Errorf("NULL confidence should map to 1.0, got %v", got)
	}
}

// TestTruncateBriefLabel_PreservesShortLabels pins that labels
// ≤120 chars are returned unchanged.
func TestTruncateBriefLabel_PreservesShortLabels(t *testing.T) {
	in := "short label"
	if got := truncateBriefLabel(in); got != in {
		t.Errorf("short label should be unchanged, got %q", got)
	}
}

// TestTruncateBriefLabel_TruncatesLongLabels pins that labels
// >120 chars get the 120-char slice plus the ellipsis. The cap
// is BYTES (matches Go string slicing), not runes — a 120-char
// ASCII input gets 120 bytes + 3-byte ellipsis = 123 bytes.
func TestTruncateBriefLabel_TruncatesLongLabels(t *testing.T) {
	in := strings.Repeat("a", 200)
	got := truncateBriefLabel(in)
	if len(got) != 123 { // 120 bytes + 3-byte "…"
		t.Errorf("expected 123 bytes (120 + 3-byte ellipsis), got %d (%q)", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

// ── fixture builders ──────────────────────────────────────────

// newClaimTestNode builds a CausalNode fixture with only the
// fields the filter/render helpers actually read. The CreatedAt
// timestamps drive the rank order so each test pins the expected
// ordering.
func newClaimTestNode(typ, label string, createdAt time.Time) db.CausalNode {
	return db.CausalNode{
		ID:             uuidFromStringForTest(label),
		WorkspaceID:    uuidFromStringForTest("ws-" + label),
		Type:           typ,
		Label:          label,
		Status:         "active",
		CreatedAt:      pgtype.Timestamptz{Time: createdAt, Valid: true},
		LastObservedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	}
}

// newClaimTestEdge builds a CausalEdge fixture with only the
// fields the filter/render helpers actually read. from / to are
// optional; pass pgtype.UUID{} (zero value) to leave the endpoint
// unset. createdAt defaults to time.Now() when zero is passed so
// callers don't have to construct a sentinel timestamp.
func newClaimTestEdge(typ string, confidence pgtype.Numeric, from, to pgtype.UUID, createdAt time.Time) db.CausalEdge {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return db.CausalEdge{
		ID:          uuidFromStringForTest("edge-" + typ),
		WorkspaceID: uuidFromStringForTest("ws-edge"),
		Type:        typ,
		Status:      "active",
		Confidence:  confidence,
		FromNodeID:  from,
		ToNodeID:    to,
		CreatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
	}
}

// uuidFromStringForTest wraps uuid.New() in a label-stable way so
// each fixture row gets a deterministic UUID. Two rows with the
// same label collide — tests that need distinct IDs should pass
// through uuidFromString(t, hex).
func uuidFromStringForTest(label string) pgtype.UUID {
	// Seed a stable v5 UUID off the label so each fixture row
	// has a deterministic ID. The hash itself is incidental —
	// the helpers only read Type / Label / Status / Confidence /
	// CreatedAt / FromNodeID / ToNodeID.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(label))
	return pgtype.UUID{Bytes: id, Valid: true}
}

// numericOrNil builds a valid pgtype.Numeric carrying v, or a
// zero-value (invalid) Numeric when v is zero. Mirrors the
// "NULL confidence counts as 1.0" test pin.
func numericOrNil(t *testing.T, v float64) pgtype.Numeric {
	t.Helper()
	if v == 0 {
		return pgtype.Numeric{}
	}
	return numericFromFloat(t, v)
}

// edgeConfEq returns true when the pgtype.Numeric carries the
// given float64 (within a 0.001 epsilon for the float round-trip).
func edgeConfEq(n pgtype.Numeric, want float64) bool {
	if !n.Valid {
		return false
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return false
	}
	diff := f.Float64 - want
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.001
}

// labelForIndex is a small deterministic label helper so the
// top-N cap test can pin which labels survived.
func labelForIndex(i int) string {
	return "n" + string(rune('A'+i%26)) + string(rune('0'+i/26))
}
