package experimental

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// Querier is the minimal surface of generated.Queries that UserPrefProvider
// needs. Defining it here keeps the provider decoupled from the full
// generated Queries struct so tests can pass a fake without dragging in
// every other query the production code calls.
type Querier interface {
	GetExperimentalPref(ctx context.Context, arg db.GetExperimentalPrefParams) (db.ExperimentalPref, error)
}

// UserPrefProvider is the per-user feature flag provider that reads the
// experimental_pref table.
//
// It implements featureflag.Provider. The contract:
//
//   - Lookup returns (Decision{}, false) when no row exists for the
//     (user, key) pair — this is the most common case and lets the
//     chain fall through to whatever comes next (typically the catalog
//     default).
//   - Lookup returns a populated Decision with Reason=ReasonStatic and
//     Source="user_pref" when the user has explicitly toggled the flag.
//   - A DB error does NOT short-circuit to (zero, false). Instead it
//     returns Reason=ReasonError with found=true so the chain stops at
//     this layer and the Service can log the failure; we never silently
//     downgrade a stored user preference to the catalog default —
//     otherwise a DB outage would silently revert every opt-in.
//
// Per-user state is intentionally NOT stored in the Provider itself;
// pass the caller identity into Lookup via EvalContext. This keeps the
// Provider safe for concurrent use across requests (the Service may
// share one instance across many goroutines).
type UserPrefProvider struct {
	q Querier
}

// NewUserPrefProvider returns a provider backed by q. A nil q is
// allowed and degrades to (zero, false) on every Lookup, which is
// the right behavior for unit tests that don't care about prefs.
func NewUserPrefProvider(q Querier) *UserPrefProvider {
	return &UserPrefProvider{q: q}
}

// Name implements featureflag.Provider. The value is consumed by
// diagnostic endpoints and the chain log so operators can tell which
// layer produced a given decision.
func (*UserPrefProvider) Name() string { return "user_pref" }

// Lookup implements featureflag.Provider. The caller identity comes from
// the EvalContext attached to ctx via featureflag.WithEvalContext;
// without it, the provider always misses and the chain falls through.
func (p *UserPrefProvider) Lookup(ctx context.Context, key string) (featureflag.Decision, bool) {
	if p == nil || p.q == nil {
		return featureflag.Decision{}, false
	}

	ec := featureflag.EvalContextFrom(ctx)
	userIDStr, ok := ec.Lookup("user_id")
	if !ok || userIDStr == "" {
		// No caller identity → we cannot answer per-user. Returning
		// (zero, false) is correct: the next provider in the chain
		// (typically the catalog default via the Service) will produce
		// the right answer.
		return featureflag.Decision{}, false
	}
	userID, err := parseUUID(userIDStr)
	if err != nil {
		// A malformed user_id in the EvalContext is almost certainly
		// a programming bug in the caller. Return Reason=ReasonError
		// so the failure is visible in logs without masking the
		// caller's mistake as "no opinion".
		return featureflag.Decision{
			Key:     key,
			Enabled: false,
			Variant: "off",
			Reason:  featureflag.ReasonError,
			Source:  p.Name(),
		}, true
	}

	row, err := p.q.GetExperimentalPref(ctx, db.GetExperimentalPrefParams{
		UserID:  userID,
		FlagKey: key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return featureflag.Decision{}, false
		}
		// Treat any other DB error as Reason=ReasonError with found=true.
		// The Service logs it and returns the user's preference flag
		// (Enabled) as the default, but the labs UI specifically keys
		// off Reason to decide whether to refresh.
		return featureflag.Decision{
			Key:     key,
			Enabled: false,
			Variant: "off",
			Reason:  featureflag.ReasonError,
			Source:  p.Name(),
		}, true
	}

	variant := "off"
	if row.Enabled {
		variant = "on"
	}
	return featureflag.Decision{
		Key:     key,
		Enabled: row.Enabled,
		Variant: variant,
		Reason:  featureflag.ReasonStatic,
		Source:  p.Name(),
	}, true
}

// parseUUID is a small adapter that turns the EvalContext's free-form
// string into a pgtype.UUID without forcing callers to depend on the
// internal util package. Mirrors util.ParseUUID semantics but keeps
// the experimental package self-contained.
func parseUUID(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if len(s) != 36 {
		return id, errors.New("invalid uuid length")
	}
	if err := id.Scan(s); err != nil {
		return id, err
	}
	return id, nil
}