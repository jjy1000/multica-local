package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// TestSignPluginArtifactURLVerify pins the HMAC sign/verify contract: only a
// signature minted for the exact (uid, slug, artifactID, exp) tuple with an
// unexpired timestamp verifies. The comparison itself is constant-time.
func TestSignPluginArtifactURLVerify(t *testing.T) {
	const (
		uid        = "user-123"
		slug       = "demo"
		artifactID = "1a2b3c4d"
	)

	validExp := time.Now().Add(5 * time.Minute).Unix()
	validSig := signPluginArtifactURL(uid, slug, artifactID, validExp)

	pastExp := time.Now().Add(-time.Minute).Unix()
	pastSig := signPluginArtifactURL(uid, slug, artifactID, pastExp)

	cases := []struct {
		name       string
		uid        string
		slug       string
		artifactID string
		sig        string
		exp        int64
		want       bool
	}{
		{"happy path", uid, slug, artifactID, validSig, validExp, true},
		{"tampered signature", uid, slug, artifactID, "deadbeef", validExp, false},
		{"tampered uid", "other-user", slug, artifactID, validSig, validExp, false},
		{"tampered slug", uid, "other", artifactID, validSig, validExp, false},
		{"cross artifact", uid, slug, "different-id", validSig, validExp, false},
		{"expired", uid, slug, artifactID, pastSig, pastExp, false},
		{"non-hex signature", uid, slug, artifactID, "zzzz", validExp, false},
		{"wrong-length signature", uid, slug, artifactID, "ab", validExp, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := verifyPluginArtifactSignature(c.uid, c.slug, c.artifactID, c.sig, c.exp); got != c.want {
				t.Fatalf("verifyPluginArtifactSignature(%q, %q, %q, %q, %d) = %v, want %v",
					c.uid, c.slug, c.artifactID, c.sig, c.exp, got, c.want)
			}
		})
	}
}

// TestServePluginArtifactRawSignedPath exercises the signed branch of the raw
// endpoint. The happy-path file-serve (200 + nosniff + no attachment
// disposition) is not asserted here because pluginArtifactDir is a package
// function that always resolves under the user's home dir (not injectable);
// instead these cases prove the auth-skip and signature-verify contract:
//   - a tampered signature is 403 WITHOUT a Bearer header (so the signed
//     branch runs before requireUserID);
//   - a valid signature falls through to artifact lookup (404 for a missing
//     artifact), never 401;
//   - an unsigned request still demands Bearer auth (401).
func TestServePluginArtifactRawSignedPath(t *testing.T) {
	h := &Handler{}
	uid := "user-123"
	// A nanosecond-unique slug keeps the test off the user's real plugin dirs.
	slug := "sigtest" + strconv.FormatInt(time.Now().UnixNano(), 10)
	artifactID := "1a2b3c4d"

	buildReq := func(sig string, exp int64, queryUID string) *http.Request {
		target := fmt.Sprintf("/api/user-plugins/%s/artifacts/%s/raw", slug, artifactID)
		if sig != "" {
			target += fmt.Sprintf("?sig=%s&exp=%d&uid=%s", sig, exp, url.QueryEscape(queryUID))
		}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		return withURLParams(req, "slug", slug, "artifactID", artifactID)
	}

	t.Run("tampered signature rejected without bearer", func(t *testing.T) {
		req := buildReq("deadbeef", time.Now().Add(5*time.Minute).Unix(), uid)
		w := httptest.NewRecorder()
		h.ServePluginArtifactRaw(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("expired signature rejected without bearer", func(t *testing.T) {
		// The TTL boundary belongs in the request path too: prove the raw
		// endpoint enforces expiry BEFORE artifact lookup / any file IO,
		// not just inside verifyPluginArtifactSignature's unit contract.
		exp := time.Now().Add(-time.Minute).Unix()
		sig := signPluginArtifactURL(uid, slug, artifactID, exp)
		w := httptest.NewRecorder()
		h.ServePluginArtifactRaw(w, buildReq(sig, exp, uid))
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for an expired signature, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("valid signature skips bearer and reaches artifact lookup", func(t *testing.T) {
		exp := time.Now().Add(5 * time.Minute).Unix()
		sig := signPluginArtifactURL(uid, slug, artifactID, exp)
		w := httptest.NewRecorder()
		h.ServePluginArtifactRaw(w, buildReq(sig, exp, uid))
		// No matching index entry → 404 (NOT 401): the signature verified and
		// Bearer auth was skipped.
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("unsigned request still requires bearer", func(t *testing.T) {
		req := withURLParams(
			httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/api/user-plugins/%s/artifacts/%s/raw", slug, artifactID), nil),
			"slug", slug, "artifactID", artifactID,
		)
		w := httptest.NewRecorder()
		h.ServePluginArtifactRaw(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestSignPluginArtifactAuth pins the sign endpoint's Bearer gate and its
// refusal to sign artifacts missing from the plugin index.
func TestSignPluginArtifactAuth(t *testing.T) {
	h := &Handler{}
	slug := "sigtest" + strconv.FormatInt(time.Now().UnixNano(), 10)
	artifactID := "1a2b3c4d"

	t.Run("missing bearer rejected", func(t *testing.T) {
		req := withURLParams(
			httptest.NewRequest(http.MethodPost,
				fmt.Sprintf("/api/user-plugins/%s/artifacts/%s/sign", slug, artifactID), nil),
			"slug", slug, "artifactID", artifactID,
		)
		w := httptest.NewRecorder()
		h.SignPluginArtifact(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("nonexistent artifact rejected", func(t *testing.T) {
		req := withURLParams(
			httptest.NewRequest(http.MethodPost,
				fmt.Sprintf("/api/user-plugins/%s/artifacts/%s/sign", slug, artifactID), nil),
			"slug", slug, "artifactID", artifactID,
		)
		req.Header.Set("X-User-ID", "user-123")
		w := httptest.NewRecorder()
		h.SignPluginArtifact(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
		}
	})
}
