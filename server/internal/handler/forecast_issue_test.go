// Package handler — forecast_issue_test.go (0.3.29+, refactored 0.5.111)
//
// Covers the per-issue Pythia forecast surface after the 0.5.111 async
// refactor: rounds resolution (explicit → natural-language → per-kind
// default), the synthetic envelope contract, the continuation history
// digest, the report comment assembly, the run bus, and the DB-backed
// end-to-end flow (POST → run row → per-round persist → comment
// writeback → SSE stream snapshot).

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestClampIssueForecastRounds covers the explicit-rounds contract: a
// caller explicitly requesting N above the cap gets clamped down to 10.
// (Zero/negative now means "caller didn't say" and is resolved by
// resolveIssueForecastRounds, so the clamp keeps the 3-round initial
// default for that input.)
func TestClampIssueForecastRounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero defaults to 3", 0, 3},
		{"negative defaults to 3", -3, 3},
		{"1 keeps 1", 1, 1},
		{"3 keeps 3", 3, 3},
		{"10 keeps 10", 10, 10},
		{"11 clamps to 10", 11, 10},
		{"1000 clamps to 10", 1000, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clampIssueForecastRounds(tc.in)
			if got != tc.want {
				t.Errorf("clampIssueForecastRounds(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseForecastRoundsFromText pins the Go port of the client-side
// natural-language round parser (forecast-rounds.ts). Tight adjacency:
// "分3轮讨论" / "第一轮" must NOT hijack the count just because the
// keyword appears elsewhere in the text.
func TestParseForecastRoundsFromText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"keyword first 推演5轮", "对纸质方案进行推演5轮的验证", 5},
		{"keyword with colon 推演：3轮", "推演：3轮", 3},
		{"count first 3轮推演", "请3轮推演这个方案", 3},
		{"模拟 8 轮", "模拟 8 轮", 8},
		{"预测12轮 clamps to 10", "预测12轮", 10},
		{"no pin", "推演这个方案，分3轮讨论", 0},
		{"ordinal round ignored", "第一轮先做A", 0},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseForecastRoundsFromText(tc.in); got != tc.want {
				t.Errorf("parseForecastRoundsFromText(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveIssueForecastRounds covers the 0.5.111 resolution order:
// explicit caller value wins, then a natural-language pin (variables text
// scanned before the issue text), then the per-kind default — 3 for
// initial runs, 6 for continuations (the user contract: 5-8 to save
// tokens).
func TestResolveIssueForecastRounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		explicit     int
		continuation bool
		texts        []string
		want         int
	}{
		{"explicit wins over continuation default", 4, true, nil, 4},
		{"initial default", 0, false, nil, 3},
		{"continuation default 6", 0, true, nil, 6},
		{"variables pin wins over initial default", 0, false, []string{"重新推演7轮", "标题"}, 7},
		{"issue text pin", 0, false, []string{"", "推演4轮验证纸质方案"}, 4},
		{"explicit above cap clamps", 99, false, nil, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveIssueForecastRounds(tc.explicit, tc.continuation, tc.texts...)
			if got != tc.want {
				t.Errorf("resolveIssueForecastRounds(%d, %v, %v) = %d, want %d",
					tc.explicit, tc.continuation, tc.texts, got, tc.want)
			}
		})
	}
}

// TestQueryOracleIssueSyntheticRelabel pins the 0.5.104 honesty fix:
// when the engine answers 200 with `synthetic: true` (its internal LLM
// bridge call failed and it emitted placeholder narrative), the envelope
// MUST carry lab_source=synthetic_oracle_failover instead of "oracle".
func TestQueryOracleIssueSyntheticRelabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		synthetic  bool
		wantLabSrc string
	}{
		{"real engine answer stays oracle", false, "oracle"},
		{"engine-reported fallback relabels", true, "synthetic_oracle_failover"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"scenario":    "scenario",
					"narrative":   "narrative",
					"probability": 0.61,
					"confidence":  0.55,
					"horizon":     "week",
					"persona":     "strategist",
					"round":       1,
					"synthetic":   tc.synthetic,
				})
			}))
			defer srv.Close()

			env, err := queryOracleIssue(context.Background(), srv.URL, &issueForecastContext{
				IssueID: "00000000-0000-0000-0000-000000000001",
				Title:   "模拟推演方案",
			}, 42, 1, issueRoundOpts{totalRounds: 3})
			if err != nil {
				t.Fatalf("queryOracleIssue returned error: %v", err)
			}
			if env.LabSource != tc.wantLabSrc {
				t.Errorf("LabSource = %q, want %q (synthetic=%v)", env.LabSource, tc.wantLabSrc, tc.synthetic)
			}
		})
	}
}

// TestQueryOracleIssueParsesCouncil pins the 0.5.111 wire addition: an
// engine response carrying base_probability + a council vote sheet lands
// on the envelope untouched, so the panel can render the council view.
func TestQueryOracleIssueParsesCouncil(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The request must carry the continuation contract fields.
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["council"] != true {
			t.Errorf("payload council = %v, want true", body["council"])
		}
		if _, ok := body["history"].([]any); !ok {
			t.Errorf("payload history missing: %v", body["history"])
		}
		w.Header().Set("Content-Type", "application/json")
		consensus := 0.62
		_ = json.NewEncoder(w).Encode(map[string]any{
			"scenario":         "scenario",
			"narrative":        "narrative",
			"probability":      consensus,
			"base_probability": 0.71,
			"confidence":       0.55,
			"horizon":          "week",
			"persona":          "strategist",
			"round":            2,
			"synthetic":        false,
			"council": map[string]any{
				"votes": []map[string]any{
					{"persona": "Strategist", "probability": 0.7, "note": "supply holds"},
					{"persona": "Skeptic", "probability": 0.45, "note": "base rates say no"},
				},
				"consensus": consensus,
				"spread":    0.25,
				"split":     false,
			},
		})
	}))
	defer srv.Close()

	env, err := queryOracleIssue(context.Background(), srv.URL, &issueForecastContext{
		IssueID: "00000000-0000-0000-0000-000000000001",
		Title:   "模拟推演方案",
	}, 42, 2, issueRoundOpts{
		history:     []forecastHistoryRound{{Round: 1, Narrative: "第一轮", Probability: 0.5}},
		totalRounds: 3,
	})
	if err != nil {
		t.Fatalf("queryOracleIssue returned error: %v", err)
	}
	if env.Council == nil {
		t.Fatalf("envelope council is nil, want the parsed vote sheet")
	}
	if len(env.Council.Votes) != 2 {
		t.Errorf("council votes = %d, want 2", len(env.Council.Votes))
	}
	if env.BaseProbability == nil || *env.BaseProbability != 0.71 {
		t.Errorf("base_probability = %v, want 0.71", env.BaseProbability)
	}
}

// TestForecastRunSource covers the 0.3.55 run-level source label that
// lands in pythia_forecast_run.source. Uniform runs keep their
// per-envelope provenance; a run whose rounds came from more than one
// source collapses to "mixed"; an unknown / empty label falls back to
// "synthetic" so the CHECK constraint never sees an unexpected value.
func TestForecastRunSource(t *testing.T) {
	t.Parallel()
	env := func(src string) forecastEnvelope { return forecastEnvelope{LabSource: src} }
	cases := []struct {
		name string
		in   []forecastEnvelope
		want string
	}{
		{"nil defaults to synthetic", nil, "synthetic"},
		{"all oracle", []forecastEnvelope{env("oracle"), env("oracle")}, "oracle"},
		{"all synthetic", []forecastEnvelope{env("synthetic")}, "synthetic"},
		{"all failover", []forecastEnvelope{env("synthetic_oracle_failover"), env("synthetic_oracle_failover")}, "synthetic_oracle_failover"},
		{"oracle then failover is mixed", []forecastEnvelope{env("oracle"), env("synthetic_oracle_failover")}, "mixed"},
		{"unknown label defaults to synthetic", []forecastEnvelope{env("weird")}, "synthetic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := forecastRunSource(tc.in); got != tc.want {
				t.Errorf("forecastRunSource = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSyntheticIssueEnvelopeCarriesIssueContext pins the 0.3.29 contract
// in its 0.5.111 shape: the fallback envelope binds the forecast to the
// issue (issue_id + scenario_context) and keeps the probability in [0,1].
func TestSyntheticIssueEnvelopeCarriesIssueContext(t *testing.T) {
	t.Parallel()

	ifc := &issueForecastContext{
		IssueID:     "11111111-1111-1111-1111-111111111111",
		IssueNumber: "JIA-42",
		Title:       "霍尔木兹海峡流量下降",
		Body:        "过去 7 天油轮 AIS 数据出现 15% 的同向下降趋势。",
		LabSource:   "pythia_oracle",
		WorkspaceID: "22222222-2222-2222-2222-222222222222",
	}
	env, err := syntheticIssueForecast(context.Background(), 42, ifc, 1, issueRoundOpts{totalRounds: 3})
	if err != nil {
		t.Fatalf("syntheticIssueForecast: %v", err)
	}
	if env.IssueID != ifc.IssueID {
		t.Errorf("env.IssueID = %q, want %q", env.IssueID, ifc.IssueID)
	}
	if !strings.Contains(env.ScenarioContext, ifc.Title) {
		t.Errorf("env.ScenarioContext missing title: %q", env.ScenarioContext)
	}
	if env.LabSource == "" {
		t.Errorf("env.LabSource empty")
	}
	if env.Probability < 0 || env.Probability > 1 {
		t.Errorf("probability out of [0,1]: %f", env.Probability)
	}
}

// TestBuildIssueForecastContextTruncatesBody verifies the 280-char
// truncation contract for the body field, so an embedded Issue
// description doesn't blow up the wire frame.
func TestBuildIssueForecastContextTruncatesBody(t *testing.T) {
	t.Parallel()

	longBody := strings.Repeat("A", 600)
	ifc := buildIssueForecastContextForTest("title", longBody, "pythia_oracle")
	if !strings.HasSuffix(ifc.Body, "…") {
		t.Errorf("expected truncated body, got %q", ifc.Body)
	}
	if len(ifc.Body) > 290 {
		// 280 chars + "…" overflow sentinel.
		t.Errorf("body too long: %d bytes", len(ifc.Body))
	}
}

// buildIssueForecastContextForTest is a tiny helper that builds an
// issueForecastContext without going through sqlc. Mirrors
// buildIssueForecastContext's body / lab_source handling so unit
// tests can exercise the truncation contract without a database.
func buildIssueForecastContextForTest(title, body, lab string) *issueForecastContext {
	if len(body) > 280 {
		body = body[:280] + "…"
	}
	return &issueForecastContext{
		IssueID:   "test-id",
		Title:     title,
		Body:      body,
		LabSource: lab,
	}
}

// TestHistoryFromEnvelopes covers the continuation digest: rounds are
// numbered from 1, narratives are rune-capped, and the digest keeps at
// most the LAST 12 rounds.
func TestHistoryFromEnvelopes(t *testing.T) {
	t.Parallel()

	envelopes := make([]forecastEnvelope, 0, 14)
	for i := 0; i < 14; i++ {
		envelopes = append(envelopes, forecastEnvelope{
			Narrative:   strings.Repeat("n", 600),
			Probability: float64(i) / 14,
		})
	}
	hist := historyFromEnvelopes(envelopes)
	if len(hist) != forecastHistoryMaxRounds {
		t.Fatalf("history length = %d, want %d (capped at last 12)", len(hist), forecastHistoryMaxRounds)
	}
	if hist[0].Round != 3 {
		t.Errorf("first kept round = %d, want 3 (14 rounds capped to last 12)", hist[0].Round)
	}
	for i, h := range hist {
		if len([]rune(h.Narrative)) > forecastHistoryNarrativeCap+1 { // +1 for the … sentinel
			t.Errorf("round %d narrative not capped: %d runes", i, len([]rune(h.Narrative)))
		}
	}
	if len(historyFromEnvelopes(nil)) != 0 {
		t.Errorf("nil envelopes should produce an empty digest")
	}
}

// TestPythiaIssueReportContent covers the comment assembly: a synthesized
// report IS the body (with the continuation header + variable echo), and
// the mechanical digest is the fallback. The honesty source note always
// lands.
func TestPythiaIssueReportContent(t *testing.T) {
	t.Parallel()

	ifc := &issueForecastContext{Title: "纸质方案可行性"}
	envelopes := []forecastEnvelope{{Scenario: "s", Narrative: "n", Probability: 0.6, Confidence: 0.5, Persona: "strategist", Horizon: "week"}}

	synth := pythiaIssueReportContent(ifc, envelopes, "oracle", "continuation", "把汇率冲击调到 20%", "## 共识结论\n概率区间 55-65%。")
	for _, want := range []string{"续推", "把汇率冲击调到 20%", "## 共识结论", "数据来源"} {
		if !strings.Contains(synth, want) {
			t.Errorf("synthesized comment missing %q:\n%s", want, synth)
		}
	}
	if strings.Contains(synth, "概率 60%") {
		t.Errorf("synthesized comment should not include the mechanical digest")
	}

	mech := pythiaIssueReportContent(ifc, envelopes, "synthetic", "initial", "", "")
	for _, want := range []string{"轮数：1", "本地回退数据", "非真实推演"} {
		if !strings.Contains(mech, want) {
			t.Errorf("mechanical comment missing %q:\n%s", want, mech)
		}
	}
}

// TestPythiaRunBus covers subscribe/publish fan-out, unsubscribe, and the
// cancel registration. Publishes to a full buffer must drop, not block.
func TestPythiaRunBus(t *testing.T) {
	t.Parallel()

	b := newPythiaRunBus()
	ch, unsub := b.subscribe("run-1")
	b.publish("run-1", pythiaRunEvent{Type: "round", Index: 0})
	ev := <-ch
	if ev.Type != "round" || ev.Index != 0 {
		t.Errorf("got %+v, want round/0", ev)
	}

	// Cancel registration round-trip.
	if b.cancelRun("run-1") {
		t.Errorf("cancelRun should report false with no runner registered")
	}
	called := false
	b.registerCancel("run-1", func() { called = true })
	if !b.cancelRun("run-1") || !called {
		t.Errorf("cancelRun did not invoke the registered cancel")
	}

	unsub()
	unsub() // double-unsubscribe must be a no-op
	b.publish("run-1", pythiaRunEvent{Type: "status", Status: "completed"})
	select {
	case ev := <-ch:
		t.Errorf("received event after unsubscribe: %+v", ev)
	default:
	}
}

// ── DB-backed end-to-end tests ────────────────────────────────────────────

// newForecastTestRequest builds a POST against the forecast surface with
// the handler stashed in the request context (the route group's
// middleware does this in production).
func newForecastTestRequest(method, path string, body any) *http.Request {
	req := newRequest(method, path, body)
	return req.WithContext(context.WithValue(req.Context(), forecastIssueHandlerCtxKey{}, testHandler))
}

// waitForRunTerminal polls pythia_forecast_run until the row leaves
// 'running' (the synthetic-path runner completes in well under a second)
// and returns the final row's status + report_comment_id.
func waitForRunTerminal(t *testing.T, runID string) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var status string
	var commentID *string
	for {
		err := testPool.QueryRow(context.Background(),
			`SELECT status, report_comment_id FROM pythia_forecast_run WHERE id = $1`, runID,
		).Scan(&status, &commentID)
		if err != nil {
			t.Fatalf("load run row: %v", err)
		}
		if status != "running" || time.Now().After(deadline) {
			return status, commentID != nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPythiaForecastStartEndToEnd pins the 0.5.111 async contract end to
// end: POST returns a run_id immediately, the detached runner persists
// the row per round, flips it to completed, and writes the report comment
// (system-author fallback when the leader agent is absent).
func TestPythiaForecastStartEndToEnd(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	issue := createIssueForTest(t, map[string]any{
		"title": "pythia-async-e2e",
	})

	w := httptest.NewRecorder()
	req := newForecastTestRequest("POST", "/api/experimental/pythia-oracle/forecast/issue", map[string]any{
		"issue_id": issue.ID,
		"rounds":   1,
	})
	pythiaIssueForecast(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST forecast: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var start issueForecastStartResponse
	if err := json.NewDecoder(w.Body).Decode(&start); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if start.RunID == "" || start.Status != "running" || start.RunKind != "initial" {
		t.Fatalf("unexpected start response: %+v", start)
	}
	if start.Rounds != 1 {
		t.Errorf("resolved rounds = %d, want 1", start.Rounds)
	}

	status, hasComment := waitForRunTerminal(t, start.RunID)
	if status != "completed" {
		t.Fatalf("run ended with status %q, want completed", status)
	}
	var rounds int
	var envelopes json.RawMessage
	if err := testPool.QueryRow(context.Background(),
		`SELECT rounds, envelopes FROM pythia_forecast_run WHERE id = $1`, start.RunID,
	).Scan(&rounds, &envelopes); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if rounds != 1 {
		t.Errorf("persisted rounds = %d, want 1 (per-round persistence contract)", rounds)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(envelopes, &parsed); err != nil || len(parsed) != 1 {
		t.Errorf("envelopes = %s (err=%v), want exactly 1 round", envelopes, err)
	}
	if !hasComment {
		t.Errorf("report comment writeback did not land (report_comment_id still NULL)")
	}
}

// insertForecastRunForTest inserts a pythia_forecast_run row directly
// (status 'completed', parent NULL) so continuation-validation tests can
// stage parents with/without envelopes without running the engine.
func insertForecastRunForTest(t *testing.T, workspaceID, issueID, envelopes string) (string, pgtype.UUID) {
	t.Helper()
	issueUUID, err := util.ParseUUID(issueID)
	if err != nil {
		t.Fatalf("parse issue id %q: %v", issueID, err)
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		t.Fatalf("parse workspace id: %v", err)
	}
	row, err := testHandler.Queries.CreatePythiaForecastRun(context.Background(), dbpkg.CreatePythiaForecastRunParams{
		WorkspaceID: wsUUID,
		IssueID:     issueUUID,
		Rounds:      0,
		Source:      "synthetic",
		Envelopes:   []byte(envelopes),
		RunKind:     "initial",
		Status:      "completed",
	})
	if err != nil {
		t.Fatalf("insert forecast run: %v", err)
	}
	return util.UUIDToString(row.ID), row.ID
}

// TestPythiaForecastContinuationValidation pins the parent-run gates:
// unknown parent → 404; parent of a DIFFERENT issue → 400; parent with no
// rounds → 400. A valid parent resolves the 6-round continuation default.
func TestPythiaForecastContinuationValidation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	issueA := createIssueForTest(t, map[string]any{"title": "pythia-cont-a"})
	issueB := createIssueForTest(t, map[string]any{"title": "pythia-cont-b"})

	oneEnvelope := `[{"id":"e1","scenario":"s","narrative":"n","probability":0.5,"confidence":0.5,"horizon":"week","persona":"strategist","lab_source":"synthetic"}]`
	runB, _ := insertForecastRunForTest(t, testWorkspaceID, issueB.ID, oneEnvelope)
	runEmpty, _ := insertForecastRunForTest(t, testWorkspaceID, issueA.ID, `[]`)

	cases := []struct {
		name    string
		issueID string
		parent  string
		want    int
	}{
		{"unknown parent", issueA.ID, "00000000-0000-0000-0000-00000000000f", http.StatusNotFound},
		{"parent of another issue", issueA.ID, runB, http.StatusBadRequest},
		{"parent with no rounds", issueA.ID, runEmpty, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newForecastTestRequest("POST", "/api/experimental/pythia-oracle/forecast/issue", map[string]any{
				"issue_id":      tc.issueID,
				"rounds":        1,
				"parent_run_id": tc.parent,
			})
			pythiaIssueForecast(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}

	// Happy path: continuation defaults to 6 rounds and stores lineage +
	// variables.
	parentIDStr, parentUUID := insertForecastRunForTest(t, testWorkspaceID, issueA.ID, oneEnvelope)
	w := httptest.NewRecorder()
	req := newForecastTestRequest("POST", "/api/experimental/pythia-oracle/forecast/issue", map[string]any{
		"issue_id":      issueA.ID,
		"parent_run_id": parentIDStr,
		"variables":     "把汇率冲击调到 20%",
	})
	pythiaIssueForecast(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("continuation POST: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var start issueForecastStartResponse
	if err := json.NewDecoder(w.Body).Decode(&start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if start.RunKind != "continuation" {
		t.Errorf("run_kind = %q, want continuation", start.RunKind)
	}
	if start.Rounds != defaultContinuationRounds {
		t.Errorf("continuation default rounds = %d, want %d", start.Rounds, defaultContinuationRounds)
	}
	var variables string
	var parentID *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT variables, parent_run_id FROM pythia_forecast_run WHERE id = $1`, start.RunID,
	).Scan(&variables, &parentID); err != nil {
		t.Fatalf("load continuation row: %v", err)
	}
	if variables != "把汇率冲击调到 20%" {
		t.Errorf("variables = %q", variables)
	}
	if parentID == nil || *parentID != parentIDStr {
		t.Errorf("parent_run_id = %v, want %s", parentID, parentIDStr)
	}
	_ = parentUUID
}

// TestPythiaRunStreamSnapshotAndTerminal pins the stream contract for a
// FINISHED run: one snapshot frame (meta + envelopes) then a terminal
// status frame, no live tail.
func TestPythiaRunStreamSnapshotAndTerminal(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	issue := createIssueForTest(t, map[string]any{"title": "pythia-stream-e2e"})
	w := httptest.NewRecorder()
	req := newForecastTestRequest("POST", "/api/experimental/pythia-oracle/forecast/issue", map[string]any{
		"issue_id": issue.ID,
		"rounds":   1,
	})
	pythiaIssueForecast(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST forecast: %d: %s", w.Code, w.Body.String())
	}
	var start issueForecastStartResponse
	if err := json.NewDecoder(w.Body).Decode(&start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	status, _ := waitForRunTerminal(t, start.RunID)
	if status != "completed" {
		t.Fatalf("run status %q, want completed", status)
	}

	sw := httptest.NewRecorder()
	sreq := withURLParam(newForecastTestRequest("GET", "/api/experimental/pythia-oracle/forecast/issue/runs/"+start.RunID+"/stream", nil), "runID", start.RunID)
	pythiaRunStream(sw, sreq)
	body := sw.Body.String()
	if !strings.Contains(body, "event: snapshot") {
		t.Fatalf("stream missing snapshot frame:\n%s", body)
	}
	if !strings.Contains(body, `"run_kind":"initial"`) {
		t.Errorf("snapshot missing run_kind:\n%s", body)
	}
	if !strings.Contains(body, "event: status") || !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("stream missing terminal status frame:\n%s", body)
	}
}

// TestPythiaForecastHandlerFallbackPopulates verifies that the
// package-level fallback handler stash is populated by
// setPythiaForecastHandlerFallback (called from
// AttachPythiaIssueForecastMiddleware) and read by
// pythiaForecastHandler().
func TestPythiaForecastHandlerFallbackPopulates(t *testing.T) {
	t.Parallel()

	// Snapshot the package state and restore it on exit so the
	// fallback doesn't leak between tests.
	prev := pythiaForecastHandler()
	t.Cleanup(func() {
		setPythiaForecastHandlerFallback(prev)
	})

	setPythiaForecastHandlerFallback(nil)
	if got := pythiaForecastHandler(); got != nil {
		t.Fatalf("pre-set: pythiaForecastHandler() = %v, want nil", got)
	}

	want := &Handler{}
	setPythiaForecastHandlerFallback(want)
	if got := pythiaForecastHandler(); got != want {
		t.Fatalf("post-set: pythiaForecastHandler() = %v, want %v", got, want)
	}
}

// TestPythiaForecastHandlerFallbackConcurrencySpawnsReaders fires N
// goroutines that all read pythiaForecastHandler() concurrently while
// a writer mutates the stash. The RWMutex must keep readers safe.
func TestPythiaForecastHandlerFallbackConcurrencySpawnsReaders(t *testing.T) {
	t.Parallel()

	prev := pythiaForecastHandler()
	t.Cleanup(func() {
		setPythiaForecastHandlerFallback(prev)
	})

	const N = 16
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = pythiaForecastHandler()
				}
			}
		}()
	}

	// Writer flips the fallback N times to exercise Lock/Unlock.
	for i := 0; i < 1000; i++ {
		if i%2 == 0 {
			setPythiaForecastHandlerFallback(&Handler{})
		} else {
			setPythiaForecastHandlerFallback(nil)
		}
	}

	close(stop)
	wg.Wait()
}

// 0.5.115 monitor workspace resolution: the monitor route lives on the
// bare authed router (no workspace middleware) and api.rawRequest never
// sends X-Workspace-ID, so the 0.5.113 header fallback matched nothing —
// every renderer load 400'd. Pins the explicit workspace_id query-param
// contract (GetClaudeLabContext pattern) plus the degraded fallbacks.
func TestPythiaForecastMonitorWorkspaceResolution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	const monitorPath = "/api/experimental/pythia-oracle/forecast/monitor"

	t.Run("explicit workspace_id query param returns 200", func(t *testing.T) {
		req := newForecastTestRequest("GET", monitorPath+"?limit=30&workspace_id="+testWorkspaceID, nil)
		rec := httptest.NewRecorder()
		pythiaForecastMonitor(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("query-param request: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var runs []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
			t.Fatalf("unmarshal monitor body: %v", err)
		}
	})

	t.Run("no workspace anywhere is 400", func(t *testing.T) {
		req := newForecastTestRequest("GET", monitorPath+"?limit=30", nil)
		// newRequest stamps the shared fixture workspace header; strip it so
		// this subtest really exercises the both-sources-empty rejection.
		req.Header.Del("X-Workspace-ID")
		req.Header.Del("X-Workspace-Slug")
		rec := httptest.NewRecorder()
		pythiaForecastMonitor(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("no-workspace request: expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, "invalid workspace id") {
			t.Fatalf("body %q should name the missing workspace id", body)
		}
	})

	t.Run("non-uuid workspace_id is 400", func(t *testing.T) {
		req := newForecastTestRequest("GET", monitorPath+"?workspace_id=not-a-uuid", nil)
		rec := httptest.NewRecorder()
		pythiaForecastMonitor(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad-uuid request: expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("X-Workspace-ID header alone still works as degraded fallback", func(t *testing.T) {
		req := newForecastTestRequest("GET", monitorPath+"?limit=30", nil)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		rec := httptest.NewRecorder()
		pythiaForecastMonitor(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("header fallback: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
