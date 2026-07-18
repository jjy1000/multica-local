// Package agent_self_optimization — flag.go (0.3.45.1).
//
// Single seam between the service and the experimental flag system.
// Split into its own file so tests can stub it via build tags or a
// var override in the test file's package init.
//
// The flag is OFF by default (catalog.DefaultVal=false per CLAUDE.md
// "flag-off completely bypasses experimental code"). When the flag
// flips on at runtime, the daemon picks it up on the next Resume()
// call; the existing per-workspace tickers continue to gate their
// own work on this function, so no re-launch is needed.

package agent_self_optimization

import (
	"github.com/multica-ai/multica/server/internal/experimental"
)

// flagOnExperimental returns true when the agent_self_optimization
// flag is enabled for the current process. The Service consults this
// before launching any ticker or resuming any run.
//
// Tests in agent_self_optimization package can override flagOn
// directly with `func init() { flagOn = func() bool { return true } }`
// because Go's package-level functions are first-class assignable
// vars in this layout — see flag_test.go for the pattern.
func flagOnExperimental() bool {
	return experimental.DefaultFor("agent_self_optimization")
}