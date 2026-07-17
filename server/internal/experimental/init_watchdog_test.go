package experimental

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunWithTimeoutCompletes(t *testing.T) {
	err := RunWithTimeout(context.Background(), "ok", "fast", func() error {
		return nil
	})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestRunWithTimeoutReturnsHookError(t *testing.T) {
	want := errors.New("hook failed")
	got := RunWithTimeout(context.Background(), "err_flag", "failing", func() error {
		return want
	})
	if !errors.Is(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRunWithTimeoutBreaksOnHang(t *testing.T) {
	withTempBlacklist(t)

	// Use a parent context with a 10ms deadline so the watchdog's
	// effective timeout is 10ms (shorter than the hook's sleep).
	// DefaultInitTimeout is intentionally a large value to make a
	// real hang noticeable; tests that need to assert the timeout
	// path must override via parent context.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := RunWithTimeout(ctx, "hanger", "stuck", func() error {
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	entry, ok := IsBroken("hanger")
	if !ok {
		t.Fatal("flag should be broken after init timeout")
	}
	if entry.Reason != ReasonInitTimeout {
		t.Errorf("Reason = %q, want %q", entry.Reason, ReasonInitTimeout)
	}
}

func TestRunWithTimeoutShortDeadline(t *testing.T) {
	withTempBlacklist(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := RunWithTimeout(ctx, "short", "tight", func() error {
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	_, ok := IsBroken("short")
	if !ok {
		t.Fatal("flag should be broken after parent context timeout")
	}
}

func TestRunWithTimeoutRecoversPanic(t *testing.T) {
	withTempBlacklist(t)

	err := RunWithTimeout(context.Background(), "panicker", "boom", func() error {
		panic("simulated")
	})
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}
	if _, ok := IsBroken("panicker"); ok {
		t.Error("panic should not auto-blacklist via the watchdog (main.go recover handles it)")
	}
}
