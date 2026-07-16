package experimental

// Visibility helpers for hiding experimental-flag-gated resources
// from list queries + the autopilot scheduler. See
// server/internal/experimental/catalog.go for the flag definitions and
// server/migrations/150_experimental_resource_visibility.up.sql for the
// backing table.
//
// The hard contract (user-approved 2026-07-12 Labs framework):
//
//   - Flag = off (default) must completely bypass the new code path.
//   - Users cannot add/edit/delete visibility rows at runtime; new
//     rows land in the migration that ships the flag.
//   - Labs tab is the only entry point.
//
// To keep the visibility layer cheap, the list-query path is a single
// `WHERE id NOT IN (...)` SQL filter over the resource's own table.
// The autopilot scheduler path is an early `shouldSkipDispatch` short
// circuit — it never opens a tx, never resolves a leader, never
// enqueues work. This matches the existing admission gate style
// (autopilot.go::shouldSkipDispatch).

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// HideableResource enumerates the kinds of domain rows that a Labs
// flag can hide from list queries + the autopilot scheduler. Each
// value MUST match a CHECK constraint value in
// experimental_resource_visibility.resource_type (migration 150).
//
// Note: this is intentionally distinct from lock.go's ResourceType,
// which models "lock a resource from edits". A resource can be both
// hidden from the visible UI (this type) AND locked from edits
// (ResourceType in lock.go) — the two layers serve different
// enforcement points and the dispatch tables must stay separate so the
// API surface for each one stays small (lock.go + visibility.go).
type HideableResource string

const (
	HideAgent     HideableResource = "agent"
	HideAutopilot HideableResource = "autopilot"
	HideSkill     HideableResource = "skill"
	// HideSquad (0.3.31): squad list filters via the visibility table
	// for the first time. Required so mythos_swarm can hide its
	// "Mythos Swarm" squad from the regular squad picker when the
	// flag is off. The CHECK constraint widening lives in
	// server/migrations/157_mythos_dual_mode.up.sql.
	HideSquad HideableResource = "squad"
)

// IsKnownHideableResource reports whether r is one of the supported
// surface types. Use this to validate caller-supplied values before
// hitting the database.
func IsKnownHideableResource(r HideableResource) bool {
	switch r {
	case HideAgent, HideAutopilot, HideSkill, HideSquad:
		return true
	}
	return false
}

// agentSelfOptimizationIDs is the canonical set of resources that the
// 0.3.17 agent_self_optimization flag hides by default. These IDs are
// also seeded in migration 150 — keep the two lists in sync; a
// divergent migration is caught by AgentSelfOptimizationVisibilitySeeded.
//
// Hard-coded as constants (not pulled from the DB at startup) so the
// catalog-only test below doesn't need a live PG connection to assert
// the catalog has the flag.
var agentSelfOptimizationIDs = struct {
	agent     uuid.UUID
	autopilot []uuid.UUID
	skill     uuid.UUID
}{
	agent: uuid.MustParse("6a647967-f56e-4661-ad39-774420b870d4"),
	autopilot: []uuid.UUID{
		uuid.MustParse("f788217e-ef6a-4af0-a5a1-cbf85d8dbb8e"), // 智能体工程师团队 · 每3工作日批量优化
		uuid.MustParse("ab5de2d9-7af9-491d-a42f-2c9f87fbcdf3"), // SkillOpt-Multica · 每日 00:00 自进化循环
	},
	skill: uuid.MustParse("18edfaed-c493-4a61-89b4-8b6bff84d8fc"), // skillopt-multica
}

// AgentSelfOptimizationAgentID returns the 智能体优化专家 agent UUID
// hidden by the agent_self_optimization flag. Used by the autopilot
// scheduler when checking whether to skip a tick — checking against
// the agent_id directly avoids the ListHiddenResourceIDs round-trip
// on every scheduler tick.
func AgentSelfOptimizationAgentID() uuid.UUID {
	return agentSelfOptimizationIDs.agent
}

// AgentSelfOptimizationAutopilotIDs returns the autopilot UUIDs hidden
// by the agent_self_optimization flag. The autopilot scheduler calls
// this once at construction and caches the result — the IDs are
// deployment constants and never change at runtime.
func AgentSelfOptimizationAutopilotIDs() []uuid.UUID {
	out := make([]uuid.UUID, len(agentSelfOptimizationIDs.autopilot))
	copy(out, agentSelfOptimizationIDs.autopilot)
	return out
}

// AgentSelfOptimizationSkillID returns the skillopt-multica Skill UUID
// hidden by the agent_self_optimization flag.
func AgentSelfOptimizationSkillID() uuid.UUID {
	return agentSelfOptimizationIDs.skill
}

// constitutionAgentIDs is the canonical set of resources that the
// 0.3.20 constitution_agent flag hides by default. These IDs are also
// seeded in migration 153 — keep the two lists in sync; a divergent
// migration is caught by ConstitutionAgentVisibilitySeeded.
//
// Hard-coded as constants (not pulled from the DB at startup) so the
// catalog-only test below doesn't need a live PG connection to assert
// the catalog has the flag.
var constitutionAgentIDs = struct {
	agent     uuid.UUID
	autopilot []uuid.UUID
}{
	agent: uuid.MustParse("125890ef-a7a2-4f94-80e2-a4ecb03407b5"), // 宪法智能体
	autopilot: []uuid.UUID{
		uuid.MustParse("eb4f3a30-5604-47c4-919c-29757cf9bfa0"), // CTR 宪章三周评审
		uuid.MustParse("e6bc3a0e-aac3-4b77-8f0f-5e85889369a5"), // CSIL 宪章自优化循环
		uuid.MustParse("b3da8c47-81d0-45ae-94f7-152dc416c6cf"), // TAOL 任务-智能体优化循环
	},
}

// ConstitutionAgentAgentID returns the 宪法智能体 agent UUID hidden
// by the constitution_agent flag. Used by the autopilot scheduler
// when checking whether to skip a tick.
func ConstitutionAgentAgentID() uuid.UUID {
	return constitutionAgentIDs.agent
}

// ConstitutionAgentAutopilotIDs returns the autopilot UUIDs hidden by
// the constitution_agent flag. The autopilot scheduler calls this
// once at construction and caches the result — the IDs are
// deployment constants and never change at runtime.
func ConstitutionAgentAutopilotIDs() []uuid.UUID {
	out := make([]uuid.UUID, len(constitutionAgentIDs.autopilot))
	copy(out, constitutionAgentIDs.autopilot)
	return out
}

// HiddenResourceIDsByFlag returns the set of resource UUIDs hidden by
// the given flag for the given resource type. An empty result means
// nothing is hidden — callers should still pass an empty `NOT IN ()`
// through to the underlying query (Postgres accepts empty arrays).
//
// The query path is intentionally simple: a single SELECT with two
// indexed parameters. We don't cache the result here because List
// handlers run as one-off requests; if a future caller needs it hot
// (the autopilot scheduler, which we handle separately above), cache
// the call site, not this function.
func HiddenResourceIDsByFlag(ctx context.Context, q *db.Queries, flagKey string, r HideableResource) ([]uuid.UUID, error) {
	if !IsKnownHideableResource(r) {
		return nil, fmt.Errorf("unknown hideable resource %q", r)
	}
	rows, err := q.ListHiddenResourceIDs(ctx, db.ListHiddenResourceIDsParams{
		FlagKey:      flagKey,
		ResourceType: string(r),
	})
	if err != nil {
		return nil, fmt.Errorf("list hidden resource ids: %w", err)
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if !row.Valid {
			continue
		}
		out = append(out, uuid.UUID(row.Bytes))
	}
	return out, nil
}

// UUIDsToPgtype converts a slice of uuid.UUID to pgtype.UUID for use
// with sqlc-generated NOT IN ($1::uuid[]) queries. The empty case
// returns a non-nil zero-length slice so the placeholder array still
// binds cleanly.
func UUIDsToPgtype(ids []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = pgtype.UUID{Bytes: id, Valid: true}
	}
	return out
}
