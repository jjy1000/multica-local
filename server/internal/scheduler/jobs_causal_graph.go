// jobs_causal_graph.go — nightly causal-graph evolution job (0.5.83
// WL3 S2). Thin JobSpec wrapper over causalgraph.Evolver, registered
// in cmd/server/main.go beside TaskUsageHourlyJob. All business logic
// (flag gate, window, curator scan, gap fill) lives in
// service/causal_graph/evolver.go.
package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	causalgraph "github.com/multica-ai/multica/server/internal/service/causal_graph"
)

// CausalGraphEvolverJob returns the JobSpec for the nightly tier-D
// evolution pass. Canonical settings:
//
//	cadence:               24h (roadmap: nightly)
//	schedule_delay:        1h after boot
//	catch_up_mode:         latest_only (missed nights collapse to one)
//	run_timeout:           10m
//	stale_timeout:         15m
//	heartbeat_interval:    30s
//	max_attempts:          3
//	retry_backoff:         1m, 5m, 15m
//
// The run window (which comments to scan) is derived inside the
// handler from the job's own SUCCESS audit rows — first-ever run
// falls back to a bounded 48h lookback (evolver.go).
func CausalGraphEvolverJob(pool *pgxpool.Pool, evolver *causalgraph.Evolver) JobSpec {
	return JobSpec{
		Name:              causalgraph.JobNameCausalGraphEvolver,
		Cadence:           24 * time.Hour,
		ScheduleDelay:     1 * time.Hour,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        10 * time.Minute,
		StaleTimeout:      15 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		MaxAttempts:       3,
		RetryBackoff: []time.Duration{
			1 * time.Minute,
			5 * time.Minute,
			15 * time.Minute,
		},
		Scopes: StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
			result, err := evolver.RunOnce(ctx)
			if err != nil {
				return HandlerResult{}, err
			}
			if in.Heartbeat != nil {
				_ = in.Heartbeat(ctx)
			}
			return HandlerResult{Result: result}, nil
		},
	}
}
