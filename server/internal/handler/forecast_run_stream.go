// Package handler — forecast_run_stream.go (0.5.111)
//
// Client-facing surfaces for a background pythia forecast run:
//
//   - GET  .../forecast/issue/runs/{runID}/stream — SSE. Writes a
//     `snapshot` frame (run meta + every envelope persisted so far),
//     then forwards live bus frames (`round` / `report` / `status`)
//     until the run reaches a terminal status. Reconnect-safe by
//     construction: the snapshot replays persistence, the bus carries
//     the live tail, and duplicate round frames are idempotent
//     client-side (the reducer keys by envelope index).
//   - POST .../forecast/issue/runs/{runID}/cancel — abort a running
//     run via the bus's registered CancelFunc; falls back to a direct
//     DB status write when the runner goroutine is already gone.
//   - POST /api/experimental/pythia-oracle/chat — issue-grounded Q&A
//     proxy to the engine's /chat (follow-up tab; optionally talk to
//     one council persona). The issue context + the latest run's
//     conclusion report ride into the prompt so the answer is about
//     THIS issue's deliberation, not the global prediction deck.

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// pythiaStreamKeepAlive pings idle streams so proxies don't buffer.
	pythiaStreamKeepAlive = 15 * time.Second
	// pythiaStreamMaxLifetime bounds one subscriber. A full-council run
	// costs 5 LLM calls per round; 10 rounds × ~90 s + report ≈ 16 min,
	// so 20 min covers the worst legal run.
	pythiaStreamMaxLifetime = 20 * time.Minute
)

// pythiaRunStreamSnapshot is the `snapshot` SSE frame payload.
type pythiaRunStreamSnapshot struct {
	Run struct {
		ID          string  `json:"id"`
		Rounds      int32   `json:"rounds"`
		Source      string  `json:"source"`
		Status      string  `json:"status"`
		RunKind     string  `json:"run_kind"`
		Variables   string  `json:"variables"`
		ParentRunID *string `json:"parent_run_id"`
		Report      string  `json:"report"`
		CreatedAt   string  `json:"created_at"`
	} `json:"run"`
	Envelopes json.RawMessage `json:"envelopes"`
}

func pythiaRunTerminal(status string) bool {
	return status == "completed" || status == "aborted" || status == "failed"
}

// loadPythiaRunForUser resolves a run id and gates it on issue
// membership through loadIssueForUser, so a foreign-workspace run id is
// a 404 exactly like every other issue-scoped surface. Returns nil
// after writing the error.
func loadPythiaRunForUser(w http.ResponseWriter, r *http.Request, h *Handler) *dbpkg.PythiaForecastRun {
	rawID := chi.URLParam(r, "runID")
	runUUID, ok := parseUUIDOrBadRequest(w, rawID, "runID")
	if !ok {
		return nil
	}
	row, err := h.Queries.GetPythiaForecastRun(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "forecast run not found")
		return nil
	}
	// Membership gate: the run's issue must be visible to the caller.
	if _, ok := h.loadIssueForUser(w, r, util.UUIDToString(row.IssueID)); !ok {
		return nil
	}
	return &row
}

// pythiaRunStream serves GET .../runs/{runID}/stream.
func pythiaRunStream(w http.ResponseWriter, r *http.Request) {
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok || h == nil || h.Queries == nil {
		writeError(w, http.StatusInternalServerError, "handler unavailable")
		return
	}
	row := loadPythiaRunForUser(w, r, h)
	if row == nil {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeFrame := func(event string, payload any) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	writeStatus := func(status string) bool {
		return writeFrame("status", map[string]string{"status": status})
	}

	// Subscribe BEFORE reading the row so no event can slip between the
	// snapshot and the live tail. A round published in that window may
	// appear in BOTH the snapshot and the channel — the client reducer
	// keys by envelope index, so the duplicate is idempotent.
	runID := util.UUIDToString(row.ID)
	ch, unsubscribe := pythiaForecastBus.subscribe(runID)
	defer unsubscribe()

	snap := pythiaRunStreamSnapshot{}
	snap.Run.ID = runID
	snap.Run.Rounds = row.Rounds
	snap.Run.Source = row.Source
	snap.Run.Status = row.Status
	snap.Run.RunKind = row.RunKind
	snap.Run.Variables = row.Variables
	snap.Run.Report = row.Report
	snap.Run.CreatedAt = row.CreatedAt.Time.Format(time.RFC3339)
	if row.ParentRunID.Valid {
		id := util.UUIDToString(row.ParentRunID)
		snap.Run.ParentRunID = &id
	}
	snap.Envelopes = json.RawMessage(row.Envelopes)
	if !writeFrame("snapshot", snap) {
		return
	}
	if pythiaRunTerminal(row.Status) {
		_ = writeStatus(row.Status)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), pythiaStreamMaxLifetime)
	defer cancel()
	keepAlive := time.NewTicker(pythiaStreamKeepAlive)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev := <-ch:
			switch ev.Type {
			case "round":
				if ev.Envelope == nil {
					continue
				}
				if !writeFrame("round", ev.Envelope) {
					return
				}
			case "report":
				if !writeFrame("report", map[string]string{"report": ev.Report}) {
					return
				}
			case "status":
				_ = writeStatus(ev.Status)
				return
			}
		}
	}
}

// pythiaRunCancel serves POST .../runs/{runID}/cancel.
func pythiaRunCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok || h == nil || h.Queries == nil {
		writeError(w, http.StatusInternalServerError, "handler unavailable")
		return
	}
	row := loadPythiaRunForUser(w, r, h)
	if row == nil {
		return
	}
	if pythiaRunTerminal(row.Status) {
		writeJSON(w, http.StatusOK, map[string]string{"status": row.Status})
		return
	}
	runID := util.UUIDToString(row.ID)
	status := "aborted"
	if pythiaForecastBus.cancelRun(runID) {
		// The runner observes ctx cancellation between rounds, flips the
		// row itself, and publishes the status frame. Give it a beat; the
		// response reports the intent, the stream reports the fact.
	} else {
		// Runner goroutine gone (server restarted mid-run): write the
		// terminal status directly so the row doesn't stay 'running'.
		if _, err := h.Queries.SetPythiaForecastRunStatus(r.Context(), dbpkg.SetPythiaForecastRunStatusParams{
			ID:     row.ID,
			Status: status,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to abort run: "+err.Error())
			return
		}
		pythiaForecastBus.publish(runID, pythiaRunEvent{Type: "status", Status: status})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// pythiaChat serves POST /api/experimental/pythia-oracle/chat — the
// follow-up tab's Q&A. Grounded on THIS issue: the title + body and the
// latest run's conclusion report ride into the message so the engine's
// oracle answers about the deliberation, not the global deck.
func pythiaChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok || h == nil || h.Queries == nil {
		writeError(w, http.StatusInternalServerError, "handler unavailable")
		return
	}
	var req struct {
		IssueID string `json:"issue_id"`
		Message string `json:"message"`
		Persona string `json:"persona,omitempty"`
		History []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"history,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}
	if len(req.Message) > 4000 {
		req.Message = truncateReportRunes(req.Message, 4000)
	}
	issue, ok := h.loadIssueForUser(w, r, req.IssueID)
	if !ok {
		return
	}
	ifc := buildIssueForecastContext(issue)

	// Latest run's conclusion report grounds the answer in the run the
	// user is looking at. Best-effort: a chat with no runs still works
	// (the engine grounds on its world view instead).
	grounding := scenarioContextFor(ifc)
	rows, err := h.Queries.ListPythiaForecastRunsByIssue(r.Context(), dbpkg.ListPythiaForecastRunsByIssueParams{
		IssueID: issue.ID,
		Limit:   1,
	})
	if err == nil && len(rows) > 0 && strings.TrimSpace(rows[0].Report) != "" {
		grounding += "\n\n=== 最近一次推演结论报告 ===\n" + truncateReportRunes(rows[0].Report, 1600)
	}

	url := oracleLoopbackURL()
	if url == "" {
		writeError(w, http.StatusServiceUnavailable, "pythia engine is not running")
		return
	}
	history := make([]map[string]string, 0, len(req.History))
	for _, m := range req.History {
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		history = append(history, map[string]string{"role": role, "content": truncateReportRunes(m.Content, 2000)})
	}
	payload, err := json.Marshal(map[string]any{
		"message": "关于议题《" + ifc.Title + "》\n\n" + grounding + "\n\n— 用户追问 —\n" + req.Message,
		"persona": strings.TrimSpace(req.Persona),
		"history": history,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode chat request")
		return
	}
	engineReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		strings.TrimRight(url, "/")+"/chat", strings.NewReader(string(payload)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build chat request")
		return
	}
	engineReq.Header.Set("Content-Type", "application/json")
	engineReq.Header.Set("X-Multica-Embedded", "1")
	engineReq.Header.Set("X-API-Key", oracleEngineKey())
	cli := &http.Client{Timeout: 120 * time.Second}
	resp, err := cli.Do(engineReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "pythia engine unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("pythia engine http %d", resp.StatusCode))
		return
	}
	var out struct {
		Answer  string  `json:"answer"`
		Persona *string `json:"persona"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		writeError(w, http.StatusBadGateway, "pythia engine returned an unparseable answer")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
