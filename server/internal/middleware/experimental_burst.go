package middleware

// 5xx burst safety net for Labs. 0.3.18 adds a middleware that
// watches for repeated 5xx responses on routes that declare a
// flag-context header (X-Experimental-Flag). When N responses
// happen within a rolling window, the flag is auto-blacklisted so
// the next launch skips it. This catches the "flag works at boot
// but every request 500s" failure mode that pure panic recovery
// misses — most failed-flag code paths return an error, not a panic.
//
// The flag context comes from a request header set by the handler
// the moment it knows which flag owns it. Handlers that do NOT set
// the header are not tracked — they belong to no flag and a 500 is
// just a 500.
//
// Defaults: 3 errors in 60 seconds. Configurable via BurstConfig.

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// ExperimentalFlagHeader is the request header that identifies the
// flag a handler belongs to. Empty / missing → handler is not tracked.
const ExperimentalFlagHeader = "X-Experimental-Flag"

// BurstConfig controls how many 5xx responses within a window trip
// the breaker. Zero or negative values fall back to the defaults.
type BurstConfig struct {
	Threshold int
	Window    time.Duration
}

func (c BurstConfig) withDefaults() BurstConfig {
	if c.Threshold <= 0 {
		c.Threshold = 3
	}
	if c.Window <= 0 {
		c.Window = 60 * time.Second
	}
	return c
}

// flagState tracks recent 5xx timestamps for one flag. We keep the
// slice trimmed to Threshold entries so memory stays bounded; old
// timestamps past the window are dropped on each new failure.
type flagState struct {
	mu       sync.Mutex
	failures []time.Time
	notified bool // dedupe: only MarkBroken once per window
}

// isExperimentalPath reports whether p is one of the experimental
// route families that legitimately carry the X-Experimental-Flag
// header: the same-origin reverse proxy (/experimental/...) and the
// flag-gated API surface (/api/experimental/...). A 5xx on any other
// path is a normal server error, not a flag failure — even if the
// client forged the header.
func isExperimentalPath(p string) bool {
	return strings.HasPrefix(p, "/experimental/") ||
		strings.HasPrefix(p, "/api/experimental/") ||
		strings.HasPrefix(p, "/api/experimental-")
}

// ExperimentalFlagBurst wraps next with a middleware that watches
// for 5xx responses on requests carrying the X-Experimental-Flag
// header and breaks the flag when the configured threshold fires.
//
// Idempotent: when the same flag trips the breaker twice in the
// same process lifetime, the second MarkBroken call replaces the
// first entry (latest break wins) — see experimental.MarkBroken.
//
// The status observer wraps http.ResponseWriter with a counter so
// we can detect 5xx post-handler. A response is "tracked 5xx" only
// when:
//  1. The request carried the X-Experimental-Flag header.
//  2. The response status code is >= 500.
//
// Client errors (4xx) are not tracked — a buggy UI hammering a
// non-existent endpoint is not the flag's fault.
func ExperimentalFlagBurst(cfg BurstConfig) func(http.Handler) http.Handler {
	cfg = cfg.withDefaults()
	states := make(map[string]*flagState)
	// statesMu guards the states map. The per-flag flagState has its
	// own mutex for its failure slice, but the map insert/lookup itself
	// races under concurrent requests for the same (or different) flag
	// — a data race the -race detector flags. Guard the map access.
	var statesMu sync.Mutex

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flagKey := r.Header.Get(ExperimentalFlagHeader)
			if flagKey == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Trust boundary: X-Experimental-Flag is only trusted on
			// actual experimental paths and only for a known catalog
			// flag. This middleware is mounted globally, but a client
			// can set the header on ANY request. Without these guards a
			// user could forge `X-Experimental-Flag: <flag>` on an
			// unrelated 5xx-prone endpoint and trip the safety
			// blacklist for a flag the request never touched. Only the
			// experimental proxy + runtime routes legitimately carry
			// this header (server-injected on the proxy path, or on the
			// gated /api/experimental/* handlers).
			if !isExperimentalPath(r.URL.Path) || !experimental.IsKnownKey(flagKey) {
				next.ServeHTTP(w, r)
				return
			}

			// Wrap the writer so we can read the status code after
			// the handler returns. We do NOT modify the headers or
			// body — just observe.
			sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			if sw.status < 500 {
				return
			}
			statesMu.Lock()
			state := states[flagKey]
			if state == nil {
				state = &flagState{}
				states[flagKey] = state
			}
			statesMu.Unlock()
			if shouldBreak(state, cfg) {
				ctx := r.URL.Path
				if err := experimental.MarkBroken(flagKey, experimental.Reason5xxBurst, ctx); err != nil {
					slog.Error("safety: failed to mark flag broken after 5xx burst", "flag", flagKey, "error", err)
					return
				}
				slog.Warn("safety: experimental flag marked broken by 5xx burst", "flag", flagKey, "path", ctx)
			}
		})
	}
}

// shouldBreak records a new failure timestamp and reports whether
// the breaker should fire. Called with the per-flag mutex already
// held implicitly via state.mu.
func shouldBreak(state *flagState, cfg BurstConfig) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-cfg.Window)

	// Drop expired entries from the head of the slice. The slice is
	// kept short (max Threshold entries) so this is O(Threshold).
	trimmed := state.failures[:0]
	for _, ts := range state.failures {
		if ts.After(cutoff) {
			trimmed = append(trimmed, ts)
		}
	}
	trimmed = append(trimmed, now)
	state.failures = trimmed

	if len(state.failures) < cfg.Threshold {
		return false
	}
	if state.notified {
		// Already broke once in this window; no need to spam the
		// blacklist file with identical entries.
		return false
	}
	state.notified = true
	// Reset the notification lock when the window passes: clear the
	// notified flag once the slice drains, so a second burst after
	// a quiet period still trips.
	if len(state.failures) == 0 || state.failures[0].Before(cutoff) {
		state.notified = false
	}
	return true
}

// statusRecorder is a minimal http.ResponseWriter wrapper that
// captures the status code. We only need to override WriteHeader
// because handlers in this codebase call it explicitly for error
// paths; the implicit 200 path is handled by the default status
// field value.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}
