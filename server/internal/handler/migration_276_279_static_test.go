// 0.5.83 WL3 static pin: migrations 276-279 (issue causal graph) have
// their schema contract asserted textually so TS-free CI gates can
// verify structure without applying the migration (same rationale as
// migration_275_static_test.go, whose loadMigration / mustContain
// helpers this file reuses — same package). Pinning here means:
//
//  1. A future rewrite that renames a column or relaxes a CHECK trips
//     the test (forward-only law enforcement).
//  2. The verbatim "causal_graph" flag-key literal in the lock-source
//     CHECK widening is pinned — dropping it would break a later
//     phase's experimental.Claim("causal_graph", ...) at install time
//     (SQLSTATE 23514), the exact failure class mig 275's static test
//     guards for "timesfm".
//  3. The typed node/edge CHECK lists are CONTRACT: the client zod
//     schemas and docs pin them verbatim, so any drift here breaks
//     the wire contract, not just the schema.
//  4. The partial unique indices (active-edge triple, provenance
//     dedup_key) are load-bearing for recorder idempotency and the
//     curation gate — they must survive refactors.
//  5. The down scripts stay the symmetric reverse, never a
//     data-touching "repair".

package handler_test

import (
	"strings"
	"testing"
)

func TestMigration276_IssueDependencyReviveIsAdditive(t *testing.T) {
	sql := loadMigration(t, "276_issue_dependency_revive.up.sql")

	// The four revived columns, additive only.
	mustContain(t, sql, "ALTER TABLE issue_dependency")
	mustContain(t, sql, "ADD COLUMN evidence_comment_id UUID NULL REFERENCES comment(id) ON DELETE SET NULL")
	mustContain(t, sql, "ADD COLUMN created_by TEXT NULL DEFAULT 'user'")
	mustContain(t, sql, "ADD COLUMN created_at TIMESTAMPTZ NULL DEFAULT now()")
	mustContain(t, sql, "ADD COLUMN updated_at TIMESTAMPTZ NULL DEFAULT now()")

	// created_by CHECK set is contract: user | agent | system, as a
	// NAMED constraint (the down migration drops it by name).
	mustContain(t, sql, "issue_dependency_created_by_check")
	for _, want := range []string{"'user'", "'agent'", "'system'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 276 created_by CHECK missing %s", want)
		}
	}

	// Forward-only: no drops, no column alterations, no type changes.
	lower := strings.ToLower(sql)
	if strings.Contains(lower, "drop column") || strings.Contains(lower, "alter column") ||
		strings.Contains(lower, "drop table") {
		t.Errorf("migration 276 up must be purely additive (forward-only law): %s", sql)
	}
}

func TestMigration276_DownIsSymmetricReverse(t *testing.T) {
	sql := loadMigration(t, "276_issue_dependency_revive.down.sql")

	mustContain(t, sql, "DROP CONSTRAINT IF EXISTS issue_dependency_created_by_check")
	mustContain(t, sql, "DROP COLUMN IF EXISTS evidence_comment_id")
	mustContain(t, sql, "DROP COLUMN IF EXISTS created_by")
	mustContain(t, sql, "DROP COLUMN IF EXISTS created_at")
	mustContain(t, sql, "DROP COLUMN IF EXISTS updated_at")
}

func TestMigration277_CausalNodeTable(t *testing.T) {
	sql := loadMigration(t, "277_causal_node.up.sql")

	mustContain(t, sql, "CREATE TABLE IF NOT EXISTS causal_node")
	mustContain(t, sql, "id UUID PRIMARY KEY DEFAULT gen_random_uuid()")
	mustContain(t, sql, "workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE")
	// Abstract nodes allowed: issue_id NULLable.
	mustContain(t, sql, "issue_id UUID NULL REFERENCES issue(id) ON DELETE CASCADE")
	mustContain(t, sql, "label TEXT NOT NULL")
	mustContain(t, sql, "metadata JSONB NOT NULL DEFAULT '{}'")
	mustContain(t, sql, "provenance JSONB NOT NULL DEFAULT '{}'")
	mustContain(t, sql, "lab_source TEXT NULL")
	mustContain(t, sql, "lab_run_id UUID NULL")
	mustContain(t, sql, "status TEXT NOT NULL DEFAULT 'active'")
	mustContain(t, sql, "last_observed_at TIMESTAMPTZ NOT NULL DEFAULT now()")

	// Node type CHECK list is CONTRACT — pinned verbatim.
	for _, want := range []string{"'decision'", "'action'", "'outcome'", "'assumption'", "'evidence'", "'constraint'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 277 causal_node type CHECK missing %s", want)
		}
	}
}

func TestMigration277_DownDropsCausalNode(t *testing.T) {
	sql := loadMigration(t, "277_causal_node.down.sql")
	mustContain(t, sql, "DROP TABLE IF EXISTS causal_node")
}

func TestMigration278_CausalEdgeTable(t *testing.T) {
	sql := loadMigration(t, "278_causal_edge.up.sql")

	mustContain(t, sql, "CREATE TABLE IF NOT EXISTS causal_edge")
	mustContain(t, sql, "id UUID PRIMARY KEY DEFAULT gen_random_uuid()")
	mustContain(t, sql, "workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE")
	mustContain(t, sql, "from_node_id UUID NOT NULL REFERENCES causal_node(id) ON DELETE CASCADE")
	mustContain(t, sql, "to_node_id UUID NOT NULL REFERENCES causal_node(id) ON DELETE CASCADE")
	mustContain(t, sql, "weight NUMERIC(4,3) NOT NULL DEFAULT 1.0")
	mustContain(t, sql, "confidence NUMERIC(4,3) NULL")
	mustContain(t, sql, "provenance JSONB NOT NULL DEFAULT '{}'")

	// Edge type CHECK list is CONTRACT — pinned verbatim.
	for _, want := range []string{"'causes'", "'supports'", "'contradicts'", "'depends_on'", "'enables'", "'blocks'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 278 causal_edge type CHECK missing %s", want)
		}
	}

	// Curation-gate columns: proposed_by CHECK + status CHECK.
	mustContain(t, sql, "'curator'")
	mustContain(t, sql, "'evolver'")
	mustContain(t, sql, "status TEXT NOT NULL DEFAULT 'active'")
	mustContain(t, sql, "'suggested'")

	// The partial unique index: one ACTIVE edge per (from, to, type),
	// suggested rows excluded.
	mustContain(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS uq_causal_edge_active_from_to_type")
	mustContain(t, sql, "ON causal_edge (from_node_id, to_node_id, type)")
	mustContain(t, sql, "WHERE status = 'active'")
}

func TestMigration278_DownDropsIndexAndTable(t *testing.T) {
	sql := loadMigration(t, "278_causal_edge.down.sql")
	mustContain(t, sql, "DROP INDEX IF EXISTS uq_causal_edge_active_from_to_type")
	mustContain(t, sql, "DROP TABLE IF EXISTS causal_edge")
}

func TestMigration279_IndicesDedupAnchorAndLockWiden(t *testing.T) {
	sql := loadMigration(t, "279_causal_graph_indices_and_lock_widen.up.sql")

	// Read-path indices.
	mustContain(t, sql, "CREATE INDEX IF NOT EXISTS idx_causal_node_workspace")
	mustContain(t, sql, "ON causal_node (workspace_id)")
	mustContain(t, sql, "CREATE INDEX IF NOT EXISTS idx_causal_node_issue")
	mustContain(t, sql, "ON causal_node (issue_id)")
	mustContain(t, sql, "CREATE INDEX IF NOT EXISTS idx_causal_edge_workspace")
	mustContain(t, sql, "ON causal_edge (workspace_id)")
	mustContain(t, sql, "CREATE INDEX IF NOT EXISTS idx_causal_edge_from_to")
	mustContain(t, sql, "ON causal_edge (from_node_id, to_node_id)")
	mustContain(t, sql, "CREATE INDEX IF NOT EXISTS idx_causal_edge_suggested")
	mustContain(t, sql, "ON causal_edge (status)")
	mustContain(t, sql, "WHERE status = 'suggested'")

	// Tier A/B dedup anchor: partial unique index on the provenance
	// dedup_key expression. Recorder idempotency relies on this.
	mustContain(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS uq_causal_node_provenance_dedup_key")
	mustContain(t, sql, "ON causal_node (workspace_id, (provenance->>'dedup_key'))")
	mustContain(t, sql, "WHERE provenance->>'dedup_key' IS NOT NULL")

	// The 0.5.82 lesson: widen the lock-source CHECK NOW so a later
	// phase's Claim("causal_graph") never hangs on 23514.
	mustContain(t, sql, "DROP CONSTRAINT experimental_resource_lock_experimental_source_check")
	mustContain(t, sql, "ADD CONSTRAINT experimental_resource_lock_experimental_source_check")
	mustContain(t, sql, "'causal_graph'::text")
	// Forward-only: prior sources must survive the rewrite.
	for _, want := range []string{"'timesfm'::text", "'semantica'::text", "'pythia_oracle'::text", "'swarm_topology'::text"} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 279 CHECK rewrite dropped prior source %s", want)
		}
	}
}

func TestMigration279_DownRestoresCheckAndDropsIndices(t *testing.T) {
	sql := loadMigration(t, "279_causal_graph_indices_and_lock_widen.down.sql")

	mustContain(t, sql, "DROP CONSTRAINT experimental_resource_lock_experimental_source_check")
	mustContain(t, sql, "'semantica'::text")
	if strings.Contains(sql, "'causal_graph'::text") {
		t.Errorf("migration 279 down must restore the pre-279 CHECK (no 'causal_graph')")
	}
	mustContain(t, sql, "DROP INDEX IF EXISTS uq_causal_node_provenance_dedup_key")
	mustContain(t, sql, "DROP INDEX IF EXISTS idx_causal_edge_suggested")
}
