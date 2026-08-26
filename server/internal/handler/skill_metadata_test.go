package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newLargeSkillFixture builds a skill whose file bodies dwarf its metadata,
// which is the condition GH #7498 reports: the response was large because it
// inlined every body, and the command that would have said so was the one that
// could not finish.
func newLargeSkillFixture(t *testing.T) (skillID string, bodies map[string]string) {
	t.Helper()
	if testPool == nil {
		t.Skip("no database available")
	}

	name := "skill-metadata-fixture-" + t.Name()
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, 'fixture for include= tests', $3, '{}'::jsonb, $4)
		RETURNING id
	`, testWorkspaceID, name, strings.Repeat("s", 4096), testUserID).Scan(&skillID); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, skillID)
	})

	bodies = map[string]string{
		"reference.md":     strings.Repeat("r", 60_000),
		"scripts/setup.sh": strings.Repeat("x", 128),
	}
	for path, body := range bodies {
		if _, err := testPool.Exec(context.Background(),
			`INSERT INTO skill_file (skill_id, path, content) VALUES ($1, $2, $3)`,
			skillID, path, body); err != nil {
			t.Fatalf("insert skill_file %s: %v", path, err)
		}
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill_file WHERE skill_id = $1`, skillID)
	})
	return skillID, bodies
}

func skillRequest(t *testing.T, skillID, path string) *http.Request {
	t.Helper()
	return withURLParam(newRequest(http.MethodGet, path, nil), "id", skillID)
}

// fileHashesMatch is a stand-in for the testutil.Call/testutil.Decode wrapper
// in upstream — fork tests read the recorder body directly.
func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response (%d): %v", w.Code, err)
	}
	return out
}

func TestListSkillFilesMetadataOmitsBodies(t *testing.T) {
	skillID, bodies := newLargeSkillFixture(t)

	req := skillRequest(t, skillID, "/api/skills/"+skillID+"/files?include=metadata")
	w := httptest.NewRecorder()
	testHandler.ListSkillFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListSkillFiles: got %d, want 200: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON[[]SkillFileMetadataResponse](t, w)

	if len(resp) != len(bodies) {
		t.Fatalf("got %d files, want %d", len(resp), len(bodies))
	}
	for _, f := range resp {
		want, ok := bodies[f.Path]
		if !ok {
			t.Fatalf("unexpected file %q in response", f.Path)
		}
		if f.Size != int64(len(want)) {
			t.Errorf("%s: size = %d, want %d", f.Path, f.Size, len(want))
		}
		if expect := contentHash(want); f.ContentHash != expect {
			t.Errorf("%s: content_hash = %q, want %q", f.Path, f.ContentHash, expect)
		}
	}

	// Asserting on the response bytes rather than on a missing struct field is
	// deliberate: a body that reappeared under a different key would satisfy a
	// field-name check and still reproduce the bug.
	if strings.Contains(w.Body.String(), strings.Repeat("r", 1000)) {
		t.Error("response inlined a file body")
	}
	if got, limit := w.Body.Len(), 4096; got > limit {
		t.Errorf("metadata listing is %d bytes, want <= %d", got, limit)
	}
}

func TestListSkillFilesIncludeContentReturnsBodies(t *testing.T) {
	skillID, bodies := newLargeSkillFixture(t)

	req := skillRequest(t, skillID, "/api/skills/"+skillID+"/files?include=content")
	w := httptest.NewRecorder()
	testHandler.ListSkillFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListSkillFiles: got %d, want 200", w.Code)
	}
	resp := decodeJSON[[]SkillFileResponse](t, w)

	if len(resp) != len(bodies) {
		t.Fatalf("got %d files, want %d", len(resp), len(bodies))
	}
	for _, f := range resp {
		if want := bodies[f.Path]; f.Content != want {
			t.Errorf("%s: content length = %d, want %d", f.Path, len(f.Content), len(want))
		}
	}
}

// Neither endpoint may change what a request without `?include=` receives.
// Their callers are installed software — desktop builds, and older CLI
// versions whose `skill files list --output json` scripts read `content` — so
// a server deploy that flipped a default would silently take content away from
// clients that have no way to ask for it back.
func TestSkillEndpointsWithoutIncludeStillReturnContent(t *testing.T) {
	skillID, bodies := newLargeSkillFixture(t)

	t.Run("files listing", func(t *testing.T) {
		req := skillRequest(t, skillID, "/api/skills/"+skillID+"/files")
		w := httptest.NewRecorder()
		testHandler.ListSkillFiles(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ListSkillFiles: got %d, want 200", w.Code)
		}
		resp := decodeJSON[[]SkillFileResponse](t, w)
		if len(resp) != len(bodies) {
			t.Fatalf("got %d files, want %d", len(resp), len(bodies))
		}
		for _, f := range resp {
			if want := bodies[f.Path]; f.Content != want {
				t.Errorf("%s: content length = %d, want %d", f.Path, len(f.Content), len(want))
			}
		}
	})

	t.Run("skill detail", func(t *testing.T) {
		req := skillRequest(t, skillID, "/api/skills/"+skillID)
		w := httptest.NewRecorder()
		testHandler.GetSkill(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GetSkill: got %d, want 200", w.Code)
		}
		resp := decodeJSON[SkillWithFilesResponse](t, w)
		if len(resp.Content) != 4096 {
			t.Errorf("content length = %d, want 4096", len(resp.Content))
		}
		for _, f := range resp.Files {
			if want := bodies[f.Path]; f.Content != want {
				t.Errorf("%s: content length = %d, want %d", f.Path, len(f.Content), len(want))
			}
		}
	})
}

func TestGetSkillIncludeMetadataDropsBodies(t *testing.T) {
	skillID, _ := newLargeSkillFixture(t)

	req := skillRequest(t, skillID, "/api/skills/"+skillID+"?include=metadata")
	w := httptest.NewRecorder()
	testHandler.GetSkill(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetSkill: got %d, want 200: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON[SkillWithFileMetadataResponse](t, w)

	if resp.ID != skillID {
		t.Errorf("id = %q, want %q", resp.ID, skillID)
	}
	if resp.ContentSize != 4096 {
		t.Errorf("content_size = %d, want 4096", resp.ContentSize)
	}
	if want := contentHash(strings.Repeat("s", 4096)); resp.ContentHash != want {
		t.Errorf("content_hash = %q, want %q", resp.ContentHash, want)
	}

	if strings.Contains(w.Body.String(), strings.Repeat("s", 1000)) {
		t.Error("response inlined the SKILL.md body")
	}
	if got, limit := w.Body.Len(), 4096; got > limit {
		t.Errorf("metadata response is %d bytes, want <= %d", got, limit)
	}
}

// The size and hash are computed in SQL, so they must agree with Go's view of
// the same bytes for every body a skill can legally hold.
//
// `sha256(content::bytea)` did not: that cast runs the bytea *input* parser
// over the text, so `\x41` hashed as the single byte `A`, and any bare
// backslash — a regex, a Windows path — failed the whole request with "invalid
// input syntax for type bytea". Skill files are full of both.
func TestSkillFileHashCoversRawUTF8Bytes(t *testing.T) {
	if testPool == nil {
		t.Skip("no database available")
	}

	const skillBody = `SKILL.md with \x41 and C:\path`
	bodies := map[string]string{
		"hex-escape.md":     `\x41`,
		"invalid-hex.md":    `\xzz`,
		"bare-backslash.md": `C:\Users\skill`,
		"regex.md":          `^\d+\s*$`,
		"unicode.md":        "héllo 世界 🌍",
		"empty.md":          "",
	}

	var skillID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, 'skill-hash-fixture-' || gen_random_uuid()::text, 'bodies that break a bytea cast', $2, '{}'::jsonb, $3)
		RETURNING id
	`, testWorkspaceID, skillBody, testUserID).Scan(&skillID); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, skillID)
	})
	for path, body := range bodies {
		if _, err := testPool.Exec(context.Background(),
			`INSERT INTO skill_file (skill_id, path, content) VALUES ($1, $2, $3)`,
			skillID, path, body); err != nil {
			t.Fatalf("insert skill_file %s: %v", path, err)
		}
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill_file WHERE skill_id = $1`, skillID)
	})

	req := skillRequest(t, skillID, "/api/skills/"+skillID+"/files?include=metadata")
	w := httptest.NewRecorder()
	testHandler.ListSkillFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListSkillFiles: got %d, want 200", w.Code)
	}
	resp := decodeJSON[[]SkillFileMetadataResponse](t, w)

	if len(resp) != len(bodies) {
		t.Fatalf("got %d files, want %d", len(resp), len(bodies))
	}
	for _, f := range resp {
		want := bodies[f.Path]
		if f.Size != int64(len(want)) {
			t.Errorf("%s: size = %d, want %d", f.Path, f.Size, len(want))
		}
		if expect := contentHash(want); f.ContentHash != expect {
			t.Errorf("%s (%q): content_hash = %q, want %q", f.Path, want, f.ContentHash, expect)
		}
	}

	// Same bytes, same rule, for the SKILL.md body — that one is hashed in Go,
	// so this pins the SQL and Go implementations to each other.
	w2 := httptest.NewRecorder()
	testHandler.GetSkill(w2, skillRequest(t, skillID, "/api/skills/"+skillID+"?include=metadata"))
	if w2.Code != http.StatusOK {
		t.Fatalf("GetSkill: got %d, want 200", w2.Code)
	}
	detail := decodeJSON[SkillWithFileMetadataResponse](t, w2)
	if detail.ContentSize != int64(len(skillBody)) {
		t.Errorf("content_size = %d, want %d", detail.ContentSize, len(skillBody))
	}
	if want := contentHash(skillBody); detail.ContentHash != want {
		t.Errorf("content_hash = %q, want %q", detail.ContentHash, want)
	}
}

func TestSkillIncludeRejectsUnknownValue(t *testing.T) {
	skillID, _ := newLargeSkillFixture(t)

	w := httptest.NewRecorder()
	testHandler.GetSkill(w, skillRequest(t, skillID, "/api/skills/"+skillID+"?include=everything"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("GetSkill ?include=everything: got %d, want 400", w.Code)
	}

	w2 := httptest.NewRecorder()
	testHandler.ListSkillFiles(w2, skillRequest(t, skillID, "/api/skills/"+skillID+"/files?include=everything"))
	if w2.Code != http.StatusBadRequest {
		t.Errorf("ListSkillFiles ?include=everything: got %d, want 400", w2.Code)
	}
}