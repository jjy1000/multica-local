package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// installableSources (0.3.19 P2): legacy allowlist kept as a soft
// fallback for callers that pre-date the registry. The runtime
// installable check is Registry.IsInstallable(); this map is only
// consulted when the registry is nil (unit tests that build a bare
// Handler without going through New).
//
// PR 3 lands with only claude_science wired up; subsequent labs
// (pythia_oracle, etc.) are added one PR at a time.
var installableSources = map[string]bool{
	string(experimental.SourceClaudeScience):         true,
	string(experimental.SourceClaudeScienceLab):     true,
	string(experimental.SourceMythosSwarm):           true,
	string(experimental.SourceConstitutionAgent):     true,
	string(experimental.SourceAgentSelfOptimization): true,
}

// isInstallableFlag reports whether key is a registered installable
// experiment. The registry is the source of truth; the legacy
// installableSources map is the fallback for tests that build a bare
// Handler without the registry. New callers should branch on
// Registry.IsInstallable; the fallback exists only so the existing
// test suite keeps passing while the migration lands.
func (h *Handler) isInstallableFlag(key string) bool {
	if h.ExperimentRegistry != nil && h.ExperimentRegistry.IsInstallable(key) {
		return true
	}
	return installableSources[key]
}

// ExperimentalResourcesManifest is the install / status response body.
// PR 3 lands with a minimal shape: counts only, no per-resource
// listing. PR 7's renderer side panel extends Manifest with a
// RecentActivity section so the Labs tab can show "最近 24 小时 0 个
// 任务" without a second round-trip.
//
// Counts are duplicated across (ResourceType, Total, Visible) so the
// renderer can phrase "已装载 292 skills (280 可见)" without a second
// round-trip.
type ExperimentalResourcesManifest struct {
	Source         string                  `json:"source"`
	Installed      bool                    `json:"installed"`
	Hidden         bool                    `json:"hidden"`
	Counts         []ManifestResourceCount `json:"counts"`
	RecentActivity *ManifestActivity       `json:"recent_activity,omitempty"`
}

// ManifestActivity is the PR 7 side-panel data. It surfaces the
// number of tasks / agent runs / comments the lab has produced in the
// last 24 hours so the renderer can phrase "上次活跃 2 小时前" or
// "今日 0 个任务". Kept short — full activity log lives in the
// `activity_log` table and is fetched separately by the dashboard.
type ManifestActivity struct {
	TasksLast24h       int    `json:"tasks_last_24h"`
	AgentRunsLast24h   int    `json:"agent_runs_last_24h"`
	LastActivityAt     string `json:"last_activity_at,omitempty"`
	InstalledWorkspace string `json:"installed_workspace_slug,omitempty"`
}

// ManifestResourceCount is the per-resource_type slice element. Total
// counts every lock row attached to the source, Visible counts every
// lock row whose hidden flag is currently false.
type ManifestResourceCount struct {
	ResourceType string `json:"resource_type"`
	Total        int    `json:"total"`
	Visible      int    `json:"visible"`
}

// GetExperimentalResourcesStatus returns the install / hide state for
// a single source. The renderer polls this on mount to decide between
// "installing…" / "已装载 N" / "已隐藏 N" badge text.
//
// Auth: any signed-in user. The lock overlay is global per source, so
// the response shape does NOT vary by user_id.
func (h *Handler) GetExperimentalResourcesStatus(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !h.isInstallableFlag(key) {
		writeError(w, http.StatusNotFound, "unknown experimental source")
		return
	}
	src := experimental.Source(key)

	manifest, err := h.statusForKey(r, src)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read lock counts")
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

// PostExperimentalResourcesInstall is the idempotent install endpoint.
// 0.3.19 P2: the per-source switch is replaced by a single
// Registry.RunInstall call. Source-specific behaviour lives in the
// install handler bound at boot via RegisterInstallHandler in
// cmd/server/router.go. When the registry is nil (older tests) the
// marker-row fallback path runs.
func (h *Handler) PostExperimentalResourcesInstall(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !h.isInstallableFlag(key) {
		writeError(w, http.StatusNotFound, "unknown experimental source")
		return
	}
	src := experimental.Source(key)

	// Restore any previously-hidden rows so the install expresses the
	// user's "I want this lab on" intent regardless of prior state.
	if _, err := experimental.Restore(r.Context(), h.Queries, src); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore lab rows")
		return
	}

	if h.ExperimentRegistry != nil {
		if err := h.ExperimentRegistry.RunInstall(key, requestUserID(r), h.resolveWorkspaceID(r)); err != nil {
			if errors.Is(err, ErrManifestUnavailable) {
				writeError(w, http.StatusServiceUnavailable,
					string(src)+" manifest not staged — run apps/desktop/scripts/build-claude-science-manifest.mjs then bundle-cli")
				return
			}
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("install failed: %v", err))
			return
		}
	} else {
		// Marker-only fallback for tests that build a bare Handler
		// without going through New() — the registry is nil.
		if err := h.markInstalled(r, src); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record install marker")
			return
		}
	}

	manifest, err := h.statusForKey(r, src)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read lock counts")
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

// PostExperimentalResourcesRollback hides every row attached to the
// source. 0.3.19 P2: also invokes the optional registry rollback
// handler for sources that need to clean domain rows beyond the
// visibility toggle. The default is no-op so existing rollback
// semantics are unchanged.
func (h *Handler) PostExperimentalResourcesRollback(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !h.isInstallableFlag(key) {
		writeError(w, http.StatusNotFound, "unknown experimental source")
		return
	}
	src := experimental.Source(key)

	hidden, err := experimental.Hide(r.Context(), h.Queries, src)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hide lab rows")
		return
	}
	if h.ExperimentRegistry != nil {
		if err := h.ExperimentRegistry.RunRollback(key); err != nil {
			slog.Warn("rollback handler errored", "flag", key, "error", err)
		}
	}
	w.Header().Set("X-Lab-Rows-Hidden", strconv.Itoa(hidden))
	w.WriteHeader(http.StatusNoContent)
}

// statusForKey is the read-side helper shared by status / install /
// rollback handlers. It walks the lock overlay once and projects the
// manifest shape the renderer expects. Keeping the helper inline
// (not exported) lets the three handlers share the same translation
// without forcing a shared package just for one struct's JSON view.
//
// PR 7 added RecentActivity — for claude_science we look up the
// installed workspace's id and surface a 24h activity summary. Other
// sources return the manifest with RecentActivity=nil (omitted in
// JSON), so a non-claude_science status does not pay the extra
// query cost.
func (h *Handler) statusForKey(r *http.Request, src experimental.Source) (ExperimentalResourcesManifest, error) {
	counts, err := experimental.CountByType(r.Context(), h.Queries, src)
	if err != nil {
		return ExperimentalResourcesManifest{}, err
	}
	m := ExperimentalResourcesManifest{
		Source:    string(src),
		Installed: len(counts) > 0,
		Hidden:    isManifestHidden(counts),
		Counts:    countsToResponse(counts),
	}
	if src == experimental.SourceClaudeScience && len(counts) > 0 {
		activity, aerr := h.claudeScienceActivity(r)
		if aerr != nil {
			// Activity is decorative, not authoritative — a DB blip
			// here should not flip the whole status response to 500.
			// Log and continue with RecentActivity=nil.
			m.RecentActivity = nil
		} else {
			m.RecentActivity = activity
		}
	}
	return m, nil
}

// claudeScienceActivity returns the side-panel activity summary.
//
// 0.3.25 reserved-workspace removal: the lab no longer owns a
// dedicated "claude-science" workspace — resources live in the
// caller's active workspace. The summary now reports that active
// workspace (resolved from the request) rather than looking up a
// reserved slug that no longer exists. The struct is decorative
// (InstalledWorkspace only); richer 24h counters are deferred until a
// query surface exists, and statusForKey already tolerates a nil
// return without failing the status response.
func (h *Handler) claudeScienceActivity(r *http.Request) (*ManifestActivity, error) {
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		return nil, nil
	}
	id := parseUUID(wsID)
	if !id.Valid {
		return nil, nil
	}
	ws, err := h.Queries.GetWorkspace(r.Context(), id)
	if err != nil {
		return nil, nil
	}
	return &ManifestActivity{InstalledWorkspace: ws.Slug}, nil
}

// markInstalled inserts a marker lock row for src so the renderer can
// flip Installed=true on a fresh toggle (the renderer reads the
// "any row exists for this source" signal from the lock count).
//
// 0.3.19 P6: the marker UUID is now derived from the flag key via
// experimental.LifecycleMarker so each lab has its own stable
// marker. The 0.3.15 implementation used the all-zero UUID
// (pgtypeUUIDZero), which collided with the "real" workspace row
// the install dispatcher inserted afterwards — a UNIQUE
// (source, type, id) violation would mark the install as failed
// even though it succeeded. The new derivation is unique per flag
// key and never equals the all-zero UUID.
func (h *Handler) markInstalled(r *http.Request, src experimental.Source) error {
	return experimental.Claim(r.Context(), h.Queries, src,
		experimental.LockWorkspace, experimental.LifecycleMarker(string(src)))
}

// isManifestHidden reports true when every row attached to the source
// is currently hidden. Used by GET status to translate counts into the
// "已隐藏 N" badge text on the renderer.
func isManifestHidden(counts []experimental.LockCounts) bool {
	if len(counts) == 0 {
		return false
	}
	for _, c := range counts {
		if c.Visible > 0 {
			return false
		}
	}
	return true
}

// countsToResponse flattens the helper's []LockCounts into the JSON
// shape the renderer expects. Keeping the helper's type unexported
// avoids leaking the helper's internal naming into the wire contract.
func countsToResponse(counts []experimental.LockCounts) []ManifestResourceCount {
	out := make([]ManifestResourceCount, 0, len(counts))
	for _, c := range counts {
		out = append(out, ManifestResourceCount{
			ResourceType: string(c.Type),
			Total:        c.Total,
			Visible:      c.Visible,
		})
	}
	return out
}

// Keep imports the file will need when PR 4 adds the flag-toggle wire
// (errors.Is for ErrUnknownSource translation). Cheap; no runtime
// impact.
var _ = errors.Is
var _ = context.Background
