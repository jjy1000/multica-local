package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// stubChatTitleProvider — a deterministic ChatTitleProvider for unit tests.
// Always returns the configured `title` (or the configured `err` when non-nil).
// ---------------------------------------------------------------------------

type stubChatTitleProvider struct {
	enabled bool
	title   string
	err     error
}

func (s *stubChatTitleProvider) Enabled() bool { return s.enabled }
func (s *stubChatTitleProvider) GenerateTitle(_ context.Context, _ string) (string, error) {
	return s.title, s.err
}

// ---------------------------------------------------------------------------
// Case 1: sanitizeChatTitle — full matrix. Identical to upstream's matrix
// (chat_title_test.go::TestSanitizeChatTitle) modulo the data — same rules.
// ---------------------------------------------------------------------------

func TestSanitizeChatTitle(t *testing.T) {
	longInput := strings.Repeat("a", chatSessionTitleMaxLen+50)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Fix login bug", "Fix login bug"},
		{"surrounding double quotes", `"Fix login bug"`, "Fix login bug"},
		{"surrounding single quotes", `'Fix login bug'`, "Fix login bug"},
		{"smart quotes", "“修复登录问题”", "修复登录问题"},
		{"cjk brackets", "「优化查询性能」", "优化查询性能"},
		{"english label prefix", "Title: Fix login bug", "Fix login bug"},
		{"chinese label prefix", "标题：修复登录问题", "修复登录问题"},
		{"label then quotes", `标题："修复登录问题"`, "修复登录问题"},
		{"prefix wrapped in quotes", `"Title: Fix login"`, "Fix login"},
		{"prefix wrapped in cjk brackets", "「标题：修复登录问题」", "修复登录问题"},
		{"prefix in quotes with trailing period", `"Title: Fix login".`, "Fix login"},
		{"prefix in cjk brackets with trailing period", "「标题：修复登录问题」。", "修复登录问题"},
		{"trailing period", "Fix login bug.", "Fix login bug"},
		{"trailing cjk period", "修复登录问题。", "修复登录问题"},
		{"newlines collapsed", "Fix\nlogin\nbug", "Fix login bug"},
		{"leading trailing space", "   Fix login bug   ", "Fix login bug"},
		{"only punctuation empty", `"。"`, ""},
		{"blank", "   ", ""},
		{"length cap", longInput, longInput[:chatSessionTitleMaxLen]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeChatTitle(tc.in); got != tc.want {
				t.Fatalf("sanitizeChatTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Case 2: nil provider → no-op, no error.
// ---------------------------------------------------------------------------

func TestChatTitle_NilProviderIsNoOp(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	h := testHandler
	prev := h.ChatTitleProvider
	h.ChatTitleProvider = nil
	t.Cleanup(func() { h.ChatTitleProvider = prev })

	session := newChatTitleTestSession(t, "original")

	// Synchronous call: returns ErrChatTitleDisabled, applied=false.
	_, applied, err := h.generateChatSessionTitle(context.Background(), session.ID, session.Title, "anything")
	if !errors.Is(err, ErrChatTitleDisabled) {
		t.Fatalf("expected ErrChatTitleDisabled, got %v", err)
	}
	if applied {
		t.Fatal("expected no application with nil provider")
	}
	if got := chatSessionTitleFromDB(t, session.ID); got != session.Title {
		t.Fatalf("DB title changed: got %q, want %q", got, session.Title)
	}
}

// ---------------------------------------------------------------------------
// Case 3: disabled provider → no-op, no error.
// ---------------------------------------------------------------------------

func TestChatTitle_DisabledProviderIsNoOp(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	h := testHandler
	prev := h.ChatTitleProvider
	h.ChatTitleProvider = &stubChatTitleProvider{enabled: false}
	t.Cleanup(func() { h.ChatTitleProvider = prev })

	session := newChatTitleTestSession(t, "original disabled")

	// Async entry point short-circuits before spawning a goroutine.
	h.maybeGenerateChatTitleAsync(testWorkspaceID, testUserID, session.ID, session.Title, "trigger text")

	_, applied, err := h.generateChatSessionTitle(context.Background(), session.ID, session.Title, "trigger text")
	if !errors.Is(err, ErrChatTitleDisabled) {
		t.Fatalf("expected ErrChatTitleDisabled, got %v", err)
	}
	if applied {
		t.Fatal("expected no application with disabled provider")
	}
	if got := chatSessionTitleFromDB(t, session.ID); got != session.Title {
		t.Fatalf("DB title changed unexpectedly: got %q, want %q", got, session.Title)
	}
}

// ---------------------------------------------------------------------------
// Case 4: provider returns empty / unusable output → sanitizes to "" → silent
// fallback, no DB change.
// ---------------------------------------------------------------------------

func TestChatTitle_EmptyModelOutputIsSilentFallback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	h := testHandler
	prev := h.ChatTitleProvider
	h.ChatTitleProvider = &stubChatTitleProvider{enabled: true, title: `"。"`}
	t.Cleanup(func() { h.ChatTitleProvider = prev })

	session := newChatTitleTestSession(t, "original empty")

	_, applied, err := h.generateChatSessionTitle(context.Background(), session.ID, session.Title, "some opening message")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Fatal("expected no application for empty model output")
	}
	if got := chatSessionTitleFromDB(t, session.ID); got != session.Title {
		t.Fatalf("DB title changed: got %q, want %q", got, session.Title)
	}
}

// ---------------------------------------------------------------------------
// Case 5: provider error → silent fallback, no DB change.
// ---------------------------------------------------------------------------

func TestChatTitle_ProviderErrorIsSilentFallback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	h := testHandler
	prev := h.ChatTitleProvider
	h.ChatTitleProvider = &stubChatTitleProvider{
		enabled: true,
		err:     errors.New("upstream 502"),
	}
	t.Cleanup(func() { h.ChatTitleProvider = prev })

	session := newChatTitleTestSession(t, "original upstream-error")

	_, applied, err := h.generateChatSessionTitle(context.Background(), session.ID, session.Title, "trigger")
	if err == nil {
		t.Fatal("expected error from failing provider")
	}
	if applied {
		t.Fatal("expected no application when provider fails")
	}
	if got := chatSessionTitleFromDB(t, session.ID); got != session.Title {
		t.Fatalf("DB title changed: got %q, want %q", got, session.Title)
	}
}

// ---------------------------------------------------------------------------
// Case 6: provider returns a clean title → CAS writes it, DB updates.
// ---------------------------------------------------------------------------

func TestChatTitle_GeneratesTitleOnSuccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	h := testHandler
	prev := h.ChatTitleProvider
	h.ChatTitleProvider = &stubChatTitleProvider{enabled: true, title: "修复登录跳转死循环"}
	t.Cleanup(func() { h.ChatTitleProvider = prev })

	session := newChatTitleTestSession(t, "long original opening prompt that the user typed")

	updated, applied, err := h.generateChatSessionTitle(context.Background(), session.ID, session.Title, "long original opening prompt...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !applied {
		t.Fatal("expected title to be applied")
	}
	if updated.Title != "修复登录跳转死循环" {
		t.Fatalf("updated.Title = %q, want %q", updated.Title, "修复登录跳转死循环")
	}
	if got := chatSessionTitleFromDB(t, session.ID); got != "修复登录跳转死循环" {
		t.Fatalf("DB title = %q, want %q", got, "修复登录跳转死循环")
	}
}

// ---------------------------------------------------------------------------
// Case 7: title already changed (manual rename / already auto-titled) → CAS
// miss → applied=false, DB title preserved. Mirrors upstream's "DoesNotClobber"
// case but uses a stub provider so the test doesn't need a live LLM stub
// server.
// ---------------------------------------------------------------------------

func TestChatTitle_DoesNotClobberConcurrentRename(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	h := testHandler
	prev := h.ChatTitleProvider
	h.ChatTitleProvider = &stubChatTitleProvider{enabled: true, title: "Generated Title"}
	t.Cleanup(func() { h.ChatTitleProvider = prev })

	session := newChatTitleTestSession(t, "stale observed title")

	// Simulate a manual rename landing BEFORE the generator runs.
	manual := "My Renamed Chat"
	if _, err := h.Queries.UpdateChatSessionTitle(context.Background(), db.UpdateChatSessionTitleParams{
		ID:    session.ID,
		Title: manual,
	}); err != nil {
		t.Fatalf("simulate manual rename: %v", err)
	}

	// Generator still holds the stale observed title → CAS no longer matches.
	_, applied, err := h.generateChatSessionTitle(context.Background(), session.ID, "stale observed title", "stale observed title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Fatal("expected CAS miss when title was concurrently renamed")
	}
	if got := chatSessionTitleFromDB(t, session.ID); got != manual {
		t.Fatalf("DB title = %q, want manual rename %q preserved", got, manual)
	}
}

// ---------------------------------------------------------------------------
// Helpers (DB-backed). Live-DB tests skip when DB is unavailable.
// ---------------------------------------------------------------------------

func newChatTitleTestSession(t *testing.T, title string) db.ChatSession {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load seeded agent: %v", err)
	}
	session, err := testHandler.Queries.CreateChatSession(context.Background(), db.CreateChatSessionParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		AgentID:     parseUUID(agentID),
		CreatorID:   parseUUID(testUserID),
		Title:       title,
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, uuidToString(session.ID))
	})
	return session
}

func chatSessionTitleFromDB(t *testing.T, sessionID pgtype.UUID) string {
	t.Helper()
	var title string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title FROM chat_session WHERE id = $1`, uuidToString(sessionID),
	).Scan(&title); err != nil {
		t.Fatalf("load session title: %v", err)
	}
	return title
}