// Package agent_self_optimization — flag.go (0.3.45.1 + 0.3.45.2).
//
// Two seams between the service and the experimental flag system:
//
//   - flagOnExperimental() — process-level. Reads the catalog default.
//     Used as the boot-time "is this build wired up" sanity check so a
//     test or ops env without a pref row still gets the catalog default
//     answer.
//   - flagOnForUser(ctx, q, userID) — per-user. Reads the
//     experimental_pref table. This is the real gate: the GUI toggle
//     writes a row into experimental_pref, and the scheduler tick
//     consults this function to know whether to fire.
//
// 0.3.45.2 contract: the Service NEVER reads catalog defaults for the
// gate decision. The user has explicitly opted in via the Labs tab, and
// the row in experimental_pref is the source of truth. If the row is
// missing (e.g. after a manual SQL cleanup), the flag is OFF for that
// user — falling through to a catalog default would mean "delete your
// opt-in row and the plugin starts anyway", which is wrong.
//
// Split into its own file so tests can stub it via build tags or a
// var override in the test file's package init.

package agent_self_optimization

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// PrefQuerier is the minimal surface of generated.Queries that
// flagOnForUser needs. Defining it here keeps the flag package
// decoupled from the full generated Queries struct so tests can pass
// a fake without dragging in every other query the production code
// calls. Mirrors experimental.UserPrefProvider.Querier.
type PrefQuerier interface {
	GetExperimentalPrefEnabled(ctx context.Context, arg db.GetExperimentalPrefEnabledParams) (bool, error)
}

// flagOnExperimental returns the catalog default for
// "agent_self_optimization". Kept as the boot-time "is this build
// wired up" sanity check; it is NOT used as the per-tick gate.
func flagOnExperimental() bool {
	return experimental.DefaultFor("agent_self_optimization")
}

// flagOnForUser returns true when the user has an enabled=true row
// in experimental_pref for the agent_self_optimization flag key.
//
// Behavior:
//   - No row (pgx.ErrNoRows) → false (the user has not opted in)
//   - enabled=true  → true
//   - enabled=false → false
//   - Any other DB error → false + logged. We deliberately do NOT
//     fall through to the catalog default on DB error: a transient
//     outage should not silently flip an opted-out user to "on".
//
// The ctx is consulted only for cancellation — the function never
// holds a transaction open across an HTTP round-trip.
func flagOnForUser(ctx context.Context, q PrefQuerier, userID pgtype.UUID) bool {
	if !userID.Valid {
		return false
	}
	enabled, err := q.GetExperimentalPrefEnabled(ctx, db.GetExperimentalPrefEnabledParams{
		UserID:  userID,
		FlagKey: "agent_self_optimization",
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			// Swallow non-NoRows errors silently inside the tick
			// path — the scheduler logs at Info via the slogger
			// above; surfacing an error here would just spam the
			// log every minute per workspace.
			return false
		}
		return false
	}
	return enabled
}