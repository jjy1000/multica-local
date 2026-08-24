// 0.5.70 audit regression pin: migration 273 + 274 have zero
// test coverage per the 0.5.67 audit (Verifier report). The
// audit specifically called out that 274 down is `SELECT 1;` —
// irreversible — and that running `migrate down 274` would
// silently succeed without rolling back data. A static check
// pins the migration SQL invariants so:
//   1. A future migration rewrite that changes 273's schema gets
//      caught (test would assert the new shape matches the
//      documented contract).
//   2. A future "fix" to 274 down that adds real rollback logic
//      gets caught (test would assert the down script stays as
//      `SELECT 1;` — the by-design irreversible contract).
//
// This is a static-only test (no live PG) — the right thing to add
// would be a real-DB integration test, but those need a test
// container / ephemeral PG setup that's out of scope for this
// commit. Per the 0.5.70 audit doc, that's a deferred follow-up.

package handler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadMigration reads the migration SQL file. Tests in this
// package run from `server/internal/handler` (the package dir).
// Migrations live at `server/migrations/<file>`. We walk up
// two levels to land at `server/`.
func loadMigration(t *testing.T, file string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	serverRoot := filepath.Join(cwd, "..", "..")
	target := filepath.Clean(filepath.Join(serverRoot, "migrations", file))
	data, err := os.ReadFile(target)
	if err != nil {
		alt := filepath.Join(cwd, "server", "migrations", file)
		if data2, err2 := os.ReadFile(alt); err2 == nil {
			return string(data2)
		}
		t.Fatalf("could not read migration file %q from %q (or %q): %v", file, target, alt, err)
	}
	return string(data)
}

func TestMigration273_CreatesSemanticaDecisionACLTable(t *testing.T) {
	sql := loadMigration(t, "273_semantica_local_decision_acl.up.sql")

	// Required table + columns per the semantica ACL schema contract.
	// The actual schema (see server/migrations/273_*.up.sql):
	//   CREATE TABLE semantica_local_decision_acl (
	//     decision_id  TEXT PRIMARY KEY,    -- upstream semantica.decisions id; == "multica_<issue_uuid>"
	//     workspace_id UUID NOT NULL,
	//     actor_type   TEXT NOT NULL CHECK (actor_type IN ('system','user','agent','team')),
	//     actor_id     TEXT NOT NULL,
	//     created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	//     visibility   TEXT NOT NULL CHECK (visibility IN ('team','individual_private','shared_team'))
	//   );
	mustContain(t, sql, "CREATE TABLE semantica_local_decision_acl")
	mustContain(t, sql, "decision_id")
	mustContain(t, sql, "TEXT PRIMARY KEY")
	mustContain(t, sql, "workspace_id")
	mustContain(t, sql, "UUID NOT NULL")
	mustContain(t, sql, "actor_type")
	mustContain(t, sql, "actor_id")
	mustContain(t, sql, "created_at")
	mustContain(t, sql, "visibility")
	// actor_type CHECK must allow only the documented values.
	if !strings.Contains(sql, "'system'") ||
		!strings.Contains(sql, "'user'") ||
		!strings.Contains(sql, "'agent'") ||
		!strings.Contains(sql, "'team'") {
		t.Errorf("migration 273 actor_type CHECK missing one of system/user/agent/team")
	}
	// visibility CHECK must allow team / individual_private / shared_team.
	if !strings.Contains(sql, "'team'") ||
		!strings.Contains(sql, "individual_private") ||
		!strings.Contains(sql, "shared_team") {
		t.Errorf("migration 273 visibility CHECK missing team/individual_private/shared_team")
	}
	// Indexes: workspace_id, (workspace_id, actor_type, actor_id),
	// (workspace_id, visibility) — used by ACL filter queries.
	mustContain(t, sql, "CREATE INDEX semantica_local_acl_ws")
	mustContain(t, sql, "CREATE INDEX semantica_local_acl_actor")
	mustContain(t, sql, "CREATE INDEX semantica_local_acl_visibility")
}

func TestMigration273_DownDropsSemanticaDecisionACLTable(t *testing.T) {
	sql := loadMigration(t, "273_semantica_local_decision_acl.down.sql")

	// Down must be the symmetric reverse of up.
	mustContain(t, sql, "DROP TABLE IF EXISTS semantica_local_decision_acl")
}

func TestMigration274_DeletesOrphanResourceLocks(t *testing.T) {
	sql := loadMigration(t, "274_cleanup_orphan_experimental_resources.up.sql")

	// Must hit both lock + visibility orphan tables.
	mustContain(t, sql, "experimental_resource_lock")
	mustContain(t, sql, "experimental_resource_visibility")
	// Must use NOT EXISTS to avoid touching rows whose resource
	// still exists (the migration is a no-op on healthy DBs).
	if !strings.Contains(sql, "NOT EXISTS") {
		t.Errorf("migration 274 up should use NOT EXISTS guard; otherwise " +
			"the migration would destroy real resource bindings")
	}
}

func TestMigration274_DownIsIrreversibleByDesign(t *testing.T) {
	sql := loadMigration(t, "274_cleanup_orphan_experimental_resources.down.sql")

	// 0.5.67 audit: 274 down is intentionally a no-op (the deleted
	// orphan rows cannot be recovered from a snapshot). Pinning
	// this contract so a future "fix" that adds rollback logic
	// doesn't accidentally introduce a fake-restore path that
	// silently produces stale state.
	trimmed := strings.TrimSpace(sql)
	if !strings.HasSuffix(trimmed, "SELECT 1;") {
		t.Errorf("migration 274 down must be `SELECT 1;` (irreversible by design); got: %q", trimmed)
	}
}

// 0.5.70 audit regression pin: migration 241 declares
// swarm_interrupt.payload as NOT NULL. A POST to /runs/{id}/interrupt
// without a payload field used to trip SQLSTATE 23502 (NULL in
// NOT NULL column) and return 500. The handler fix defaults
// missing payload to '{}' so the simple "cancel this run" UX
// (no extra fields) works. Pin the schema contract so a future
// migration that relaxes this (e.g. to NULL) doesn't silently
// re-break the audit-trail invariant.
func TestMigration241_SwarmInterruptPayloadIsNotNull(t *testing.T) {
	sql := loadMigration(t, "241_swarm_topology.up.sql")
	if !strings.Contains(sql, "payload") {
		t.Fatalf("migration 241 should reference payload column")
	}
	notNullPatterns := []string{
		"payload JSONB NOT NULL",
		"payload jsonb NOT NULL",
		"payload NOT NULL",
	}
	found := false
	for _, p := range notNullPatterns {
		if strings.Contains(sql, p) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("migration 241 swarm_interrupt.payload must be NOT NULL " +
			"(the 0.5.70 handler default-to-'{}' fix relies on this)")
	}
}

func mustContain(t *testing.T, sql, fragment string) {
	t.Helper()
	if !strings.Contains(sql, fragment) {
		t.Errorf("migration SQL missing required fragment %q", fragment)
	}
}