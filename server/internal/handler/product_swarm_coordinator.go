// Package handler — product_swarm_coordinator.go (0.5.22)
//
// 0.5.22: the swarm_topology leader agent `swarm_coordinator` is
// boot-provisioned alongside `agent_creation_expert`. The
// multica-creating-swarms SKILL.md defines the orchestrator's
// lifecycle contract (3 phases: bootstrap → execute → cleanup);
// the leader agent row is the runtime hook the daemon drives
// against the multica-creating-swarms skill.
//
// The orchestrator is intentionally hidden from regular pickers
// (agent.lab_managed=true via experimental_resource_visibility
// row) — the mutex contract #5 (issue.go:2222-2254) forbids manual
// assignee on swarm_topology issues, so the agent must not be
// reachable except via the lab leader-rewrite path.
//
// Authorization is asymmetric: the runner-side agent (the
// orchestrator that walks the 5-phase machine) is created by THIS
// helper at boot. The role-agents authored during Phase 1 bootstrap
// are created by the orchestrator itself (see multica-creating-swarms
// SKILL.md), so they are out of scope here.

package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SwarmCoordinatorName is the swarm topology leader agent name. The
// leader lookup tables in handler/issue.go (defaultLabLeaderForKey
// at line 3018) + service/issue.go (defaultLeaderAgentForLab) mirror
// this constant.
const SwarmCoordinatorName = "swarm_coordinator"

// swarmCoordinatorSkillName is the embedded builtin skill that
// carries the orchestrator's lifecycle contract (3 phases:
// bootstrap → execute → cleanup). Bound to the leader agent via
// agent_skill so the runtime brief injects the SKILL.md body at
// claim time.
const swarmCoordinatorSkillName = "multica-creating-swarms"

// upsertSwarmCoordinator finds-or-creates the swarm topology leader
// agent, mirroring the 0.5.5 upsertAgentCreationExpert shape.
//
// 0.5.22: on first create, also writes an
// experimental_resource_visibility row tagged flag_key='swarm_topology'
// + resource_type='agent' so the agent DTO stamp on ListAgents/GetAgent
// sets lab_managed=true (hide from regular pickers). The mutex
// contract #5 forbids manual assignee on swarm_topology issues, so
// the agent must not be reachable via AssigneePicker.
//
// 0.5.22: also materialises the embedded multica-creating-swarms
// SKILL.md into the workspace and binds it via agent_skill so the
// orchestrator picks up the lifecycle contract at claim time.
// Non-fatal: a missing embedded SKILL.md logs at warn and the
// agent_skill row is skipped (BuiltinSkills() in the runtime layer
// still exposes whatever is embedded).
//
// The online-runtime-first fallback already handles cold boot (no
// daemon yet) via resolveOrSynthesizeProductRuntime. The visibility
// row is non-fatal: a transient DB failure logs at warn and the next
// leader-rewrite call self-heals via the lazy fallback.
func upsertSwarmCoordinator(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        SwarmCoordinatorName,
	}); err == nil {
		// Agent row exists — still try to bind the skill if missing.
		// On a disabled-flag cold boot the binding may not have run
		// yet; idempotent so safe to re-fire.
		bindSwarmCoordinatorSkill(ctx, h, workspaceID, existing.ID)
		return existing.ID, nil
	}
	runtimeID, err := resolveOrSynthesizeProductRuntime(ctx, h, workspaceID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("resolve product runtime: %w", err)
	}
	if !runtimeID.Valid {
		return pgtype.UUID{}, errors.New("no online local runtime and synthetic runtime upsert returned invalid id")
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               SwarmCoordinatorName,
		Description:        "蜂群拓扑协调员 — 自组织多智能体系统的 leader。在启动时按 multica-creating-swarms 技能创建 N 角色 agents + M skills + 1 协调 squad,运行 5 阶段机器 (research → design → implement → review → done),并在任务终止时清理临时资源。",
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		PermissionMode:    "public_to",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「蜂群拓扑协调员」。当 issue.lab_source='swarm_topology' 委托给你时,理解需求并调用 multica-creating-swarms 技能:Phase 1 创建角色 agents + skills + 协调 squad,Phase 2 运行 5 阶段机器 (research → design → implement → review → done),Phase 3 在任务终止时清理临时资源。每个阶段都通过 SQL 写状态;人类中断通过 MUL-4304 注释 reconcile 进群。",
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
	// 0.5.22: visibility row so the agent DTO stamp on
	// ListAgents/GetAgent sets lab_managed=true (hide from regular
	// pickers). grep sees two callers: ListAgents (agent.go) +
	// GetAgent (single-fetch, 0.5.18 SEC-P1-7). If this row is
	// missing, the agent surfaces in regular pickers and the mutex
	// becomes a UX papercut (user can pick something they cannot
	// actually use). Non-fatal: a failure logs at warn and the next
	// boot will retry.
	if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      "swarm_topology",
		ResourceType: "agent",
		ResourceID:   created.ID,
	}); err != nil {
		slog.Warn("upsertSwarmCoordinator: visibility row insert failed",
			"workspace_id", util.UUIDToString(workspaceID),
			"agent_id", util.UUIDToString(created.ID),
			"err", err)
	}
	// 0.5.22: bind the multica-creating-swarms skill so the runtime
	// brief injects the lifecycle contract at claim time.
	bindSwarmCoordinatorSkill(ctx, h, workspaceID, created.ID)
	slog.Info("upsertSwarmCoordinator: product agent provisioned",
		"workspace_id", util.UUIDToString(workspaceID),
		"agent_id", util.UUIDToString(created.ID),
		"runtime_id", util.UUIDToString(runtimeID))
	return created.ID, nil
}

// bindSwarmCoordinatorSkill materialises the embedded
// multica-creating-swarms SKILL.md into the workspace (find-or-create
// via UNIQUE(workspace_id, name)) and writes the agent_skill binding
// so the runtime brief injects the skill body at claim time.
// AddAgentSkill uses ON CONFLICT DO NOTHING; the SKILL.md CreateSkill
// path is the usual find-or-create shape. Mirrors install_code_canvas.go's
// upsertCodeCanvasSkill + bindCodeCanvasSkill pair.
func bindSwarmCoordinatorSkill(ctx context.Context, h *Handler, workspaceID, agentID pgtype.UUID) {
	var skillID pgtype.UUID
	existing, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        swarmCoordinatorSkillName,
	})
	if err == nil {
		skillID = existing.ID
	} else {
		// Not yet in the workspace — materialise from the embedded FS.
		skill, ok := service.LoadBuiltinSkillByName(swarmCoordinatorSkillName)
		if !ok {
			slog.Warn("bindSwarmCoordinatorSkill: embedded SKILL.md not found; agent_skill row will not be created",
				"name", swarmCoordinatorSkillName)
			return
		}
		created, cerr := h.Queries.CreateSkill(ctx, db.CreateSkillParams{
			WorkspaceID: workspaceID,
			Name:        skill.Name,
			Description: "Swarm topology 创建与编排技能 — 蜂群拓扑 leader 的 3 阶段生命周期合同 (bootstrap → execute → cleanup)。",
			Content:     skill.Content,
			Config:      []byte(`{}`),
			CreatedBy:   pgtype.UUID{},
		})
		if cerr != nil {
			// Race / already-exists — re-read.
			if re, rerr := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
				WorkspaceID: workspaceID,
				Name:        swarmCoordinatorSkillName,
			}); rerr == nil {
				skillID = re.ID
			} else {
				slog.Warn("bindSwarmCoordinatorSkill: create skill failed",
					"workspace_id", util.UUIDToString(workspaceID),
					"err", cerr)
				return
			}
		} else {
			skillID = created.ID
		}
	}
	if !skillID.Valid {
		return
	}
	if err := h.Queries.AddAgentSkill(ctx, db.AddAgentSkillParams{
		AgentID: agentID,
		SkillID: skillID,
	}); err != nil {
		slog.Warn("bindSwarmCoordinatorSkill: agent_skill binding failed",
			"workspace_id", util.UUIDToString(workspaceID),
			"agent_id", util.UUIDToString(agentID),
			"skill_id", util.UUIDToString(skillID),
			"err", err)
	}
}
