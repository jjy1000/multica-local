package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// parseExpectedActiveAgentIDs is the cascade endpoint's input validator.
// Empty list is a valid plan ("no active agents" — cascade just deletes the
// runtime); malformed UUIDs must surface as 400 so a bug in the front-end
// can't silently dilute the plan check.
func TestParseExpectedActiveAgentIDs(t *testing.T) {
	t.Run("empty list returns empty set, ok", func(t *testing.T) {
		got, ok := parseExpectedActiveAgentIDs(nil)
		if !ok {
			t.Fatalf("expected ok for nil input")
		}
		if len(got) != 0 {
			t.Fatalf("expected empty set, got %d entries", len(got))
		}
	})

	t.Run("valid uuids are accepted and deduplicated by set semantics", func(t *testing.T) {
		ids := []string{
			"11111111-1111-1111-1111-111111111111",
			"22222222-2222-2222-2222-222222222222",
			"11111111-1111-1111-1111-111111111111", // dup is intentional
		}
		got, ok := parseExpectedActiveAgentIDs(ids)
		if !ok {
			t.Fatalf("expected ok for valid uuid list")
		}
		if len(got) != 2 {
			t.Fatalf("expected dedup set of 2, got %d", len(got))
		}
		for _, want := range []string{
			"11111111-1111-1111-1111-111111111111",
			"22222222-2222-2222-2222-222222222222",
		} {
			if _, ok := got[want]; !ok {
				t.Fatalf("expected %s in set", want)
			}
		}
	})

	t.Run("any malformed entry fails the whole list", func(t *testing.T) {
		ids := []string{
			"11111111-1111-1111-1111-111111111111",
			"not-a-uuid",
		}
		_, ok := parseExpectedActiveAgentIDs(ids)
		if ok {
			t.Fatal("expected !ok for list containing malformed uuid")
		}
	})
}

// activeAgentSetMatches drives the runtime_delete_plan_changed branch: it
// must report mismatch for any divergence — extra agent, missing agent, or
// substituted agent — and accept order-insensitive set equality.
func TestActiveAgentSetMatches(t *testing.T) {
	mkAgent := func(id string) db.Agent {
		u, err := uuidFromString(id)
		if err != nil {
			t.Fatalf("uuidFromString: %v", err)
		}
		return db.Agent{ID: u}
	}
	a1 := mkAgent("11111111-1111-1111-1111-111111111111")
	a2 := mkAgent("22222222-2222-2222-2222-222222222222")
	a3 := mkAgent("33333333-3333-3333-3333-333333333333")

	t.Run("equal sets match regardless of order", func(t *testing.T) {
		expected := map[string]struct{}{
			"11111111-1111-1111-1111-111111111111": {},
			"22222222-2222-2222-2222-222222222222": {},
		}
		if !activeAgentSetMatches([]db.Agent{a2, a1}, expected) {
			t.Fatal("expected match for set-equal inputs")
		}
	})

	t.Run("missing agent is a mismatch", func(t *testing.T) {
		expected := map[string]struct{}{
			"11111111-1111-1111-1111-111111111111": {},
			"22222222-2222-2222-2222-222222222222": {},
		}
		if activeAgentSetMatches([]db.Agent{a1}, expected) {
			t.Fatal("expected mismatch when an agent disappeared")
		}
	})

	t.Run("extra agent is a mismatch", func(t *testing.T) {
		expected := map[string]struct{}{
			"11111111-1111-1111-1111-111111111111": {},
		}
		if activeAgentSetMatches([]db.Agent{a1, a2}, expected) {
			t.Fatal("expected mismatch when a new agent appeared")
		}
	})

	t.Run("substituted agent is a mismatch", func(t *testing.T) {
		expected := map[string]struct{}{
			"11111111-1111-1111-1111-111111111111": {},
			"22222222-2222-2222-2222-222222222222": {},
		}
		if activeAgentSetMatches([]db.Agent{a1, a3}, expected) {
			t.Fatal("expected mismatch when one agent was swapped for another")
		}
	})

	t.Run("both empty matches", func(t *testing.T) {
		if !activeAgentSetMatches(nil, map[string]struct{}{}) {
			t.Fatal("expected empty/empty to match")
		}
	})
}

func uuidFromString(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

// TestDeleteAgentRuntime_StructuredConflict covers the new 409 shape: the
// strict DELETE refuses with `runtime_has_active_agents` and the body carries
// the live active-agent list so the front-end can pivot to the cascade dialog
// without a second round-trip.
func TestDeleteAgentRuntime_StructuredConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createCascadeFixtureRuntime(t, ctx, "Cascade 409 Runtime")
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Cascade 409 Agent")
	_ = agentID

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/"+runtimeID, nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.DeleteAgentRuntime(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Error        string          `json:"error"`
		Code         string          `json:"code"`
		ActiveAgents []AgentResponse `json:"active_agents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "runtime_has_active_agents" {
		t.Fatalf("expected code runtime_has_active_agents, got %q", body.Code)
	}
	if len(body.ActiveAgents) != 1 || body.ActiveAgents[0].ID != agentID {
		t.Fatalf("expected one active agent %s, got %+v", agentID, body.ActiveAgents)
	}
}

func TestDeleteAgentRuntime_CustomProfileInstanceRefusesDirectDelete(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID, _ := createProfileBackedRuntime(t, ctx, "Custom Instance Delete Guard")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/"+runtimeID, nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.DeleteAgentRuntime(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "runtime_profile_instance_delete_unsupported" {
		t.Fatalf("expected runtime_profile_instance_delete_unsupported, got %q", body.Code)
	}

	var rtRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&rtRows); err != nil {
		t.Fatalf("count runtime rows: %v", err)
	}
	if rtRows != 1 {
		t.Fatalf("expected custom runtime instance to survive refusal, count=%d", rtRows)
	}
}

// TestArchiveAgentsAndDeleteRuntime_HappyPath exercises the cascade endpoint
// end-to-end: with the correct expected_active_agent_ids snapshot, it must
// archive the active agent and tombstone the runtime. The pre-0.5.128 flow
// hard-deleted the archived agents + the runtime row — the "误删后重加回来
// 智能体消失" incident — and an earlier version of this very test pinned
// that destruction as its happy path; the pin was rewritten to the tombstone
// contract: row survives (hidden from lists via metadata.deleted_at), agent
// survives archived with its recorded id in the tombstone, task_usage_daily
// history untouched.
func TestArchiveAgentsAndDeleteRuntime_HappyPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createCascadeFixtureRuntime(t, ctx, "Cascade Happy Runtime")
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Cascade Happy Agent")
	createCascadeFixtureUsage(t, ctx, runtimeID, agentID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/"+runtimeID+"/archive-agents-and-delete",
		map[string]any{"expected_active_agent_ids": []string{agentID}})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ArchiveAgentsAndDeleteRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Runtime row must SURVIVE as a tombstone: still present, marked deleted,
	// carrying the archived-agent id list for revival.
	var meta struct {
		DeletedAt               string   `json:"deleted_at"`
		DeletedArchivedAgentIDs []string `json:"deleted_archived_agent_ids"`
	}
	var rawMetadata []byte
	if err := testPool.QueryRow(ctx, `SELECT metadata FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&rawMetadata); err != nil {
		t.Fatalf("runtime row must survive the delete: %v", err)
	}
	if err := json.Unmarshal(rawMetadata, &meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if meta.DeletedAt == "" {
		t.Fatalf("expected metadata.deleted_at tombstone, got %s", rawMetadata)
	}
	if len(meta.DeletedArchivedAgentIDs) != 1 || meta.DeletedArchivedAgentIDs[0] != agentID {
		t.Fatalf("expected tombstone to record [%s], got %v", agentID, meta.DeletedArchivedAgentIDs)
	}

	// The tombstoned row is hidden from the workspace runtime list.
	var listed int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1 AND metadata->>'deleted_at' IS NULL`, runtimeID).Scan(&listed); err != nil {
		t.Fatalf("list filter check: %v", err)
	}
	if listed != 0 {
		t.Fatal("tombstoned runtime must not appear in list queries")
	}

	// Agent must be archived but NOT hard-deleted — the restore path needs
	// the row (and its runtime_id binding) intact.
	var archived bool
	if err := testPool.QueryRow(ctx, `SELECT (archived_at IS NOT NULL) FROM agent WHERE id = $1`, agentID).Scan(&archived); err != nil {
		t.Fatalf("agent row must survive: %v", err)
	}
	if !archived {
		t.Fatal("expected agent archived by the cascade")
	}

	// Usage history must be untouched (the old hard delete CASCADE-dropped it).
	var usage int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_usage_hourly WHERE runtime_id = $1`, runtimeID).Scan(&usage); err != nil {
		t.Fatalf("count usage rows: %v", err)
	}
	if usage != 1 {
		t.Fatalf("expected usage history to survive the delete, found %d rows", usage)
	}
}

func TestArchiveAgentsAndDeleteRuntime_CustomProfileInstanceRefusesDirectDelete(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID, _ := createProfileBackedRuntime(t, ctx, "Custom Instance Cascade Guard")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/"+runtimeID+"/archive-agents-and-delete",
		map[string]any{"expected_active_agent_ids": []string{}})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ArchiveAgentsAndDeleteRuntime(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "runtime_profile_instance_delete_unsupported" {
		t.Fatalf("expected runtime_profile_instance_delete_unsupported, got %q", body.Code)
	}

	var rtRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&rtRows); err != nil {
		t.Fatalf("count runtime rows: %v", err)
	}
	if rtRows != 1 {
		t.Fatalf("expected custom runtime instance to survive refusal, count=%d", rtRows)
	}
}

// TestArchiveAgentsAndDeleteRuntime_PlanChanged proves the dialog-confirm
// race guard: if the user's snapshot of active agents drifts from the live
// set (somebody added or archived an agent while the dialog was open), the
// cascade endpoint must refuse with 409 + runtime_delete_plan_changed and
// surface the new live snapshot so the dialog can re-prompt.
func TestArchiveAgentsAndDeleteRuntime_PlanChanged(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createCascadeFixtureRuntime(t, ctx, "Cascade Drift Runtime")
	agent1 := createCascadeFixtureAgent(t, ctx, runtimeID, "Cascade Drift Agent A")
	agent2 := createCascadeFixtureAgent(t, ctx, runtimeID, "Cascade Drift Agent B")

	// User confirmed only agent1 — but the live set is {agent1, agent2}.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/"+runtimeID+"/archive-agents-and-delete",
		map[string]any{"expected_active_agent_ids": []string{agent1}})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ArchiveAgentsAndDeleteRuntime(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Code         string          `json:"code"`
		ActiveAgents []AgentResponse `json:"active_agents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "runtime_delete_plan_changed" {
		t.Fatalf("expected code runtime_delete_plan_changed, got %q", body.Code)
	}
	if len(body.ActiveAgents) != 2 {
		t.Fatalf("expected 2 active agents in fresh snapshot, got %d", len(body.ActiveAgents))
	}
	// Runtime must still exist — the plan-changed branch is non-destructive.
	var rtRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&rtRows); err != nil {
		t.Fatalf("count runtime rows: %v", err)
	}
	if rtRows != 1 {
		t.Fatalf("expected runtime to survive plan-changed refusal, count=%d", rtRows)
	}

	_ = agent2
}

// createCascadeFixtureRuntime creates a fresh runtime owned by testUserID
// inside testWorkspaceID and registers cleanup. Each cascade test uses its
// own runtime so the destructive paths don't trample the shared fixture.
func createCascadeFixtureRuntime(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, NULL, $2, 'cloud', 'cascade-test', 'online', $3, '{}'::jsonb, $4, now())
		RETURNING id
	`, testWorkspaceID, name, name+" device", testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert cascade fixture runtime: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort cleanup. Tombstoned rows are real rows now, so this
		// matters on failure paths AND after a successful tombstone.
		testPool.Exec(context.Background(), `DELETE FROM task_usage_hourly WHERE runtime_id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE runtime_id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID
}

func createProfileBackedRuntime(t *testing.T, ctx context.Context, name string) (string, string) {
	t.Helper()
	var profileID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO runtime_profile (
			workspace_id, display_name, protocol_family, command_name,
			fixed_args, visibility, created_by, enabled
		)
		VALUES ($1, $2, 'codex', 'custom-codex', '[]'::jsonb, 'workspace', $3, true)
		RETURNING id
	`, testWorkspaceID, name+" Profile", testUserID).Scan(&profileID); err != nil {
		t.Fatalf("insert runtime profile: %v", err)
	}

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, profile_id, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'codex', 'online', $4, '{}'::jsonb, $5, $6, now())
		RETURNING id
	`, testWorkspaceID, "daemon-"+profileID, name, name+" device", testUserID, profileID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert profile-backed runtime: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE runtime_id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM runtime_profile WHERE id = $1`, profileID)
	})
	return runtimeID, profileID
}

func createCascadeFixtureAgent(t *testing.T, ctx context.Context, runtimeID, name string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id
	`, testWorkspaceID, name, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("insert cascade fixture agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

// createCascadeFixtureUsage seeds one task_usage_daily row (the table the
// runtimes-list cost cell and runtime detail read) so the tombstone tests can
// prove usage history survives a runtime delete AND stays attached to the
// revived row. The old hard delete didn't cascade-drop these rows (no FK on
// task_usage_hourly.runtime_id) — it orphaned them: still on disk, but
// permanently detached from the fresh runtime id a re-registration minted.
// The tombstone keeps the row id stable, so the history simply continues.
func createCascadeFixtureUsage(t *testing.T, ctx context.Context, runtimeID, usageAgentID string) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_usage_hourly (bucket_hour, workspace_id, runtime_id, agent_id, provider, model, input_tokens, output_tokens, task_count, event_count)
		VALUES (date_trunc('hour', now()), $1, $2, $3, 'cascade-test', 'test-model', 1000, 500, 1, 3)
	`, testWorkspaceID, runtimeID, usageAgentID); err != nil {
		t.Fatalf("insert cascade fixture usage: %v", err)
	}
}

// TestDeleteAgentRuntime_LightPathTombstones covers the no-active-agents
// delete: the runtime row survives as a tombstone (hidden from lists), any
// already-archived agents stay put, and task_usage_daily survives. The old flow
// hard-deleted archived agents + the row here.
func TestDeleteAgentRuntime_LightPathTombstones(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createCascadeFixtureRuntime(t, ctx, "Tombstone Light Runtime")
	// An ALREADY-archived agent (archived before the delete — the restore-on-
	// revival path must NOT resurrect this one; only deletion-archived ids
	// are recorded).
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Tombstone PreArchived Agent")
	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now(), archived_by = $2::uuid WHERE id = $1`, agentID, testUserID); err != nil {
		t.Fatalf("pre-archive fixture agent: %v", err)
	}
	createCascadeFixtureUsage(t, ctx, runtimeID, agentID)

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/"+runtimeID, nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.DeleteAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var meta struct {
		DeletedAt               string   `json:"deleted_at"`
		DeletedArchivedAgentIDs []string `json:"deleted_archived_agent_ids"`
	}
	var rawMetadata []byte
	if err := testPool.QueryRow(ctx, `SELECT metadata FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&rawMetadata); err != nil {
		t.Fatalf("runtime row must survive: %v", err)
	}
	if err := json.Unmarshal(rawMetadata, &meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if meta.DeletedAt == "" {
		t.Fatalf("expected tombstone, got %s", rawMetadata)
	}
	// Light path archives nobody — the recorded list must be empty so revival
	// doesn't resurrect pre-existing archived agents.
	if len(meta.DeletedArchivedAgentIDs) != 0 {
		t.Fatalf("light path must record no archived ids, got %v", meta.DeletedArchivedAgentIDs)
	}
	var agentRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent WHERE id = $1`, agentID).Scan(&agentRows); err != nil {
		t.Fatalf("count agent rows: %v", err)
	}
	if agentRows != 1 {
		t.Fatal("archived agent must survive the light-path delete")
	}
	var usage int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_usage_hourly WHERE runtime_id = $1`, runtimeID).Scan(&usage); err != nil {
		t.Fatalf("count usage: %v", err)
	}
	if usage != 1 {
		t.Fatalf("usage history must survive, got %d", usage)
	}
}

// TestRuntimeTombstoneRevival pins the daemon-re-registration resurrection:
// snapshot → upsert (overwrites metadata, clearing the marker) → restore.
// This mirrors DaemonRegister's built-in branch call order — the snapshot
// MUST come first because the upsert erases the recorded id list.
func TestRuntimeTombstoneRevival(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createCascadeFixtureRuntime(t, ctx, "Tombstone Revival Runtime")
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Tombstone Revival Agent")
	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now(), archived_by = $2::uuid WHERE id = $1`, agentID, testUserID); err != nil {
		t.Fatalf("archive fixture agent: %v", err)
	}
	deletedIDsJSON, _ := json.Marshal([]string{agentID})
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime
		SET metadata = metadata || jsonb_build_object('deleted_at', '2026-09-29T00:00:00Z', 'deleted_archived_agent_ids', $2::jsonb)
		WHERE id = $1
	`, runtimeID, deletedIDsJSON); err != nil {
		t.Fatalf("tombstone fixture: %v", err)
	}

	wsUUID, err := uuidFromString(testWorkspaceID)
	if err != nil {
		t.Fatalf("parse workspace: %v", err)
	}

	// The fixture row carries daemon_id NULL (the arbiter can't match NULL),
	// so give it the daemon id the "re-registering" daemon would send BEFORE
	// the snapshot — same (workspace, daemon_id, provider) as the upsert below.
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET daemon_id = 'tombstone-revival-daemon' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("set fixture daemon_id: %v", err)
	}

	// Step 1: snapshot BEFORE upsert (the recorded ids live only here).
	snap := testHandler.tombstoneSnapshot(ctx, wsUUID, "tombstone-revival-daemon", "cascade-test")
	if len(snap) != 1 || snap[0] != agentID {
		t.Fatalf("snapshot = %v, want [%s]", snap, agentID)
	}

	// A non-tombstoned / absent row snapshots to nil.
	if got := testHandler.tombstoneSnapshot(ctx, wsUUID, "tombstone-revival-daemon", "never-registered-provider"); got != nil {
		t.Fatalf("absent row must snapshot nil, got %v", got)
	}

	// Step 2: upsert with daemon-sent metadata — clears the tombstone marker
	// and reuses the SAME row id (the arbiter the whole design rides on).
	row, err := testHandler.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: wsUUID,
		DaemonID:    strToText("tombstone-revival-daemon"),
		Name:        "Tombstone Revival Runtime (MacBook-Pro)",
		RuntimeMode: "local",
		Provider:    "cascade-test",
		Status:      "online",
		DeviceInfo:  "revival device",
		Metadata:    []byte(`{"version":"1"}`),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if uuidToString(row.ID) != runtimeID {
		t.Fatalf("revival must reuse the same row id, got %s want %s", uuidToString(row.ID), runtimeID)
	}

	// Step 3: restore the recorded agents.
	restored, err := testHandler.Queries.RestoreAgentsArchivedByRuntimeDeletion(ctx, deletedIDsJSON)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(restored) != 1 || uuidToString(restored[0].ID) != agentID {
		t.Fatalf("expected to restore exactly %s, got %v", agentID, restored)
	}
	var activeAgain bool
	if err := testPool.QueryRow(ctx, `SELECT (archived_at IS NULL) FROM agent WHERE id = $1`, agentID).Scan(&activeAgain); err != nil {
		t.Fatalf("reload agent: %v", err)
	}
	if !activeAgain {
		t.Fatal("restored agent must be active again")
	}

	// The restored agent's runtime binding was never broken — it still points
	// at the revived row.
	var boundRuntime string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&boundRuntime); err != nil {
		t.Fatalf("reload binding: %v", err)
	}
	if boundRuntime != runtimeID {
		t.Fatalf("binding drifted: %s != %s", boundRuntime, runtimeID)
	}

	// Marker is gone after the upsert — a second snapshot is nil.
	if got := testHandler.tombstoneSnapshot(ctx, wsUUID, "", "cascade-test"); got != nil {
		t.Fatalf("tombstone must be cleared by the upsert, got %v", got)
	}

	// Restoring the same ids twice is a no-op (already active).
	again, err := testHandler.Queries.RestoreAgentsArchivedByRuntimeDeletion(ctx, deletedIDsJSON)
	if err != nil || len(again) != 0 {
		t.Fatalf("double restore must return nothing, got %v / %v", again, err)
	}
}
