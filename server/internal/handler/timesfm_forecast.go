// Package handler — timesfm_forecast.go (0.5.82 WL2)
//
// POST /api/experimental/timesfm/forecast/issue
// GET  /api/experimental/timesfm/forecast/issue/runs?issue_id=&limit=
//
// Issue-task-first surface for the TimesFM lab: an agent assignee
// (leader `timesfm_oracle`, provisioned by install_timesfm.go) posts
// numeric series extracted from the issue context (CSV artifacts,
// metric tables in comments) and gets a quantile-band forecast back;
// every successful run is persisted into timesfm_forecast_run
// (migration 275) so the read-only lab view (route suffix
// timesfm-lab) can list records WITHOUT a live engine (ICP-2) and
// deep links can target a single run (ICP-3, ?issue=<id>&run=<id>).
//
// Hard rules (mirroring forecast_issue.go for pythia):
//
//  1. Route is gated by RequireExperimentalFlag("timesfm") in
//     router.go — when the flag is off the route returns a uniform
//     404 (experimental_guard.go), indistinguishable from a
//     nonexistent route.
//
//  2. The handler resolves the issue through loadIssueForUser so it
//     goes through the same identifier-or-UUID + workspace-membership
//     scope the rest of the issue surface uses. A foreign-workspace
//     UUID returns 404, never leaks issue data.
//
//  3. The handler does NOT spawn the engine subprocess — the desktop
//     main process owns the manager. The engine base URL is resolved
//     exactly like forecast_issue.go resolves the pythia oracle: via
//     the process-local experimentalLoopback registry populated by
//     the desktop's __experimental/upstream registration
//     (LoopbackService "timesfm" from the catalog entry). When the
//     URL is not registered we return 503 — unlike pythia there is
//     deliberately no synthetic envelope: the engine's own Tier-0
//     seasonal-naive fallback is the honest fallback, and GET /runs
//     keeps working engine-down (ICP record listing).
//
//  4. Persistence is non-fatal: an engine answer is returned to the
//     caller even if the run row fails to land (logged as WRN),
//     mirroring persistIssueForecastRun's contract.

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// timesfmForecastMaxHorizon mirrors the engine's ForecastConfig
// max_horizon=256 cap; the engine clamps too, but clamping here keeps
// the persisted run row's horizons value equal to what was actually
// forecast without a second engine round-trip.
const timesfmForecastMaxHorizon = 256

// timesfmForecastDefaultHorizon is used when a caller omits `horizon`.
// 24 steps is the report's issue-scale default (R5: context ≤1024,
// horizon ≤256 expected seconds on CPU).
const timesfmForecastDefaultHorizon = 24

// timesfmForecastUpstreamTimeout covers CPU inference of seconds-minutes
// per request (feasibility report R5) while still bounding the handler.
const timesfmForecastUpstreamTimeout = 90 * time.Second

// RegisterTimesfmIssueForecastRoutes wires the per-issue endpoints onto
// the supplied chi router. The caller MUST gate this call on
// RequireExperimentalFlag("timesfm") — when the flag is off the routes
// return a uniform 404.
func RegisterTimesfmIssueForecastRoutes(r chi.Router, h *Handler) {
	r.Post("/api/experimental/timesfm/forecast/issue", h.timesfmIssueForecast)
	// Literal path registered alongside POST — chi matches by method +
	// segment count, so /runs can never be captured by a param route.
	r.Get("/api/experimental/timesfm/forecast/issue/runs", h.timesfmIssueForecastRuns)
}

// timesfmForecastRequest is the wire shape for the POST endpoint.
// `series` accepts either a single series (number[]) or a batch
// (number[][]) — the single-series form is the skill's primary idiom
// (feasibility §3 contract) and is normalized to a 1-row batch.
type timesfmForecastRequest struct {
	IssueID string `json:"issue_id"`
	// Series is kept raw so the handler can accept both shapes; it is
	// validated + normalized by normalizeTimesfmSeries before any
	// forward.
	Series json.RawMessage `json:"series"`
	// Horizon defaults to 24 and clamps to [1, 256].
	Horizon int `json:"horizon,omitempty"`
	// Dates optionally labels future points: string[] (single series)
	// or string[][] parallel to the normalized series batch. Echoed by
	// the engine for chart rendering; never interpreted.
	Dates json.RawMessage `json:"dates,omitempty"`
}

// normalizeTimesfmSeries validates the raw `series` field and returns
// the canonical number[][] batch forwarded to the engine. JSON has no
// NaN literal, so the NaN-tolerance the engine offers upstream is
// already excluded by the wire format; infinities are rejected
// outright.
func normalizeTimesfmSeries(raw json.RawMessage) ([][]float64, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("series is required")
	}
	var single []float64
	if err := json.Unmarshal(raw, &single); err == nil {
		if len(single) == 0 {
			return nil, fmt.Errorf("series must be non-empty")
		}
		for _, v := range single {
			if math.IsInf(v, 0) {
				return nil, fmt.Errorf("series values must be finite")
			}
		}
		return [][]float64{single}, nil
	}
	var batch [][]float64
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("series must be number[] or number[][]")
	}
	if len(batch) == 0 {
		return nil, fmt.Errorf("series must be non-empty")
	}
	for i, s := range batch {
		if len(s) == 0 {
			return nil, fmt.Errorf("series[%d] must be non-empty", i)
		}
		for _, v := range s {
			if math.IsInf(v, 0) {
				return nil, fmt.Errorf("series[%d] values must be finite", i)
			}
		}
	}
	return batch, nil
}

// normalizeTimesfmDates validates the optional `dates` field against
// the normalized series count. Accepts string[] (single series) or
// string[][] parallel to the batch. Returns nil when absent.
func normalizeTimesfmDates(raw json.RawMessage, seriesCount int) ([][]string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	var single []string
	if err := json.Unmarshal(raw, &single); err == nil {
		if seriesCount != 1 {
			return nil, fmt.Errorf("dates as string[] only supports a single series")
		}
		return [][]string{single}, nil
	}
	var batch [][]string
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("dates must be string[] or string[][]")
	}
	if len(batch) != seriesCount {
		return nil, fmt.Errorf("dates must be parallel to series (%d vs %d)", len(batch), seriesCount)
	}
	return batch, nil
}

// clampTimesfmHorizon normalises the caller-supplied horizon.
func clampTimesfmHorizon(req int) int {
	if req <= 0 {
		return timesfmForecastDefaultHorizon
	}
	if req > timesfmForecastMaxHorizon {
		return timesfmForecastMaxHorizon
	}
	return req
}

// timesfmLoopbackURL resolves the engine base URL exactly like
// forecast_issue.go resolves the pythia oracle: the process-local
// experimentalLoopback registry the desktop main process populates via
// __experimental/upstream (catalog LoopbackService "timesfm").
// Empty string = engine not running.
func timesfmLoopbackURL() string {
	experimentalLoopback.RLock()
	reg := experimentalLoopback.registry
	experimentalLoopback.RUnlock()
	if reg == nil {
		return ""
	}
	return reg.LoopbackURL("timesfm")
}

// timesfmEngineSeries / timesfmEngineResponse mirror run_loopback.py's
// POST /forecast response shape.
type timesfmEngineSeries struct {
	Point      []float64            `json:"point"`
	Quantiles  map[string][]float64 `json:"quantiles,omitempty"`
	Provenance string               `json:"provenance,omitempty"`
	Dates      []string             `json:"dates,omitempty"`
}

type timesfmEngineResponse struct {
	Series       []timesfmEngineSeries `json:"series"`
	Provenance   string                `json:"provenance"`
	ModelPresent bool                  `json:"model_present"`
	Horizon      int                   `json:"horizon"`
}

// timesfmProvenanceFor collapses the engine's provenance labels into
// the timesfm_forecast_run.provenance CHECK set
// ('model' | 'seasonal_naive' | 'mixed'). A missing top-level label is
// recomputed from the per-series labels; any unknown label falls back
// to "mixed" so the CHECK constraint can never reject the row.
func timesfmProvenanceFor(top string, series []timesfmEngineSeries) string {
	switch top {
	case "model", "seasonal_naive", "mixed":
		return top
	}
	seen := ""
	for _, s := range series {
		p := s.Provenance
		if p != "model" && p != "seasonal_naive" {
			return "mixed"
		}
		if seen == "" {
			seen = p
			continue
		}
		if seen != p {
			return "mixed"
		}
	}
	if seen == "" {
		return "mixed"
	}
	return seen
}

// timesfmIssueForecast serves POST
// /api/experimental/timesfm/forecast/issue.
func (h *Handler) timesfmIssueForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req timesfmForecastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssueID) == "" {
		writeError(w, http.StatusBadRequest, "issue_id is required")
		return
	}
	series, err := normalizeTimesfmSeries(req.Series)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dates, err := normalizeTimesfmDates(req.Dates, len(series))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	horizon := clampTimesfmHorizon(req.Horizon)

	// Standard issue loader: identifier-or-UUID + workspace-membership
	// scope identical to the rest of the issue surface.
	issue, ok := h.loadIssueForUser(w, r, req.IssueID)
	if !ok {
		return
	}

	baseURL := timesfmLoopbackURL()
	if baseURL == "" {
		// Engine-down is an honest 503 — the lab view reads persisted
		// rows via GET /runs, which keeps working (ICP-2).
		writeError(w, http.StatusServiceUnavailable,
			"timesfm engine is not running — enable the lab and let the desktop spawn resources/timesfm/run.sh, then retry")
		return
	}

	engineResp, rawBody, err := forwardTimesfmForecast(r.Context(), baseURL, series, horizon, dates)
	if err != nil {
		slog.Warn("timesfm forecast: upstream call failed",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		writeError(w, http.StatusBadGateway, "timesfm engine call failed: "+err.Error())
		return
	}

	provenance := timesfmProvenanceFor(engineResp.Provenance, engineResp.Series)

	// Persist BEFORE responding so run_id can ride along (ICP-3 deep
	// links). Persistence failure is non-fatal (logged, run_id="").
	runID := ""
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	run, err := h.Queries.CreateTimesfmForecastRun(persistCtx, dbpkg.CreateTimesfmForecastRunParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		Horizons:    int32(horizon),
		Provenance:  provenance,
		Result:      rawBody,
	})
	if err != nil {
		slog.Warn("timesfm forecast: persist run failed",
			"issue_id", util.UUIDToString(issue.ID),
			"workspace_id", util.UUIDToString(issue.WorkspaceID),
			"horizon", horizon,
			"provenance", provenance,
			"error", err)
	} else {
		runID = util.UUIDToString(run.ID)
		slog.Info("timesfm forecast: persist run OK",
			"issue_id", util.UUIDToString(issue.ID),
			"workspace_id", util.UUIDToString(issue.WorkspaceID),
			"run_id", runID,
			"horizon", horizon,
			"provenance", provenance)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":        runID,
		"issue_id":      util.UUIDToString(issue.ID),
		"provenance":    provenance,
		"model_present": engineResp.ModelPresent,
		"horizon":       horizon,
		"series":        engineResp.Series,
	})
}

// forwardTimesfmForecast posts the normalized envelope to the engine's
// /forecast endpoint and returns the decoded response plus the raw
// body (the raw body is what lands in timesfm_forecast_run.result, so
// the lab view re-renders exactly what the engine answered).
func forwardTimesfmForecast(
	ctx context.Context,
	baseURL string,
	series [][]float64,
	horizon int,
	dates [][]string,
) (timesfmEngineResponse, []byte, error) {
	var resp timesfmEngineResponse
	payload := map[string]any{
		"series":  series,
		"horizon": horizon,
	}
	if dates != nil {
		payload["dates"] = dates
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return resp, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/forecast", bytes.NewReader(body))
	if err != nil {
		return resp, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Embedded", "1")
	cli := &http.Client{Timeout: timesfmForecastUpstreamTimeout}
	httpResp, err := cli.Do(req)
	if err != nil {
		return resp, nil, err
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return resp, nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return resp, nil, fmt.Errorf("engine http %d: %s", httpResp.StatusCode, truncateForLog(raw))
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, nil, fmt.Errorf("engine returned non-JSON body: %w", err)
	}
	if len(resp.Series) == 0 {
		return resp, nil, fmt.Errorf("engine returned an empty series array")
	}
	return resp, raw, nil
}

func truncateForLog(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// timesfmIssueForecastRuns serves
// GET /api/experimental/timesfm/forecast/issue/runs?issue_id=<id>&limit=N.
// Returns persisted runs for the bound issue, newest first, default
// limit 10. Works even when the engine is down — this is the ICP-2
// record-listing substrate for the read-only lab view.
func (h *Handler) timesfmIssueForecastRuns(w http.ResponseWriter, r *http.Request) {
	issueID := r.URL.Query().Get("issue_id")
	if strings.TrimSpace(issueID) == "" {
		writeError(w, http.StatusBadRequest, "issue_id is required")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	rows, err := h.Queries.ListTimesfmForecastRunsByIssue(r.Context(), dbpkg.ListTimesfmForecastRunsByIssueParams{
		IssueID: issue.ID,
		Limit:   int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list forecast runs: "+err.Error())
		return
	}
	type runSummary struct {
		ID         string          `json:"id"`
		Horizons   int32           `json:"horizons"`
		Provenance string          `json:"provenance"`
		CreatedAt  string          `json:"created_at"`
		Result     json.RawMessage `json:"result"`
	}
	out := make([]runSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, runSummary{
			ID:         util.UUIDToString(row.ID),
			Horizons:   row.Horizons,
			Provenance: row.Provenance,
			CreatedAt:  row.CreatedAt.Time.Format(time.RFC3339),
			Result:     json.RawMessage(row.Result),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
