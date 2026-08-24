// Package handler — experimental_mythos_run.go (0.3.28 PR-4)
//
// HTTP endpoint that drives the server-side Mythos RDT runner.
//
// Wire:
//
//   POST /api/experimental/mythos-swarm/run
//   body: {"problem": "...", "root_issue_id": "...", "max_loop_iters": 3}
//
//   200 OK {"run_id":"...","coda_summary":"...","iterations_run":N,
//           "convergence_history":[...], "final_issue_id":"..."}
//
// 0.3.28 PR-4: each loop iteration now forks a real sub-issue assigned
// to a loop-member agent (was a synthetic string stub). The coda
// stage also forks a real sub-issue assigned to the coda agent. The
// runner blocks via a 60s status-poll waitFn so the daemon's
// completion comment is captured as the iter body — convergence is now
// driven by actual agent output, not a hard-coded answer.
//
// Hard-cap ceiling (5) and per-workspace rate limit (1 run / 5min)
// are both enforced at the HTTP boundary; the runner package
// re-enforces MaxLoopItersHardCap as a defence-in-depth check.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service/mythos"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Mythos run-time knobs (handler-side enforcement).
//
// MaxLoopHardCap is the upper bound on the user's
// req.MaxLoopIters field. Mirrored as mythos.MaxLoopItersHardCap so
// the runner enforces the same ceiling regardless of caller path.
const (
	mythosMaxLoopHardCap   = 5               // 0.3.28 PR-4 (was 16)
	mythosRateLimitWindow  = 5 * time.Minute // 1 run per workspace per 5 min
	mythosRateLimitMaxRuns = 1
	mythosWaitPollInterval = 1 * time.Second
	mythosWaitTimeout      = 60 * time.Second
)

// mythosRateLimiter is a per-workspace sliding-window limiter. The
// state is in-memory; a multi-replica deployment would need a
// Redis-backed equivalent (mirrors the pattern in webhook_rate_limiter.go)
// but for the current single-binary server pod that ships, in-memory
// is sufficient.
type mythosRateLimiter struct {
	mu  sync.Mutex
	hit map[string][]time.Time
}

func newMythosRateLimiter() *mythosRateLimiter {
	return &mythosRateLimiter{hit: make(map[string][]time.Time)}
}

// Allow returns true if the workspace is within its 1 run/5min budget.
// Trims stale entries before counting so the window is sliding.
func (l *mythosRateLimiter) Allow(workspaceID string) bool {
	now := time.Now()
	cutoff := now.Add(-mythosRateLimitWindow)
	l.mu.Lock()
	defer l.mu.Unlock()

	hits := l.hit[workspaceID]
	keep := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= mythosRateLimitMaxRuns {
		l.hit[workspaceID] = keep
		return false
	}
	keep = append(keep, now)
	l.hit[workspaceID] = keep
	return true
}

// retryAfter returns the time until the oldest in-window hit ages out
// of the budget. Used as the HTTP Retry-After header value.
func (l *mythosRateLimiter) retryAfter(workspaceID string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	hits := l.hit[workspaceID]
	if len(hits) == 0 {
		return 0
	}
	oldest := hits[0]
	return time.Until(oldest.Add(mythosRateLimitWindow))
}

// mythosRunLimiter is the package-level singleton rate limiter for the
// mythos_swarm run endpoint. In-memory; sufficient for the current
// single-binary server pod. Lazily constructed on first request so the
// import cost is paid only when the flag is actually on.
var (
	mythosRunLimiterOnce sync.Once
	mythosRunLimiter     *mythosRateLimiter
)

func getMythosRunLimiter() *mythosRateLimiter {
	mythosRunLimiterOnce.Do(func() {
		mythosRunLimiter = newMythosRateLimiter()
	})
	return mythosRunLimiter
}

// retryAfterString formats a duration as an HTTP Retry-After integer
// seconds value (RFC 7231 §7.1.3). Floors to 1s so the header is
// always at least the minimum.
func retryAfterString(d time.Duration) string {
	if d <= 0 {
		return "1"
	}
	secs := int(d.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

// mythosWaitFn returns a waitFn suitable for mythos.Service.Run that
// blocks until the agent has closed the sub-issue. Polls the issue
// status every mythosWaitPollInterval; returns the latest comment body
// on completion or an error on timeout. workspaceUUID is captured
// from the original request so ListCommentsForIssue can scope the
// comment query.
//
// The shape matches the existing IssueCompletionNotifier contract the
// runner package documents in its godoc: callers MUST provide a
// waitFn per iteration and the runner MUST treat a nil waitFn as the
// skip-blocking case (used by tests / legacy clients).
func (h *Handler) mythosWaitFn(workspaceUUID pgtype.UUID) func(context.Context, pgtype.UUID) (string, error) {
	return func(issueCtx context.Context, issueID pgtype.UUID) (string, error) {
		if !issueID.Valid {
			return "", nil
		}
		// Bound the wait so a stalled daemon can't deadlock the HTTP
		// request.
		waitCtx, cancel := context.WithTimeout(issueCtx, mythosWaitTimeout)
		defer cancel()

		ticker := time.NewTicker(mythosWaitPollInterval)
		defer ticker.Stop()

		var lastBody string
		for {
			issue, err := h.Queries.GetIssue(waitCtx, issueID)
			if err == nil {
				// Capture the latest agent comment so the convergence
				// signal reads what the agent actually produced.
				// ListCommentsForIssue is ASC; we read up to 2000
				// and take the tail (queries/comment.sql:7).
				if comments, cerr := h.Queries.ListCommentsForIssue(waitCtx, db.ListCommentsForIssueParams{
					IssueID:     issueID,
					WorkspaceID: workspaceUUID,
					Limit:       2000,
				}); cerr == nil && len(comments) > 0 {
					lastBody = comments[len(comments)-1].Content
				}
				switch issue.Status {
				case "done", "cancelled":
					return lastBody, nil
				}
			}
			select {
			case <-waitCtx.Done():
				return lastBody, waitCtx.Err()
			case <-ticker.C:
			}
		}
	}
}

// MythosRunRequest is the POST body.
type MythosRunRequest struct {
	Problem      string `json:"problem"`
	RootIssueID  string `json:"root_issue_id"`
	MaxLoopIters int    `json:"max_loop_iters"`
}

// MythosRunResponse is the JSON body on 2xx.
type MythosRunResponse struct {
	RunID              string    `json:"run_id"`
	RootIssueID        string    `json:"root_issue_id,omitempty"`
	FinalIssueID       string    `json:"final_issue_id,omitempty"`
	CodaSummary        string    `json:"coda_summary"`
	IterationsRun      int       `json:"iterations_run"`
	ConvergenceHistory []float64 `json:"convergence_history"`
}

// RunMythosSwarm drives the three-stage RDT pipeline once.
//
// Auth: any workspace member. Per-user flag gating lives in the
// router middleware (RequireExperimentalFlag) — this handler does
// NOT re-check via experimental.DefaultFor because DefaultFor reads
// Catalog.DefaultVal only and ignores experimental_pref rows, which
// would 404 every per-user enabled lab. (0.5.61 audit fix.)
func (h *Handler) RunMythosSwarm(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req MythosRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Problem == "" {
		writeError(w, http.StatusBadRequest, "problem is required")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	var userUUID pgtype.UUID
	if err := userUUID.Scan(userID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	prelude, loopIDs, coda, err := h.lookupMythosAgents(r, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up Mythos agents: "+err.Error())
		return
	}
	if !prelude.Valid {
		writeError(w, http.StatusBadRequest, "no Mythos prelude agent installed; enable mythos_swarm first")
		return
	}

	var rootUUID pgtype.UUID
	if req.RootIssueID != "" {
		parsed, perr := uuid.Parse(req.RootIssueID)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid root_issue_id")
			return
		}
		rootUUID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	maxLoop := req.MaxLoopIters
	if maxLoop == 0 {
		maxLoop = 3
	}
	// 0.3.28 PR-4: hard ceiling lowered from 16 → 5. The runner also
	// caps to mythos.MaxLoopItersHardCap, this is the
	// user-facing clamp.
	if maxLoop > mythosMaxLoopHardCap {
		maxLoop = mythosMaxLoopHardCap
	}

	workspaceUUID := pgtype.UUID{}
	if err := workspaceUUID.Scan(workspaceID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	// 0.3.28 PR-4: per-workspace rate limit. Cheap in-memory
	// sliding-window limiter; 1 run per workspace per 5 minutes.
	limiter := getMythosRunLimiter()
	if !limiter.Allow(workspaceID) {
		retry := limiter.retryAfter(workspaceID)
		w.Header().Set("Retry-After", retryAfterString(retry))
		writeError(w, http.StatusTooManyRequests,
			"mythos_swarm: rate limit exceeded; retry after "+retryAfterString(retry))
		return
	}

	cfg := mythos.Config{
		WorkspaceID:          workspaceUUID,
		CreatorUserID:        userUUID,
		Problem:              req.Problem,
		MaxLoopIters:         maxLoop,
		ConvergenceThreshold: 0.95,
		PreludeAgentID:       prelude,
		LoopAgentIDs:         loopIDs,
		CodaAgentID:          coda,
		SubIssuePrefix:       "[mythos] ",
	}
	if rootUUID.Valid {
		if rootIssue, err := h.Queries.GetIssue(r.Context(), rootUUID); err == nil {
			cfg.WorkspaceID = rootIssue.WorkspaceID
			workspaceUUID = rootIssue.WorkspaceID
		}
	}

	// Wait until the daemon closes each forked sub-issue. Status poll;
	// 60s per-iter timeout. A non-completion yields empty body so the
	// runner's synthetic fallback kicks in and the convergence signal
	// stays defined.
	//
	// 0.3.68: run through the boot-wired h.MythosService (the same
	// instance the supervise HTTP handlers and ResumeSupervision use).
	// The previous per-request mythos.NewService meant enhancer-mode
	// supervise goroutines registered on a throwaway superviseSet that
	// Stop() and the idempotent cancel-replace guard could never see.
	// The nil fallback keeps bare-Handler tests (no router wiring)
	// working.
	svc := h.MythosService
	if svc == nil {
		svc = mythos.NewService(h.Queries, h.TaskService)
	}
	res, runErr := svc.Run(
		r.Context(), cfg, h.mythosWaitFn(workspaceUUID),
	)
	if runErr != nil {
		writeError(w, http.StatusInternalServerError, runErr.Error())
		return
	}
	if res == nil {
		writeError(w, http.StatusInternalServerError, "runner returned nil result")
		return
	}

	if rootUUID.Valid && res.CodaSummary != "" {
		_, _ = h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
			IssueID:    rootUUID,
			AuthorType: "user",
			AuthorID:   userUUID,
			Content:    res.CodaSummary,
			Type:       "comment",
			ParentID:   pgtype.UUID{},
		})
	}
	if rootUUID.Valid {
		_ = h.Queries.SetMythosRunRootIssue(r.Context(), db.SetMythosRunRootIssueParams{
			ID:          pgtype.UUID{Bytes: res.RunID, Valid: true},
			RootIssueID: rootUUID,
		})
	}

	resp := MythosRunResponse{
		RunID:              res.RunID.String(),
		CodaSummary:        res.CodaSummary,
		IterationsRun:      res.IterationsRun,
		ConvergenceHistory: res.ConvergenceHistory,
	}
	if rootUUID.Valid {
		resp.RootIssueID = req.RootIssueID
	}
	if res.FinalIssueID.Valid {
		resp.FinalIssueID = uuidToString(res.FinalIssueID)
	}
	writeJSON(w, http.StatusOK, resp)
}

// lookupMythosAgents resolves prelude / loop / coda agent IDs in the
// active workspace by name. Missing names are non-fatal (the loop
// list may be empty if the install was previously rolled back).
func (h *Handler) lookupMythosAgents(r *http.Request, workspaceID string) (pgtype.UUID, []pgtype.UUID, pgtype.UUID, error) {
	var ws pgtype.UUID
	if err := ws.Scan(workspaceID); err != nil {
		return pgtype.UUID{}, nil, pgtype.UUID{}, err
	}
	resolve := func(name string) (pgtype.UUID, error) {
		row, err := h.Queries.GetAgentByWorkspaceAndName(r.Context(), db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: ws,
			Name:        name,
		})
		if err != nil {
			return pgtype.UUID{}, nil // missing name → non-fatal
		}
		return row.ID, nil
	}
	prelude, err := resolve("mythos_prelude")
	if err != nil {
		return pgtype.UUID{}, nil, pgtype.UUID{}, err
	}
	coda, err := resolve("mythos_coda")
	if err != nil {
		return pgtype.UUID{}, nil, pgtype.UUID{}, err
	}
	var loop []pgtype.UUID
	for _, n := range []string{"mythos_loop_researcher", "mythos_loop_coder"} {
		id, _ := resolve(n)
		if id.Valid {
			loop = append(loop, id)
		}
	}
	return prelude, loop, coda, nil
}
