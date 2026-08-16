// Package handler — product_agent_creation_expert.go (0.5.6)
//
// 0.5.6 extraction: the `agent_creation_expert` leader agent is a
// product-level resource (boot-provisioned by
// `boot_provision_product_labs.go`). Before 0.5.6, the const
// `AgentCreationExpertName` and the helper `upsertAgentCreationExpert`
// lived in `install_agent_creation_studio.go` (deleted in 0.5.6
// alongside the catalog literal removal). This file is the new
// canonical home for both: the const is referenced from
// `defaultLabLeaderForKey` (legacy `lab_source='agent_creation_studio'`
// resolution) and the helper is referenced from
// `BootProvisionProductLabs` (cold-boot upsert into every workspace).
//
// Rationale for keeping the helper here rather than inlining it into
// `boot_provision_product_labs.go`:
//   - The synthetic-runtime fallback (0.5.5 cold-boot FK fix) and
//     the 0.3.35 online-runtime binding logic are non-trivial
//     (15+ lines each); keeping them in a dedicated file matches
//     the 0.3.35 `upsertClaudeScienceRuntime` shape.
//   - `Handler.EnsureProductAgentForWorkspace` is the lazy fallback
//     used by the 0.3.46 P0#4 leader-rewrite path when a workspace
//     is skipped during boot — it dispatches on the agent name string
//     and needs the same const.

package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AgentCreationExpertName is the studio leader agent name. The leader
// lookup tables in handler/issue.go + service/issue.go mirror this
// constant (0.3.46 P0#4 contract).
const AgentCreationExpertName = "agent_creation_expert"

// upsertAgentCreationExpert finds-or-creates the studio leader agent.
//
// 0.5.5: this helper is called from `boot_provision_product_labs.go`
// at server startup, BEFORE the local daemon has registered a runtime
// row. The previous implementation relied on `resolveWorkspaceOnlineRuntime`
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
		PermissionMode:    "public_to",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       "你是「智能体创建专家」。当用户把任务委托给你时,理解需求并调用技能(multica-lab-builder / multica-creating-agents)创建对应的智能体、技能、团队或自动化工程。创建后把结果、用法和后续维护建议写回任务。",
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
	slog.Info("upsertAgentCreationExpert: product agent provisioned",
		"workspace_id", util.UUIDToString(workspaceID),
		"agent_id", util.UUIDToString(created.ID),
		"runtime_id", util.UUIDToString(runtimeID))
	return created.ID, nil
}
