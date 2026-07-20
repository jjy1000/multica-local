// Package handler — install_pythia.go (0.3.54)
//
// pythia_oracle install handler. Mirrors install_mythos.go in shape.
// Provisions a single `pythia_runtime` leader agent bound to the
// multica-pythia skill. The Pythia Python service itself is started by
// the desktop manager-factory (apps/desktop/src/main/experimental/);
// this handler only owns the agent / skill / lock rows in the user's
// workspace.
//
// Idempotent: a re-install of an already-present workspace is a no-op
// for the leader agent (looked up by name first), and the lock row
// INSERTs go through ON CONFLICT DO NOTHING.

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

const pythiaSource = "pythia_oracle"

// InstallPythia provisions the pythia_oracle lab into the caller's
// active workspace. Idempotent.
func (h *Handler) InstallPythia(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installPythia: handler not initialized")
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
	// when an issue with lab_source=pythia_oracle gets auto-assigned
	// via assignDefaultLabAgentOnUpdate. The actual `/forecast/issue`
	// oracle work runs in the loopback Python service, which the
	// desktop manager-factory spawns separately.
	agentID, err := upsertPythiaRuntimeAgent(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert pythia_runtime agent: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, pythiaSource, experimental.LockAgent, agentID); err != nil {
		return fmt.Errorf("pythia_runtime agent lock: %w", err)
	}

	// 2. Forward guard (mirrors upsertClaudeScienceVisibility at
	// install_claude_science.go:741). The current ListVisibleAgentsByWorkspace
	// path reads experimental_resource_lock, but a future flag-gated
	// picker filter (filterLabsHiddenByDefault) will read visibility
	// rows. Seed both so a later picker rewrite does not silently
	// re-leak the leader.
	if err := upsertPythiaVisibility(ctx, h, agentID); err != nil {
		return fmt.Errorf("pythia visibility: %w", err)
	}

	// 0.3.35: heal leader agent runtime_id (was empty pre-0.3.35).
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)
	return nil
}

func upsertPythiaRuntimeAgent(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	const name = "pythia_runtime"
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        name,
	}); err == nil {
		return existing.ID, nil
	}
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		slog.Info("upsertPythiaRuntimeAgent: no online local runtime; "+
			"agent created without runtime — daemon auto-assign will skip dispatch until daemon is online",
			"workspace_id", util.UUIDToString(workspaceID))
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               name,
		Description:        "Pythia 引擎 — 多视角推演(Swarm 群体预测 + Osiris 全球情报),通过 multica-pythia 技能暴露给上层 agent。",
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「Pythia 多视角预测引擎」lead agent。当 issue.lab_source 设为 pythia_oracle 时,daemon 会自动派单给你,使用 multica-pythia 技能调用 loopback Python 服务(/forecast/issue 等)输出 10 轮多视角预测贴在 IssueLab 区域。",
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

// upsertPythiaVisibility inserts the forward-guarded
// experimental_resource_visibility row for the pythia_runtime leader
// agent so a future flag-gated picker filter (filterLabsHiddenByDefault)
// hides it from the regular agent picker when pythia_oracle is OFF.
// Idempotent: ON CONFLICT DO NOTHING.
func upsertPythiaVisibility(ctx context.Context, h *Handler, agentID pgtype.UUID) error {
	if h == nil || h.Queries == nil {
		return errors.New("upsertPythiaVisibility: handler not initialized")
	}
	return h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      pythiaSource,
		ResourceType: "agent", // LockAgent (ResourceType) is "agent" in the SQL enum
		ResourceID:   agentID,
	})
}
