package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/llmwiki"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestLLMWikiFlagOn_PerUserPref_Respected is the regression test
// for the 0.5.13+ llm_wiki_bridge hot bug: the FlagOn closure on
// RegisterLLMWikiBridgeRoutes used experimental.DefaultFor(...) —
// which is always false for every current flag — so every bridge
// call returned ErrFlagDisabled even after the user enabled the
// lab in the GUI.
//
// The fix routes the gate through experimentalFlagEnabled + the
// request context's user-id, so an explicit per-user
// experimental_pref.row.enabled flips the gate on. This file
// pins the wiring.
//
// We construct the closure shape directly (llmWikiFlagOnFor wires
// experimentalFlagEnabled(ctx, q, userIDFromCtx(ctx), key)) instead
// of exercising the full chi middleware stack, because the
// middleware is the proven path (router.go:885 +
// RequireExperimentalFlag + experimental_guard_test.go).
//
// fakeExperimentalQuerier is declared in experimental_guard_test.go
// and implements experimental.Querier; guardTestUserID is a valid
// UUID parseUUID accepts.
func TestLLMWikiFlagOn_PerUserPref_Respected(t *testing.T) {
	const storedKey = "llm_wiki_bridge"

	// closure is the body of llmWikiFlagOnFor's returned func. We
	// build it once per table row so the test mirrors what runs in
	// production (ctx -> userIDFromCtx -> experimentalFlagEnabled).
	closure := func(q experimental.Querier, ctx context.Context) bool {
		return experimentalFlagEnabled(ctx, q, userIDFromLLMWikiCtx(ctx), storedKey)
	}

	tests := []struct {
		name    string
		ctx     context.Context
		querier func() *fakeExperimentalQuerier
		want    bool
	}{
		{
			// The hot bug shape: an unauthenticated caller (no
			// user-id on ctx) must resolve to the catalog default,
			// which is off. This keeps the gate fail-closed for any
			// path that forgot to stamp the user-id.
			name: "empty ctx user-id falls through to catalog default",
			ctx:  context.Background(),
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{}
			},
			want: false,
		},
		{
			// The fix shape: a stored enabled=true + a matching
			// user-id on ctx returns true. This is what the GUI
			// opt-in produces.
			name: "stored enabled + ctx user-id returns true",
			ctx:  withLLMWikiUserID(context.Background(), guardTestUserID),
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{
					row: db.ExperimentalPref{FlagKey: storedKey, Enabled: true},
				}
			},
			want: true,
		},
		{
			// Explicit opt-out stays off even with a valid ctx
			// user-id — the user explicitly disabled the lab.
			name: "stored disabled + ctx user-id returns false",
			ctx:  withLLMWikiUserID(context.Background(), guardTestUserID),
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{
					row: db.ExperimentalPref{FlagKey: storedKey, Enabled: false},
				}
			},
			want: false,
		},
		{
			// No row in experimental_pref + a valid ctx user-id
			// falls through to the catalog default (off). The
			// "I never touched the toggle" state must keep the
			// gate off; this is the safe default.
			name: "no stored pref + ctx user-id falls through to catalog default",
			ctx:  withLLMWikiUserID(context.Background(), guardTestUserID),
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{err: pgx.ErrNoRows}
			},
			want: false,
		},
		{
			// A transient DB error must NOT surface the flag —
			// conservative default is off. Mirrors the
			// experimental_guard_test.go "db error falls back to
			// catalog default" case.
			name: "db error + ctx user-id falls through to catalog default",
			ctx:  withLLMWikiUserID(context.Background(), guardTestUserID),
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{err: context.DeadlineExceeded}
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.querier()
			got := closure(q, tt.ctx)
			if got != tt.want {
				t.Fatalf("gate = %v, want %v", got, tt.want)
			}
			// When a user-id is on ctx the gate must query with
			// the resolved flag key so per-flag preferences
			// don't cross-wire (mirrors experimental_guard_test.go
			// assertion on q.gotParams.FlagKey).
			if userIDFromLLMWikiCtx(tt.ctx) != "" && q.gotParams.FlagKey != storedKey {
				t.Fatalf("querier called with flag %q, want %q", q.gotParams.FlagKey, storedKey)
			}
		})
	}
}

// TestLLMWikiFlagOn_NilQuerierKeepsCatalogDefault guards the
// boot path where the Handler has no Queries wired (the
// experimentalFlagEnabled helper already falls through to the
// catalog default in that case). Without this, RegisterLLMWikiBridgeRoutes
// could panic on early-boot callers if h.Queries were nil.
func TestLLMWikiFlagOn_NilQuerierKeepsCatalogDefault(t *testing.T) {
	got := llmWikiFlagOnFor(nil)(withLLMWikiUserID(context.Background(), guardTestUserID))
	if got {
		t.Fatalf("nil querier + valid ctx user-id should resolve to catalog default (false)")
	}
}

// TestLLMWikiFailureClassification pins the /status failure codes
// the renderer's warning cards key off (0.5.92): the three user
//-facing states — client missing, installed but not launched,
// running but unauthorized — must never collapse into one another.
func TestLLMWikiFailureClassification(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		installed bool
		want      string
	}{
		{"healthy", nil, true, "ok"},
		{"token rejected", llmwiki.ErrUnauthorized, true, "unauthorized"},
		{"token rejected still beats not-installed", llmwiki.ErrUnauthorized, false, "unauthorized"},
		{"probe error, client absent", context.DeadlineExceeded, false, "not_installed"},
		{"probe error, client present but down", context.DeadlineExceeded, true, "not_running"},
	}
	for _, tc := range cases {
		if got := classifyLLMWikiFailure(tc.err, tc.installed); got != tc.want {
			t.Errorf("%s: classifyLLMWikiFailure = %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, f := range []string{"not_installed", "not_running", "unauthorized"} {
		if strings.TrimSpace(llmWikiFailureHint(f)) == "" {
			t.Errorf("failure %q must ship a remediation hint", f)
		}
	}
	if llmWikiFailureHint("ok") != "" {
		t.Errorf("ok must not carry a hint")
	}
}

// TestLLMWikiTokenEndpoints exercises the 0.5.92 key-paste surface
// (Labs → LLM Wiki page / `multica experimental llm-wiki token`)
// against a redirected store: empty paste 400s, a valid paste wins
// discovery for the user source, and DELETE falls back to none.
func TestLLMWikiTokenEndpoints(t *testing.T) {
	t.Setenv("LLM_WIKI_API_TOKEN", "")
	dir := t.TempDir()
	llmwiki.SetHomeDirForTest(dir)
	t.Cleanup(func() { llmwiki.SetHomeDirForTest("") })
	store := filepath.Join(dir, ".multica", "llm-wiki.json")
	llmwiki.SetTokenStorePath(store)
	t.Cleanup(func() { llmwiki.SetTokenStorePath("") })

	h := &Handler{}

	// Initial state: nothing configured anywhere.
	w := httptest.NewRecorder()
	h.GetLLMWikiToken(w, newRequest(http.MethodGet, "/api/experimental/llm-wiki/token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET token: %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Configured bool   `json:"configured"`
		Source     string `json:"source"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Configured || got.Source != "none" {
		t.Fatalf("initial token state = %+v, want configured=false source=none", got)
	}
	if strings.Contains(w.Body.String(), "tok-") {
		t.Fatalf("token endpoint must never echo the token: %s", w.Body.String())
	}

	// Empty paste is a 400, not a store write.
	w = httptest.NewRecorder()
	h.SetLLMWikiToken(w, newRequest(http.MethodPost, "/api/experimental/llm-wiki/token", map[string]any{"token": "   "}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST empty token: expected 400, got %d", w.Code)
	}

	// Valid paste persists and reports the user source.
	w = httptest.NewRecorder()
	h.SetLLMWikiToken(w, newRequest(http.MethodPost, "/api/experimental/llm-wiki/token", map[string]any{"token": "tok-pasted"}))
	if w.Code != http.StatusOK {
		t.Fatalf("POST token: %d: %s", w.Code, w.Body.String())
	}
	b, err := os.ReadFile(store)
	if err != nil || !strings.Contains(string(b), "tok-pasted") {
		t.Fatalf("token store missing after POST: %v", err)
	}
	w = httptest.NewRecorder()
	h.GetLLMWikiToken(w, newRequest(http.MethodGet, "/api/experimental/llm-wiki/token", nil))
	got = struct {
		Configured bool   `json:"configured"`
		Source     string `json:"source"`
	}{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Configured || got.Source != "user" {
		t.Fatalf("after paste: %+v, want configured=true source=user", got)
	}

	// DELETE clears; with an empty redirected home nothing else
	// resolves, so discovery reports none.
	w = httptest.NewRecorder()
	h.ClearLLMWikiToken(w, newRequest(http.MethodDelete, "/api/experimental/llm-wiki/token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE token: %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("token store should be removed, stat err=%v", err)
	}
	w = httptest.NewRecorder()
	h.GetLLMWikiToken(w, newRequest(http.MethodGet, "/api/experimental/llm-wiki/token", nil))
	got = struct {
		Configured bool   `json:"configured"`
		Source     string `json:"source"`
	}{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Configured || got.Source != "none" {
		t.Fatalf("after clear: %+v, want configured=false source=none", got)
	}
}
