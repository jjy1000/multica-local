package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service/mcpsync"
)

// ---------------------------------------------------------------------------
// MCP 管理 — read-only mirror of the user's Claude Code MCP servers.
//
//   GET  /api/workspaces/{id}/mcp-sync          mirror snapshot (redacted)
//   POST /api/workspaces/{id}/mcp-sync/refresh  force a sync pass, then read
//
// The mirror lives in mcp_sync_server (migration 285) and is written only by
// the mcpsync worker; these endpoints are read-side only, which is what makes
// "synced servers cannot be deleted from Multica" true — there is no
// mutation path here at all. Definitions are returned with env/header values
// masked (mcpsync.RedactServerDefinition): the DB-side plaintext exists so
// the claim-time merge can dispatch working servers, not so the UI can print
// credentials. Merge semantics live in mcpsync.MergeForClaim (manual agent
// mcp_config wins on name collisions).
// ---------------------------------------------------------------------------

// McpSyncServerResponse is one mirrored server. Definition carries masked
// env/header values — see RedactServerDefinition.
type McpSyncServerResponse struct {
	Name        string          `json:"name"`
	Definition  json.RawMessage `json:"definition"`
	Status      string          `json:"status"`
	FirstSeenAt string          `json:"first_seen_at"`
	LastSeenAt  string          `json:"last_seen_at"`
}

// McpSyncResponse is the settings-tab payload: the mirror rows plus the
// worker's freshness indicator and last sync error.
type McpSyncResponse struct {
	Servers      []McpSyncServerResponse `json:"servers"`
	LastSyncedAt string                  `json:"last_synced_at"`
	LastError    string                  `json:"last_error"`
}

// GetMcpSync returns the current mirror snapshot.
func (h *Handler) GetMcpSync(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	resp, err := h.buildMcpSyncResponse(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read mcp sync state")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// RefreshMcpSync runs a sync pass on demand, then returns the snapshot. A
// failed pass is not a failed request — the mirror keeps its last good state
// and the reason rides back in last_error (the worker recorded it).
func (h *Handler) RefreshMcpSync(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	if h.McpSync == nil {
		writeError(w, http.StatusServiceUnavailable, "mcp sync worker not available")
		return
	}
	if _, err := h.McpSync.SyncOnce(r.Context()); err != nil {
		slog.Warn("mcp sync manual refresh failed", "error", err)
	}
	resp, err := h.buildMcpSyncResponse(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read mcp sync state")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) buildMcpSyncResponse(ctx context.Context) (*McpSyncResponse, error) {
	rows, err := h.Queries.GetMcpSyncServers(ctx)
	if err != nil {
		return nil, err
	}
	state, err := h.Queries.GetMcpSyncState(ctx)
	if err != nil {
		return nil, err
	}

	servers := make([]McpSyncServerResponse, 0, len(rows))
	for _, row := range rows {
		servers = append(servers, McpSyncServerResponse{
			Name:        row.Name,
			Definition:  mcpsync.RedactServerDefinition(row.Definition),
			Status:      row.Status,
			FirstSeenAt: formatMcpSyncTimestamp(row.FirstSeenAt),
			LastSeenAt:  formatMcpSyncTimestamp(row.LastSeenAt),
		})
	}
	return &McpSyncResponse{
		Servers:      servers,
		LastSyncedAt: formatMcpSyncTimestamp(state.LastSyncedAt),
		LastError:    state.LastError,
	}, nil
}

// formatMcpSyncTimestamp renders a pg timestamptz for the API. The seeded
// state row's 'epoch' default renders as a real timestamp — the UI treats
// last_synced_at <= epoch+1h as "never synced" via its own guard.
func formatMcpSyncTimestamp(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(timeRFC3339)
}
