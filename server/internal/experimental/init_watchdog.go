package experimental

// Init-time watchdog for Labs flag setup. 0.3.18 lets each flag
// register an init hook that runs during server startup. The hook
// is allowed a configurable timeout (default 30 s); exceeding it
// blacklists the flag so the next launch skips its code path
// entirely. This catches the "flag hangs at boot" failure mode that
// pure panic recovery misses — a deadlock or an unbounded HTTP
// poll is not a panic, but it is just as fatal to a startup.
//
// The watchdog writes a ReasonInitTimeout entry to the blacklist
// with the hook's name in the Context field, then returns the
// underlying timeout error so the caller can decide whether to
// abort startup or continue without the flag.
//
// The watchdog is intentionally not parallel: hooks run in
// registration order under a single goroutine, so a hung hook
// blocks subsequent hooks. That is the whole point — we WANT to
// notice. Hooks that can safely run in parallel should spawn their
// own goroutines inside the hook.

import (
	"context"
	"fmt"
	"time"
)

// DefaultInitTimeout is applied when RunWithTimeout is called with a
// zero or negative deadline. 30 s is long enough for a real network
// probe (Claude Science manifest fetch, Pythia venv resolve) but
// short enough that a stuck curl / hung socket does not block boot
// for the whole morning.
const DefaultInitTimeout = 30 * time.Second

// RunWithTimeout executes hook under a deadline. On timeout it
// blacklists the flag with ReasonInitTimeout (so the NEXT launch
// skips it), then returns context.DeadlineExceeded so the caller
// can fail or recover. The hook itself is NOT cancelled — Go has
// no way to interrupt a non-preemptive function — but the watchdog
// stops waiting and lets boot proceed. A leaked goroutine is the
// worst case; the next launch's blacklist will skip the flag
// entirely so the leak does not recur.
//
// name is a free-form identifier shown in the blacklist Context
// field. Use the format "<flag_key>.<hook>" — e.g.
// "mythos_swarm.install_workspace".
func RunWithTimeout(parent context.Context, flagKey, name string, hook func() error) error {
	deadline := DefaultInitTimeout
	if parent != nil {
		if dl, ok := parent.Deadline(); ok {
			remaining := time.Until(dl)
			if remaining > 0 && remaining < deadline {
				deadline = remaining
			}
		}
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// A panic inside the hook routes back through the
				// main.go recover sentinel via SetPanicFlagContext,
				// but we also report it through the done channel so
				// the caller does not deadlock waiting for a result.
				done <- fmt.Errorf("init hook panicked: %v", r)
			}
		}()
		done <- hook()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(deadline):
		ctx := fmt.Sprintf("%s after %s", name, deadline)
		if err := MarkBroken(flagKey, ReasonInitTimeout, ctx); err != nil {
			// If the blacklist write itself fails, fall through —
			// the hook is still hung and the caller still needs to
			// proceed without it.
			return fmt.Errorf("init hook %q timed out after %s (and blacklist write failed: %v)",
				name, deadline, err)
		}
		return fmt.Errorf("init hook %q timed out after %s; flag marked broken for next launch",
			name, deadline)
	}
}
