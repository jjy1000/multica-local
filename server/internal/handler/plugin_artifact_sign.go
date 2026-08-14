package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Signed artifact URLs (0.5.18 M5). A file-backed artifact raw URL is
// normally served only to Bearer-authenticated callers, but an <img> /
// <iframe> / <a download> element cannot attach the Authorization header.
// The /sign endpoint mints a short-lived, self-contained URL whose HMAC
// proves the caller obtained it through the authenticated endpoint, so the
// raw handler can serve it without a Bearer token.

// pluginArtifactSignTTL is the lifetime of a signed artifact URL.
const pluginArtifactSignTTL = 5 * time.Minute

var (
	pluginArtifactSignSecretOnce sync.Once
	pluginArtifactSignSecretVal  []byte
)

// pluginArtifactSignSecret returns the process-wide signing secret (32 random
// bytes). It is generated once per process so a server restart invalidates
// every outstanding signed URL.
func pluginArtifactSignSecret() []byte {
	pluginArtifactSignSecretOnce.Do(func() {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			// crypto/rand.Read cannot fail on supported platforms; panic
			// instead of silently serving with a nil/empty secret.
			panic(fmt.Sprintf("plugin artifact sign: read secret: %v", err))
		}
		pluginArtifactSignSecretVal = secret
	})
	return pluginArtifactSignSecretVal
}

// pluginArtifactSignMessage builds the canonical signed message. Every field
// is inside the HMAC so tampering with uid/slug/artifactID/exp invalidates
// the signature. Fields cannot contain the "|" delimiter (uid is a UUID,
// slug is a validated plugin slug, artifactID is hex, exp is a base-10 int).
func pluginArtifactSignMessage(uid, slug, artifactID string, exp int64) string {
	return fmt.Sprintf("%s|%s|%s|%d", uid, slug, artifactID, exp)
}

// pluginArtifactSignMAC computes the raw HMAC-SHA256 over the signed message.
func pluginArtifactSignMAC(uid, slug, artifactID string, exp int64) []byte {
	mac := hmac.New(sha256.New, pluginArtifactSignSecret())
	mac.Write([]byte(pluginArtifactSignMessage(uid, slug, artifactID, exp)))
	return mac.Sum(nil)
}

// signPluginArtifactURL returns the hex-encoded HMAC for a signed artifact URL.
func signPluginArtifactURL(uid, slug, artifactID string, exp int64) string {
	return hex.EncodeToString(pluginArtifactSignMAC(uid, slug, artifactID, exp))
}

// verifyPluginArtifactSignature reports whether sig is a valid, unexpired
// HMAC for the given uid/slug/artifactID/exp tuple. The HMAC comparison uses
// crypto/subtle.ConstantTimeCompare so the length of the comparison does not
// leak prefix information.
func verifyPluginArtifactSignature(uid, slug, artifactID, sig string, exp int64) bool {
	if exp <= time.Now().Unix() {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	want := pluginArtifactSignMAC(uid, slug, artifactID, exp)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// SignPluginArtifact mints a short-lived signed URL for a file-backed
// artifact. It is Bearer-authenticated and refuses to sign artifacts that do
// not exist in the plugin's index.
// POST /api/user-plugins/{slug}/artifacts/{artifactID}/sign
func (h *Handler) SignPluginArtifact(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return
	}

	slug := chi.URLParam(r, "slug")
	artifactID := chi.URLParam(r, "artifactID")
	if !validateUserPluginSlug(slug) || artifactID == "" {
		writeError(w, http.StatusBadRequest, "invalid plugin slug or artifact ID")
		return
	}

	dir, err := pluginArtifactDir(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve artifact directory")
		return
	}

	items, err := loadArtifactIndex(dir)
	if err != nil {
		slog.Error("plugin artifacts: load index for sign", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read artifact index")
		return
	}

	var target *storedArtifact
	for i := range items {
		if items[i].ID == artifactID {
			target = &items[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}

	exp := time.Now().Add(pluginArtifactSignTTL).Unix()
	sig := signPluginArtifactURL(uid, slug, artifactID, exp)
	signedPath := fmt.Sprintf(
		"/api/user-plugins/%s/artifacts/%s/raw?sig=%s&exp=%s&uid=%s",
		slug, artifactID, sig, strconv.FormatInt(exp, 10), url.QueryEscape(uid),
	)
	writeJSON(w, http.StatusOK, map[string]any{"url": signedPath, "exp": exp})
}
