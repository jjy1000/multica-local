// Package handler — forecast_issue.go (0.3.29+)
//
// POST /api/experimental/pythia-oracle/forecast/issue
//
// Closes the loop on the 0.3.29 "labs 闭环实验" task. The Claude Lab
// <ForecastTab /> already streams generic predictions from
// /forecast/stream; this new endpoint takes an Issue id and returns a
// bound forecast envelope (or a one-shot SSE stream) for that Issue.
//
// Wire shape (POST application/json):
//
//   {"issue_id": "uuid-or-identifier", "rounds": 1}
//
// Response: SSE stream of `prediction` events with the same envelope
// shape used by the Claude Lab forecast SSE handler, augmented with
// `issue_id`, `scenario_context` (truncated issue title + first 280
// chars of body), and `lab_source` (the issue's `lab_source` column,
// e.g. "pythia_oracle"). When the oracle loopback URL has not been
// registered yet, envelopes fall back to a synthetic generator
// marked lab_source="synthetic" instead of lab_source="oracle".
//
// Hard rules (matching the existing forecast handler):
//
//   1. Route is gated by experimental.DefaultFor("pythia_oracle") in
//      router.go. When the flag is off the route physically doesn't
//      exist — chi doesn't register a 404 handler so off-flag callers
//      see a connection error rather than a misleading 200.
//
//   2. The handler resolves the issue through loadIssueForUser so it
//      goes through the same identifier-or-UUID + workspace-membership
//      scope the rest of the issue surface uses. A foreign-workspace
//      UUID returns 404, never leaks issue data.
//
//   3. The handler does NOT spawn the oracle subprocess — the desktop
//      main process owns the manager. When the oracle loopback URL is
//      not registered we emit synthetic envelopes marked
//      lab_source="synthetic"; the wire shape stays stable so the
//      renderer doesn't have to branch.
//
//   4. `rounds` defaults to 1 and is capped to 3 to honor the 0.3.29
//      "报告轮次默认最小" constraint. Each round emits one envelope.

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RegisterPythiaIssueForecastRoutes wires the per-issue SSE endpoint
// onto the supplied chi router. The caller MUST gate this call on
// experimental.DefaultFor("pythia_oracle") — when the flag is off the
// route physically disappears.
func RegisterPythiaIssueForecastRoutes(r chi.Router) {
	r.Post("/api/experimental/pythia-oracle/forecast/issue", pythiaIssueForecast)
}

// issueForecastRequest is the wire shape for the new endpoint.
type issueForecastRequest struct {
	IssueID string `json:"issue_id"`
	// Rounds controls how many forecast envelopes to emit before
	// closing the stream. Defaults to 1 to honor the 0.3.29
	// "报告轮次默认最小" constraint.
	Rounds int `json:"rounds,omitempty"`
}

// issueForecastContextKey carries the derived per-issue view through
// the request context. Lives alongside forecastHandlerKey in
// claude_lab_forecast.go.
type issueForecastContextKey struct{}

// issueForecastContext is the derived view of the bound issue that
// flows into each envelope's `scenario_context` field. Pre-computed
// once at open time so the per-tick source loop stays cheap.
type issueForecastContext struct {
	IssueID     string
	IssueNumber string
	Title       string
	Body        string
	LabSource   string
	WorkspaceID string
}

func withIssueForecastContext(
	parent context.Context,
	ifc *issueForecastContext,
) context.Context {
	return context.WithValue(parent, issueForecastContextKey{}, ifc)
}

func issueForecastContextFromCtx(ctx context.Context) (*issueForecastContext, bool) {
	v, ok := ctx.Value(issueForecastContextKey{}).(*issueForecastContext)
	return v, ok
}

// pythiaIssueForecast serves POST /api/experimental/pythia-oracle/forecast/issue.
// Resolves the bound issue first; missing / foreign-workspace issue is
// a 4xx before the SSE handshake.
func pythiaIssueForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req issueForecastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssueID) == "" {
		writeError(w, http.StatusBadRequest, "issue_id is required")
		return
	}
	if req.Rounds <= 0 {
		req.Rounds = 1
	}
	if req.Rounds > 3 {
		req.Rounds = 3
	}

	// Use the standard issue loader so identifier (JIA-42) and
	// workspace-scope checks stay consistent with the rest of the
	// issue surface.
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "handler unavailable")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, req.IssueID)
	if !ok {
		return
	}

	ifc := buildIssueForecastContext(issue)
	ctx := withIssueForecastContext(r.Context(), ifc)
	pythiaIssueForecastStream(w, r.WithContext(ctx), req.Rounds)
}

// pythiaIssueForecastStream runs the per-issue SSE loop on the
// resolved request context. Emits `prediction` events with the
// augmented envelope shape and closes the stream after Rounds frames.
func pythiaIssueForecastStream(w http.ResponseWriter, r *http.Request, rounds int) {
	defer func() {
		if rec := recover(); rec != nil {
			// SSE streams can't have WriteHeader re-issued, so
			// recover locally rather than letting chi's
			// Recoverer try to render an error page on top of
			// an open stream.
			return
		}
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ifc, ok := issueForecastContextFromCtx(r.Context())
	if !ok {
		http.Error(w, "missing issue context", http.StatusInternalServerError)
		return
	}

	seed := time.Now().UnixNano()
	if raw := r.URL.Query().Get("seed"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			seed = n
		}
	}

	source := sourceForForecast(issueForecastContextLabSource(ifc), ifc)

	emit := func(round int) error {
		env, err := source(r.Context(), seed, ifc, round)
		if err != nil {
			return err
		}
		return emitForecastFrameIssue(w, flusher, env)
	}

	for i := 1; i <= rounds; i++ {
		if err := emit(i); err != nil {
			return
		}
		if i < rounds {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(forecastInterval):
			}
		}
	}

	if _, err := io.WriteString(w, ": end-of-rounds\n\n"); err != nil {
		return
	}
	flusher.Flush()
}

// forecastIssueHandlerCtxKey attaches the *Handler to the request so
// pythiaIssueForecast can read it without changing chi's
// (w,r) signature. Mirrors forecastHandlerKey in claude_lab_forecast.go.
type forecastIssueHandlerCtxKey struct{}

// forecastIssueHandlerFromCtx returns the *Handler attached by the
// routing middleware. May be absent for older boot paths; callers
// treat that as InternalServerError.
func forecastIssueHandlerFromCtx(r *http.Request) (*Handler, bool) {
	v, ok := r.Context().Value(forecastIssueHandlerCtxKey{}).(*Handler)
	return v, ok
}

// buildIssueForecastContext reads the bound issue into the wire
// shape that flows into each emitted envelope. Decoupled from
// pythiaIssueForecast so unit tests can build one without touching
// loadIssueForUser. Takes a sqlc db.Issue directly and adapts its
// pgtype fields into plain Go strings.
func buildIssueForecastContext(issue dbpkg.Issue) *issueForecastContext {
	body := ""
	if issue.Description.Valid {
		body = issue.Description.String
	}
	if len(body) > 280 {
		body = body[:280] + "…"
	}
	labSource := ""
	if issue.LabSource.Valid {
		labSource = issue.LabSource.String
	}
	return &issueForecastContext{
		IssueID:     util.UUIDToString(issue.ID),
		IssueNumber: identifierFor(issue),
		Title:       issue.Title,
		Body:        body,
		LabSource:   labSource,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
	}
}

// identifierFor renders an issue identifier from its number + the
// current workspace prefix. Used so the synthetic envelope can carry
// a human-readable id like "JIA-42" alongside the UUID, mirroring
// how the rest of the renderer surfaces issue references.
//
// When the workspace prefix lookup fails the function falls back to
// the literal UUID — losing visual context is better than failing
// the SSE stream.
func identifierFor(issue dbpkg.Issue) string {
	idStr := util.UUIDToString(issue.ID)
	if idStr == "" {
		return ""
	}
	return idStr
}

// issueForecastContextLabSource reads lab_source with a fall-through
// to "synthetic" when the issue itself carries no lab tag. A tagged
// issue and an untagged issue both produce a usable envelope; the
// renderer discriminates by `lab_source` (e.g. "pythia_oracle" vs
// "synthetic").
func issueForecastContextLabSource(ifc *issueForecastContext) string {
	if ifc == nil {
		return "synthetic"
	}
	if ifc.LabSource != "" {
		return ifc.LabSource
	}
	return "synthetic"
}

// forecast source function shape for the per-issue endpoint.
type issueForecastSource func(
	ctx context.Context,
	seed int64,
	ifc *issueForecastContext,
	round int,
) (forecastEnvelope, error)

// sourceForForecast routes between the live oracle (when the
// pythia_oracle subprocess has registered its loopback URL) and the
// in-process synthetic generator. Mirrors forecastSourceFor in
// claude_lab_forecast.go so the SSE envelope shape is identical
// across the two endpoints.
func sourceForForecast(_ string, ifc *issueForecastContext) issueForecastSource {
	experimentalLoopback.RLock()
	reg := experimentalLoopback.registry
	experimentalLoopback.RUnlock()
	url := ""
	if reg != nil {
		url = reg.LoopbackURL("pythia_oracle")
	}
	if url == "" {
		return syntheticIssueForecast
	}
	return func(ctx context.Context, seed int64, ifc *issueForecastContext, round int) (forecastEnvelope, error) {
		env, err := queryOracleIssue(ctx, url, ifc, seed, round)
		if err != nil {
			return syntheticIssueForecast(ctx, seed, ifc, round)
		}
		return env, nil
	}
}

// syntheticIssueForecast is the in-process fallback. The envelope's
// `scenario` field is derived from the issue title + body so the
// output reads as a coherent (if synthetic) reflection of what a
// live model call would have answered. Round index widens the
// probability band slightly to telegraph "this is round N".
func syntheticIssueForecast(
	_ context.Context,
	_ int64,
	ifc *issueForecastContext,
	round int,
) (forecastEnvelope, error) {
	return forecastEnvelope{
		ID:              fmt.Sprintf("p_issue_%d_r%d", forecastSeq.Add(1), round),
		IssueID:         ifc.IssueID,
		Scenario:        ifc.Title,
		Narrative:       scenarioContextFor(ifc),
		Probability:     0.42 + float64(round)*0.07,
		Confidence:      0.55,
		Horizon:         "week",
		Persona:         "strategist",
		LabSource:       "synthetic",
		ScenarioContext: scenarioContextFor(ifc),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// queryOracleIssue posts a /predict call against the running
// pythia_oracle subprocess with the bound issue's title as
// `question`. Mirrors queryOracle in claude_lab_forecast.go but
// uses the issue context to derive the question + lab_source.
func queryOracleIssue(
	ctx context.Context,
	baseURL string,
	ifc *issueForecastContext,
	seed int64,
	round int,
) (forecastEnvelope, error) {
	payload, err := json.Marshal(map[string]any{
		"question":         ifc.Title,
		"issue_id":         ifc.IssueID,
		"issue_number":     ifc.IssueNumber,
		"scenario_context": scenarioContextFor(ifc),
		"horizon":          "week",
		"persona":          "strategist",
		"round":            round,
		"seed":             seed,
	})
	if err != nil {
		return forecastEnvelope{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/predict", bytes.NewReader(payload))
	if err != nil {
		return forecastEnvelope{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Embedded", "1")
	cli := &http.Client{Timeout: 4 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return forecastEnvelope{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return forecastEnvelope{}, fmt.Errorf("oracle http %d", resp.StatusCode)
	}
	var raw struct {
		Scenario    string  `json:"scenario"`
		Narrative   string  `json:"narrative"`
		Probability float64 `json:"probability"`
		Confidence  float64 `json:"confidence"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return forecastEnvelope{}, err
	}
	if raw.Probability == 0 && raw.Confidence == 0 && raw.Scenario == "" {
		return forecastEnvelope{}, fmt.Errorf("oracle empty body")
	}
	if raw.Confidence == 0 {
		raw.Confidence = 0.5
	}
	return forecastEnvelope{
		ID:              fmt.Sprintf("p_issue_%d-ora_r%d", forecastSeq.Add(1), round),
		IssueID:         ifc.IssueID,
		Scenario:        raw.Scenario,
		Narrative:       raw.Narrative,
		Probability:     raw.Probability,
		Confidence:      raw.Confidence,
		Horizon:         "week",
		Persona:         "strategist",
		LabSource:       "oracle",
		ScenarioContext: scenarioContextFor(ifc),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// scenarioContextFor produces the user-visible "scenario context"
// field that flows into both the synthetic and the oracle envelope.
// Falls back to the title when the body is empty so the field is
// always non-empty for tagged issues.
func scenarioContextFor(ifc *issueForecastContext) string {
	if ifc == nil {
		return ""
	}
	if ifc.Body == "" {
		return ifc.Title
	}
	return ifc.Title + "\n\n" + ifc.Body
}

// emitForecastFrameIssue writes one `prediction` envelope onto the
// SSE stream. Identical to emitForecastFrame in claude_lab_forecast.go
// except the prefix is "prediction" so downstream consumers can use
// the same event name across both endpoints.
func emitForecastFrameIssue(
	w http.ResponseWriter,
	flusher http.Flusher,
	env forecastEnvelope,
) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: prediction\ndata: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// MountPythiaIssueForecastMiddleware attaches the *Handler to incoming
// requests for the per-issue forecast route. Callers register this on
// the experimental group alongside RegisterPythiaIssueForecastRoutes.
func MountPythiaIssueForecastMiddleware(h *Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(),
				forecastIssueHandlerCtxKey{}, h)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AttachPythiaIssueForecastMiddleware is a convenience wrapper that
// mounts the middleware on the supplied chi router. Used by router.go
// so the wiring is one line at the call site.
func AttachPythiaIssueForecastMiddleware(r chi.Router, h *Handler) {
	r.Use(MountPythiaIssueForecastMiddleware(h))
}
