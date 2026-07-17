// Package experimental_helpers: read-path filters that respect the
// experimental-resource-lock overlay.
//
// WHY THIS FILE EXISTS
//
// The Labs framework (server/internal/experimental) hides lab-owned
// resources when the flag is off. Hiding is implemented purely as a
// "hidden=true" flag on experimental_resource_lock rows; the underlying
// domain tables (skill / agent / squad / member) keep their rows. The
// GUI, CLI, and agent runtime therefore need a read path that filters
// out hidden rows *before* the data reaches the user.
//
// This file is the single place that combines:
//
//   - the sqlc-generated ListVisible*ByWorkspace queries (PR 2), with
//   - the hidden=true predicate, and
//   - the "any lock row exists ⇒ row is locked / can be queried" policy.
//
// Existing ListVisible* variants are preferred over keeping stale
// data on the call site. We do NOT rewrite every handler to translate
// the existing list query into a filtered version inline — the helper
// layer makes the policy obvious in one read.
//
// WHEN TO USE THIS vs. UNFILTERED LISTERS
//
//   - User-facing reads (GUI skill picker, agent picker, sidebar):
//     always Use the ListVisible* variants.
//
//   - System reads (install flow in PR 6, the /api/experimental-resources
//     debug endpoint, CLI tools): use the unfiltered List* variants.
//
//   - Single-row reads (GetSkill, GetAgent, GetSquad): the helper adds
//     a post-load IsHidden check and returns 404 (resource not found)
//     when the row exists but is currently hidden. This keeps the
//     resource "invisible" from the user's point of view without
//     changing the underlying row id.
package helpers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// HideFilter returns true if the lock-overlay query says the row is
// currently visible. A no-lock row returns true (visible). The boolean
// inverse is what the GUI uses as "do not render this".
//
// This is the project's single seam between the lock helpers and the
// list-by-workspace SQL: any future variant (workspace-scoped GPT tools,
// runtime "which MCP servers can a workspace see?", etc.) comes through
// here rather than re-implementing the NOT EXISTS predicate.
type HideFilter struct {
	q *db.Queries
}

// NewHideFilter constructs a HideFilter pinned to a *db.Queries. Caller
// is responsible for matching the request-scoped query; pass q with
// tx in flight, q.WithTx(tx), as appropriate.
func NewHideFilter(q *db.Queries) *HideFilter {
	return &HideFilter{q: q}
}

// IsResourceHidden reports whether the lock overlay currently hides a
// single resource by (src, rt, id). Returns false when no claim exists.
//
// Helpers in this package use this for the post-load "single-row
// fetch returns 404 because the row is hidden" path.
func (h *HideFilter) IsResourceHidden(
	ctx context.Context, src experimental.Source,
	rt experimental.ResourceType, id pgtype.UUID,
) (bool, error) {
	return experimental.IsHidden(ctx, h.q, src, rt, id)
}

// IsSourceKnownErr is returned by the IsResourceHidden helper callers
// when the lock package rejects an unknown Source. Catch this once,
// at the HTTP boundary, and turn it into a 400.
var IsSourceKnownErr = errors.New("hide-filter: unknown experimental source")
