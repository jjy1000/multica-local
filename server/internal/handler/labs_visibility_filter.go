package handler

// Labs visibility gate — shared list-filter helper.
//
// Before 0.3.20 every list handler (ListAgents / ListAutopilots /
// ListSkills) had to inline a 17-line "if !DefaultFor → lookup hidden
// IDs → build set → filter in place" block for every Labs flag whose
// resources it gates. Two flags (agent_self_optimization +
// constitution_agent) meant the same block landed 5 times across 3
// files with only the flag key, resource type, and ID-extractor
// changing.
//
// FilterHiddenByFlagByDefault collapses the boilerplate into one
// generics-based call site:
//
//	agents = filterLabsHiddenByDefault(
//	    r.Context(), h.Queries, agents,
//	    "agent_self_optimization", experimental.HideAgent,
//	    func(a db.Agent) pgtype.UUID { return a.ID },
//	    "list agents: resolve hidden set failed",
//	)
//
// Hard contract:
//
//   - Returns the original slice (unfiltered) when the flag is ON — the
//     gate is bypassed entirely. This matches the catalog DefaultVal=false
//     contract: ON means "show everything".
//   - Returns the original slice on any error path (fail-open). The list
//     handler logs the error with the supplied prefix so ops can spot
//     a misconfigured visibility table; the user still sees their roster.
//   - When the flag is OFF AND the hidden set is empty (no rows seeded
//     for this flag), returns the original slice unchanged — avoids
//     allocating a filter slice for the common "no opt-ins yet" case.
//
// The fail-open path is deliberate: the visibility table is read at
// every list request; if it errors (PG hiccup, schema drift) we would
// rather show the (possibly hidden) resource than drop the user's
// roster. The flag-off path also doesn't strictly need the table at
// all when no resources are seeded for it, so we early-return before
// the SELECT to keep the hot path cheap.
//
// Go 1.26 generics. The constraints are minimal (any slice of T works)
// because the helper doesn't need anything beyond the extract function.

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// filterLabsHiddenByDefault filters out items whose IDs are listed in
// the visibility table for (flagKey, kind) when the flag is OFF. Pass
// an extract function that returns the row's ID field as pgtype.UUID
// (the canonical sqlc representation).
//
// flagKey must match a Catalog entry; otherwise experimental.DefaultFor
// returns false and we run the gate unconditionally — same as if the
// catalog default were OFF. That's the desired safety posture: an
// unknown flagKey can never bypass the visibility filter.
//
// logOnErr is the slog.Warn message used when the visibility lookup
// fails; include enough context for an operator to know which list
// endpoint degraded to fail-open. A typical value is
// `"list agents: resolve hidden set failed"`.
func filterLabsHiddenByDefault[T any](
	ctx context.Context,
	q *db.Queries,
	items []T,
	flagKey string,
	kind experimental.HideableResource,
	extractID func(T) pgtype.UUID,
	logOnErr string,
) []T {
	if experimental.DefaultFor(flagKey) {
		// Flag is on (or no user override and catalog default true):
		// show everything. The whole SELECT + set build is skipped.
		return items
	}
	if !experimental.IsKnownKey(flagKey) {
		// Safety: an unknown flag key is equivalent to "nobody opted
		// in yet", not "permission to show everything". Returning the
		// input unchanged matches the empty-hidden-set short-circuit
		// below — both paths mean "no resources are gated by this
		// flag, fall through". Without this check the helper would
		// query the visibility table with a flagKey that can never
		// have rows, wasting a roundtrip and potentially logging a
		// confusing empty-result warning.
		return items
	}
	hidden, err := experimental.HiddenResourceIDsByFlag(ctx, q, flagKey, kind)
	if err != nil {
		// Fail-open: log and return the unfiltered slice so the user
		// keeps seeing their roster. Visibility is a UX concern, not
		// an authorization concern — surfacing a hidden resource is
		// strictly less bad than refusing to list at all.
		slog.Warn(logOnErr, "flag", flagKey, "err", err)
		return items
	}
	if len(hidden) == 0 {
		// No rows seeded for this flag yet. Short-circuit before the
		// filter allocation; this is the common case for every flag
		// that ships without any opt-in resources.
		return items
	}
	hiddenSet := make(map[uuid.UUID]struct{}, len(hidden))
	for _, id := range hidden {
		hiddenSet[id] = struct{}{}
	}
	// Allocate a fresh slice rather than reusing items[:0]: an in-place
	// filter would rewrite the caller's backing array, so a caller that
	// still holds the original slice (e.g. for a pre-filter count or a
	// second pass) would observe silently truncated data. The allocation
	// is negligible next to the SQL round-trip the caller just made.
	filtered := make([]T, 0, len(items))
	for _, item := range items {
		id := extractID(item)
		if _, skip := hiddenSet[uuid.UUID(id.Bytes)]; skip {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

// labManagedSet returns the resource IDs the visibility table marks as
// lab-managed for the given resource type — resources that belong to some
// Labs flag and must never be selectable as standalone actors. The set is
// keyed by the raw 16-byte UUID so callers can test membership against a
// sqlc pgtype.UUID's .Bytes without importing google/uuid. Flag-state and
// `hidden`-column independent by design (see the ListLabManagedResourceIDs
// query comment): a row's mere existence means "lab infrastructure".
//
// Fail-open, like the filter above: on lookup error we log and return an
// empty set so the list degrades to today's behaviour (lab rows left
// unmarked, still selectable) instead of erroring the whole list. The
// marker is a UX affordance, not an authorization boundary.
func labManagedSet(
	ctx context.Context,
	q *db.Queries,
	kind experimental.HideableResource,
) map[[16]byte]struct{} {
	set := map[[16]byte]struct{}{}
	if q == nil {
		return set
	}
	rows, err := q.ListLabManagedResourceIDs(ctx, string(kind))
	if err != nil {
		slog.Warn("list lab-managed ids failed; leaving rows unmarked", "kind", string(kind), "err", err)
		return set
	}
	for _, id := range rows {
		if id.Valid {
			set[id.Bytes] = struct{}{}
		}
	}
	return set
}
