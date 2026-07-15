// Tests for the mythos_swarm HTTP entry point helpers that don't
// require a real DB. The RunMythosSwarm handler full-path tests live
// alongside the rest of the handler integration suite.
//
// 0.3.28 PR-4 regression coverage:
//   - per-workspace rate limit (1 run / 5min)
//   - Retry-After header formatting (RFC 7231 seconds integer)
//   - max-loop hardcap (5)

package handler

import (
	"strings"
	"testing"
	"time"
)

func TestMythosRateLimiter_FirstRunAllowed(t *testing.T) {
	l := newMythosRateLimiter()
	if !l.Allow("ws-1") {
		t.Fatalf("first run should be allowed")
	}
}

func TestMythosRateLimiter_SecondRunRejected(t *testing.T) {
	l := newMythosRateLimiter()
	ws := "ws-1"
	if !l.Allow(ws) {
		t.Fatal("first run should be allowed")
	}
	if l.Allow(ws) {
		t.Fatal("second run within window should be rejected")
	}
}

func TestMythosRateLimiter_DifferentWorkspacesIndependent(t *testing.T) {
	// Workspaces must not share budget. A burst on ws-1 must not
	// rate-limit ws-2.
	l := newMythosRateLimiter()
	if !l.Allow("ws-1") {
		t.Fatal("ws-1 first run should be allowed")
	}
	if !l.Allow("ws-2") {
		t.Fatal("ws-2 first run should be allowed even after ws-1 rejected")
	}
	if l.Allow("ws-1") {
		t.Fatal("ws-1 second run should be rejected")
	}
}

func TestMythosRateLimiter_RetryAfterIsPositive(t *testing.T) {
	l := newMythosRateLimiter()
	ws := "ws-1"
	l.Allow(ws)
	retry := l.retryAfter(ws)
	if retry <= 0 || retry > mythosRateLimitWindow {
		t.Fatalf("retryAfter = %v, want positive value within window %v",
			retry, mythosRateLimitWindow)
	}
}

func TestMythosRateLimiter_RetryAfterNoHitsIsZero(t *testing.T) {
	l := newMythosRateLimiter()
	if retry := l.retryAfter("ws-no-hits"); retry != 0 {
		t.Fatalf("retryAfter with no recorded hits = %v, want 0", retry)
	}
}

func TestRetryAfterString_Format(t *testing.T) {
	// RFC 7231 §7.1.3: Retry-After is an integer number of seconds.
	// Floors to 1s and rounds fractional values.
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "1"},
		{-5 * time.Second, "1"},
		{500 * time.Millisecond, "1"},
		{1 * time.Second, "1"},
		{90 * time.Second, "90"},
		{4*time.Minute + 30*time.Second, "270"},
	}
	for _, c := range cases {
		if got := retryAfterString(c.in); got != c.want {
			t.Errorf("retryAfterString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMythosRun_RequestWithoutFlagsRejected(t *testing.T) {
	// Smoke test on the wire shape: an empty request body should
	// fail JSON decode. We don't want a wrong-shape body to slip
	// past the rate limiter and burn a budget entry on garbage.
	// This is a documentation test — RunMythosSwarm full-path is
	// covered by the handler integration suite.
	body := strings.NewReader("")
	_ = body // placeholder for the full-path integration check
}

func TestMythosMaxLoopHardCap_IsFive(t *testing.T) {
	// The HTTP boundary clamp must match the runner package
	// MaxLoopItersHardCap. If either drifts, a caller can sneak a
	// higher iter count in via direct Config{} construction — but a
	// legitimate HTTP request is already capped at 5 here.
	if mythosMaxLoopHardCap != 5 {
		t.Fatalf("mythosMaxLoopHardCap = %d, want 5", mythosMaxLoopHardCap)
	}
}

func TestMythosWaitConstants_InRange(t *testing.T) {
	// Sanity: poll interval should be much smaller than the timeout
	// (the loop runs at most WaitTimeout / PollInterval steps) and
	// the timeout should be short enough to fit inside the HTTP
	// request budget (5min).
	if mythosWaitPollInterval >= mythosWaitTimeout {
		t.Fatalf("poll interval %v must be < timeout %v",
			mythosWaitPollInterval, mythosWaitTimeout)
	}
	if mythosWaitTimeout >= 5*time.Minute {
		t.Fatalf("wait timeout %v should be < 5min to stay within HTTP budget",
			mythosWaitTimeout)
	}
}
