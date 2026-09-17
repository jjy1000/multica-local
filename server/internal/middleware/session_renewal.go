package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
)

// renewCookieSession re-issues the browser's auth cookie when the session
// that authenticated this request has dropped below half its TTL. It returns
// the request to pass downstream — the same one when nothing was renewed.
//
// Three conditions gate it, and each rules out a specific way this could go
// wrong:
//
//   - Cookie auth only. A bearer-token client keeps its session in its own
//     storage; a Set-Cookie it never reads would leave the two copies
//     disagreeing about when the session ends. Those clients call
//     POST /api/auth/refresh instead.
//   - Safe methods only. Re-issuing the cookie also rotates the CSRF cookie
//     that is HMAC-bound to the token value, so confining it to methods that
//     carry no CSRF check means a rotation can never invalidate the very
//     request that triggered it — and every request composes its auth cookie
//     and CSRF header from one cookie-jar snapshot, so an in-flight write is
//     always a self-consistent pair.
//   - Inside the renewal window. Outside it there is nothing to do, and
//     re-signing on every request would put a Set-Cookie on every response
//     for no gain.
//
// Every failure below is non-fatal and silent to the caller: the session that
// authenticated this request is still valid for at least another TTL/2, so a
// failed renewal costs one more chance at it, not the session.
func renewCookieSession(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims, fromCookie bool) *http.Request {
	if !fromCookie || !auth.IsSafeMethod(r.Method) {
		return r
	}
	if !auth.ShouldRenewSession(time.Now(), auth.SessionExpiry(claims)) {
		return r
	}

	token, _, err := auth.RenewSessionToken(claims)
	if err != nil {
		slog.Debug("auth: session renewal skipped", "path", r.URL.Path, "error", err)
		return r
	}

	// Headers must be written before the handler starts the response body,
	// which is why this runs ahead of next.ServeHTTP rather than after.
	if err := auth.SetAuthCookies(w, token); err != nil {
		slog.Warn("auth: failed to set renewed session cookies", "path", r.URL.Path, "error", err)
		return r
	}

	return r
}
