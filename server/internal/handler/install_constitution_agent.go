// Package handler — install_constitution_agent.go (0.3.27 B4)
//
// Constitution Agent lab install handler. Provisions the 宪法智能体
// agent and its 3 autopilots (CTR 三周评审 / CSIL 宪章自优化 / TAOL
// 任务-智能体优化) into the caller's active workspace, then writes the
// expected experimental_resource_lock rows. Idempotent.
//
// 0.3.20 PR first shipped the visibility hide for these rows; the
// autopilot + agent rows themselves were never inserted, so toggling
// the flag on had no visible effect (the autopilot scheduler's
// shouldSkipDispatch gate referenced UUIDs that did not exist). This
// handler finally lands the row provisioning so the flag acts as the
// opt-in gate it was always meant to be.

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

const constitutionSource = "constitution_agent"

// InstallConstitutionAgent provisions the constitution_agent lab.
// Mirrors the shape of InstallMythos / InstallClaudeScience so the
// HTTP layer can dispatch either source through the same endpoint.
func (h *Handler) InstallConstitutionAgent(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installConstitutionAgent: handler not initialized")
	}

	// 1. Resolve target workspace — caller's active workspace, no
	// reserved workspace is created (0.3.25 hard constraint).
	workspaceUUID, err := resolveLabWorkspace(ctx, h, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if userID != "" {
		if err := ensureWorkspaceOwner(ctx, h, workspaceUUID, userID); err != nil {
			return fmt.Errorf("ensure workspace owner: %w", err)
		}
	}

	// 2. Provision agent + 3 autopilots. The autopilot/agent IDs are
	// pinned (canonical IDs in experimental/visibility.go) so the
	// scheduler's `shouldSkipDispatch` and visibility hide-set keep
	// matching the row that lands here.
	agentID, err := upsertConstitutionAgent(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert agent: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, constitutionSource, experimental.LockAgent, agentID); err != nil {
		return fmt.Errorf("agent lock: %w", err)
	}

	for _, aut := range constitutionAutopilots() {
		_, err := upsertConstitutionAutopilot(ctx, h, workspaceUUID, agentID, aut)
		if err != nil {
			return fmt.Errorf("autopilot %s: %w", aut.title, err)
		}
		// 0.3.27 B4: autopilots are not separately locked in
		// experimental_resource_lock (no LockAutopilot constant). The
		// agent lock above is what gates the lab surface; auto-
		// pilots are governed by the experimental_resource_visibility
		// rows in migration 153.
	}
	// 0.3.35: heal existing leader agent's runtime_id (was empty
	// pre-0.3.35) so the issue-creation auto-dispatch path actually
	// enqueues. Best-effort; offline workspaces leave the row alone.
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)
	return nil
}

// constitutionAutopilotSpec is one autopilot row the constitution_agent
// install handler provisions. The pinned IDs mirror the constants in
// experimental/visibility.go so the visibility table and the live row
// agree.
type constitutionAutopilotSpec struct {
	title       string
	description string
	// cron is the empty string for event-driven autopilots; the
	// install handler calls CreateAutopilotTrigger separately when
	// the row materialises.
	cron string
}

func constitutionAutopilots() []constitutionAutopilotSpec {
	return []constitutionAutopilotSpec{
		{
			title:       "CTR 宪章三周评审",
			description: "每 3 周对 workspace 《智能体宪章》做一次三周评审并落地 issue 决议报告。",
			cron:        "0 10 1,22 * *", // day-of-month 1 and 22 at 10:00 UTC
		},
		{
			title:       "CSIL 宪章自优化循环",
			description: "宪章自优化循环 — 由 宪法智能体 驱动的 charter self-improvement loop。",
			cron:        "0 3 * * 0", // 03:00 UTC every Sunday
		},
		{
			title:       "TAOL 任务-智能体优化循环",
			description: "任务-智能体优化循环 — task ↔ agent 双向优化循环。",
			cron:        "0 4 * * *", // 04:00 UTC daily
		},
	}
}

func upsertConstitutionAgent(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	const name = "宪法智能体"
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        name,
	}); err == nil {
		return existing.ID, nil
	}
	var ownerID pgtype.UUID
	if err := ownerID.Scan(util.UUIDToString(workspaceID)); err != nil {
		// workspaceID is not a user — leave owner empty.
		ownerID = pgtype.UUID{}
	}
	// 0.3.35: bind the leader agent to a real daemon when one is
	// online. The previous empty RuntimeID meant
	// isAgentAssigneeReady returned false and the auto-dispatch path
	// (issue.lab_source='constitution_agent') silently skipped enqueue.
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		slog.Info("upsertConstitutionAgent: no online local runtime; "+
			"agent created without runtime — autopilot cron will skip dispatch until daemon is online",
			"workspace_id", util.UUIDToString(workspaceID))
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               name,
		Description:        "《智能体宪章 v6》守护智能体,通过 CTR/CSIL/TAOL 三轨制持续评审与优化工作区的宪法与 task-agent 行为契约。",
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            ownerID,
		Instructions:       "你是「宪法智能体」。你的职责是:(1) 维护 workspace 当前的《智能体宪章》;(2) 周期性执行 CTR 三周评审、CSIL 自优化循环、TAOL 任务-智能体优化循环;(3) 任何宪法修订必须输出可审计的决策记录。详见 multica-constitution-agent Skill。",
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

func upsertConstitutionAutopilot(
	ctx context.Context, h *Handler,
	workspaceID, agentID pgtype.UUID,
	spec constitutionAutopilotSpec,
) (pgtype.UUID, error) {
	existing, err := h.Queries.GetAutopilotByWorkspaceAndTitle(ctx, db.GetAutopilotByWorkspaceAndTitleParams{
		WorkspaceID: workspaceID,
		Title:       spec.title,
	})
	if err == nil {
		return existing.ID, nil
	}
	created, cerr := h.Queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:         workspaceID,
		Title:               spec.title,
		Description:         pgtype.Text{String: spec.description, Valid: true},
		AssigneeType:        "agent",
		AssigneeID:          agentID,
		Status:              "active",
		ExecutionMode:       "create_issue",
		IssueTitleTemplate:  pgtype.Text{String: "[constitution] " + spec.title + " — {{date}}", Valid: true},
		ProjectID:           pgtype.UUID{},
		CreatedByType:       "agent",
		CreatedByID:         agentID,
	})
	if cerr != nil {
		// Race: another caller inserted between our SELECT and CREATE.
		// Re-fetch and return that row.
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
			// ComputeNextRun error is non-fatal at install time — the
			// scheduler will populate next_run_at on its first tick via
			// scheduler.advanceTriggerNextRun. Log so a broken cron
			// doesn't go silently unnoticed, but still provision the
			// trigger so the user can fix the cron from the UI.
			fmt.Printf(
				"install_constitution_agent: compute next_run_at failed cron=%q err=%v\n",
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
