package agent

import (
	"bufio"
	"io"
)

// agentStreamMaxLineBytes bounds a single line read from an agent CLI's
// event stream. Agent transports are line-delimited JSON, and one line can
// carry a whole conversation: Codex app-server serializes an entire thread
// into the single `thread/resume` response, and providers that re-send a
// growing message partial on every delta or embed a large tool result in one
// event grow the same way. Crossing this bound makes scanner.Scan() return
// false with bufio.ErrTooLong, which the backends can only report as a
// transport failure — the session itself is fine, we just cannot read it.
//
// 10 MiB preserves the bound codex.go carried inline before this shared
// constructor was extracted; it is headroom, not a guarantee: a thread can
// still outgrow any fixed cap, so the recovery path matters more than the
// number.
const agentStreamMaxLineBytes = 10 * 1024 * 1024

// agentStreamInitialBufferBytes is the scanner's starting allocation. Lines
// above it still grow up to agentStreamMaxLineBytes; this only decides how
// much is reserved before the first grow, so ordinary events never realloc.
const agentStreamInitialBufferBytes = 1024 * 1024

// newAgentStreamScanner returns a bufio.Scanner sized for agent event
// streams. Every backend reading a line-delimited agent transport must go
// through this constructor: the bound used to be copy-pasted per backend and
// silently drifted, which is how a fix reached only one of them.
func newAgentStreamScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, agentStreamInitialBufferBytes), agentStreamMaxLineBytes)
	return scanner
}
