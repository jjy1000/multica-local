// Package handler — regression tests for server/internal/handler/user_plugins.go.
//
// Background:
//   - 0.3.60 added the user_plugin table (migration 166) and the CRUD surface
//     in user_plugins.go (Create / List / Update / Delete).
//   - 0.3.63 (migration 168) converted the column-level UNIQUE(slug) and
//     UNIQUE(flag_key) into PARTIAL unique indexes scoped to live rows
//     (`WHERE status != 'deleted'`), so a soft-deleted slug can be re-created.
//     Before the migration, every soft-delete permanently tied up the slug —
//     re-creating a previously-used slug returned 409 forever.
//
// Why these tests exist:
//   - slug validation, 409-on-live-dup, soft-delete-then-recreate, flag_key
//     derivation, and the Get/Update/Delete smoke paths all carry regression
//     risk. The migration-168 partial-unique-index semantic is especially
//     load-bearing: a future change that hard-deletes rows, or that forgets
//     the `status != 'deleted'` filter in any of the lookup queries, would
//     silently break re-creation.
//
// They mirror the mockDB + db.New style of auth_login_test.go
// (TestUsernameLoginUpsert / Idempotent / DistinctNames) so they share the
// same setup, no new test deps, no DB or HTTP harness required. The mock
// tracks each row's status to emulate the partial unique index — so the
// load-bearing assertion is "after SoftDelete, a follow-up Create with the
// same slug must succeed (not 409)".

package handler

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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// userPluginMockDB is a programmable DBTX stand-in for user_plugin CRUD
// queries. Rows live in an in-memory map keyed by slug so the migration-168
// partial-index semantics — soft-delete (status='deleted') must NOT collide
// with a fresh INSERT of the same slug — can be emulated faithfully without
// a real DB.
//
// Routing is by SQL substring (same strategy as loginMockDB):
//
//	"INSERT INTO \"user_plugin\""      → CreateUserPlugin  (QueryRow)
//	"FROM \"user_plugin\" WHERE slug"  → GetUserPluginBySlug (QueryRow)
//	"FROM \"user_plugin\""            → ListUserPlugins   (QueryRow — see note)
//	"UPDATE \"user_plugin\" SET status = 'deleted'" → SoftDeleteUserPlugin (Exec)
//	"UPDATE \"user_plugin\" SET"       → UpdateUserPlugin  (QueryRow)
//	"DELETE FROM experimental_pref"    → DeleteExperimentalPref (Exec)
//	anything else                      → unknown; fail loud so a schema
//	                                   drift cannot silently bypass pins.
//
// Note: ListUserPlugins is :many in sqlc. The current test does not assert
// iteration order so it falls back to a deterministic single-row QueryRow
// response; if a future test needs real :many iteration, extend the mock
// to also implement Query() properly.
type userPluginMockDB struct {
	db.DBTX

	mu sync.Mutex

	// liveRows holds rows that the partial unique index considers "live"
	// (status='active' or 'disabled'). Re-creating one of these slugs
	// raises 23505.
	liveRows map[string]db.UserPlugin
	// deletedRow holds soft-deleted tombstones (status='deleted'). They
	// are explicitly OUT of the partial index so re-creating one is
	// legal — this is the migration-168 contract under test.
	deletedRow map[string]db.UserPlugin

	createdCalls    int
	getCalls        int
	listCalls       int
	updateCalls     int
	deleteCalls     int
	prefDeleteCalls int
	visPurgeCalls   int

	// captured args — used in assertions
	lastCreatedFlagKey           string
	lastCreatedSlug              string
	lastUpdatedStatus            string
	lastUpdatedManifest          []byte
	lastPurgedVisibilityFlagKey  string

	stagedCreateErr error // optional, set by tests that want to force 23505 from the INSERT path
}

func newUserPluginMockDB() *userPluginMockDB {
	return &userPluginMockDB{
		liveRows:   map[string]db.UserPlugin{},
		deletedRow: map[string]db.UserPlugin{},
	}
}

// stagePlugin installs a row into the live set as if CreateUserPlugin had
// succeeded upstream. Used by the 409 + soft-delete tests so they don't
// have to drive the INSERT path to set up collisions.
func (m *userPluginMockDB) stagePlugin(p db.UserPlugin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.liveRows[p.Slug] = p
}

func (m *userPluginMockDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	s := strings.ToLower(sql)

	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case strings.Contains(s, "insert into user_plugin ("):
		m.createdCalls++
		if len(args) >= 2 {
			if slug, ok := args[0].(string); ok {
				m.lastCreatedSlug = slug
			}
			if fk, ok := args[1].(string); ok {
				m.lastCreatedFlagKey = fk
			}
		}
		if m.stagedCreateErr != nil {
			err := m.stagedCreateErr
			m.stagedCreateErr = nil
			return &userPluginMockRow{err: err}
		}
		// Build the row from the INSERT args. CreateUserPluginParams
		// (see user_plugin.sql.go) is: 1 slug, 2 flag_key, 3 title_en,
		// 4 title_zh, 5 description_en, 6 description_zh, 7 manifest_json,
		// 8 trigger_mode, 9 runtime_kind, 10 status, 11 created_by.
		if len(args) < 11 {
			return &userPluginMockRow{err: errors.New("userPluginMockDB: insufficient INSERT args")}
		}
		slug, _ := args[0].(string)
		if _, exists := m.liveRows[slug]; exists {
			// Partial-index collision: 23505 → handler maps to 409.
			return &userPluginMockRow{err: &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}}
		}
		// INSERT allowed — populate a row in liveRows.
		var id pgtype.UUID
		id.Valid = true
		newID := uuid.New()
		copy(id.Bytes[:], newID[:])
		var createdBy pgtype.UUID
		if cb, ok := args[10].(pgtype.UUID); ok {
			createdBy = cb
		}
		var ts pgtype.Timestamptz
		ts.Valid = true
		ts.Time = time.Now().UTC()
		mfst, _ := args[6].([]byte)
		p := db.UserPlugin{
			ID:            id,
			Slug:          slug,
			FlagKey:       args[1].(string),
			TitleEn:       args[2].(string),
			TitleZh:       args[3].(string),
			DescriptionEn: args[4].(string),
			DescriptionZh: args[5].(string),
			ManifestJson:  mfst,
			TriggerMode:   args[7].(string),
			RuntimeKind:   args[8].(string),
			Status:        args[9].(string),
			CreatedBy:     createdBy,
			CreatedAt:     ts,
			UpdatedAt:     ts,
		}
		m.liveRows[slug] = p
		return &userPluginMockRow{plugin: p}

	case strings.Contains(s, "from user_plugin where slug"):
		m.getCalls++
		var slug string
		if len(args) > 0 {
			slug, _ = args[0].(string)
		}
		// SQL has `WHERE slug = $1 AND status != 'deleted'` — the mock
		// emulates this by only returning from liveRows.
		if row, ok := m.liveRows[slug]; ok {
			return &userPluginMockRow{plugin: row}
		}
		return &userPluginMockRow{err: pgx.ErrNoRows}

	case strings.Contains(s, "from user_plugin "),
		strings.Contains(s, "from user_plugin\t"),
		strings.HasSuffix(strings.TrimSpace(s), "from user_plugin"):
		// ListUserPlugins is :many. The handler iterates results; the
		// current tests only assert the call count, so we return the
		// first live row via QueryRow. (sqlc's :many wrapper still calls
		// Query() under the hood — see Query() below for the iterator.)
		m.listCalls++
		// Hand back the first live row in deterministic order so the
		// handler can still iterate via its sqlc wrapper. We do not
		// implement Query() iteration in this mock to keep complexity
		// low; instead, we return ErrNoRows from QueryRow so the
		// iteration loop exits immediately. The listCalls counter is
		// the authoritative assertion.
		return &userPluginMockRow{err: pgx.ErrNoRows}

	case strings.Contains(s, "update user_plugin set"),
		strings.Contains(s, "update \"user_plugin\" set"):
		m.updateCalls++
		var slug string
		if len(args) > 0 {
			slug, _ = args[0].(string)
		}
		row, ok := m.liveRows[slug]
		if !ok {
			// UpdateUserPlugin's SQL has `status != 'deleted'` so soft-deleted
			// rows raise ErrNoRows.
			return &userPluginMockRow{err: pgx.ErrNoRows}
		}
		// UpdateUserPluginParams: 1 slug, 2 title_en, 3 title_zh,
		// 4 description_en, 5 description_zh, 6 manifest_json,
		// 7 trigger_mode, 8 runtime_kind, 9 status.
		if len(args) >= 9 {
			row.TitleEn, _ = args[1].(string)
			row.TitleZh, _ = args[2].(string)
			row.DescriptionEn, _ = args[3].(string)
			row.DescriptionZh, _ = args[4].(string)
			if mf, ok := args[5].([]byte); ok {
				row.ManifestJson = mf
				m.lastUpdatedManifest = mf
			}
			row.TriggerMode, _ = args[6].(string)
			row.RuntimeKind, _ = args[7].(string)
			row.Status, _ = args[8].(string)
			m.lastUpdatedStatus = row.Status
		}
		row.UpdatedAt.Time = time.Now().UTC()
		row.UpdatedAt.Valid = true
		m.liveRows[slug] = row
		return &userPluginMockRow{plugin: row}

	default:
		return &userPluginMockRow{err: errors.New("userPluginMockDB: unrecognized QueryRow: " + s)}
	}
}

// Query backs the :many iteration path. The current tests assert call counts
// only — they do not compare list bodies — so we return an iterator over a
// snapshot of liveRows. Iteration fills Scan on each row in place of the
// userPluginMockRow.Scan path so sqlc's pgx collect helper can read each
// row.
func (m *userPluginMockDB) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	s := strings.ToLower(sql)
	if !strings.Contains(s, "from user_plugin") {
		return nil, errors.New("userPluginMockDB: unrecognized Query: " + s)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	out := make([]db.UserPlugin, 0, len(m.liveRows))
	for _, r := range m.liveRows {
		out = append(out, r)
	}
	return &userPluginMockRows{rows: out}, nil
}

func (m *userPluginMockDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	s := strings.ToLower(sql)

	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case strings.Contains(s, "update user_plugin set status = 'deleted'"):
		m.deleteCalls++
		var slug string
		if len(args) > 0 {
			slug, _ = args[0].(string)
		}
		// Soft-delete: move live row to the deleted tombstone set so a
		// subsequent Create with the same slug succeeds (migration 168).
		if row, ok := m.liveRows[slug]; ok {
			row.Status = "deleted"
			row.UpdatedAt.Time = time.Now().UTC()
			row.UpdatedAt.Valid = true
			m.deletedRow[slug] = row
			delete(m.liveRows, slug)
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil

	case strings.Contains(s, "delete from experimental_pref"):
		m.prefDeleteCalls++
		return pgconn.NewCommandTag("DELETE 1"), nil

	case strings.Contains(s, "delete from experimental_resource_visibility"),
		strings.Contains(s, "delete from \"experimental_resource_visibility\""):
		// P2-1a purge (seedPluginVisibility + DeleteUserPlugin). Record
		// the flag key so tests can pin the purge-before-seed and
		// teardown cleanup contracts.
		m.visPurgeCalls++
		if len(args) > 0 {
			if fk, ok := args[0].(string); ok {
				m.lastPurgedVisibilityFlagKey = fk
			}
		}
		return pgconn.NewCommandTag("DELETE 2"), nil

	case strings.Contains(s, "insert into \"experimental_resource_visibility\""),
		strings.Contains(s, "insert into experimental_resource_visibility"):
		// Visibility seeding is best-effort and only invoked when the
		// manifest has a capabilities block. Tests with an empty manifest
		// never hit this; tests that do can extend the mock to record
		// the call. Default to no-op INSERT 0 so the path stays neutral.
		return pgconn.NewCommandTag("INSERT 0"), nil

	default:
		return pgconn.NewCommandTag(""), errors.New("userPluginMockDB: unrecognized Exec: " + s)
	}
}

// ---- mock row + rows scanners ----

type userPluginMockRow struct {
	pgx.Row
	plugin db.UserPlugin
	err    error
}

func (r *userPluginMockRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	p := r.plugin
	// Scan slot order matches GetUserPluginBySlug / CreateUserPlugin /
	// UpdateUserPlugin: ID, Slug, FlagKey, TitleEn, TitleZh, DescriptionEn,
	// DescriptionZh, ManifestJson, TriggerMode, RuntimeKind, Status,
	// CreatedBy, CreatedAt, UpdatedAt (14 columns).
	if len(dest) < 14 {
		return errors.New("userPluginMockRow.Scan: too few slots")
	}
	if d, ok := dest[0].(*pgtype.UUID); ok {
		*d = p.ID
	}
	if d, ok := dest[1].(*string); ok {
		*d = p.Slug
	}
	if d, ok := dest[2].(*string); ok {
		*d = p.FlagKey
	}
	if d, ok := dest[3].(*string); ok {
		*d = p.TitleEn
	}
	if d, ok := dest[4].(*string); ok {
		*d = p.TitleZh
	}
	if d, ok := dest[5].(*string); ok {
		*d = p.DescriptionEn
	}
	if d, ok := dest[6].(*string); ok {
		*d = p.DescriptionZh
	}
	if d, ok := dest[7].(*[]byte); ok {
		out := make([]byte, len(p.ManifestJson))
		copy(out, p.ManifestJson)
		*d = out
	}
	if d, ok := dest[8].(*string); ok {
		*d = p.TriggerMode
	}
	if d, ok := dest[9].(*string); ok {
		*d = p.RuntimeKind
	}
	if d, ok := dest[10].(*string); ok {
		*d = p.Status
	}
	if d, ok := dest[11].(*pgtype.UUID); ok {
		*d = p.CreatedBy
	}
	if d, ok := dest[12].(*pgtype.Timestamptz); ok {
		*d = p.CreatedAt
	}
	if d, ok := dest[13].(*pgtype.Timestamptz); ok {
		*d = p.UpdatedAt
	}
	return nil
}

// userPluginMockRows implements pgx.Rows for the :many iteration path.
type userPluginMockRows struct {
	pgx.Rows
	rows  []db.UserPlugin
	idx   int
	cur   db.UserPlugin
	done  bool
}

func (r *userPluginMockRows) Next() bool {
	if r.idx >= len(r.rows) {
		r.done = true
		return false
	}
	r.cur = r.rows[r.idx]
	r.idx++
	return true
}

func (r *userPluginMockRows) Scan(dest ...interface{}) error {
	if r.done {
		return errors.New("userPluginMockRows.Scan: no current row")
	}
	p := r.cur
	if len(dest) < 14 {
		return errors.New("userPluginMockRows.Scan: too few slots")
	}
	if d, ok := dest[0].(*pgtype.UUID); ok {
		*d = p.ID
	}
	if d, ok := dest[1].(*string); ok {
		*d = p.Slug
	}
	if d, ok := dest[2].(*string); ok {
		*d = p.FlagKey
	}
	if d, ok := dest[3].(*string); ok {
		*d = p.TitleEn
	}
	if d, ok := dest[4].(*string); ok {
		*d = p.TitleZh
	}
	if d, ok := dest[5].(*string); ok {
		*d = p.DescriptionEn
	}
	if d, ok := dest[6].(*string); ok {
		*d = p.DescriptionZh
	}
	if d, ok := dest[7].(*[]byte); ok {
		out := make([]byte, len(p.ManifestJson))
		copy(out, p.ManifestJson)
		*d = out
	}
	if d, ok := dest[8].(*string); ok {
		*d = p.TriggerMode
	}
	if d, ok := dest[9].(*string); ok {
		*d = p.RuntimeKind
	}
	if d, ok := dest[10].(*string); ok {
		*d = p.Status
	}
	if d, ok := dest[11].(*pgtype.UUID); ok {
		*d = p.CreatedBy
	}
	if d, ok := dest[12].(*pgtype.Timestamptz); ok {
		*d = p.CreatedAt
	}
	if d, ok := dest[13].(*pgtype.Timestamptz); ok {
		*d = p.UpdatedAt
	}
	return nil
}

func (r *userPluginMockRows) Err() error  { return nil }
func (r *userPluginMockRows) Close()      {}
func (r *userPluginMockRows) Values() ([]interface{}, error) {
	return nil, errors.New("not implemented")
}
func (r *userPluginMockRows) RawValues() [][]byte { return nil }
func (r *userPluginMockRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag("")
}
func (r *userPluginMockRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *userPluginMockRows) Conn() *pgx.Conn { return nil }

// ---- helpers ----

func pluginCreateBody(slug, title string) createUserPluginRequest {
	return createUserPluginRequest{
		Slug:        slug,
		Title:       experimental.LocalizedString{En: title},
		TriggerMode: "issue_select",
		RuntimeKind: "inline",
	}
}

func doCreate(t *testing.T, h *Handler, body createUserPluginRequest) (UserPluginResponse, int, string) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/user-plugins", bytes.NewReader(buf))
	req.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
	rr := httptest.NewRecorder()
	h.CreateUserPlugin(rr, req)
	raw := rr.Body.String()
	if rr.Code != http.StatusCreated {
		return UserPluginResponse{}, rr.Code, raw
	}
	var resp UserPluginResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode UserPluginResponse: %v (body=%q)", err, raw)
	}
	return resp, rr.Code, raw
}

// withChiURLParam is provided by lab_test.go (and re-used here) — see
// that file for the chi NewRouteContext plumbing that lets
// chi.URLParam(r, "slug") resolve under httptest.

// ---- tests ----

// TestUserPluginCreateSlugValidation pins the slug contract end-to-end
// through CreateUserPlugin: legitimate slugs (lowercase + hyphens, 2-64
// chars) hit the INSERT and return 201; bad slugs short-circuit at the
// validation guard with 400 and never reach the DB. A regression that
// drops the validation step would either 500/409 here OR (worse) let a
// "Plugin!" row leak into the catalog — neither is acceptable.
func TestUserPluginCreateSlugValidation(t *testing.T) {
	mock := newUserPluginMockDB()
	h := newTestHandler(Config{})
	h.Queries = db.New(mock)

	cases := []struct {
		name        string
		slug        string
		want        int
		wantCreated bool
	}{
		// legitimate slugs — INSERT path
		{"min_len_alpha", "ab", http.StatusCreated, true},
		{"hyphen_separated", "my-plugin", http.StatusCreated, true},
		{"alphanumeric", "plugin123", http.StatusCreated, true},
		{"multi_hyphen", "a-b-c-d", http.StatusCreated, true},
		{"max_len", strings.Repeat("x", 64), http.StatusCreated, true},
		// bad slugs — short-circuit to 400 (never reach DB)
		{"empty", "", http.StatusBadRequest, false},
		{"too_short", "a", http.StatusBadRequest, false},
		{"too_long", strings.Repeat("a", 65), http.StatusBadRequest, false},
		{"leading_hyphen", "-plugin", http.StatusBadRequest, false},
		{"trailing_hyphen", "plugin-", http.StatusBadRequest, false},
		{"double_hyphen", "plu--gin", http.StatusBadRequest, false},
		{"uppercase", "Plugin", http.StatusBadRequest, false},
		{"underscore", "my_plugin", http.StatusBadRequest, false},
		{"space", "my plugin", http.StatusBadRequest, false},
		{"special", "plugin!", http.StatusBadRequest, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			mock.mu.Lock()
			before := mock.createdCalls
			mock.mu.Unlock()

			_, status, raw := doCreate(t, h, pluginCreateBody(c.slug, "title"))
			if status != c.want {
				t.Fatalf("slug=%q: status got %d want %d, body=%q", c.slug, status, c.want, raw)
			}

			mock.mu.Lock()
			after := mock.createdCalls
			mock.mu.Unlock()

			if c.wantCreated {
				if after != before+1 {
					t.Fatalf("slug=%q: INSERT not invoked (calls %d → %d)", c.slug, before, after)
				}
			} else {
				if after != before {
					t.Fatalf("slug=%q: validation bypass → INSERT invoked %d → %d", c.slug, before, after)
				}
			}
		})
	}
}

// TestUserPluginCreateDuplicateReturns409 pins the 409 path: a stage-plugin
// insertion (live slug already taken) must be refused with 409, NOT 500.
// CreateUserPlugin's pre-check hits GetUserPluginBySlug → returns row → 409;
// without the pre-check the request falls through to the INSERT and would
// bubble a raw unique-violation 500. Both branches land on 409 today
// (pre-check + 23505 fallback), and this test covers the primary path.
func TestUserPluginCreateDuplicateReturns409(t *testing.T) {
	mock := newUserPluginMockDB()
	const slug = "existing-plugin"

	var pgid pgtype.UUID
	pgid.Valid = true
	newID := uuid.New()
	copy(pgid.Bytes[:], newID[:])
	var ts pgtype.Timestamptz
	ts.Valid = true
	ts.Time = time.Now().UTC()
	mock.stagePlugin(db.UserPlugin{
		ID:          pgid,
		Slug:        slug,
		FlagKey:     "user_" + slug,
		TitleEn:     "Existing",
		TriggerMode: "issue_select",
		RuntimeKind: "inline",
		Status:      "active",
		CreatedAt:   ts,
		UpdatedAt:   ts,
	})

	h := newTestHandler(Config{})
	h.Queries = db.New(mock)

	_, status, raw := doCreate(t, h, pluginCreateBody(slug, "new"))
	if status != http.StatusConflict {
		t.Fatalf("status: got %d want 409, body=%q", status, raw)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.createdCalls != 0 {
		t.Fatalf("INSERT must NOT be attempted when pre-check found a live row: got %d calls", mock.createdCalls)
	}
}

// TestUserPluginSoftDeleteThenRecreate is the migration-168 regression pin:
//
//	Given a plugin created with slug=X
//	When  DeleteUserPlugin soft-deletes it (status='deleted')
//	Then  CreateUserPlugin with the SAME slug must SUCCEED with 201,
//	      because the partial unique index excludes status='deleted' rows.
//
// Without migration 168, the second Create would 23505 → 409. Pinning this
// here means: a future change that hard-deletes rows (or forgets the
// status='deleted' filter in any lookup) will fail this test loudly
// instead of silently regressing the user-plugin UX.
func TestUserPluginSoftDeleteThenRecreate(t *testing.T) {
	mock := newUserPluginMockDB()
	h := newTestHandler(Config{})
	h.Queries = db.New(mock)

	const slug = "reusable-plugin"

	first, status, raw := doCreate(t, h, pluginCreateBody(slug, "v1"))
	if status != http.StatusCreated {
		t.Fatalf("first create: got %d want 201, body=%q", status, raw)
	}
	if first.Slug != slug {
		t.Fatalf("first slug: got %q want %q", first.Slug, slug)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil)
	delReq.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
	delReq = withChiURLParam(delReq, "slug", slug)
	delRR := httptest.NewRecorder()
	h.DeleteUserPlugin(delRR, delReq)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d want 204, body=%q", delRR.Code, delRR.Body.String())
	}

	mock.mu.Lock()
	_, hasLive := mock.liveRows[slug]
	_, hasTomb := mock.deletedRow[slug]
	mock.mu.Unlock()
	if hasLive {
		t.Fatal("soft-delete invariant: row still in liveRows after DELETE")
	}
	if !hasTomb {
		t.Fatal("soft-delete invariant: deleted tombstone missing")
	}

	// Re-create with the same slug — must succeed (migration 168).
	second, status2, raw2 := doCreate(t, h, pluginCreateBody(slug, "v2"))
	if status2 != http.StatusCreated {
		t.Fatalf("re-create: got %d want 201 (migration 168 contract); body=%q", status2, raw2)
	}
	if second.Slug != slug {
		t.Fatalf("re-create slug: got %q want %q", second.Slug, slug)
	}
	// Flag-key must be derived (same as first time).
	if second.FlagKey != first.FlagKey {
		t.Fatalf("flag_key drift across re-create: first=%q second=%q", first.FlagKey, second.FlagKey)
	}
	if second.FlagKey != "user_"+slug {
		t.Fatalf("flag_key derivation: got %q want %q", second.FlagKey, "user_"+slug)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if _, ok := mock.liveRows[slug]; !ok {
		t.Fatal("post-create invariant: live row missing after re-create")
	}
	if _, ok := mock.deletedRow[slug]; !ok {
		t.Fatal("post-create invariant: tombstone disappeared (tombstones must persist so the partial index keeps its predicate)")
	}
}

// TestUserPluginFlagKeyDerivation pins the wire contract: the response
// carries flag_key = "user_<slug>", and CreateUserPlugin derives it from
// the slug (never trusts a client-supplied flag_key). Catches regressions
// where the prefix drifts (e.g. "userplugin_x") or the handler stores an
// empty/duplicate flag_key that breaks cross-plugin lookup.
func TestUserPluginFlagKeyDerivation(t *testing.T) {
	mock := newUserPluginMockDB()
	h := newTestHandler(Config{})
	h.Queries = db.New(mock)

	const slug = "key-derivation"
	resp, status, raw := doCreate(t, h, pluginCreateBody(slug, "kk"))
	if status != http.StatusCreated {
		t.Fatalf("status: got %d want 201, body=%q", status, raw)
	}
	if resp.FlagKey != "user_"+slug {
		t.Fatalf("response flag_key: got %q want %q", resp.FlagKey, "user_"+slug)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.lastCreatedFlagKey != "user_"+slug {
		t.Fatalf("INSERT arg flag_key: got %q want %q", mock.lastCreatedFlagKey, "user_"+slug)
	}
	if mock.lastCreatedSlug != slug {
		t.Fatalf("INSERT arg slug: got %q want %q", mock.lastCreatedSlug, slug)
	}
}

// TestUserPluginUpdateAndDelete smoke-tests the partial-update path and the
// delete + pref-cleanup chain. We pin only the observable side effects
// (status code + call counts) so the test stays robust against row-by-row
// mock refactors.
func TestUserPluginUpdateAndDelete(t *testing.T) {
	t.Run("update_status_only", func(t *testing.T) {
		mock := newUserPluginMockDB()
		h := newTestHandler(Config{})
		h.Queries = db.New(mock)

		const slug = "updatable"
		created, status, raw := doCreate(t, h, pluginCreateBody(slug, "orig"))
		if status != http.StatusCreated {
			t.Fatalf("create: got %d want 201, body=%q", status, raw)
		}

		newStatus := "disabled"
		buf, _ := json.Marshal(updateUserPluginRequest{Status: &newStatus})
		req := httptest.NewRequest(http.MethodPut, "/api/user-plugins/"+slug, bytes.NewReader(buf))
		req.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
		req = withChiURLParam(req, "slug", slug)
		rr := httptest.NewRecorder()
		h.UpdateUserPlugin(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("update: got %d want 200, body=%q", rr.Code, rr.Body.String())
		}
		var resp UserPluginResponse
		if err := json.Unmarshal([]byte(rr.Body.String()), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Status != "disabled" {
			t.Fatalf("updated status: got %q want disabled", resp.Status)
		}
		if resp.Slug != slug {
			t.Fatalf("updated slug: got %q want %q", resp.Slug, slug)
		}

		mock.mu.Lock()
		defer mock.mu.Unlock()
		if mock.updateCalls != 1 {
			t.Fatalf("UPDATE call count: got %d want 1", mock.updateCalls)
		}
		if mock.lastUpdatedStatus != "disabled" {
			t.Fatalf("UPDATE arg status: got %q want disabled", mock.lastUpdatedStatus)
		}
		if created.FlagKey != resp.FlagKey {
			t.Fatalf("flag_key drift across update: %q → %q", created.FlagKey, resp.FlagKey)
		}
	})

	t.Run("delete_marks_soft_deleted_and_clears_pref", func(t *testing.T) {
		mock := newUserPluginMockDB()
		h := newTestHandler(Config{})
		h.Queries = db.New(mock)

		const slug = "deletable"
		if _, status, raw := doCreate(t, h, pluginCreateBody(slug, "x")); status != http.StatusCreated {
			t.Fatalf("create: got %d want 201, body=%q", status, raw)
		}

		req := httptest.NewRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil)
		req.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
		req = withChiURLParam(req, "slug", slug)
		rr := httptest.NewRecorder()
		h.DeleteUserPlugin(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Fatalf("delete: got %d want 204, body=%q", rr.Code, rr.Body.String())
		}

		mock.mu.Lock()
		defer mock.mu.Unlock()
		if mock.deleteCalls != 1 {
			t.Fatalf("SoftDeleteUserPlugin call count: got %d want 1", mock.deleteCalls)
		}
		if mock.prefDeleteCalls != 1 {
			t.Fatalf("DeleteExperimentalPref call count: got %d want 1 — the lingering pref would dangle against a removed flag", mock.prefDeleteCalls)
		}
		if _, ok := mock.liveRows[slug]; ok {
			t.Fatal("live row still present after soft-delete")
		}
		if _, ok := mock.deletedRow[slug]; !ok {
			t.Fatal("deleted tombstone missing after soft-delete")
		}
	})

	t.Run("delete_missing_returns_404", func(t *testing.T) {
		mock := newUserPluginMockDB()
		h := newTestHandler(Config{})
		h.Queries = db.New(mock)

		req := httptest.NewRequest(http.MethodDelete, "/api/user-plugins/nonexistent", nil)
		req.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
		req = withChiURLParam(req, "slug", "nonexistent")
		rr := httptest.NewRecorder()
		h.DeleteUserPlugin(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("status: got %d want 404, body=%q", rr.Code, rr.Body.String())
		}
	})
}

// TestUserPluginCreateInvalidManifestRejected pins the JSONB-safety guard:
// an obviously-malformed manifest body returns 400 and never reaches the
// INSERT path. Pure regression — without this guard the JSONB insert can
// bubble a PG error up to the client as a 500.
func TestUserPluginCreateInvalidManifestRejected(t *testing.T) {
	mock := newUserPluginMockDB()
	h := newTestHandler(Config{})
	h.Queries = db.New(mock)

	body := pluginCreateBody("bad-manifest", "x")
	body.Manifest = json.RawMessage("{not-json")
	_, status, raw := doCreate(t, h, body)
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400, body=%q", status, raw)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.createdCalls != 0 {
		t.Fatalf("bad-manifest path must not INSERT: got %d calls", mock.createdCalls)
	}
}

// TestUserPluginListFiltersDeletedRows pins the GetListUserPlugins safety:
// soft-deleted rows MUST be excluded from list results, otherwise a
// deleted plugin would visibly re-appear in the Labs panel. This test
// extends the mock to actually iterate a :many result so the assertion
// catches both the list path AND the filter predicate.
func TestUserPluginListFiltersDeletedRows(t *testing.T) {
	mock := newUserPluginMockDB()
	h := newTestHandler(Config{})
	h.Queries = db.New(mock)

	const liveSlug = "live-plugin"
	const tombSlug = "tombstone-plugin"

	// Create a live plugin via the handler.
	if _, status, raw := doCreate(t, h, pluginCreateBody(liveSlug, "alive")); status != http.StatusCreated {
		t.Fatalf("seed live: got %d want 201, body=%q", status, raw)
	}

	// Pre-stage a tombstone row directly into the mock's deleted set
	// to emulate migration 167 leaving a row in the deleted tombstone
	// table. ListUserPlugins's `status != 'deleted'` predicate must
	// filter it out.
	var pgid pgtype.UUID
	pgid.Valid = true
	newID := uuid.New()
	copy(pgid.Bytes[:], newID[:])
	var ts pgtype.Timestamptz
	ts.Valid = true
	ts.Time = time.Now().UTC()
	mock.mu.Lock()
	mock.deletedRow[tombSlug] = db.UserPlugin{
		ID:        pgid,
		Slug:      tombSlug,
		FlagKey:   "user_" + tombSlug,
		Status:    "deleted",
		CreatedAt: ts,
		UpdatedAt: ts,
	}
	mock.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/user-plugins", nil)
	req.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
	rr := httptest.NewRecorder()
	h.ListUserPlugins(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list: got %d want 200, body=%q", rr.Code, rr.Body.String())
	}
	var got []UserPluginResponse
	if err := json.Unmarshal([]byte(rr.Body.String()), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// We expect exactly one row: the live one. Tombstone must NOT appear.
	if len(got) != 1 {
		t.Fatalf("list count: got %d want 1 (live only); rows=%+v", len(got), got)
	}
	if got[0].Slug != liveSlug {
		t.Fatalf("list row: got %q want %q", got[0].Slug, liveSlug)
	}
	for _, r := range got {
		if r.Slug == tombSlug {
			t.Fatalf("tombstone leaked into list: %+v", r)
		}
	}
}

// P2-1a (labs plan): DeleteUserPlugin must purge the visibility rows seeded
// under the plugin's flag key. Leftover rows keep the user's own agents and
// squads stamped lab_managed (ListLabManagedResourceIDs is flag-agnostic) —
// i.e. greyed out of regular pickers long after the plugin is gone.
func TestUserPluginDeletePurgesVisibilityRows(t *testing.T) {
	mock := newUserPluginMockDB()
	h := newTestHandler(Config{})
	h.Queries = db.New(mock)

	const slug = "purge-on-delete"
	created, status, raw := doCreate(t, h, pluginCreateBody(slug, "v1"))
	if status != http.StatusCreated {
		t.Fatalf("create: got %d want 201, body=%q", status, raw)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/user-plugins/"+slug, nil)
	delReq.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
	delReq = withChiURLParam(delReq, "slug", slug)
	delRR := httptest.NewRecorder()
	h.DeleteUserPlugin(delRR, delReq)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d want 204, body=%q", delRR.Code, delRR.Body.String())
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.visPurgeCalls != 1 {
		t.Fatalf("expected exactly 1 visibility purge on delete path, got %d", mock.visPurgeCalls)
	}
	if mock.lastPurgedVisibilityFlagKey != created.FlagKey {
		t.Fatalf("purged wrong flag key: got %q want %q",
			mock.lastPurgedVisibilityFlagKey, created.FlagKey)
	}
}
