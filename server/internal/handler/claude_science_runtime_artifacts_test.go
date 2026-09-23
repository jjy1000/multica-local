package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// 0.5.114 issue-first embed: the by-issue artifact listing. Pins (a)
// the sqlc INSERT actually writing the mig-156 issue_id column (it
// shipped dead — no writer until now) and (b) the workspace-scoped
// read the embed leans on. Skip semantics follow the shared harness
// (testPool is nil without DATABASE_URL).
//
// issue.number defaults to 0 per workspace (mig 020) and
// mustCreateTestLabIssue relies on that default, so this test keeps
// exactly one default-numbered issue and creates its "bare" control
// issue with an explicit high number to respect
// uq_issue_workspace_number.
func TestClaudeScienceRuntimeArtifacts_ByIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test harness unavailable (needs DATABASE_URL)")
	}
	wsUUID := mustParseUUID(t, testWorkspaceID)
	ctx := context.Background()

	// Self-heal a prior crashed run: the default-numbered fixture issue
	// is the only row this test owns, and a run that died before its
	// cleanup would otherwise collide with the recreate below.
	if _, err := testPool.Exec(ctx,
		`DELETE FROM issue WHERE workspace_id = $1 AND title = $2`,
		wsUUID, "lab-test-"+t.Name()); err != nil {
		t.Fatalf("pre-clean fixture issue: %v", err)
	}

	memberID := mustCreateTestMember(t, wsUUID)
	agentID := mustCreateTestAgent(t, wsUUID, "artifact-test-agent-"+t.Name(), memberID)
	issueID := mustCreateTestLabIssue(t, wsUUID, memberID, agentID)
	var bareIssueID pgtype.UUID
	err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_id, creator_type, title, status, lab_source, assignee_id, assignee_type, number)
		VALUES ($1, $2, 'member', $3, 'todo', 'claude_science_lab', $4, 'agent', 987654)
		RETURNING id`,
		wsUUID, memberID, "artifact-bare-"+t.Name(), agentID).Scan(&bareIssueID)
	if err != nil {
		t.Fatalf("create bare issue: %v", err)
	}

	var sessionID pgtype.UUID
	err = testPool.QueryRow(ctx, `
		INSERT INTO experimental_claude_runtime_session (workspace_id, agent_id, issue_id, language, code)
		VALUES ($1, $2, $3, 'python', 'print(1)')
		RETURNING id`, wsUUID, agentID, issueID).Scan(&sessionID)
	if err != nil {
		t.Fatalf("insert runtime session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM experimental_runtime_artifact WHERE session_id = $1`, sessionID)
		_, _ = testPool.Exec(ctx, `DELETE FROM experimental_claude_runtime_session WHERE id = $1`, sessionID)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id IN ($1, $2)`, issueID, bareIssueID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID)
	})

	insertArtifact := func(issue pgtype.UUID, name string) {
		t.Helper()
		_, err := testHandler.Queries.InsertExperimentalRuntimeArtifact(ctx, db.InsertExperimentalRuntimeArtifactParams{
			SessionID:   sessionID,
			WorkspaceID: wsUUID,
			IssueID:     issue,
			Name:        name,
			Kind:        "png",
			Bytes:       12,
			Sha256:      "deadbeef",
			Path:        "runs/s1/" + name,
		})
		if err != nil {
			t.Fatalf("insert artifact %q: %v", name, err)
		}
	}
	insertArtifact(issueID, "figure-1.png")
	insertArtifact(issueID, "report.csv")

	listByIssue := func(issue pgtype.UUID) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := withChiURLParam(
			newRequestAs(util.UUIDToString(memberID), "GET",
				"/api/experimental/claude-science-runtime/issues/"+util.UUIDToString(issue)+"/artifacts?workspace_id="+testWorkspaceID, nil),
			"issueID", util.UUIDToString(issue),
		)
		testHandler.ListClaudeScienceRuntimeArtifactsByIssue(rec, req)
		return rec
	}

	rec := listByIssue(issueID)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp runtimeArtifactsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nbody: %s", err, rec.Body.String())
	}
	if resp.Total != 2 || len(resp.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got total=%d len=%d", resp.Total, len(resp.Artifacts))
	}
	names := map[string]bool{}
	for _, a := range resp.Artifacts {
		names[a.Name] = true
		if a.URL == "" || a.SHA256 != "deadbeef" {
			t.Errorf("stub incomplete for %q: url=%q sha=%q", a.Name, a.URL, a.SHA256)
		}
	}
	if !names["figure-1.png"] || !names["report.csv"] {
		t.Errorf("artifact names mismatch: %v", names)
	}

	// an issue with no sandbox sessions lists empty — the embed's
	// "no artifacts yet" path.
	rec2 := listByIssue(bareIssueID)
	if rec2.Code != http.StatusOK {
		t.Fatalf("bare issue: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp2 runtimeArtifactsResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal bare response: %v", err)
	}
	if resp2.Total != 0 || len(resp2.Artifacts) != 0 {
		t.Fatalf("bare issue must list 0 artifacts, got total=%d", resp2.Total)
	}

	// missing workspace_id is a 400, mirroring the context endpoint.
	rec3 := httptest.NewRecorder()
	req3 := withChiURLParam(
		newRequestAs(util.UUIDToString(memberID), "GET",
			"/api/experimental/claude-science-runtime/issues/"+util.UUIDToString(issueID)+"/artifacts", nil),
		"issueID", util.UUIDToString(issueID),
	)
	testHandler.ListClaudeScienceRuntimeArtifactsByIssue(rec3, req3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("missing workspace_id: expected 400, got %d", rec3.Code)
	}
}
