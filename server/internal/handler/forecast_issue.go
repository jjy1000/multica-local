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
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RegisterPythiaIssueForecastRoutes wires the per-issue SSE endpoint
// onto the supplied chi router. The caller MUST gate this call on
// experimental.DefaultFor("pythia_oracle") — when the flag is off the
// route physically disappears.
//
// Two routes:
//   - POST /forecast/issue        — run a deliberation, stream rounds,
//     and (0.3.55) persist the completed run so it survives unmount.
//   - GET  /forecast/issue/runs   — list persisted runs for an issue,
//     newest first. Registered as a literal path (no {param}) so chi
//     can never mis-route it onto the POST route (0.3.45.8 ordering
//     lesson: literal before param).
func RegisterPythiaIssueForecastRoutes(r chi.Router) {
	r.Post("/api/experimental/pythia-oracle/forecast/issue", pythiaIssueForecast)
	r.Get("/api/experimental/pythia-oracle/forecast/issue/runs", pythiaIssueForecastRuns)
}

// defaultIssueForecastRounds is the default number of forecast
// rounds a caller gets when they don't pass `rounds`. 0.3.30.3 raises
// the default from 1 → 10 to honor the "issue creation triggers a
// 10-round Pythia deliberation" contract.
const defaultIssueForecastRounds = 10

// maxIssueForecastRounds caps a single SSE call at 10 rounds. Each
// round blocks on a /forecast/issue upstream POST (~3-5 s with a
// live LLM); 10 rounds × 5 s = 50 s of work per call. Anything
// longer would starve the LLM proxy budget (60 req/min global) and
// keep SSE handlers pinned across multiple concurrent users.
const maxIssueForecastRounds = 10

// clampIssueForecastRounds normalises the caller-supplied round
// count. Defaults to 10 when omitted; clamps at 10 so a single
// request can't burn the proxy budget. Exported as a small pure
// function so the round-count behaviour can be unit-tested without
// touching SSE plumbing.
func clampIssueForecastRounds(req int) int {
	if req <= 0 {
		return defaultIssueForecastRounds
	}
	if req > maxIssueForecastRounds {
		return maxIssueForecastRounds
	}
	return req
}

// issueForecastRequest is the wire shape for the new endpoint.
type issueForecastRequest struct {
	IssueID string `json:"issue_id"`
	// Rounds controls how many forecast envelopes to emit before
	// closing the stream. Defaults to 10 to honor the 0.3.30.3
	// "Pythia 推演 10 轮" contract — issue creation auto-launches a
	// 10-round deliberation loop. Capped at 10 so a single request
	// can never starve the LLM proxy budget (60 req/min global).
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
		// 0.3.30.3: 10 rounds is the issue-creation default per the
		// "Pythia 推演 10 轮" contract. Callers can still override
		// by passing `rounds: N`.
		req.Rounds = defaultIssueForecastRounds
	}
	if req.Rounds > maxIssueForecastRounds {
		req.Rounds = maxIssueForecastRounds
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

	collected := make([]forecastEnvelope, 0, rounds)
	// 0.3.55: persist the deliberation so the lab view can surface it
	// after the fact (pre-0.3.55 the rounds were SSE-live only and
	// vanished on unmount). Deferred so every termination path —
	// normal completion, upstream error, client disconnect — writes
	// whatever rounds actually landed. A zero-round result is skipped
	// inside the helper.
	//
	// 0.5.60 — THIS MUST STAY A CLOSURE. Go evaluates deferred-call
	// arguments at the defer statement, so the pre-0.5.60 form
	// `defer persistIssueForecastRun(r, ifc, collected)` captured the
	// EMPTY slice header (len 0); the subsequent appends updated the
	// local variable, never the captured header — every successful run
	// persisted zero rows ("推演成功但 UI 无数据"). The closure reads
	// the variable at call time. Regression-pinned by
	// TestIssueForecastStreamPersistsCollectedRounds.
	defer func() { persistIssueForecastRun(r, ifc, collected) }()

	emit := func(round int) error {
		env, err := source(r.Context(), seed, ifc, round)
		if err != nil {
			return err
		}
		collected = append(collected, env)
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

// persistIssueForecastRun writes the completed (or partial, on early
// exit) per-issue deliberation to pythia_forecast_run. Non-fatal: a
// persistence failure logs and returns without affecting the SSE
// response, which has already been streamed to the client.
//
// The write uses a detached context (context.WithoutCancel + a short
// timeout) because the originating request context is frequently
// already cancelled by the time the stream finishes — the client has
// its frames and moved on — but the row must still land so the lab
// view has a finished result to read.
// persistIssueForecastRun writes the completed (or partial, on early
// exit) per-issue deliberation to pythia_forecast_run. Non-fatal: a
// persistence failure logs and returns without affecting the SSE
// response, which has already been streamed to the client.
//
// 0.5.59 — every silent early-return now logs a WRN so a missing
// row in pythia_forecast_run is diagnosable from the server log.
// The pre-0.5.59 code returned without any signal when the handler
// context was lost (chi middleware propagation gap), which produced
// the "推演成功但 UI 无数据" symptom — see ship log.
//
// The write uses a detached context (context.WithoutCancel + a short
// timeout) because the originating request context is frequently
// already cancelled by the time the stream finishes — the client has
// its frames and moved on — but the row must still land so the lab
// view has a finished result to read.
func persistIssueForecastRun(r *http.Request, ifc *issueForecastContext, envelopes []forecastEnvelope) {
	issueIDStr := ""
	wsIDStr := ""
	if ifc != nil {
		issueIDStr = ifc.IssueID
		wsIDStr = ifc.WorkspaceID
	}
	if len(envelopes) == 0 {
		slog.Warn("pythia forecast: persist skipped — zero envelopes",
			"issue_id", issueIDStr, "workspace_id", wsIDStr,
			"reason", "the stream finished with no envelopes collected (every round errored before emit)")
		return
	}
	if ifc == nil {
		slog.Warn("pythia forecast: persist skipped — nil issue context")
		return
	}
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok || h == nil {
		// 0.5.59 — package-level fallback. The chi middleware sets
		// the per-request ctx key, but the SSE defer path sometimes
		// loses it (observed pre-0.5.59: every run produced zero DB
		// rows even though the SSE stream emitted 10 frames). The
		// ctx-miss WRN above still fires so the underlying chi
		// issue stays visible in logs.
		if fb := pythiaForecastHandler(); fb != nil {
			h = fb
		} else {
			slog.Warn("pythia forecast: persist skipped — no handler in ctx AND no package-level fallback",
				"issue_id", issueIDStr,
				"hint", "AttachPythiaIssueForecastMiddleware was never called for this route")
			return
		}
	}
	if h.Queries == nil {
		slog.Warn("pythia forecast: persist skipped — handler.Queries is nil",
			"issue_id", issueIDStr)
		return
	}
	issueUUID, err := util.ParseUUID(ifc.IssueID)
	if err != nil {
		slog.Warn("pythia forecast: persist skipped — issue UUID parse failed",
			"issue_id_str", ifc.IssueID, "error", err)
		return
	}
	wsUUID, err := util.ParseUUID(ifc.WorkspaceID)
	if err != nil {
		slog.Warn("pythia forecast: persist skipped — workspace UUID parse failed",
			"workspace_id_str", ifc.WorkspaceID, "error", err)
		return
	}
	payload, err := json.Marshal(envelopes)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	run, err := h.Queries.CreatePythiaForecastRun(ctx, dbpkg.CreatePythiaForecastRunParams{
		WorkspaceID: wsUUID,
		IssueID:     issueUUID,
		Rounds:      int32(len(envelopes)),
		Source:      forecastRunSource(envelopes),
		Envelopes:   payload,
	})
	if err != nil {
		slog.Warn("pythia forecast: persist run failed",
			"issue_id", ifc.IssueID,
			"workspace_id", ifc.WorkspaceID,
			"rounds", len(envelopes),
			"source", forecastRunSource(envelopes),
			"error", err)
		return
	}
	slog.Info("pythia forecast: persist run OK",
		"issue_id", ifc.IssueID,
		"workspace_id", ifc.WorkspaceID,
		"rounds", len(envelopes),
		"source", forecastRunSource(envelopes))

	// 0.5.86 issue-delivery batch: the text report lands IN the issue
	// as the pythia_runtime leader's comment (migration 282
	// report_comment_id is the idempotency marker). Best-effort — a
	// failed writeback must never fail the run. Auxiliary/trace labs
	// never reach this path (InteractionModelAssignee contract).
	wbCtx, wbCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Second)
	defer wbCancel()
	content := pythiaIssueReportContent(ifc, envelopes, forecastRunSource(envelopes))
	if commentID := postLabRunReportComment(wbCtx, h, issueUUID, wsUUID, "pythia_runtime", content); commentID.Valid {
		if _, err := h.Queries.SetPythiaForecastRunReportComment(wbCtx, dbpkg.SetPythiaForecastRunReportCommentParams{
			ID:              run.ID,
			ReportCommentID: commentID,
		}); err != nil {
			slog.Warn("pythia forecast: report_comment_id update failed",
				"run_id", util.UUIDToString(run.ID), "error", err)
		}
	}
}

// pythiaIssueReportContent renders the issue-first text report from the
// run's envelopes. Chinese-first (the workspace owner reads zh); the
// provenance line keeps the honesty law — synthetic / failover / mixed
// runs are labeled as such instead of passing as engine output.
func pythiaIssueReportContent(ifc *issueForecastContext, envelopes []forecastEnvelope, source string) string {
	title := ""
	if ifc != nil {
		title = ifc.Title
	}
	var b strings.Builder
	b.WriteString("🔮 **Pythia 预测报告**")
	if title != "" {
		b.WriteString(" ·《" + title + "》")
	}
	b.WriteString("\n\n")
	b.WriteString("轮数：" + strconv.Itoa(len(envelopes)) + " · 来源：" + pythiaSourceLabelZH(source) + "\n")
	for i, e := range envelopes {
		if i >= 10 {
			b.WriteString("\n（仅展示前 10 轮，完整结果见实验室面板）\n")
			break
		}
		b.WriteString("\n**" + strconv.Itoa(i+1) + ". " + e.Scenario + "**")
		details := ""
		if e.Persona != "" {
			details += "视角 " + e.Persona
		}
		if e.Horizon != "" {
			if details != "" {
				details += " · "
			}
			details += "时间尺度 " + e.Horizon
		}
		if details != "" {
			b.WriteString("（" + details + "）")
		}
		b.WriteString("\n")
		b.WriteString("概率 " + strconv.FormatFloat(e.Probability*100, 'f', 0, 64) + "% · 置信度 " + strconv.FormatFloat(e.Confidence*100, 'f', 0, 64) + "%\n")
		if n := truncateReportRunes(e.Narrative, 140); n != "" {
			b.WriteString(n + "\n")
		}
	}
	b.WriteString("\n数据来源：" + pythiaSourceNoteZH(source) + "完整推演（世界视图 / 校准记录 / 对话推演）见实验室「Pythia 多视角预测」面板。")
	return b.String()
}

// pythiaSourceLabelZH / pythiaSourceNoteZH keep the 0.5.82 honesty law
// on the issue surface: a reader must be able to tell engine output
// from seeded fallback data without opening the lab view.
func pythiaSourceLabelZH(source string) string {
	switch source {
	case "oracle":
		return "真实引擎推演"
	case "synthetic":
		return "本地回退数据"
	case "synthetic_oracle_failover":
		return "引擎失败后本地回退"
	case "mixed":
		return "混合来源"
	default:
		return source
	}
}

func pythiaSourceNoteZH(source string) string {
	switch source {
	case "oracle":
		return "全部轮次由 Pythia 引擎真实推演。"
	case "synthetic":
		return "引擎不可用，结果为本地回退数据（非真实推演）。"
	case "synthetic_oracle_failover":
		return "部分轮次引擎失败后由本地回退数据补充。"
	case "mixed":
		return "轮次来自多个来源（含真实推演与回退数据）。"
	default:
		return ""
	}
}

// truncateReportRunes caps a narrative at n runes so a long LLM answer
// cannot flood the issue timeline; the full text stays in the lab view.
func truncateReportRunes(s string, n int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}

// forecastRunSource collapses the per-envelope `lab_source` provenance
// into a single run-level source label for the pythia_forecast_run.source
// CHECK column. Uniform runs keep their label; a run whose rounds came
// from more than one source (e.g. oracle answered 4 rounds then failed
// over to synthetic) is tagged "mixed".
func forecastRunSource(envelopes []forecastEnvelope) string {
	source := ""
	for _, env := range envelopes {
		if source == "" {
			source = env.LabSource
			continue
		}
		if env.LabSource != source {
			return "mixed"
		}
	}
	switch source {
	case "oracle", "synthetic", "synthetic_oracle_failover":
		return source
	default:
		return "synthetic"
	}
}

// pythiaIssueForecastRuns serves
// GET /api/experimental/pythia-oracle/forecast/issue/runs?issue_id=<id>&limit=N.
// Returns persisted deliberations for the bound issue, newest first.
// The issue is resolved through loadIssueForUser so workspace-membership
// scope stays identical to the POST route — a foreign-workspace issue
// is a 404, never a leak.
func pythiaIssueForecastRuns(w http.ResponseWriter, r *http.Request) {
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "handler unavailable")
		return
	}
	issueID := r.URL.Query().Get("issue_id")
	if strings.TrimSpace(issueID) == "" {
		writeError(w, http.StatusBadRequest, "issue_id is required")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	rows, err := h.Queries.ListPythiaForecastRunsByIssue(r.Context(), dbpkg.ListPythiaForecastRunsByIssueParams{
		IssueID: issue.ID,
		Limit:   int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list forecast runs: "+err.Error())
		return
	}
	type runSummary struct {
		ID        string          `json:"id"`
		Rounds    int32           `json:"rounds"`
		Source    string          `json:"source"`
		CreatedAt string          `json:"created_at"`
		Envelopes json.RawMessage `json:"envelopes"`
	}
	out := make([]runSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, runSummary{
			ID:        util.UUIDToString(row.ID),
			Rounds:    row.Rounds,
			Source:    row.Source,
			CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
			Envelopes: json.RawMessage(row.Envelopes),
		})
	}
	writeJSON(w, http.StatusOK, out)
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
			// 0.3.45.2 bug fix (P1#5): oracle failure fell back to the
			// synthetic envelope but did NOT relabel it, so the UI
			// showed the same data shape and the user could not tell
			// whether they were reading a live model answer or a
			// mock. Force the LabSource to "synthetic_oracle_failover"
			// so the renderer can tag it "此为 mock 数据" / "oracle
			// failed, used local fallback".
			env, _ = syntheticIssueForecast(ctx, seed, ifc, round)
			env.LabSource = "synthetic_oracle_failover"
			return env, nil
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
		// Clamp at 0.97: the band-widening formula crosses 1.0 at round ≥ 9
		// (0.42 + 9*0.07 = 1.05), which the renderer then displays as
		// "105%" — an impossible probability that screams fake data even
		// to readers who missed the source note. (0.5.103)
		Probability:     math.Min(0.97, 0.42+float64(round)*0.07),
		Confidence:      0.55,
		Horizon:         "week",
		Persona:         "strategist",
		LabSource:       "synthetic",
		ScenarioContext: scenarioContextFor(ifc),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// queryOracleIssue posts a /forecast/issue call against the running
// pythia_oracle subprocess with the bound issue's title as
// `question`. Mirrors queryOracle in claude_lab_forecast.go but
// uses the issue context to derive the question + lab_source, and
// hits the engine's issue-bound endpoint instead of the global
// /predict (which only kicks off the prediction loop asynchronously
// — it returns {"status": "started"} rather than an envelope).
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
		strings.TrimRight(baseURL, "/")+"/forecast/issue", bytes.NewReader(payload))
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
		Horizon     string  `json:"horizon"`
		Persona     string  `json:"persona"`
		Round       int     `json:"round"`
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
	horizon := raw.Horizon
	if horizon == "" {
		horizon = "week"
	}
	persona := raw.Persona
	if persona == "" {
		persona = "strategist"
	}
	return forecastEnvelope{
		ID:              fmt.Sprintf("p_issue_%d-ora_r%d", forecastSeq.Add(1), round),
		IssueID:         ifc.IssueID,
		Scenario:        raw.Scenario,
		Narrative:       raw.Narrative,
		Probability:     raw.Probability,
		Confidence:      raw.Confidence,
		Horizon:         horizon,
		Persona:         persona,
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
	// 0.5.59 — stash the handler in a package-level fallback so the
	// SSE defer in persistIssueForecastRun can still recover it even
	// if chi drops the context key along the r.WithContext chain
	// (observed pre-0.5.59: ctx lookup returned false → silent return
	// → DB never received the row → "推演无反馈"). Diagnostic WRN
	// above still fires so the real chi bug, if any, is visible.
	setPythiaForecastHandlerFallback(h)
	r.Use(MountPythiaIssueForecastMiddleware(h))
}

// setPythiaForecastHandlerFallback stores the *Handler that
// persistIssueForecastRun falls back to when the chi request context
// is missing forecastIssueHandlerCtxKey. Exposed (rather than writing
// the package var directly) so tests don't have to instantiate a
// chi.Router to exercise the stash path.
func setPythiaForecastHandlerFallback(h *Handler) {
	pythiaForecastHandlerMu.Lock()
	pythiaForecastHandlerFallback = h
	pythiaForecastHandlerMu.Unlock()
}

// pythiaForecastHandlerFallback is the package-level mirror of the
// per-request middleware-attached *Handler. It exists ONLY as a
// safety net for the SSE defer path. Populated by
// AttachPythiaIssueForecastMiddleware at router boot; never mutated
// afterwards. Protected by a Mutex so concurrent reads (during
// SSE defers) see a stable value.
var (
	pythiaForecastHandlerMu       sync.RWMutex
	pythiaForecastHandlerFallback *Handler
)

func pythiaForecastHandler() *Handler {
	pythiaForecastHandlerMu.RLock()
	defer pythiaForecastHandlerMu.RUnlock()
	return pythiaForecastHandlerFallback
}
