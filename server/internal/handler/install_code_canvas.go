// Package handler — install_code_canvas.go (0.3.54)
//
// code_canvas install handler. Provisions a single `code_canvas_worker`
// leader agent bound to the bundled code-canvas run.sh stub subprocess.
// The subprocess itself is started by apps/desktop/src/main/experimental/
// manager-factory.ts (manager-factory currently spawns pythia; this
// file provisions the agent row that the auto-dispatch path lands on
// when a code_canvas lab_source is set).
//
// Same shape as install_pythia.go / install_agent_self_opt.go.
// Idempotent.

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

const codeCanvasSource = "code_canvas"

// InstallCodeCanvas provisions the code_canvas lab into the caller's
// active workspace. Idempotent.
func (h *Handler) InstallCodeCanvas(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installCodeCanvas: handler not initialized")
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

	agentID, err := upsertCodeCanvasAgent(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert code_canvas_worker agent: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, codeCanvasSource, experimental.LockAgent, agentID); err != nil {
		return fmt.Errorf("code_canvas_worker agent lock: %w", err)
	}
	if err := upsertCodeCanvasVisibility(ctx, h, agentID); err != nil {
		return fmt.Errorf("code_canvas visibility: %w", err)
	}

	// 0.3.35: heal leader agent runtime_id (was empty pre-0.3.35).
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)
	return nil
}

func upsertCodeCanvasAgent(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	const name = "code_canvas_worker"
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        name,
	}); err == nil {
		return existing.ID, nil
	}
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		slog.Info("upsertCodeCanvasAgent: no online local runtime; "+
			"agent created without runtime — daemon auto-assign will skip dispatch until daemon is online",
			"workspace_id", util.UUIDToString(workspaceID))
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               name,
		Description:        "Code Canvas worker — 通过实验平台 subprocess 模板启起的内部 lab worker,真实 subprocess 生命周期由 desktop manager-factory 拥有。",
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「Code Canvas worker」lead agent。当 issue.lab_source 设为 code_canvas 时,daemon 会自动派单给你,通过 bundle-cli 分发的 run.sh stub 子进程返回 /health 即可。完整路径详情见 .omc/release-notes-0.3.51.md。",
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

func upsertCodeCanvasVisibility(ctx context.Context, h *Handler, agentID pgtype.UUID) error {
	if h == nil || h.Queries == nil {
		return errors.New("upsertCodeCanvasVisibility: handler not initialized")
	}
	return h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      codeCanvasSource,
		ResourceType: "agent",
		ResourceID:   agentID,
	})
}
