package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
)

// TestForgedAgentIdentityHeadersResolveAsMember is the actor-forgery regression
// for MUL-3428 (upstream 3ac53a68a, shaped after upstream's
// autopilot_actor_forgery_test.go): a plain JWT request carrying a real,
// member-observable (agent_id, task_id) pair — both ids are returned by
// GET /api/issues/{id}/task-runs, so "secret" is exactly what they are not —
// must resolve as the MEMBER, never as the agent. The Auth middleware stripping
// both headers is what closes the impersonation path; resolveActor's
// pair-matching fallback survives only for in-process callers and tests that
// set the headers directly.
//
// The two halves are mirror images over the SAME (agent, task) pair: once
// replayed as headers on a JWT (must be ignored), once stamped by an mat_ task
// token (must be honored — see TestResolveActor_TrustsTaskTokenStamp, the
// pre-existing pin on the stamp path).
//
// Fork port note: the upstream test drove this through the full router plus the
// MUL-7108 autopilot grant surface (can_write / webhook redaction), which is a
// separate upstream change this batch does not port. The load-bearing
// assertion — forged pair on a plain JWT resolves as the member — is pinned
// here at the middleware + resolveActor seam instead. Pure unit: the stripped
// request never reaches the database, so no DB fixture is required.
func TestForgedAgentIdentityHeadersResolveAsMember(t *testing.T) {
	const (
		forgeAgentID = "11111111-1111-1111-1111-111111111111"
		forgeTaskID  = "22222222-2222-2222-2222-222222222222"
		forgeUserID  = "33333333-3333-3333-3333-333333333333"
	)

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": forgeUserID,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	signed, err := tok.SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	var actorType, actorID string
	mw := middleware.Auth(nil, nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// resolveActor is exactly what downstream authorship/ownership code
		// (CreateIssue, CreateComment, subscriber self-subscribe, ...) calls.
		h := &Handler{}
		actorType, actorID = h.resolveActor(r, requestUserID(r), "ws-forgery-test")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	// The forgery: a real, member-readable pair replayed as headers.
	req.Header.Set("X-Agent-ID", forgeAgentID)
	req.Header.Set("X-Task-ID", forgeTaskID)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if actorType != "member" {
		t.Fatalf("actor = %q (%s), want member — the forged pair must not lend agent identity to a JWT request", actorType, actorID)
	}
	if actorID != forgeUserID {
		t.Fatalf("actor id = %q, want the JWT member %q", actorID, forgeUserID)
	}
}
