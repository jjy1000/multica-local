package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/service/agent_trust"
	causalgraph "github.com/multica-ai/multica/server/internal/service/causal_graph"
	"github.com/multica-ai/multica/server/internal/service/mcpsync"
	"github.com/multica-ai/multica/server/internal/service/mythos"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// ---------------------------------------------------------------------------
// Daemon workspace ownership helpers
// ---------------------------------------------------------------------------

// requireDaemonWorkspaceAccess verifies the caller has access to the given workspace.
// For daemon tokens (mdt_), compares the token's workspace ID directly.
// For PAT/JWT fallback, verifies user membership in the workspace.
func (h *Handler) requireDaemonWorkspaceAccess(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	if workspaceID == "" {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}

	// Daemon token: workspace must match.
	if daemonWsID := middleware.DaemonWorkspaceIDFromContext(r.Context()); daemonWsID != "" {
		if daemonWsID != workspaceID {
			writeError(w, http.StatusNotFound, "not found")
			return false
		}
		return true
	}

	// PAT/JWT fallback: check membership cache before hitting DB.
	userID := requestUserID(r)
	if userID != "" {
		if h.MembershipCache.Get(r.Context(), userID, workspaceID) {
			return true
		}
	}

	_, ok := h.requireWorkspaceMember(w, r, workspaceID, "not found")
	if ok && userID != "" {
		h.MembershipCache.Set(r.Context(), userID, workspaceID)
	}
	return ok
}

// requireDaemonRuntimeAccess looks up a runtime and verifies the caller owns its workspace.
//
// Only pgx.ErrNoRows is treated as a real "runtime gone" 404 — the daemon uses
// that response to drop the stale runtime from its in-memory map and re-register,
// so collapsing transient DB errors into the same 404 would force the daemon to
// self-cleanup on a hiccup. Other DB errors become 500.
func (h *Handler) requireDaemonRuntimeAccess(w http.ResponseWriter, r *http.Request, runtimeID string) (db.AgentRuntime, bool) {
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return db.AgentRuntime{}, false
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "runtime not found")
			return db.AgentRuntime{}, false
		}
		slog.Warn("get agent runtime failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load runtime")
		return db.AgentRuntime{}, false
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID)) {
		return db.AgentRuntime{}, false
	}
	return rt, true
}

// requireDaemonTaskAccess looks up a task and verifies the caller owns its workspace.
func (h *Handler) requireDaemonTaskAccess(w http.ResponseWriter, r *http.Request, taskID string) (db.AgentTaskQueue, bool) {
	task, _, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	return task, ok
}

// requireDaemonTaskAccessWithWorkspace is the workspace-aware variant of
// requireDaemonTaskAccess. It returns the resolved workspace ID alongside
// the task row so callers that need to forward workspace_id into
// taskToResponse (powering RelativeWorkDir) don't have to repeat the
// ResolveTaskWorkspaceID lookup. The two helpers share their entire
// implementation; the simpler one is preserved for ergonomic call sites
// that genuinely don't need workspace_id.
func (h *Handler) requireDaemonTaskAccessWithWorkspace(w http.ResponseWriter, r *http.Request, taskID string) (db.AgentTaskQueue, string, bool) {
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task_id")
	if !ok {
		return db.AgentTaskQueue{}, "", false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil {
		// Only treat pgx.ErrNoRows as a real "task gone" signal — daemon
		// uses this 404 to interrupt the running agent, so a transient DB
		// error must not be reported as a deletion.
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "task not found")
			return db.AgentTaskQueue{}, "", false
		}
		slog.Warn("get agent task failed", "task_id", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load task")
		return db.AgentTaskQueue{}, "", false
	}

	wsID := h.TaskService.ResolveTaskWorkspaceID(r.Context(), task)
	if wsID == "" {
		writeError(w, http.StatusNotFound, "task not found")
		return db.AgentTaskQueue{}, "", false
	}

	if !h.requireDaemonWorkspaceAccess(w, r, wsID) {
		return db.AgentTaskQueue{}, "", false
	}
	return task, wsID, true
}

// verifyDaemonWorkspaceAccess checks workspace access without writing an HTTP error.
// Used in loops where individual items may be skipped silently.
func (h *Handler) verifyDaemonWorkspaceAccess(r *http.Request, workspaceID string) bool {
	if workspaceID == "" {
		return false
	}
	if daemonWsID := middleware.DaemonWorkspaceIDFromContext(r.Context()); daemonWsID != "" {
		return daemonWsID == workspaceID
	}
	userID := requestUserID(r)
	if userID == "" {
		return false
	}
	if h.MembershipCache.Get(r.Context(), userID, workspaceID) {
		return true
	}
	_, err := h.getWorkspaceMember(r.Context(), userID, workspaceID)
	if err != nil {
		return false
	}
	h.MembershipCache.Set(r.Context(), userID, workspaceID)
	return true
}

// ---------------------------------------------------------------------------
// Daemon Registration & Heartbeat
// ---------------------------------------------------------------------------

type DaemonRegisterRequest struct {
	WorkspaceID string `json:"workspace_id"`
	DaemonID    string `json:"daemon_id"`
	// LegacyDaemonIDs lists prior hostname-derived daemon_ids this machine
	// may have registered under before switching to a persistent UUID. The
	// handler merges any matching runtime rows into the new row so agents
	// and tasks keep working without manual intervention.
	LegacyDaemonIDs []string `json:"legacy_daemon_ids"`
	DeviceName      string   `json:"device_name"`
	CLIVersion      string   `json:"cli_version"` // multica CLI version
	LaunchedBy      string   `json:"launched_by"` // "desktop" when spawned by the Electron app
	Runtimes        []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Version string `json:"version"` // agent CLI version (claude/codex)
		Status  string `json:"status"`
		// ProfileID, when non-empty, marks this as an instance of a custom
		// runtime_profile (MUL-3284). Empty = built-in runtime (legacy path).
		// Type carries the protocol family for both built-in and custom rows
		// so task routing (agent.New) is unchanged.
		ProfileID string `json:"profile_id"`
	} `json:"runtimes"`
	FailedProfiles []struct {
		ProfileID   string `json:"profile_id"`
		CommandName string `json:"command_name"`
		Reason      string `json:"reason"`
	} `json:"failed_profiles"`
}

type daemonWorkspaceReposResponse struct {
	WorkspaceID  string          `json:"workspace_id"`
	Repos        []RepoData      `json:"repos"`
	ReposVersion string          `json:"repos_version"`
	Settings     json.RawMessage `json:"settings,omitempty"`
}

func normalizeWorkspaceRepos(repos []RepoData) []RepoData {
	if len(repos) == 0 {
		return []RepoData{}
	}

	normalized := make([]RepoData, 0, len(repos))
	seen := make(map[string]struct{}, len(repos))
	for _, repo := range repos {
		url := strings.TrimSpace(repo.URL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		normalized = append(normalized, RepoData{URL: url, Description: repo.Description})
	}
	return normalized
}

func workspaceReposVersion(repos []RepoData) string {
	urls := make([]string, 0, len(repos))
	for _, repo := range repos {
		if repo.URL == "" {
			continue
		}
		urls = append(urls, repo.URL)
	}
	sort.Strings(urls)
	sum := sha256.Sum256([]byte(strings.Join(urls, "\n")))
	return hex.EncodeToString(sum[:])
}

func parseWorkspaceRepos(raw []byte) []RepoData {
	if len(raw) == 0 {
		return []RepoData{}
	}

	var repos []RepoData
	if err := json.Unmarshal(raw, &repos); err != nil {
		return []RepoData{}
	}
	return normalizeWorkspaceRepos(repos)
}

func workspaceReposResponse(workspaceID string, raw []byte, settingsRaw []byte) daemonWorkspaceReposResponse {
	repos := parseWorkspaceRepos(raw)
	resp := daemonWorkspaceReposResponse{
		WorkspaceID:  workspaceID,
		Repos:        repos,
		ReposVersion: workspaceReposVersion(repos),
	}
	if len(settingsRaw) > 0 {
		resp.Settings = json.RawMessage(settingsRaw)
	}
	return resp
}

// normalizeProvider canonicalizes a provider string for storage: trimmed and
// lowercased so client-side pricing lookups tolerate case drift. Returns "" for
// a blank input.
func normalizeProvider(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (h *Handler) DaemonRegister(w http.ResponseWriter, r *http.Request) {
	var req DaemonRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.DaemonID = strings.TrimSpace(req.DaemonID)
	req.DeviceName = strings.TrimSpace(req.DeviceName)

	if req.DaemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if len(req.Runtimes) == 0 && len(req.FailedProfiles) == 0 {
		writeError(w, http.StatusBadRequest, "at least one runtime or failed profile is required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, req.WorkspaceID, "workspace_id")
	if !ok {
		return
	}
	req.WorkspaceID = uuidToString(wsUUID)

	// Verify workspace access and resolve owner.
	// Daemon tokens (mdt_) prove workspace access directly; OwnerID will be zero
	// (the SQL COALESCE preserves any existing owner on upsert).
	// PAT/JWT tokens require a membership check and set OwnerID from the member.
	var ownerID pgtype.UUID
	if daemonWsID := middleware.DaemonWorkspaceIDFromContext(r.Context()); daemonWsID != "" {
		if daemonWsID != req.WorkspaceID {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		// ownerID stays zero — COALESCE keeps the existing owner on upsert.
	} else {
		member, ok := h.requireWorkspaceMember(w, r, req.WorkspaceID, "workspace not found")
		if !ok {
			return
		}
		ownerID = member.UserID
	}

	ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	resp := make([]AgentRuntimeResponse, 0, len(req.Runtimes))
	for _, runtime := range req.Runtimes {
		provider := normalizeProvider(runtime.Type)
		if provider == "" {
			provider = "unknown"
		}
		name := strings.TrimSpace(runtime.Name)
		if name == "" {
			name = provider
			if req.DeviceName != "" {
				name = fmt.Sprintf("%s (%s)", provider, req.DeviceName)
			}
		}
		deviceInfo := strings.TrimSpace(req.DeviceName)
		if runtime.Version != "" && deviceInfo != "" {
			deviceInfo = fmt.Sprintf("%s · %s", deviceInfo, runtime.Version)
		} else if runtime.Version != "" {
			deviceInfo = runtime.Version
		}
		status := "online"
		if runtime.Status == "offline" {
			status = "offline"
		}
		metadata, _ := json.Marshal(map[string]any{
			"version":     runtime.Version,
			"cli_version": req.CLIVersion,
			"launched_by": req.LaunchedBy,
		})

		var registered db.AgentRuntime
		var inserted bool
		isCustom := strings.TrimSpace(runtime.ProfileID) != ""

		if isCustom {
			profileUUID, pok := parseUUIDOrBadRequest(w, strings.TrimSpace(runtime.ProfileID), "profile_id")
			if !pok {
				return
			}
			// The profile must exist in this workspace and be enabled. Trust
			// the profile's stored protocol_family over the daemon-sent type so
			// the provider used for task routing cannot drift from the profile.
			profile, perr := h.Queries.GetRuntimeProfileForWorkspace(r.Context(), db.GetRuntimeProfileForWorkspaceParams{
				ID:          profileUUID,
				WorkspaceID: wsUUID,
			})
			if perr != nil {
				writeError(w, http.StatusBadRequest, "unknown runtime profile: "+runtime.ProfileID)
				return
			}
			if !profile.Enabled {
				writeError(w, http.StatusConflict, "runtime profile is disabled: "+runtime.ProfileID)
				return
			}
			provider = profile.ProtocolFamily

			prow, err := h.Queries.UpsertAgentRuntimeWithProfile(r.Context(), db.UpsertAgentRuntimeWithProfileParams{
				WorkspaceID: wsUUID,
				DaemonID:    strToText(req.DaemonID),
				Name:        name,
				RuntimeMode: "local",
				Provider:    provider,
				Status:      status,
				DeviceInfo:  deviceInfo,
				Metadata:    metadata,
				OwnerID:     ownerID,
				ProfileID:   profileUUID,
			})
			if err != nil {
				obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeFailed(
					uuidToString(ownerID),
					req.WorkspaceID,
					req.DaemonID,
					provider,
					"registration_failed",
					"db_error",
					true,
				))
				writeError(w, http.StatusInternalServerError, "failed to register runtime: "+err.Error())
				return
			}
			inserted = prow.Inserted
			registered = db.AgentRuntime{
				ID:             prow.ID,
				WorkspaceID:    prow.WorkspaceID,
				DaemonID:       prow.DaemonID,
				Name:           prow.Name,
				RuntimeMode:    prow.RuntimeMode,
				Provider:       prow.Provider,
				Status:         prow.Status,
				DeviceInfo:     prow.DeviceInfo,
				Metadata:       prow.Metadata,
				LastSeenAt:     prow.LastSeenAt,
				CreatedAt:      prow.CreatedAt,
				UpdatedAt:      prow.UpdatedAt,
				OwnerID:        prow.OwnerID,
				LegacyDaemonID: prow.LegacyDaemonID,
				Visibility:     prow.Visibility,
				ProfileID:      prow.ProfileID,
			}
		} else {
			row, err := h.Queries.UpsertAgentRuntime(r.Context(), db.UpsertAgentRuntimeParams{
				WorkspaceID: wsUUID,
				DaemonID:    strToText(req.DaemonID),
				Name:        name,
				RuntimeMode: "local",
				Provider:    provider,
				Status:      status,
				DeviceInfo:  deviceInfo,
				Metadata:    metadata,
				OwnerID:     ownerID,
			})
			if err != nil {
				obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeFailed(
					uuidToString(ownerID),
					req.WorkspaceID,
					req.DaemonID,
					provider,
					"registration_failed",
					"db_error",
					true,
				))
				writeError(w, http.StatusInternalServerError, "failed to register runtime: "+err.Error())
				return
			}
			inserted = row.Inserted
			registered = db.AgentRuntime{
				ID:             row.ID,
				WorkspaceID:    row.WorkspaceID,
				DaemonID:       row.DaemonID,
				Name:           row.Name,
				RuntimeMode:    row.RuntimeMode,
				Provider:       row.Provider,
				Status:         row.Status,
				DeviceInfo:     row.DeviceInfo,
				Metadata:       row.Metadata,
				LastSeenAt:     row.LastSeenAt,
				CreatedAt:      row.CreatedAt,
				UpdatedAt:      row.UpdatedAt,
				OwnerID:        row.OwnerID,
				LegacyDaemonID: row.LegacyDaemonID,
				Visibility:     row.Visibility,
				ProfileID:      row.ProfileID,
			}
		}

		// Inserted is false for normal daemon reconnects/upserts, so
		// runtime_ready is a first-ready-per-runtime-row signal.
		if inserted {
			obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeRegistered(
				uuidToString(ownerID),
				req.WorkspaceID,
				uuidToString(registered.ID),
				req.DaemonID,
				provider,
				runtime.Version,
				req.CLIVersion,
			))
			if registered.Status == "online" {
				obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeReady(
					uuidToString(ownerID),
					req.WorkspaceID,
					uuidToString(registered.ID),
					req.DaemonID,
					provider,
					0,
				))
			}
		}

		// Seamless migration from the previous hostname-derived identity. The
		// daemon sends every legacy daemon_id it may have registered under
		// (e.g. "host.local", "host", "host-staging"); for each match we
		// reassign agents + tasks onto the new UUID-keyed row, then delete
		// the stale row so there's only ever one runtime per machine.
		//
		// Only built-in runtimes participate: legacy rows predate custom
		// profiles, so a profile-keyed instance never has a hostname-derived
		// ancestor to merge, and mergeLegacyRuntimes scopes by provider alone
		// (no profile_id), which could otherwise fold a built-in row into a
		// custom one of the same provider.
		if !isCustom {
			h.mergeLegacyRuntimes(r, registered, provider, req.LegacyDaemonIDs)
		}

		resp = append(resp, runtimeToResponse(registered))
	}
	for _, failed := range req.FailedProfiles {
		profileID := strings.TrimSpace(failed.ProfileID)
		if profileID == "" {
			continue
		}
		profileUUID, pok := parseUUIDOrBadRequest(w, profileID, "profile_id")
		if !pok {
			return
		}
		profile, perr := h.Queries.GetRuntimeProfileForWorkspace(r.Context(), db.GetRuntimeProfileForWorkspaceParams{
			ID:          profileUUID,
			WorkspaceID: wsUUID,
		})
		if perr != nil || !profile.Enabled {
			continue
		}
		name := profile.DisplayName
		if req.DeviceName != "" {
			name = fmt.Sprintf("%s (%s)", name, req.DeviceName)
		}
		deviceInfo := strings.TrimSpace(req.DeviceName)
		reason := strings.TrimSpace(failed.Reason)
		if reason == "" {
			reason = "custom runtime command could not be resolved"
		}
		commandName := strings.TrimSpace(failed.CommandName)
		if commandName == "" {
			commandName = profile.CommandName
		}
		metadata, _ := json.Marshal(map[string]any{
			"version":                            "",
			"cli_version":                        req.CLIVersion,
			"launched_by":                        req.LaunchedBy,
			"runtime_profile_registration_error": true,
			"runtime_profile_failure_reason":     reason,
			"command_name":                       commandName,
		})
		if _, err := h.Queries.UpsertAgentRuntimeWithProfile(r.Context(), db.UpsertAgentRuntimeWithProfileParams{
			WorkspaceID: wsUUID,
			DaemonID:    strToText(req.DaemonID),
			Name:        name,
			RuntimeMode: "local",
			Provider:    profile.ProtocolFamily,
			Status:      "offline",
			DeviceInfo:  deviceInfo,
			Metadata:    metadata,
			OwnerID:     ownerID,
			ProfileID:   profileUUID,
		}); err != nil {
			slog.Warn("failed to record runtime profile registration failure",
				"workspace_id", req.WorkspaceID, "daemon_id", req.DaemonID,
				"profile_id", profileID, "error", err)
		}
	}

	slog.Info("daemon registered", "workspace_id", req.WorkspaceID, "daemon_id", req.DaemonID, "runtimes_count", len(resp))

	h.publish(protocol.EventDaemonRegister, req.WorkspaceID, "system", "", map[string]any{
		"runtimes": resp,
	})

	repoResp := workspaceReposResponse(req.WorkspaceID, ws.Repos, ws.Settings)

	writeJSON(w, http.StatusOK, map[string]any{
		"runtimes":      resp,
		"repos":         repoResp.Repos,
		"repos_version": repoResp.ReposVersion,
		"settings":      repoResp.Settings,
	})
}

// mergeLegacyRuntimes folds every runtime row keyed on a prior hostname-derived
// daemon_id into the newly registered UUID-keyed row. For each legacy id the
// lookup is case-insensitive and returns *all* matching rows — case-only drift
// may have already minted duplicates historically (e.g. `Foo.local` AND
// `foo.local` coexisting), and we need to consolidate every one of them, not
// just the first. Per match we reassign agents and tasks, record the legacy
// id on the new row for audit, then delete the stale row.
//
// Scoping by (workspace_id, provider) is sufficient since provider is single-
// runtime-per-daemon; `unique (workspace_id, daemon_id, provider)` prevents
// any two *exact* matches but the `LOWER(...)` comparison crosses that bound
// precisely when case-duplicate rows exist — which is the bug we're fixing.
// We also dedupe across legacy ids so overlapping candidates (e.g. `foo` and
// `foo.local` both resolving to the same stored row) don't double-process.
func (h *Handler) mergeLegacyRuntimes(r *http.Request, registered db.AgentRuntime, provider string, legacyIDs []string) {
	newID := uuidToString(registered.ID)
	merged := make(map[string]struct{})

	for _, legacyID := range legacyIDs {
		legacyID = strings.TrimSpace(legacyID)
		if legacyID == "" {
			continue
		}

		matches, err := h.Queries.FindLegacyRuntimesByDaemonID(r.Context(), db.FindLegacyRuntimesByDaemonIDParams{
			WorkspaceID: registered.WorkspaceID,
			Provider:    provider,
			DaemonID:    legacyID,
		})
		if err != nil {
			slog.Warn("legacy runtime merge: lookup failed", "legacy_daemon_id", legacyID, "error", err)
			continue
		}
		for _, old := range matches {
			oldID := uuidToString(old.ID)
			if oldID == newID {
				continue
			}
			if _, seen := merged[oldID]; seen {
				continue
			}
			merged[oldID] = struct{}{}

			agents, err := h.Queries.ReassignAgentsToRuntime(r.Context(), db.ReassignAgentsToRuntimeParams{
				NewRuntimeID: registered.ID,
				OldRuntimeID: old.ID,
			})
			if err != nil {
				slog.Warn("legacy runtime merge: reassign agents failed", "legacy_daemon_id", legacyID, "old_runtime_id", oldID, "new_runtime_id", newID, "error", err)
				continue
			}
			tasks, err := h.Queries.ReassignTasksToRuntime(r.Context(), db.ReassignTasksToRuntimeParams{
				NewRuntimeID: registered.ID,
				OldRuntimeID: old.ID,
			})
			if err != nil {
				slog.Warn("legacy runtime merge: reassign tasks failed", "legacy_daemon_id", legacyID, "old_runtime_id", oldID, "new_runtime_id", newID, "error", err)
				continue
			}
			if err := h.Queries.RecordRuntimeLegacyDaemonID(r.Context(), db.RecordRuntimeLegacyDaemonIDParams{
				ID:             registered.ID,
				LegacyDaemonID: strToText(legacyID),
			}); err != nil {
				slog.Warn("legacy runtime merge: record legacy daemon_id failed", "legacy_daemon_id", legacyID, "error", err)
			}
			if err := h.Queries.DeleteAgentRuntime(r.Context(), old.ID); err != nil {
				slog.Warn("legacy runtime merge: delete old runtime failed", "old_runtime_id", oldID, "error", err)
				continue
			}

			slog.Info("legacy runtime merged",
				"legacy_daemon_id", legacyID,
				"old_runtime_id", oldID,
				"new_runtime_id", newID,
				"provider", provider,
				"agents_reassigned", agents,
				"tasks_reassigned", tasks,
			)
		}
	}
}

func (h *Handler) GetDaemonWorkspaceRepos(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceId"))
	if !h.requireDaemonWorkspaceAccess(w, r, workspaceID) {
		return
	}

	ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	writeJSON(w, http.StatusOK, workspaceReposResponse(workspaceID, ws.Repos, ws.Settings))
}

// DaemonDeregister marks runtimes as offline when the daemon shuts down.
func (h *Handler) DaemonDeregister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RuntimeIDs []string `json:"runtime_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.RuntimeIDs) == 0 {
		writeError(w, http.StatusBadRequest, "runtime_ids is required")
		return
	}
	runtimeUUIDs, ok := parseUUIDSliceOrBadRequest(w, req.RuntimeIDs, "runtime_ids")
	if !ok {
		return
	}

	// Track affected workspaces for WS notifications.
	affectedWorkspaces := make(map[string]bool)

	for i, rid := range req.RuntimeIDs {
		// Look up the runtime and verify ownership.
		rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUIDs[i])
		if err != nil {
			slog.Warn("deregister: runtime not found", "runtime_id", rid, "error", err)
			continue
		}

		wsID := uuidToString(rt.WorkspaceID)
		if !h.verifyDaemonWorkspaceAccess(r, wsID) {
			slog.Warn("deregister: workspace mismatch", "runtime_id", rid)
			continue
		}

		if err := h.Queries.SetAgentRuntimeOffline(r.Context(), rt.ID); err != nil {
			slog.Warn("deregister: failed to set offline", "runtime_id", rid, "error", err)
			continue
		}
		obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeOffline(
			uuidToString(rt.OwnerID),
			wsID,
			uuidToString(rt.ID),
			rt.DaemonID.String,
			rt.Provider,
		))

		affectedWorkspaces[wsID] = true
	}

	// Notify frontend clients so they re-fetch runtime list.
	for wsID := range affectedWorkspaces {
		h.publish(protocol.EventDaemonRegister, wsID, "system", "", map[string]any{
			"action": "deregister",
		})
	}

	slog.Info("daemon deregistered", "runtime_ids", req.RuntimeIDs)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type DaemonHeartbeatRequest struct {
	RuntimeID           string `json:"runtime_id"`
	SupportsBatchImport bool   `json:"supports_batch_import,omitempty"`
}

// heartbeatHasPendingTimeout bounds the cheap HasPending probe on the
// heartbeat hot path. Probes are read-only (ZCARD in Redis) so a timeout is
// ack-safe: the worst case is "we didn't find out if anything was queued this
// tick" and the next heartbeat (default 15s later) will try again.
//
// PopPending is deliberately NOT bounded this way — its Redis implementation
// runs a Lua claim script whose ZREM + SET-running side effects cannot be
// cleanly un-run from the client side if the context expires mid-script. We
// therefore only invoke PopPending after HasPending confirms there is work
// to claim, so we never start a claim we might have to abort.
const heartbeatHasPendingTimeout = 1 * time.Second

// maxLocalSkillImportBatch is how many pending import requests the heartbeat
// handler pops per cycle. Higher values let the daemon process more imports
// in parallel but increase per-heartbeat latency.
//
// Timeout invariant: IMPORT_CONCURRENCY (views/.../runtime-local-skill-import-panel.tsx)
// × heartbeat period (~15s) must stay within runtimeLocalSkillPendingTimeout
// (runtime_local_skills.go), and IMPORT_POLL_TIMEOUT_MS (core/runtimes/local-skills.ts)
// must exceed pendingTimeout + runningTimeout.
const maxLocalSkillImportBatch = 10

// runtimeLivenessTTL is how long a Redis liveness record stays valid before
// expiring. The daemon refreshes it every heartbeat (~15s), so this just
// needs to be a few heartbeats long — the value (90s) tolerates ~6 missed
// beats before Redis declares the runtime dead.
//
// It is intentionally shorter than the sweeper's stale threshold (150s in
// cmd/server/runtime_sweeper.go). That ordering is safe and desirable:
// Redis can declare a runtime dead before the DB stale window opens, and
// the sweeper will simply ignore it until the DB column also crosses the
// threshold. The unsafe direction would be the opposite (Redis claiming
// "alive" past the DB stale window, masking a truly dead runtime when the
// sweeper consults Redis as the source of truth) — that cannot happen here.
const runtimeLivenessTTL = 90 * time.Second

// runtimeHeartbeatDBFlushInterval is the maximum staleness we tolerate on
// agent_runtime.last_seen_at while Redis is the active liveness source. When
// last_seen_at gets older than this, the heartbeat path schedules a DB write
// so (a) the UI's "last seen" display stays bounded and (b) the sweeper's
// DB-only fallback path (used when an IsAliveBatch call to Redis errors) does
// not false-positive on alive-but-Redis-only runtimes.
//
// Load-bearing invariant: this must be strictly less than the sweeper's
// stale threshold (150s in cmd/server/runtime_sweeper.go) MINUS one daemon
// heartbeat cycle (~15s) MINUS the BatchedHeartbeatScheduler tick interval
// (~30s). Worst-case DB age for an alive runtime is therefore bounded by
// flush + heartbeat + batchTick = 60 + 15 + 30 = 105s, leaving a 45s buffer
// below the 150s stale window. If you tune any of these constants, recompute
// the chain and keep at least a one-tick buffer.
//
// We intentionally keep the per-runtime flush throttle at 60s (rather than
// pushing it higher) so a crashed runtime is detected within ~150s instead
// of ~10 minutes. The bulk of the DB-pressure win comes from batched
// coalescing in HeartbeatScheduler — at 70 online runtimes that collapses
// ~17 single-row UPDATE/s into ~0.03 bulk UPDATE/s (one per batch tick),
// independent of how the per-runtime throttle is tuned.
const runtimeHeartbeatDBFlushInterval = 60 * time.Second

func (h *Handler) DaemonHeartbeat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	authPath := middleware.DaemonAuthPathFromContext(r.Context())
	var (
		outcome                                                                                            = "unauth"
		runtimeID                                                                                          string
		decodeMs, runtimeLookupMs, workspaceCheckMs                                                        int64
		authMs, updateMs, probeModelMs, popModelMs, probeSkillsMs, popSkillsMs, probeImportMs, popImportMs int64
		probeModelTimedOut, probeSkillsTimedOut, probeImportTimedOut                                       bool
	)
	defer func() {
		logHeartbeatEndpointSlow(runtimeID, outcome, authPath, start, decodeMs, runtimeLookupMs, workspaceCheckMs, authMs, updateMs, probeModelMs, popModelMs, probeSkillsMs, popSkillsMs, probeImportMs, popImportMs, probeModelTimedOut, probeSkillsTimedOut, probeImportTimedOut)
	}()

	decodeStart := time.Now()
	var req DaemonHeartbeatRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&req)
	decodeMs = time.Since(decodeStart).Milliseconds()
	if decodeErr != nil {
		outcome = "bad_body"
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RuntimeID == "" {
		outcome = "missing_runtime_id"
		writeError(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	runtimeID = req.RuntimeID

	// Inlined and instrumented version of requireDaemonRuntimeAccess so we
	// can attribute the runtime-lookup and workspace-check sub-stages
	// independently in slow-logs. Together with the auth_path label set by
	// DaemonAuth middleware, this lets us tell whether prod heartbeat tail
	// latency is in pgx pool acquisition (runtime_lookup_ms), in the PAT
	// fallback workspace-membership query (workspace_check_ms), or upstream.
	runtimeUUID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		outcome = "bad_runtime_id"
		return
	}
	lookupStart := time.Now()
	rt, lookupErr := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	runtimeLookupMs = time.Since(lookupStart).Milliseconds()
	if lookupErr != nil {
		// Only pgx.ErrNoRows means the runtime row is gone. Daemon reads this
		// 404 as a signal to drop the stale runtime locally; treating a
		// transient DB error the same way would force daemons to self-cleanup
		// on a hiccup.
		if isNotFound(lookupErr) {
			outcome = "runtime_not_found"
			writeError(w, http.StatusNotFound, "runtime not found")
			return
		}
		outcome = "runtime_lookup_error"
		slog.Warn("get agent runtime failed", "runtime_id", req.RuntimeID, "error", lookupErr)
		writeError(w, http.StatusInternalServerError, "failed to load runtime")
		return
	}
	wsCheckStart := time.Now()
	wsOK := h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID))
	workspaceCheckMs = time.Since(wsCheckStart).Milliseconds()
	if !wsOK {
		outcome = "workspace_denied"
		return
	}
	authMs = time.Since(start).Milliseconds()

	ack, m, err := h.processHeartbeat(r.Context(), rt, req.SupportsBatchImport)
	updateMs = m.UpdateMs
	probeModelMs = m.ProbeModelMs
	popModelMs = m.PopModelMs
	probeSkillsMs = m.ProbeSkillsMs
	popSkillsMs = m.PopSkillsMs
	probeImportMs = m.ProbeImportMs
	popImportMs = m.PopImportMs
	probeModelTimedOut = m.ProbeModelTimedOut
	probeSkillsTimedOut = m.ProbeSkillsTimedOut
	probeImportTimedOut = m.ProbeImportTimedOut
	if err != nil {
		outcome = "error_update"
		writeError(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}

	outcome = "ok"
	// Preserve the existing HTTP response shape: the runtime_id field is new
	// in the WS path and would be redundant noise on the HTTP path where the
	// caller already knows which runtime it asked about.
	resp := map[string]any{"status": ack.Status}
	if ack.PendingUpdate != nil {
		resp["pending_update"] = ack.PendingUpdate
	}
	if ack.PendingModelList != nil {
		resp["pending_model_list"] = ack.PendingModelList
	}
	if ack.PendingLocalSkills != nil {
		resp["pending_local_skills"] = ack.PendingLocalSkills
	}
	if ack.PendingLocalSkillImport != nil {
		resp["pending_local_skill_import"] = ack.PendingLocalSkillImport
	}
	if len(ack.PendingLocalSkillImports) > 0 {
		resp["pending_local_skill_imports"] = ack.PendingLocalSkillImports
	}
	if ack.FeatureFlags != nil {
		resp["feature_flags"] = ack.FeatureFlags
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleDaemonWSHeartbeat is the daemonws.HeartbeatHandler entry point: it
// resolves the runtime, verifies the connection's workspace owns it, and
// returns the ack payload. It is the WebSocket-side mirror of DaemonHeartbeat.
//
// Workspace authorization is re-checked on every heartbeat instead of trusted
// from the upgrade-time check because runtime ownership can change (e.g. a
// runtime is reassigned to another workspace mid-connection).
//
// When the runtime row is missing (pgx.ErrNoRows), the function returns a
// successful ack with Status=HeartbeatStatusRuntimeGone and RuntimeGone=true
// instead of an error. That keeps the hub from logging every beat at Warn,
// and tells the daemon to drop the stale runtime and re-register. Other DB
// errors still propagate as errors so they keep their existing Warn logging
// and the daemon does not mistake a hiccup for a deletion.
func (h *Handler) HandleDaemonWSHeartbeat(ctx context.Context, identity daemonws.ClientIdentity, runtimeID string, supportsBatchImport bool) (*protocol.DaemonHeartbeatAckPayload, error) {
	runtimeUUID, err := util.ParseUUID(runtimeID)
	if err != nil {
		return nil, fmt.Errorf("invalid runtime_id: %w", err)
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, runtimeUUID)
	if err != nil {
		if isNotFound(err) {
			return &protocol.DaemonHeartbeatAckPayload{
				RuntimeID:   runtimeID,
				Status:      protocol.HeartbeatStatusRuntimeGone,
				RuntimeGone: true,
			}, nil
		}
		return nil, fmt.Errorf("get agent runtime: %w", err)
	}
	if !identity.AllowsWorkspace(uuidToString(rt.WorkspaceID)) {
		return nil, fmt.Errorf("runtime not in connection workspace")
	}
	ack, _, err := h.processHeartbeat(ctx, rt, supportsBatchImport)
	return ack, err
}

// recordHeartbeat marks the runtime as alive. When LivenessStore is available
// (Redis configured and reachable) it writes a TTL'd liveness key and skips
// the DB row write on most beats — the DB is only updated on the
// offline→online transition or once per runtimeHeartbeatDBFlushInterval to
// keep last_seen_at fresh enough for the UI and the DB-fallback sweeper.
//
// When LivenessStore is unavailable (no Redis configured) or any Touch call
// errors, recordHeartbeat falls back to writing the DB on every beat — that
// is the original behavior and keeps the sweeper's DB-only path correct.
//
// The actual DB write is delegated to h.HeartbeatScheduler so production can
// coalesce many runtimes' bumps into one bulk UPDATE per tick. See
// heartbeat_scheduler.go for the two implementations.
func (h *Handler) recordHeartbeat(ctx context.Context, rt db.AgentRuntime) error {
	now := time.Now()

	// Decide whether the DB row needs a write *before* touching Redis, so a
	// Touch failure can simply force needDBWrite=true without re-evaluating
	// the structural reasons.
	needDBWrite := !h.LivenessStore.Available() ||
		rt.Status != "online" ||
		!rt.LastSeenAt.Valid ||
		now.Sub(rt.LastSeenAt.Time) >= runtimeHeartbeatDBFlushInterval

	if h.LivenessStore.Available() {
		if err := h.LivenessStore.Touch(ctx, uuidToString(rt.ID), runtimeLivenessTTL); err != nil {
			// Redis hiccup: degrade transparently to the DB-only path for
			// this beat. The sweeper falls back to its DB threshold the
			// same way when IsAliveBatch fails, so end-to-end correctness
			// is preserved.
			slog.Warn("liveness touch failed; falling back to DB heartbeat",
				"runtime_id", uuidToString(rt.ID), "error", err)
			needDBWrite = true
		}
	}

	if !needDBWrite {
		return nil
	}

	// Either bumps last_seen_at on an already-online row (Touch + race
	// fallback) or flips status from offline to online. The scheduler
	// chooses sync vs batched per case; see HeartbeatScheduler doc.
	return h.HeartbeatScheduler.Schedule(ctx, rt)
}

// heartbeatMetrics carries per-stage timings out of processHeartbeat so the
// HTTP slow-log can stay structured. The WS path discards them.
type heartbeatMetrics struct {
	UpdateMs, ProbeModelMs, PopModelMs, ProbeSkillsMs, PopSkillsMs, ProbeImportMs, PopImportMs int64
	ProbeModelTimedOut, ProbeSkillsTimedOut, ProbeImportTimedOut                               bool
}

// processHeartbeat does the work shared by HTTP POST /api/daemon/heartbeat and
// the WebSocket daemon:heartbeat path: records liveness and pulls any pending
// actions queued for the runtime. Auth and request decoding live in the
// caller because they differ between transports.
func (h *Handler) processHeartbeat(ctx context.Context, rt db.AgentRuntime, supportsBatchImport bool) (*protocol.DaemonHeartbeatAckPayload, heartbeatMetrics, error) {
	var m heartbeatMetrics
	runtimeID := uuidToString(rt.ID)

	updateStart := time.Now()
	if err := h.recordHeartbeat(ctx, rt); err != nil {
		m.UpdateMs = time.Since(updateStart).Milliseconds()
		return nil, m, err
	}
	m.UpdateMs = time.Since(updateStart).Milliseconds()

	slog.Debug("daemon heartbeat", "runtime_id", runtimeID)

	ack := &protocol.DaemonHeartbeatAckPayload{
		RuntimeID: runtimeID,
		Status:    "ok",
	}
	if h.DaemonFeatureFlags != nil {
		ack.FeatureFlags = h.DaemonFeatureFlags.EvaluateForRuntime(ctx, rt)
	}

	probeUpdateCtx, cancelProbeUpdate := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasUpdate, probeUpdateErr := h.UpdateStore.HasPending(probeUpdateCtx, runtimeID)
	cancelProbeUpdate()
	switch {
	case probeUpdateErr == nil && hasUpdate:
		pending, popUpdateErr := h.UpdateStore.PopPending(ctx, runtimeID)
		if popUpdateErr != nil {
			slog.Warn("update PopPending failed", "error", popUpdateErr, "runtime_id", runtimeID)
		} else if pending != nil {
			ack.PendingUpdate = &protocol.DaemonHeartbeatPendingUpdate{
				ID:            pending.ID,
				TargetVersion: pending.TargetVersion,
			}
		}
	case probeUpdateErr != nil:
		if errors.Is(probeUpdateErr, context.DeadlineExceeded) || errors.Is(probeUpdateErr, context.Canceled) {
			slog.Warn("update HasPending timed out", "runtime_id", runtimeID)
		} else {
			slog.Warn("update HasPending failed", "error", probeUpdateErr, "runtime_id", runtimeID)
		}
	}

	// Probe then claim the model list queue. Same pattern as the local-skill
	// queues below — a slow shared store cannot stall the heartbeat on
	// empty-queue ticks, but the claim itself runs unbounded because its
	// Lua side effects cannot be safely aborted mid-script.
	probeModelStart := time.Now()
	probeModelCtx, cancelProbeModel := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasModel, probeModelErr := h.ModelListStore.HasPending(probeModelCtx, runtimeID)
	cancelProbeModel()
	m.ProbeModelMs = time.Since(probeModelStart).Milliseconds()
	switch {
	case probeModelErr == nil && hasModel:
		popStart := time.Now()
		pendingModel, popErr := h.ModelListStore.PopPending(ctx, runtimeID)
		m.PopModelMs = time.Since(popStart).Milliseconds()
		if popErr != nil {
			slog.Warn("model list PopPending failed", "error", popErr, "runtime_id", runtimeID)
		} else if pendingModel != nil {
			ack.PendingModelList = &protocol.DaemonHeartbeatPendingModelList{ID: pendingModel.ID}
		}
	case probeModelErr != nil:
		if errors.Is(probeModelErr, context.DeadlineExceeded) || errors.Is(probeModelErr, context.Canceled) {
			m.ProbeModelTimedOut = true
			slog.Warn("model list HasPending timed out", "runtime_id", runtimeID, "elapsed_ms", m.ProbeModelMs)
		} else {
			slog.Warn("model list HasPending failed", "error", probeModelErr, "runtime_id", runtimeID)
		}
	}

	// Probe then claim the local-skill list queue. The probe is bounded so a
	// slow shared store cannot stall the heartbeat on empty-queue ticks; the
	// claim runs unbounded (it inherits only ctx) because its Lua side
	// effects cannot be safely aborted mid-script.
	probeSkillsStart := time.Now()
	probeSkillsCtx, cancelProbeSkills := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasSkills, probeErr := h.LocalSkillListStore.HasPending(probeSkillsCtx, runtimeID)
	cancelProbeSkills()
	m.ProbeSkillsMs = time.Since(probeSkillsStart).Milliseconds()
	switch {
	case probeErr == nil && hasSkills:
		popStart := time.Now()
		pendingSkills, popErr := h.LocalSkillListStore.PopPending(ctx, runtimeID)
		m.PopSkillsMs = time.Since(popStart).Milliseconds()
		if popErr != nil {
			slog.Warn("local skill list PopPending failed", "error", popErr, "runtime_id", runtimeID)
		} else if pendingSkills != nil {
			ack.PendingLocalSkills = &protocol.DaemonHeartbeatPendingLocalSkills{ID: pendingSkills.ID}
		}
	case probeErr != nil:
		if errors.Is(probeErr, context.DeadlineExceeded) || errors.Is(probeErr, context.Canceled) {
			m.ProbeSkillsTimedOut = true
			slog.Warn("local skill list HasPending timed out", "runtime_id", runtimeID, "elapsed_ms", m.ProbeSkillsMs)
		} else {
			slog.Warn("local skill list HasPending failed", "error", probeErr, "runtime_id", runtimeID)
		}
	}

	probeImportStart := time.Now()
	probeImportCtx, cancelProbeImport := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasImport, probeErr := h.LocalSkillImportStore.HasPending(probeImportCtx, runtimeID)
	cancelProbeImport()
	m.ProbeImportMs = time.Since(probeImportStart).Milliseconds()
	switch {
	case probeErr == nil && hasImport:
		popStart := time.Now()
		if supportsBatchImport {
			pendingImports, popErr := h.LocalSkillImportStore.PopPendingBatch(ctx, runtimeID, maxLocalSkillImportBatch)
			m.PopImportMs = time.Since(popStart).Milliseconds()
			if popErr != nil {
				slog.Warn("local skill import PopPendingBatch failed", "error", popErr, "runtime_id", runtimeID, "claimed", len(pendingImports))
			}
			// Always dispatch whatever was claimed — even on partial
			// failure the claimed requests have already transitioned to
			// running in the store. Dropping them here would leave them
			// stranded until the running timeout.
			if len(pendingImports) > 0 {
				// Backwards compat: singular field carries the first item so
				// old daemons that don't know the plural field still get one.
				ack.PendingLocalSkillImport = &protocol.DaemonHeartbeatPendingLocalSkillImport{
					ID:       pendingImports[0].ID,
					SkillKey: pendingImports[0].SkillKey,
				}
				batch := make([]protocol.DaemonHeartbeatPendingLocalSkillImport, 0, len(pendingImports))
				for _, p := range pendingImports {
					batch = append(batch, protocol.DaemonHeartbeatPendingLocalSkillImport{
						ID:       p.ID,
						SkillKey: p.SkillKey,
					})
				}
				ack.PendingLocalSkillImports = batch
			}
		} else {
			pendingImport, popErr := h.LocalSkillImportStore.PopPending(ctx, runtimeID)
			m.PopImportMs = time.Since(popStart).Milliseconds()
			if popErr != nil {
				slog.Warn("local skill import PopPending failed", "error", popErr, "runtime_id", runtimeID)
			} else if pendingImport != nil {
				ack.PendingLocalSkillImport = &protocol.DaemonHeartbeatPendingLocalSkillImport{
					ID:       pendingImport.ID,
					SkillKey: pendingImport.SkillKey,
				}
			}
		}
	case probeErr != nil:
		if errors.Is(probeErr, context.DeadlineExceeded) || errors.Is(probeErr, context.Canceled) {
			m.ProbeImportTimedOut = true
			slog.Warn("local skill import HasPending timed out", "runtime_id", runtimeID, "elapsed_ms", m.ProbeImportMs)
		} else {
			slog.Warn("local skill import HasPending failed", "error", probeErr, "runtime_id", runtimeID)
		}
	}

	return ack, m, nil
}

// logHeartbeatEndpointSlow emits one structured log when /api/daemon/heartbeat
// exceeds 500ms, splitting auth / update / probe / pop phases for both queues
// so the prod tail can be attributed without flooding logs at normal rates.
// auth_ms is further decomposed into decode_ms, runtime_lookup_ms, and
// workspace_check_ms; auth_path labels which token kind authenticated the
// request ("daemon_token", "pat", or "jwt"). Mirrors logClaimEndpointSlow.
func logHeartbeatEndpointSlow(runtimeID, outcome, authPath string, start time.Time, decodeMs, runtimeLookupMs, workspaceCheckMs, authMs, updateMs, probeModelMs, popModelMs, probeSkillsMs, popSkillsMs, probeImportMs, popImportMs int64, probeModelTimedOut, probeSkillsTimedOut, probeImportTimedOut bool) {
	totalMs := time.Since(start).Milliseconds()
	if totalMs < 500 && !probeModelTimedOut && !probeSkillsTimedOut && !probeImportTimedOut {
		return
	}
	slog.Info("heartbeat_endpoint slow",
		"runtime_id", runtimeID,
		"outcome", outcome,
		"auth_path", authPath,
		"total_ms", totalMs,
		"auth_ms", authMs,
		"decode_ms", decodeMs,
		"runtime_lookup_ms", runtimeLookupMs,
		"workspace_check_ms", workspaceCheckMs,
		"update_ms", updateMs,
		"probe_model_ms", probeModelMs,
		"pop_model_ms", popModelMs,
		"probe_skills_ms", probeSkillsMs,
		"pop_skills_ms", popSkillsMs,
		"probe_import_ms", probeImportMs,
		"pop_import_ms", popImportMs,
		"probe_model_timed_out", probeModelTimedOut,
		"probe_skills_timed_out", probeSkillsTimedOut,
		"probe_import_timed_out", probeImportTimedOut,
	)
}

// logClaimEndpointSlow emits one structured log when the /tasks/claim endpoint
// exceeds 500ms, splitting auth / claim / response-build phases so the prod
// tail can be diagnosed without flooding logs at normal poll rates.
func logClaimEndpointSlow(runtimeID, outcome string, start time.Time, authMs, claimMs, buildMs int64, payloadBytes, agentSkillCount, builtinSkillCount, skillPayloadBytes int) {
	totalMs := time.Since(start).Milliseconds()
	if totalMs < 500 {
		return
	}
	slog.Info("claim_endpoint slow",
		"runtime_id", runtimeID,
		"outcome", outcome,
		"total_ms", totalMs,
		"auth_ms", authMs,
		"claim_ms", claimMs,
		"build_ms", buildMs,
		"payload_bytes", payloadBytes,
		"agent_skill_count", agentSkillCount,
		"builtin_skill_count", builtinSkillCount,
		"skill_payload_bytes", skillPayloadBytes,
	)
}

func requestHasDaemonCapability(r *http.Request, capability string) bool {
	for _, part := range strings.Split(r.Header.Get("X-Client-Capabilities"), ",") {
		if strings.TrimSpace(part) == capability {
			return true
		}
	}
	return false
}

// chatSessionResumeFallbackNeeded reports whether the chat claim response
// needs a fallback lookup against agent_task_queue to recover a missing
// session_id / work_dir pointer. Returns true when at least one pointer is
// empty, so the fallback query only fires when there's something to recover.
// MUL-6324: most claims land in the chat_session.fast-path (both fields
// populated), and this guard keeps GetLastChatTaskSession off the hot path.
func chatSessionResumeFallbackNeeded(priorSessionID, priorWorkDir string) bool {
	return priorSessionID == "" || priorWorkDir == ""
}

// ClaimTaskByRuntime atomically claims the next queued task for a runtime.
// The response includes the agent's name and skills, fetched fresh from the DB.
func (h *Handler) ClaimTaskByRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	start := time.Now()

	var (
		outcome                  = "unauth"
		authMs, claimMs, buildMs int64
		payloadBytes             int
		agentSkillCount          int
		builtinSkillCount        int
		skillPayloadBytes        int
		buildStart               time.Time
	)
	defer func() {
		// Emit at function exit so error / unauth paths also carry timing.
		// build_ms is computed from buildStart only when we entered the
		// response-build phase (otherwise stays 0).
		if !buildStart.IsZero() {
			buildMs = time.Since(buildStart).Milliseconds()
		}
		logClaimEndpointSlow(runtimeID, outcome, start, authMs, claimMs, buildMs, payloadBytes, agentSkillCount, builtinSkillCount, skillPayloadBytes)
	}()

	// Verify the caller owns this runtime's workspace. The runtime's
	// workspace_id is the authoritative value a claimed task must match
	// below — a task whose resolved workspace doesn't equal this runtime's
	// workspace is rejected even if it was enqueued against this
	// runtime_id (defense-in-depth against upstream routing bugs).
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	runtimeWorkspaceID := uuidToString(runtime.WorkspaceID)
	authMs = time.Since(start).Milliseconds()

	claimStart := time.Now()
	task, err := h.TaskService.ClaimTaskForRuntime(r.Context(), parseUUID(runtimeID))
	claimMs = time.Since(claimStart).Milliseconds()
	if err != nil {
		outcome = "error_claim"
		writeError(w, http.StatusInternalServerError, "failed to claim task: "+err.Error())
		return
	}

	if task == nil {
		slog.Debug("no task to claim", "runtime_id", runtimeID)
		payloadBytes, _ = writeMeasuredJSON(w, http.StatusOK, map[string]any{"task": nil})
		outcome = "no_task"
		return
	}

	outcome = "claimed"
	buildStart = time.Now()

	// Build response with fresh agent data (name + skills + custom_env + custom_args).
	resp := taskToResponse(*task, runtimeWorkspaceID)
	if agent, err := h.Queries.GetAgent(r.Context(), task.AgentID); err == nil {
		// 0.5.18 F-002: auto-approval is gated on the agent's trust score,
		// computed server-side at claim time (the daemon has no DB). The wire
		// carries the boolean down to the spawn path.
		prof, profOK := agent_trust.LoadProfile(r.Context(), h.Queries, agent.WorkspaceID, agent.ID)
		resp.BypassPermissions = agent_trust.ShouldGrantBypassPermissions(agent_trust.Score(prof, profOK), profOK)
		useSkillRefs := requestHasDaemonCapability(r, protocol.DaemonCapabilitySkillBundlesV1)
		var customEnv map[string]string
		if agent.CustomEnv != nil {
			if err := json.Unmarshal(agent.CustomEnv, &customEnv); err != nil {
				slog.Warn("failed to unmarshal agent custom_env", "agent_id", uuidToString(agent.ID), "error", err)
			}
		}
		var customArgs []string
		if agent.CustomArgs != nil {
			if err := json.Unmarshal(agent.CustomArgs, &customArgs); err != nil {
				slog.Warn("failed to unmarshal agent custom_args", "agent_id", uuidToString(agent.ID), "error", err)
			}
		}
		var mcpConfig json.RawMessage
		if agent.McpConfig != nil {
			mcpConfig = json.RawMessage(agent.McpConfig)
		}
		// MCP sync (0.5.92): overlay the Claude Code mirror beneath the
		// agent's manual mcp_config for claude-provider runtimes. Manual
		// entries win on name collisions (explicit per-agent config is more
		// specific than the workspace-wide mirror). Every failure path keeps
		// mcpConfig untouched — a DB read error or an unparsable manual
		// config must degrade to the pre-sync behavior, not to a silently
		// different MCP set.
		if runtime.Provider == "claude" {
			syncedRows, err := h.Queries.GetSyncedMcpServerDefinitions(r.Context())
			if err != nil {
				slog.Warn("load synced mcp servers for claim failed", "error", err)
			} else if len(syncedRows) > 0 {
				synced := make(map[string]json.RawMessage, len(syncedRows))
				for _, row := range syncedRows {
					synced[row.Name] = row.Definition
				}
				if merged, overridden := mcpsync.MergeForClaim(mcpConfig, synced); merged != nil {
					mcpConfig = merged
					if overridden > 0 {
						slog.Debug("claim mcp merge: manual config overrode synced servers",
							"agent_id", uuidToString(agent.ID), "overridden", overridden)
					}
				}
			}
		}
		// runtime_config is stored as JSONB and may legitimately be the
		// empty object `{}` for agents that haven't opted into any
		// provider-specific tuning. Forward only non-empty payloads so the
		// daemon's per-provider decoders treat absent-or-empty identically.
		var runtimeConfig json.RawMessage
		if rc := bytes.TrimSpace(agent.RuntimeConfig); len(rc) > 0 && !bytes.Equal(rc, []byte("{}")) && !bytes.Equal(rc, []byte("null")) {
			runtimeConfig = json.RawMessage(agent.RuntimeConfig)
		}
		resp.Agent = &TaskAgentData{
			ID:            uuidToString(agent.ID),
			Name:          agent.Name,
			Instructions:  agent.Instructions,
			CustomEnv:     customEnv,
			CustomArgs:    customArgs,
			McpConfig:     mcpConfig,
			Model:         agent.Model.String,
			ThinkingLevel: agent.ThinkingLevel.String,
			SystemKey:     agent.SystemKey.String,
			RuntimeConfig: runtimeConfig,
		}
		if useSkillRefs {
			_, skillRefs := h.TaskService.LoadAgentSkillBundles(r.Context(), task.AgentID, claimIssueLabSource(r.Context(), h, task.IssueID))
			agentSkillCount = len(skillRefs)
			resp.Agent.SkillRefs = skillRefs
		} else {
			skills := h.TaskService.LoadAgentSkillsForClaim(r.Context(), task.AgentID, claimIssueLabSource(r.Context(), h, task.IssueID))
			agentSkillCount = len(skills)
			builtinSkills := h.TaskService.BuiltinSkills()
			builtinSkillCount = len(builtinSkills)
			skills = append(skills, builtinSkills...)
			resp.Agent.Skills = skills
		}
	}

	// Resolve the runtime owner's profile description so the daemon can
	// inject "## Requesting User" into the brief. Empty fields short-circuit
	// the heading entirely on the daemon side; cloud / system runtimes with
	// no owner stay anonymous. Failure here must not block claim — the agent
	// can still run without the user-context section.
	if runtime.OwnerID.Valid {
		if owner, err := h.Queries.GetUser(r.Context(), runtime.OwnerID); err == nil {
			resp.RequestingUserName = owner.Name
			resp.RequestingUserProfileDescription = owner.ProfileDescription
		} else {
			slog.Debug("failed to load runtime owner for brief injection",
				"runtime_id", runtimeID,
				"owner_id", uuidToString(runtime.OwnerID),
				"error", err,
			)
		}
	}

	// Stored task initiator: chat tasks persist the real message sender at
	// enqueue time (web: request user; Lark: inbound sender — NOT the chat
	// session creator, which for Lark groups is the installer). When set, it is
	// the authoritative initiator for this run; resolve the live name/email so
	// the daemon can render `## Task Initiator`. Comment-triggered tasks instead
	// resolve their initiator from the triggering comment's author below; the
	// two paths are mutually exclusive (a task is either chat or issue-bound).
	// See MUL-2645.
	if task.InitiatorUserID.Valid {
		resp.InitiatorType = "member"
		resp.InitiatorID = uuidToString(task.InitiatorUserID)
		if u, err := h.Queries.GetUser(r.Context(), task.InitiatorUserID); err == nil {
			resp.InitiatorName = u.Name
			resp.InitiatorEmail = u.Email
		}
	}

	// Include workspace ID and repos so the daemon can set up worktrees.
	//
	// Repo precedence: project-bound github_repo resources override workspace
	// repos when present. Mixing both would just confuse the agent — if a
	// project explicitly attached its repos, those are the authoritative set
	// for issues inside that project. When the project has no github_repo
	// resources (or no project at all), we fall back to the workspace repos.
	if task.IssueID.Valid {
		if issue, err := h.Queries.GetIssue(r.Context(), task.IssueID); err == nil {
			resp.WorkspaceID = uuidToString(issue.WorkspaceID)
			resp.ThreadName = issue.Title

			// Squad-leader briefing injection. The briefing follows the task
			// (MUL-3724): the gate key is task.IsLeaderTask + task.SquadID,
			// not issue.AssigneeType. The legacy issue-based gate only saw
			// the issue-assigned-to-squad path; comment-mention and
			// sub-issue-done-callback paths left issue.assignee_type alone
			// and silently skipped the briefing, so the leader woke with
			// zero squad context and degraded into doing the work itself.
			//
			// shouldInjectSquadLeaderBriefing encapsulates the gate +
			// history fallback (legacy issue.AssigneeType=="squad" lookup
			// for in-flight tasks predating migration 127). We append the
			// briefing rather than replacing — per-agent instructions stay
			// authoritative for general behavior; the squad briefing stacks
			// on top as task-specific squad context.
			if resp.Agent != nil {
				if squad, ok := shouldInjectSquadLeaderBriefing(r.Context(), h.Queries, task, resp.Agent.ID, issue); ok {
					briefing := buildSquadLeaderBriefing(r.Context(), h.Queries, squad)
					if strings.TrimSpace(resp.Agent.Instructions) == "" {
						resp.Agent.Instructions = briefing
					} else {
						resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + briefing
					}
					slog.Debug("injected squad leader briefing",
						"squad_id", uuidToString(squad.ID),
						"squad_name", squad.Name,
						"leader_agent_id", resp.Agent.ID,
					)
				}
			}

			// 0.3.51: prepend the system-prompt binding (if any) to the
			// agent's Instructions before the daemon dispatches the task.
			// Keys are tolerated at the API surface (CreateAgent /
			// UpdateAgent) and resolved against the Skill catalogue here
			// so adding a new system prompt later is a daemon-side
			// change, not a schema change. The original
			// `constitution_agent_v1` binding shipped in 0.3.51 and was
			// retired in 0.3.57 alongside the constitution_agent lab;
			// its body lookup now returns (zero, false) cleanly.
			if resp.Agent != nil && resp.Agent.SystemKey != "" {
				if body, ok := loadSystemPromptBinding(resp.Agent.SystemKey); ok {
					if strings.TrimSpace(resp.Agent.Instructions) == "" {
						resp.Agent.Instructions = body
					} else {
						resp.Agent.Instructions = body + "\n\n" + resp.Agent.Instructions
					}
					slog.Debug("injected system prompt binding",
						"agent_id", resp.Agent.ID,
						"system_key", resp.Agent.SystemKey,
					)
				} else {
					slog.Warn("system_key set but binding body not found",
						"agent_id", resp.Agent.ID,
						"system_key", resp.Agent.SystemKey,
					)
				}
			}

			var projectRepos []RepoData
			if issue.ProjectID.Valid {
				resp.ProjectID = uuidToString(issue.ProjectID)
				if proj, err := h.Queries.GetProject(r.Context(), issue.ProjectID); err == nil {
					resp.ProjectTitle = proj.Title
					resp.ProjectDescription = proj.Description.String
				}
				if rows := h.listProjectResourcesForProject(r.Context(), issue.ProjectID); len(rows) > 0 {
					out := make([]ProjectResourceData, 0, len(rows))
					for _, row := range rows {
						label := ""
						if row.Label.Valid {
							label = row.Label.String
						}
						ref := json.RawMessage(row.ResourceRef)
						if len(ref) == 0 {
							ref = json.RawMessage("{}")
						}
						out = append(out, ProjectResourceData{
							ID:           uuidToString(row.ID),
							ResourceType: row.ResourceType,
							ResourceRef:  ref,
							Label:        label,
						})
						// Lift github_repo resources into the daemon's repo list
						// so `multica repo checkout` and the meta-skill render
						// them as the issue's repos.
						if row.ResourceType == "github_repo" {
							var payload struct {
								URL string `json:"url"`
								Ref string `json:"ref,omitempty"`
							}
							if json.Unmarshal(row.ResourceRef, &payload) == nil && payload.URL != "" {
								projectRepos = append(projectRepos, RepoData{URL: payload.URL, Ref: strings.TrimSpace(payload.Ref)})
							}
						}
					}
					resp.ProjectResources = out
				}
			}

			if len(projectRepos) > 0 {
				resp.Repos = projectRepos
			} else if ws, err := h.Queries.GetWorkspace(r.Context(), issue.WorkspaceID); err == nil && ws.Repos != nil {
				var repos []RepoData
				if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
					resp.Repos = repos
				}
			}
		}

		// Fetch the triggering comment content so the daemon can embed it
		// directly in the agent prompt (prevents the agent from ignoring comments
		// when stale output files exist in a reused workdir). Also surface the
		// comment author's kind and display name so the agent knows whether it
		// was triggered by a human or by another agent — a signal used by the
		// harness instructions to avoid mention loops between agents.
		if task.TriggerCommentID.Valid {
			if comment, err := h.Queries.GetComment(r.Context(), task.TriggerCommentID); err == nil {
				resp.TriggerCommentContent = comment.Content
				resp.TriggerThreadID = uuidToString(comment.ID)
				if comment.ParentID.Valid {
					resp.TriggerThreadID = uuidToString(comment.ParentID)
				}
				resp.TriggerAuthorType = comment.AuthorType
				// The triggering comment's author is the task initiator — the
				// real requester behind this run. Surface it (type + id + name,
				// plus email for members) so a workspace-visible agent can
				// attribute the request to the right person instead of to the
				// runtime owner. Same lookups as the display name above; we just
				// also capture the id and email. See MUL-2645.
				resp.InitiatorType = comment.AuthorType
				if comment.AuthorID.Valid {
					resp.InitiatorID = uuidToString(comment.AuthorID)
				}
				switch comment.AuthorType {
				case "agent":
					if comment.AuthorID.Valid {
						if a, err := h.Queries.GetAgent(r.Context(), comment.AuthorID); err == nil {
							resp.TriggerAuthorName = a.Name
							resp.InitiatorName = a.Name
						}
					}
				case "member":
					// For member-authored comments, AuthorID is a user UUID
					// (see handler.resolveActor) — look up the user's display name.
					if comment.AuthorID.Valid {
						if u, err := h.Queries.GetUser(r.Context(), comment.AuthorID); err == nil {
							resp.TriggerAuthorName = u.Name
							resp.InitiatorName = u.Name
							resp.InitiatorEmail = u.Email
						}
					}
				}
				// Count comments that arrived issue-wide since this agent's last
				// run, so the daemon can tell it the full catch-up volume up front
				// (the prompt then steers it to read the triggering thread first).
				// Anchor = the prior task's started_at (never completed_at: a long
				// run would miss comments posted while it ran). Cold start (no prior
				// task) → no anchor → no hint. Excludes the agent's own comments and
				// the triggering comment itself because that body is already
				// injected into the prompt. Best-effort: any DB error or zero count
				// leaves the hint suppressed.
				if startedAt, err := h.Queries.GetLastTaskStartedAtForIssueAndAgent(r.Context(), db.GetLastTaskStartedAtForIssueAndAgentParams{
					AgentID: task.AgentID,
					IssueID: comment.IssueID,
				}); err == nil && startedAt.Valid {
					if cnt, err := h.Queries.CountNewCommentsSince(r.Context(), db.CountNewCommentsSinceParams{
						AnchorID:    task.TriggerCommentID,
						IssueID:     comment.IssueID,
						WorkspaceID: comment.WorkspaceID,
						Since:       startedAt,
						AuthorID:    task.AgentID,
					}); err == nil && cnt > 0 {
						resp.NewCommentCount = int(cnt)
						resp.NewCommentsSince = startedAt.Time.UTC().Format(time.RFC3339)
					}
				}
			}
		}

		// Look up the prior session for this (agent, issue) pair so the daemon
		// can resume the Claude Code conversation context.
		//
		// Skip all prior state when the task was flagged as a manual rerun:
		// the user just judged the prior output bad, so the daemon must start a
		// fresh agent session in a fresh workdir instead of resuming anything
		// from the same conversation that produced that output.
		if !task.ForceFreshSession {
			if prior, err := h.Queries.GetLastTaskSession(r.Context(), db.GetLastTaskSessionParams{
				AgentID: task.AgentID,
				IssueID: task.IssueID,
			}); err == nil && prior.SessionID.Valid {
				// Resume the prior session when it ran on the same runtime —
				// including comment-triggered follow-ups, so the agent keeps the
				// issue's conversation context across turns. The "Focus on THIS
				// comment" guard in prompt.go defends against inheriting the prior
				// turn's "Done." marker, and GetLastTaskSession already excludes
				// poisoned sessions.
				if prior.RuntimeID == task.RuntimeID {
					resp.PriorSessionID = prior.SessionID.String
				}
				if prior.WorkDir.Valid {
					resp.PriorWorkDir = prior.WorkDir.String
				}
			}
		}
	}

	// Chat task: populate workspace/session info from the chat_session table.
	if task.ChatSessionID.Valid {
		if cs, err := h.Queries.GetChatSession(r.Context(), task.ChatSessionID); err == nil {
			resp.WorkspaceID = uuidToString(cs.WorkspaceID)
			resp.ChatSessionID = uuidToString(cs.ID)
			resp.ThreadName = cs.Title
			if ws, err := h.Queries.GetWorkspace(r.Context(), cs.WorkspaceID); err == nil && ws.Repos != nil {
				var repos []RepoData
				if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
					resp.Repos = repos
				}
			}
			if !task.ForceFreshSession {
				// Resume chat sessions only when the stored pointer was produced
				// by the same runtime as the claiming task. When the chat_session
				// pointer is missing (legacy NULL runtime_id), stale (last task
				// failed before reporting completion), or runtime-mismatched, fall
				// back to the most recent task row that recorded a session_id —
				// otherwise a single failed turn would silently drop the entire
				// conversation memory on the next message. The fallback also
				// requires runtime to match.
				if cs.SessionID.Valid && cs.RuntimeID.Valid && cs.RuntimeID == task.RuntimeID {
					resp.PriorSessionID = cs.SessionID.String
				}
				if cs.WorkDir.Valid {
					resp.PriorWorkDir = cs.WorkDir.String
				}
				if chatSessionResumeFallbackNeeded(resp.PriorSessionID, resp.PriorWorkDir) {
					h.Metrics.RecordChatClaimSessionFallbackNeeded()
					started := time.Now()
					prior, err := h.Queries.GetLastChatTaskSession(r.Context(), cs.ID)
					h.Metrics.ObserveChatClaimLastSessionQuery(time.Since(started).Seconds())
					switch {
					case err == nil && prior.SessionID.Valid:
						h.Metrics.RecordChatClaimSessionFallbackHit()
						if resp.PriorSessionID == "" && prior.RuntimeID == task.RuntimeID {
							resp.PriorSessionID = prior.SessionID.String
						}
						if prior.WorkDir.Valid && resp.PriorWorkDir == "" {
							resp.PriorWorkDir = prior.WorkDir.String
						}
					case errors.Is(err, pgx.ErrNoRows), err == nil:
						h.Metrics.RecordChatClaimSessionFallbackMiss()
					default:
						h.Metrics.RecordChatClaimSessionFallbackError()
					}
				}
			}
			// Build the chat prompt from EVERY user message that has arrived
			// since the agent's last reply — not just the most recent one. A
			// short-window debounce (MUL-2968) can land several user messages
			// before a single run fires; the agent resumes its prior session
			// and only learns of new input through resp.ChatMessage, so
			// delivering just the latest message would silently drop the
			// earlier ones (e.g. "看上海天气" then "还有青岛" → only Qingdao
			// answered).
			//
			// MUL-4351 (migration 142): if task.chat_input_task_id is set, the
			// claim loads exactly that user-message batch via the 144 partial
			// index — this is the *immutable ownership marker* that prevents
			// the trailing-message selector from racing a concurrent new
			// message arriving mid-claim. When NULL (legacy rows predating
			// 142, or channel / Slack / Lark tasks), fall back to the
			// trailing run of user messages after the last assistant row
			// (every completed or failed run writes an assistant row, so
			// that anchor advances each turn). Attachments are collected
			// from each included message so the agent can `multica
			// attachment download <id>` — the markdown URL alone is signed
			// and 30-min expiring on the private CDN.
			if len(resp.ChatMessage) == 0 {
				if task.ChatInputTaskID.Valid {
					if batch, err := h.Queries.ListChatInputMessages(r.Context(), task.ChatInputTaskID); err == nil && len(batch) > 0 {
						parts := make([]string, 0, len(batch))
						for _, m := range batch {
							if strings.TrimSpace(m.Content) != "" {
								parts = append(parts, m.Content)
							}
							if atts, attErr := h.Queries.ListAttachmentsByChatMessage(r.Context(), db.ListAttachmentsByChatMessageParams{
								ChatMessageID: m.ID,
								WorkspaceID:   parseUUID(resp.WorkspaceID),
							}); attErr == nil && len(atts) > 0 {
								for _, a := range atts {
									resp.ChatMessageAttachments = append(resp.ChatMessageAttachments, ChatAttachmentMeta{
										ID:          uuidToString(a.ID),
										Filename:    a.Filename,
										ContentType: a.ContentType,
									})
								}
							}
						}
						resp.ChatMessage = strings.Join(parts, "\n\n")
						if strings.TrimSpace(resp.ThreadName) == "" && len(parts) > 0 {
							resp.ThreadName = parts[0]
						}
					}
				} else {
					if msgs, err := h.Queries.ListChatMessages(r.Context(), cs.ID); err == nil && len(msgs) > 0 {
						unanswered := trailingUserMessages(msgs)
						parts := make([]string, 0, len(unanswered))
						for _, m := range unanswered {
							if strings.TrimSpace(m.Content) != "" {
								parts = append(parts, m.Content)
							}
							if atts, attErr := h.Queries.ListAttachmentsByChatMessage(r.Context(), db.ListAttachmentsByChatMessageParams{
								ChatMessageID: m.ID,
								WorkspaceID:   parseUUID(resp.WorkspaceID),
							}); attErr == nil && len(atts) > 0 {
								for _, a := range atts {
									resp.ChatMessageAttachments = append(resp.ChatMessageAttachments, ChatAttachmentMeta{
										ID:          uuidToString(a.ID),
										Filename:    a.Filename,
										ContentType: a.ContentType,
									})
								}
							}
						}
						resp.ChatMessage = strings.Join(parts, "\n\n")
						if strings.TrimSpace(resp.ThreadName) == "" {
							resp.ThreadName = resp.ChatMessage
						}
					}
				}
			}
		}
	}

	// Autopilot run_only task: resolve workspace from autopilot_run →
	// autopilot, and include the autopilot instructions because there is no
	// issue for the agent to fetch.
	if task.AutopilotRunID.Valid {
		if run, err := h.Queries.GetAutopilotRun(r.Context(), task.AutopilotRunID); err == nil {
			resp.AutopilotID = uuidToString(run.AutopilotID)
			resp.AutopilotSource = run.Source
			if run.TriggerPayload != nil {
				resp.AutopilotTriggerPayload = json.RawMessage(run.TriggerPayload)
			}
			if ap, err := h.Queries.GetAutopilot(r.Context(), run.AutopilotID); err == nil {
				resp.AutopilotTitle = ap.Title
				resp.ThreadName = ap.Title
				if ap.Description.Valid {
					resp.AutopilotDescription = ap.Description.String
				}
				if resp.WorkspaceID == "" {
					resp.WorkspaceID = uuidToString(ap.WorkspaceID)
				}
				if len(resp.Repos) == 0 {
					if ws, err := h.Queries.GetWorkspace(r.Context(), ap.WorkspaceID); err == nil && ws.Repos != nil {
						var repos []RepoData
						if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
							resp.Repos = repos
						}
					}
				}
			}
		}
	}

	// Handoff note (MUL-3375) is populated by taskToResponse (the shared mapper
	// resp came from above), so the daemon's prompt + issue_context.md render the
	// assignment-handoff branch. Empty for all other task kinds.

	// Quick-create task: no issue / chat / autopilot link — workspace and
	// prompt come from the task's context JSONB. Resolve workspace from
	// there so the isolation check below has something to compare.
	hasQuickCreate := false
	if task.Context != nil && !task.IssueID.Valid && !task.ChatSessionID.Valid && !task.AutopilotRunID.Valid {
		var qc service.QuickCreateContext
		if json.Unmarshal(task.Context, &qc) == nil && qc.Type == service.QuickCreateContextType {
			hasQuickCreate = true
			resp.QuickCreatePrompt = qc.Prompt
			resp.QuickCreateAttachmentIDs = append([]string(nil), qc.AttachmentIDs...)
			resp.ThreadName = qc.Prompt
			resp.WorkspaceID = qc.WorkspaceID

			// When the user picked a project in the modal, surface its title
			// and resources to the daemon so the agent has the same context
			// it would for an issue-bound task: the prompt template can name
			// the project, and `multica repo checkout` sees the project's
			// github_repo resources instead of the workspace fallback.
			var projectRepos []RepoData
			if qc.ProjectID != "" {
				projectUUID, err := util.ParseUUID(qc.ProjectID)
				if err == nil {
					resp.ProjectID = qc.ProjectID
					if proj, err := h.Queries.GetProject(r.Context(), projectUUID); err == nil {
						resp.ProjectTitle = proj.Title
						resp.ProjectDescription = proj.Description.String
					}
					if rows := h.listProjectResourcesForProject(r.Context(), projectUUID); len(rows) > 0 {
						out := make([]ProjectResourceData, 0, len(rows))
						for _, row := range rows {
							label := ""
							if row.Label.Valid {
								label = row.Label.String
							}
							ref := json.RawMessage(row.ResourceRef)
							if len(ref) == 0 {
								ref = json.RawMessage("{}")
							}
							out = append(out, ProjectResourceData{
								ID:           uuidToString(row.ID),
								ResourceType: row.ResourceType,
								ResourceRef:  ref,
								Label:        label,
							})
							if row.ResourceType == "github_repo" {
								var payload struct {
									URL string `json:"url"`
									Ref string `json:"ref,omitempty"`
								}
								if json.Unmarshal(row.ResourceRef, &payload) == nil && payload.URL != "" {
									projectRepos = append(projectRepos, RepoData{URL: payload.URL, Ref: strings.TrimSpace(payload.Ref)})
								}
							}
						}
						resp.ProjectResources = out
					}
				}
			}

			if len(projectRepos) > 0 {
				resp.Repos = projectRepos
			} else if ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(qc.WorkspaceID)); err == nil && ws.Repos != nil {
				var repos []RepoData
				if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
					resp.Repos = repos
				}
			}

			// Parent-issue resolution for quick-create tasks opened from
			// "Add sub issue". The handler already verified workspace
			// membership at submit time; here we re-fetch to pull the
			// human-readable identifier (e.g. MUL-123) the agent will
			// reference in the prompt. If the parent was deleted between
			// submit and claim we surface the UUID anyway — the agent
			// still passes `--parent <uuid>` and the server-side create
			// will fail loud, which is a better outcome than silently
			// dropping the sub-issue intent.
			if qc.ParentIssueID != "" {
				resp.ParentIssueID = qc.ParentIssueID
				if parentUUID, err := util.ParseUUID(qc.ParentIssueID); err == nil {
					if wsUUID, wsErr := util.ParseUUID(qc.WorkspaceID); wsErr == nil {
						parent, perr := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
							ID:          parentUUID,
							WorkspaceID: wsUUID,
						})
						if perr == nil && parent.ID.Valid {
							if ws, werr := h.Queries.GetWorkspace(r.Context(), wsUUID); werr == nil {
								resp.ParentIssueIdentifier = ws.IssuePrefix + "-" + strconv.Itoa(int(parent.Number))
							}
						}
					}
				}
			}

			// Squad-leader briefing injection for quick-create tasks. When
			// the user picked a squad in the modal, the task runs on the
			// squad's leader agent (resolved by the handler). Surface the
			// same Operating Protocol + Roster + user Instructions that
			// issue-bound squad tasks see, so the leader can decide to
			// delegate before opening the issue.
			if resp.Agent != nil && qc.SquadID != "" {
				wsUUID, wsErr := util.ParseUUID(qc.WorkspaceID)
				squadUUID, sqErr := util.ParseUUID(qc.SquadID)
				if wsErr == nil && sqErr == nil {
					if squad, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
						ID:          squadUUID,
						WorkspaceID: wsUUID,
					}); err == nil && uuidToString(squad.LeaderID) == resp.Agent.ID {
						briefing := buildSquadLeaderBriefing(r.Context(), h.Queries, squad)
						if strings.TrimSpace(resp.Agent.Instructions) == "" {
							resp.Agent.Instructions = briefing
						} else {
							resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + briefing
						}
						// Surface the squad identity to the daemon so the
						// quick-create prompt defaults the new issue's
						// assignee to the squad, not the leader agent.
						resp.SquadID = uuidToString(squad.ID)
						resp.SquadName = squad.Name
						slog.Debug("injected squad leader briefing for quick-create",
							"squad_id", uuidToString(squad.ID),
							"squad_name", squad.Name,
							"leader_agent_id", resp.Agent.ID,
						)
					}
				}
			}
		}
	}

	// Workspace isolation check: the daemon uses this response's workspace_id
	// as the only authority for MULTICA_WORKSPACE_ID in the agent env. An
	// empty value would make the CLI silently fall back to the user-global
	// config and talk to whatever workspace the user happened to last
	// configure; a value that doesn't match the runtime's workspace means
	// upstream routed a foreign-workspace task here. Both cases must hard-
	// fail AND cancel the just-dispatched task so the queue / agent status
	// don't sit stuck until the stale-task sweeper fires minutes later.
	if resp.WorkspaceID == "" || resp.WorkspaceID != runtimeWorkspaceID {
		outcome = "error_workspace"
		slog.Error("task claim: workspace isolation check failed, cancelling task",
			"task_id", uuidToString(task.ID),
			"runtime_id", runtimeID,
			"runtime_workspace", runtimeWorkspaceID,
			"resolved_workspace", resp.WorkspaceID,
			"has_issue", task.IssueID.Valid,
			"has_chat", task.ChatSessionID.Valid,
			"has_autopilot_run", task.AutopilotRunID.Valid,
			"has_quick_create", hasQuickCreate,
		)
		if _, cerr := h.TaskService.CancelTask(r.Context(), task.ID); cerr != nil {
			slog.Error("task claim: cancel after workspace check failed",
				"task_id", uuidToString(task.ID), "error", cerr)
		}
		writeError(w, http.StatusInternalServerError, "task workspace isolation check failed")
		return
	}

	// Workspace-level Context (workspace.context DB column) — the per-workspace
	// system prompt that workspace owners set in Settings → General. Inject it
	// into the brief regardless of task kind (issue / chat / autopilot /
	// quick-create) so every agent running in the workspace sees the same
	// shared context. Empty string when the owner hasn't set one; the daemon
	// skips rendering the heading in that case.
	if ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(resp.WorkspaceID)); err == nil {
		if ws.Context.Valid {
			resp.WorkspaceContext = ws.Context.String
		}
	} else {
		slog.Warn("task claim: failed to load workspace for context injection",
			"task_id", uuidToString(task.ID),
			"workspace_id", resp.WorkspaceID,
			"error", err,
		)
	}

	// 0.5.85 P1: causal subgraph briefing injection (issue-bound tasks only).
	// The build loads the active causal subgraph for the issue (BFS depth≤2),
	// filters out noise types + low-confidence edges, and formats a compact
	// markdown section that surfaces actions / outcomes / decisions / constraints
	// already recorded by prior runs. Agents then see "## Prior Causal
	// Context (read-only)" instead of rediscovering what prior runs already
	// learned. Silent fallback on any error — BuildClaimSubgraph returns
	// ("", nil) on timeout / DB error / empty graph so a broken causal read
	// never blocks the claim hot path. Flag-gated upstream
	// (experimental.DefaultFor("causal_graph")) so off-flag installs pay zero
	// overhead.
	if task.IssueID.Valid && experimental.DefaultFor("causal_graph") {
		if subgraph, err := causalgraph.BuildClaimSubgraph(r.Context(), h.Queries,
			parseUUID(resp.WorkspaceID),
			task.IssueID); err == nil && strings.TrimSpace(subgraph) != "" {
			if strings.TrimSpace(resp.Agent.Instructions) == "" {
				resp.Agent.Instructions = subgraph
			} else {
				resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + subgraph
			}
			slog.Debug("injected causal subgraph briefing",
				"task_id", uuidToString(task.ID),
				"issue_id", uuidToString(task.IssueID),
				"workspace_id", resp.WorkspaceID,
				"brief_bytes", len(subgraph),
			)
		}
	}

	// 0.5.88: Available Labs (delegation) briefing injection (issue-bound
	// tasks only). Lists the workspace's ENABLED assignee-model labs with a
	// resolvable leader (frozen + roster labs skipped inside the builder)
	// and the copy-pasteable `multica lab delegate --parent` command, so a
	// working agent can discover delegation targets without leaving its
	// task. Same error isolation as the causal subgraph block above:
	// BuildDelegateBrief returns ("", nil) on every error path / empty lab
	// set, so a broken lookup never blocks the claim. NOT gated on the
	// causal_graph flag — the section advertises delegation, not causal
	// context; its own empty-when-none contract is the gate. The leader
	// resolver is injected (not duplicated) from the handler's existing
	// resolveLabLeader helper, riding a detached context so the leader
	// lookups survive claim-request cancellation; each lookup carries its
	// own DelegateBriefTimeout bound (see the closure below).
	if task.IssueID.Valid && resp.WorkspaceID != "" {
		briefCtx := context.WithoutCancel(r.Context())
		if brief, err := causalgraph.BuildDelegateBrief(briefCtx, h.Queries,
			func(key string) (string, bool) {
				// briefCtx (WithoutCancel) carries no deadline and the
				// builder's 200ms budget only wraps its enabled-flag query —
				// bound EACH leader lookup separately or a wedged
				// GetUserPluginByFlagKey would block the claim hot path.
				lookupCtx, cancel := context.WithTimeout(briefCtx, causalgraph.DelegateBriefTimeout)
				defer cancel()
				return h.resolveLabLeader(lookupCtx, key)
			},
			uuidToString(task.IssueID)); err == nil && strings.TrimSpace(brief) != "" {
			if strings.TrimSpace(resp.Agent.Instructions) == "" {
				resp.Agent.Instructions = brief
			} else {
				resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + brief
			}
			slog.Debug("injected delegation briefing",
				"task_id", uuidToString(task.ID),
				"issue_id", uuidToString(task.IssueID),
				"workspace_id", resp.WorkspaceID,
				"brief_bytes", len(brief),
			)
		}
	}

	// 0.5.89 WS1: Lab Plugin Management briefing — tells every issue-bound
	// agent that it can create/manage user lab plugins for the user from
	// inside the conversation (the conversational plugin-creation loop).
	// Static text from experimental.PluginManagementBrief: no DB lookups,
	// no error path, ~650 bytes. Skipped for lab-bound issues — that agent
	// IS the lab; advertising plugin management to a lab leader only
	// invites recursion. The verbs are contract-pinned against the cobra
	// tree by TestPluginManagementBriefVerbsMatchCLI (briefing/CLI
	// never-disagree law, extended to five verbs).
	//
	// 0.5.90: the lab_source read is hoisted so the OpenMythos strategy
	// injection below reuses it (one query per claim, not two).
	issueLabSource := ""
	if task.IssueID.Valid {
		issueLabSource = claimIssueLabSource(r.Context(), h, task.IssueID)
	}
	if task.IssueID.Valid && issueLabSource == "" {
		if strings.TrimSpace(resp.Agent.Instructions) == "" {
			resp.Agent.Instructions = experimental.PluginManagementBrief
		} else {
			resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + experimental.PluginManagementBrief
		}
	}

	// 0.5.90: OpenMythos enhancer strategy briefing — injected ONLY for
	// claims on mythos_swarm-tagged issues. Delivers the outer loop's
	// distilled strategy to the target assignee at claim time (the
	// system comment is the human half of the same delivery). Same
	// silent-fallback discipline as the causal subgraph block:
	// BuildEnhancerBrief returns ("", nil) on every error/empty path, so
	// a broken read never blocks the claim. Detached context: the lookup
	// survives claim-request cancellation.
	if issueLabSource == "mythos_swarm" && resp.WorkspaceID != "" {
		strategyCtx := context.WithoutCancel(r.Context())
		if brief, err := mythos.BuildEnhancerBrief(strategyCtx, h.Queries,
			parseUUID(resp.WorkspaceID), task.IssueID); err == nil && strings.TrimSpace(brief) != "" {
			if strings.TrimSpace(resp.Agent.Instructions) == "" {
				resp.Agent.Instructions = brief
			} else {
				resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + brief
			}
			slog.Debug("injected openmythos strategy briefing",
				"task_id", uuidToString(task.ID),
				"issue_id", uuidToString(task.IssueID),
				"workspace_id", resp.WorkspaceID,
				"brief_bytes", len(brief),
			)
		}
	}

	// Mint a task-scoped `mat_` token bound to (agent, task, workspace,
	// owner). The daemon will inject this as MULTICA_TOKEN into the agent
	// process instead of its own credential, so any API call the agent
	// makes — even one that strips X-Agent-ID / X-Task-ID headers — is
	// recognized server-side as actor=agent, closing the lateral-movement
	// path on owner-only endpoints (e.g. `/api/agents/{id}/env`). Runtime
	// owner is required because task tokens are still bound to an owning user;
	// without one, fail the claim explicitly instead of letting the daemon
	// fall back to a member/owner credential. MUL-3292.
	// Token expires after the queue/runtime upper bound (24h) so it survives
	// long-running tasks but cannot outlive a forgotten one.
	if !runtime.OwnerID.Valid {
		outcome = "error_token"
		slog.Error("task claim: runtime owner missing; cancelling task to avoid unscoped agent credentials",
			"task_id", uuidToString(task.ID),
			"runtime_id", runtimeID,
			"workspace_id", runtimeWorkspaceID,
		)
		if _, cerr := h.TaskService.CancelTask(r.Context(), task.ID); cerr != nil {
			slog.Error("task claim: cancel after missing runtime owner failed",
				"task_id", uuidToString(task.ID), "error", cerr)
		}
		writeError(w, http.StatusInternalServerError, "runtime owner required to mint task token")
		return
	}
	tokenStr, terr := auth.GenerateAgentTaskToken()
	if terr != nil {
		outcome = "error_token"
		slog.Error("task claim: failed to generate agent task token",
			"task_id", uuidToString(task.ID), "error", terr)
		writeError(w, http.StatusInternalServerError, "failed to mint task token")
		return
	}
	if _, terr := h.Queries.CreateTaskToken(r.Context(), db.CreateTaskTokenParams{
		TokenHash:   auth.HashToken(tokenStr),
		TaskID:      task.ID,
		AgentID:     task.AgentID,
		WorkspaceID: parseUUID(resp.WorkspaceID),
		UserID:      runtime.OwnerID,
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	}); terr != nil {
		outcome = "error_token"
		slog.Error("task claim: failed to persist agent task token",
			"task_id", uuidToString(task.ID), "error", terr)
		writeError(w, http.StatusInternalServerError, "failed to persist task token")
		return
	}
	resp.AuthToken = tokenStr

	slog.Info("task claimed by runtime", "task_id", uuidToString(task.ID), "runtime_id", runtimeID, "agent_id", uuidToString(task.AgentID), "prior_session", resp.PriorSessionID)
	if resp.Agent != nil && len(resp.Agent.Skills) > 0 {
		if skillPayload, err := json.Marshal(resp.Agent.Skills); err == nil {
			skillPayloadBytes = len(skillPayload)
		}
	} else if resp.Agent != nil && len(resp.Agent.SkillRefs) > 0 {
		if skillPayload, err := json.Marshal(resp.Agent.SkillRefs); err == nil {
			skillPayloadBytes = len(skillPayload)
		}
	}
	payloadBytes, _ = writeMeasuredJSON(w, http.StatusOK, map[string]any{"task": resp})
}

type resolveSkillBundlesRequest struct {
	Skills []resolveSkillBundleRef `json:"skills"`
}

type resolveSkillBundleRef struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Hash   string `json:"hash"`
}

// ResolveTaskSkillBundles returns full skill content for refs from a slim
// claim. The daemon calls this after claim and before execenv.Prepare so
// runtimes still see complete local skill files at startup.
//
// If a requested hash no longer matches the agent's current skill bundle, the
// endpoint returns the current bundle and hash. Stage 1 does not snapshot skill
// content at claim time; the daemon validates the returned bundle before
// writing it to cache and materializing it.
// claimIssueLabSource reads the claimed issue's lab_source so the skill
// loader can scope capabilities.skills_visibility="lab_scoped" plugins to
// their own lab's runs (0.5.89). One narrow query (PK lookup, one nullable
// TEXT column); "" on any error / issue-less claim keeps the path
// best-effort exactly like the rest of the claim injections.
func claimIssueLabSource(ctx context.Context, h *Handler, issueID pgtype.UUID) string {
	if !issueID.Valid {
		return ""
	}
	ls, err := h.Queries.GetIssueLabSource(ctx, issueID)
	if err != nil || !ls.Valid {
		return ""
	}
	return ls.String
}

// ResolveTaskSkillBundles returns the full bundles for the refs a slim-claim
// runtime requests, scoped to the task's agent.
func (h *Handler) ResolveTaskSkillBundles(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	taskID := chi.URLParam(r, "taskId")

	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	task, taskWorkspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	if !ok {
		return
	}
	if taskWorkspaceID != uuidToString(runtime.WorkspaceID) || uuidToString(task.RuntimeID) != runtimeID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Status != "dispatched" && task.Status != "waiting_local_directory" {
		writeError(w, http.StatusConflict, "task is not preparing")
		return
	}

	var req resolveSkillBundlesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Skills) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"bundles": []service.AgentSkillData{}})
		return
	}

	bundles, _ := h.TaskService.LoadAgentSkillBundles(r.Context(), task.AgentID, claimIssueLabSource(r.Context(), h, task.IssueID))
	allowed := make(map[string]service.AgentSkillData, len(bundles))
	for _, bundle := range bundles {
		allowed[bundle.Source+"\x00"+bundle.ID] = bundle
	}

	resolved := make([]service.AgentSkillData, 0, len(req.Skills))
	for _, ref := range req.Skills {
		if ref.ID == "" || ref.Source == "" || ref.Hash == "" {
			writeError(w, http.StatusBadRequest, "invalid skill ref")
			return
		}
		bundle, ok := allowed[ref.Source+"\x00"+ref.ID]
		if !ok {
			writeError(w, http.StatusNotFound, "skill bundle not found")
			return
		}
		resolved = append(resolved, bundle)
	}

	writeJSON(w, http.StatusOK, map[string]any{"bundles": resolved})
}

// trailingUserMessages returns the run of user messages after the last
// assistant message in a chronologically-ordered chat history — the set the
// agent has NOT yet replied to. The agent resumes its prior session and only
// learns of new input through the claim response's chat_message, so a single
// run that covers a debounced burst (MUL-2968) must deliver every one of
// these, not just the latest. Every completed or failed run writes an
// assistant row, so the anchor advances one turn at a time; the result is the
// whole slice on the first turn and exactly the new message(s) thereafter.
func trailingUserMessages(msgs []db.ChatMessage) []db.ChatMessage {
	start := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			start = i + 1
			break
		}
	}
	return msgs[start:]
}

// ListPendingTasksByRuntime returns queued/dispatched tasks for a runtime.
func (h *Handler) ListPendingTasksByRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")

	// Verify the caller owns this runtime's workspace.
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	workspaceID := uuidToString(runtime.WorkspaceID)

	tasks, err := h.Queries.ListPendingTasksByRuntime(r.Context(), parseUUID(runtimeID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pending tasks")
		return
	}

	resp := make([]AgentTaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = taskToResponse(t, workspaceID)
	}

	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Task Lifecycle (called by daemon)
// ---------------------------------------------------------------------------

// ExtendTaskPrepareLease keeps a dispatched task protected while the daemon is
// resolving startup inputs and preparing the execution environment.
func (h *Handler) ExtendTaskPrepareLease(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	taskID := chi.URLParam(r, "taskId")

	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	task, taskWorkspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	if !ok {
		return
	}
	if taskWorkspaceID != uuidToString(runtime.WorkspaceID) || uuidToString(task.RuntimeID) != runtimeID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	updated, err := h.TaskService.ExtendTaskPrepareLease(r.Context(), parseUUID(taskID), parseUUID(runtimeID))
	if err != nil {
		slog.Warn("extend task prepare lease failed", "task_id", taskID, "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, taskToResponse(*updated, taskWorkspaceID))
}

// StartTask marks a dispatched task as running.
func (h *Handler) StartTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	_, workspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	if !ok {
		return
	}

	task, err := h.TaskService.StartTask(r.Context(), parseUUID(taskID))
	if err != nil {
		slog.Warn("start task failed", "task_id", taskID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("task started", "task_id", taskID, "agent_id", uuidToString(task.AgentID))
	writeJSON(w, http.StatusOK, taskToResponse(*task, workspaceID))
}

// TaskWaitLocalDirectoryRequest is the body the daemon POSTs when it parks
// a freshly-dispatched task on a busy local_directory path.
type TaskWaitLocalDirectoryRequest struct {
	// Reason is a short hint surfaced by the UI alongside the status —
	// typically "<path>" or "<path> (holder: <task short id>)". Small
	// enough to fit on the issue card. Empty is accepted; the column is
	// nullable on the server.
	Reason string `json:"reason"`
}

// MarkTaskWaitingLocalDirectory transitions a dispatched task to
// waiting_local_directory. Called by the daemon when, after claiming a task
// whose project carries a local_directory resource, it discovers another
// in-flight task already holds the path's mutex.
func (h *Handler) MarkTaskWaitingLocalDirectory(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	_, workspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	if !ok {
		return
	}

	var req TaskWaitLocalDirectoryRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	task, err := h.TaskService.MarkTaskWaitingLocalDirectory(r.Context(), parseUUID(taskID), req.Reason)
	if err != nil {
		slog.Warn("mark task waiting_local_directory failed", "task_id", taskID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, taskToResponse(*task, workspaceID))
}

// ReportTaskProgress broadcasts a progress update.
type TaskProgressRequest struct {
	Summary string `json:"summary"`
	Step    int    `json:"step"`
	Total   int    `json:"total"`
}

func (h *Handler) ReportTaskProgress(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	var req TaskProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Verify ownership and resolve workspace ID.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	workspaceID := ""
	if task.IssueID.Valid {
		if issue, err := h.Queries.GetIssue(r.Context(), task.IssueID); err == nil {
			workspaceID = uuidToString(issue.WorkspaceID)
		}
	}

	h.TaskService.ReportProgress(r.Context(), taskID, workspaceID, req.Summary, req.Step, req.Total)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// CompleteTask marks a running task as completed.
type TaskCompleteRequest struct {
	PRURL     string `json:"pr_url"`
	Output    string `json:"output"`
	SessionID string `json:"session_id"` // Claude session ID for future resumption
	WorkDir   string `json:"work_dir"`   // working directory used during execution
}

// sanitizeTaskCompleteRequest / sanitizeTaskFailRequest scrub every
// caller-supplied string on a terminal task callback. Both request types are
// flat bags of strings, so this is exhaustive by construction — but that also
// means a NEW string field must be added here, or it reopens GH #7098 through a
// fresh door. The task-row columns these feed (error, work_dir, session_id)
// are all TEXT, and result is JSONB; neither tolerates a NUL.
func sanitizeTaskCompleteRequest(req *TaskCompleteRequest) {
	req.PRURL = util.SanitizeTextForPostgres(req.PRURL)
	req.Output = util.SanitizeTextForPostgres(req.Output)
	req.SessionID = util.SanitizeTextForPostgres(req.SessionID)
	req.WorkDir = util.SanitizeTextForPostgres(req.WorkDir)
}

func sanitizeTaskFailRequest(req *TaskFailRequest) {
	req.Error = util.SanitizeTextForPostgres(req.Error)
	req.SessionID = util.SanitizeTextForPostgres(req.SessionID)
	req.WorkDir = util.SanitizeTextForPostgres(req.WorkDir)
	req.FailureReason = util.SanitizeTextForPostgres(req.FailureReason)
}

// ResultEnvelope keys (0.3.40 v2): when the agent emits a pure JSON
// object as its final reply (no markdown wrapping), the server
// promotes the keys below into the top level of the persisted
// `result` jsonb so the Claude Lab workbench can render them
// without parsing markdown. Agents that still emit free-form
// markdown text fall through to the legacy code path — the
// existing result_summary extractor picks up the markdown for the
// Plan timeline regardless.
var (
	resultEnvelopeKeyAttachments = "attachments"
	resultEnvelopeKeyPredictions = "predictions"
	resultEnvelopeKeyCodeBlocks  = "code_blocks"
)

// json.Unmarshal depth / key-count limits. The agent-emitted
// Output can be up to 4 MB after the CompleteTask body cap — a
// deeply-nested object of `{"a":{"a":...}}` × 10^5 layers causes
// json.Unmarshal to blow the goroutine stack with a StackOverflow
// panic; a flat object with 10^7 keys allocates gigabytes. We bound
// both before Unmarshal so a malicious or buggy agent can't DoS the
// server via CompleteTask. The bound is generous for the v2 envelope
// contract (typical envelope: 4 attachments, 3 predictions, 2 code
// blocks) — anything bigger is rejected with 400 + logged.
const (
	maxJSONDepth    = 32   // 32 nested object/array levels
	maxJSONKeyCount = 4096 // total distinct keys across the parsed tree
)

// jsonBytesExceedsLimits reports whether raw parses cleanly AND
// fits inside the depth / key-count budgets. Cheaper than parsing
// then walking — walks the raw bytes once with a tiny state
// machine, tracking bracket depth and counting "key" tokens (an
// unquoted identifier, a string literal, or a number prefix
// immediately following `{` or `,`).
//
// Worst case the walk costs O(n) over up to 4 MB — cheap. It errs
// on the side of accepting (false negatives are impossible to
// avoid without actually parsing), but the only way an envelope
// slips past is if it's deep AND wide at the same time, which the
// Claude Lab v2 contract explicitly forbids.
func jsonBytesExceedsLimits(raw []byte) bool {
	depth := 0
	maxDepth := 0
	keys := 0
	i := 0
	for i < len(raw) {
		c := raw[i]
		switch c {
		case '{', '[':
			depth++
			if depth > maxDepth {
				maxDepth = depth
				if maxDepth > maxJSONDepth {
					return true
				}
			}
			i++
		case '}', ']':
			depth--
			i++
		case '"':
			// Skip the string literal. json.Valid does this
			// already — we just need an approximate count of
			// string keys. Walk to the closing unescaped quote.
			keys++
			if keys > maxJSONKeyCount {
				return true
			}
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
		default:
			i++
		}
	}
	return false
}

// buildTaskResultJSON produces the bytes that get stored in
// agent_task_queue.result (jsonb). The default shape mirrors the
// inbound TaskCompleteRequest (pr_url / output / session_id /
// work_dir) so older callers / tests keep their existing surface.
//
// When req.Output parses as a JSON object AND carries one of the
// 0.3.40 v2 Claude Lab workbench keys (attachments / predictions /
// code_blocks), we promote those keys to the top level of the
// envelope and demote the agent's textual Output into the `output`
// field of the same envelope. Agents that emit only plain markdown
// (no JSON) fall through to the default shape — the lab workbench
// already renders that path via extractResultSummary.
//
// 0.3.42: use `json.Valid` (zero-allocation O(n) check) instead of
// the previous `HasPrefix("{") && HasSuffix("}")` heuristic. The
// heuristic could be bypassed by a 4 MB string starting with `{`
// and ending with `}` that wasn't actually valid JSON — json.Valid
// short-circuits cheaply before json.Unmarshal allocates a parse
// tree.
//
// 0.3.43: depth + key-count guard before Unmarshal. A 4 MB blob
// with `{"a":{"a":...}}` × 10^5 layers panics the goroutine stack;
// a flat object with 10^7 keys allocates gigabytes. The bound is
// tuned to the v2 envelope contract (typical: 4 attachments, 3
// predictions, 2 code blocks) — anything bigger returns nil to
// signal "fall through to the legacy envelope" and the caller logs
// a warning so the operator can spot a runaway agent.
//
// 0.3.43: deep-copy the promoted slices/maps at the boundary so
// the persisted envelope doesn't share backing arrays with the
// parser-owned tree. The parser recycles its internal buffer
// after the request returns; without the copy, a later
// post-Marshal mutation (e.g. dedupe attachments) would corrupt
// the next request that happens to reuse the same buffer pool.
// json.Marshal + json.Unmarshal into a generic target gives us
// the deep copy cheaply.
//
// We deliberately do NOT touch pr_url / session_id / work_dir when
// the JSON envelope is present: those stay at the top level so the
// run-finished audit / restart hooks keep working unchanged.
func buildTaskResultJSON(req TaskCompleteRequest) ([]byte, error) {
	envelope := map[string]any{
		"pr_url":     req.PRURL,
		"output":     req.Output,
		"session_id": req.SessionID,
		"work_dir":   req.WorkDir,
	}
	trimmed := strings.TrimSpace(req.Output)
	if trimmed == "" {
		return json.Marshal(envelope)
	}
	// json.Valid is the cheap gate — it scans the bytes once and
	// returns false on any malformed JSON, including trailing
	// garbage after a closing `}`. Falling through here means
	// the legacy {pr_url, output, session_id, work_dir} envelope
	// is returned unchanged.
	if !json.Valid([]byte(trimmed)) {
		return json.Marshal(envelope)
	}
	// Depth / key-count gate. json.Valid passed, so the bytes are
	// syntactically valid — this catches pathological shapes that
	// would blow up Unmarshal. We bail to legacy envelope on
	// overflow (the alternative is 400, but legacy envelope is
	// more permissive and matches the documented fall-through
	// contract).
	if jsonBytesExceedsLimits([]byte(trimmed)) {
		slog.Warn("complete task: output exceeds json depth/key limits, falling back to legacy envelope",
			"output_bytes", len(trimmed),
			"max_depth", maxJSONDepth,
			"max_keys", maxJSONKeyCount,
		)
		return json.Marshal(envelope)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		// Defensive: Valid said yes but Unmarshal failed (rare —
		// number-precision edges). Keep legacy envelope.
		return json.Marshal(envelope)
	}
	// Promote the structured deliverables to the top level of the
	// persisted envelope, then replace the `output` slot with the
	// envelope's own `output` field if present (so a JSON-only
	// reply still surfaces a readable summary on the timeline).
	if rawOut, hasOut := parsed["output"]; hasOut {
		if s, ok := rawOut.(string); ok {
			envelope["output"] = s
		}
	}
	// Deep-copy each promoted key across the parser-owned /
	// envelope-owned boundary. json.Marshal + Unmarshal into a
	// fresh generic drops any back-reference into the parser's
	// internal buffer pool.
	for _, key := range []string{
		resultEnvelopeKeyAttachments,
		resultEnvelopeKeyPredictions,
		resultEnvelopeKeyCodeBlocks,
	} {
		if _, has := parsed[key]; !has {
			continue
		}
		copied, err := deepCopyJSONValue(parsed[key])
		if err != nil {
			slog.Warn("complete task: failed to deep-copy envelope key",
				"key", key, "err", err,
			)
			continue
		}
		envelope[key] = copied
	}
	return json.Marshal(envelope)
}

// deepCopyJSONValue round-trips a generic JSON value (parsed from
// json.Unmarshal into map[string]any / []any / string / float64 /
// bool / nil) through Marshal+Unmarshal to drop any back-reference
// into the source parser's internal buffer. Cost is one Marshal +
// one Unmarshal — both bounded by the depth/key-count guards
// above, so neither can recurse unboundedly.
func deepCopyJSONValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Handler) CompleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	_, workspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	if !ok {
		return
	}

	// 0.3.42: cap request body to 8 MB so a compromised daemon can't
	// OOM the server by POSTing a giant body. The decoded Output is
	// further capped at 4 MB below — that value lands in jsonb.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)

	var req TaskCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Strip bytes PostgreSQL cannot store BEFORE anything else reads this payload
	// (GH #7098). The whole request is marshalled into agent_task_queue.result,
	// a JSONB column, and encoding/json renders an embedded NUL as \u0000 —
	// which JSONB rejects (SQLSTATE 22P05), rolling the completion transaction
	// back and leaving the task stuck in 'running' forever. Sanitizing here
	// rather than just before the Marshal is deliberate: the context-exhaustion
	// re-route below feeds req.Output into the failure classifier, and that
	// classifier must see exactly the text we are going to persist.
	sanitizeTaskCompleteRequest(&req)

	// 0.3.42: explicit per-field cap on Output. Output is persisted
	// to agent_task_queue.result jsonb — a 50 MB blob there bloats
	// every subsequent lab-context read.
	const maxTaskOutputBytes = 4 << 20
	if len(req.Output) > maxTaskOutputBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "output exceeds 4 MB cap")
		return
	}

	result, err := buildTaskResultJSON(req)
	if err != nil {
		slog.Error("complete task: failed to encode task result",
			"task_id", taskID, "err", err,
		)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// GH #6402: a daemon whose backend does not (yet) read the provider's
	// structured terminal reason reports a context-exhausted run as a clean
	// success, with the CLI's "your context window is full" notice as the
	// answer. Re-route it to the failure path here so the fix does not have to
	// wait for every installed daemon to update: an un-upgraded host would
	// otherwise keep publishing that notice as the agent's reply AND keep the
	// dead session pinned as the resume pointer, which is a permanently stuck
	// (agent, issue) pair rather than a mislabelled row. A current daemon
	// classifies this before it ever calls /complete, so this branch is dead
	// weight for it — by design.
	if taskfailure.ContextExhaustedCompletion(req.Output) {
		slog.Warn("complete task: output is a provider context-exhaustion notice, recording as failed",
			"task_id", taskID,
			"failure_reason", taskfailure.ReasonAgentContextOverflow,
		)
		h.failTask(w, r, taskID, workspaceID, TaskFailRequest{
			Error:         req.Output,
			FailureReason: string(taskfailure.ReasonAgentContextOverflow),
			SessionID:     req.SessionID,
			WorkDir:       req.WorkDir,
		})
		return
	}

	task, err := h.TaskService.CompleteTask(r.Context(), parseUUID(taskID), result, req.SessionID, req.WorkDir)
	if err != nil {
		// 0.3.43: don't leak pgx/sqlc constraint strings to the
		// daemon. The previous `err.Error()` body could include
		// schema/column names and table constraints. Log full
		// detail server-side, surface a generic body. Validation-
		// class 4xx errors (e.g. unknown task id, already-completed
		// task) are surfaced via the service's sentinel errors
		// and handled by the project-wide generic-message contract
		// (see .omc/decisions/0.3.42-err-error-audit-scope.md).
		slog.Error("complete task failed",
			"task_id", taskID, "err", err,
		)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.emitIssueExecutedOnFirstCompletion(r, task)

	// Best-effort revoke of any agent task token minted at claim time.
	// The token would naturally expire at the 24h watermark and is also
	// cascaded on agent_task deletion, but eagerly deleting it on
	// completion shrinks the window where a compromised agent process
	// can keep making API calls after its task finishes. Failure here is
	// non-fatal; the expiry / cascade are the durable guards.
	if err := h.Queries.DeleteTaskTokensByTask(r.Context(), task.ID); err != nil {
		slog.Warn("complete task: failed to revoke task tokens", "task_id", uuidToString(task.ID), "error", err)
	}

	// MUL-4195 / MUL-4304 at-least-once comment reconciliation. After a run
	// completes, replay every undelivered member comment that landed during
	// the run (or whose trigger_comment_id was folded into the task after
	// its claim response was built) so the agent picks them up next. Best
	// effort: an error here is logged but never fails the completion — the
	// task has already reached a terminal state server-side.
	h.reconcileCommentsOnCompletion(r.Context(), task)

	// 0.3.45.4 (P0#3.5): the lab runner (mythos_swarm) writes
	// issue.status='done' on its own terminal branch (0.3.45.3
	// P0#3). The non-lab / lab-agent path through this handler
	// did not — claude_science_lab's research agent runs through
	// agent_task_queue and was leaving bound issues stuck in
	// 'in_review' forever. Now: when a task completes successfully
	// against an issue whose status is still 'in_review' (the
	// agent-finished marker per
	// internal/daemon/execenv/runtime_config.go:722), flip the
	// issue to 'done' so the issue list reflects "agent finished
	// work" the same way the lab-side runner already does. The
	// UpdateIssueStatus workspace_id predicate keeps the write
	// tenant-safe.
	//
	// 0.3.45.5 (P0#3.6): expand the trigger to also flip
	// 'todo' → 'done'. A daemon restart (or any other path that
	// re-dispatches a task whose previous run already wrote an
	// 'in_review' comment) resets the issue to 'todo' so the
	// scheduler can pick it up again — and the agent's final
	// state is "task done, do not re-dispatch", so the issue
	// should be 'done' regardless of whether the prior run left
	// it as 'in_review' or 'todo'. Anything stricter (e.g.
	// 'in_progress' → 'done') is intentionally out of scope:
	// 'in_progress' means the daemon is still running, and the
	// task that just completed is a child task, not the issue
	// itself. Only flip the two states the agent is expected to
	// leave behind on completion. The gate keys on
	// agent_task_queue.status, whose terminal enum is 'completed'
	// (not 'done' — 'done' is an issue.status value).
	if task.IssueID.Valid && task.Status == "completed" {
		issueRow, ierr := h.Queries.GetIssue(r.Context(), task.IssueID)
		// Fork deviation (MUL-6243): resolve to the canonical category so a
		// custom status in the todo/in_review category still flips to done
		// when the agent completes — the daemon-side mirror of the
		// service-layer Effective normalization.
		if ierr == nil {
			effective := issuestatus.Effective(r.Context(), h.Queries, issueRow.WorkspaceID, issueRow.Status)
			if effective == "in_review" || effective == "todo" {
				if _, uerr := h.Queries.UpdateIssueStatus(r.Context(), db.UpdateIssueStatusParams{
					ID:          task.IssueID,
					Status:      "done",
					WorkspaceID: issueRow.WorkspaceID,
				}); uerr != nil {
					slog.Warn("complete task: issue status to done failed",
						"task_id", taskID, "issue", task.IssueID, "err", uerr)
				} else {
					// 0.5.38 (fork-local): the flip above bypasses the HTTP
					// UpdateIssue handler, so the platform-driven parent
					// notification (notifyParentOfChildDone) never fired for
					// agent-completed child issues — a parent agent whose
					// sub-issues finished via the daemon completion path got no
					// system comment and no wake-up, and had to be polled
					// manually. Re-read the flipped row and notify exactly like
					// the UpdateIssue path does. The stage-barrier + parent
					// guards inside make this a no-op for non-children and for
					// completions that do not close a stage.
					if updated, ierr := h.Queries.GetIssue(r.Context(), task.IssueID); ierr == nil {
						h.notifyParentOfChildDone(r.Context(), issueRow, updated)
					}
					// H5 (audit 2026-09-06): this daemon path mutates
					// issue.status directly without going through the HTTP
					// UpdateIssue handler, so the RefreshForIssue call the
					// handler makes at L3386 is bypassed. Active Contract
					// #9 silent bypass — same root cause as the
					// BatchUpdateIssues / github advanceIssueToDone /
					// notifyParentOfChildDone gaps.
					if h.CausalRecorder != nil {
						h.CausalRecorder.RefreshForIssue(r.Context(), task.IssueID)
					}
				}
			}
		}
	}

	slog.Info("task completed", "task_id", taskID, "agent_id", uuidToString(task.AgentID))
	writeJSON(w, http.StatusOK, taskToResponse(*task, workspaceID))
}

// emitIssueExecutedOnFirstCompletion atomically flips issue.first_executed_at
// and fires the issue_executed analytics event iff this is the first task on
// the issue to reach terminal done. Retries / re-assignments / comment-
// triggered follow-ups hit the WHERE first_executed_at IS NULL clause and
// no-op, so the funnel counts unique issues, not tasks.
func (h *Handler) emitIssueExecutedOnFirstCompletion(r *http.Request, task *db.AgentTaskQueue) {
	if task == nil {
		return
	}
	marked, err := h.Queries.MarkIssueFirstExecuted(r.Context(), task.IssueID)
	if err != nil {
		if !isNotFound(err) {
			slog.Warn("analytics: mark issue first-executed failed", "issue_id", uuidToString(task.IssueID), "error", err)
		}
		return
	}
	var durationMS int64
	if task.StartedAt.Valid && task.CompletedAt.Valid {
		durationMS = task.CompletedAt.Time.Sub(task.StartedAt.Time).Milliseconds()
	}
	taskContext := h.TaskService.AnalyticsContextForTask(r.Context(), *task)
	// distinct_id prefers the human creator so agent-driven events flow into
	// the issue-author's person profile (same place signup and
	// workspace_created land). Agent-created issues keep the agent id with a
	// prefix so PostHog doesn't merge them into a user by accident.
	distinct := uuidToString(marked.CreatorID)
	if marked.CreatorType == "agent" {
		distinct = "agent:" + distinct
	}
	obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.IssueExecuted(
		distinct,
		uuidToString(marked.WorkspaceID),
		uuidToString(marked.ID),
		uuidToString(task.ID),
		uuidToString(task.AgentID),
		taskContext.Source,
		taskContext.RuntimeMode,
		taskContext.Provider,
		durationMS,
	))
}

// reconcileCommentsOnCompletion closes the at-least-once gap for member
// comments a completing run did NOT deliver (MUL-4195).
//
// The merge path (mergeCommentIntoPendingTask) folds a comment into a task only
// while it is still PRE-CLAIM (queued/deferred), so trigger_comment_id plus
// coalesced_comment_ids is EXACTLY the set the run's claim response carried —
// its "delivered set". Anything a member posted during this run's lifetime that
// is NOT in that set was never delivered and must earn a follow-up.
//
// Anchor = created_at + delivered-set exclusion, NOT a dispatch/start timestamp
// (MUL-4195 review round-3 must-fix). A timestamp anchor cannot tell a
// delivered comment from an undelivered one, and there is a race it structurally
// misses: a comment created while the task was still queued, but whose merge
// lost the race to the daemon claiming the task (queued→dispatched) — the merge
// then finds no pre-claim row (ErrNoRows), the enqueue path defers to reconcile,
// yet the comment's created_at is BEFORE dispatched_at, so a dispatched_at
// anchor would skip it and it would vanish. Anchoring on the task's own
// created_at reaches back over the whole run, and excluding the delivered set
// (trigger + coalesced) is what prevents re-firing comments the run actually
// received. Together they catch the pre-dispatch merge-race comment, the
// dispatch→start comment, and the during-run comment, while never double-firing
// a delivered one.
//
// Scope + loop safety:
//   - MEMBER comments qualify as before, with their full routing. AGENT comments
//     now also qualify, but ONLY through an explicit @agent/@squad mention
//     (keepExplicitMentionTriggers). Every non-mention agent route — the
//     assigned-squad-leader fallback, thread-parent / conversation continuation
//     — is intentionally excluded, so a plain agent reply / acknowledgement
//     earns no follow-up here regardless of issue assignment. That is the
//     anti-loop boundary the old member-only filter protected.
//     This closes MUL-4304: an explicit agent→agent @mention that landed while
//     the target already had a DISPATCHED task is dropped by the create-time
//     enqueue path — merge only folds a comment into a QUEUED task, so a
//     dispatched target hits the merge-miss + active-task `continue` and is
//     deferred here — and was then never replayed because agent comments were
//     excluded. (A target with only a RUNNING/queued task does not hit that
//     drop: queued merges in, running-only takes the normal fresh-enqueue path.)
//   - Only comments routing to THE AGENT THAT JUST RAN earn a follow-up here;
//     an `@other-agent` comment is left to that agent's own creation-time
//     trigger, so a completion never re-wakes an unrelated agent.
//   - Every undelivered qualifying comment is replayed through the normal
//     enqueue path in chronological order, so they coalesce into a SINGLE
//     follow-up task (the first enqueues it, the rest merge in). Bounded to one
//     run, and terminating: the follow-up's own created_at is later than all of
//     these comments and its delivered set will contain them, so its completion
//     finds nothing to re-schedule.
func (h *Handler) reconcileCommentsOnCompletion(ctx context.Context, task *db.AgentTaskQueue) {
	if task == nil || !task.IssueID.Valid || !task.AgentID.Valid || !task.CreatedAt.Valid {
		return
	}
	comments, err := h.Queries.ListReconcilableCommentsForIssueSince(ctx, db.ListReconcilableCommentsForIssueSinceParams{
		IssueID: task.IssueID,
		Since:   task.CreatedAt,
	})
	if err != nil {
		slog.Warn("reconcile comments on completion: list comments failed",
			"issue_id", uuidToString(task.IssueID), "task_id", uuidToString(task.ID), "error", err)
		return
	}
	if len(comments) == 0 {
		return
	}
	// The delivered set: everything this run's claim response actually carried.
	// Because merges only ever touch pre-claim rows, this is exactly
	// trigger_comment_id ∪ coalesced_comment_ids. Any member comment since the
	// task was created that is NOT in here was never delivered to the run.
	delivered := make(map[string]struct{}, len(task.CoalescedCommentIds)+1)
	if task.TriggerCommentID.Valid {
		delivered[uuidToString(task.TriggerCommentID)] = struct{}{}
	}
	for _, id := range task.CoalescedCommentIds {
		if id.Valid {
			delivered[uuidToString(id)] = struct{}{}
		}
	}
	issue, err := h.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("reconcile comments on completion: load issue failed",
			"issue_id", uuidToString(task.IssueID), "error", err)
		return
	}
	agentID := uuidToString(task.AgentID)
	scheduled := 0
	for i := range comments {
		c := comments[i]
		if _, ok := delivered[uuidToString(c.ID)]; ok {
			// Already delivered to this run (trigger or pre-claim coalesced).
			continue
		}
		if isNoteComment(c.Content) {
			continue
		}
		var parentComment *db.Comment
		if c.ParentID.Valid {
			if parent, err := h.Queries.GetComment(ctx, c.ParentID); err == nil {
				parentComment = &parent
			}
		}
		// Compute what this comment would trigger, then keep ONLY the agent
		// that just completed — never the full fan-out (that would re-wake
		// unrelated `@other-agent` targets).
		//
		// The comment is routed under its OWN author_type. A member is its own
		// originator. For an agent author, the originator is the human at the
		// top of that agent's trigger chain (resolved from the comment's source
		// task); canInvokeAgent judges an agent→agent (A2A) mention by that
		// originator, not the immediate agent principal (MUL-3963).
		actorType := c.AuthorType
		actorID := uuidToString(c.AuthorID)
		originatorUserID := actorID
		if actorType != "member" {
			originatorUserID = uuidToString(h.TaskService.ResolveOriginatorFromTriggerComment(ctx, c.ID))
		}
		triggers, _ := h.computeCommentAgentTriggers(ctx, issue, c.Content, parentComment, actorType, actorID, commentTriggerComputeOptions{
			ExcludeTriggerCommentID: c.ID,
			OriginatorUserID:        originatorUserID,
		})
		// For an AGENT author, compensate ONLY explicit @agent/@squad mentions.
		// computeCommentAgentTriggers can also return the assigned-squad-leader
		// fallback (Source = issue-assignee) for a plain worker-agent reply on a
		// squad-assigned issue; that conversational routing is intentionally NOT
		// replayed here. Restricting to the explicit-mention sources keeps the
		// invariant unconditional — a plain agent reply / acknowledgement earns
		// no follow-up regardless of issue assignment — which is the anti-loop
		// boundary the old member-only filter protected (MUL-4304). Member
		// comments are unaffected: they keep their full routing.
		if actorType != "member" {
			triggers = keepExplicitMentionTriggers(triggers)
		}
		scoped := make([]commentAgentTrigger, 0, 1)
		for _, trigger := range triggers {
			if uuidToString(trigger.Agent.ID) == agentID {
				scoped = append(scoped, trigger)
			}
		}
		if len(scoped) == 0 {
			continue
		}
		// The first qualifying comment enqueues the follow-up task; later ones
		// find it AlreadyPending and merge in, so all undelivered comments end
		// up covered by a single bounded run.
		h.enqueueCommentAgentTriggers(ctx, issue, c.ID, scoped, actorType, actorID, originatorUserID)
		scheduled++
	}
	if scheduled > 0 {
		slog.Info("reconcile comments on completion: scheduled follow-up",
			"issue_id", uuidToString(task.IssueID),
			"completed_task_id", uuidToString(task.ID),
			"agent_id", agentID,
			"undelivered_comments", scheduled)
	}
}

// keepExplicitMentionTriggers filters a computed trigger set down to the ones
// produced by an EXPLICIT @agent / @squad mention (MUL-4304). It is applied to
// agent-authored comments during completion reconcile so that only a
// deliberately-targeted mention earns a replay — the assigned-squad-leader
// fallback, thread-parent / conversation continuation, and issue-assignee
// routing (all non-mention sources) are intentionally excluded, so a plain
// agent reply or acknowledgement never earns a follow-up here. Member comments
// are never passed through this filter; they keep their full routing.
func keepExplicitMentionTriggers(triggers []commentAgentTrigger) []commentAgentTrigger {
	if len(triggers) == 0 {
		return triggers
	}
	filtered := make([]commentAgentTrigger, 0, len(triggers))
	for _, trigger := range triggers {
		switch trigger.Source {
		case commentTriggerSourceMentionAgent, commentTriggerSourceMentionSquadLeader:
			filtered = append(filtered, trigger)
		}
	}
	return filtered
}

// buildCoalescedCommentData loads the full detail of each comment that was
// folded into a not-yet-started run (MUL-4195) so the claim response can embed
// them and the prompt can address each without assuming they share the
// triggering thread (review should-fix #3). Thread id follows the same rule as
// the triggering comment (parent id when the comment is a reply, else the
// comment's own id). Missing comments (deleted / wrong workspace) are skipped
// rather than failing the claim. The set is bounded by how many comments a user
// fires before a run starts, so the per-comment lookups stay cheap.
func (h *Handler) buildCoalescedCommentData(ctx context.Context, ids []pgtype.UUID) []CoalescedCommentData {
	if len(ids) == 0 {
		return nil
	}
	out := make([]CoalescedCommentData, 0, len(ids))
	for _, id := range ids {
		if !id.Valid {
			continue
		}
		comment, err := h.Queries.GetComment(ctx, id)
		if err != nil {
			continue
		}
		data := CoalescedCommentData{
			ID:         uuidToString(comment.ID),
			ThreadID:   uuidToString(comment.ID),
			AuthorType: comment.AuthorType,
			Content:    comment.Content,
			CreatedAt:  timestampToString(comment.CreatedAt),
		}
		if comment.ParentID.Valid {
			data.ThreadID = uuidToString(comment.ParentID)
		}
		if comment.AuthorID.Valid {
			switch comment.AuthorType {
			case "agent":
				if a, err := h.Queries.GetAgent(ctx, comment.AuthorID); err == nil {
					data.AuthorName = a.Name
				}
			case "member":
				if u, err := h.Queries.GetUser(ctx, comment.AuthorID); err == nil {
					data.AuthorName = u.Name
				}
			}
		}
		out = append(out, data)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ReportTaskUsage stores per-task token usage. Called independently of
// complete/fail so usage is captured even when tasks fail or are blocked.
type TaskUsagePayload struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
}

func (h *Handler) ReportTaskUsage(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	var req struct {
		Usage []TaskUsagePayload `json:"usage"`
		// McpCalls is task-level (no per-model split). Pointer so an older
		// daemon that omits the field leaves the stored count untouched
		// instead of zeroing it; negative values are rejected outright.
		McpCalls *int `json:"mcp_calls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.McpCalls != nil && *req.McpCalls < 0 {
		writeError(w, http.StatusBadRequest, "mcp_calls must be >= 0")
		return
	}

	// Provider is lowercased on write so client-side pricing lookups tolerate
	// case drift. An empty provider (an older daemon that omits the field) is
	// stamped from the task's runtime, so generic model ids like `auto` still
	// resolve to a provider instead of landing as '' and pricing $0.
	var runtimeProvider string
	runtimeProviderLoaded := false
	for _, u := range req.Usage {
		provider := normalizeProvider(u.Provider)
		if provider == "" {
			if !runtimeProviderLoaded {
				if rt, err := h.Queries.GetAgentRuntime(r.Context(), task.RuntimeID); err == nil {
					runtimeProvider = normalizeProvider(rt.Provider)
				} else {
					slog.Warn("load runtime provider for usage backfill failed",
						"task_id", taskID, "runtime_id", uuidToString(task.RuntimeID), "error", err)
				}
				runtimeProviderLoaded = true
			}
			provider = runtimeProvider
		}
		if err := h.Queries.UpsertTaskUsage(r.Context(), db.UpsertTaskUsageParams{
			TaskID:           parseUUID(taskID),
			Provider:         provider,
			Model:            u.Model,
			InputTokens:      u.InputTokens,
			OutputTokens:     u.OutputTokens,
			CacheReadTokens:  u.CacheReadTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		}); err != nil {
			slog.Warn("upsert task usage failed", "task_id", taskID, "model", u.Model, "error", err)
			continue
		}
		h.TaskService.CaptureTaskUsage(r.Context(), task, provider, u.Model, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}

	// Task-level MCP tool-call volume rides the same channel so blocked runs
	// are captured. Written even when the usage list is empty — a task can
	// invoke MCP tools without accumulating any token entries.
	if req.McpCalls != nil {
		if err := h.Queries.SetTaskMcpCalls(r.Context(), db.SetTaskMcpCallsParams{
			TaskID:   parseUUID(taskID),
			McpCalls: int32(*req.McpCalls),
		}); err != nil {
			slog.Warn("set task mcp calls failed", "task_id", taskID, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetTaskStatus returns the current status of a task.
// Used by the daemon to detect terminal/interruption signals (cancelled,
// failed, completed) while a task is executing mid-flight.
func (h *Handler) GetTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": task.Status})
}

// FailTask marks a running task as failed.
type TaskFailRequest struct {
	Error         string `json:"error"`
	SessionID     string `json:"session_id,omitempty"`
	WorkDir       string `json:"work_dir,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
}

func (h *Handler) FailTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	_, workspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	if !ok {
		return
	}

	var req TaskFailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// TaskService.FailTask normalizes req.Error itself, but every other field
	// here lands in a TEXT column too and a NUL in any one of them fails the
	// same transaction (GH #7098).
	sanitizeTaskFailRequest(&req)

	h.failTask(w, r, taskID, workspaceID, req)
}

// failTask records a terminal failure and writes the response. Shared by the
// /fail endpoint and by CompleteTask's context-exhaustion normalization so a
// run re-classified at the /complete boundary lands through exactly the same
// transaction, token revocation and runtime wake-up as one the daemon reported
// as failed itself.
func (h *Handler) failTask(w http.ResponseWriter, r *http.Request, taskID, workspaceID string, req TaskFailRequest) {
	task, err := h.TaskService.FailTask(r.Context(), parseUUID(taskID), req.Error, req.SessionID, req.WorkDir, req.FailureReason)
	if err != nil {
		slog.Warn("fail task failed", "task_id", taskID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Best-effort revoke of the mat_ task token minted at claim. Same
	// rationale as CompleteTask — eager deletion shrinks the post-
	// terminal window. The 24h expiry / cascade are the durable guards.
	if err := h.Queries.DeleteTaskTokensByTask(r.Context(), task.ID); err != nil {
		slog.Warn("fail task: failed to revoke task tokens", "task_id", uuidToString(task.ID), "error", err)
	}

	slog.Info("task failed", "task_id", taskID, "agent_id", uuidToString(task.AgentID), "task_error", req.Error, "failure_reason", req.FailureReason)
	writeJSON(w, http.StatusOK, taskToResponse(*task, workspaceID))
}

// ---------------------------------------------------------------------------
// Task Messages (live agent output)
// ---------------------------------------------------------------------------

type TaskMessageRequest struct {
	Seq     int            `json:"seq"`
	Type    string         `json:"type"`
	Tool    string         `json:"tool,omitempty"`
	Content string         `json:"content,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Output  string         `json:"output,omitempty"`
}

type TaskMessageBatchRequest struct {
	Messages []TaskMessageRequest `json:"messages"`
}

// ReportTaskMessages receives a batch of agent execution messages from the daemon.
func (h *Handler) ReportTaskMessages(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	var req TaskMessageBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Verify the caller owns this task's workspace.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	workspaceID := ""
	if task.IssueID.Valid {
		if issue, err := h.Queries.GetIssue(r.Context(), task.IssueID); err == nil {
			workspaceID = uuidToString(issue.WorkspaceID)
		}
	}
	if workspaceID == "" && task.ChatSessionID.Valid {
		if cs, err := h.Queries.GetChatSession(r.Context(), task.ChatSessionID); err == nil {
			workspaceID = uuidToString(cs.WorkspaceID)
		}
	}

	for _, msg := range req.Messages {
		// Redact sensitive information before persisting or broadcasting.
		msg.Content = redact.Text(msg.Content)
		msg.Output = redact.Text(msg.Output)
		msg.Input = redact.InputMap(msg.Input)

		// redact.Text only masks secret-shaped substrings; it has no opinion on
		// bytes PostgreSQL refuses to store. Tool output is the likeliest
		// carrier of a stray NUL in the whole system — an agent that cats a
		// binary, or a Windows tool emitting UTF-16 — and this endpoint has no
		// retry on the daemon side, so an unsanitized batch is silently lost
		// (GH #7098). Input is a JSONB column, so it needs the deep walk: the
		// offending byte can sit at any depth of a tool's arguments.
		msg.Type = util.SanitizeTextForPostgres(msg.Type)
		msg.Tool = util.SanitizeTextForPostgres(msg.Tool)
		msg.Content = util.SanitizeTextForPostgres(msg.Content)
		msg.Output = util.SanitizeTextForPostgres(msg.Output)
		if msg.Input != nil {
			if cleaned, ok := util.SanitizeJSONForPostgres(msg.Input).(map[string]any); ok {
				msg.Input = cleaned
			}
		}

		var inputJSON []byte
		if msg.Input != nil {
			inputJSON, _ = json.Marshal(msg.Input)
		}
		created, createErr := h.Queries.CreateTaskMessage(r.Context(), db.CreateTaskMessageParams{
			TaskID:  parseUUID(taskID),
			Seq:     int32(msg.Seq),
			Type:    msg.Type,
			Tool:    pgtype.Text{String: msg.Tool, Valid: msg.Tool != ""},
			Content: pgtype.Text{String: msg.Content, Valid: msg.Content != ""},
			Input:   inputJSON,
			Output:  pgtype.Text{String: msg.Output, Valid: msg.Output != ""},
		})
		if createErr != nil {
			slog.Error("failed to create task message", "task_id", taskID, "seq", msg.Seq, "error", createErr)
			writeError(w, http.StatusInternalServerError, "failed to persist task message")
			return
		}

		if workspaceID != "" {
			h.publishTask(protocol.EventTaskMessage, workspaceID, "system", "", taskID,
				truncateTaskMessageForBroadcast(
					taskMessageToPayload(created, taskID, uuidToString(task.IssueID))))
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func taskMessageToPayload(m db.TaskMessage, taskID, issueID string) protocol.TaskMessagePayload {
	var input map[string]any
	if m.Input != nil {
		json.Unmarshal(m.Input, &input)
	}
	createdAt := ""
	if m.CreatedAt.Valid {
		createdAt = m.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return protocol.TaskMessagePayload{
		TaskID:    taskID,
		IssueID:   issueID,
		Seq:       int(m.Seq),
		Type:      m.Type,
		Tool:      m.Tool.String,
		Content:   m.Content.String,
		Input:     input,
		Output:    m.Output.String,
		CreatedAt: createdAt,
	}
}

// taskMessageBroadcastClipEnv gates the clipping below. It is OFF by default,
// and must stay off until clients that understand `truncated` have saturated.
//
// Clipping is not an additive field a client can ignore: it changes the meaning
// of `input` / `output`, which every existing client already consumes. A client
// built before this PR writes the clipped copy into a `staleTime: Infinity`
// cache and never refetches, so its execution log would stay incomplete until
// the window is reloaded. The client half of this change (the `truncated`
// reader) ships first; flipping this env var is the second step, once installed
// builds have caught up.
//
// Routing by connection capability instead would be the principled fix, but the
// hub does not retain the `client_version` it is handed at upgrade, and frames
// cross nodes through the Redis relay as already-serialized bytes — so it needs
// the same protocol work that per-task scope routing is waiting on
// (server/cmd/server/listeners.go). Deliberately one env var, not that.
const taskMessageBroadcastClipEnv = "MULTICA_CLIP_TASK_MESSAGE_BROADCAST"

// taskMessageBroadcastClipEnabled reports whether oversized tool input/output
// should be clipped out of the realtime copy of a task message.
func taskMessageBroadcastClipEnabled() bool {
	v := strings.TrimSpace(os.Getenv(taskMessageBroadcastClipEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// Byte budgets for the realtime fanout of a task message (MUL-6396).
//
// A `task:message` frame is broadcast to EVERY client in the workspace, and a
// tool_use input is unbounded: a Write of a large file, or a MultiEdit with
// long old/new strings, ships the whole body to every open client, which then
// retains it. Persisted rows keep the full content — only the broadcast copy
// is clipped, and `Truncated` tells the client to backfill from the REST
// endpoint when it actually needs the full text.
const (
	// broadcastStringLimit caps each individual string inside Input, so small
	// fields (file_path, command, description) survive intact and only the
	// genuinely large ones are clipped.
	broadcastStringLimit = 4096
	// broadcastInputLimit caps the serialized Input after per-string clipping.
	// A map with thousands of small keys can still be large; past this the
	// input is dropped entirely and the client backfills.
	broadcastInputLimit = 16384
	// broadcastOutputLimit mirrors the daemon-side cap on tool output
	// (see daemon.reportMessages). Belt and braces: other producers, and any
	// future relaxation of the daemon cap, must not reopen the fanout hole.
	broadcastOutputLimit = 8192
)

// clipUTF8 truncates s to at most limit bytes without splitting a rune.
func clipUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// clipJSONValue returns v with every string at any depth clipped to
// broadcastStringLimit. It never mutates the input: containers are rebuilt
// only when something inside them actually changed.
func clipJSONValue(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		if len(t) <= broadcastStringLimit {
			return t, false
		}
		return clipUTF8(t, broadcastStringLimit), true
	case map[string]any:
		var out map[string]any
		for k, val := range t {
			clipped, did := clipJSONValue(val)
			if !did {
				continue
			}
			if out == nil {
				out = make(map[string]any, len(t))
				for ck, cv := range t {
					out[ck] = cv
				}
			}
			out[k] = clipped
		}
		if out == nil {
			return t, false
		}
		return out, true
	case []any:
		var out []any
		for i, val := range t {
			clipped, did := clipJSONValue(val)
			if !did {
				continue
			}
			if out == nil {
				out = make([]any, len(t))
				copy(out, t)
			}
			out[i] = clipped
		}
		if out == nil {
			return t, false
		}
		return out, true
	default:
		return v, false
	}
}

// truncateTaskMessageForBroadcast clips the realtime copy of a task message so
// one oversized tool call cannot flood every client in the workspace. The
// returned payload is a copy; the caller's row and the REST responses built
// from it are untouched.
func truncateTaskMessageForBroadcast(p protocol.TaskMessagePayload) protocol.TaskMessagePayload {
	if !taskMessageBroadcastClipEnabled() {
		return p
	}

	truncated := false

	if len(p.Output) > broadcastOutputLimit {
		p.Output = clipUTF8(p.Output, broadcastOutputLimit)
		truncated = true
	}

	if p.Input != nil {
		clipped, didClip := clipJSONValue(p.Input)
		if didClip {
			truncated = true
			if m, ok := clipped.(map[string]any); ok {
				p.Input = m
			}
		}
		// Re-measure: per-string clipping bounds each value, not the total.
		if encoded, err := json.Marshal(p.Input); err != nil || len(encoded) > broadcastInputLimit {
			p.Input = nil
			truncated = true
		}
	}

	p.Truncated = truncated
	return p
}

// ListTaskMessages returns the persisted messages for a task (for catch-up after reconnect).
func (h *Handler) ListTaskMessages(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	var (
		messages []db.TaskMessage
		err      error
	)
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		sinceSeq, parseErr := strconv.Atoi(sinceStr)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid since parameter")
			return
		}
		messages, err = h.Queries.ListTaskMessagesSince(r.Context(), db.ListTaskMessagesSinceParams{
			TaskID: parseUUID(taskID),
			Seq:    int32(sinceSeq),
		})
	} else {
		messages, err = h.Queries.ListTaskMessages(r.Context(), parseUUID(taskID))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list task messages")
		return
	}

	issueID := uuidToString(task.IssueID)

	resp := make([]protocol.TaskMessagePayload, len(messages))
	for i, m := range messages {
		resp[i] = taskMessageToPayload(m, taskID, issueID)
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetActiveTaskForIssue returns all currently active tasks for an issue.
// Returns { tasks: [...] } array (may be empty).
func (h *Handler) GetActiveTaskForIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	tasks, err := h.Queries.ListActiveTasksByIssue(r.Context(), issue.ID)
	if err != nil {
		tasks = nil
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	resp := make([]AgentTaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = taskToResponse(t, workspaceID)
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": resp})
}

// CancelTask cancels a running or queued task by ID.
// Verifies both that the URL-parameter issue belongs to the caller's workspace
// and that the task belongs to that same issue — a task UUID from a different
// issue (in any workspace) must not be cancellable through this route.
func (h *Handler) CancelTask(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "taskId")
	existing, err := h.Queries.GetAgentTask(r.Context(), parseUUID(taskID))
	if err != nil || uuidToString(existing.IssueID) != uuidToString(issue.ID) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	task, err := h.TaskService.CancelTask(r.Context(), existing.ID)
	if err != nil {
		slog.Warn("cancel task failed", "task_id", taskID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("task cancelled by user", "task_id", taskID, "issue_id", uuidToString(task.IssueID))
	writeJSON(w, http.StatusOK, taskToResponse(*task, uuidToString(issue.WorkspaceID)))
}

// ListTasksByIssue returns all tasks (any status) for an issue — used for execution history.
func (h *Handler) ListTasksByIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	tasks, err := h.Queries.ListTasksByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tasks")
		return
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	resp := make([]AgentTaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = taskToResponse(t, workspaceID)
	}
	// Upstream also hydrates initiator/originator display names here via
	// hydrateTaskAttributions (MUL-4302) — that attribution chain is a separate
	// workstream this fork has not integrated, so it is intentionally omitted.
	h.hydrateTaskUsage(r.Context(), issue.ID, resp)

	writeJSON(w, http.StatusOK, resp)
}

// hydrateTaskUsage attaches each run's own token usage to the execution-log
// rows. One query for the whole issue, then a map join — not one query per
// task, which would be an N+1 over a list the UI always renders in full.
//
// Usage is display metadata: a failure here must not take the execution log
// down with it, so the error is swallowed and every row keeps its nil Usage,
// which the UI already renders as "no usage recorded".
func (h *Handler) hydrateTaskUsage(ctx context.Context, issueID pgtype.UUID, resp []AgentTaskResponse) {
	if len(resp) == 0 {
		return
	}

	rows, err := h.Queries.ListIssueTaskUsage(ctx, issueID)
	if err != nil || len(rows) == 0 {
		return
	}

	byTask := make(map[string][]TaskUsageData, len(resp))
	for _, row := range rows {
		var cost *int64
		if row.CostUsdTicks.Valid {
			v := row.CostUsdTicks.Int64
			cost = &v
		}
		taskID := uuidToString(row.TaskID)
		byTask[taskID] = append(byTask[taskID], TaskUsageData{
			Provider:         row.Provider,
			Model:            row.Model,
			InputTokens:      row.InputTokens,
			OutputTokens:     row.OutputTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
			CostUsdTicks:     cost,
		})
	}

	for i := range resp {
		if usage, ok := byTask[resp[i].ID]; ok {
			resp[i].Usage = usage
		}
	}
}

// ListTaskMessagesByUser returns task messages for a task.
// Used by the frontend under regular user auth (not daemon auth).
// Verifies the task belongs to the caller's workspace.
func (h *Handler) ListTaskMessagesByUser(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task_id")
	if !ok {
		return
	}

	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	// Verify the task belongs to the caller's workspace.
	wsID := h.TaskService.ResolveTaskWorkspaceID(r.Context(), task)
	if wsID == "" || wsID != middleware.WorkspaceIDFromContext(r.Context()) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	var (
		messages []db.TaskMessage
		queryErr error
	)
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		sinceSeq, parseErr := strconv.Atoi(sinceStr)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid since parameter")
			return
		}
		messages, queryErr = h.Queries.ListTaskMessagesSince(r.Context(), db.ListTaskMessagesSinceParams{
			TaskID: taskUUID,
			Seq:    int32(sinceSeq),
		})
	} else {
		messages, queryErr = h.Queries.ListTaskMessages(r.Context(), taskUUID)
	}
	if queryErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to list task messages")
		return
	}

	issueID := uuidToString(task.IssueID)

	resp := make([]protocol.TaskMessagePayload, len(messages))
	for i, m := range messages {
		resp[i] = taskMessageToPayload(m, taskID, issueID)
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetIssueUsage returns aggregated token usage for all tasks belonging to an issue.
func (h *Handler) GetIssueUsage(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	row, err := h.Queries.GetIssueUsageSummary(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get issue usage")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_input_tokens":       row.TotalInputTokens,
		"total_output_tokens":      row.TotalOutputTokens,
		"total_cache_read_tokens":  row.TotalCacheReadTokens,
		"total_cache_write_tokens": row.TotalCacheWriteTokens,
		"task_count":               row.TaskCount,
	})
}

// GetIssueGCCheck returns minimal issue info needed by the daemon GC loop.
// Gated on workspace access so a daemon token scoped to workspace A cannot
// read issue metadata from workspace B via UUID enumeration.
func (h *Handler) GetIssueGCCheck(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueId")
	issueUUID, ok := parseUUIDOrBadRequest(w, issueID, "issue_id")
	if !ok {
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), issueUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(issue.WorkspaceID)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// Normalize server-side so the daemon's terminal-status test stays
		// correct against a custom status in the done/cancelled category.
		// (MUL-6243)
		"status":     issuestatus.Effective(r.Context(), h.Queries, issue.WorkspaceID, issue.Status),
		"updated_at": issue.UpdatedAt.Time,
	})
}

// GetChatSessionGCCheck returns the status and updated_at of a chat session
// for the daemon GC loop. A 404 here means the session was hard-deleted
// (DeleteChatSession in chat.go runs a real DELETE), which the daemon treats
// as an immediate-clean signal — the user's explicit delete is the strongest
// reclaim authorization we can get.
//
// Same anti-enumeration shape as GetIssueGCCheck: workspace mismatch returns
// the same 404 so a scoped daemon token can't probe other workspaces.
func (h *Handler) GetChatSessionGCCheck(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	sessionUUID, ok := parseUUIDOrBadRequest(w, sessionID, "session_id")
	if !ok {
		return
	}
	session, err := h.Queries.GetChatSession(r.Context(), sessionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "chat session not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(session.WorkspaceID)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     session.Status,
		"updated_at": session.UpdatedAt.Time,
	})
}

// GetAutopilotRunGCCheck returns the status and completed_at of an autopilot
// run for the daemon GC loop. The daemon decides purely on terminal status:
// an autopilot run's workdir is never reused, so a terminal run is reclaimed on
// sight while non-terminal status is a skip signal — completed_at is returned
// for the API contract and diagnostics, not as a TTL anchor.
//
// Workspace ownership is resolved via the parent autopilot row.
func (h *Handler) GetAutopilotRunGCCheck(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	runUUID, ok := parseUUIDOrBadRequest(w, runID, "run_id")
	if !ok {
		return
	}
	run, err := h.Queries.GetAutopilotRun(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "autopilot run not found")
		return
	}
	autopilot, err := h.Queries.GetAutopilot(r.Context(), run.AutopilotID)
	if err != nil {
		// Parent autopilot is gone — treat as not found rather than 500
		// so the daemon can fall through to its orphan-by-mtime path.
		writeError(w, http.StatusNotFound, "autopilot run not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(autopilot.WorkspaceID)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       run.Status,
		"completed_at": run.CompletedAt.Time,
	})
}

// GetTaskGCCheck returns the agent_task_queue status for quick-create cleanup.
// Quick-create tasks have no parent record (no issue_id at WriteGCMeta time,
// no chat session, no autopilot run) so the daemon keys GC directly on the
// task row itself.
func (h *Handler) GetTaskGCCheck(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       task.Status,
		"completed_at": task.CompletedAt.Time,
	})
}

// loadSystemPromptBinding resolves an `agent.system_key` to the body
// text that should be prepended to the agent's Instructions at
// dispatch time. The mapping is intentionally minimal: each key
// points at exactly one builtin Skill whose body becomes the root
// system prompt. Adding a new system prompt means (a) adding a Skill
// under server/internal/service/builtin_skills/multica-<name>/, and
// (b) adding a case here.
//
// Returns (body, true) on hit; ("", false) when the key is unknown
// or the Skill file is missing. The caller logs a warning so an
// uninstalled Skill doesn't silently strip the binding.
//
// 0.3.51: initial wiring with one binding — constitution_agent_v1 →
// multica-constitution-agent. 0.3.57: that binding was retired
// alongside the constitution_agent lab (migration 165); the case
// stays here as an explicit (zero, false) so historical user-saved
// agents carrying system_key="constitution_agent_v1" silently downgrade
// to no binding rather than 500-ing. New bindings drop in here later
// via the same loadBuiltinSkill helper used by the Skill catalogue.
func loadSystemPromptBinding(key string) (string, bool) {
	switch key {
	case "constitution_agent_v1":
		// 0.3.57: retired. The Skill file no longer ships in
		// builtin_skills/multica-constitution-agent/, so this lookup
		// resolves to (zero, false) and the daemon logs a one-time
		// warning, then continues without the binding.
		return "", false
	default:
		return "", false
	}
}
