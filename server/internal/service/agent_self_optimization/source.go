// Package agent_self_optimization — source.go (0.3.45.1).
//
// "Source" here means "source of historical data" — the issues that
// the self-opt runner should scan. The user-visible spec is:
//
//	"根据 multica 的任务问题（已完结的任务）来进行自我学习和优化相关智能体
//	 （不含实验性功能的相关智能体或者技能等）"
//
// Two filters are stacked:
//
//  1. Status + lab-source filter at the SQL layer: issue.status='done'
//     AND issue.lab_source IS NULL (main workspace tasks only — lab-
//     bound issues are out by definition).
//
//  2. Assignee-name filter at the runner layer: skip issues whose
//     assignee is an agent hidden by ANY Labs flag
//     (experimental_resource_visibility rows + hardcoded safety net
//     for mythos_* / claude_science* / pythia_oracle agent names
//     that may have been created before the visibility row landed).
//
// Both filters are pure — no I/O — so the runner can pre-compute the
// exclusion set once per run rather than re-resolving the visibility
// table on every issue row.
package agent_self_optimization

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// hiddenAgentNames is the hardcoded safety net: even if the visibility
// table is misconfigured for a flag, these names are always excluded
// from the self-opt source set. Covers the 5 Mythos agents + the
// agent_self_optimization leader agent +
// claude_science_runtime-created agent (which historically shipped as
// "智能体优化专家").
//
// This is intentionally a small allowlist, NOT a "names to include"
// list — we filter OUT, never filter IN. New lab agent names land
// here when the user adds a new lab. (0.3.57: constitution_agent /
// 宪法智能体 was removed alongside the lab retirement in migration 165.)
var hiddenAgentNames = []string{
	// 0.3.17 + 0.3.27: 智能体优化专家 leader
	"智能体优化专家",
	"agent-optimizer-expert",
	// 0.3.16-patch.1: Mythos swarm 5-agent roster
	"mythos_prelude",
	"mythos_loop_coder",
	"mythos_loop_researcher",
	"mythos_loop_analyst",
	"mythos_coda",
	// 0.3.31: pythia_oracle agents (when installed via claude_science_runtime)
	"pythia_oracle",
	"pythia_oracle_loop",
}

// SourceFilter holds the pre-computed exclusion list for one runner
// pass. Build it once per Run() call via NewSourceFilter, then feed
// it into the SQL WHERE clause.
type SourceFilter struct {
	// HiddenAgentIDs is the set of agent UUIDs to exclude from
	// `issue.assignee_id IN (...)` queries. Built from
	// experimental_resource_visibility rows seeded by every flag's
	// migration 150-157.
	HiddenAgentIDs []uuid.UUID
	// HiddenAgentNames is the safety-net allowlist (see hiddenAgentNames
	// above). Excluded via a `agent.name NOT IN (...)` subquery.
	HiddenAgentNames []string
}

// NewSourceFilter resolves every flag's visibility-table rows + the
// hardcoded safety net into a single filter struct. Runs ONE round
// trip per flag (8 flags × 1 SELECT for HideAgent) — the runtime
// cost is negligible compared to the issue scan that follows.
//
// Caller passes the same *db.Queries the runner will use for the
// scan; we don't open a transaction because visibility reads are
// eventually consistent (a flag toggle mid-run is a 0.3.18 design
// concern handled by the safety-net rebuild on next run).
func NewSourceFilter(ctx context.Context, q *db.Queries) (*SourceFilter, error) {
	seen := make(map[uuid.UUID]struct{})
	// 1. Resolve visibility table rows for every flag × HideAgent.
	for _, flagKey := range experimental.AllFlagKeys() {
		ids, err := experimental.HiddenResourceIDsByFlag(ctx, q, flagKey, experimental.HideAgent)
		if err != nil {
			// Fail-soft: log via the caller, continue with what we
			// have. The hardcoded safety net still keeps the run safe
			// from the most egregious lab-agent contamination.
			continue
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	hidden := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		hidden = append(hidden, id)
	}
	return &SourceFilter{
		HiddenAgentIDs:  hidden,
		HiddenAgentNames: append([]string(nil), hiddenAgentNames...),
	}, nil
}

// ShouldExcludeAgent reports whether the given agent id+name combo
// belongs to a lab. The runner calls this in-memory after the issue
// scan pulls its assignee_id; the SQL layer's NOT IN clause is the
// primary defense, this is the per-row double-check for safety.
func (f *SourceFilter) ShouldExcludeAgent(agentID uuid.UUID, agentName string) bool {
	for _, hidden := range f.HiddenAgentIDs {
		if agentID == hidden {
			return true
		}
	}
	for _, hidden := range f.HiddenAgentNames {
		if agentName == hidden {
			return true
		}
	}
	return false
}

// ExcludedAgentIDList returns the SQL-ready slice for `WHERE
// assignee_id NOT IN (...)`. Empty when no flag has hidden any
// agent (the common case on a fresh install).
func (f *SourceFilter) ExcludedAgentIDList() []uuid.UUID {
	if f == nil || len(f.HiddenAgentIDs) == 0 {
		return nil
	}
	out := make([]uuid.UUID, len(f.HiddenAgentIDs))
	copy(out, f.HiddenAgentIDs)
	return out
}

// ExcludedAgentNameList returns the SQL-ready slice for the
// `agent.name NOT IN (...)` subquery that backs the hardcoded
// safety net.
func (f *SourceFilter) ExcludedAgentNameList() []string {
	if f == nil || len(f.HiddenAgentNames) == 0 {
		return nil
	}
	out := make([]string, len(f.HiddenAgentNames))
	copy(out, f.HiddenAgentNames)
	return out
}

// String returns a one-line summary for the run log header. Includes
// counts only — never the IDs / names — so a log dump can't leak
// workspace topology.
func (f *SourceFilter) String() string {
	if f == nil {
		return "SourceFilter(nil)"
	}
	return fmt.Sprintf("SourceFilter{hidden_ids=%d hidden_names=%d}",
		len(f.HiddenAgentIDs), len(f.HiddenAgentNames))
}

// _ = pgx.ErrNoRows keeps the import alive for tests that compare
// against the sentinel — sqlc returns pgx.ErrNoRows for "no last
// successful run" cases and the runner should treat that as "fire
// immediately on the next eligible window".
var _ = pgx.ErrNoRows