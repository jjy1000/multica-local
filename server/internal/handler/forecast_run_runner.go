// Package handler — forecast_run_runner.go (0.5.111)
//
// The detached goroutine behind POST /forecast/issue. One runner per
// run: loops the rounds (each round = oracle pass + full council via the
// engine), persists EVERY round as it lands (the SocialSim "轮完成即时
// 持久化" law — a crash mid-run must leave the completed rounds
// readable), publishes to the run bus, synthesizes the conclusion
// report, writes it back to the issue as the pythia_runtime leader's
// comment (migration 282 idempotency), and flips the row to its
// terminal status.
//
// Lifecycle: the request context dies when the POST returns, so the
// runner uses a DETACHED context (context.WithoutCancel) owned by the
// job. Cancel goes through the bus's registered CancelFunc
// (POST .../runs/{runID}/cancel), not through a client disconnect.

package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// pythiaForecastJob is everything the runner needs, resolved before the
// goroutine starts.
type pythiaForecastJob struct {
	RunID     string       // string form of RunUUID (bus key + logs)
	RunUUID   pgtype.UUID
	IssueID   string
	Workspace string
	RunKind   string       // "initial" | "continuation" (comment header)
	Ifc       *issueForecastContext
	Rounds    int
	History   []forecastHistoryRound
	Variables string
	Seed      int64
}

// startPythiaForecastJob spawns the detached runner. Never blocks the
// HTTP response.
func startPythiaForecastJob(h *Handler, job pythiaForecastJob) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(context.Background()))
	pythiaForecastBus.registerCancel(job.RunID, cancel)
	go runPythiaForecastJob(ctx, cancel, h, job)
}

// runPythiaForecastJob executes the rounds + report + writeback. All
// persistence is best-effort (WRN + continue): a failed write must never
// kill a run whose rounds are still landing.
func runPythiaForecastJob(ctx context.Context, cancel context.CancelFunc, h *Handler, job pythiaForecastJob) {
	runID := job.RunID
	defer cancel()
	defer pythiaForecastBus.removeRun(runID)

	if h == nil || h.Queries == nil {
		slog.Warn("pythia run: no handler/queries — aborting", "run_id", runID)
		pythiaForecastBus.publish(runID, pythiaRunEvent{Type: "status", Status: "failed"})
		return
	}
	// The detached ctx has no values — recover the handler from the
	// package-level fallback stash (same safety net the SSE-era defer
	// used, 0.5.59).
	if h == nil {
		h = pythiaForecastHandler()
	}

	source := issueRoundSourceFor(job.Ifc)
	opts := issueRoundOpts{
		history:     job.History,
		variables:   job.Variables,
		totalRounds: job.Rounds,
	}
	envelopes := make([]forecastEnvelope, 0, job.Rounds)

	for i := 1; i <= job.Rounds; i++ {
		if ctx.Err() != nil {
			pythiaFinalizeStatus(ctx, h, job, "aborted")
			return
		}
		env, err := source(ctx, job.Seed, job.Ifc, i, opts)
		if err != nil {
			// Both the oracle AND the synthetic fallback failed —
			// practically impossible (synthetic never errors), but a
			// panicked round must not wedge the run: skip the round.
			slog.Warn("pythia run: round errored", "run_id", runID, "round", i, "error", err)
			continue
		}
		envelopes = append(envelopes, env)

		// Per-round persistence: crash-safe replay substrate.
		persistCtx, persistCancel := context.WithTimeout(ctx, 3*time.Second)
		payload, marshalErr := json.Marshal(envelopes)
		if marshalErr == nil {
			if _, err := h.Queries.UpdatePythiaForecastRunProgress(persistCtx, dbpkg.UpdatePythiaForecastRunProgressParams{
				ID:        job.RunUUID,
				Rounds:    int32(len(envelopes)),
				Source:    forecastRunSource(envelopes),
				Envelopes: payload,
			}); err != nil {
				slog.Warn("pythia run: progress persist failed",
					"run_id", runID, "round", i, "error", err)
			}
		}
		persistCancel()

		// Publish AFTER persist so a stream reconnecting between the two
		// reads the round from the DB snapshot, never gets a gap.
		envCopy := env
		pythiaForecastBus.publish(runID, pythiaRunEvent{Type: "round", Index: len(envelopes) - 1, Envelope: &envCopy})

		if i < job.Rounds {
			select {
			case <-ctx.Done():
				pythiaFinalizeStatus(ctx, h, job, "aborted")
				return
			case <-time.After(forecastInterval):
			}
		}
	}

	// Conclusion report: engine synthesis first, mechanical fallback
	// second (honesty law preserved via the source note either way).
	report, synthesized := queryOracleIssueReport(ctx, oracleLoopbackURL(), job.Ifc, envelopes, job.Variables)
	if synthesized {
		report = ""
	}

	finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	_, err := h.Queries.FinalizePythiaForecastRun(finalCtx, dbpkg.FinalizePythiaForecastRunParams{
		ID:     job.RunUUID,
		Report: report,
		Status: "completed",
	})
	if err != nil {
		slog.Warn("pythia run: finalize persist failed", "run_id", runID, "error", err)
	}
	finalCancel()

	if report != "" {
		pythiaForecastBus.publish(runID, pythiaRunEvent{Type: "report", Report: report})
	}
	pythiaForecastBus.publish(runID, pythiaRunEvent{Type: "status", Status: "completed"})

	// Issue writeback: the conclusion report lands IN the issue as the
	// pythia_runtime leader's comment (0.5.86 contract, migration 282
	// idempotency marker). Best-effort.
	pythiaWritebackReport(ctx, h, job, envelopes, report)
}

// pythiaWritebackReport posts the comment + records report_comment_id.
func pythiaWritebackReport(ctx context.Context, h *Handler, job pythiaForecastJob, envelopes []forecastEnvelope, report string) {
	issueUUID, err := util.ParseUUID(job.IssueID)
	if err != nil {
		slog.Warn("pythia run: writeback skipped — issue UUID parse failed", "run_id", job.RunID, "error", err)
		return
	}
	wsUUID, err := util.ParseUUID(job.Workspace)
	if err != nil {
		slog.Warn("pythia run: writeback skipped — workspace UUID parse failed", "run_id", job.RunID, "error", err)
		return
	}
	source := forecastRunSource(envelopes)
	content := pythiaIssueReportContent(job.Ifc, envelopes, source, job.RunKind, job.Variables, report)
	wbCtx, wbCancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
	defer wbCancel()
	if commentID := postLabRunReportComment(wbCtx, h, issueUUID, wsUUID, "pythia_runtime", content); commentID.Valid {
		if _, err := h.Queries.SetPythiaForecastRunReportComment(wbCtx, dbpkg.SetPythiaForecastRunReportCommentParams{
			ID:              job.RunUUID,
			ReportCommentID: commentID,
		}); err != nil {
			slog.Warn("pythia run: report_comment_id update failed",
				"run_id", job.RunID, "error", err)
		}
	}
}

// pythiaFinalizeStatus flips the row to a terminal status without a
// report (abort / fail paths).
func pythiaFinalizeStatus(ctx context.Context, h *Handler, job pythiaForecastJob, status string) {
	finCtx, finCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer finCancel()
	if _, err := h.Queries.SetPythiaForecastRunStatus(finCtx, dbpkg.SetPythiaForecastRunStatusParams{
		ID:     job.RunUUID,
		Status: status,
	}); err != nil {
		slog.Warn("pythia run: status persist failed", "run_id", job.RunID, "status", status, "error", err)
	}
	pythiaForecastBus.publish(job.RunID, pythiaRunEvent{Type: "status", Status: status})
}
