// Package handler — install_mythos.go
//
// Mythos Swarm install handler. Provisions one lab-runtime, five
// Mythos agents (prelude / three loop members / coda), one squad, and
// the expected experimental_resource_lock rows under
// experimental_source='mythos_swarm', all inside the caller's active
// workspace (0.3.25: no dedicated reserved workspace is created).
//
// Idempotent: re-running the install reuses existing rows and only
// claims lock rows that are missing. The user's existing squads /
// agents are NOT touched — Mythos owns its own roster.

package handler

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const mythosSource = "mythos_swarm"

const mythosRuntimeDaemonID = "mythos-swarm"

// mythosAgentSpec is one row in the synthetic roster the install
// handler provisions. The five agents (prelude / three loop members /
// coda) are named after the OpenMythos RDT architecture stages and
// run as an ordinary Multica squad — this lab provisions a themed
// multi-agent team, not a literal recurrent-transformer runner (there
// is no weight-sharing loop or ACT halting; the OpenMythos convergence
// algorithm is not reimplemented server-side).
type mythosAgentSpec struct {
	Name         string
	Description  string
	Instructions string
}

var mythosAgents = []mythosAgentSpec{
	{
		Name:         "mythos_prelude",
		Description:  "Mythos RDT prelude agent — turns the user's problem into a plan + sub-task decomposition.",
		Instructions: "你是 Mythos 蜂群拓扑的 prelude 智能体。接到用户问题时,把问题拆解成可由 loop 智能体并行处理的子任务,写出执行计划与成功标准,然后交还给 coda 智能体汇总结论。",
	},
	{
		Name:         "mythos_loop_researcher",
		Description:  "Mythos loop member — literature / web research sub-task.",
		Instructions: "你是 Mythos 蜂群的一个 loop 智能体(mythos_loop_researcher)。每次迭代你会拿到 prelude 给的子任务,专注于文献/网络调研任务,产出可在 coda 汇总的具体结论。",
	},
	{
		Name:         "mythos_loop_coder",
		Description:  "Mythos loop member — code / experiment sub-task.",
		Instructions: "你是 Mythos 蜂群的 loop 智能体(mythos_loop_coder)。每次迭代拿到 prelude 给的子任务,聚焦代码、实验与可执行验证,产出可在 coda 汇总的实现思路。",
	},
	{
		Name:         "mythos_loop_analyst",
		Description:  "Mythos loop member — data analysis sub-task.",
		Instructions: "你是 Mythos 蜂群的 loop 智能体(mythos_loop_analyst)。每次迭代拿到 prelude 给的子任务,聚焦数据分析、统计与可视化,产出可在 coda 汇总的分析结论。",
	},
	{
		Name:         "mythos_coda",
		Description:  "Mythos RDT coda agent — synthesizes every loop iteration's answer into a single conclusion.",
		Instructions: "你是 Mythos 蜂群拓扑的 coda 智能体。等所有 loop 迭代结束后,把每轮的输出融合成一段用户能直接采纳的结论,标注未达成共识的点。",
	},
}

// ErrMythosManifestUnavailable is returned when the install path is
// invoked without the necessary install context (rare — Mythos does
// not depend on a manifest file, but we keep the same error surface
// as install_claude_science.go so the HTTP layer can map both to 503).
var ErrMythosManifestUnavailable = errors.New("mythos install: prerequisites missing")

// InstallMythos provisions the Mythos lab. Mirrors the shape of
// InstallClaudeScience so the HTTP layer can dispatch either source
// through the same endpoint family.
//
// userID is the authenticated caller; an empty string is allowed for
// test fixtures and falls back to the workspace's first existing
// member (same leniency as claude_science). workspaceID is the
// caller's active workspace — 0.3.25 reserved-workspace removal: the
// lab writes into it instead of creating a dedicated "mythos-swarm"
// reserved workspace. Isolation is via the lock + visibility tables.
func (h *Handler) InstallMythos(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installMythos: handler not initialized")
	}

	// 1. Resolve the target workspace (caller's active workspace). No
	// reserved workspace is created, and we do NOT Claim the workspace
	// itself — locking a user's own workspace would let a rollback
	// hide it.
	workspaceUUID, err := resolveLabWorkspace(ctx, h, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if userID != "" {
		if err := ensureWorkspaceOwner(ctx, h, workspaceUUID, userID); err != nil {
			return fmt.Errorf("ensure workspace owner: %w", err)
		}
	}

	// 2. Lab runtime. Mirror of upsertClaudeScienceRuntime with a
	// distinct daemon_id so the runtime registry treats the lab as
	// a separate logical runtime (no daemon registers against it).
	runtimeID, err := upsertMythosRuntime(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("lab runtime: %w", err)
	}

	// 3. Five Mythos agents.
	agentIDs := make(map[string]pgtype.UUID, len(mythosAgents))
	for _, ag := range mythosAgents {
		id, err := upsertMythosAgent(ctx, h, workspaceUUID, runtimeID, ag)
		if err != nil {
			return fmt.Errorf("agent %s: %w", ag.Name, err)
		}
		agentIDs[ag.Name] = id
		if err := experimental.Claim(ctx, h.Queries, mythosSource, experimental.LockAgent, id); err != nil {
			return fmt.Errorf("agent lock %s: %w", ag.Name, err)
		}
	}

	// 4. One Mythos squad. squad.creator_id is NOT NULL (migration 084);
	// fall back to the workspace's first existing member when the
	// caller is unauthenticated (mirrors the InstallClaudeScience
	// leniency contract).
	var creatorID pgtype.UUID
	if userID != "" {
		if err := creatorID.Scan(userID); err != nil {
			return fmt.Errorf("invalid creator uuid: %w", err)
		}
	} else {
		members, err := h.Queries.ListMembers(ctx, workspaceUUID)
		if err == nil && len(members) > 0 {
			creatorID = members[0].UserID
		}
	}
	squadID, err := upsertMythosSquad(ctx, h, workspaceUUID, agentIDs, creatorID)
	if err != nil {
		return fmt.Errorf("squad: %w", err)
	}
	if err := experimental.Claim(ctx, h.Queries, mythosSource, experimental.LockSquad, squadID); err != nil {
		return fmt.Errorf("squad lock: %w", err)
	}

	// 0.3.31: seed visibility rows for every mythos-owned agent + the
	// squad itself. Without this the squad picker would leak the
	// "Mythos Swarm" squad (no other flag hides squads today), and
	// the mythos_* agents would still appear in the regular agent
	// picker once the experimental_pref flag was toggled off. The
	// upsertMythosVisibility call uses ON CONFLICT DO NOTHING so
	// re-running the install is idempotent.
	if err := upsertMythosVisibility(ctx, h, agentIDs, squadID); err != nil {
		return fmt.Errorf("mythos visibility: %w", err)
	}

	return nil
}

// upsertMythosVisibility inserts experimental_resource_visibility rows
// for the 5 mythos_* agents + the Mythos Swarm squad so the regular
// agent / squad pickers hide them when the mythos_swarm flag is off.
//
// We do NOT seed agent_self_optimization-style rows in mig 157
// directly because the agent UUIDs are runtime-derived at install
// time (the install handler is the only writer of the underlying
// agent rows). The squad row IS seeded by the migration if a known
// stable squad UUID is provided — but we do not have one yet (the
// install handler upserts by name, not UUID), so the install path
// is the single source of truth for both rows.
//
// Idempotent: re-running inserts the same (flag_key, resource_type,
// resource_id) triple is a no-op thanks to the table UNIQUE
// constraint + ON CONFLICT DO NOTHING.
func upsertMythosVisibility(ctx context.Context, h *Handler, agentIDs map[string]pgtype.UUID, squadID pgtype.UUID) error {
	if h == nil || h.Queries == nil {
		return errors.New("upsertMythosVisibility: handler not initialized")
	}
	for _, name := range []string{
		"mythos_prelude",
		"mythos_loop_researcher",
		"mythos_loop_coder",
		"mythos_loop_analyst",
		"mythos_coda",
	} {
		id, ok := agentIDs[name]
		if !ok {
			continue
		}
		if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      mythosSource,
			ResourceType: "agent",
			ResourceID:   id,
		}); err != nil {
			return fmt.Errorf("agent %s visibility: %w", name, err)
		}
	}
	if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
		FlagKey:      mythosSource,
		ResourceType: "squad",
		ResourceID:   squadID,
	}); err != nil {
		return fmt.Errorf("squad visibility: %w", err)
	}
	return nil
}

func upsertMythosRuntime(ctx context.Context, h *Handler, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	created, err := h.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: mythosRuntimeDaemonID, Valid: true},
		Name:        "Mythos Swarm Lab Runtime",
		RuntimeMode: "local",
		Provider:    "mythos",
		// "offline": no real daemon registers against this synthetic
		// runtime (mirrors upsertClaudeScienceRuntime). The lab's
		// agents run through the normal local agent runtime; this row
		// only exists to satisfy agent.runtime_id NOT NULL (migration
		// 004). Reporting "online" would falsely imply a live daemon.
		Status:     "offline",
		DeviceInfo: "",
		Metadata:   []byte(`{}`),
		OwnerID:    pgtype.UUID{},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return created.ID, nil
}

func upsertMythosAgent(
	ctx context.Context, h *Handler,
	workspaceID, runtimeID pgtype.UUID, ag mythosAgentSpec,
) (pgtype.UUID, error) {
	id, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        ag.Name,
	})
	if err == nil {
		return id.ID, nil
	}
	created, cerr := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               ag.Name,
		Description:        ag.Description,
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       ag.Instructions,
		CustomEnv:          []byte(`{}`),
		CustomArgs:         []byte(`{}`),
		McpConfig:          []byte(`{}`),
		Model:              pgtype.Text{},
		ThinkingLevel:      pgtype.Text{},
	})
	if cerr != nil {
		if re, rerr := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        ag.Name,
		}); rerr == nil {
			return re.ID, nil
		}
		return pgtype.UUID{}, cerr
	}
	return created.ID, nil
}

func upsertMythosSquad(
	ctx context.Context, h *Handler,
	workspaceID pgtype.UUID,
	agents map[string]pgtype.UUID,
	creatorID pgtype.UUID,
) (pgtype.UUID, error) {
	squadName := "Mythos Swarm"
	existing, err := h.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        squadName,
	})
	if err == nil {
		return existing.ID, nil
	}
	leader, ok := agents["mythos_prelude"]
	if !ok {
		return pgtype.UUID{}, errors.New("mythos_prelude agent missing")
	}
	created, cerr := h.Queries.CreateSquad(ctx, db.CreateSquadParams{
		WorkspaceID: workspaceID,
		Name:        squadName,
		Description: "Mythos 蜂群主 squad(prelude leader + 3 loop members + coda)。",
		LeaderID:    leader,
		CreatorID:   creatorID,
		AvatarUrl:   pgtype.Text{},
	})
	if cerr != nil {
		if re, rerr := h.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        squadName,
		}); rerr == nil {
			return re.ID, nil
		}
		return pgtype.UUID{}, cerr
	}

	// Attach loop + coda members as squad_members. Leader is set via
	// squad.leader_id above. We only attach successfully-created
	// agents (the map only contains entries that got past
	// upsertMythosAgent).
	memberNames := []string{"mythos_loop_researcher", "mythos_loop_coder", "mythos_loop_analyst", "mythos_coda"}
	for _, n := range memberNames {
		id, ok := agents[n]
		if !ok {
			continue
		}
		if sm, err := h.Queries.AddSquadMember(ctx, db.AddSquadMemberParams{
			SquadID:    created.ID,
			MemberType: "agent",
			MemberID:   id,
			Role:       n,
		}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("attach member %s: %w", n, err)
		} else {
			_ = sm
		}
	}
	return created.ID, nil
}

// keep os import alive even though we don't currently use it.
var _ = os.Getenv
