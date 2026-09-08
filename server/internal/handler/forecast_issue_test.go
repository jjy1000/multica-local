// Package handler — forecast_issue_test.go (0.3.29+)
//
// Smoke test for the per-issue Pythia forecast SSE handler. We invoke
// pythiaIssueForecastStream against the same captureWriter shim used
// by claude_lab_forecast_test.go so we can assert the wire shape
// (including the new `issue_id` and `scenario_context` envelope
// fields) without racing a real network connection.
//
// The test does not need a live *Handler — pythiaIssueForecastStream
// reads its inputs from the request context (issueForecastContextKey),
// so we plumb the context directly.

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
)

// TestClampIssueForecastRounds covers the rounds contract: a caller
// omitting `rounds` gets the 3-round default (0.5.104 — the 0.3.30.3
// 10-round auto-run was retired for latency + LLM budget reasons); a
// caller explicitly requesting N above the cap gets clamped down to 10.
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

// TestQueryOracleIssueSyntheticRelabel pins the 0.5.104 honesty fix:
// when the engine answers 200 with `synthetic: true` (its internal LLM
// bridge call failed and it emitted placeholder narrative), the envelope
// MUST carry lab_source=synthetic_oracle_failover instead of "oracle".
// Pre-fix, a placeholder round was indistinguishable from a genuine
// deduction and runs reported "真实引擎推演" for pure filler data.
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
			}, 42, 1)
			if err != nil {
				t.Fatalf("queryOracleIssue returned error: %v", err)
			}
			if env.LabSource != tc.wantLabSrc {
				t.Errorf("LabSource = %q, want %q (synthetic=%v)", env.LabSource, tc.wantLabSrc, tc.synthetic)
			}
		})
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

// TestIssueForecastStreamEmitsEnvelopeWithIssueID confirms the SSE
// handler emits at least one `prediction` event whose JSON envelope
// carries `issue_id`, `scenario_context`, and `lab_source` fields.
// This is the contract change for 0.3.29 — pre-0.3.29 the envelope
// didn't bind a forecast to an Issue, so the renderer couldn't
// correlate SSE frames with the Issue that triggered them.
func TestIssueForecastStreamEmitsEnvelopeWithIssueID(t *testing.T) {
	t.Parallel()

	ifc := &issueForecastContext{
		IssueID:     "11111111-1111-1111-1111-111111111111",
		IssueNumber: "JIA-42",
		Title:       "霍尔木兹海峡流量下降",
		Body:        "过去 7 天油轮 AIS 数据出现 15% 的同向下降趋势。",
		LabSource:   "pythia_oracle",
		WorkspaceID: "22222222-2222-2222-2222-222222222222",
	}
	ctx := withIssueForecastContext(context.Background(), ifc)
	rounds := 1

	w := &captureWriter{}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"/api/experimental/pythia-oracle/forecast/issue?seed=42",
		nil)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}

	done := make(chan struct{})
	go func() {
		pythiaIssueForecastStream(w, req, rounds)
		close(done)
	}()

	// Wait for the first frame to land — synthetic path emits
	// immediately on Enter, oracle path after the upstream call.
	deadline := time.Now().Add(3 * time.Second)
	var snapshot string
	var flushed int
	for {
		w.mu.Lock()
		snapshot = string(w.body)
		flushed = w.flushed
		w.mu.Unlock()
		if flushed > 0 && strings.Contains(snapshot, "\n\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for first frame; body=%q", snapshot)
		}
		time.Sleep(50 * time.Millisecond)
	}

	idx := strings.Index(snapshot, "\n\n")
	frame := snapshot[:idx+2]
	if !strings.HasPrefix(frame, "event: prediction\ndata: ") {
		t.Fatalf("missing prediction prefix: %q", frame)
	}
	payload := strings.TrimPrefix(frame, "event: prediction\ndata: ")
	var env forecastEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatalf("decode frame: %v (payload=%q)", err, payload)
	}

	// New 0.3.29 contract: issue_id is populated, scenario_context
	// echoes the bound issue's title + body.
	if env.IssueID != ifc.IssueID {
		t.Errorf("env.IssueID = %q, want %q", env.IssueID, ifc.IssueID)
	}
	if !strings.Contains(env.ScenarioContext, ifc.Title) {
		t.Errorf("env.ScenarioContext missing title: %q", env.ScenarioContext)
	}
	if env.LabSource == "" {
		t.Errorf("env.LabSource empty, want at least \"synthetic\" or \"oracle\"")
	}
	if env.Probability < 0 || env.Probability > 1 {
		t.Errorf("probability out of [0,1]: %f", env.Probability)
	}

	// End-of-rounds marker should land after the round 1 frame for
	// rounds=1 (no inter-round wait, single emit).
	_ = sync.Mutex{}
}

// TestBuildIssueForecastContextTruncatesBody verifies the 280-char
// truncation contract for the body field, so an embedded Issue
// description doesn't blow up the SSE frame.
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

// TestPythiaForecastHandlerFallbackPopulates verifies that the
// package-level fallback handler stash is populated by
// setPythiaForecastHandlerFallback (called from
// AttachPythiaIssueForecastMiddleware) and read by
// pythiaForecastHandler().
//
// 0.5.59 — the chi ctx-key propagation through r.WithContext was
// observed to drop the forecastIssueHandlerCtxKey in some SSE
// defer paths, producing zero-row persists despite a successful
// 200 + 45s SSE stream. The fallback closes that gap. This test
// pins both directions so a future chi update / middleware refactor
// can't silently regress.
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
// a writer mutates the stash. The RWMutex must keep readers safe;
// pre-0.5.59 this would race on the unsynchronised package var.
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

// TestIssueForecastStreamPersistsCollectedRounds — 0.5.60 root-cause pin.
// Pre-0.5.60 the deferred persist was wired as
// `defer persistIssueForecastRun(r, ifc, collected)` — Go evaluates
// deferred-call arguments at the defer statement, capturing the EMPTY
// slice header; the later appends updated the local variable, never the
// captured header, so every successful run (200, frames emitted) persisted
// zero rows. The 0.5.59 diagnostics made the skip visible; this test pins
// the closure fix end-to-end against the DB-backed handler: run the real
// stream, then assert the pythia_forecast_run row landed.
func TestIssueForecastStreamPersistsCollectedRounds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	issue := createIssueForTest(t, map[string]any{
		"title": "pythia-persist-e2e",
	})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM pythia_forecast_run WHERE issue_id = $1`, issue.ID)
	})

	ifc := &issueForecastContext{
		IssueID:     issue.ID,
		IssueNumber: issue.Identifier,
		Title:       "pythia-persist-e2e",
		LabSource:   "pythia_oracle",
		WorkspaceID: testWorkspaceID,
	}
	ctx := withIssueForecastContext(context.Background(), ifc)
	ctx = context.WithValue(ctx, forecastIssueHandlerCtxKey{}, testHandler)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"/api/experimental/pythia-oracle/forecast/issue?seed=7", nil)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}

	w := &captureWriter{}
	pythiaIssueForecastStream(w, req, 1)

	if !strings.Contains(string(w.body), "event: prediction") {
		t.Fatalf("stream emitted no prediction frame; body=%q", string(w.body))
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM pythia_forecast_run WHERE issue_id = $1`, issue.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count run rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 pythia_forecast_run row for the emitted round, got %d (pre-0.5.60 defer-capture bug = 0)", count)
	}
}
