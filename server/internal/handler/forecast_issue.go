// Package handler — forecast_issue.go (0.3.29+, refactored 0.5.111)
//
// POST /api/experimental/pythia-oracle/forecast/issue
//
// 0.5.111 continuation contract: the per-issue Pythia deliberation moved
// from a synchronous SSE loop to an ASYNC background run. The POST now
// validates, creates the pythia_forecast_run row (status='running'), and
// returns {"run_id": ...} immediately; the rounds execute in a detached
// goroutine that persists EVERY round as it lands (crash-safe), publishes
// to the in-memory run bus, and finishes with an LLM-synthesized conclusion
// report. Clients watch GET .../runs/{runID}/stream (see
// forecast_run_stream.go). A continuation run carries parent_run_id +
// variables: the parent's envelopes become the round history injected into
// every prompt, so "原问题 + 历史报告 + 新变量 → 新轮次" works end to end.
//
// Wire shape (POST application/json):
//
//	{"issue_id": "uuid-or-identifier", "rounds": 3,
//	 "variables": "把汇率冲击调高到 20% 后重新推演",       // optional
//	 "parent_run_id": "uuid-of-parent-run"}                // optional → continuation
//
// Response: {"run_id": "...", "rounds": N, "status": "running"}.
// Errors: 400 bad request (missing issue / bad parent), 404 foreign issue.
//
// Hard rules (unchanged from the SSE era):
//
//  1. Route is gated by experimental.DefaultFor("pythia_oracle") in
//     router.go. When the flag is off the route physically doesn't exist.
//  2. The handler resolves the issue through loadIssueForUser so
//     identifier-or-UUID + workspace-membership scope stays consistent
//     with the rest of the issue surface.
//  3. The handler does NOT spawn the oracle subprocess — the desktop
//     main process owns the manager. When the oracle loopback URL is not
//     registered, rounds fall back to synthetic envelopes labelled
//     lab_source="synthetic" (honesty law: the label rides on every
//     envelope AND the report comment).
//  4. `rounds` defaults to 3 for initial runs / 6 for continuations
//     (0.5.111; the user contract caps continuation rounds at 5-8 to
//     save tokens) and is clamped at 10. Natural-language pins in the
//     issue text or the variables text ("推演5轮") are parsed
//     server-side now — previously only the client parsed them.

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RegisterPythiaIssueForecastRoutes wires the per-issue forecast surface
// onto the supplied chi router. The caller MUST gate this call on
// experimental.DefaultFor("pythia_oracle") — when the flag is off the
// routes physically disappear.
//
// Five routes (literal paths registered BEFORE any {runID} subtree —
// 0.3.45.8 chi ordering lesson):
//   - POST /forecast/issue                  — start an async deliberation
//     (initial or continuation), returns {run_id} immediately.
//   - GET  /forecast/issue/runs             — list persisted runs for an
//     issue, newest first.
//   - GET  /forecast/issue/runs/{runID}/stream — SSE: snapshot (meta +
//     envelopes so far) → live round/report/status frames → close.
//   - POST /forecast/issue/runs/{runID}/cancel — abort a running run.
//   - POST /chat                            — issue-grounded Q&A proxy to
//     the engine's /chat (council persona or oracle).
func RegisterPythiaIssueForecastRoutes(r chi.Router) {
	r.Post("/api/experimental/pythia-oracle/forecast/issue", pythiaIssueForecast)
	r.Get("/api/experimental/pythia-oracle/forecast/issue/runs", pythiaIssueForecastRuns)
	r.Get("/api/experimental/pythia-oracle/forecast/issue/runs/{runID}/stream", pythiaRunStream)
	r.Post("/api/experimental/pythia-oracle/forecast/issue/runs/{runID}/cancel", pythiaRunCancel)
	r.Post("/api/experimental/pythia-oracle/chat", pythiaChat)
}

// defaultIssueForecastRounds is the default number of forecast rounds an
// INITIAL run gets when the caller passes no rounds and no natural-language
// pin. 3 since 0.5.104 (the 0.3.30.3-era default of 10 was retired for
// latency + LLM budget reasons).
const defaultIssueForecastRounds = 3

// defaultContinuationRounds is the default for CONTINUATION runs
// (0.5.111). The user contract: "如果用户没有指定,为节省 token 轮次默认
// 5-8" — 6 sits mid-band. A continuation already has history context, so
// fewer rounds still sharpen the forecast.
const defaultContinuationRounds = 6

// maxIssueForecastRounds caps a single run at 10 rounds. With the 0.5.111
// full-council contract each round is 1 oracle pass + 4 persona votes, so
// 10 rounds ≈ 50 LLM calls — the hard ceiling defends the LLM proxy
// budget (60 req/min global).
const maxIssueForecastRounds = 10

// clampIssueForecastRounds normalises an explicit caller-supplied round
// count. Zero/negative means "caller didn't say" (the caller's default is
// resolved by resolveIssueForecastRounds); anything above the cap is
// clamped so a single request can't burn the proxy budget.
func clampIssueForecastRounds(req int) int {
	if req <= 0 {
		return defaultIssueForecastRounds
	}
	if req > maxIssueForecastRounds {
		return maxIssueForecastRounds
	}
	return req
}

// Natural-language round pins, ported byte-for-byte from
// packages/views/issues/utils/forecast-rounds.ts (KEYWORD_FIRST /
// COUNT_FIRST). Tight adjacency on purpose: "分3轮讨论" or "第一轮" must
// NOT hijack the count just because 推演 appears elsewhere — a false
// positive silently spends more LLM rounds than the user asked for.
var (
	forecastKeywordFirst = regexp.MustCompile(`(?:推演|预测|模拟|预演)\s*[：:]?\s*(\d{1,2})\s*轮`)
	forecastCountFirst   = regexp.MustCompile(`(\d{1,2})\s*轮\s*(?:推演|预测|模拟|预演)`)
)

// parseForecastRoundsFromText extracts a natural-language round pin from
// the given texts (scanned in order; first match wins). Returns 0 when
// nothing pins a count. Unit-pinned in forecast_issue_test.go.
func parseForecastRoundsFromText(texts ...string) int {
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		m := forecastKeywordFirst.FindStringSubmatch(text)
		if m == nil {
			m = forecastCountFirst.FindStringSubmatch(text)
		}
		if len(m) < 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			continue
		}
		return min(n, maxIssueForecastRounds)
	}
	return 0
}

// resolveIssueForecastRounds picks the round count for a run: an explicit
// caller value wins, then a natural-language pin in the variables text or
// the issue title/body, then the per-kind default (continuation 6 /
// initial 3).
func resolveIssueForecastRounds(explicit int, continuation bool, texts ...string) int {
	if explicit > 0 {
		return clampIssueForecastRounds(explicit)
	}
	if n := parseForecastRoundsFromText(texts...); n > 0 {
		return n
	}
	if continuation {
		return defaultContinuationRounds
	}
	return defaultIssueForecastRounds
}

// issueForecastRequest is the wire shape for the POST endpoint.
type issueForecastRequest struct {
	IssueID string `json:"issue_id"`
	// Rounds is the explicit round count. 0 = resolve from natural
	// language, else the per-kind default. Clamped at 10.
	Rounds int `json:"rounds,omitempty"`
	// Variables is the user-injected continuation text ("修正方案:…").
	// Non-empty continuation input rides into every round prompt AND the
	// report synthesis prompt.
	Variables string `json:"variables,omitempty"`
	// ParentRunID turns the request into a CONTINUATION of a prior run:
	// the parent's envelopes become the round history. Must reference a
	// run of the SAME issue with at least one envelope.
	ParentRunID string `json:"parent_run_id,omitempty"`
}

// issueForecastStartResponse is the POST reply. The run executes in the
// background; clients subscribe to the stream route with run_id.
type issueForecastStartResponse struct {
	RunID   string `json:"run_id"`
	Rounds  int    `json:"rounds"`
	Status  string `json:"status"`
	RunKind string `json:"run_kind"`
}

// issueForecastContextKey carries the derived per-issue view through
// the request context. Lives alongside forecastHandlerKey in
// claude_lab_forecast.go.
type issueForecastContextKey struct{}

// issueForecastContext is the derived view of the bound issue that flows
// into each envelope's `scenario_context` field.
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

// forecastHistoryRound is one prior-round digest injected into the round
// prompts of a continuation run (and mirrored into the engine's history
// payload). Narrative is truncated at 400 runes per round, 12 rounds max
// — enough signal to condition round N on rounds 1..N-1 without blowing
// the engine's context budget.
type forecastHistoryRound struct {
	Round       int     `json:"round"`
	Narrative   string  `json:"narrative"`
	Probability float64 `json:"probability"`
}

const (
	forecastHistoryMaxRounds    = 12
	forecastHistoryNarrativeCap = 400
)

// historyFromEnvelopes converts a parent run's envelopes into the digest
// injected into continuation prompts. Round numbers reflect the parent
// run's ORIGINAL positions (a parent with 14 rounds yields rounds 3-14),
// so continuation prompts can reference earlier rounds unambiguously.
func historyFromEnvelopes(envelopes []forecastEnvelope) []forecastHistoryRound {
	if len(envelopes) == 0 {
		return nil
	}
	offset := 0
	if len(envelopes) > forecastHistoryMaxRounds {
		offset = len(envelopes) - forecastHistoryMaxRounds
		envelopes = envelopes[offset:]
	}
	out := make([]forecastHistoryRound, 0, len(envelopes))
	for i, e := range envelopes {
		out = append(out, forecastHistoryRound{
			Round:       offset + i + 1,
			Narrative:   truncateReportRunes(e.Narrative, forecastHistoryNarrativeCap),
			Probability: e.Probability,
		})
	}
	return out
}

// pythiaIssueForecast serves POST /api/experimental/pythia-oracle/forecast/issue.
// Resolves the bound issue, validates continuation inputs, creates the
// running row, spawns the detached runner, and replies with the run_id.
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
	req.Variables = strings.TrimSpace(req.Variables)
	if len(req.Variables) > 2000 {
		req.Variables = truncateReportRunes(req.Variables, 2000)
	}

	// Use the standard issue loader so identifier (JIA-42) and
	// workspace-scope checks stay consistent with the rest of the
	// issue surface.
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok || h == nil {
		writeError(w, http.StatusInternalServerError, "handler unavailable")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, req.IssueID)
	if !ok {
		return
	}
	ifc := buildIssueForecastContext(issue)

	// Continuation validation: the parent must exist, belong to the SAME
	// issue, and carry at least one envelope (else there is no history to
	// inherit and the run would silently behave like an initial run).
	var parentRow dbpkg.PythiaForecastRun
	continuation := false
	if req.ParentRunID != "" {
		parentUUID, err := util.ParseUUID(req.ParentRunID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "parent_run_id is not a UUID")
			return
		}
		row, err := h.Queries.GetPythiaForecastRun(r.Context(), parentUUID)
		if err != nil {
			writeError(w, http.StatusNotFound, "parent run not found")
			return
		}
		if row.IssueID != issue.ID {
			writeError(w, http.StatusBadRequest, "parent run belongs to a different issue")
			return
		}
		var parentEnvelopes []forecastEnvelope
		if err := json.Unmarshal(row.Envelopes, &parentEnvelopes); err != nil || len(parentEnvelopes) == 0 {
			writeError(w, http.StatusBadRequest, "parent run has no rounds to continue from")
			return
		}
		parentRow = row
		continuation = true
	}

	rounds := resolveIssueForecastRounds(req.Rounds, continuation,
		req.Variables, ifc.Title, ifc.Body)

	// Self-heal: a 'running' row untouched for >15 minutes belongs to a
	// dead server goroutine. Sweep it so the runs list never accumulates
	// phantom in-flight entries.
	if _, err := h.Queries.AbandonStalePythiaForecastRuns(r.Context(), issue.ID); err != nil {
		slog.Warn("pythia forecast: stale-run sweep failed", "issue_id", ifc.IssueID, "error", err)
	}

	// Create the row UPFRONT with status='running' (0.5.111). source is a
	// placeholder until the first round lands and forecastRunSource can
	// compute the real provenance. "synthetic" is the CHECK-conservative
	// neutral value and matches the pre-landing wire default.
	parentID := pgtypeZeroUUID()
	if continuation {
		parentID = parentRow.ID
	}
	variables := req.Variables
	runRow, err := h.Queries.CreatePythiaForecastRun(r.Context(), dbpkg.CreatePythiaForecastRunParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		Rounds:      0,
		Source:      "synthetic",
		Envelopes:   []byte("[]"),
		ParentRunID: parentID,
		RunKind:     pythiaRunKindLabel(continuation),
		Variables:   variables,
		Status:      "running",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create forecast run: "+err.Error())
		return
	}

	startPythiaForecastJob(h, pythiaForecastJob{
		RunID:     util.UUIDToString(runRow.ID),
		RunUUID:   runRow.ID,
		IssueID:   ifc.IssueID,
		Workspace: ifc.WorkspaceID,
		RunKind:   pythiaRunKindLabel(continuation),
		Ifc:       ifc,
		Rounds:    rounds,
		History:   historyFromEnvelopes(parentEnvelopesOf(parentRow, continuation)),
		Variables: variables,
		Seed:      time.Now().UnixNano(),
	})

	writeJSON(w, http.StatusOK, issueForecastStartResponse{
		RunID:   util.UUIDToString(runRow.ID),
		Rounds:  rounds,
		Status:  "running",
		RunKind: pythiaRunKindLabel(continuation),
	})
}

// pgtypeZeroUUID is the NULL parent_run_id value.
func pgtypeZeroUUID() pgtype.UUID { return pgtype.UUID{} }

// pythiaRunKindLabel maps the continuation flag onto the migration 290
// CHECK vocabulary.
func pythiaRunKindLabel(continuation bool) string {
	if continuation {
		return "continuation"
	}
	return "initial"
}

// parentEnvelopesOf decodes the parent row's envelopes (already validated
// non-empty in pythiaIssueForecast; safe to return nil here on re-entry).
func parentEnvelopesOf(row dbpkg.PythiaForecastRun, continuation bool) []forecastEnvelope {
	if !continuation {
		return nil
	}
	var envelopes []forecastEnvelope
	_ = json.Unmarshal(row.Envelopes, &envelopes)
	return envelopes
}

// issueRoundOpts carries everything a round needs beyond the issue
// context — the continuation history, the injected variables, and the
// total round count (so prompts can say "round N of M").
type issueRoundOpts struct {
	history    []forecastHistoryRound
	variables  string
	totalRounds int
}

// issueRoundSource produces one round envelope for the run.
type issueRoundSource func(
	ctx context.Context,
	seed int64,
	ifc *issueForecastContext,
	round int,
	opts issueRoundOpts,
) (forecastEnvelope, error)

// oracleLoopbackURL reads the pythia_oracle loopback URL from the
// desktop-registered upstream registry, or "" when the engine is down.
func oracleLoopbackURL() string {
	experimentalLoopback.RLock()
	reg := experimentalLoopback.registry
	experimentalLoopback.RUnlock()
	if reg == nil {
		return ""
	}
	return reg.LoopbackURL("pythia_oracle")
}

// issueRoundSourceFor routes between the live oracle (when the
// pythia_oracle subprocess has registered its loopback URL) and the
// in-process synthetic generator. Mirrors the old sourceForForecast
// contract: an oracle transport failure falls back to the synthetic
// envelope RELABELLED synthetic_oracle_failover so the UI never mistakes
// a mock for a live model answer (0.3.45.2 P1#5).
func issueRoundSourceFor(ifc *issueForecastContext) issueRoundSource {
	url := oracleLoopbackURL()
	if url == "" {
		return syntheticIssueForecast
	}
	return func(ctx context.Context, seed int64, ifc *issueForecastContext, round int, opts issueRoundOpts) (forecastEnvelope, error) {
		env, err := queryOracleIssue(ctx, url, ifc, seed, round, opts)
		if err != nil {
			env, _ = syntheticIssueForecast(ctx, seed, ifc, round, opts)
			env.LabSource = "synthetic_oracle_failover"
			return env, nil
		}
		return env, nil
	}
}

// syntheticIssueForecast is the in-process fallback. The envelope's
// `scenario` field is derived from the issue title + body so the output
// reads as a coherent (if synthetic) reflection of what a live model call
// would have answered. Round index widens the probability band slightly to
// telegraph "this is round N".
func syntheticIssueForecast(
	_ context.Context,
	_ int64,
	ifc *issueForecastContext,
	round int,
	_ issueRoundOpts,
) (forecastEnvelope, error) {
	return forecastEnvelope{
		ID:        fmt.Sprintf("p_issue_%d_r%d", forecastSeq.Add(1), round),
		IssueID:   ifc.IssueID,
		Scenario:  ifc.Title,
		Narrative: scenarioContextFor(ifc),
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
// pythia_oracle subprocess. 0.5.111: the payload carries the continuation
// history, the injected variables, and the full-council flag; the response
// may carry base_probability + the council vote sheet, which ride on the
// envelope for the panel's council view.
func queryOracleIssue(
	ctx context.Context,
	baseURL string,
	ifc *issueForecastContext,
	seed int64,
	round int,
	opts issueRoundOpts,
) (forecastEnvelope, error) {
	payload, err := json.Marshal(map[string]any{
		"question":         ifc.Title,
		"issue_id":         ifc.IssueID,
		"issue_number":     ifc.IssueNumber,
		"scenario_context": scenarioContextFor(ifc),
		"history":          opts.history,
		"variables":        opts.variables,
		"council":          true,
		"total_rounds":     opts.totalRounds,
		"horizon":          "week",
		"persona":          "strategist",
		"round":            round,
		"seed":             seed,
	})
	if err != nil {
		return forecastEnvelope{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/forecast/issue", strings.NewReader(string(payload)))
	if err != nil {
		return forecastEnvelope{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Embedded", "1")
	// One real round now costs an oracle pass PLUS a 4-persona council
	// (5 LLM calls; personas run with bounded concurrency). 180s matches
	// the engine's own httpx budget per call and keeps a hung engine from
	// pinning the run forever — the runner's own ctx still wins.
	cli := &http.Client{Timeout: 180 * time.Second}
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
		// BaseProbability is the oracle's SOLO estimate; the headline
		// probability is the council consensus when a council landed.
		BaseProbability *float64       `json:"base_probability"`
		Confidence      float64        `json:"confidence"`
		Horizon         string         `json:"horizon"`
		Persona         string         `json:"persona"`
		Round           int            `json:"round"`
		Synthetic       bool           `json:"synthetic"`
		Council         *pythiaCouncil `json:"council"`
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
	labSource := "oracle"
	if raw.Synthetic {
		// Engine-reported fallback relabels to the failover provenance so
		// the report comment's 数据来源 note and the renderer's "模拟数据"
		// badge stay truthful. Transport-level failures get the same label
		// from issueRoundSourceFor's error path.
		labSource = "synthetic_oracle_failover"
	}
	return forecastEnvelope{
		ID:              fmt.Sprintf("p_issue_%d-ora_r%d", forecastSeq.Add(1), round),
		IssueID:         ifc.IssueID,
		Scenario:        raw.Scenario,
		Narrative:       raw.Narrative,
		Probability:     raw.Probability,
		BaseProbability: raw.BaseProbability,
		Council:         raw.Council,
		Confidence:      raw.Confidence,
		Horizon:         horizon,
		Persona:         persona,
		LabSource:       labSource,
		ScenarioContext: scenarioContextFor(ifc),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// queryOracleIssueReport asks the engine to synthesize the conclusion
// report for a completed run (one LLM pass over all rounds). Returns
// ("", true) when the engine is unreachable or the synthesis fails — the
// caller falls back to the mechanical summary and keeps the honesty label.
func queryOracleIssueReport(
	ctx context.Context,
	baseURL string,
	ifc *issueForecastContext,
	envelopes []forecastEnvelope,
	variables string,
) (string, bool) {
	if baseURL == "" || len(envelopes) == 0 {
		return "", true
	}
	rounds := make([]map[string]any, 0, len(envelopes))
	for i, e := range envelopes {
		rounds = append(rounds, map[string]any{
			"round":       i + 1,
			"scenario":    e.Scenario,
			"narrative":   truncateReportRunes(e.Narrative, 700),
			"probability": e.Probability,
			"confidence":  e.Confidence,
		})
	}
	payload, err := json.Marshal(map[string]any{
		"question":         ifc.Title,
		"scenario_context": scenarioContextFor(ifc),
		"variables":        variables,
		"rounds":           rounds,
	})
	if err != nil {
		return "", true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/forecast/issue/report", strings.NewReader(string(payload)))
	if err != nil {
		return "", true
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Embedded", "1")
	cli := &http.Client{Timeout: 180 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return "", true
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", true
	}
	var raw struct {
		Report    string `json:"report"`
		Synthetic bool   `json:"synthetic"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", true
	}
	if raw.Synthetic || strings.TrimSpace(raw.Report) == "" {
		return "", true
	}
	return raw.Report, false
}

// pythiaIssueReportContent renders the issue-first comment for a finished
// run. 0.5.111: when the engine synthesized a conclusion report it IS the
// comment body (the SocialSim "第一个文字报告" contract); the mechanical
// per-round digest is the fallback. Continuation runs echo the injected
// variables so the issue timeline shows what changed. The provenance line
// keeps the honesty law — synthetic / failover / mixed runs are labeled as
// such instead of passing as engine output.
func pythiaIssueReportContent(
	ifc *issueForecastContext,
	envelopes []forecastEnvelope,
	source string,
	runKind string,
	variables string,
	report string,
) string {
	title := ""
	if ifc != nil {
		title = ifc.Title
	}
	var b strings.Builder
	b.WriteString("🔮 **Pythia 预测报告**")
	if title != "" {
		b.WriteString(" ·《" + title + "》")
	}
	if runKind == "continuation" {
		b.WriteString("（续推）")
	}
	b.WriteString("\n\n")
	if variables != "" {
		b.WriteString("**注入新变量：**" + truncateReportRunes(variables, 300) + "\n\n")
	}
	if trimmed := strings.TrimSpace(report); trimmed != "" {
		b.WriteString(trimmed)
		b.WriteString("\n")
	} else {
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
	}
	b.WriteString("\n数据来源：" + pythiaSourceNoteZH(source) + "完整推演（实时过程 / 逐轮 council 票据 / 回放 / 追问）见实验室「Pythia 多视角预测」面板。")
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
// 0.5.111: each summary now carries the continuation lineage (parent_run_id,
// run_kind, variables), the live status, and the synthesized report so the
// panel tabs read without a second fetch.
func pythiaIssueForecastRuns(w http.ResponseWriter, r *http.Request) {
	h, ok := forecastIssueHandlerFromCtx(r)
	if !ok || h == nil {
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
		ID          string          `json:"id"`
		Rounds      int32           `json:"rounds"`
		Source      string          `json:"source"`
		CreatedAt   string          `json:"created_at"`
		Envelopes   json.RawMessage `json:"envelopes"`
		ParentRunID *string         `json:"parent_run_id"`
		RunKind     string          `json:"run_kind"`
		Variables   string          `json:"variables"`
		Status      string          `json:"status"`
		Report      string          `json:"report"`
	}
	out := make([]runSummary, 0, len(rows))
	for _, row := range rows {
		summary := runSummary{
			ID:        util.UUIDToString(row.ID),
			Rounds:    row.Rounds,
			Source:    row.Source,
			CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
			Envelopes: json.RawMessage(row.Envelopes),
			RunKind:   row.RunKind,
			Variables: row.Variables,
			Status:    row.Status,
			Report:    row.Report,
		}
		if row.ParentRunID.Valid {
			id := util.UUIDToString(row.ParentRunID)
			summary.ParentRunID = &id
		}
		out = append(out, summary)
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
// the stream.
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
	// runner goroutine can still reach it even if the request context
	// is gone by the time persistence runs (observed pre-0.5.59: ctx
	// lookup returned false → silent return → DB never received the
	// row → "推演无反馈").
	setPythiaForecastHandlerFallback(h)
	r.Use(MountPythiaIssueForecastMiddleware(h))
}

// setPythiaForecastHandlerFallback stores the *Handler that
// the runner goroutine falls back to when the chi request context
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
// safety net for the runner's persistence path. Populated by
// AttachPythiaIssueForecastMiddleware at router boot; never mutated
// afterwards. Protected by a Mutex so concurrent reads see a stable
// value.
var (
	pythiaForecastHandlerMu       sync.RWMutex
	pythiaForecastHandlerFallback *Handler
)

func pythiaForecastHandler() *Handler {
	pythiaForecastHandlerMu.RLock()
	defer pythiaForecastHandlerMu.RUnlock()
	return pythiaForecastHandlerFallback
}
