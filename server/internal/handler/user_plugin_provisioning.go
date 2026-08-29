package handler

// user_plugin_provisioning.go — 0.5.89 WS2: inline resource provisioning +
// teardown reclaim for user plugins.
//
// Two halves, one ledger:
//
//  1. PROVISION (create/update path): the manifest's
//     capabilities.agents_inline / skills_inline blocks ask the server to
//     create hidden agents/skills on the plugin's behalf (create-or-reuse).
//     Every provisioned resource gets an experimental_resource_visibility
//     row (lab_managed — Active Contract #4 keeps it out of every picker)
//     and a user_plugin_resource ledger row with origin='provisioned'.
//     Resources the manifest merely NAMES (capabilities.agents/squads/
//     autopilots/skills — resolved inside seedPluginVisibility) ledger as
//     origin='declared' and are NEVER reclaimed.
//
//  2. RECLAIM (delete path): DeleteUserPlugin walks the ledger —
//     provisioned agents/squads archive, provisioned autopilots pause,
//     provisioned skills hard-delete (CreateSkill rows have no soft-delete;
//     the origin guard means user-authored skills are never touched), the
//     ~/.multica/plugins/<slug>/ dir moves to .trash/ (30-day physical GC
//     is a follow-up; the move is the reversible step). Declared rows are
//     marked 'skipped'. Failures mark the row 'failed' and are retryable
//     via POST /api/user-plugins/{slug}/reclaim.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ProvisionOutcome / ReclaimOutcome are the wire shapes surfaced through
// the create/update response (`provisioning`), the reclaim-plan endpoint,
// and the DELETE response (`reclaim`). Action verbs are stable strings so
// the CLI and the UI can render them without a second contract.
type ProvisionOutcome struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	ID     string `json:"id,omitempty"`
	Action string `json:"action"` // created | reused | failed
	Detail string `json:"detail,omitempty"`
}

type ReclaimOutcome struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	ID     string `json:"id,omitempty"`
	Origin string `json:"origin"`
	Action string `json:"action"` // reclaimed | kept | failed
	Detail string `json:"detail,omitempty"`
}

// userPluginAgentLookup mirrors seedPluginVisibility's raw-SQL resolution:
// find the live (non-archived) agent by (workspace, name).
const userPluginAgentLookupSQL = `SELECT id FROM agent WHERE workspace_id = $1 AND name = $2 AND archived_at IS NULL ORDER BY created_at LIMIT 1`

// provisionPluginCapabilities creates the manifest's inline agents/skills
// (create-or-reuse) and records every outcome in the teardown ledger. It
// is idempotent: re-running it on manifest update reuses existing rows and
// self-heals a partially-failed first pass.
//
// Best-effort per resource: one failed agent/skill never aborts the rest —
// the outcome list is the caller's signal (CLI prints it; a failed leader
// surfaces as "no dispatchable leader" on the next delegation attempt and
// heals on the next manifest PUT).
func (h *Handler) provisionPluginCapabilities(
	ctx context.Context,
	flagKey, slug string,
	manifest json.RawMessage,
	workspaceID pgtype.UUID,
	ownerID pgtype.UUID,
	createdByTask *pgtype.UUID,
	pluginID pgtype.UUID,
) []ProvisionOutcome {
	if h.Queries == nil || h.DB == nil || !workspaceID.Valid {
		return nil
	}
	var out []ProvisionOutcome

	// --- agents_inline ---
	agents, err := experimental.UserPluginInlineAgents(manifest)
	if err != nil {
		// Validation errors are rejected before storage by the CRUD
		// handlers; a row written before that gate existed just skips.
		slog.Warn("plugin provisioning: agents_inline rejected", "slug", slug, "error", err)
		return out
	}
	for _, spec := range agents {
		var id pgtype.UUID
		err := h.DB.QueryRow(ctx, userPluginAgentLookupSQL, workspaceID, spec.Name).Scan(&id)
		switch {
		case err == nil:
			// Pre-existing agent — reuse but DO NOT claim ownership: the
			// ledger records origin='declared' so delete keeps it alive.
			h.seedPluginVisibilityRows(ctx, flagKey, workspaceID, spec.Name)
			h.ledgerPluginResource(ctx, workspaceID, slug, "agent", &id, "declared", createdByTask)
			out = append(out, ProvisionOutcome{Type: "agent", Name: spec.Name, ID: util.UUIDToString(id), Action: "reused"})
		case errors.Is(err, pgx.ErrNoRows):
			// agent.runtime_id is NOT NULL (mig 004): a provisioned agent
			// binds to the workspace's most-recently-seen runtime — the same
			// daemon-registered runtime the other lab installers target — so
			// the roster is dispatchable immediately. No runtime in the
			// workspace yet → the outcome records "failed" and the manifest
			// re-PUT self-heals once a daemon registers.
			var runtimeID pgtype.UUID
			if err := h.DB.QueryRow(ctx,
				`SELECT id FROM agent_runtime WHERE workspace_id = $1 ORDER BY last_seen_at DESC NULLS LAST LIMIT 1`,
				workspaceID,
			).Scan(&runtimeID); err != nil {
				slog.Warn("plugin provisioning: no runtime to bind agent", "slug", slug, "agent", spec.Name, "error", err)
				out = append(out, ProvisionOutcome{Type: "agent", Name: spec.Name, Action: "failed",
					Detail: "no daemon runtime registered in this workspace yet — re-save the plugin after the daemon is online"})
				continue
			}
			created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
				WorkspaceID:        workspaceID,
				Name:               spec.Name,
				Description:        spec.Description,
				Instructions:       spec.Instructions,
				RuntimeMode:        "local",
				RuntimeConfig:      []byte("{}"),
				RuntimeID:          runtimeID,
				CustomEnv:          []byte("{}"),
				CustomArgs:         []byte("[]"),
				McpConfig:          []byte("{}"),
				Visibility:         "workspace",
				MaxConcurrentTasks: 1,
				OwnerID:            ownerID,
				PermissionMode:     "private",
			})
			if err != nil {
				slog.Warn("plugin provisioning: agent create failed", "slug", slug, "agent", spec.Name, "error", err)
				out = append(out, ProvisionOutcome{Type: "agent", Name: spec.Name, Action: "failed", Detail: err.Error()})
				continue
			}
			h.seedPluginVisibilityRows(ctx, flagKey, workspaceID, spec.Name)
			id := created.ID
			h.ledgerPluginResource(ctx, workspaceID, slug, "agent", &id, "provisioned", createdByTask)
			out = append(out, ProvisionOutcome{Type: "agent", Name: spec.Name, ID: util.UUIDToString(id), Action: "created"})
		default:
			slog.Warn("plugin provisioning: agent lookup failed", "slug", slug, "agent", spec.Name, "error", err)
			out = append(out, ProvisionOutcome{Type: "agent", Name: spec.Name, Action: "failed", Detail: err.Error()})
		}
	}

	// --- skills_inline ---
	skills, err := experimental.UserPluginInlineSkills(manifest)
	if err != nil {
		slog.Warn("plugin provisioning: skills_inline rejected", "slug", slug, "error", err)
		return out
	}
	for _, spec := range skills {
		existing, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        spec.Name,
		})
		if err == nil {
			h.seedSkillVisibilityRow(ctx, flagKey, existing.ID)
			id := existing.ID
			h.ledgerPluginResource(ctx, workspaceID, slug, "skill", &id, "declared", createdByTask)
			out = append(out, ProvisionOutcome{Type: "skill", Name: spec.Name, ID: util.UUIDToString(id), Action: "reused"})
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("plugin provisioning: skill lookup failed", "slug", slug, "skill", spec.Name, "error", err)
			out = append(out, ProvisionOutcome{Type: "skill", Name: spec.Name, Action: "failed", Detail: err.Error()})
			continue
		}
		created, err := h.Queries.CreateSkill(ctx, db.CreateSkillParams{
			WorkspaceID: workspaceID,
			Name:        spec.Name,
			Description: spec.Description,
			Content:     spec.Content,
			Config:      []byte("{}"),
			CreatedBy:   ownerID,
		})
		if err != nil {
			slog.Warn("plugin provisioning: skill create failed", "slug", slug, "skill", spec.Name, "error", err)
			out = append(out, ProvisionOutcome{Type: "skill", Name: spec.Name, Action: "failed", Detail: err.Error()})
			continue
		}
		h.seedSkillVisibilityRow(ctx, flagKey, created.ID)
		id := created.ID
		h.ledgerPluginResource(ctx, workspaceID, slug, "skill", &id, "provisioned", createdByTask)
		out = append(out, ProvisionOutcome{Type: "skill", Name: spec.Name, ID: util.UUIDToString(id), Action: "created"})
	}

	// --- env_dir ledger row ---
	// The persistent plugin env dir (~/.multica/plugins/<slug>/) exists from
	// the first run; ledger it at create time with the plugin row's UUID as
	// the sentinel resource_id (NULLs never conflict in PG, so a real
	// sentinel keeps the UNIQUE constraint meaningful).
	if pluginID.Valid {
		h.ledgerPluginResource(ctx, workspaceID, slug, "env_dir", &pluginID, "provisioned", createdByTask)
	}

	return out
}

// seedPluginVisibilityRows seeds one agent visibility row (best-effort) —
// the per-resource slice of seedPluginVisibility used by the provisioning
// pass, which works from names it already resolved.
func (h *Handler) seedPluginVisibilityRows(ctx context.Context, flagKey string, workspaceID pgtype.UUID, agentName string) {
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx, userPluginAgentLookupSQL, workspaceID, agentName).Scan(&id)
	if err != nil {
		return
	}
	if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      flagKey,
		ResourceType: string(experimental.HideAgent),
		ResourceID:   id,
	}); err != nil {
		slog.Warn("plugin provisioning: agent visibility seed failed", "flag_key", flagKey, "agent", agentName, "error", err)
	}
}

// seedSkillVisibilityRow hides an inline-provisioned skill from the regular
// skill list (skill.go::ListSkills already applies filterLabsHiddenByDefault).
func (h *Handler) seedSkillVisibilityRow(ctx context.Context, flagKey string, skillID pgtype.UUID) {
	if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      flagKey,
		ResourceType: string(experimental.HideSkill),
		ResourceID:   skillID,
	}); err != nil {
		slog.Warn("plugin provisioning: skill visibility seed failed", "flag_key", flagKey, "error", err)
	}
}

// ledgerPluginResource records one teardown-ledger row (idempotent via ON
// CONFLICT DO NOTHING). resourceID may be nil only for env_dir-less future
// types; the current env_dir row always carries the plugin-UUID sentinel.
func (h *Handler) ledgerPluginResource(ctx context.Context, workspaceID pgtype.UUID, slug, resType string, resourceID *pgtype.UUID, origin string, createdByTask *pgtype.UUID) {
	var resID pgtype.UUID
	if resourceID != nil {
		resID = *resourceID
	}
	var taskID pgtype.UUID
	if createdByTask != nil {
		taskID = *createdByTask
	}
	if err := h.Queries.InsertUserPluginResource(ctx, db.InsertUserPluginResourceParams{
		WorkspaceID:   workspaceID,
		PluginSlug:    slug,
		ResourceType:  resType,
		ResourceID:    resID,
		Origin:        origin,
		CreatedByTask: taskID,
	}); err != nil {
		// The ledger must never block provisioning — but a gap here means
		// delete cannot reclaim, so log at WARN for ops correlation.
		slog.Warn("plugin ledger insert failed", "slug", slug, "type", resType, "origin", origin, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Reclaim
// ---------------------------------------------------------------------------

// pluginTrashDir is where removed plugin env dirs wait out the 30-day
// physical-GC grace period (mirrors the runtime_gc retention-ladder
// philosophy: the move is the reversible step, the unlink comes later).
func pluginTrashDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".multica", "plugins", ".trash"), nil
}

// userPluginHomeDir is the plugin's on-disk root (env/ + artifacts/ +
// runs.json live under it).
func userPluginHomeDir(slug string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".multica", "plugins", slug), nil
}

// reclaimPluginEnvDir moves ~/.multica/plugins/<slug>/ into
// ~/.multica/plugins/.trash/<unixts>-<slug>/. Best-effort: a cross-device
// rename (EXDEV) or any IO error is returned as a descriptive error and
// the ledger row is marked 'failed' (retryable).
func reclaimPluginEnvDir(slug string) error {
	src, err := userPluginHomeDir(slug)
	if err != nil {
		return err
	}
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil // nothing on disk — already clean
	} else if err != nil {
		return fmt.Errorf("stat plugin dir: %w", err)
	}
	trash, err := pluginTrashDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return fmt.Errorf("create trash dir: %w", err)
	}
	dst := filepath.Join(trash, fmt.Sprintf("%d-%s", time.Now().Unix(), slug))
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move plugin dir to trash: %w", err)
	}
	return nil
}

// reclaimPluginResources walks the teardown ledger and reclaims every
// plugin-owned resource. Declared (pre-existing) resources are kept and
// reported as such. Every row's reclaim_status is updated with the
// outcome; failures stay 'failed' for the retry endpoint.
func (h *Handler) reclaimPluginResources(ctx context.Context, slug string) []ReclaimOutcome {
	rows, err := h.Queries.ListUserPluginResources(ctx, slug)
	if err != nil {
		slog.Warn("plugin reclaim: ledger lookup failed", "slug", slug, "error", err)
		return []ReclaimOutcome{{Type: "ledger", Name: slug, Action: "failed", Detail: err.Error()}}
	}
	out := make([]ReclaimOutcome, 0, len(rows))
	for _, row := range rows {
		resID := ""
		if row.ResourceID.Valid {
			resID = util.UUIDToString(row.ResourceID)
		}
		outcome := h.reclaimOnePluginResource(ctx, row)
		out = append(out, outcome)

		status := "reclaimed"
		switch outcome.Action {
		case "kept":
			status = "skipped"
		case "failed":
			status = "failed"
		}
		if _, err := h.Queries.UpdateUserPluginResourceReclaimStatus(ctx, db.UpdateUserPluginResourceReclaimStatusParams{
			ID:            row.ID,
			ReclaimStatus: status,
		}); err != nil {
			slog.Warn("plugin reclaim: status update failed", "slug", slug, "resource", resID, "error", err)
		}
	}
	return out
}

// reclaimOnePluginResource reclaims a single ledger row. Kept as a method
// so each branch can use the shared Queries.
func (h *Handler) reclaimOnePluginResource(ctx context.Context, row db.UserPluginResource) ReclaimOutcome {
	resID := ""
	if row.ResourceID.Valid {
		resID = util.UUIDToString(row.ResourceID)
	}
	outcome := ReclaimOutcome{Type: row.ResourceType, ID: resID, Origin: row.Origin, Action: "reclaimed"}

	if row.Origin != "provisioned" {
		// Declared = the user's own resource; delete must never reclaim it.
		outcome.Action = "kept"
		outcome.Detail = "declared resource — not owned by the plugin"
		return outcome
	}

	switch row.ResourceType {
	case "agent":
		n, err := h.Queries.ArchiveUserPluginAgent(ctx, row.ResourceID)
		if err != nil {
			outcome.Action = "failed"
			outcome.Detail = err.Error()
		} else if n == 0 {
			outcome.Detail = "already archived"
		}
	case "squad":
		n, err := h.Queries.ArchiveUserPluginSquad(ctx, row.ResourceID)
		if err != nil {
			outcome.Action = "failed"
			outcome.Detail = err.Error()
		} else if n == 0 {
			outcome.Detail = "already archived"
		}
	case "autopilot":
		n, err := h.Queries.DisableUserPluginAutopilot(ctx, row.ResourceID)
		if err != nil {
			outcome.Action = "failed"
			outcome.Detail = err.Error()
		} else if n == 0 {
			outcome.Detail = "not active"
		}
	case "skill":
		// Hard delete (skill has no soft-delete column); the origin guard
		// above means only plugin-created skills reach this branch. The
		// workspace scoping comes from the ledger row.
		if err := h.Queries.DeleteSkill(ctx, db.DeleteSkillParams{ID: row.ResourceID, WorkspaceID: row.WorkspaceID}); err != nil {
			outcome.Action = "failed"
			outcome.Detail = err.Error()
		}
	case "env_dir":
		if err := reclaimPluginEnvDir(row.PluginSlug); err != nil {
			outcome.Action = "failed"
			outcome.Detail = err.Error()
		}
	default:
		outcome.Action = "kept"
		outcome.Detail = "unknown resource type"
	}
	return outcome
}

// resolveResourceLabel resolves a human-readable name for a ledger row
// (best-effort; the raw UUID is the fallback).
func (h *Handler) resolveResourceLabel(ctx context.Context, row db.UserPluginResource) string {
	if !row.ResourceID.Valid {
		return row.PluginSlug + "/ (env dir)"
	}
	switch row.ResourceType {
	case "agent":
		var name string
		if err := h.DB.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, row.ResourceID).Scan(&name); err == nil {
			return name
		}
	case "squad":
		var name string
		if err := h.DB.QueryRow(ctx, `SELECT name FROM squad WHERE id = $1`, row.ResourceID).Scan(&name); err == nil {
			return name
		}
	case "autopilot":
		var title string
		if err := h.DB.QueryRow(ctx, `SELECT title FROM autopilot WHERE id = $1`, row.ResourceID).Scan(&title); err == nil {
			return title
		}
	case "skill":
		var name string
		if err := h.DB.QueryRow(ctx, `SELECT name FROM skill WHERE id = $1`, row.ResourceID).Scan(&name); err == nil {
			return name
		}
	}
	return ""
}

// ReclaimPlanResponse is the wire shape of
// GET /api/user-plugins/{slug}/reclaim-plan and the retry endpoint.
type ReclaimPlanResponse struct {
	Slug         string                `json:"slug"`
	FlagKey      string                `json:"flag_key"`
	ActiveIssues int64                 `json:"active_issues"`
	DirBytes     int64                 `json:"dir_bytes"`
	Resources    []reclaimPlanResource `json:"resources"`
}

type reclaimPlanResource struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	ID      string `json:"id,omitempty"`
	Origin  string `json:"origin"`
	Status  string `json:"status"`
	Outcome string `json:"outcome,omitempty"` // set on the reclaim retry path
}

// buildReclaimPlan assembles the plan the CLI --dry-run and the UI delete
// confirmation both render.
func (h *Handler) buildReclaimPlan(ctx context.Context, slug, flagKey string) ReclaimPlanResponse {
	plan := ReclaimPlanResponse{Slug: slug, FlagKey: flagKey, Resources: []reclaimPlanResource{}}

	if n, err := h.Queries.CountActiveIssuesByLabSource(ctx, pgtype.Text{String: flagKey, Valid: flagKey != ""}); err == nil {
		plan.ActiveIssues = n
	} else {
		slog.Warn("reclaim plan: active-issue count failed", "slug", slug, "error", err)
	}

	plan.DirBytes = pluginDirBytes(slug)

	rows, err := h.Queries.ListUserPluginResources(ctx, slug)
	if err != nil {
		slog.Warn("reclaim plan: ledger lookup failed", "slug", slug, "error", err)
		return plan
	}
	for _, row := range rows {
		plan.Resources = append(plan.Resources, reclaimPlanResource{
			Type:   row.ResourceType,
			Name:   h.resolveResourceLabel(ctx, row),
			ID:     util.UUIDToString(row.ResourceID),
			Origin: row.Origin,
			Status: row.ReclaimStatus,
		})
	}
	return plan
}

// pluginDirBytes sums the size of the plugin's on-disk tree (0 when absent).
func pluginDirBytes(slug string) int64 {
	root, err := userPluginHomeDir(slug)
	if err != nil {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // skip unreadable entries — best-effort size
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// optionalUUIDParam parses a provenance UUID supplied by the CLI
// (MULTICA_ISSUE_ID / MULTICA_TASK_ID). Empty → zero UUID; malformed →
// error so the handler can 400 instead of silently dropping provenance.
func optionalUUIDParam(s string) (pgtype.UUID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.UUID{}, nil
	}
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q", s)
	}
	return u, nil
}
