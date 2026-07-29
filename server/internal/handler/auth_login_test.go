package handler

// Regression tests for server/internal/handler/auth.go::UsernameLogin.
//
// Background (see .omc/incidents/2026-06-27-username-only-login-loses-workspaces.md):
// the localized fork is username-only — POST /auth/login {"name":"alice"} creates
// a fresh user row on first sight and returns the existing row on subsequent
// logins. Workspace membership is bound to the creator's user_id, so a
// regression that turns this into "always create" or "always update existing"
// is a silent data-loss bug.
//
// These tests pin the four observable semantics of UsernameLogin:
//   1. upsert path:        unseen name → new user row, 200, response carries new id
//   2. idempotent path:    seen name → no new user row, same id, 200
//   3. token continuity:   same name twice → both 200, same user id (token may differ)
//   4. multi-name:         distinct names → distinct user ids (no cross-contamination)
//
// They mirror the mockDB + db.New style of auth_signup_test.go (TestFindOrCreateUserGating)
// so they share the same setup, no new test deps, no DB or HTTP harness required.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// loginMockDB is a programmable DBTX stand-in. It records call counts so
// tests can assert "CreateUser invoked exactly once across two logins" —
// the load-bearing regression pin for the upsert semantics.
//
// The mock decides what to return per QueryRow by inspecting the SQL string
// the sqlc method dispatches (we don't replay SQL parsing — we route by a
// counter map because sqlc embeds named constants).
type loginMockDB struct {
	db.DBTX

	mu sync.Mutex

	// getByEmailResponses is a FIFO queue of (User, error) pairs returned
	// by successive GetUserByEmail calls. Empty queue ⇒ ErrNoRows.
	getByEmailResponses []queuedResp

	// createResponses is a FIFO queue of (User, error) pairs returned by
	// successive CreateUser calls. Empty queue ⇒ ErrNoRows (defensive;
	// the handler should only invoke CreateUser when GetUserByEmail
	// already returned ErrNoRows, so a missing entry means the test
	// forgot to stage enough responses).
	createResponses []queuedResp

	// lastCreatedName is the Name field of the most recent CreateUser
	// scan target. Captured so tests can assert the handler hands the
	// request name through unmodified.
	lastCreatedName string

	// observed emails (GetUserByEmail arg[0]) and created names (CreateUser
	// arg[0]) are recorded in order — used for the multi-name case.
	emailsLookedUp []string
	namesCreated   []string

	getByEmailCalls int
	createCalls     int
}

type queuedResp struct {
	user db.User
	err  error
}

func newLoginMockDB() *loginMockDB {
	return &loginMockDB{}
}

func (m *loginMockDB) enqueueGet(user db.User, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getByEmailResponses = append(m.getByEmailResponses, queuedResp{user: user, err: err})
}

func (m *loginMockDB) enqueueCreate(user db.User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createResponses = append(m.createResponses, queuedResp{user: user})
}

func (m *loginMockDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	// sqlc-named constants in user.sql.go distinguish GetUserByEmail vs
	// CreateUser; the route string contains the table column list which
	// is enough to disambiguate. We match on the SELECT projection.
	s := strings.ToLower(sql)
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case strings.Contains(s, `from "user"`):
		m.getByEmailCalls++
		if len(args) > 0 {
			if email, ok := args[0].(string); ok {
				m.emailsLookedUp = append(m.emailsLookedUp, email)
			}
		}
		if len(m.getByEmailResponses) == 0 {
			return &loginMockRow{err: pgx.ErrNoRows}
		}
		r := m.getByEmailResponses[0]
		m.getByEmailResponses = m.getByEmailResponses[1:]
		// Clone the user into the scan target so each Scan() is fresh.
		return &loginMockRow{user: r.user, err: r.err}
	case strings.Contains(s, `insert into "user"`):
		m.createCalls++
		if len(args) >= 2 {
			if name, ok := args[0].(string); ok {
				m.namesCreated = append(m.namesCreated, name)
				m.lastCreatedName = name
			}
		}
		if len(m.createResponses) == 0 {
			return &loginMockRow{err: errors.New("loginMockDB: no CreateUser response staged")}
		}
		r := m.createResponses[0]
		m.createResponses = m.createResponses[1:]
		return &loginMockRow{user: r.user, err: r.err}
	default:
		// Unknown query — fail loud so a future sqlc schema change
		// doesn't silently bypass the regression pins.
		return &loginMockRow{err: errors.New("loginMockDB: unrecognized query: " + sql)}
	}
}

func (m *loginMockDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 1"), nil
}

type loginMockRow struct {
	pgx.Row
	user db.User
	err  error
}

func (r *loginMockRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	u := r.user
	// Map into the destination slots. The slot order matches user.sql.go
	// GetUserByEmail / CreateUser Scan: ID (*pgtype.UUID), Name, Email,
	// AvatarUrl (*pgtype.Text), CreatedAt/UpdatedAt/OnboardedAt
	// (*pgtype.Timestamptz), OnboardingQuestionnaire, CloudWaitlistEmail,
	// CloudWaitlistReason, StarterContentState, Language,
	// ProfileDescription, Timezone.
	if len(dest) < 14 {
		return errors.New("loginMockRow.Scan: unexpected slot count")
	}
	if d, ok := dest[0].(*pgtype.UUID); ok {
		*d = u.ID
	}
	if d, ok := dest[1].(*string); ok {
		*d = u.Name
	}
	if d, ok := dest[2].(*string); ok {
		*d = u.Email
	}
	// Optional pgtype.Text / pgtype.Timestamptz slots — leave as zero
	// (Valid=false); userToResponse coalesces them to nil pointers.
	return nil
}

// stageUser builds a deterministic db.User with the given name + UUID so
// the test can assert "CreateUser got handed this name" and "GetUserByEmail
// returned this UUID" across calls.
func stageUser(id uuid.UUID, name string) db.User {
	var pgid pgtype.UUID
	pgid.Valid = true
	copy(pgid.Bytes[:], id[:])
	return db.User{
		ID:    pgid,
		Name:  name,
		Email: name + "@local",
	}
}

// doLogin fires the UsernameLogin handler with the supplied name and
// returns the decoded LoginResponse, the HTTP status, and the raw body
// (for error assertions on bad-input cases).
func doLogin(t *testing.T, h *Handler, name string) (LoginResponse, int, string) {
	t.Helper()
	body, _ := json.Marshal(UsernameLoginRequest{Name: name})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.UsernameLogin(rr, req)
	raw := rr.Body.String()
	if rr.Code != http.StatusOK {
		return LoginResponse{}, rr.Code, raw
	}
	var resp LoginResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode LoginResponse: %v (body=%q)", err, raw)
	}
	return resp, rr.Code, raw
}

// TestUsernameLoginUpsert pins the "first login creates a user row" path.
// Regression guard: a future change that turns UsernameLogin into a
// pure-read (always 404 for unseen names) would silently lock new users
// out — the Created assertion catches it. A change that always-inserts
// without checking is caught by TestUsernameLoginIdempotent.
func TestUsernameLoginUpsert(t *testing.T) {
	mock := newLoginMockDB()
	userID := uuid.New()
	mock.enqueueGet(db.User{}, pgx.ErrNoRows) // first GetUserByEmail → not found
	mock.enqueueCreate(stageUser(userID, "alice"))

	h := newTestHandler(Config{AllowSignup: true})
	h.Queries = db.New(mock)

	resp, status, raw := doLogin(t, h, "alice")
	if status != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%q", status, raw)
	}
	if resp.User.ID != userID.String() {
		t.Fatalf("user.id: got %q want %q", resp.User.ID, userID.String())
	}
	if resp.User.Name != "alice" {
		t.Fatalf("user.name: got %q want %q", resp.User.Name, "alice")
	}
	if resp.Token == "" {
		t.Fatal("token is empty in LoginResponse")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.getByEmailCalls != 1 {
		t.Fatalf("GetUserByEmail calls: got %d want 1", mock.getByEmailCalls)
	}
	if mock.createCalls != 1 {
		t.Fatalf("CreateUser calls: got %d want 1", mock.createCalls)
	}
	if len(mock.namesCreated) != 1 || mock.namesCreated[0] != "alice" {
		t.Fatalf("CreateUser arg name: got %v want [alice]", mock.namesCreated)
	}
	if len(mock.emailsLookedUp) != 1 || mock.emailsLookedUp[0] != "alice@local" {
		t.Fatalf("GetUserByEmail arg email: got %v want [alice@local]", mock.emailsLookedUp)
	}
}

// TestUsernameLoginIdempotent pins "second login with the same name
// returns the same user row without calling CreateUser". This is the
// load-bearing regression: a change that always inserts (or always
// updates) would silently swap the user's workspace ownership.
func TestUsernameLoginIdempotent(t *testing.T) {
	mock := newLoginMockDB()
	userID := uuid.New()
	existing := stageUser(userID, "alice")
	// Stage two GetUserByEmail responses — both should hit the same
	// user row. Crucially, NO CreateUser response is staged: if the
	// handler ever calls CreateUser on the 2nd login, the mock will
	// return an error and the handler will 500 (and the createCalls
	// assertion below will fire).
	mock.enqueueGet(existing, nil)
	mock.enqueueGet(existing, nil)

	h := newTestHandler(Config{AllowSignup: true})
	h.Queries = db.New(mock)

	first, status1, raw1 := doLogin(t, h, "alice")
	if status1 != http.StatusOK {
		t.Fatalf("first login status: got %d want 200, body=%q", status1, raw1)
	}
	second, status2, raw2 := doLogin(t, h, "alice")
	if status2 != http.StatusOK {
		t.Fatalf("second login status: got %d want 200, body=%q", status2, raw2)
	}

	if first.User.ID != userID.String() {
		t.Fatalf("first.User.ID: got %q want %q", first.User.ID, userID.String())
	}
	if second.User.ID != first.User.ID {
		t.Fatalf("idempotency broken: first=%q second=%q", first.User.ID, second.User.ID)
	}
	if second.User.ID != userID.String() {
		t.Fatalf("second.User.ID: got %q want %q", second.User.ID, userID.String())
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	// REGRESSION PIN: exactly two GetUserByEmail + ZERO CreateUser.
	// A future "always create on login" patch would push createCalls
	// to 2 and trip this.
	if mock.getByEmailCalls != 2 {
		t.Fatalf("GetUserByEmail calls: got %d want 2", mock.getByEmailCalls)
	}
	if mock.createCalls != 0 {
		t.Fatalf("CreateUser calls: got %d want 0 (idempotent login must NOT insert)", mock.createCalls)
	}
	if len(mock.namesCreated) != 0 {
		t.Fatalf("namesCreated: got %v want []", mock.namesCreated)
	}
}

// TestUsernameLoginTokenContinuity asserts that consecutive logins with
// the same name both succeed and return the same user_id. We deliberately
// do NOT pin the token value: the handler issues a fresh JWT on every
// request (iat = time.Now()), so tokens are expected to differ across
// logins even when the user_id is stable. The contract under test is
// user identity continuity, not token equality.
func TestUsernameLoginTokenContinuity(t *testing.T) {
	mock := newLoginMockDB()
	userID := uuid.New()
	mock.enqueueGet(stageUser(userID, "alice"), nil)
	mock.enqueueGet(stageUser(userID, "alice"), nil)
	// A third GetUserByEmail for a safety net if the handler ever
	// does a double-lookup (e.g. an extra authz check). Same row.
	mock.enqueueGet(stageUser(userID, "alice"), nil)

	h := newTestHandler(Config{AllowSignup: true})
	h.Queries = db.New(mock)

	first, status1, raw1 := doLogin(t, h, "alice")
	if status1 != http.StatusOK {
		t.Fatalf("login #1 status: got %d want 200, body=%q", status1, raw1)
	}
	second, status2, raw2 := doLogin(t, h, "alice")
	if status2 != http.StatusOK {
		t.Fatalf("login #2 status: got %d want 200, body=%q", status2, raw2)
	}

	if first.User.ID != second.User.ID {
		t.Fatalf("user_id continuity broken: login#1=%q login#2=%q", first.User.ID, second.User.ID)
	}
	if first.Token == "" || second.Token == "" {
		t.Fatal("token must be non-empty on every successful login")
	}
}

// TestUsernameLoginDistinctNames asserts that two different names yield
// two different user_ids — the "no cross-contamination" guarantee. If
// the handler ever caches or aliases name lookups, this catches it.
//
// Also pins the upsert-vs-existing asymmetry: alice is unseen (inserts)
// while bob is seen (returns existing). Both must succeed; both must
// carry distinct IDs.
func TestUsernameLoginDistinctNames(t *testing.T) {
	mock := newLoginMockDB()
	aliceID := uuid.New()
	bobID := uuid.New()

	// alice: unseen → ErrNoRows then CreateUser
	mock.enqueueGet(db.User{}, pgx.ErrNoRows)
	mock.enqueueCreate(stageUser(aliceID, "alice"))
	// bob: already in DB → existing row, no CreateUser
	mock.enqueueGet(stageUser(bobID, "bob"), nil)

	h := newTestHandler(Config{AllowSignup: true})
	h.Queries = db.New(mock)

	aliceResp, aliceStatus, aliceRaw := doLogin(t, h, "alice")
	if aliceStatus != http.StatusOK {
		t.Fatalf("alice status: got %d want 200, body=%q", aliceStatus, aliceRaw)
	}
	bobResp, bobStatus, bobRaw := doLogin(t, h, "bob")
	if bobStatus != http.StatusOK {
		t.Fatalf("bob status: got %d want 200, body=%q", bobStatus, bobRaw)
	}

	if aliceResp.User.ID == bobResp.User.ID {
		t.Fatalf("cross-contamination: alice and bob share user_id %q", aliceResp.User.ID)
	}
	if aliceResp.User.ID != aliceID.String() {
		t.Fatalf("alice id: got %q want %q", aliceResp.User.ID, aliceID.String())
	}
	if bobResp.User.ID != bobID.String() {
		t.Fatalf("bob id: got %q want %q", bobResp.User.ID, bobID.String())
	}
	if aliceResp.User.Name != "alice" {
		t.Fatalf("alice name: got %q want alice", aliceResp.User.Name)
	}
	if bobResp.User.Name != "bob" {
		t.Fatalf("bob name: got %q want bob", bobResp.User.Name)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.createCalls != 1 {
		t.Fatalf("CreateUser calls: got %d want 1 (only alice is new)", mock.createCalls)
	}
	if len(mock.namesCreated) != 1 || mock.namesCreated[0] != "alice" {
		t.Fatalf("CreateUser arg names: got %v want [alice]", mock.namesCreated)
	}
}

// TestUsernameLoginRejectsEmptyName pins the input-validation guard at
// the top of the handler — a regression that drops the trim/empty check
// would silently create ""-named users.
func TestUsernameLoginRejectsEmptyName(t *testing.T) {
	mock := newLoginMockDB()
	h := newTestHandler(Config{AllowSignup: true})
	h.Queries = db.New(mock)

	_, status, raw := doLogin(t, h, "   ")
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400, body=%q", status, raw)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.getByEmailCalls != 0 || mock.createCalls != 0 {
		t.Fatalf("empty name must not hit DB: getByEmail=%d create=%d", mock.getByEmailCalls, mock.createCalls)
	}
}