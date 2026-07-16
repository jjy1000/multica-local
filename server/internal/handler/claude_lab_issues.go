// Package handler — claude_lab_issues.go (0.3.29+)
//
// GET /api/experimental/claude-science-lab/issues — Claude Lab tab
// data source. Returns the most recent issues in the caller's active
// workspace tagged with a given lab_source (e.g. 'claude_science_lab').
//
// Why a dedicated endpoint (not reusing /api/issues with a lab filter):
// the experimental lab tab data flow is gated by
// experimental.DefaultFor("claude_science_lab") and the response shape
// is intentionally compact (id / title / number / status / lab_source
// / created_at). Forwarding a TanStack Query call through the issue
// schema would over-fetch the columns the tab doesn't render.
//
// Hard rules:
//
//  1. The route is gated on experimental.DefaultFor("claude_science_lab")
//     in router.go — same chokepoint as the runtime + forecast routes.
//     Off-flag clients see a 404 / connection error rather than 200.
//  2. Lab-source value is restricted to the registry's known flags.
//     A bogus value (e.g. 'sql_injection') returns 200 with an empty
//     list rather than echoing a non-catalog key into the SQL filter
//     (defense in depth — list helpers always pair workspace + tag).
//  3. Workspace membership is enforced via h.workspaceMember before
//     the SELECT.

package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/util"
)

// allowedClaudeLabSources is the closed set of lab_source values the
// Claude Lab tab accepts. Bumping the list requires updating the
// catalog (server/internal/experimental/catalog.go) AND the migration
// that widens the issue.lab_source CHECK — both intentional friction.
var allowedClaudeLabSources = map[string]struct{}{
	"claude_science_lab": {},
	"mythos_swarm":      {},
}

// RegisterClaudeLabIssuesRoute wires the Claude Lab issue-listing
// endpoint onto the supplied router. The caller (router.go) MUST
// gate the entire call on experimental.DefaultFor("claude_science_lab")
// — same flag as the runtime + forecast routes.
func RegisterClaudeLabIssuesRoute(r chi.Router, h *Handler) {
	r.Get("/api/experimental/claude-science-lab/issues", h.GetClaudeLabIssues)
}

// GetClaudeLabIssues returns the most recent issues in the caller's
// active workspace tagged with the supplied `lab` query param. The
// caller must be a member of the workspace.
//
// Query params:
//
//	workspace_id  UUID  — required
//	lab           string — required (must be a known catalog key)
//	limit         int   — optional, capped at 100, default 50
func (h *Handler) GetClaudeLabIssues(w http.ResponseWriter, r *http.Request) {
	wsRaw := r.URL.Query().Get("workspace_id")
	wsID, err := util.ParseUUID(wsRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is required"})
		return
	}
	if _, ok := h.workspaceMember(w, r, wsID.String()); !ok {
		return
	}

	lab := r.URL.Query().Get("lab")
	if lab == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "lab query param is required"})
		return
	}
	if _, ok := allowedClaudeLabSources[lab]; !ok {
		// Unknown / not-yet-registered lab: respond with empty list
		// rather than 400 so a flag-toggle race during install does
		// not 500 the Plan tab. The catalog is the source of truth;
		// an unregistered key here just means no rows qualify.
		writeJSON(w, http.StatusOK, map[string]any{"issues": []any{}, "total": 0, "lab": lab})
		return
	}

	limit := int32(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, perr := strconv.Atoi(raw); perr == nil && n > 0 {
			if n > 100 {
				n = 100
			}
			limit = int32(n)
		}
	}

	rows, err := h.Queries.ListIssuesByLabSource(r.Context(), db.ListIssuesByLabSourceParams{
		WorkspaceID: wsID,
		LabSource:   pgtype.Text{String: lab, Valid: true},
		Limit:       limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issues": rows,
		"total":  len(rows),
		"lab":    lab,
	})
}