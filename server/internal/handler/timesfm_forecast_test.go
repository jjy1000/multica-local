// Package handler — timesfm_forecast_test.go (0.5.82 WL2)
//
// Handler-level suite for the TimesFM lab surface:
//
//   - POST /api/experimental/timesfm/forecast/issue — happy path
//     against a FAKE loopback upstream (httptest.Server speaking the
//     run_loopback.py /forecast contract), engine-down 503, request
//     validation, horizon clamp, and the run-row persistence +
//     provenance passthrough contract (migration 275).
//   - GET  /api/experimental/timesfm/forecast/issue/runs — record
//     listing works engine-down (ICP-2), default limit 10.
//   - install_timesfm.go — leader agent + purge-before-seed visibility
//     contract, idempotent re-install.
//
// The engine base URL is injected the same way production wires it:
// the process-local experimentalLoopback registry (code_canvas_test.go
// pattern). DB-backed cases are skipped by TestMain when DATABASE_URL
// is unreachable.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// timesfmOracleAgentName mirrors the hardcoded leader name in
// install_timesfm.go. Declared locally per the duplication law.
const timesfmOracleAgentName = "timesfm_oracle"

// withTimesfmLoopbackRegistry swaps the process-local registry for a
// fresh one (or nil) and restores the previous value on cleanup.
func withTimesfmLoopbackRegistry(t *testing.T, reg *experimental.Registry) {
	t.Helper()
	experimentalLoopback.Lock()
	prev := experimentalLoopback.registry
	experimentalLoopback.registry = reg
	experimentalLoopback.Unlock()
	t.Cleanup(func() {
		experimentalLoopback.Lock()
		experimentalLoopback.registry = prev
		experimentalLoopback.Unlock()
	})
}

// timesfmTestIssue creates a fixture issue through the real handler
// path and returns its UUID.
func timesfmTestIssue(t *testing.T, title string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
		cleanupReq = withURLParam(cleanupReq, "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
	})
	return created.ID
}

// fakeTimesfmEngine is an httptest.Server speaking the run_loopback.py
// /forecast contract. It records the last request body so tests can
// assert the forwarded envelope (series normalization, horizon clamp).
type fakeTimesfmEngine struct {
	*httptest.Server
	mu         sync.Mutex
	lastBody   map[string]any
	provenance string
}

func newFakeTimesfmEngine(t *testing.T, provenance string) *fakeTimesfmEngine {
	t.Helper()
	fe := &fakeTimesfmEngine{provenance: provenance}
	fe.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/forecast" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		fe.mu.Lock()
		fe.lastBody = body
		fe.mu.Unlock()
		horizon := int(body["horizon"].(float64))
		series, _ := body["series"].([]any)
		out := make([]map[string]any, 0, len(series))
		for range series {
			point := make([]float64, horizon)
			for i := range point {
				point[i] = 1.5
			}
			out = append(out, map[string]any{
				"point":      point,
				"quantiles":  map[string]any{},
				"provenance": provenance,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"series":        out,
			"provenance":    provenance,
			"model_present": provenance == "model",
			"horizon":       horizon,
		})
	}))
	t.Cleanup(fe.Close)
	return fe
}

func (fe *fakeTimesfmEngine) last() map[string]any {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	return fe.lastBody
}

func TestTimesfmForecastIssue_EngineDown(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := timesfmTestIssue(t, "TimesFM engine-down fixture")
	withTimesfmLoopbackRegistry(t, nil) // engine not registered

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/experimental/timesfm/forecast/issue", map[string]any{
		"issue_id": issueID,
		"series":   []float64{1, 2, 3, 4, 5},
	})
	testHandler.timesfmIssueForecast(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 engine-down, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTimesfmForecastIssue_HappyPathPersistsRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := timesfmTestIssue(t, "TimesFM happy path fixture")

	fe := newFakeTimesfmEngine(t, "model")
	reg := experimental.NewRegistry()
	reg.SetLoopbackURL("timesfm", fe.URL)
	withTimesfmLoopbackRegistry(t, reg)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/experimental/timesfm/forecast/issue", map[string]any{
		"issue_id": issueID,
		"series":   []float64{1, 2, 3, 4, 5, 6}, // single-series idiom
		"horizon":  4,
	})
	testHandler.timesfmIssueForecast(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		RunID        string `json:"run_id"`
		IssueID      string `json:"issue_id"`
		Provenance   string `json:"provenance"`
		ModelPresent bool   `json:"model_present"`
		Horizon      int    `json:"horizon"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RunID == "" {
		t.Fatal("run_id is empty — run row did not persist")
	}
	if resp.IssueID != issueID {
		t.Fatalf("issue_id = %q, want %q", resp.IssueID, issueID)
	}
	if resp.Provenance != "model" || !resp.ModelPresent {
		t.Fatalf("provenance/model_present = %q/%v, want model/true", resp.Provenance, resp.ModelPresent)
	}
	if resp.Horizon != 4 {
		t.Fatalf("horizon = %d, want 4", resp.Horizon)
	}

	// Forwarded envelope: single series normalized to a 1-row batch,
	// horizon passed through unclamped.
	if got, _ := fe.last()["horizon"].(float64); int(got) != 4 {
		t.Fatalf("forwarded horizon = %v, want 4", fe.last()["horizon"])
	}
	fwdSeries, _ := fe.last()["series"].([]any)
	if len(fwdSeries) != 1 {
		t.Fatalf("forwarded series rows = %d, want 1 (single-series normalization)", len(fwdSeries))
	}

	// Run row round-trip via the SQL surface (not just the API): the
	// persisted result must be the RAW engine body.
	run, err := testHandler.Queries.GetTimesfmForecastRun(ctx, uuidToPgtype(resp.RunID))
	if err != nil {
		t.Fatalf("GetTimesfmForecastRun: %v", err)
	}
	if run.Provenance != "model" || run.Horizons != 4 {
		t.Fatalf("run row provenance/horizons = %q/%d, want model/4", run.Provenance, run.Horizons)
	}
	var rawResult struct {
		Series []map[string]any `json:"series"`
	}
	if err := json.Unmarshal(run.Result, &rawResult); err != nil {
		t.Fatalf("run.Result is not the raw engine body: %v", err)
	}
	if len(rawResult.Series) != 1 {
		t.Fatalf("run.Result series rows = %d, want 1", len(rawResult.Series))
	}

	// GET /runs lists the persisted row newest-first, engine down.
	experimentalLoopback.Lock()
	experimentalLoopback.registry = nil
	experimentalLoopback.Unlock()
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/experimental/timesfm/forecast/issue/runs?issue_id="+issueID, nil)
	testHandler.timesfmIssueForecastRuns(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET runs expected 200 (must work engine-down), got %d: %s", w.Code, w.Body.String())
	}
	var runs []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0]["id"] != resp.RunID {
		t.Fatalf("runs[0].id = %v, want %v", runs[0]["id"], resp.RunID)
	}
	if runs[0]["provenance"] != "model" {
		t.Fatalf("runs[0].provenance = %v, want model", runs[0]["provenance"])
	}
}

func TestTimesfmForecastIssue_SeasonalNaiveProvenance(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := timesfmTestIssue(t, "TimesFM seasonal-naive fixture")

	fe := newFakeTimesfmEngine(t, "seasonal_naive")
	reg := experimental.NewRegistry()
	reg.SetLoopbackURL("timesfm", fe.URL)
	withTimesfmLoopbackRegistry(t, reg)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/experimental/timesfm/forecast/issue", map[string]any{
		"issue_id": issueID,
		"series":   [][]float64{{1, 2, 3}, {4, 5, 6}}, // batch idiom
	})
	testHandler.timesfmIssueForecast(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Provenance string `json:"provenance"`
		Horizon    int    `json:"horizon"`
		RunID      string `json:"run_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Provenance != "seasonal_naive" {
		t.Fatalf("provenance = %q, want seasonal_naive", resp.Provenance)
	}
	// Horizon omitted → the 24-step default.
	if resp.Horizon != timesfmForecastDefaultHorizon {
		t.Fatalf("horizon = %d, want default %d", resp.Horizon, timesfmForecastDefaultHorizon)
	}
	if resp.RunID == "" {
		t.Fatal("run_id empty — seasonal_naive runs must persist too")
	}
}

func TestTimesfmForecastIssue_RequestValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := timesfmTestIssue(t, "TimesFM validation fixture")
	reg := experimental.NewRegistry()
	reg.SetLoopbackURL("timesfm", "http://127.0.0.1:1") // never reached
	withTimesfmLoopbackRegistry(t, reg)

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing issue_id", map[string]any{"series": []float64{1, 2}}, http.StatusBadRequest},
		{"missing series", map[string]any{"issue_id": issueID}, http.StatusBadRequest},
		{"empty series", map[string]any{"issue_id": issueID, "series": []float64{}}, http.StatusBadRequest},
		{"non-numeric series", map[string]any{"issue_id": issueID, "series": []string{"a", "b"}}, http.StatusBadRequest},
		{"empty batch row", map[string]any{"issue_id": issueID, "series": [][]float64{{1, 2}, {}}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest("POST", "/api/experimental/timesfm/forecast/issue", tc.body)
			testHandler.timesfmIssueForecast(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}

	// Foreign-workspace issue is a 404, never a leak (loader scope).
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/experimental/timesfm/forecast/issue", map[string]any{
		"issue_id": "00000000-0000-0000-0000-000000000000",
		"series":   []float64{1, 2},
	})
	testHandler.timesfmIssueForecast(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign/missing issue: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTimesfmForecastIssue_HorizonClamped(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := timesfmTestIssue(t, "TimesFM horizon clamp fixture")

	fe := newFakeTimesfmEngine(t, "model")
	reg := experimental.NewRegistry()
	reg.SetLoopbackURL("timesfm", fe.URL)
	withTimesfmLoopbackRegistry(t, reg)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/experimental/timesfm/forecast/issue", map[string]any{
		"issue_id": issueID,
		"series":   []float64{1, 2, 3},
		"horizon":  99999,
	})
	testHandler.timesfmIssueForecast(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Horizon int `json:"horizon"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Horizon != timesfmForecastMaxHorizon {
		t.Fatalf("horizon = %d, want clamp %d", resp.Horizon, timesfmForecastMaxHorizon)
	}
	if got, _ := fe.last()["horizon"].(float64); int(got) != timesfmForecastMaxHorizon {
		t.Fatalf("forwarded horizon = %v, want %d", fe.last()["horizon"], timesfmForecastMaxHorizon)
	}
}

func TestTimesfmProvenanceFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		top  string
		ser  []timesfmEngineSeries
		want string
	}{
		{"top-level model", "model", nil, "model"},
		{"top-level seasonal_naive", "seasonal_naive", nil, "seasonal_naive"},
		{"top-level mixed", "mixed", nil, "mixed"},
		{"derived uniform", "", []timesfmEngineSeries{{Provenance: "model"}, {Provenance: "model"}}, "model"},
		{"derived divergent", "", []timesfmEngineSeries{{Provenance: "model"}, {Provenance: "seasonal_naive"}}, "mixed"},
		{"unknown label coerced", "weird", nil, "mixed"},
		{"unknown per-series coerced", "", []timesfmEngineSeries{{Provenance: "quantum"}}, "mixed"},
		{"empty everything", "", nil, "mixed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := timesfmProvenanceFor(tc.top, tc.ser); got != tc.want {
				t.Errorf("timesfmProvenanceFor(%q) = %q, want %q", tc.top, got, tc.want)
			}
		})
	}
}

func TestClampTimesfmHorizon(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want int
	}{
		{0, timesfmForecastDefaultHorizon},
		{-3, timesfmForecastDefaultHorizon},
		{1, 1},
		{24, 24},
		{256, 256},
		{257, timesfmForecastMaxHorizon},
		{99999, timesfmForecastMaxHorizon},
	}
	for _, tc := range cases {
		if got := clampTimesfmHorizon(tc.in); got != tc.want {
			t.Errorf("clampTimesfmHorizon(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeTimesfmSeries(t *testing.T) {
	t.Parallel()
	single, err := normalizeTimesfmSeries(json.RawMessage(`[1,2,3]`))
	if err != nil || len(single) != 1 || len(single[0]) != 3 {
		t.Fatalf("single series: %v, %v", single, err)
	}
	batch, err := normalizeTimesfmSeries(json.RawMessage(`[[1,2],[3]]`))
	if err != nil || len(batch) != 2 {
		t.Fatalf("batch series: %v, %v", batch, err)
	}
	if _, err := normalizeTimesfmSeries(json.RawMessage(`[]`)); err == nil {
		t.Fatal("empty single must error")
	}
	if _, err := normalizeTimesfmSeries(json.RawMessage(`"nope"`)); err == nil {
		t.Fatal("non-array must error")
	}
	if _, err := normalizeTimesfmSeries(nil); err == nil {
		t.Fatal("nil raw must error")
	}
}

// TestInstallTimesfm_SeedsVisibilityAndIdempotent pins the install
// contract: leader agent + lock + exactly one visibility row, stable
// across re-install (purge-before-seed keeps the count at 1).
func TestInstallTimesfm_SeedsVisibilityAndIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "timesfm-install")
	cleanupVisibilityRows(t, workspaceID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM experimental_resource_lock WHERE experimental_source = 'timesfm'`)
	})

	var firstAgentID string
	for i := 0; i < 2; i++ {
		if err := testHandler.InstallTimesfm(ctx, userID, workspaceID); err != nil {
			t.Fatalf("InstallTimesfm (attempt %d): %v", i+1, err)
		}
		agentRow, err := testHandler.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: uuidToPgtype(workspaceID),
			Name:        timesfmOracleAgentName,
		})
		if err != nil {
			t.Fatalf("timesfm_oracle agent not created (attempt %d): %v", i+1, err)
		}
		if i == 0 {
			firstAgentID = pgtypeToString(agentRow.ID)
		} else if pgtypeToString(agentRow.ID) != firstAgentID {
			t.Fatalf("re-install recreated the leader agent (%s → %s)", firstAgentID, pgtypeToString(agentRow.ID))
		}
		if !agentRow.RuntimeID.Valid {
			t.Fatal("timesfm_oracle agent has no runtime_id — online local runtime not bound")
		}
	}

	// Lock row exists under the verbatim "timesfm" source.
	var lockCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_lock
		WHERE experimental_source = 'timesfm' AND resource_type = 'agent'
	`).Scan(&lockCount); err != nil {
		t.Fatalf("lock count: %v", err)
	}
	if lockCount != 1 {
		t.Fatalf("lock rows = %d, want 1", lockCount)
	}

	// Purge-before-seed: exactly ONE visibility row survives re-installs.
	var visCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = 'timesfm' AND resource_type = 'agent'
	`).Scan(&visCount); err != nil {
		t.Fatalf("visibility count: %v", err)
	}
	if visCount != 1 {
		t.Fatalf("visibility rows = %d, want 1 (purge-before-seed)", visCount)
	}
}

// TestTimesfmFlagKeyLiteralsPinned is the duplication-law anchor: the
// catalog Key, the install-source literal, and the migration CHECK all
// carry the VERBATIM "timesfm" literal.
func TestTimesfmFlagKeyLiteralsPinned(t *testing.T) {
	t.Parallel()
	if got := timesfmSource; got != "timesfm" {
		t.Fatalf("timesfmSource = %q, want the verbatim literal", got)
	}
	if string(experimental.SourceTimesfm) != "timesfm" {
		t.Fatalf("experimental.SourceTimesfm = %q, want the verbatim literal", experimental.SourceTimesfm)
	}
	found := false
	for _, f := range experimental.Catalog {
		if f.Key == "timesfm" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("catalog entry %q missing", "timesfm")
	}
}
