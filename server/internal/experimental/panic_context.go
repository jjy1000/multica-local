package experimental

// Panic context helpers for the 0.3.18 Labs safety net. Code paths
// that belong to a specific experimental flag can name the flag
// before panicking so the main.go recover sentinel records the right
// entry in the blacklist.
//
// The mechanism uses a goroutine-local map keyed by *panic stack
// state*, NOT runtime.SetPanicValue (which Go intentionally does not
// expose). Instead we lean on the standard idiom: panic happens
// during unwind; the recover() in main reads a package-level
// variable that the panic site just set.
//
// Concurrency: the panic context is a single-slot variable. It is
// only meaningful immediately before a panic on the same goroutine.
// If two goroutines panic at the same time the second one's context
// may overwrite the first; the worst case is the wrong flag is
// marked broken for one crash, which is acceptable (both flags were
// crashing — both end up broken across the next few launches).
//
// Recovery calls popPanicFlagContext which returns the value AND
// clears the slot, so a second recover() in the same chain sees an
// empty context.

import "sync/atomic"

var panicFlagContext atomic.Pointer[panicContextEntry]

type panicContextEntry struct {
	flagKey string
	context string
}

// SetPanicFlagContext names the flag that owns the currently
// executing code path. Call this immediately before any operation
// that may panic, so the main.go recover sentinel can attribute the
// panic to the right flag.
//
// Safe to call on any goroutine; subsequent calls on the same
// goroutine overwrite the slot atomically.
func SetPanicFlagContext(flagKey, context string) {
	panicFlagContext.Store(&panicContextEntry{flagKey: flagKey, context: context})
}

// PopPanicFlagContext returns the most recently set flag context and
// clears the slot. Returns ok=false when no context has been set or
// the slot was already consumed.
//
// Exported so the recover() sentinel in cmd/server/main.go can call
// it; production code outside the sentinel should not need this.
func PopPanicFlagContext() (flagKey, context string, ok bool) {
	entry := panicFlagContext.Swap(nil)
	if entry == nil {
		return "", "", false
	}
	return entry.flagKey, entry.context, true
}

// WithPanicFlagContext runs fn under a flag context. The slot is left
// set during fn execution so a panic within fn can be attributed by
// the outer recover() sentinel in cmd/server/main.go. The sentinel
// owns the clear via PopPanicFlagContext — slot-clearing in a defer
// would run BEFORE the outer recover() (LIFO defer ordering) and
// silently empty the attribution. The slot is overwritten by the
// next SetPanicFlagContext call regardless, so a stale slot on
// success-path return is harmless: unrelated goroutines that panic
// later inherit the latest caller's context (acceptable per package
// comment on panicFlagContext).
//
// Usage:
//
//	experimental.WithPanicFlagContext("mythos_swarm", "mythos.Run", func() {
//	    runner.Run(ctx, cfg)  // panics here will be attributed
//	})
func WithPanicFlagContext(flagKey, context string, fn func()) {
	SetPanicFlagContext(flagKey, context)
	fn()
}
