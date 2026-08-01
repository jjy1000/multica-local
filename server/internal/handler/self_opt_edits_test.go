package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/experimental"
	selfoptsvc "github.com/multica-ai/multica/server/internal/service/agent_self_optimization"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// setupSelfOptEditFixture seeds a skill + a suggested skill edit row, then
// returns the skill id + edit id. The flag pref row is upserted so the
// edit endpoints pass their experimentalFlagEnabled gate.
func setupSelfOptEditFixture(t *testing.T) (skillID, editID string) {
	t.Helper()
	ctx := context.Background()

	// 1. Enable the agent_self_optimization flag for the test user.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO experimental_pref (user_id, flag_key, enabled)
		VALUES ($1, 'agent_self_optimization', true)
		ON CONFLICT (user_id, flag_key) DO UPDATE SET enabled = true
	`, testUserID); err != nil {
		t.Fatalf("enable self-opt flag: %v", err)
	}

	// 2. Create a skill. E'' escape strings so the content newlines are
	// REAL newlines (plain '...' keeps backslash-n as two literal chars).
	var skillIDRaw string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, 'test-skill-opt', 'test skill', E'# Test skill\nBase content.', '{}', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&skillIDRaw); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	// 3. Create a self-opt run row (agent_opt_edit.run_id FK requires it).
	var runIDRaw string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_self_opt_run (workspace_id, status, trigger_kind)
		VALUES ($1, 'done', 'manual')
		RETURNING id
	`, testWorkspaceID).Scan(&runIDRaw); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// 4. Create a suggested ADD edit targeting the skill (0.5.3 shape).
	// E'' escape strings so the \n in after_text is a REAL newline (plain
	// '...' in Postgres keeps backslash-n as two literal chars).
	var editIDRaw string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_opt_edit
			(target_type, target_id, subject_scope, run_id, workspace_id,
			 edit_type, before_text, after_text, rationale, application, accepted)
		VALUES ('skill', $1, 'enroll', $2, $3,
		        'add', '', E'## Usage\nRun with --dry-run.', 'add usage doc',
		        'suggested', false)
		RETURNING id
	`, skillIDRaw, runIDRaw, testWorkspaceID).Scan(&editIDRaw); err != nil {
		t.Fatalf("create suggested edit: %v", err)
	}

	return skillIDRaw, editIDRaw
}

func cleanupSelfOptEditFixture(t *testing.T, skillID, editID string) {
	t.Helper()
	ctx := context.Background()
	if editID != "" {
		testPool.Exec(ctx, `DELETE FROM agent_opt_edit WHERE id = $1`, editID)
	}
	if skillID != "" {
		testPool.Exec(ctx, `DELETE FROM skill WHERE id = $1`, skillID)
	}
	testPool.Exec(ctx, `DELETE FROM agent_self_opt_run WHERE workspace_id = $1`, testWorkspaceID)
}

// TestApplyEditSkillRoundTrip pins the 0.5.3 generalization of the
// human-confirm tier: a suggested edit targeting a SKILL is applied via the
// content-only updater (skill.content updated, agent untouched), and the
// ledger row flips to applied with applied_by=user + a snapshot.
func TestApplyEditSkillRoundTrip(t *testing.T) {
	skillID, editID := setupSelfOptEditFixture(t)
	defer cleanupSelfOptEditFixture(t, skillID, editID)

	// The handler test fixture has no SelfOptService wired; the edit
	// endpoints delegate to it. Wire a real one (it only needs queries).
	prev := testHandler.SelfOptService
	testHandler.SelfOptService = selfoptsvc.NewService(testHandler.Queries)
	defer func() { testHandler.SelfOptService = prev }()

	// Apply the suggested skill edit.
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/experimental/self-opt/edits/"+editID+"/apply", map[string]any{
		"workspace_id": testWorkspaceID,
	})
	req = withURLParam(req, "id", editID)
	testHandler.ApplySelfOptEdit(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("apply edit: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify skill.content was updated + agent instructions untouched.
	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM skill WHERE id = $1`, skillID).Scan(&content); err != nil {
		t.Fatalf("load skill content: %v", err)
	}
	if content != "# Test skill\nBase content.\n## Usage\nRun with --dry-run.\n" {
		t.Fatalf("skill content not updated: %q", content)
	}

	// Verify the ledger row flipped to applied + applied_by=user + snapshot.
	var application, appliedBy string
	var snapshot *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT application, applied_by, instructions_snapshot FROM agent_opt_edit WHERE id = $1`, editID,
	).Scan(&application, &appliedBy, &snapshot); err != nil {
		t.Fatalf("load edit row: %v", err)
	}
	if application != "applied" || appliedBy != "user" {
		t.Fatalf("edit row: application=%s applied_by=%s; want applied/user", application, appliedBy)
	}
	if snapshot == nil || *snapshot != "# Test skill\nBase content." {
		t.Fatalf("edit row snapshot = %v; want pre-edit content", snapshot)
	}

	// Revert it (回退到上一版本) — restores the snapshot.
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/experimental/self-opt/edits/"+editID+"/revert", map[string]any{
		"workspace_id": testWorkspaceID,
	})
	req = withURLParam(req, "id", editID)
	testHandler.RevertSelfOptEdit(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revert edit: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM skill WHERE id = $1`, skillID).Scan(&content); err != nil {
		t.Fatalf("load skill content after revert: %v", err)
	}
	if content != "# Test skill\nBase content." {
		t.Fatalf("skill content not restored: %q", content)
	}
	var app2 string
	if err := testPool.QueryRow(context.Background(),
		`SELECT application FROM agent_opt_edit WHERE id = $1`, editID).Scan(&app2); err != nil {
		t.Fatalf("load edit row after revert: %v", err)
	}
	if app2 != "reverted" {
		t.Fatalf("edit row after revert = %s; want reverted", app2)
	}
}

// TestListSelfOptEditsIncludesTargetType pins the DTO wire shape: the list
// endpoint returns target_type + target_id so the renderer can badge the
// subject kind (0.5.3).
func TestListSelfOptEditsIncludesTargetType(t *testing.T) {
	skillID, editID := setupSelfOptEditFixture(t)
	defer cleanupSelfOptEditFixture(t, skillID, editID)

	prev := testHandler.SelfOptService
	testHandler.SelfOptService = selfoptsvc.NewService(testHandler.Queries)
	defer func() { testHandler.SelfOptService = prev }()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/experimental/self-opt/edits?workspace_id="+testWorkspaceID, nil)
	testHandler.ListSelfOptEdits(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list edits: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Edits []SelfOptEditDTO `json:"edits"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, e := range resp.Edits {
		if e.ID == editID {
			found = true
			if e.TargetType != "skill" || e.TargetID == "" {
				t.Fatalf("edit DTO missing target fields: %+v", e)
			}
			if e.AgentName != "" {
				t.Fatalf("skill edit must not carry agent_name: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("suggested edit %s not in list", editID)
	}
}

// ensure the experimental imports are used even if the fixture helpers
// drift (the gate helper is exercised via the endpoints above).
var _ = experimental.DefaultFor
var _ = db.AgentOptEdit{}
