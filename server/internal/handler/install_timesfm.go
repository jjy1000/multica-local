// Package handler — install_timesfm.go (0.5.82 WL2)
//
// timesfm install handler. Mirrors install_pythia.go in shape.
// Provisions a single `timesfm_oracle` leader agent bound to the
// multica-timesfm skill. The vendored TimesFM 2.5 Python subprocess
// itself is started by the desktop manager-factory
// (apps/desktop/src/main/experimental/, generic manifest-driven
// manager from resources/timesfm/run.sh); this handler only owns the
// agent / lock / visibility rows in the user's workspace.
//
// Idempotent: a re-install of an already-present workspace is a no-op
// for the leader agent (looked up by name first). Visibility is
// purge-before-seed (DeletePluginResourceVisibilityByFlagKey then
// re-insert) so stale rows from a previous agent UUID or a
// delete/reseed cycle can never accumulate — the 0.5.78 labs
// hardening contract for user-plugin visibility, applied to a
// built-in lab.
//
// FLAG-KEY DUPLICATION LAW: the literal "timesfm" below is a VERBATIM
// copy of the catalog Key (import cycles forbid sharing a constant
// across packages — swarm_topology precedent). Pinned by
// TestCatalogAutoDispatchContract (catalog_test.go) and the migration
// 275 static test.

package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const timesfmSource = "timesfm"

// InstallTimesfm provisions the timesfm lab into the caller's active
// workspace. Idempotent.
func (h *Handler) InstallTimesfm(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installTimesfm: handler not initialized")
	}
	workspaceUUID, err := resolveLabWorkspace(ctx, h, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if userID != "" {
		if err := ensureWorkspaceOwner(ctx, h, workspaceUUID, userID); err != nil {
			return fmt.Errorf("ensure workspace owner: %w", err)
		}
	}

	// 1. Leader agent. The agent row is the daemon's dispatch target
	// when an issue with lab_source=timesfm gets auto-assigned. The
	// actual forecast work runs in the loopback Python service
	// (POST /forecast via the /experimental/timesfm proxy), and the
	// handler-side persistence lands in timesfm_forecast_run
	// (migration 275).
	agentID, err := upsertTimesfmOracleAgent(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert timesfm_oracle agent: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, timesfmSource, experimental.LockAgent, agentID); err != nil {
		return fmt.Errorf("timesfm_oracle agent lock: %w", err)
	}

	// 2. Visibility seed (purge-before-seed). Mirrors
	// upsertPythiaVisibility's forward-guard rationale: the current
	// ListVisibleAgentsByWorkspace path reads
	// experimental_resource_lock, but a future flag-gated picker
	// filter (filterLabsHiddenByDefault) will read visibility rows.
	// Purge first so rows tied to a stale resource id (previous
	// install, delete/reseed) never survive alongside the fresh one.
	if err := reseedTimesfmVisibility(ctx, h, agentID); err != nil {
		return fmt.Errorf("timesfm visibility reseed: %w", err)
	}

	// 0.3.35 heal: leader agent runtime_id (was empty pre-0.3.35).
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)
	return nil
}

func upsertTimesfmOracleAgent(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	const name = "timesfm_oracle"
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        name,
	}); err == nil {
		return existing.ID, nil
	}
	runtimeID, err := resolveOrSynthesizeLabRuntime(ctx, h, workspaceID,
		"timesfm", "TimesFM Lab Runtime", "timesfm",
		"experimental.timesfm")
	if err != nil {
		return pgtype.UUID{}, err
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:   workspaceID,
		Name:          name,
		Description:   "TimesFM 预测引擎 — 本地 TimesFM 2.5 时序预测(离线 torch 运行时),通过 multica-timesfm 技能暴露给上层 agent,分位数区间结果按 issue 持久化。",
		AvatarUrl:     pgtype.Text{},
		RuntimeMode:   "local",
		RuntimeConfig: []byte(`{}`),
		RuntimeID:     runtimeID,
		Visibility:    "workspace",
		// 0.5.22 MUL-3963: match the visibility='workspace' with
		// permission_mode='public_to' (per mig 245 backfill convention).
		PermissionMode: "public_to",
		// CPU inference is seconds-minutes per call; the leader is
		// single-task-at-a-time so two issues cannot thrash the model.
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「TimesFM 预测实验室」lead agent。当 issue.lab_source 设为 timesfm 时,daemon 会自动派单给你。使用 multica-timesfm 技能,对 issue 上下文中的数值序列(CSV 附件或评论中的指标表)调用 POST /api/experimental/timesfm/forecast/issue(引擎离线本地运行;权重未放置时以季节性朴素回退应答)。把预测结果(逐点值 + lower_80/upper_80/lower_90/upper_90 分位数区间、provenance、run_id)以评论形式贴回 issue;run_id 用于在实验室记录页 (/experimental/timesfm-lab) 深链到对应运行。",
		CustomEnv:          []byte(`{}`),
		// Must be a JSON array, not an object: agent readers unmarshal
		// custom_args into []string and log a WARN per read otherwise
		// (2026-08-06 audit; migration 238 repairs pre-existing rows).
		CustomArgs:    []byte(`[]`),
		McpConfig:     []byte(`{}`),
		Model:         pgtype.Text{},
		ThinkingLevel: pgtype.Text{},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return created.ID, nil
}

// reseedTimesfmVisibility purges every existing visibility row for the
// timesfm flag key, then inserts the fresh forward-guard row for the
// leader agent. DeletePluginResourceVisibilityByFlagKey is the same
// query user_plugins.go uses on plugin delete/reseed (0.5.78 hardening).
func reseedTimesfmVisibility(ctx context.Context, h *Handler, agentID pgtype.UUID) error {
	if h == nil || h.Queries == nil {
		return errors.New("reseedTimesfmVisibility: handler not initialized")
	}
	if _, err := h.Queries.DeletePluginResourceVisibilityByFlagKey(ctx, timesfmSource); err != nil {
		return fmt.Errorf("purge stale visibility rows: %w", err)
	}
	return h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      timesfmSource,
		ResourceType: "agent", // LockAgent (ResourceType) is "agent" in the SQL enum
		ResourceID:   agentID,
	})
}
