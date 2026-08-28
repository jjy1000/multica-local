// Package causalgraph — nightly evolver (0.5.83 WL3 S2, roadmap
// §3.3 item 14). Registered as the scheduler JobSpec
// "causal_graph_evolver" (jobs_causal_graph.go), cadence 24h,
// catch-up latest-only.
//
// Two phases, both tier-D gated (status='suggested', confidence
// capped at 0.5, human confirm required):
//
//  1. curator scan — deterministic depends_on extraction over the
//     comment window since the last successful run (curator.go).
//  2. gap fill — transitive depends_on shortcuts: when ACTIVE edges
//     A→B and B→C exist but no A→C in ANY status, propose A→C at
//     min(confidence) capped to 0.5, proposed_by='evolver'. Rejected
//     tombstones (mig 280) keep the pair silent forever (ICP-5).
//
// The run window comes from the scheduler's own audit rows
// (sys_cron_executions, status='SUCCESS') — no extra state table.
// First-ever run falls back to a bounded 48h lookback. Idempotent by
// construction: the scan's edge probes collapse replays, and the gap
// fill only ever writes after an any-status probe miss.
//
// This JobSpec supersedes the roadmap's optional "1 autopilot" row
// for the hidden team (documented deviation, install_causal_graph.go).
package causalgraph

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// EvolverTuning holds the evolver's knobs (exported for tests).
type EvolverTuning struct {
	// FirstRunLookback is the scan window when no prior SUCCESS run
	// exists. Defaults to 48h.
	FirstRunLookback time.Duration
	// WindowOverlap re-scans this much of the previous window so a
	// comment written while a run was in flight is not missed.
	// Dedup probes make the overlap free. Defaults to 10m.
	WindowOverlap time.Duration
	// GapFillCap is the hard confidence ceiling for gap-fill
	// proposals (the tier-D ceiling). Defaults to 0.5.
	GapFillCap float64
}

func (t EvolverTuning) withDefaults() EvolverTuning {
	if t.FirstRunLookback <= 0 {
		t.FirstRunLookback = 48 * time.Hour
	}
	if t.WindowOverlap <= 0 {
		t.WindowOverlap = 10 * time.Minute
	}
	if t.GapFillCap <= 0 || t.GapFillCap > 0.5 {
		t.GapFillCap = 0.5
	}
	return t
}

// Evolver orchestrates the nightly pass.
type Evolver struct {
	Pool    *pgxpool.Pool
	Queries *db.Queries
	Tuning  EvolverTuning

	curator *Curator
}

func NewEvolver(pool *pgxpool.Pool, q *db.Queries, tuning EvolverTuning) *Evolver {
	return &Evolver{
		Pool:    pool,
		Queries: q,
		Tuning:  tuning.withDefaults(),
		curator: NewCurator(pool, q),
	}
}

// RunOnce executes one full pass and returns the audit summary for
// the scheduler row. The flag gate is fail-closed: with the
// causal_graph flag off the pass is a no-op (zero writes).
func (e *Evolver) RunOnce(ctx context.Context) (map[string]any, error) {
	v, err := e.Queries.FlagEnabledForAnyUser(ctx, FlagKeyCausalGraph)
	if err != nil || !v {
		return map[string]any{"skipped": "flag_off"}, nil
	}
	ctx = context.WithoutCancel(ctx)

	until := time.Now().UTC()
	since, err := e.lastSuccess(ctx)
	if err != nil {
		return nil, err
	}
	if since.IsZero() {
		since = until.Add(-e.Tuning.FirstRunLookback)
	} else {
		since = since.Add(-e.Tuning.WindowOverlap)
	}

	scanned, err := e.curator.ScanWindow(ctx, since, until)
	if err != nil {
		// A broken scan must not block the gap fill — the phases are
		// independent and both are tier-D gated.
		slog.Warn("causal evolver: curator scan failed", "error", err)
	}

	gapFilled, err := e.gapFill(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"scan_since":    since.UTC().Format(time.RFC3339),
		"scan_until":    until.UTC().Format(time.RFC3339),
		"scan_proposed": scanned,
		"gap_proposed":  gapFilled,
	}, nil
}

// lastSuccess reads the newest SUCCESS finish time for this job from
// the scheduler's audit rows.
func (e *Evolver) lastSuccess(ctx context.Context) (time.Time, error) {
	var t pgtype.Timestamptz
	err := e.Pool.QueryRow(ctx, `
		SELECT max(finished_at) FROM sys_cron_executions
		WHERE job_name = $1 AND status = 'SUCCESS' AND finished_at IS NOT NULL
	`, JobNameCausalGraphEvolver).Scan(&t)
	if err != nil {
		return time.Time{}, err
	}
	if !t.Valid {
		return time.Time{}, nil
	}
	return t.Time, nil
}

// gapFill proposes transitive depends_on shortcuts over ACTIVE edges.
type gapCandidate struct {
	workspaceID pgtype.UUID
	fromID      pgtype.UUID
	midID       pgtype.UUID
	toID        pgtype.UUID
	confA       pgtype.Numeric
	confB       pgtype.Numeric
}

func (e *Evolver) gapFill(ctx context.Context) (int, error) {
	rows, err := e.Pool.Query(ctx, `
		SELECT a.workspace_id, a.from_node_id, a.to_node_id, b.to_node_id,
		       a.confidence, b.confidence
		FROM causal_edge a
		JOIN causal_edge b
		  ON b.from_node_id = a.to_node_id
		 AND b.type = 'depends_on' AND b.status = 'active'
		WHERE a.type = 'depends_on' AND a.status = 'active'
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var candidates []gapCandidate
	for rows.Next() {
		var g gapCandidate
		if err := rows.Scan(&g.workspaceID, &g.fromID, &g.midID, &g.toID, &g.confA, &g.confB); err != nil {
			continue
		}
		candidates = append(candidates, g)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	proposed := 0
	for _, g := range candidates {
		if util.UUIDToString(g.fromID) == util.UUIDToString(g.toID) {
			continue // A→B→A cycle — a shortcut would be a self-loop
		}
		if _, err := e.Queries.FindCausalEdgeBetween(ctx, db.FindCausalEdgeBetweenParams{
			FromNodeID: g.fromID,
			ToNodeID:   g.toID,
			EdgeType:   "depends_on",
		}); err == nil {
			continue // decided before (active or tombstone) — silent
		}
		conf := gapConfidence(g.confA, g.confB, e.Tuning.GapFillCap)
		if _, err := e.Queries.CreateCausalEdge(ctx, db.CreateCausalEdgeParams{
			WorkspaceID: g.workspaceID,
			FromNodeID:  g.fromID,
			ToNodeID:    g.toID,
			EdgeType:    "depends_on",
			Confidence:  conf,
			Provenance: mustJSON(map[string]string{
				"source":    "evolver_gap_fill",
				"dedup_key": "evolver_gap:" + util.UUIDToString(g.fromID) + ":" + util.UUIDToString(g.toID),
				"via":       util.UUIDToString(g.midID),
			}),
			CreatedBy:  pgtype.Text{Valid: true, String: "system"},
			ProposedBy: pgtype.Text{Valid: true, String: "evolver"},
			EdgeStatus: pgtype.Text{Valid: true, String: "suggested"},
		}); err != nil {
			slog.Warn("causal evolver: gap-fill write failed",
				"from", util.UUIDToString(g.fromID),
				"to", util.UUIDToString(g.toID),
				"error", err)
			continue
		}
		proposed++
	}
	return proposed, nil
}

// gapConfidence computes min(a, b) capped at the ceiling, treating
// NULL confidence as 1.0 (dependency edges created without one). The
// 0.8 attenuation marks the shortcut as weaker evidence than either
// hop; the cap enforces the tier-D ceiling.
func gapConfidence(a, b pgtype.Numeric, cap float64) pgtype.Numeric {
	val := func(n pgtype.Numeric) float64 {
		if !n.Valid {
			return 1.0
		}
		f, _ := n.Float64Value()
		if !f.Valid {
			return 1.0
		}
		return f.Float64
	}
	m := val(a)
	if bv := val(b); bv < m {
		m = bv
	}
	m = m * 0.8
	if m > cap {
		m = cap
	}
	if m < 0 {
		m = 0
	}
	var out pgtype.Numeric
	_ = out.Scan(trimFloat(m))
	return out
}

// trimFloat renders a confidence with three decimals (NUMERIC(4,3)).
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 3, 64)
}

// JobNameCausalGraphEvolver is the canonical audit key for the nightly
// pass (sys_cron_executions.job_name). Stable across releases — do
// not rename without a migration.
const JobNameCausalGraphEvolver = "causal_graph_evolver"
