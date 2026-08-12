package handler

// chat_input_ownership_test.go
// ----------------------------------------------------------------------------
// Tests for MUL-4351 (chat input batch ownership, migration 142) + the 0.3.4
// IM-style unread count path (migration 135).
//
// Strategy: drive the sqlc queries directly with sqlc.Queries — the public
// handler layer would need a full Mux + middleware harness which other chat
// tests sidestep. We use testPool directly to seed sessions / messages,
// then call testHandler.Queries.* to assert the new wiring.
// ----------------------------------------------------------------------------

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// helper: create a chat session in the test workspace, return its id and a
// pre-loaded agent id the test can drive messages against. Does NOT register
// t.Cleanup — callers manage cleanup explicitly so the order is well-defined
// when one cleanup depends on another (e.g. delete chat_session before
// deleting agent).
func newTestChatSessionForOwnership(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	agentID := createTestAgentLocal(t, fmt.Sprintf("ownership-test-agent-%d", time.Now().UnixNano()))
	session, err := testHandler.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
		AgentID:     util.MustParseUUID(agentID),
		CreatorID:   util.MustParseUUID(testUserID),
		Title:       "ownership test session",
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	return agentID, uuidToString(session.ID)
}

// createTestAgentLocal inserts a minimal agent row directly. Mirrors the
// INSERT pattern in handler_test.go's createHandlerTestAgent. Does NOT
// register t.Cleanup — caller manages order (typically: delete chat_session
// first so FK cascade removes agent_task_queue, then delete agent).
func createTestAgentLocal(t *testing.T, name string) string {
	t.Helper()
	var agentID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id
	`, testWorkspaceID, name, handlerTestRuntimeID(t), testUserID).Scan(&agentID)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	return agentID
}

// 1. EnqueueChatTask binds chat_input_task_id = task.id.
func TestEnqueueChatTask_BindsChatInputTaskIdEqualsTaskId(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	agentID, sessionID := newTestChatSessionForOwnership(t, context.Background())
	// Cleanup must run AFTER the test body. t.Cleanup runs at test exit,
	// not defer order — we register all three cleanups via t.Cleanup so the
	// order is explicit. Order: chat_session (cascades agent_task_queue) →
	// agent_task_queue (for the freshly created row) → agent.
	taskHint := ""
	t.Cleanup(func() {
		if taskHint != "" {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskHint)
		}
		_, _ = testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, sessionID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	// Pass the FULL ChatSession struct (with AgentID/RuntimeID populated)
	// to EnqueueChatTask — a zero-value struct would make GetAgent look up
	// an invalid UUID and fail with "no rows in result set".
	session, err := testHandler.Queries.GetChatSession(context.Background(), util.MustParseUUID(sessionID))
	if err != nil {
		t.Fatalf("get chat session: %v", err)
	}
	task, err := testHandler.TaskService.EnqueueChatTask(
		context.Background(),
		session,
		util.MustParseUUID(testUserID),
		false,
	)
	if err != nil {
		t.Fatalf("enqueue chat task: %v", err)
	}
	taskHint = uuidToString(task.ID)

	if !task.ChatInputTaskID.Valid {
		t.Fatalf("expected chat_input_task_id to be set, got NULL")
	}
	if task.ChatInputTaskID != task.ID {
		t.Fatalf("chat_input_task_id (%s) != task.id (%s)",
			uuidToString(task.ChatInputTaskID), uuidToString(task.ID))
	}
}

// 2. LastReadAt cursor is updated by TouchChatSessionLastRead.
func TestTouchChatSessionLastRead_AdvancesCursor(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	_, sessionID := newTestChatSessionForOwnership(t, context.Background())
	defer cleanupTestChatSession(t, sessionID)
	ctx := context.Background()

	if err := testHandler.Queries.TouchChatSessionLastRead(ctx, util.MustParseUUID(sessionID)); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err := testHandler.Queries.GetChatSession(ctx, util.MustParseUUID(sessionID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.LastReadAt.Valid {
		t.Fatalf("expected last_read_at set after TouchChatSessionLastRead")
	}
}

// 3. Unread count counts only assistant messages after the cursor.
// Uses 1-second gaps between seeded messages to avoid pg microsecond-rounding
// collisions that could mask the cursor boundary (now() + 1ms and 1.5ms are
// commonly rounded to the same microsecond in real timing).
func TestListChatSessionsByCreatorWithUnreadCount_CountsAfterCursor(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	_, sessionID := newTestChatSessionForOwnership(t, context.Background())
	defer cleanupTestChatSession(t, sessionID)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := testPool.Exec(ctx, `
			INSERT INTO chat_message (chat_session_id, role, content, created_at)
			VALUES ($1, 'assistant', $2, now() + ($3::int * interval '1 second'))
		`, sessionID, "assistant reply", i+1)
		if err != nil {
			t.Fatalf("seed assistant msg %d: %v", i, err)
		}
	}

	// last_read_at between message 1 (now+1s) and message 2 (now+2s).
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_session SET last_read_at = now() + interval '1500 milliseconds' WHERE id = $1
	`, sessionID); err != nil {
		t.Fatalf("set last_read_at: %v", err)
	}

	rows, err := testHandler.Queries.ListChatSessionsByCreatorWithUnreadCount(ctx, db.ListChatSessionsByCreatorWithUnreadCountParams{
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
		CreatorID:   util.MustParseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}

	var found *db.ListChatSessionsByCreatorWithUnreadCountRow
	for i := range rows {
		if uuidToString(rows[i].ID) == sessionID {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("session %s not in list", sessionID)
	}
	if found.UnreadCount != 2 {
		t.Fatalf("unread_count: got %d, want 2 (msgs 2 and 3 are after cursor at 1.5s)",
			found.UnreadCount)
	}
}

// 4. Unread count falls back to unread_since when unread_since is *earlier*
// than last_read_at (legacy flag preservation). Migration 135 marks
// last_read_at NOT NULL DEFAULT now(); the fallback path activates when
// unread_since < last_read_at, mirroring the 135 UPDATE that backfills
// last_read_at = unread_since - 1us for pre-existing unread rows.
func TestListChatSessionsByCreatorWithUnreadCount_FallsBackToUnreadSince(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	_, sessionID := newTestChatSessionForOwnership(t, context.Background())
	defer cleanupTestChatSession(t, sessionID)
	ctx := context.Background()

	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, created_at)
		VALUES ($1, 'assistant', 'after-unread', now())
	`, sessionID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// unread_since = 1s ago, last_read_at = now() (newer). 135's fallback
	// in the query is COALESCE(last_read_at, unread_since, '1970-01-01').
	// With both non-NULL, last_read_at wins — but if we set last_read_at to
	// an older value, unread_since (1s ago) sits between last_read_at and
	// now(), and unread_since is the more permissive cursor (count after
	// unread_since includes the assistant message we just seeded).
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_session
		SET unread_since = now() - interval '1 second',
		    last_read_at = now() - interval '1 hour'
		WHERE id = $1
	`, sessionID); err != nil {
		t.Fatalf("set cursors: %v", err)
	}

	rows, err := testHandler.Queries.ListChatSessionsByCreatorWithUnreadCount(ctx, db.ListChatSessionsByCreatorWithUnreadCountParams{
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
		CreatorID:   util.MustParseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range rows {
		if uuidToString(rows[i].ID) == sessionID {
			// The fallback picks the *earlier* of last_read_at / unread_since.
			// With last_read_at = -1h and unread_since = -1s, last_read_at
			// wins, so the assistant message (at now) is counted.
			if rows[i].UnreadCount != 1 {
				t.Fatalf("unread_count: got %d, want 1 (cursor at last_read_at -1h)",
					rows[i].UnreadCount)
			}
			return
		}
	}
	t.Fatalf("session not in list")
}

// 5. Fresh session with no messages returns 0.
func TestListChatSessionsByCreatorWithUnreadCount_ZeroOnFreshSession(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	_, sessionID := newTestChatSessionForOwnership(t, context.Background())
	defer cleanupTestChatSession(t, sessionID)
	ctx := context.Background()

	rows, err := testHandler.Queries.ListChatSessionsByCreatorWithUnreadCount(ctx, db.ListChatSessionsByCreatorWithUnreadCountParams{
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
		CreatorID:   util.MustParseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range rows {
		if uuidToString(rows[i].ID) == sessionID {
			if rows[i].UnreadCount != 0 {
				t.Fatalf("unread_count on fresh session: got %d, want 0",
					rows[i].UnreadCount)
			}
			return
		}
	}
	t.Fatalf("session not in list")
}

// 6. PinChatSession / UnpinChatSession toggles pinned_at.
func TestPinChatSession_TogglesPinnedAt(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	_, sessionID := newTestChatSessionForOwnership(t, context.Background())
	defer cleanupTestChatSession(t, sessionID)
	ctx := context.Background()

	if err := testHandler.Queries.PinChatSession(ctx, util.MustParseUUID(sessionID)); err != nil {
		t.Fatalf("pin: %v", err)
	}
	pinned, err := testHandler.Queries.GetChatSession(ctx, util.MustParseUUID(sessionID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !pinned.PinnedAt.Valid {
		t.Fatalf("pinned_at not set after PinChatSession")
	}

	if err := testHandler.Queries.UnpinChatSession(ctx, util.MustParseUUID(sessionID)); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	unpinned, err := testHandler.Queries.GetChatSession(ctx, util.MustParseUUID(sessionID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if unpinned.PinnedAt.Valid {
		t.Fatalf("pinned_at still set after UnpinChatSession: %v", unpinned.PinnedAt.Time)
	}
}

// 6b. ListChatSessionsByCreator puts pinned rows at the top of the result
// (pinned_at DESC), then unpinned rows by updated_at DESC. Backs the
// chat_pin_ui list-sort contract (migration 139 pinned_at + 140 partial
// index). All 3 list queries (chat.sql: ListChatSessionsByCreator +
// ListAllChatSessionsByCreator, chat_input_ownership.sql:
// ListChatSessionsByCreatorWithUnreadCount) share the same ORDER BY, so a
// single test against the canonical Web/Desktop live query is enough to
// pin the contract.
func TestListChatSessionsByCreator_PinnedFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	ctx := context.Background()

	// Three independent agents + sessions so the FK chain (session → agent
	// → workspace) is the same shape as the other chat tests, and so each
	// session can carry its own deterministic updated_at / pinned_at
	// without cross-test interference from the shared test workspace's
	// other rows.
	_, pinnedSessionID := newTestChatSessionForOwnership(t, ctx)
	defer cleanupTestChatSession(t, pinnedSessionID)
	_, olderSessionID := newTestChatSessionForOwnership(t, ctx)
	defer cleanupTestChatSession(t, olderSessionID)
	_, newerSessionID := newTestChatSessionForOwnership(t, ctx)
	defer cleanupTestChatSession(t, newerSessionID)

	// Pinned row: pinned_at = 2020 (very early), updated_at = 2020 (very
	// early). The pinned group sorts by pinned_at DESC, so this lands at
	// the very top.
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_session
		SET pinned_at = '2020-06-15 12:00:00+00',
		    updated_at = '2020-06-15 12:00:00+00'
		WHERE id = $1
	`, pinnedSessionID); err != nil {
		t.Fatalf("set pinned row: %v", err)
	}
	// Unpinned rows: distinct updated_at values so the unpinned group's
	// tie-breaker is deterministic. newer (2025-06) must sort above
	// older (2025-01).
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_session SET updated_at = '2025-01-01 00:00:00+00' WHERE id = $1
	`, olderSessionID); err != nil {
		t.Fatalf("set older updated_at: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_session SET updated_at = '2025-06-01 00:00:00+00' WHERE id = $1
	`, newerSessionID); err != nil {
		t.Fatalf("set newer updated_at: %v", err)
	}

	rows, err := testHandler.Queries.ListChatSessionsByCreator(ctx, db.ListChatSessionsByCreatorParams{
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
		CreatorID:   util.MustParseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}

	// Record the index of each of the 3 seeded sessions in the result.
	// Other test rows may live in this list too (the test workspace is
	// shared across handler_test.go), so we only assert relative order
	// among the 3 known ids.
	positions := map[string]int{}
	for i, r := range rows {
		id := uuidToString(r.ID)
		if id == pinnedSessionID || id == olderSessionID || id == newerSessionID {
			positions[id] = i
		}
	}
	if len(positions) != 3 {
		t.Fatalf("expected 3 seeded sessions in list, found %d", len(positions))
	}
	if positions[pinnedSessionID] >= positions[newerSessionID] {
		t.Fatalf("pinned session must sort above unpinned group: pinned=%d, newer=%d",
			positions[pinnedSessionID], positions[newerSessionID])
	}
	if positions[newerSessionID] >= positions[olderSessionID] {
		t.Fatalf("within unpinned group, newer (2025-06) must sort above older (2025-01): newer=%d, older=%d",
			positions[newerSessionID], positions[olderSessionID])
	}
}

// 7. SetChatSessionAgentIntro flags an intro session. Migration 138 wiring.
func TestSetChatSessionAgentIntro_FlagsIntroSession(t *testing.T) {
	if testing.Short() {
		t.Skip("requires DB")
	}
	_, sessionID := newTestChatSessionForOwnership(t, context.Background())
	defer cleanupTestChatSession(t, sessionID)
	ctx := context.Background()

	if err := testHandler.Queries.SetChatSessionAgentIntro(ctx, util.MustParseUUID(sessionID)); err != nil {
		t.Fatalf("set intro: %v", err)
	}
	got, err := testHandler.Queries.GetChatSession(ctx, util.MustParseUUID(sessionID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.IsAgentIntro {
		t.Fatalf("expected is_agent_intro = true after SetChatSessionAgentIntro")
	}
}

// cleanup helpers — local to this test file.
func cleanupTestChatSession(t *testing.T, sessionID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, sessionID); err != nil {
		t.Logf("cleanup chat_session: %v", err)
	}
}

func cleanupTestAgentByID(t *testing.T, agentID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID); err != nil {
		t.Logf("cleanup agent: %v", err)
	}
}

func cleanupTestTask(t *testing.T, taskID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID); err != nil {
		t.Logf("cleanup task: %v", err)
	}
}
