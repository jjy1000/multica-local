// Package handler — install_semantica.go (0.5.22 Semantica × Multica Phase 2)
//
// semantica install handler. Mirrors install_pythia.go in shape.
//
// Provisions a single `semantica_decision_advisor` leader agent
// bound to the multica-semantica-decision-advisor Skill (auto-loaded
// via the embed.FS in service/builtin_skills.go). The Semantica
// Explorer FastAPI server itself is started by the desktop
// manager-factory (apps/desktop/src/main/experimental/subprocess-manager.ts);
// this handler only owns the agent / lock / visibility rows in the
// user's workspace so that:
//   - IssueService.assignDefaultLabAgent + handler.assignDefaultLabAgentOnUpdate
//     resolve semantica → semantica_decision_advisor (P0#4 contract).
//   - The leader is hidden from the regular agent picker when the
//     semantica flag is off (visibility row, mirrors
//     upsertPythiaVisibility at install_pythia.go:121-130).
//
// Idempotent: lookup-by-name before insert + ON CONFLICT DO NOTHING
// on the visibility row. Re-installs never 500.

package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// semanticaSource is the flag_key / lab_source constant for semantica.
// MUST equal experimental.SourceSemantica (lock.go) and the catalog
// entry Key (catalog.go).
const semanticaSource = "semantica"

// semanticaDecisionAdvisorName is the agent_name string that
// service.IssueService.defaultLeaderAgentForLab + handler.defaultLabLeaderForKey
// both resolve "semantica" → this. KEEP IN SYNC across the two tables
// (any rename here must update issue.go:3035 / service/issue.go:380).
const semanticaDecisionAdvisorName = "semantica_decision_advisor"

// InstallSemantica provisions the semantica lab into the caller's
// active workspace. Idempotent across re-installs.
func (h *Handler) InstallSemantica(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installSemantica: handler not initialized")
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
	// when an issue with lab_source=semantica gets auto-assigned via
	// assignDefaultLabAgentOnUpdate. The actual REST calls run inside
	// the agent subprocess via the bundled multica-semantica-decision-advisor
	// skill (which calls curl against /experimental/semantica/api/...).
	agentID, err := upsertSemanticaDecisionAdvisorAgent(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert semantica_decision_advisor agent: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, semanticaSource, experimental.LockAgent, agentID); err != nil {
		return fmt.Errorf("semantica_decision_advisor agent lock: %w", err)
	}

	// 2. Forward guard visibility row (mirrors upsertPythiaVisibility
	// at install_pythia.go:121-130). Hides the leader from the regular
	// agent picker when semantica is off.
	if err := upsertSemanticaVisibility(ctx, h, agentID); err != nil {
		return fmt.Errorf("semantica visibility: %w", err)
	}

	// 0.3.35: heal leader agent runtime_id (was empty pre-0.3.35).
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)
	return nil
}

func upsertSemanticaDecisionAdvisorAgent(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        semanticaDecisionAdvisorName,
	}); err == nil {
		return existing.ID, nil
	}
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		slog.Info("upsertSemanticaDecisionAdvisorAgent: no online local runtime; "+
			"agent created without runtime — daemon auto-assign will skip dispatch until daemon is online",
			"workspace_id", util.UUIDToString(workspaceID))
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:   workspaceID,
		Name:          semanticaDecisionAdvisorName,
		Description:   "Semantica 决策情报专员 — 调用 Semantica REST API(ontology / decision / chain / provenance / sparql / analytics / temporal / enrich),为上层 agent 提供决策上下文与因果链溯源。",
		AvatarUrl:     pgtype.Text{},
		RuntimeMode:   "local",
		RuntimeConfig: []byte(`{}`),
		RuntimeID:     runtimeID,
		Visibility:    "workspace",
		// 0.3.56: leader is single-task-at-a-time; one Semantica call
		// per issue keeps the JSON envelope clean for the caller.
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「Semantica 决策情报专员」lead agent。当 issue.lab_source 设为 semantica 时,daemon 会自动派单给你,使用 multica-semantica-decision-advisor 技能调用 Semantica REST API(/api/decisions、/api/decisions/{id}/chain、/api/ontology/entity/{uri}),输出 JSON-可解析的决策上下文供调用方 agent 解析。",
		CustomEnv:          []byte(`{}`),
		// MUST be a JSON array, not an object: agent readers unmarshal
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

// upsertSemanticaVisibility inserts the forward-guarded
// experimental_resource_visibility row for the semantica_decision_advisor
// leader agent so a flag-gated picker filter
// (filterLabsHiddenByDefault(flagKey, HideAgent)) hides it from the
// regular agent picker when semantica is OFF. Idempotent: ON CONFLICT
// DO NOTHING on (flag_key, resource_type, resource_id).
func upsertSemanticaVisibility(ctx context.Context, h *Handler, agentID pgtype.UUID) error {
	if h == nil || h.Queries == nil {
		return errors.New("upsertSemanticaVisibility: handler not initialized")
	}
	return h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      semanticaSource,
		ResourceType: "agent",
		ResourceID:   agentID,
	})
}
