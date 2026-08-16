// Package handler — install_code_canvas.go (0.3.54)
//
// code_canvas install handler. Provisions a single `code_canvas_worker`
// leader agent bound to the bundled code-canvas run.sh stub subprocess,
// the `multica-code-canvas` builtin skill (auto-loaded for all agents
// via BuiltinSkills(), and explicitly bound here for testability), and
// the experimental_resource_visibility row. The subprocess itself is
// started by apps/desktop/src/main/experimental/manager-factory.ts;
// this file only owns the agent / skill / lock rows in the user's
// workspace.
//
// 0.3.56 (this rev): pre-0.3.56 the install only created the agent +
// visibility rows. When a `lab_source=code_canvas` issue auto-dispatched
// to code_canvas_worker via assignDefaultLabAgentOnUpdate, the agent
// ran its placeholder instructions and the issue stayed pending forever
// because the agent had no skill body to act on. The fix: also
// provision the multica-code-canvas SKILL.md body into a `skill` row
// and bind it to the agent via agent_skill, so the auto-dispatch path
// lands on an agent with a real prompt body to read.
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
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const codeCanvasSource = "code_canvas"

// codeCanvasBuiltinSkillName is the embedded skill directory under
// server/internal/service/builtin_skills/ that the install handler
// materialises into the workspace + binds to the code_canvas_worker
// leader. Must match the directory name (LoadBuiltinSkillByName keys
// off it) and the manifest's `capabilities.skills[0]` entry.
const codeCanvasBuiltinSkillName = "multica-code-canvas"

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

	// 0.3.56: bind the embedded multica-code-canvas skill body into
	// the workspace + attach it to the leader. Without this, the
	// auto-dispatch path lands on a code_canvas_worker with only the
	// agent's Instructions text and no skill body to act on — the
	// issue stays pending forever. The skill is also auto-loaded for
	// every agent via BuiltinSkills() (loadMainProductSkills walks
	// server/internal/service/builtin_skills/); the explicit
	// agent_skill row here is the testable seam the install handler
	// owns.
	skillID, err := upsertCodeCanvasSkill(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("upsert multica-code-canvas skill: %w", err)
	}
	if err := bindCodeCanvasSkill(ctx, h, agentID, skillID); err != nil {
		return fmt.Errorf("bind multica-code-canvas skill: %w", err)
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
		PermissionMode:    "public_to",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		// 0.3.56: explicit reference to the multica-code-canvas skill
		// so the auto-dispatched agent knows which skill body to read
		// when an issue lands on it. The actual binding is created by
		// upsertCodeCanvasSkill + bindCodeCanvasSkill below; this text
		// is the breadcrumb that tells the runtime to use the skill.
		Instructions: "你是「Code Canvas worker」lead agent。当 issue.lab_source 设为 code_canvas 时,daemon 会自动派单给你。code_canvas 是 P9 内部 pilot,只暴露 /health,使用 `multica-code-canvas` 技能完成 stub lab 的受限操作(查询 flag status、ping subprocess /health、报告 lab 状态;真实研究/预测/编码工作请按技能指引 escalate 到对应 lab)。",
		CustomEnv:    []byte(`{}`),
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

// upsertCodeCanvasSkill materialises the embedded
// `multica-code-canvas` SKILL.md body into the caller's workspace so
// the agent_skill binding below has a row to point at. Re-running the
// install on an already-provisioned workspace is a no-op — the
// find-or-create reuses the existing row (UNIQUE(workspace_id, name)
// from migration 008). Mirrors the shape of upsertClaudeScienceSkill
// in install_claude_science.go but reads the body from the Go
// embedded FS (service.LoadBuiltinSkillByName) instead of a manifest
// asset on disk, because the skill is shipped as part of the binary,
// not as a packaged lab resource.
//
// If the embedded skill is missing the install still succeeds — the
// `agent_skill` row is not created, but BuiltinSkills() at the
// runtime layer auto-loads whatever is in
// server/internal/service/builtin_skills/ regardless, so the agent
// still has a fallback skill body. The test that asserts the binding
// does require the embedded skill, so a missing SKILL.md surfaces as
// a test failure rather than a silent data path regression.
func upsertCodeCanvasSkill(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	if h == nil || h.Queries == nil {
		return pgtype.UUID{}, errors.New("upsertCodeCanvasSkill: handler not initialized")
	}
	if existing, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        codeCanvasBuiltinSkillName,
	}); err == nil {
		return existing.ID, nil
	}
	skill, ok := service.LoadBuiltinSkillByName(codeCanvasBuiltinSkillName)
	if !ok {
		// Soft-fail: log and return ErrNoRows so the caller can decide.
		// Bind step below will skip the row; BuiltinSkills() still
		// exposes whatever is embedded.
		slog.Warn("upsertCodeCanvasSkill: embedded SKILL.md not found; agent_skill row will not be created",
			"name", codeCanvasBuiltinSkillName)
		return pgtype.UUID{}, nil
	}
	created, err := h.Queries.CreateSkill(ctx, db.CreateSkillParams{
		WorkspaceID: workspaceID,
		Name:        skill.Name,
		Description: "Code Canvas 内部 pilot 的 builtin skill —— 通过 /health 探活、查询 flag 状态;真实研究/预测/编码请 escalate 到对应 lab。",
		Content:     skill.Content,
		Config:      []byte(`{}`),
		CreatedBy:   pgtype.UUID{},
	})
	if err != nil {
		// Race / already-exists path — re-read.
		if re, rerr := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        codeCanvasBuiltinSkillName,
		}); rerr == nil {
			return re.ID, nil
		}
		return pgtype.UUID{}, fmt.Errorf("create skill: %w", err)
	}
	// Best-effort: write the embedded skill's supporting files. The
	// current SKILL.md has none, but this keeps the binding
	// forward-compatible with future additions to the directory
	// without touching this function again.
	for _, f := range skill.Files {
		if _, ferr := h.Queries.UpsertSkillFile(ctx, db.UpsertSkillFileParams{
			SkillID: created.ID,
			Path:    f.Path,
			Content: f.Content,
		}); ferr != nil {
			slog.Warn("upsertCodeCanvasSkill: failed to write supporting file",
				"path", f.Path, "err", ferr)
		}
	}
	return created.ID, nil
}

// bindCodeCanvasSkill is the explicit agent_skill binding that makes
// the lab's leader agent pick up the multica-code-canvas skill body
// at claim time. AddAgentSkill uses ON CONFLICT DO NOTHING, so
// re-running the install does not error. A zero UUID (from a missing
// SKILL.md) short-circuits with no error — see upsertCodeCanvasSkill.
func bindCodeCanvasSkill(ctx context.Context, h *Handler, agentID, skillID pgtype.UUID) error {
	if h == nil || h.Queries == nil {
		return errors.New("bindCodeCanvasSkill: handler not initialized")
	}
	if !skillID.Valid {
		return nil
	}
	return h.Queries.AddAgentSkill(ctx, db.AddAgentSkillParams{
		AgentID: agentID,
		SkillID: skillID,
	})
}
