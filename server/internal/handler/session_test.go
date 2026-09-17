package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
)

// The fork has no DB-backed handler fixture: RefreshSession reads nothing but
// the X-User-ID header the auth middleware stamps and the raw credential, so
// a bare Handler exercises the whole handler. The middleware hop itself is
// covered in internal/middleware's session_renewal tests.
const refreshTestUserID = "11111111-1111-1111-1111-111111111111"

func refreshSessionToken(t *testing.T, remaining time.Duration) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":   refreshTestUserID,
		"email": "user-1@multica.ai",
		"name":  "User One",
		"exp":   time.Now().Add(remaining).Unix(),
		"iat":   time.Now().Add(-time.Hour).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	return signed
}

func bearerRefreshRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", refreshTestUserID)
	return req
}

func callRefreshSession(t *testing.T, req *http.Request) (*httptest.ResponseRecorder, RefreshSessionResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	(&Handler{}).RefreshSession(rec, req)
	var resp RefreshSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return rec, resp
}

// Bearer mode inside the renewal window: the client gets the new string back
// and its lifetime restarts from the full TTL.
func TestRefreshSession_RenewsBearerSessionInsideWindow(t *testing.T) {
	old := refreshSessionToken(t, auth.AuthRenewThreshold()/2)

	rec, resp := callRefreshSession(t, bearerRefreshRequest(old))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !resp.Renewed {
		t.Fatalf("expected renewed=true, got %+v", resp)
	}
	if resp.Token == "" || resp.Token == old {
		t.Fatalf("expected a new token in the body, got %+v", resp)
	}

	claims, err := auth.ParseSessionToken(resp.Token)
	if err != nil {
		t.Fatalf("renewed token does not parse: %v", err)
	}
	if sub, _ := claims["sub"].(string); sub != refreshTestUserID {
		t.Errorf("renewed sub = %q, want %q", sub, refreshTestUserID)
	}
	if remaining := time.Until(auth.SessionExpiry(claims)); remaining <= auth.AuthRenewThreshold() {
		t.Errorf("renewed session has %s left, expected more than the renewal threshold", remaining)
	}
	if resp.CheckAgainInSeconds < 1 {
		t.Errorf("check_again_in_seconds = %d, want a positive cadence", resp.CheckAgainInSeconds)
	}
}

// Asking early is the normal answer, not an error: renewed=false plus the
// cadence to come back at.
func TestRefreshSession_ReportsUnrenewedOutsideWindow(t *testing.T) {
	fresh := refreshSessionToken(t, auth.AuthRenewThreshold()*2)

	rec, resp := callRefreshSession(t, bearerRefreshRequest(fresh))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if resp.Renewed || resp.Token != "" {
		t.Fatalf("expected renewed=false with no token, got %+v", resp)
	}
	if resp.CheckAgainInSeconds != int(auth.SessionRenewCheckInterval().Seconds()) {
		t.Errorf("check_again_in_seconds = %d, want the server-derived cadence %d",
			resp.CheckAgainInSeconds, int(auth.SessionRenewCheckInterval().Seconds()))
	}
}

// A cookie-authenticated browser gets its renewed session as a Set-Cookie.
// Handing the JWT back in a readable body would undo the point of the
// HttpOnly cookie.
func TestRefreshSession_CookieModeDoesNotLeakTheToken(t *testing.T) {
	token := refreshSessionToken(t, auth.AuthRenewThreshold()/2)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: auth.AuthCookieName, Value: token})
	req.Header.Set("X-User-ID", refreshTestUserID)

	rec, resp := callRefreshSession(t, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !resp.Renewed {
		t.Fatalf("expected renewed=true, got %+v", resp)
	}
	if resp.Token != "" {
		t.Error("a cookie-authenticated refresh must not return the JWT in the body")
	}

	var authCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.AuthCookieName {
			authCookie = c
		}
	}
	if authCookie == nil {
		t.Fatal("cookie mode must receive the renewed session as a Set-Cookie")
	}
	if !authCookie.HttpOnly {
		t.Error("the renewed cookie must stay HttpOnly")
	}
	if authCookie.Value == token {
		t.Error("the renewed cookie must carry a new token")
	}
}

// Every machine credential the auth middleware accepts authenticates here
// too. None of them may be exchanged for an interactive session: that would
// turn a scoped, revocable token into an unscoped, unrevocable one.
func TestRefreshSession_RejectsNonSessionCredentials(t *testing.T) {
	cases := map[string]string{
		"personal access token": "mul_0123456789abcdef0123456789abcdef01234567",
		"agent task token":      "mat_0123456789abcdef0123456789abcdef01234567",
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			req := bearerRefreshRequest(token)
			rec := httptest.NewRecorder()
			(&Handler{}).RefreshSession(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}

	t.Run("no credential at all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
		req.Header.Set("X-User-ID", refreshTestUserID)
		rec := httptest.NewRecorder()
		(&Handler{}).RefreshSession(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// An expired JWT never reaches this handler in production — the middleware
// rejects it — but if it ever did, refresh must not be the thing that brings
// it back to life.
func TestRefreshSession_DoesNotReviveExpiredSession(t *testing.T) {
	expired := refreshSessionToken(t, -time.Minute)

	rec, _ := callRefreshSession(t, bearerRefreshRequest(expired))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Defense in depth: X-User-ID is set by the middleware from these very
// claims, so a mismatch means a header was forged past it.
func TestRefreshSession_RejectsSubjectMismatch(t *testing.T) {
	signed := refreshSessionToken(t, auth.AuthRenewThreshold()/2)

	req := bearerRefreshRequest(signed)
	req.Header.Set("X-User-ID", "99999999-9999-9999-9999-999999999999")
	rec := httptest.NewRecorder()
	(&Handler{}).RefreshSession(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "does not belong to caller") {
		t.Errorf("body = %q, want the subject-mismatch message", rec.Body.String())
	}
}
