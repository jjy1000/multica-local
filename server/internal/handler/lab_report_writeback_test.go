package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// 0.5.86 issue-delivery batch: pins the lab report writeback
// (postLabRunReportComment) and the migration-282 idempotency marker.
//
// Contract under test:
//   - leader agent present → comment authored by that agent
//     (author_type='agent').
//   - leader agent missing → comment still lands, author_type='system'
//     (all-zero UUID Valid:true — mig 107 convention).
//   - empty content → no comment (zero UUID).
//   - SetPythiaForecastRunReportComment is exactly-once: the
//     report_comment_id IS NULL guard makes re-entry a no-op.
func TestLabReportWriteback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}
	ctx := context.Background()

	// Fixture: leader agent + bound issue.
	const leaderName = "pythia_runtime"
	testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, leaderName)
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":                 leaderName,
		"description":          "0.5.86 report writeback fixture",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgent(%s): expected 201, got %d: %s", leaderName, w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`,
			testWorkspaceID, leaderName)
	})

	created := createIssueForTest(t, map[string]any{"title": "lab-report-writeback"})
	issueUUID := parseUUID(created.ID)
	wsUUID := parseUUID(testWorkspaceID)

	t.Run("leader agent authors the report comment", func(t *testing.T) {
		commentID := postLabRunReportComment(ctx, testHandler, issueUUID, wsUUID, leaderName, "🔮 测试预测报告")
		if !commentID.Valid {
			t.Fatal("expected a valid comment id, got zero UUID")
		}
		var authorType, content string
		err := testPool.QueryRow(ctx,
			`SELECT author_type, content FROM comment WHERE id = $1`,
			util.UUIDToString(commentID)).Scan(&authorType, &content)
		if err != nil {
			t.Fatalf("comment row missing: %v", err)
		}
		if authorType != "agent" {
			t.Errorf("author_type = %q, want agent", authorType)
		}
		if content != "🔮 测试预测报告" {
			t.Errorf("content = %q", content)
		}
	})

	t.Run("missing leader falls back to system author", func(t *testing.T) {
		commentID := postLabRunReportComment(ctx, testHandler, issueUUID, wsUUID, "no_such_leader_agent", "系统回退报告")
		if !commentID.Valid {
			t.Fatal("expected a valid comment id (system-author fallback), got zero UUID")
		}
		var authorType string
		if err := testPool.QueryRow(ctx,
			`SELECT author_type FROM comment WHERE id = $1`,
			util.UUIDToString(commentID)).Scan(&authorType); err != nil {
			t.Fatalf("comment row missing: %v", err)
		}
		if authorType != "system" {
			t.Errorf("author_type = %q, want system", authorType)
		}
	})

	t.Run("empty content is a no-op", func(t *testing.T) {
		commentID := postLabRunReportComment(ctx, testHandler, issueUUID, wsUUID, leaderName, "")
		if commentID.Valid {
			t.Fatal("expected zero UUID for empty content")
		}
	})

	t.Run("report_comment_id marker is exactly-once", func(t *testing.T) {
		run, err := testHandler.Queries.CreatePythiaForecastRun(ctx, dbpkg.CreatePythiaForecastRunParams{
			WorkspaceID: wsUUID,
			IssueID:     issueUUID,
			Rounds:      2,
			Source:      "oracle",
			Envelopes:   []byte("[]"),
		})
		if err != nil {
			t.Fatalf("CreatePythiaForecastRun: %v", err)
		}
		first := parseUUID(created.ID) // any valid marker uuid
		updated, err := testHandler.Queries.SetPythiaForecastRunReportComment(ctx, dbpkg.SetPythiaForecastRunReportCommentParams{
			ID:              run.ID,
			ReportCommentID: first,
		})
		if err != nil {
			t.Fatalf("first SetPythiaForecastRunReportComment: %v", err)
		}
		if !updated.ReportCommentID.Valid || util.UUIDToString(updated.ReportCommentID) != util.UUIDToString(first) {
			t.Fatalf("marker not recorded: %+v", updated.ReportCommentID)
		}
		// Re-entry with a DIFFERENT id must be a no-op — the row keeps
		// the first marker instead of inviting a duplicate comment.
		second := parseUUID(testWorkspaceID)
		reupdated, err := testHandler.Queries.SetPythiaForecastRunReportComment(ctx, dbpkg.SetPythiaForecastRunReportCommentParams{
			ID:              run.ID,
			ReportCommentID: second,
		})
		if err != nil {
			t.Fatalf("second SetPythiaForecastRunReportComment: %v", err)
		}
		if util.UUIDToString(reupdated.ReportCommentID) != util.UUIDToString(first) {
			t.Errorf("marker overwrote: got %s, want %s",
				util.UUIDToString(reupdated.ReportCommentID), util.UUIDToString(first))
		}
	})
}
