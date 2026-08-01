// Package handler — install_agent_creation_studio.go (0.5.3).
//
// Agent Creation Studio install handler. 0.3.45 shipped the studio as an
// action-type lab (open a manual creator, no issue binding). 0.5.3 upgrades
// it to an issue-bound lab: selecting it in LabPicker writes
// issue.lab_source='agent_creation_studio', the leader agent
// `agent_creation_expert` is auto-assigned (mirroring the other leader
// labs), and tasks created for that issue dispatch to the leader agent.
//
// The manual creator (the /experimental/agent-creation-studio view) is
// UNCHANGED — it stays reachable via the LabPicker action footer. The
// issue-bound path is the new "task assigned to this lab" flow.
//
// Idempotent: upsert-by-name, claim under the lab source.
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

const agentCreationStudioSource = "agent_creation_studio"

// AgentCreationExpertName is the studio leader agent name. The leader
// lookup tables in handler/issue.go + service/issue.go mirror this
// constant (0.3.46 P0#4 contract).
const AgentCreationExpertName = "agent_creation_expert"

// InstallAgentCreationStudio provisions the agent_creation_studio lab:
// the `agent_creation_expert` leader agent (bound to a live runtime so
// dispatched tasks are picked up). No autopilots — the lab is driven by
// user-picked issues, not cron.
func (h *Handler) InstallAgentCreationStudio(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installAgentCreationStudio: handler not initialized")
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
	agentID, err := upsertAgentCreationExpert(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert agent: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, agentCreationStudioSource, experimental.LockAgent, agentID); err != nil {
		return fmt.Errorf("agent lock: %w", err)
	}
	return nil
}

// upsertAgentCreationExpert finds-or-creates the studio leader agent.
func upsertAgentCreationExpert(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        AgentCreationExpertName,
	}); err == nil {
		return existing.ID, nil
	}
	// 0.3.35: bind the leader agent to a real daemon when one is online.
	// Without this the dispatched task rows land on an agent with
	// RuntimeID invalid → daemon never picks them up.
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		slog.Info("upsertAgentCreationExpert: no online local runtime; "+
			"agent created without runtime — dispatched tasks wait until the daemon is online",
			"workspace_id", util.UUIDToString(workspaceID))
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               AgentCreationExpertName,
		Description:        "智能体创建专家 — 根据用户委托创建智能体 / 技能 / 团队 / 自动化工程,遵循 multica-lab-builder 与 multica-creating-agents 技能。",
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「智能体创建专家」。当用户把任务委托给你时,理解需求并调用技能(multica-lab-builder / multica-creating-agents)创建对应的智能体、技能、团队或自动化工程。创建后把结果、用法和后续维护建议写回任务。",
		CustomEnv:          []byte(`{}`),
		CustomArgs:         []byte(`{}`),
		McpConfig:          []byte(`{}`),
		Model:              pgtype.Text{},
		ThinkingLevel:      pgtype.Text{},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return created.ID, nil
}
