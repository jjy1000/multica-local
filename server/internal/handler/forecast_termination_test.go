package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// 0.5.112 termination-closure + monitor pins.
//
// Contract under test:
//   - abortPythiaRunsForIssue flips ONLY the issue's 'running' rows to
//     'aborted' (completed rows untouched) — the closure behind "stop the
//     agent task / cancel / delete the issue ⇒ the in-flight forecast
//     stops with it". Wired at: CancelTask, CancelTaskByUser, issue
//     cancelled (single + batch), DeleteIssue, and both trigger-comment
//     cancel paths.
//   - ListRecentPythiaForecastRuns (the GET /forecast/monitor wire) lists
//     workspace runs newest-first with the issue title joined in — the
//     passive lab monitor page's data source.
func TestPythiaTerminationClosureAndMonitor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}
	ctx := context.Background()

	created := createIssueForTest(t, map[string]any{"title": "pythia-termination-closure"})
	issueUUID := parseUUID(created.ID)
	wsUUID := parseUUID(testWorkspaceID)

	mkRun := func(status string) dbpkg.PythiaForecastRun {
		t.Helper()
		run, err := testHandler.Queries.CreatePythiaForecastRun(ctx, dbpkg.CreatePythiaForecastRunParams{
			WorkspaceID: wsUUID,
			IssueID:     issueUUID,
			Rounds:      2,
			Source:      "synthetic",
			Envelopes:   []byte("[]"),
			RunKind:     "initial",
			Status:      status,
		})
		if err != nil {
			t.Fatalf("CreatePythiaForecastRun(%s): %v", status, err)
		}
		return run
	}

	running := mkRun("running")
	completed := mkRun("completed")
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM pythia_forecast_run WHERE issue_id = $1`, issueUUID)
	})

	t.Run("abort flips only running rows", func(t *testing.T) {
		if n := abortPythiaRunsForIssue(ctx, testHandler, issueUUID); n != 1 {
			t.Fatalf("abortPythiaRunsForIssue signalled %d runs, want 1", n)
		}
		var status string
		if err := testPool.QueryRow(ctx,
			`SELECT status FROM pythia_forecast_run WHERE id = $1`,
			util.UUIDToString(running.ID)).Scan(&status); err != nil {
			t.Fatalf("running row missing: %v", err)
		}
		if status != "aborted" {
			t.Errorf("running row status = %q, want aborted", status)
		}
		if err := testPool.QueryRow(ctx,
			`SELECT status FROM pythia_forecast_run WHERE id = $1`,
			util.UUIDToString(completed.ID)).Scan(&status); err != nil {
			t.Fatalf("completed row missing: %v", err)
		}
		if status != "completed" {
			t.Errorf("completed row status = %q, want completed (abort must not touch terminal rows)", status)
		}
	})

	t.Run("monitor listing joins the issue title", func(t *testing.T) {
		rows, err := testHandler.Queries.ListRecentPythiaForecastRuns(ctx, dbpkg.ListRecentPythiaForecastRunsParams{
			WorkspaceID: wsUUID,
			Limit:       30,
		})
		if err != nil {
			t.Fatalf("ListRecentPythiaForecastRuns: %v", err)
		}
		found := false
		for _, row := range rows {
			if row.ID == running.ID {
				found = true
				if row.IssueTitle != "pythia-termination-closure" {
					t.Errorf("monitor row issue_title = %q, want the joined issue title", row.IssueTitle)
				}
				if row.Status != "aborted" {
					t.Errorf("monitor row status = %q, want aborted", row.Status)
				}
			}
		}
		if !found {
			t.Fatal("monitor listing missing the run row")
		}
	})
}
