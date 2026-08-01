// Package handler — install_agent_self_opt.go (0.3.27 B4)
//
// Agent Self-Optimization lab install handler. Mirrors
// install_constitution_agent.go. Provisions the 智能体优化专家 agent
// plus 2 autopilots (每3工作日批量优化 + SkillOpt-Multica 每日自进化
// 循环) into the caller's active workspace, idempotent.

package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentSelfOptSource = "agent_self_optimization"

// InstallAgentSelfOptimization provisions the agent_self_optimization
// lab. Same shape as the retired InstallConstitutionAgent (0.3.57,
// migration 165); the agents and auto-
// pilots are different (the lab owns a different roster).
func (h *Handler) InstallAgentSelfOptimization(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installAgentSelfOptimization: handler not initialized")
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
	agentID, err := upsertAgentSelfOptAgent(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert agent: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, agentSelfOptSource, experimental.LockAgent, agentID); err != nil {
		return fmt.Errorf("agent lock: %w", err)
	}
	for _, aut := range agentSelfOptAutopilots() {
		if _, err := upsertAgentSelfOptAutopilot(ctx, h, workspaceUUID, agentID, aut); err != nil {
			return fmt.Errorf("autopilot %s: %w", aut.title, err)
		}
	}
	// 0.3.35: heal leader agent runtime_id (was empty pre-0.3.35).
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)
	return nil
}

type agentSelfOptAutopilotSpec struct {
	title       string
	description string
	cron        string
}

func agentSelfOptAutopilots() []agentSelfOptAutopilotSpec {
	return []agentSelfOptAutopilotSpec{
		{
			// 0.5.3: title matches the legacy 2026-06 row (migration 150
			// seeded its visibility ID). The pre-0.5.3 no-space titles
			// created DUPLICATE rows on re-install whose visibility never
			// landed (migration 235 fixes the existing dupes). Keep the
			// space-separated titles so re-install upserts the SAME row.
			title:       "SkillOpt-Multica · 每日 00:00 自进化循环",
			description: "SkillOpt-Multica · 每日 00:00 自进化循环,对工作区的 Skill 做体检与改进。",
			cron:        "0 0 * * *",
		},
		{
			title:       "智能体工程师团队 · 每3工作日批量优化",
			description: "智能体工程师团队驱动的每3工作日批量优化任务。",
			cron:        "0 2 * * 1-5", // weekday 02:00 (3 workday cadence is enforced by shouldSkipDispatch, not the cron)
		},
	}
}

func upsertAgentSelfOptAgent(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	const name = "智能体优化专家"
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        name,
	}); err == nil {
		return existing.ID, nil
	}
	// 0.3.35: bind the leader agent to a real daemon when one is
	// online. Without this the autopilot cron dispatches land on an
	// agent with RuntimeID invalid → daemon never picks it up.
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		slog.Info("upsertAgentSelfOptAgent: no online local runtime; "+
			"agent created without runtime — autopilot cron will skip dispatch until daemon is online",
			"workspace_id", util.UUIDToString(workspaceID))
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               name,
		Description:        "智能体优化专家 — 协同 SkillOpt-Multica 自进化循环驱动工作区的智能体/技能持续质量改进。",
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「智能体优化专家」。你的职责是周期性地审视工作区的 agent / skill / autopilot,识别高频失败模式与可优化点,并 SkillOpt 自进化循环驱动改进。详见 skillopt-multica Skill。",
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

func upsertAgentSelfOptAutopilot(
	ctx context.Context, h *Handler,
	workspaceID, agentID pgtype.UUID,
	spec agentSelfOptAutopilotSpec,
) (pgtype.UUID, error) {
	existing, err := h.Queries.GetAutopilotByWorkspaceAndTitle(ctx, db.GetAutopilotByWorkspaceAndTitleParams{
		WorkspaceID: workspaceID,
		Title:       spec.title,
	})
	if err == nil {
		return existing.ID, nil
	}
	created, cerr := h.Queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        workspaceID,
		Title:              spec.title,
		Description:        pgtype.Text{String: spec.description, Valid: true},
		AssigneeType:       "agent",
		AssigneeID:         agentID,
		Status:             "active",
		ExecutionMode:      "create_issue",
		IssueTitleTemplate: pgtype.Text{String: "[agent-self-opt] " + spec.title + " — {{date}}", Valid: true},
		ProjectID:          pgtype.UUID{},
		CreatedByType:      "agent",
		CreatedByID:        agentID,
	})
	if cerr != nil {
		if re, rerr := h.Queries.GetAutopilotByWorkspaceAndTitle(ctx, db.GetAutopilotByWorkspaceAndTitleParams{
			WorkspaceID: workspaceID,
			Title:       spec.title,
		}); rerr == nil {
			return re.ID, nil
		}
		return pgtype.UUID{}, cerr
	}
	if spec.cron != "" {
		nextRun, nextErr := service.ComputeNextRun(
			spec.cron, "UTC",
		)
		var nextRunTS pgtype.Timestamptz
		if nextErr == nil {
			nextRunTS = pgtype.Timestamptz{Time: nextRun, Valid: true}
		} else {
			// Non-fatal — the scheduler will populate next_run_at on
			// its first tick via scheduler.advanceTriggerNextRun. Log
			// so a broken cron doesn't go silently unnoticed.
			fmt.Printf(
				"install_agent_self_opt: compute next_run_at failed cron=%q err=%v\n",
				spec.cron, nextErr,
			)
		}
		_, _ = h.Queries.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
			AutopilotID:    created.ID,
			Kind:           "schedule",
			Enabled:        true,
			CronExpression: pgtype.Text{String: spec.cron, Valid: true},
			Timezone:       pgtype.Text{String: "UTC", Valid: true},
			NextRunAt:      nextRunTS,
			Label:          pgtype.Text{String: "schedule", Valid: true},
			Provider:       pgtype.Text{String: "generic", Valid: true},
			EventFilters:   []byte(`{}`),
			WebhookToken:   pgtype.Text{},
		})
	}
	return created.ID, nil
}
