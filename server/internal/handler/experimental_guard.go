// experimental_guard.go
//
// Per-request gating for the experimental Labs HTTP surfaces.
//
// Why this exists (0.3.30): the labs routes used to be registered at
// boot time behind `if experimental.DefaultFor(key) { ... }`. That call
// only reads the server-global catalog default (false for every current
// flag) plus the 0.3.18 blacklist — it CANNOT see a per-user runtime
// toggle stored in experimental_pref. Because chi builds its route table
// once at startup, a user enabling a lab from the GUI could never cause
// the corresponding route to be registered, so every backend call 404'd
// even though the frontend rendered the surface. The frontend already
// evaluates flags per-user (pickEnabled over experimental_pref); this
// guard closes the asymmetry on the backend.
//
// The guard preserves the Labs safety contract:
//
//   - A flag that is OFF for the caller returns 404 — indistinguishable
//     from a route that does not exist, so flag-off users cannot
//     enumerate the surface.
//   - A blacklisted flag (experimental.IsBroken) is forced off no matter
//     what the stored preference says, keeping the 0.3.18 auto-disable
//     safety net authoritative.
//   - The guard runs before any handler logic, so an off-flag request
//     never touches the new code path.
//
// The routes it protects MUST be mounted inside the authenticated group
// (router.go), because the decision — and the handlers themselves —
// depend on the X-User-ID header that middleware.Auth injects.

package handler

import (
	"context"
	"net/http"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RequireExperimentalFlag returns middleware that admits the request only
// when the given catalog flag is enabled for the calling user. When the
// flag is off it writes a 404 and does not call the next handler.
func (h *Handler) RequireExperimentalFlag(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !experimentalFlagEnabled(r.Context(), h.Queries, requestUserID(r), key) {
				// 404 (not 403) so an off-flag caller cannot tell a
				// gated-off surface apart from a nonexistent route.
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// experimentalFlagEnabled resolves whether `key` is enabled for `userID`.
//
// Resolution order mirrors the frontend's pickEnabled semantics with the
// blacklist layered on top:
//
//  1. Blacklisted flag → off (safety net wins over any stored preference).
//  2. Stored per-user preference → its value (explicit opt-in / opt-out).
//  3. No stored preference / unresolved caller → catalog default.
//
// A DB read error other than "no rows" is treated the same as "no rows":
// we fall through to the catalog default (off for every current flag)
// rather than hard-failing the request. This matches the conservative
// default and avoids a transient DB blip surfacing a lab that the user
// never opted into.
//
// q is taken as the narrow experimental.Querier interface so the decision
// logic is unit-testable with a fake and stays decoupled from the full
// generated Queries struct.
func experimentalFlagEnabled(ctx context.Context, q experimental.Querier, userID, key string) bool {
	if _, broken := experimental.IsBroken(key); broken {
		return false
	}
	if userID != "" && q != nil {
		row, err := q.GetExperimentalPref(ctx, db.GetExperimentalPrefParams{
			UserID:  parseUUID(userID),
			FlagKey: key,
		})
		if err == nil {
			return row.Enabled
		}
	}
	return experimental.DefaultFor(key)
}
