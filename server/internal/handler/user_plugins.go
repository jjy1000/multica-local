package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// userPluginKeyPrefix aliases experimental.UserPluginPrefix so the handler
// and the registry can never drift (0.5.18 BE-P1-2). Every user_plugin row
// maps to a dynamic experimental flag whose key is "user_<slug>" (mig 166).
const userPluginKeyPrefix = experimental.UserPluginPrefix

// userPluginSlugPattern enforces the slug contract: lowercase alphanumeric
// segments separated by single hyphens, no leading/trailing hyphen. Mirrors
// workspaceSlugPattern so the two identifier styles stay consistent.
var userPluginSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	userPluginSlugMinLen = 2
	userPluginSlugMaxLen = 64
)

// UserPluginResponse is the wire shape for a single user-created plugin.
// Title/Description are localized pairs so the client renders the current
// locale without a second i18n round-trip, matching the experimental flag
// response convention. Manifest is the raw JSONB the user supplied; it is
// omitted when empty so the default "{}" rows stay compact.
// 0.5.89: CreatedByIssue/CreatedByTask carry conversational provenance
// (NULL → omitted, UI/API creates) and Provisioning carries the inline
// resource outcomes of the create/update call that produced this response.
type UserPluginResponse struct {
	ID             string                       `json:"id"`
	Slug           string                       `json:"slug"`
	FlagKey        string                       `json:"flag_key"`
	Title          experimental.LocalizedString `json:"title"`
	Description    experimental.LocalizedString `json:"description"`
	TriggerMode    string                       `json:"trigger_mode"`
	RuntimeKind    string                       `json:"runtime_kind"`
	Status         string                       `json:"status"`
	Manifest       json.RawMessage              `json:"manifest,omitempty"`
	CreatedAt      time.Time                    `json:"created_at"`
	UpdatedAt      time.Time                    `json:"updated_at"`
	CreatedByIssue string                       `json:"created_by_issue,omitempty"`
	CreatedByTask  string                       `json:"created_by_task,omitempty"`
	Provisioning   []ProvisionOutcome           `json:"provisioning,omitempty"`
}

// userPluginToResponse maps a sqlc row onto the wire struct. ManifestJson is
// stored as JSONB ([]byte) and surfaced verbatim as json.RawMessage; an empty
// blob is dropped by the omitempty tag.
func userPluginToResponse(p db.UserPlugin) UserPluginResponse {
	return UserPluginResponse{
		ID:      util.UUIDToString(p.ID),
		Slug:    p.Slug,
		FlagKey: p.FlagKey,
		Title: experimental.LocalizedString{
			En: p.TitleEn,
			Zh: p.TitleZh,
		},
		Description: experimental.LocalizedString{
			En: p.DescriptionEn,
			Zh: p.DescriptionZh,
		},
		TriggerMode:    p.TriggerMode,
		RuntimeKind:    p.RuntimeKind,
		Status:         p.Status,
		Manifest:       json.RawMessage(p.ManifestJson),
		CreatedAt:      p.CreatedAt.Time,
		UpdatedAt:      p.UpdatedAt.Time,
		CreatedByIssue: util.UUIDToString(p.CreatedByIssue),
		CreatedByTask:  util.UUIDToString(p.CreatedByTask),
	}
}

// userPluginToFlag builds the dynamic catalog flag for a plugin row. The flag
// key is the row's stored flag_key ("user_<slug>"); user plugins default to
// off so they never activate a code path the user has not opted into. The
// manifest JSONB is intentionally NOT mapped onto Flag.ManifestPath — user
// plugins carry inline manifest data, not a resources-dir path.
// 0.5.88 P4: InteractionModel is stamped from the manifest's interaction-
// model contract so the registered flag space (InteractionModelOf /
// FlagByKey / the GET /api/experimental-flags payload) treats user plugins
// exactly like built-ins ("assignee" locks, absent → "auxiliary" never locks).
func userPluginToFlag(p db.UserPlugin) experimental.Flag {
	return experimental.Flag{
		Key:        p.FlagKey,
		DefaultVal: false,
		Title: experimental.LocalizedString{
			En: p.TitleEn,
			Zh: p.TitleZh,
		},
		Description: experimental.LocalizedString{
			En: p.DescriptionEn,
			Zh: p.DescriptionZh,
		},
		RuntimeKind:      p.RuntimeKind,
		InteractionModel: experimental.UserPluginInteractionModel(p.ManifestJson),
	}
}

// validateUserPluginSlug reports whether slug is a valid plugin identifier:
// 2-64 chars, lowercase alphanumeric segments joined by single hyphens.
func validateUserPluginSlug(slug string) bool {
	if len(slug) < userPluginSlugMinLen || len(slug) > userPluginSlugMaxLen {
		return false
	}
	return userPluginSlugPattern.MatchString(slug)
}

func isValidTriggerMode(s string) bool { return s == "auto" || s == "issue_select" }

func isValidRuntimeKind(s string) bool { return s == "none" || s == "inline" || s == "subprocess" }

func isValidPluginStatus(s string) bool { return s == "active" || s == "disabled" || s == "deleted" }

// normalizeManifest returns a valid JSONB blob for storage. An empty/absent
// manifest defaults to "{}" (the column default); a present-but-invalid blob
// is rejected so the JSONB insert cannot fail server-side.
func normalizeManifest(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), true
	}
	if !json.Valid(raw) {
		return nil, false
	}
	return raw, true
}

// ListUserPlugins returns every non-deleted user plugin (status active or
// disabled). Auth: any authenticated user can read; plugins are server-global
// (not workspace-scoped), mirroring the experimental flag catalog.
func (h *Handler) ListUserPlugins(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	plugins, err := h.Queries.ListUserPlugins(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list user plugins")
		return
	}

	resp := make([]UserPluginResponse, 0, len(plugins))
	for _, p := range plugins {
		resp = append(resp, userPluginToResponse(p))
	}
	writeJSON(w, http.StatusOK, resp)
}

type createUserPluginRequest struct {
	Slug        string                       `json:"slug"`
	Title       experimental.LocalizedString `json:"title"`
	Description experimental.LocalizedString `json:"description"`
	TriggerMode string                       `json:"trigger_mode"`
	RuntimeKind string                       `json:"runtime_kind"`
	Manifest    json.RawMessage              `json:"manifest"`
	// 0.5.89 conversational provenance: the agent-context CLI stamps these
	// from MULTICA_ISSUE_ID / MULTICA_TASK_ID so the Labs settings page can
	// show which task created the plugin. Optional; malformed values are a
	// 400 (never silently dropped — provenance that lies is worse than none).
	CreatedByIssue string `json:"created_by_issue"`
	CreatedByTask  string `json:"created_by_task"`
}

// CreateUserPlugin registers a new user-defined plugin and merges it into the
// live experimental catalog. The slug drives the flag_key ("user_<slug>");
// a duplicate slug is rejected with 409 so the client can surface a clean
// "already exists" state instead of a raw unique-violation 500.
func (h *Handler) CreateUserPlugin(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var body createUserPluginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !validateUserPluginSlug(body.Slug) {
		writeError(w, http.StatusBadRequest, "slug must be 2-64 chars, lowercase alphanumeric and hyphens")
		return
	}

	// Default the optional enums to the column defaults so a minimal body
	// (slug + title) is accepted; reject any explicit-but-invalid value.
	triggerMode := body.TriggerMode
	if triggerMode == "" {
		triggerMode = "issue_select"
	}
	if !isValidTriggerMode(triggerMode) {
		writeError(w, http.StatusBadRequest, "trigger_mode must be 'auto' or 'issue_select'")
		return
	}
	runtimeKind := body.RuntimeKind
	if runtimeKind == "" {
		runtimeKind = "inline"
	}
	if !isValidRuntimeKind(runtimeKind) {
		writeError(w, http.StatusBadRequest, "runtime_kind must be 'none', 'inline', or 'subprocess'")
		return
	}

	manifest, okManifest := normalizeManifest(body.Manifest)
	if !okManifest {
		writeError(w, http.StatusBadRequest, "manifest must be valid JSON")
		return
	}
	// 0.5.88 P4: validate the interaction-model contract before storage.
	// "assignee" REQUIRES a non-empty leader_agent; an invalid model
	// literal is rejected so the registered flag space never carries a
	// contract the issue-layer lock gate cannot interpret.
	if _, err := experimental.ParseUserPluginContract(manifest); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 0.5.89: validate the provisioning + skills-visibility contract before
	// storage so inline agent/skill definitions can never land malformed and
	// an unknown skills_visibility literal never registers.
	if _, err := experimental.UserPluginInlineAgents(manifest); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := experimental.UserPluginInlineSkills(manifest); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := experimental.ValidateUserPluginSkillsVisibility(experimental.UserPluginRawSkillsVisibility(manifest)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	createdByIssue, err := optionalUUIDParam(body.CreatedByIssue)
	if err != nil {
		writeError(w, http.StatusBadRequest, "created_by_issue must be a UUID")
		return
	}
	createdByTask, err := optionalUUIDParam(body.CreatedByTask)
	if err != nil {
		writeError(w, http.StatusBadRequest, "created_by_task must be a UUID")
		return
	}

	flagKey := userPluginKeyPrefix + body.Slug

	// Reject duplicates up front for a deterministic 409. The UNIQUE slug
	// constraint is the final guard (checked again after insert) because a
	// concurrent create could land between this read and the insert.
	if _, err := h.Queries.GetUserPluginBySlug(r.Context(), body.Slug); err == nil {
		writeError(w, http.StatusConflict, "a plugin with this slug already exists")
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check existing plugin")
		return
	}

	plugin, err := h.Queries.CreateUserPlugin(r.Context(), db.CreateUserPluginParams{
		Slug:           body.Slug,
		FlagKey:        flagKey,
		TitleEn:        body.Title.En,
		TitleZh:        body.Title.Zh,
		DescriptionEn:  body.Description.En,
		DescriptionZh:  body.Description.Zh,
		ManifestJson:   manifest,
		TriggerMode:    triggerMode,
		RuntimeKind:    runtimeKind,
		Status:         "active",
		CreatedBy:      parseUUID(userID),
		CreatedByIssue: createdByIssue,
		CreatedByTask:  createdByTask,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "a plugin with this slug already exists")
			return
		}
		slog.Error("user plugin create: failed to insert", "slug", body.Slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create user plugin")
		return
	}

	// Merge the new flag into the live catalog + registry so it is usable
	// immediately without a restart. Best-effort nil-guard on the registry:
	// older test harnesses construct a Handler without one.
	experimental.RegisterUserPlugins([]experimental.Flag{userPluginToFlag(plugin)})
	if h.ExperimentRegistry != nil {
		h.ExperimentRegistry.MergeUserPlugins([]experimental.Flag{userPluginToFlag(plugin)})
	}

	// Seed visibility rows for any agents/squads declared in the manifest's
	// capabilities block so they are hidden from regular pickers by default.
	// F-013: scoped to the installer's workspace — a plugin must never seed
	// visibility rows for resources living in another workspace.
	// 0.5.89: the same scope drives the teardown ledger for DECLARED
	// resources (seedPluginVisibility) and inline PROVISIONING
	// (capabilities.agents_inline / skills_inline) plus the env_dir ledger
	// row; outcomes ride the response so the calling agent can report them
	// in its "[lab plugin]" comment.
	var provisioning []ProvisionOutcome
	wsID, wsErr := resolveLabWorkspace(r.Context(), h, h.resolveWorkspaceID(r), userID)
	if wsErr == nil {
		h.seedPluginVisibility(r.Context(), flagKey, manifest, wsID)
		provisioning = h.provisionPluginCapabilities(r.Context(), flagKey, body.Slug, manifest, wsID,
			parseUUID(userID), taskUUIDPtr(createdByTask), plugin.ID)
	} else {
		slog.Debug("user plugin create: skipping visibility seeding (no installer workspace)",
			"slug", body.Slug, "error", wsErr)
	}

	resp := userPluginToResponse(plugin)
	resp.Provisioning = provisioning
	writeJSON(w, http.StatusCreated, resp)
}

// taskUUIDPtr avoids taking the address of a parameter copy in multiple
// call sites — provisioning wants *pgtype.UUID (nil = no provenance).
func taskUUIDPtr(u pgtype.UUID) *pgtype.UUID {
	if !u.Valid {
		return nil
	}
	return &u
}

type updateUserPluginRequest struct {
	Title       *experimental.LocalizedString `json:"title"`
	Description *experimental.LocalizedString `json:"description"`
	TriggerMode *string                       `json:"trigger_mode"`
	RuntimeKind *string                       `json:"runtime_kind"`
	Status      *string                       `json:"status"`
	Manifest    json.RawMessage               `json:"manifest"`
}

// UpdateUserPlugin applies a partial update to an existing plugin. Only the
// fields present in the body are changed; everything else is carried over
// from the stored row. After the write the flag is re-registered so the
// in-memory catalog reflects the new title/description/runtime immediately.
func (h *Handler) UpdateUserPlugin(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}

	var body updateUserPluginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	existing, err := h.Queries.GetUserPluginBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load plugin")
		return
	}

	// Start from the stored values; overlay each provided field. The sqlc
	// update is a full-row write, so every column must be supplied.
	titleEn := existing.TitleEn
	titleZh := existing.TitleZh
	if body.Title != nil {
		titleEn = body.Title.En
		titleZh = body.Title.Zh
	}
	descEn := existing.DescriptionEn
	descZh := existing.DescriptionZh
	if body.Description != nil {
		descEn = body.Description.En
		descZh = body.Description.Zh
	}
	triggerMode := existing.TriggerMode
	if body.TriggerMode != nil {
		if !isValidTriggerMode(*body.TriggerMode) {
			writeError(w, http.StatusBadRequest, "trigger_mode must be 'auto' or 'issue_select'")
			return
		}
		triggerMode = *body.TriggerMode
	}
	runtimeKind := existing.RuntimeKind
	if body.RuntimeKind != nil {
		if !isValidRuntimeKind(*body.RuntimeKind) {
			writeError(w, http.StatusBadRequest, "runtime_kind must be 'none', 'inline', or 'subprocess'")
			return
		}
		runtimeKind = *body.RuntimeKind
	}
	status := existing.Status
	if body.Status != nil {
		if !isValidPluginStatus(*body.Status) {
			writeError(w, http.StatusBadRequest, "status must be 'active', 'disabled', or 'deleted'")
			return
		}
		status = *body.Status
	}
	manifest := existing.ManifestJson
	if len(body.Manifest) > 0 {
		normalized, okManifest := normalizeManifest(body.Manifest)
		if !okManifest {
			writeError(w, http.StatusBadRequest, "manifest must be valid JSON")
			return
		}
		// 0.5.88 P4: same interaction-model contract validation as the
		// create path, applied only when the caller supplies a new
		// manifest (re-validating the stored blob on unrelated field
		// updates would 400 legacy rows this release never wrote).
		if _, err := experimental.ParseUserPluginContract(normalized); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		manifest = normalized
	}

	updated, err := h.Queries.UpdateUserPlugin(r.Context(), db.UpdateUserPluginParams{
		Slug:          slug,
		TitleEn:       titleEn,
		TitleZh:       titleZh,
		DescriptionEn: descEn,
		DescriptionZh: descZh,
		ManifestJson:  manifest,
		TriggerMode:   triggerMode,
		RuntimeKind:   runtimeKind,
		Status:        status,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		slog.Error("user plugin update: failed to write", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update user plugin")
		return
	}

	// Re-register the flag: drop the stale entry, then merge the fresh one.
	// The slug (and thus flag_key) is immutable, so old and new keys match —
	// the unregister keeps the catalog from holding a torn intermediate state
	// if the merged flag's metadata changed shape.
	//
	// 0.5.105 (audit M2): status must take effect IN-PROCESS, not only at
	// the next boot. The boot path registers only ListActiveUserPlugins
	// rows, but this handler used to re-register unconditionally — a PUT
	// status:'disabled' left the lab live until restart. Mirror the boot
	// semantics: only active rows (re-)register; disabled/deleted rows
	// unregister and stay gone. (The real enable/disable switch is
	// experimental_pref and is untouched here.)
	flagKey := updated.FlagKey
	experimental.UnregisterUserPlugin(flagKey)
	if updated.Status == "active" {
		experimental.RegisterUserPlugins([]experimental.Flag{userPluginToFlag(updated)})
	}
	if h.ExperimentRegistry != nil {
		h.ExperimentRegistry.RemoveUserPlugin(flagKey)
		if updated.Status == "active" {
			h.ExperimentRegistry.MergeUserPlugins([]experimental.Flag{userPluginToFlag(updated)})
		}
	}

	// Re-seed visibility when the manifest changed. The common lab-builder
	// flow creates a plugin with empty capabilities, provisions its
	// agents/autopilots/squads via the CLI, then PUTs the filled manifest
	// here — so without this the lab roster would never be hidden. Seeding
	// is additive and idempotent (INSERT ... ON CONFLICT DO NOTHING); it
	// does not un-hide resources dropped from the manifest.
	// 0.5.89: the manifest change also re-runs inline provisioning —
	// create-or-reuse makes this the self-heal path for a partially-failed
	// create (e.g. an inline leader whose insert failed lands on retry).
	var provisioning []ProvisionOutcome
	if len(body.Manifest) > 0 {
		// F-013: same installer-workspace scoping as create.
		if wsID, err := resolveLabWorkspace(r.Context(), h, h.resolveWorkspaceID(r), requestUserID(r)); err == nil {
			h.seedPluginVisibility(r.Context(), flagKey, manifest, wsID)
			provisioning = h.provisionPluginCapabilities(r.Context(), flagKey, slug, manifest, wsID,
				parseUUID(requestUserID(r)), nil, updated.ID)
		} else {
			slog.Debug("user plugin update: skipping visibility seeding (no installer workspace)",
				"slug", slug, "error", err)
		}
	}

	resp := userPluginToResponse(updated)
	resp.Provisioning = provisioning
	writeJSON(w, http.StatusOK, resp)
}

// DeleteUserPlugin soft-deletes a plugin (status='deleted'), removes its flag
// from the live catalog + registry, clears the caller's stored preference
// for the flag so a stale toggle never references a gone plugin, and —
// 0.5.89 — RECLAIMS every plugin-owned resource recorded in the
// user_plugin_resource teardown ledger (provisioned agents/squads archive,
// provisioned autopilots pause, provisioned skills hard-delete, the
// ~/.multica/plugins/<slug>/ dir moves to .trash/). Declared (pre-existing)
// resources are kept and reported as such. Returns 200 with the per-resource
// reclaim report (was 204 before the report existed).
//
// Guard: a plugin whose flag still has non-terminal bound issues is refused
// with 409 — deleting the manifest out from under running work would strand
// the delegation loop.
func (h *Handler) DeleteUserPlugin(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}

	existing, err := h.Queries.GetUserPluginBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load plugin")
		return
	}

	flagKey := existing.FlagKey

	// 0.5.89 active-reference guard: terminal statuses mirror
	// isTerminalIssueStatus (done/closed/cancelled).
	activeIssues, err := h.Queries.CountActiveIssuesByLabSource(r.Context(), pgtype.Text{String: flagKey, Valid: flagKey != ""})
	if err != nil {
		slog.Error("user plugin delete: active-issue count failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to check bound issues")
		return
	}
	if activeIssues > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":         fmt.Sprintf("plugin still has %d bound issue(s) in a non-terminal state; resolve or rebind them before deleting", activeIssues),
			"active_issues": activeIssues,
		})
		return
	}

	if err := h.Queries.SoftDeleteUserPlugin(r.Context(), slug); err != nil {
		slog.Error("user plugin delete: soft delete failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete plugin")
		return
	}

	// Drop the flag from the in-memory catalog + registry so the Labs UI and
	// DefaultFor lookups stop seeing it immediately.
	experimental.UnregisterUserPlugin(flagKey)
	if h.ExperimentRegistry != nil {
		h.ExperimentRegistry.RemoveUserPlugin(flagKey)
	}

	// Purge the visibility rows seeded under this flag key (labs plan
	// P2-1a). ListLabManagedResourceIDs stamps `lab_managed` from row
	// existence alone, so leftover rows keep the user's own agents/squads
	// greyed out of regular pickers long after the plugin is gone.
	// Best-effort: the tombstone is already committed and recreate purges
	// again, so a failure here must not turn a successful DELETE into 500.
	if _, err := h.Queries.DeletePluginResourceVisibilityByFlagKey(r.Context(), flagKey); err != nil {
		slog.Error("user plugin delete: failed to purge visibility rows",
			"slug", slug, "flag_key", flagKey, "error", err)
	}

	// 0.5.89 teardown: reclaim ledgered resources (best-effort — failures
	// mark their ledger row 'failed' and are retryable via
	// POST /api/user-plugins/{slug}/reclaim). Runs BEFORE the pref clear so
	// a report-carrying 200 only leaves after the full best-effort pass.
	reclaim := h.reclaimPluginResources(r.Context(), slug)

	// Clear the caller's preference row for the retired flag. This is a
	// single-user fork, so the current user's pref is the only one that
	// exists; a failure here is a real error (the row would otherwise dangle
	// against a flag that no longer resolves).
	if err := h.Queries.DeleteExperimentalPref(r.Context(), db.DeleteExperimentalPrefParams{
		UserID:  parseUUID(userID),
		FlagKey: flagKey,
	}); err != nil {
		slog.Error("user plugin delete: failed to clear experimental pref",
			"slug", slug, "flag_key", flagKey, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to clear plugin preference")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slug":         slug,
		"flag_key":     flagKey,
		"deleted":      true,
		"reclaim":      reclaim,
		"reclaim_note": "failed rows are retryable via POST /api/user-plugins/" + slug + "/reclaim",
	})
}

// pluginManifestCapabilities is the subset of a plugin manifest we inspect
// for visibility seeding. The capabilities block lists agent/squad/autopilot
// names the plugin provisions; each is hidden from regular pickers by
// default. Leader names the lab's dispatch agent (used by the issue layer,
// not by visibility seeding) and is parsed via experimental.UserPluginLeader.
type pluginManifestCapabilities struct {
	Capabilities struct {
		Agents     []string `json:"agents"`
		Squads     []string `json:"squads"`
		Autopilots []string `json:"autopilots"`
		Leader     string   `json:"leader"`
	} `json:"capabilities"`
}

// seedPluginVisibility inserts experimental_resource_visibility rows
// for any agents/squads/autopilots declared in the plugin manifest's
// capabilities block. Hidden by default — the user sees lab resources
// only through the lab's own panel, not in the regular pickers, so a
// lab's private automation/agent roster never pollutes Multica's own.
//
// F-013 (0.5.18): lookups are scoped to the installer's workspace
// (workspaceID), so a plugin can never seed visibility rows for resources
// living in another workspace. Agents/squads/autopilots that do not exist
// in that workspace yet are silently skipped; the plugin's install handler
// (if any) is the authoritative seeding point.
//
// Best-effort: every error is logged and swallowed so a visibility
// failure never blocks plugin creation.
func (h *Handler) seedPluginVisibility(ctx context.Context, flagKey string, manifest json.RawMessage, workspaceID pgtype.UUID) {
	if h.Queries == nil || h.DB == nil {
		return
	}

	// Purge-then-seed (labs plan P2-1a): drop every visibility row previously
	// seeded under this flag key so manifest capability removals on update
	// and slug reuse after a soft-delete never leave orphans behind
	// (lab_managed is stamped from row existence, flag-agnostic). Runs before
	// the workspace / caps early-returns so a manifest that DROPPED its
	// capabilities block still cleans up after itself. Best-effort.
	if _, err := h.Queries.DeletePluginResourceVisibilityByFlagKey(ctx, flagKey); err != nil {
		slog.Warn("plugin visibility: purge before seed failed",
			"flag_key", flagKey, "error", err)
	}

	if !workspaceID.Valid {
		// No installer workspace → nothing to seed. Seeding must never run
		// against resources in another workspace (F-013).
		return
	}

	// 0.5.89: declared resources join the teardown ledger as
	// origin='declared' (never reclaimed on delete — the resource belongs
	// to the user; delete only drops visibility). Idempotent via ON CONFLICT.
	ledgerDeclared := func(resType string, id pgtype.UUID) {
		h.ledgerPluginResource(ctx, workspaceID, strings.TrimPrefix(flagKey, userPluginKeyPrefix), resType, &id, "declared", nil)
	}

	var caps pluginManifestCapabilities
	if err := json.Unmarshal(manifest, &caps); err != nil {
		// Manifest without a capabilities block — nothing to seed.
		return
	}

	if len(caps.Capabilities.Agents) == 0 && len(caps.Capabilities.Squads) == 0 && len(caps.Capabilities.Autopilots) == 0 {
		return
	}

	// Resolve agent names → UUIDs via raw SQL (no workspace-scoped sqlc
	// query fits the server-global plugin context).
	for _, name := range caps.Capabilities.Agents {
		var id pgtype.UUID
		err := h.DB.QueryRow(ctx,
			`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2 AND archived_at IS NULL ORDER BY created_at LIMIT 1`,
			workspaceID, name,
		).Scan(&id)
		if err != nil {
			// Agent not provisioned yet — skip silently at debug level.
			slog.Debug("plugin visibility: agent not found, skipping",
				"flag_key", flagKey, "agent", name, "error", err)
			continue
		}
		if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      flagKey,
			ResourceType: string(experimental.HideAgent),
			ResourceID:   id,
		}); err != nil {
			slog.Warn("plugin visibility: failed to seed agent row",
				"flag_key", flagKey, "agent", name, "error", err)
			continue
		}
		ledgerDeclared("agent", id)
	}

	// Resolve squad names → UUIDs.
	for _, name := range caps.Capabilities.Squads {
		var id pgtype.UUID
		err := h.DB.QueryRow(ctx,
			`SELECT id FROM squad WHERE workspace_id = $1 AND name = $2 ORDER BY created_at LIMIT 1`,
			workspaceID, name,
		).Scan(&id)
		if err != nil {
			slog.Debug("plugin visibility: squad not found, skipping",
				"flag_key", flagKey, "squad", name, "error", err)
			continue
		}
		if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      flagKey,
			ResourceType: string(experimental.HideSquad),
			ResourceID:   id,
		}); err != nil {
			slog.Warn("plugin visibility: failed to seed squad row",
				"flag_key", flagKey, "squad", name, "error", err)
			continue
		}
		ledgerDeclared("squad", id)
	}

	// Resolve autopilot titles → UUIDs. Autopilots key off `title`
	// (not `name`), so the manifest lists titles here; each hidden row
	// keeps the lab's automation out of the regular autopilot list.
	for _, title := range caps.Capabilities.Autopilots {
		var id pgtype.UUID
		err := h.DB.QueryRow(ctx,
			`SELECT id FROM autopilot WHERE workspace_id = $1 AND title = $2 ORDER BY created_at LIMIT 1`,
			workspaceID, title,
		).Scan(&id)
		if err != nil {
			slog.Debug("plugin visibility: autopilot not found, skipping",
				"flag_key", flagKey, "autopilot", title, "error", err)
			continue
		}
		if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      flagKey,
			ResourceType: string(experimental.HideAutopilot),
			ResourceID:   id,
		}); err != nil {
			slog.Warn("plugin visibility: failed to seed autopilot row",
				"flag_key", flagKey, "autopilot", title, "error", err)
			continue
		}
		ledgerDeclared("autopilot", id)
	}
}

// GetUserPluginReclaimPlan renders what a delete would reclaim — the
// shared artifact behind the CLI `lab delete --dry-run`, the UI delete
// confirmation, and the agent's conversational confirm-before-delete
// protocol. Read-only.
func (h *Handler) GetUserPluginReclaimPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	existing, err := h.Queries.GetUserPluginBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load plugin")
		return
	}
	writeJSON(w, http.StatusOK, h.buildReclaimPlan(r.Context(), slug, existing.FlagKey))
}

// PostUserPluginReclaim retries the reclaim for an already-deleted plugin:
// ledger rows left 'failed' by the delete pass (a cross-device rename, a
// transient DB error) are re-run. 409 when the plugin is still live —
// deletion is the entry point, not this retry.
func (h *Handler) PostUserPluginReclaim(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	existing, err := h.Queries.GetUserPluginBySlugAnyStatus(r.Context(), slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load plugin")
		return
	}
	if existing.Status != "deleted" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "plugin is still live — reclaim runs after deletion",
			"slug":  slug,
		})
		return
	}
	reclaim := h.reclaimPluginResources(r.Context(), slug)
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":     slug,
		"flag_key": existing.FlagKey,
		"reclaim":  reclaim,
	})
}
