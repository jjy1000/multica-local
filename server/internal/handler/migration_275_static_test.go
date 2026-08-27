// 0.5.82 WL2 static pin: migration 275's schema contract is asserted
// textually so TS-free CI gates can verify structure without applying
// the migration (same rationale as migration_273_274_static_test.go,
// whose loadMigration / mustContain helpers this file reuses — same
// package). Pinning here means:
//
//  1. A future rewrite that renames/drops a column or relaxes the
//     provenance CHECK trips the test (forward-only law enforcement).
//  2. The verbatim "timesfm" flag-key literal in the lock-source CHECK
//     widening is pinned — dropping it would break
//     install_timesfm.go's experimental.Claim at install time (23514),
//     which is exactly the class of drift the duplication law exists
//     to catch.
//  3. The down script must stay the symmetric reverse (CHECK restore +
//     DROP TABLE IF EXISTS), never a data-touching "repair".

package handler_test

import (
	"strings"
	"testing"
)

func TestMigration275_CreatesTimesfmForecastRunTable(t *testing.T) {
	sql := loadMigration(t, "275_timesfm_forecast_run.up.sql")

	// Required table + columns per the timesfm_forecast_run schema
	// contract (sibling of pythia_forecast_run, mig 164):
	//   id UUID PK default gen_random_uuid()
	//   workspace_id UUID NOT NULL FK CASCADE
	//   issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE
	//   horizons INT NOT NULL DEFAULT 0
	//   provenance TEXT NOT NULL CHECK IN ('model','seasonal_naive','mixed')
	//   result JSONB NOT NULL
	//   created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	mustContain(t, sql, "CREATE TABLE IF NOT EXISTS timesfm_forecast_run")
	mustContain(t, sql, "id UUID PRIMARY KEY DEFAULT gen_random_uuid()")
	mustContain(t, sql, "workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE")
	mustContain(t, sql, "issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE")
	mustContain(t, sql, "horizons INT NOT NULL DEFAULT 0")
	mustContain(t, sql, "provenance TEXT NOT NULL")
	mustContain(t, sql, "result JSONB NOT NULL")
	mustContain(t, sql, "created_at TIMESTAMPTZ NOT NULL DEFAULT now()")

	// provenance CHECK must allow exactly the engine's provenance set.
	for _, want := range []string{"'model'", "'seasonal_naive'", "'mixed'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 275 provenance CHECK missing %s", want)
		}
	}

	// Dominant read path index: (issue_id, created_at DESC).
	mustContain(t, sql, "CREATE INDEX IF NOT EXISTS idx_timesfm_forecast_run_issue")
	mustContain(t, sql, "ON timesfm_forecast_run (issue_id, created_at DESC)")
}

func TestMigration275_WidensExperimentalSourceCheck(t *testing.T) {
	sql := loadMigration(t, "275_timesfm_forecast_run.up.sql")

	// The lock-source CHECK must gain the verbatim 'timesfm' literal —
	// without it experimental.Claim("timesfm", LockAgent, ...) fails
	// with 23514 on install (the exact failure mig 242 fixed for
	// 'semantica').
	mustContain(t, sql, "DROP CONSTRAINT experimental_resource_lock_experimental_source_check")
	mustContain(t, sql, "ADD CONSTRAINT experimental_resource_lock_experimental_source_check")
	mustContain(t, sql, "'timesfm'::text")
	// Forward-only: prior sources must survive the rewrite.
	for _, want := range []string{"'semantica'::text", "'pythia_oracle'::text", "'swarm_topology'::text"} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 275 CHECK rewrite dropped prior source %s", want)
		}
	}
	// No table/column drops — the only DROP in the up script is the
	// constraint rewrite. (The down script drops the run table; that is
	// its contract, asserted separately.)
	if strings.Contains(strings.ToLower(sql), "drop table") {
		t.Errorf("migration 275 up must not DROP TABLE (forward-only law)")
	}
}

func TestMigration275_DownRestoresCheckAndDropsTable(t *testing.T) {
	sql := loadMigration(t, "275_timesfm_forecast_run.down.sql")

	// Down must be the symmetric reverse of up: CHECK restored without
	// 'timesfm', run table dropped.
	mustContain(t, sql, "DROP CONSTRAINT experimental_resource_lock_experimental_source_check")
	mustContain(t, sql, "'semantica'::text")
	if strings.Contains(sql, "'timesfm'::text") {
		t.Errorf("migration 275 down must restore the pre-275 CHECK (no 'timesfm')")
	}
	mustContain(t, sql, "DROP TABLE IF EXISTS timesfm_forecast_run")
}
