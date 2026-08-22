// Package experimental — lock_gc.go (0.5.60).
//
// Orphan sweeper for experimental_resource_lock /
// experimental_resource_visibility (audit P0-3). Claim() is idempotent
// per (source, type, resource_id), so every orphan row proves the
// underlying resource was hard-deleted without releasing its lock:
//
//   - runtime teardown cascade (DeleteArchivedAgentsByRuntime +
//     DeleteSquadsByArchivedAgentsOnRuntime inside DeleteAgentRuntime /
//     ArchiveAgentsAndDeleteRuntime) hard-deletes archived lab agents
//     and their squads;
//   - workspace delete cascades agent/squad/skill/member rows, and
//     lock.resource_id has no FK at all (migration 148).
//
// swarm_gc only releases swarm_run locks, so these tables leaked
// unboundedly (3401/3418 lock rows orphaned at audit time, growing
// ~36-126 rows/day). Migration 274 did the one-shot cleanup; this file
// is the periodic counterpart, wired into the SwarmGC tick (the same
// 6h cadence — see SwarmGC.sweepOrphans).

package experimental

import (
	"context"
	"fmt"
)

// orphanResourceSweeper is the minimal query surface
// SweepOrphanedExperimentalResources needs. Defined where it is used;
// *db.Queries satisfies it once sqlc regenerates.
type orphanResourceSweeper interface {
	DeleteOrphanResourceLocks(ctx context.Context) (int64, error)
	DeleteOrphanResourceVisibilityRows(ctx context.Context) (int64, error)
}

// SweepOrphanedExperimentalResources deletes lock and visibility rows
// whose underlying resource no longer exists. Returns the per-table
// deletion counts so callers can log only when something actually moved.
// Errors are wrapped with the failing table for diagnosability; a lock
// sweep failure aborts the visibility sweep (fail-fast, next tick
// retries both — mirroring the swarm_gc.sweep error posture).
func SweepOrphanedExperimentalResources(ctx context.Context, q orphanResourceSweeper) (locks int64, visibility int64, err error) {
	locks, err = q.DeleteOrphanResourceLocks(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("delete orphan resource locks: %w", err)
	}
	visibility, err = q.DeleteOrphanResourceVisibilityRows(ctx)
	if err != nil {
		return locks, 0, fmt.Errorf("delete orphan resource visibility rows: %w", err)
	}
	return locks, visibility, nil
}
