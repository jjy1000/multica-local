package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// userPluginKeyPrefix is the flag_key namespace for user-created plugins.
// Every user_plugin row maps to a dynamic experimental flag whose key is
// "user_<slug>" (see migration 166). The prefix is duplicated as the
// experimental.UserPluginPrefix constant; it is inlined here so the handler
// does not depend on that symbol's value staying in lock-step.
const userPluginKeyPrefix = "user_"

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
type UserPluginResponse struct {
	ID          string                       `json:"id"`
	Slug        string                       `json:"slug"`
	FlagKey     string                       `json:"flag_key"`
	Title       experimental.LocalizedString `json:"title"`
	Description experimental.LocalizedString `json:"description"`
	TriggerMode string                       `json:"trigger_mode"`
	RuntimeKind string                       `json:"runtime_kind"`
	Status      string                       `json:"status"`
	Manifest    json.RawMessage              `json:"manifest,omitempty"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
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
		TriggerMode: p.TriggerMode,
		RuntimeKind: p.RuntimeKind,
		Status:      p.Status,
		Manifest:    json.RawMessage(p.ManifestJson),
		CreatedAt:   p.CreatedAt.Time,
		UpdatedAt:   p.UpdatedAt.Time,
	}
}

// userPluginToFlag builds the dynamic catalog flag for a plugin row. The flag
// key is the row's stored flag_key ("user_<slug>"); user plugins default to
// off so they never activate a code path the user has not opted into. The
// manifest JSONB is intentionally NOT mapped onto Flag.ManifestPath — user
// plugins carry inline manifest data, not a resources-dir path.
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
		RuntimeKind: p.RuntimeKind,
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
		Slug:          body.Slug,
		FlagKey:       flagKey,
		TitleEn:       body.Title.En,
		TitleZh:       body.Title.Zh,
		DescriptionEn: body.Description.En,
		DescriptionZh: body.Description.Zh,
		ManifestJson:  manifest,
		TriggerMode:   triggerMode,
		RuntimeKind:   runtimeKind,
		Status:        "active",
		CreatedBy:     parseUUID(userID),
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
	h.seedPluginVisibility(r.Context(), flagKey, manifest)

	writeJSON(w, http.StatusCreated, userPluginToResponse(plugin))
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
	flagKey := updated.FlagKey
	experimental.UnregisterUserPlugin(flagKey)
	experimental.RegisterUserPlugins([]experimental.Flag{userPluginToFlag(updated)})
	if h.ExperimentRegistry != nil {
		h.ExperimentRegistry.RemoveUserPlugin(flagKey)
		h.ExperimentRegistry.MergeUserPlugins([]experimental.Flag{userPluginToFlag(updated)})
	}

	writeJSON(w, http.StatusOK, userPluginToResponse(updated))
}

// DeleteUserPlugin soft-deletes a plugin (status='deleted'), removes its flag
// from the live catalog + registry, and clears the caller's stored preference
// for the flag so a stale toggle never references a gone plugin. Returns 204.
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

	if err := h.Queries.SoftDeleteUserPlugin(r.Context(), slug); err != nil {
		slog.Error("user plugin delete: soft delete failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete user plugin")
		return
	}

	flagKey := existing.FlagKey

	// Drop the flag from the in-memory catalog + registry so the Labs UI and
	// DefaultFor lookups stop seeing it immediately.
	experimental.UnregisterUserPlugin(flagKey)
	if h.ExperimentRegistry != nil {
		h.ExperimentRegistry.RemoveUserPlugin(flagKey)
	}

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

	w.WriteHeader(http.StatusNoContent)
}

// pluginManifestCapabilities is the subset of a plugin manifest we inspect
// for visibility seeding. The capabilities block lists agent/squad names
// the plugin provisions; each is hidden from regular pickers by default.
type pluginManifestCapabilities struct {
	Capabilities struct {
		Agents []string `json:"agents"`
		Squads []string `json:"squads"`
	} `json:"capabilities"`
}

// seedPluginVisibility inserts experimental_resource_visibility rows
// for any agents/squads declared in the plugin manifest's capabilities
// block. Hidden by default — the user sees lab resources only through
// the lab's own panel, not in the regular agent/squad pickers.
//
// Name resolution uses a cross-workspace lookup (LIMIT 1) because user
// plugins are server-global, not workspace-scoped. Agents/squads that do
// not exist yet at creation time are silently skipped; the plugin's
// install handler (if any) is the authoritative seeding point.
//
// Best-effort: every error is logged and swallowed so a visibility
// failure never blocks plugin creation.
func (h *Handler) seedPluginVisibility(ctx context.Context, flagKey string, manifest json.RawMessage) {
	if h.Queries == nil || h.DB == nil {
		return
	}

	var caps pluginManifestCapabilities
	if err := json.Unmarshal(manifest, &caps); err != nil {
		// Manifest without a capabilities block — nothing to seed.
		return
	}

	if len(caps.Capabilities.Agents) == 0 && len(caps.Capabilities.Squads) == 0 {
		return
	}

	// Resolve agent names → UUIDs via raw SQL (no workspace-scoped sqlc
	// query fits the server-global plugin context).
	for _, name := range caps.Capabilities.Agents {
		var id pgtype.UUID
		err := h.DB.QueryRow(ctx,
			`SELECT id FROM agent WHERE name = $1 AND archived_at IS NULL ORDER BY created_at LIMIT 1`,
			name,
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
		}
	}

	// Resolve squad names → UUIDs.
	for _, name := range caps.Capabilities.Squads {
		var id pgtype.UUID
		err := h.DB.QueryRow(ctx,
			`SELECT id FROM squad WHERE name = $1 ORDER BY created_at LIMIT 1`,
			name,
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
		}
	}
}
