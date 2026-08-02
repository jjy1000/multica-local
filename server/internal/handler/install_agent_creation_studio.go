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

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
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
	// 0.5.3 visibility: hide the workspace's agent-engineering resources
	// (智能体工程师团队 squad + its members) from the regular agent/squad
	// lists — they are lab infrastructure, shown only when the lab flag is
	// on. By-name lookup keeps this generic across workspaces; a missing
	// member is skipped (the lab's own leader is provisioned above).
	upsertAgentCreationStudioVisibility(ctx, h, workspaceUUID)
	return nil
}

// agentCreationStudioTeam is the squad + member names whose resources must
// be hidden behind the lab flag (migration 234 seeds this for the primary
// workspace; this helper re-seeds by name for any workspace).
const agentCreationStudioTeam = "智能体工程师团队"

var agentCreationStudioMemberNames = []string{
	"智能体工程负责人",
	"智能体专家",
	"外挂知识库专家",
	"自动化专家",
	"技能专家",
	"智能体优化专家",
}

// upsertAgentCreationStudioVisibility resolves the engineering squad +
// members by name in the workspace and seeds their visibility rows under
// the agent_creation_studio flag. Idempotent (ON CONFLICT DO NOTHING);
// missing rows are skipped silently — the squad may not exist in every
// workspace.
func upsertAgentCreationStudioVisibility(ctx context.Context, h *Handler, workspaceID pgtype.UUID) {
	if h == nil || h.Queries == nil {
		return
	}
	if sq, err := h.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        agentCreationStudioTeam,
	}); err == nil {
		_ = h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      agentCreationStudioSource,
			ResourceType: string(experimental.HideSquad),
			ResourceID:   sq.ID,
		})
	}
	for _, name := range agentCreationStudioMemberNames {
		ag, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        name,
		})
		if err != nil {
			continue // not present in this workspace; skip
		}
		_ = h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      agentCreationStudioSource,
			ResourceType: string(experimental.HideAgent),
			ResourceID:   ag.ID,
		})
	}
}

// upsertAgentCreationExpert finds-or-creates the studio leader agent.
//
// 0.5.5: this helper is now also called from `boot_provision_product_labs.go`
// at server startup, BEFORE the local daemon has registered a runtime row.
// The previous implementation relied on `resolveWorkspaceOnlineRuntime`
// returning a valid UUID at call time — true under user-triggered install
// (the daemon is already online by then) but always false under cold boot
// (no daemon has registered yet). Without a fix, `CreateAgent` would fail
// the `agent.runtime_id` NOT NULL constraint (SQLSTATE 23502).
//
// The fix mirrors the 0.3.35 fallback used by install_claude_science.go:
// when no online local runtime exists, provision a synthetic offline stub
// runtime so the leader agent row satisfies the FK. Once the user later
// starts the local daemon, the existing `rebindLabAgentsToOnlineRuntime`
// path re-points the agent's runtime_id at the live daemon (the studio
// leader agent name is in `labLeaderAgentNames` so it is rebound
// automatically on the next install or daemon bootstrap walk).
func upsertAgentCreationExpert(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        AgentCreationExpertName,
	}); err == nil {
		return existing.ID, nil
	}
	// 0.3.35 + 0.5.5: try the online local runtime first; on miss,
	// fall back to a synthetic offline stub so the agent row can
	// satisfy `agent.runtime_id` NOT NULL even on cold boot (before
	// the daemon has registered). The stub is the same shape as
	// install_claude_science.go's `upsertClaudeScienceRuntime` —
	// stable daemon_id, offline status, metadata tag for audit.
	runtimeID, err := resolveOrSynthesizeProductRuntime(ctx, h, workspaceID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("resolve product runtime: %w", err)
	}
	if !runtimeID.Valid {
		// Both the online lookup and the synthetic upsert returned
		// invalid. Treat as a hard fail — the boot hook logs and
		// the next user-triggered install will retry once the DB is
		// healthier.
		return pgtype.UUID{}, errors.New("no online local runtime and synthetic runtime upsert returned invalid id")
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
