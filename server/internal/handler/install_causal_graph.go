// Package handler — install_causal_graph.go (0.5.83 WL3)
//
// causal_graph install handler. Mirrors install_timesfm.go in shape
// (purge-before-seed visibility), provisions the hidden three-agent
// team instead of a single leader:
//
//	causal_graph_curator  — 因果图谱策展人, bound to the
//	                        multica-causal-graph-curator builtin skill
//	                        (tier D: read timeline → extract ≤3 triples
//	                        with rationale → POST suggestions; suggested-
//	                        gated, confidence halved to ≤0.5 server-side).
//	causal_graph_historian — 因果图谱史官, read-only narrative duty
//	                        (label/description context, timeline summaries
//	                        when mentioned; writes nothing in 0.5.83).
//	causal_graph_verifier  — 因果图谱审计员, read-only audit duty (spot-
//	                        checks low-confidence active edges against the
//	                        issue timeline when mentioned; the 0.5.84
//	                        Tier C closure will hand it real work).
//
// None of the three is a dispatch leader: the catalog entry has
// AutoDispatch=false and defaultLabLeaderForKey has NO causal_graph
// case (timesfm precedent), so the daemon never auto-assigns them.
// They surface only when the user mentions them. The nightly evolver
// (service/causal_graph/evolver.go, scheduler JobSpec) and the
// maintenance ticker do the autonomous work in Go — the roadmap's
// "1 autopilot" row is superseded by the JobSpec (documented deviation).
//
// Idempotent: lookup-by-name before insert + purge-before-seed on the
// visibility rows (DeletePluginResourceVisibilityByFlagKey then
// re-insert, the 0.5.78 hardening contract applied to a built-in lab,
// same as install_timesfm.go).
//
// FLAG-KEY DUPLICATION LAW: the literal "causal_graph" below is a
// VERBATIM copy of experimental.SourceCausalGraph (lock.go) and the
// catalog Key (catalog.go) — import cycles forbid sharing the constant.
// Pinned by TestCatalogAutoDispatchContract (catalog_test.go) and the
// migration 279 lock-widen static coverage.

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

const causalGraphSource = "causal_graph"

// Hidden-team agent names. KEEP IN SYNC: these strings are the lookup
// keys for the idempotent upserts below; a rename must revisit every
// existing workspace's agent rows (no migration renames them).
const (
	causalGraphCuratorName   = "causal_graph_curator"
	causalGraphHistorianName = "causal_graph_historian"
	causalGraphVerifierName  = "causal_graph_verifier"
)

// InstallCausalGraph provisions the causal_graph lab's hidden team into
// the caller's active workspace. Idempotent across re-installs.
func (h *Handler) InstallCausalGraph(ctx context.Context, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installCausalGraph: handler not initialized")
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

	// 1. The three hidden agents. No leader is claimed for dispatch —
	// see the file header. The curator is the only one bound to a
	// skill; historian/verifier carry their mandate in Instructions
	// until the 0.5.84 deepening (Tier C closure + claim-time context
	// injection) gives them automated work.
	curatorID, err := upsertCausalGraphAgent(ctx, h, workspaceUUID, causalAgentSpec{
		name:        causalGraphCuratorName,
		description: "因果图谱策展人 — 阅读问题时间线(评论与状态流转),抽取(主体, 关系, 客体)三元组并以 suggested 边提交到因果图谱,置信度上限 0.5,等待用户确认;从不在问题线程发言。",
		instructions: "你是「因果图谱策展人」,隐藏团队 tier D 成员。当被 @ 提及或被要求整理某问题的因果关系时,使用 multica-causal-graph-curator 技能执行循环:读时间窗口(multica issue get / comment list)→ 读既有节点(GET /api/causal-graph/nodes)→ 抽取不超过 3 条三元组(每条必须给出 rationale)→ POST /api/causal-graph/suggestions 提交。" +
			"硬规则:绝不编造时间线中不存在的关系;只产出 suggested 状态的提议(服务端会把置信度强制减半到 ≤0.5);由用户确认后才成为 active 边;从不在 issue 评论线程发言(只读 + 提议)。",
	})
	if err != nil {
		return fmt.Errorf("upsert %s agent: %w", causalGraphCuratorName, err)
	}
	historianID, err := upsertCausalGraphAgent(ctx, h, workspaceUUID, causalAgentSpec{
		name:        causalGraphHistorianName,
		description: "因果图谱史官 — 图谱叙事层维护者:解读节点/边的来源与时间线,回答「这条因果链发生了什么」类问题;0.5.83 为只读职责,不写任何图数据。",
		instructions: "你是「因果图谱史官」,隐藏团队叙事成员。职责(全部只读):当被 @ 提及时,用 GET /api/causal-graph/subgraph 与 /api/causal-graph/path 读取相关链路,结合 issue 时间线输出时间线摘要与叙事解释。" +
			"硬规则:不创建/修改/删除任何节点或边;不确认 suggested 边(那是用户的权力);所有输出只出现在触发你的对话里,不主动写评论。",
	})
	if err != nil {
		return fmt.Errorf("upsert %s agent: %w", causalGraphHistorianName, err)
	}
	verifierID, err := upsertCausalGraphAgent(ctx, h, workspaceUUID, causalAgentSpec{
		name:        causalGraphVerifierName,
		description: "因果图谱审计员 — 抽查低置信度 active 边的证据(回到 issue 时间线核对),输出审计意见;0.5.84 Tier C(Pythia 假设-证据闭环)接入后负责校验 supports/contradicts 边。",
		instructions: "你是「因果图谱审计员」,隐藏团队审计成员。职责:当被 @ 提及时,列出指定范围内 confidence 最低的 active 边(GET /api/causal-graph/edges),对每条回到对应 issue 时间线核对证据,输出审计意见(证据充分 / 依据不足 / 建议降级为 suggested)。" +
			"硬规则:不直接删边或改边(审计只产出意见);确认/拒绝由用户执行;所有输出只出现在触发你的对话里,不主动写评论。",
	})
	if err != nil {
		return fmt.Errorf("upsert %s agent: %w", causalGraphVerifierName, err)
	}

	// 2. Locks. Claim is idempotent per (source, type, id); all three
	// agents attach to the causal_graph source so flag-off Hide covers
	// the whole team in one call.
	for _, id := range []pgtype.UUID{curatorID, historianID, verifierID} {
		if err := experimental.Claim(ctx, h.Queries, causalGraphSource, experimental.LockAgent, id); err != nil {
			return fmt.Errorf("causal_graph agent lock: %w", err)
		}
	}

	// 3. Visibility seed (purge-before-seed). Mirrors
	// reseedTimesfmVisibility: rows tied to a stale agent UUID from a
	// previous install/delete-reseed cycle must never survive
	// alongside the fresh ones.
	if err := reseedCausalGraphVisibility(ctx, h, curatorID, historianID, verifierID); err != nil {
		return fmt.Errorf("causal_graph visibility reseed: %w", err)
	}

	// 0.3.35 heal: agents' runtime_id (was empty pre-0.3.35).
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)
	return nil
}

// causalAgentSpec is the per-agent payload for upsertCausalGraphAgent.
type causalAgentSpec struct {
	name         string
	description  string
	instructions string
}

// upsertCausalGraphAgent creates one hidden-team agent row if absent.
// All three share the timesfm/semantica row shape: local runtime mode,
// workspace visibility, public_to permission (mig 245 convention),
// single-task concurrency, JSON-array custom_args (2026-08-06 audit —
// agent readers unmarshal []string and WARN per read on an object).
func upsertCausalGraphAgent(ctx context.Context, h *Handler, workspaceID pgtype.UUID, spec causalAgentSpec) (pgtype.UUID, error) {
	if existing, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        spec.name,
	}); err == nil {
		return existing.ID, nil
	}
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		slog.Info("upsertCausalGraphAgent: no online local runtime; "+
			"agent created without runtime — mention dispatch will skip until a daemon is online",
			"agent", spec.name,
			"workspace_id", util.UUIDToString(workspaceID))
	}
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               spec.name,
		Description:        spec.description,
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		PermissionMode:     "public_to",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       spec.instructions,
		CustomEnv:          []byte(`{}`),
		CustomArgs:         []byte(`[]`),
		McpConfig:          []byte(`{}`),
		Model:              pgtype.Text{},
		ThinkingLevel:      pgtype.Text{},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return created.ID, nil
}

// reseedCausalGraphVisibility purges every visibility row for the
// causal_graph flag key, then inserts the fresh forward-guard rows for
// the three hidden agents (DeletePluginResourceVisibilityByFlagKey is
// the same query user_plugins.go uses on plugin delete/reseed — the
// 0.5.78 hardening contract).
func reseedCausalGraphVisibility(ctx context.Context, h *Handler, agentIDs ...pgtype.UUID) error {
	if h == nil || h.Queries == nil {
		return errors.New("reseedCausalGraphVisibility: handler not initialized")
	}
	if _, err := h.Queries.DeletePluginResourceVisibilityByFlagKey(ctx, causalGraphSource); err != nil {
		return fmt.Errorf("purge stale visibility rows: %w", err)
	}
	for _, id := range agentIDs {
		if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      causalGraphSource,
			ResourceType: "agent", // LockAgent (ResourceType) is "agent" in the SQL enum
			ResourceID:   id,
		}); err != nil {
			return err
		}
	}
	return nil
}
