// Package handler — semantica_decisions.go (0.5.56 P4)
//
// Fork-side ACL-filtered read endpoint for the semantica flag.
// Returns the decision records the current viewer is permitted to
// see in the given workspace, using the ACL table introduced in
// migration 273.
//
// Why a fork-side endpoint and not just GET /experimental/semantica/
// api/decisions from the upstream subprocess:
//   - The upstream semantica stores decisions in semantica.decisions
//     (an in-memory rdflib graph). Per-actor visibility is not a
//     concept upstream — every decision is visible to anyone with
//     the shared X-API-Key.
//   - The per-actor_type=team / individual_private / shared_team
//     distinction is a Multica-fork concept; the SQL in
//     semantica_acl.sql is the source of truth.
//
// Membership gate: the workspace_id query param must match a
// workspace the viewer (X-User-ID) belongs to. Non-members get 403
// even if they guess a valid workspace_id — the ACL filter is
// defence-in-depth, not the access control.
package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// semanticaDecisionSummary is the renderer-facing shape of one ACL row.
// Mirrors the wire envelope (id, visibility, actor provenance) — the
// upstream semantica stores the body, the ACL row stores the access
// envelope, and this is the slice the renderer actually renders in
// the ModeBanner / per-actor feed.
type semanticaDecisionSummary struct {
	DecisionID  string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	ActorType   string `json:"actor_type"`
	ActorID     string `json:"actor_id"`
	Visibility  string `json:"visibility"`
	CreatedAt   string `json:"created_at"`
}

// semanticaDecisionListResponse is the JSON envelope the renderer
// expects. `count` is the number of returned rows; `mode` echoes the
// workspace mode (individual | team) so the renderer can render the
// ModeBanner without a second roundtrip.
type semanticaDecisionListResponse struct {
	Count int                        `json:"count"`
	Mode  string                     `json:"mode"`
	Items []semanticaDecisionSummary `json:"items"`
}

// ListSemanticaDecisions returns ACL-filtered decision summaries for
// the calling viewer in the requested workspace. Membership-gated.
//
// Route: GET /api/experimental/semantica/decisions?workspace=<uuid>
//
// The LIMIT 200 is hardcoded in semantica_acl.sql (ListSemantica
// DecisionsForViewer); future pagination goes through a new query,
// not a query-param knob here.
func (h *Handler) ListSemanticaDecisions(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		writeError(w, http.StatusServiceUnavailable, "queries handle unavailable")
		return
	}
	workspaceIDStr := r.URL.Query().Get("workspace")
	if workspaceIDStr == "" {
		writeError(w, http.StatusBadRequest, "missing workspace query param")
		return
	}
	workspaceID, err := util.ParseUUID(workspaceIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace uuid")
		return
	}

	// Membership gate — defence-in-depth, not the primary ACL. Even
	// if the SQL filter misbehaves, a non-member never reaches it.
	viewerID := requestUserID(r)
	if viewerID == "" {
		writeError(w, http.StatusUnauthorized, "missing X-User-ID")
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, util.UUIDToString(workspaceID), "workspace not found"); !ok {
		// requireWorkspaceMember already wrote 404/403 to w.
		return
	}

	ctx := r.Context()
	rows, err := h.Queries.ListSemanticaDecisionsForViewer(ctx, dbListParams(workspaceID, viewerID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list decisions failed")
		return
	}

	// Compute mode for the response envelope so the renderer can
	// pick the ModeBanner label without a second roundtrip.
	mode := experimental.ModeIndividual
	if count, countErr := experimental.WorkspaceMemberCount(ctx, h.Queries, workspaceID); countErr == nil {
		mode = experimental.Mode(count)
	}

	items := make([]semanticaDecisionSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, semanticaDecisionSummary{
			DecisionID:  r.DecisionID,
			WorkspaceID: util.UUIDToString(r.WorkspaceID),
			ActorType:   r.ActorType,
			ActorID:     r.ActorID,
			Visibility:  r.Visibility,
			CreatedAt:   r.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	writeJSON(w, http.StatusOK, semanticaDecisionListResponse{
		Count: len(items),
		Mode:  mode,
		Items: items,
	})
}

// dbListParams assembles the sqlc params struct. Pulled into its own
// function so the test can construct identical inputs without
// importing the generated package's exact struct name.
func dbListParams(workspaceID pgtype.UUID, viewerID string) db.ListSemanticaDecisionsForViewerParams {
	return db.ListSemanticaDecisionsForViewerParams{
		WorkspaceID: workspaceID,
		ActorID:     viewerID,
	}
}