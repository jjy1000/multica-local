package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ExperimentalFlagResponse is one entry in the GET /api/experimental-flags
// response. It mirrors the server-side catalog so the frontend can render
// the labs UI without a second i18n lookup. The fields here are what the
// frontend `ExperimentalFlag` type consumes; see packages/core/types/experimental.ts.
//
// 0.3.15: the response grows an optional `installation` field that is
// populated for labs whose flag key has a corresponding
// /api/experimental-resources/{key}/status entry (currently only
// claude_science). When present, it carries the install manifest so
// the Labs UI can render "已装载 292 skills" without a second round
// trip. The field is omitted when the lab has no installable backing
// — keeping the wire shape stable for the existing single-flag
// (chat_pin_ui) and the still-readonly pythia_oracle.
type ExperimentalFlagResponse struct {
	Key            string                         `json:"key"`
	Enabled        bool                           `json:"enabled"`
	DefaultEnabled bool                           `json:"default_enabled"`
	Title          experimental.LocalizedString   `json:"title"`
	Description    experimental.LocalizedString   `json:"description"`
	Installation   *ExperimentalResourcesManifest `json:"installation,omitempty"`
	// 0.3.20: catalog RuntimeKind — mirrors experimental.Flag.RuntimeKind.
	// Surfaced so the renderer's manager-factory loader (apps/desktop)
	// can build its descriptor list without an extra round-trip and
	// without reading the desktop-side hard-coded flags.
	RuntimeKind string `json:"runtime_kind,omitempty"`
	// 0.3.20: sidebar entries declared in the manifest's
	// entry_points.sidebar[*]. Empty array means no sidebar row;
	// absent means the flag has no manifest (chat_pin_ui).
	SidebarEntries []experimental.SidebarEntry `json:"sidebar_entries,omitempty"`
	// 0.3.45.8: mirror of Flag.HideFromIssueLabPicker. The issue-detail
	// LabPicker filters by this field so infrastructure / self-driven
	// labs (llm_wiki_bridge, agent_self_optimization) are not offered
	// as per-issue "实验插件" choices. Absent means the picker offers
	// the flag as usual.
	HideFromIssueLabPicker bool `json:"hide_from_issue_lab_picker,omitempty"`
	// 0.5.3: mirror of Flag.AlwaysShowInLabPicker. When true, the
	// LabPicker shows the entry even if the flag is not enabled —
	// action-type labs (agent_creation_studio) must be reachable
	// without a prior Labs opt-in.
	AlwaysShowInLabPicker bool `json:"always_show_in_lab_picker,omitempty"`
	// 0.3.49.1: mirror of Flag.HidesDeliverableInIssueTimeline. When
	// true, `issue-detail.tsx` hides the lab's agent deliverable
	// comments from the plain issue timeline (the results belong in
	// the lab's workspace-scoped view — Claude Lab Artifact tab, Mythos
	// Swarm reflection panel — not in the timeline). Absent means
	// deliverable comments stay visible. Pre-0.3.49.1 this was a
	// renderer-side hardcoded `VIEW_LAB_SOURCES` set; the migration
	// moved the source of truth into the catalog.
	HidesDeliverableInIssueTimeline bool `json:"hides_deliverable_in_issue_timeline,omitempty"`
	// IsUserPlugin marks flags created by the user through the plugin
	// sandbox (0.3.60+). Built-in flags omit this field (false).
	IsUserPlugin bool `json:"is_user_plugin,omitempty"`
	// 0.3.65: the workspace agent name this lab auto-assigns as the issue
	// owner when the user picks it (the "实验室测试智能体" the property panel
	// shows under a locked assignee). Mirrors the leader-rewrite table
	// (defaultLabLeaderForKey / UserPluginLeader) the create+update paths use
	// server-side, surfaced so the renderer can display the default assignee
	// without a second round trip. Empty means the lab owns no single agent
	// (mythos_swarm runs via its squad roster; llm_wiki_bridge / chat_pin_ui
	// have no per-issue agent) — the renderer then falls back to a generic
	// "lab owns the roster" hint.
	LeaderAgent string `json:"leader_agent,omitempty"`
	// 0.5.22: mirror of Flag.AutoDispatch. True (default) means the
	// service-layer auto-dispatch path (CreateIssue + UpdateIssue + Batch)
	// still fires the lab leader agent right after the leader-rewrite.
	// False (claude_science_lab) means the gate short-circuits — the
	// assignee is still written, but no agent_task_queue row is created
	// until the user explicitly clicks "Run research" on the lab
	// workbench's IssueContextBar. The renderer uses this to know whether
	// to render the Run button.
	AutoDispatch bool `json:"auto_dispatch"`
	// 0.5.86: mirror of Flag.InteractionModel — "assignee" (独立工作型:
	// the lab's leader agent owns the bound issue's assignee slot; the
	// renderer locks the AssigneePicker for these labs) or "auxiliary"
	// (辅助协作型: trace/visualize-only, never an assignee). Empty means
	// legacy/unclassified — no lock, no auxiliary semantics.
	InteractionModel string `json:"interaction_model,omitempty"`
	// 0.5.88: mirror of Flag.Frozen — the lab is frozen for new
	// delegation/bindings (entry + routes stay; nothing new may be
	// started against it). Advisory metadata only: toggle behavior is
	// unchanged. First consumer is the swarm_topology freeze.
	Frozen bool `json:"frozen,omitempty"`
	// 0.5.88: mirror of Flag.SuccessorKey — when Frozen is true, the
	// catalog key to use instead (swarm_topology → mythos_swarm).
	// Empty when the frozen lab has no successor.
	SuccessorKey string `json:"successor_key,omitempty"`
}

// ExperimentalFlagsListResponse wraps the list so future metadata
// (e.g. a `version` field for cache busting) can be added without
// breaking the consumer shape.
type ExperimentalFlagsListResponse struct {
	Flags []ExperimentalFlagResponse `json:"flags"`
}

// ListExperimentalFlags returns the merged view of every catalog flag and
// the caller's stored preference for it. Flags the user has not toggled
// appear with Enabled=DefaultEnabled — the frontend should treat Enabled
// as the source of truth and only show DefaultEnabled as a helper hint
// ("Off by default — enable to try").
//
// Auth: any authenticated user can read. There is no workspace scoping
// because experimental prefs are per-user (not per-workspace) and the
// catalog is server-global. If a future flag ever becomes workspace
// scoped, this is the handler that adds the workspace check.
func (h *Handler) ListExperimentalFlags(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	prefs, err := h.Queries.ListExperimentalPrefsByUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list experimental flags")
		return
	}

	// Build a quick lookup so the per-flag loop stays O(N) instead of
	// O(N*M). Without this, every catalog entry would scan the prefs
	// slice linearly — fine for a 5-entry catalog, wasteful as it grows.
	prefByKey := make(map[string]bool, len(prefs))
	for _, p := range prefs {
		prefByKey[p.FlagKey] = p.Enabled
	}

	// User-created plugins (0.3.60 Labs sandbox) are merged in after the
	// built-in catalog. Bind once so the capacity buffer and the append
	// loop agree on the same snapshot.
	userFlags := experimental.UserPluginFlags()
	resp := ExperimentalFlagsListResponse{
		Flags: make([]ExperimentalFlagResponse, 0, len(experimental.Catalog)+len(userFlags)),
	}

	// 0.5.x: pre-compute the sidebar entry map once per request. The
	// built-in loop below and the user-plugin loop below both consult
	// h.ExperimentRegistry.SidebarEntries; without the map, each call
	// takes r.mu.Lock() (SidebarEntries is a write-lock accessor that
	// may mutate r.flags). Pre-computing collapses N+M registry
	// acquisitions into one set of (max) N+M map writes followed by
	// N+M map reads in the loop bodies. Keys not present in the map
	// are an empty slice — equivalent to the pre-0.5.x behaviour
	// where a flag with no manifest or an unknown flag skipped the
	// SidebarEntries assignment via `len(entries) > 0`.
	sidebarEntriesByKey := make(map[string][]experimental.SidebarEntry)
	if reg := h.ExperimentRegistry; reg != nil {
		for _, f := range experimental.Catalog {
			sidebarEntriesByKey[f.Key] = reg.SidebarEntries(f.Key)
		}
		for _, f := range userFlags {
			if _, seen := sidebarEntriesByKey[f.Key]; seen {
				continue
			}
			sidebarEntriesByKey[f.Key] = reg.SidebarEntries(f.Key)
		}
	}

	for _, f := range experimental.Catalog {
		enabled, hasOverride := prefByKey[f.Key]
		flag := ExperimentalFlagResponse{
			Key:                             f.Key,
			Enabled:                         pickEnabled(f.DefaultVal, enabled, hasOverride),
			DefaultEnabled:                  f.DefaultVal,
			Title:                           f.Title,
			Description:                     f.Description,
			RuntimeKind:                     f.RuntimeKind,
			HideFromIssueLabPicker:          f.HideFromIssueLabPicker,
			AlwaysShowInLabPicker:           f.AlwaysShowInLabPicker,
			HidesDeliverableInIssueTimeline: f.HidesDeliverableInIssueTimeline,
			AutoDispatch:                    f.AutoDispatch == nil || *f.AutoDispatch,
			InteractionModel:                f.InteractionModel,
			Frozen:                          f.Frozen,
			SuccessorKey:                    f.SuccessorKey,
		}
		// 0.3.65: expose the lab's default owner agent so the property panel
		// can show "实验室测试智能体: <name>" under the locked assignee. Same
		// table the create+update leader-rewrite paths consult.
		if leader, ok := defaultLabLeaderForKey(f.Key); ok {
			flag.LeaderAgent = leader
		}
		// 0.3.20: surface manifest entry_points.sidebar so the renderer's
		// nav hook can render the Experimental sidebar group from the
		// catalog payload instead of a hard-coded STATIC_NAV list. Flags
		// without a manifest (e.g. chat_pin_ui) omit the field; flags
		// with an empty sidebar omit it too. Reading the manifest on
		// every request is cheap — JSON parse of a ~1KB file under the
		// resources dir. 0.5.x: read from sidebarEntriesByKey rather
		// than calling the registry inline; see pre-compute block above.
		if entries := sidebarEntriesByKey[f.Key]; len(entries) > 0 {
			flag.SidebarEntries = entries
		}
		// Surface installation manifest for keys wired through PR 3's
		// resource endpoints. The renderer's 4-state UI ("未装载" /
		// "安装中" / "已装载 N" / "已隐藏") reads this field instead
		// of issuing a second /api/experimental-resources/{key}/status
		// request when the user is on the Labs page.
		if h.isInstallableFlag(f.Key) {
			manifest, err := readInstallStatus(r.Context(), h.Queries, experimental.Source(f.Key))
			if err == nil {
				flag.Installation = &manifest
			}
		}
		resp.Flags = append(resp.Flags, flag)
	}

	// Append user-created plugins (0.3.60 Labs sandbox). These are
	// stored in the user_plugin table and merged into the catalog's
	// dynamic layer at boot. The frontend renders them identically to
	// built-in flags; the "user_" prefix on the key is the only
	// distinguishing signal.
	for _, f := range userFlags {
		enabled, hasOverride := prefByKey[f.Key]
		flag := ExperimentalFlagResponse{
			Key:                             f.Key,
			Enabled:                         pickEnabled(f.DefaultVal, enabled, hasOverride),
			DefaultEnabled:                  f.DefaultVal,
			Title:                           f.Title,
			Description:                     f.Description,
			RuntimeKind:                     f.RuntimeKind,
			HideFromIssueLabPicker:          f.HideFromIssueLabPicker,
			AlwaysShowInLabPicker:           f.AlwaysShowInLabPicker,
			HidesDeliverableInIssueTimeline: f.HidesDeliverableInIssueTimeline,
			IsUserPlugin:                    true,
			AutoDispatch:                    f.AutoDispatch == nil || *f.AutoDispatch,
			InteractionModel:                f.InteractionModel,
			Frozen:                          f.Frozen,
			SuccessorKey:                    f.SuccessorKey,
		}
		// 0.3.65: user plugins auto-dispatch via their manifest's
		// capabilities.leader (UserPluginLeader) — surface it the same way
		// built-in labs surface defaultLabLeaderForKey.
		if leader, ok := h.resolveLabLeader(r.Context(), f.Key); ok {
			flag.LeaderAgent = leader
		}
		// 0.5.x: read sidebar entries from the pre-computed map; see
		// the pre-compute block at the top of the handler.
		if entries := sidebarEntriesByKey[f.Key]; len(entries) > 0 {
			flag.SidebarEntries = entries
		}
		resp.Flags = append(resp.Flags, flag)
	}

	writeJSON(w, http.StatusOK, resp)
}

// readInstallStatus is a small adapter around the lock helpers; it
// mirrors Handler.statusForKey but in package-internal form so the
// experimental_flags handler does not have to know about the
// resources handler's internal struct names.
func readInstallStatus(
	ctx context.Context,
	q *db.Queries,
	src experimental.Source,
) (ExperimentalResourcesManifest, error) {
	counts, err := experimental.CountByType(ctx, q, src)
	if err != nil {
		return ExperimentalResourcesManifest{}, err
	}
	return ExperimentalResourcesManifest{
		Source:    string(src),
		Installed: len(counts) > 0,
		// Mirror statusForKey (experimental_resources.go): a rolled-back
		// lab keeps its lock rows but flips them to hidden, so Hidden
		// must be computed from the same counts, not hardcoded false —
		// otherwise the Labs side panel can never show the "已隐藏"
		// state after a rollback.
		Hidden: isManifestHidden(counts),
		Counts: countsToResponse(counts),
	}, nil
}

// pickEnabled merges a stored user override with the catalog default.
// hasOverride=false means the user has never toggled this flag, so the
// catalog default applies verbatim.
func pickEnabled(defaultVal, override bool, hasOverride bool) bool {
	if !hasOverride {
		return defaultVal
	}
	return override
}

// UpdateExperimentalFlag upserts the caller's preference for the given
// flag. Unknown flag keys are rejected with 400 — the labs UI must not
// be able to land rows that no flag definition knows about, otherwise the
// row becomes invisible to the user and they cannot un-set it from the UI.
//
// The body shape is intentionally tiny: the toggle UI only needs to
// express "on" or "off", so we accept a single boolean instead of the
// full Decision shape. Future fields (variant, expires_at) belong on a
// different endpoint.
func (h *Handler) UpdateExperimentalFlag(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	flagKey := chi.URLParam(r, "key")
	if flagKey == "" {
		writeError(w, http.StatusBadRequest, "flag key is required")
		return
	}
	if !experimental.IsKnownKey(flagKey) {
		writeError(w, http.StatusBadRequest, "unknown flag key")
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if _, err := h.Queries.UpsertExperimentalPref(r.Context(), upsertParams(userID, flagKey, body.Enabled)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update experimental flag")
		return
	}

	// 0.3.15: wire the flag toggle into the lock-table install/rollback
	// endpoints. Off → rollback hides every lab-attached row; on →
	// install restores visibility (and inserts the marker row that the
	// renderer's Installed flag reads from). Labs whose key is NOT
	// installable skip this branch entirely.
	//
	// 0.3.45.2 bug fix (P0#1): previously the toggle path only called
	// experimental.Restore() which flips existing hidden rows back to
	// visible — it does NOT actually create the lab's agents/squads/
	// skills. For the 4 installable labs that ship in 0.3.22+ this
	// meant the user could toggle the flag on, the visibility rows
	// were restored (count goes from 0 to N because the rollback
	// leftovers un-hide), but the underlying resource rows were never
	// created. The RunInstall call below closes the gap: it delegates
	// to the per-source install handler registered at boot
	// (router.go:533-549) which is the one that actually inserts the
	// lab's agents / squads / skills. Idempotent — safe to call on
	// every toggle.
	if h.isInstallableFlag(flagKey) {
		src := experimental.Source(flagKey)
		if body.Enabled {
			if _, err := experimental.Restore(r.Context(), h.Queries, src); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to install lab resources")
				return
			}
			var installErr error
			if h.ExperimentRegistry != nil {
				workspaceID := h.resolveWorkspaceID(r)
				if err := h.ExperimentRegistry.RunInstall(flagKey, userID, workspaceID); err != nil {
					// Install failure is not fatal for the pref write —
					// the flag stays on and the user can retry by toggling
					// off+on. Log for the operator, and surface the error
					// in the response body (200 + install_error) so the
					// Labs tab can toast a warning instead of showing a
					// silently-empty lab.
					slog.Warn("experimental flag install: RunInstall failed",
						"flag", flagKey, "err", err)
					installErr = err
				}
			}
			if err := h.markInstalled(r, src); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to record install marker")
				return
			}
			if installErr != nil {
				writeJSON(w, http.StatusOK, map[string]string{
					"install_error": installErr.Error(),
				})
				return
			}
		} else {
			if _, err := experimental.Hide(r.Context(), h.Queries, src); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to rollback lab resources")
				return
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// upsertParams is a tiny adapter that lives in its own function so the
// conversion from string userID + bool to the sqlc Params struct is
// documented once. parseUUID is the package helper used by every other
// handler that needs a UUID from a request header.
func upsertParams(userID, flagKey string, enabled bool) db.UpsertExperimentalPrefParams {
	return db.UpsertExperimentalPrefParams{
		UserID:  parseUUID(userID),
		FlagKey: flagKey,
		Enabled: enabled,
	}
}
