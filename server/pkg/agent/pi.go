package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// piBackend implements Backend by spawning the Pi CLI in non-interactive
// JSON mode (`pi -p --mode json --session <path>`) and parsing its event
// stream on stdout.
type piBackend struct {
	cfg Config
	// turnErrorGrace overrides the silence window after a turn-level provider
	// error. Production uses defaultPiTurnErrorGrace; tests shorten it without
	// changing concurrent executions through package-global state.
	turnErrorGrace time.Duration
}

var (
	piControlTokenRE = regexp.MustCompile(`<\|[A-Za-z0-9_-]+>[A-Za-z0-9_-]*|<[A-Za-z0-9_-]+\|>`)
)

// defaultPiTurnErrorGrace is the recovery window Pi gets after reporting a
// provider error without exiting. Pi reports the same turn_end stopReason
// before automatic retries, so the error cannot be treated as terminal at
// once. Ten minutes matches the existing provider-specific semantic-idle
// precedent while remaining far below the daemon's two-hour fallback.
const defaultPiTurnErrorGrace = 10 * time.Minute

// piTurnErrorGuard owns the only timer associated with a pending Pi turn
// error. The stream goroutine records protocol activity; the timer callback
// invokes the execution's bounded stop path after re-checking the state. It
// never emits a Message, so observing the provider error cannot refresh the
// daemon's independent lastActivityAt watchdog clock.
type piTurnErrorGuard struct {
	mu            sync.Mutex
	grace         time.Duration
	expireRun     func()
	timer         *time.Timer
	generation    uint64
	lastError     string
	inFlightTools int
	graceExpired  bool
	stopped       bool
}

func newPiTurnErrorGuard(grace time.Duration, expireRun func()) *piTurnErrorGuard {
	return &piTurnErrorGuard{grace: grace, expireRun: expireRun}
}

// closePiReadPipe interrupts a scanner/copy blocked in Read before releasing
// the adapter's descriptor. The deadline matters on Unix, where Close from a
// different goroutine need not interrupt a syscall already in the kernel.
func closePiReadPipe(pipe io.ReadCloser) {
	if deadlinePipe, ok := pipe.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = deadlinePipe.SetReadDeadline(time.Now())
	}
	_ = pipe.Close()
}

// observeEvent records a parseable Pi protocol event. Recovery events clear a
// stale provider error; every other event gives an unresolved error a fresh
// grace window. Tool accounting is kept inside the adapter so an error timer
// can never terminate a tool that is still in flight.
func (g *piTurnErrorGuard) observeEvent(eventType string) {
	if eventType == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}

	switch eventType {
	case "turn_start", "auto_retry_start":
		g.clearErrorLocked()
		return
	case "tool_execution_start":
		g.inFlightTools++
	case "tool_execution_end":
		if g.inFlightTools > 0 {
			g.inFlightTools--
		}
	}
	g.rearmLocked()
}

func (g *piTurnErrorGuard) record(errText string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	g.lastError = errText
	g.rearmLocked()
}

func (g *piTurnErrorGuard) clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	g.clearErrorLocked()
}

func (g *piTurnErrorGuard) clearErrorLocked() {
	g.lastError = ""
	g.generation++
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
}

func (g *piTurnErrorGuard) rearmLocked() {
	if g.lastError == "" || g.stopped {
		return
	}
	g.generation++
	generation := g.generation
	if g.timer != nil {
		g.timer.Stop()
	}
	g.timer = time.AfterFunc(g.grace, func() {
		g.expire(generation)
	})
}

func (g *piTurnErrorGuard) expire(generation uint64) {
	g.mu.Lock()
	if g.stopped || generation != g.generation || g.lastError == "" {
		g.mu.Unlock()
		return
	}
	g.timer = nil
	if g.inFlightTools > 0 {
		g.mu.Unlock()
		return
	}
	g.graceExpired = true
	g.stopped = true
	expireRun := g.expireRun
	g.mu.Unlock()

	// The callback owns both process-tree cancellation and the adapter's local
	// pipes, so finalization cannot stay blocked on an unowned descendant.
	expireRun()
}

func (g *piTurnErrorGuard) finish() (lastError string, graceExpired bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	g.generation++
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	return g.lastError, g.graceExpired
}

func stripPiToolCallMarkup(s string) string {
	s = stripPiStructuredToolMarkup(s)
	return piControlTokenRE.ReplaceAllString(s, "")
}

func drainPiTextBuffer(buf *strings.Builder, delta string) string {
	buf.WriteString(delta)
	emit, pending := drainPiSanitizedText(buf.String())
	buf.Reset()
	buf.WriteString(pending)
	return emit
}

func flushPiTextBuffer(buf *strings.Builder) string {
	s := buf.String()
	buf.Reset()
	emit, pending := drainPiSanitizedText(s)
	emit += piControlTokenRE.ReplaceAllString(pending, "")
	return emit
}

func drainPiSanitizedText(s string) (string, string) {
	var out strings.Builder
	for i := 0; i < len(s); {
		start, prefixLen := nextPiToolMarkupPrefix(s, i)
		if start == -1 {
			safeLen := safePiTextEmitLen(s[i:])
			out.WriteString(s[i : i+safeLen])
			return piControlTokenRE.ReplaceAllString(out.String(), ""), s[i+safeLen:]
		}
		out.WriteString(s[i:start])
		end, ok := scanPiToolMarkupEnd(s, start+prefixLen)
		if !ok {
			return piControlTokenRE.ReplaceAllString(out.String(), ""), s[start:]
		}
		i = end
	}
	return piControlTokenRE.ReplaceAllString(out.String(), ""), ""
}

func stripPiStructuredToolMarkup(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		start, prefixLen := nextPiToolMarkupPrefix(s, i)
		if start == -1 {
			out.WriteString(s[i:])
			break
		}
		out.WriteString(s[i:start])
		end, ok := scanPiToolMarkupEnd(s, start+prefixLen)
		if !ok {
			out.WriteString(s[start:])
			break
		}
		i = end
	}
	return out.String()
}

func safePiTextEmitLen(s string) int {
	hold := 0
	for _, prefix := range []string{"call:", "response:"} {
		for n := 1; n < len(prefix) && n <= len(s); n++ {
			if strings.HasSuffix(s, prefix[:n]) && n > hold {
				hold = n
			}
		}
	}
	if i := strings.LastIndexByte(s, '<'); i >= 0 && looksLikePiControlTokenPrefix(s[i:]) {
		if len(s)-i > hold {
			hold = len(s) - i
		}
	}
	return len(s) - hold
}

func looksLikePiControlTokenPrefix(s string) bool {
	if len(s) == 0 || s[0] != '<' || len(s) > 64 {
		return false
	}
	for i := 1; i < len(s); i++ {
		b := s[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-' || b == '|' || b == '>' {
			continue
		}
		return false
	}
	return true
}

func nextPiToolMarkupPrefix(s string, from int) (int, int) {
	best := -1
	bestLen := 0
	for _, prefix := range []string{"call:", "response:"} {
		if i := strings.Index(s[from:], prefix); i >= 0 {
			abs := from + i
			if best == -1 || abs < best {
				best = abs
				bestLen = len(prefix)
			}
		}
	}
	return best, bestLen
}

func scanPiToolMarkupEnd(s string, i int) (int, bool) {
	nameStart := i
	for i < len(s) && isPiToolNameByte(s[i]) {
		i++
	}
	if i == nameStart || i >= len(s) || s[i] != '{' {
		return 0, false
	}

	const quoteMarker = `<|"|>`
	depth := 0
	inQuote := false
	for i < len(s) {
		if strings.HasPrefix(s[i:], quoteMarker) {
			inQuote = !inQuote
			i += len(quoteMarker)
			continue
		}

		if !inQuote {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					i++
					if strings.HasPrefix(s[i:], "<tool_call|>") {
						i += len("<tool_call|>")
					}
					return i, true
				}
			}
		}
		i++
	}
	return 0, false
}

func isPiToolNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

func (b *piBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execName := b.cfg.ExecutablePath
	if execName == "" {
		execName = "pi"
	}
	lookedUp, err := exec.LookPath(execName)
	if err != nil {
		return nil, fmt.Errorf("pi executable not found at %q: %w", execName, err)
	}

	timeout := opts.Timeout

	// Pi's --session flag expects a file path where events are appended.
	// The path doubles as our opaque session identifier: we return it as
	// SessionID and expect it back as ResumeSessionID on the next turn.
	sessionPath := opts.ResumeSessionID
	if sessionPath == "" {
		p, err := newPiSessionPath()
		if err != nil {
			return nil, fmt.Errorf("pi session path: %w", err)
		}
		sessionPath = p
	}
	if err := ensurePiSessionFile(sessionPath); err != nil {
		return nil, fmt.Errorf("pi session file: %w", err)
	}

	runCtx, cancel := runContext(ctx, timeout)
	processCtx, cancelProcess := context.WithCancel(runCtx)

	args := buildPiArgs(prompt, sessionPath, opts, b.cfg.Logger)
	argv0, cmdArgs := choosePiInvocation(execName, lookedUp, args, b.cfg.Logger)

	cmd := newRuntimeCmd(exec.CommandContext(processCtx, argv0, cmdArgs...))
	hideAgentWindow(cmd)
	b.cfg.logAgentCommand(cmd, newAgentCommandLogArgs(args))
	cmd.WaitDelay = 10 * time.Second
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelProcess()
		cancel()
		return nil, fmt.Errorf("pi stdout pipe: %w", err)
	}
	// Attach an explicit stdin pipe so we can close it ourselves. Pi reads
	// its prompt from argv (positional, see buildPiArgs) and never expects
	// interactive input, but when the parent leaves cmd.Stdin nil and the
	// daemon is run under systemd, Pi has been observed to block in its
	// event loop awaiting stdin events instead of progressing to "done"
	// (#2188). Closing the pipe immediately after Start delivers an
	// explicit EOF on a FIFO, which unblocks Pi's readable side.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		closePiReadPipe(stdout)
		cancelProcess()
		cancel()
		return nil, fmt.Errorf("pi stdin pipe: %w", err)
	}
	// Log stderr, but own the pipe directly instead of letting os/exec hide
	// it behind an internal copy goroutine (MUL-7467): an escaped descendant
	// can inherit stderr too, and cmd.Wait() drains that internal copy until
	// WaitDelay, which would hold Result finalization open for the full 10s
	// backstop after the error-grace path has already decided to stop. The
	// grace and cancellation paths must be able to release every read that
	// could block finalization.
	stderrRead, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		closePiReadPipe(stdout)
		cancelProcess()
		cancel()
		return nil, fmt.Errorf("pi stderr pipe: %w", err)
	}
	stderrSink := newLogWriter(b.cfg.Logger, "[pi:stderr] ")

	if err := startOwnedProcessTree(cmd, b.cfg.Logger); err != nil {
		_ = stdin.Close()
		closePiReadPipe(stdout)
		closePiReadPipe(stderrRead)
		cancelProcess()
		cancel()
		return nil, fmt.Errorf("start pi: %w", err)
	}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderrSink, stderrRead)
		close(stderrDone)
	}()
	_ = stdin.Close()

	b.cfg.Logger.Info("pi started", "pid", cmd.Process.Pid, "cwd", opts.Cwd, "model", opts.Model)

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)
	turnErrorGrace := b.turnErrorGrace
	if turnErrorGrace <= 0 {
		turnErrorGrace = defaultPiTurnErrorGrace
	}
	turnErrors := newPiTurnErrorGuard(turnErrorGrace, func() {
		b.cfg.Logger.Info("pi turn error recovery grace expired; stopping runtime")
		// CommandContext owns process-tree cancellation for every runtime
		// command. An escaped descendant can nevertheless retain the
		// stdout/stderr write ends after that group dies, so close the
		// adapter-owned read pipes too: finalization must not depend on an
		// unowned process eventually exiting. (This fork closes stdin
		// eagerly right after Start — the #2188 contract above — so no
		// stdin close is needed here.)
		cancelProcess()
		closePiReadPipe(stdout)
		closePiReadPipe(stderrRead)
	})

	// Close the adapter-owned read pipes when the context is cancelled so the
	// stream scanner and the stderr copier unblock.
	go func() {
		<-runCtx.Done()
		closePiReadPipe(stdout)
		closePiReadPipe(stderrRead)
	}()

	go func() {
		defer cancelProcess()
		defer cancel()
		defer close(msgCh)
		defer close(resCh)

		startTime := time.Now()
		var output strings.Builder
		finalStatus := "completed"
		var finalError string
		usage := make(map[string]TokenUsage)

		scanner := bufio.NewScanner(stdout)
		// Pi message_update events can be large (they embed the full message
		// partial on each delta), so give the scanner generous headroom.
		scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
		var textBuffer strings.Builder

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var evt piStreamEvent
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				continue
			}
			turnErrors.observeEvent(evt.Type)

			switch evt.Type {
			case "agent_start":
				trySend(msgCh, Message{Type: MessageStatus, Status: "running"})

			case "message_update":
				if evt.AssistantMessageEvent == nil {
					continue
				}
				switch evt.AssistantMessageEvent.Type {
				case "text_delta":
					if d := drainPiTextBuffer(&textBuffer, evt.AssistantMessageEvent.Delta); d != "" {
						output.WriteString(d)
						trySend(msgCh, Message{Type: MessageText, Content: d})
					}
				case "thinking_delta":
					if d := evt.AssistantMessageEvent.Delta; d != "" {
						trySend(msgCh, Message{Type: MessageThinking, Content: d})
					}
				}

			case "tool_execution_start":
				var params map[string]any
				if len(evt.Args) > 0 {
					_ = json.Unmarshal(evt.Args, &params)
				}
				trySend(msgCh, Message{
					Type:   MessageToolUse,
					Tool:   evt.ToolName,
					CallID: evt.ToolCallID,
					Input:  params,
				})

			case "tool_execution_end":
				trySend(msgCh, Message{
					Type:   MessageToolResult,
					CallID: evt.ToolCallID,
					Output: decodePiResult(evt.Result),
				})

			case "turn_end":
				msg := decodePiMessage(evt.Message)
				if msg == nil {
					continue
				}
				if msg.Usage != nil {
					model := msg.Model
					if model == "" {
						model = opts.Model
					}
					if model == "" {
						model = "unknown"
					}
					u := usage[model]
					u.InputTokens += msg.Usage.Input
					u.OutputTokens += msg.Usage.Output
					u.CacheReadTokens += msg.Usage.CacheRead
					u.CacheWriteTokens += msg.Usage.CacheWrite
					usage[model] = u
				}
				// A turn Pi ends on an error is only terminal when nothing
				// follows it. Pi emits the same stopReason before an automatic
				// retry, and turn_start clears this, so a later successful turn
				// leaves nothing behind.
				if msg.StopReason == "error" {
					turnError := msg.ErrorMessage
					if turnError == "" {
						turnError = "pi ended the turn with an error"
					}
					turnErrors.record(turnError)
				} else {
					// A successful terminal turn is positive recovery evidence even
					// if a future Pi version omits the expected turn_start.
					turnErrors.clear()
				}

			case "error":
				errText := decodePiString(evt.Message)
				trySend(msgCh, Message{Type: MessageError, Content: errText})
				if finalStatus == "completed" {
					finalStatus = "failed"
					finalError = errText
				}

			case "auto_retry_end":
				if evt.Success {
					turnErrors.clear()
				} else if finalStatus == "completed" {
					finalStatus = "failed"
					if evt.FinalError != "" {
						finalError = evt.FinalError
					} else {
						finalError = "pi exhausted automatic retries"
					}
				}
			}
		}
		if d := flushPiTextBuffer(&textBuffer); d != "" {
			output.WriteString(d)
			trySend(msgCh, Message{Type: MessageText, Content: d})
		}

		// Finish the adapter-owned stderr read before Wait closes the
		// StderrPipe descriptor. Normal exit gets the same 10s backstop
		// cmd.WaitDelay provided when os/exec owned the copier. Cancellation
		// and error-grace expiry close stderrRead above, so their
		// finalization does not pay it.
		stderrTimer := time.NewTimer(cmd.WaitDelay)
		select {
		case <-stderrDone:
			stderrTimer.Stop()
		case <-stderrTimer.C:
			closePiReadPipe(stderrRead)
			<-stderrDone
		}

		waitErr := cmd.Wait()
		// Leader reaped; drop the runtime-process-tree ownership handle
		// (Unix: no-op; Windows: closes the Job Object).
		releaseProcessGroup(cmd)
		duration := time.Since(startTime)
		lastTurnError, turnErrorGraceExpired := turnErrors.finish()

		if runCtx.Err() == context.DeadlineExceeded {
			finalStatus = "timeout"
			finalError = fmt.Sprintf("pi timed out after %s", timeout)
		} else if runCtx.Err() == context.Canceled {
			if lastTurnError != "" {
				// A daemon watchdog or user cancellation must not replace a
				// provider failure Pi had already reported with a generic local
				// cancellation. The daemon can then persist and classify the
				// original provider message.
				finalStatus = "failed"
				finalError = lastTurnError
			} else {
				finalStatus = "aborted"
				finalError = "execution cancelled"
			}
		} else if turnErrorGraceExpired {
			finalStatus = "failed"
			finalError = lastTurnError
		} else if waitErr != nil && finalStatus == "completed" {
			finalStatus = "failed"
			// Prefer the turn's provider message over the process exit code.
			// Pi (and pi-print-clean-exit) exits 1 after stopReason=error, so
			// Wait() wins this branch and used to drop lastTurnError. The
			// classifier then saw only "exit status 1" and filed the run as
			// non-retryable process_failure — even when the turn was a
			// transient LiteLLM/OpenAI "Connection error." / "Request timed
			// out." Keep the exit status as a suffix so a genuine crash is
			// still visible (same shape as the OpenCode empty-step+exit
			// composite).
			if lastTurnError != "" {
				finalError = fmt.Sprintf("%s; pi exited with error: %v", lastTurnError, waitErr)
			} else {
				finalError = fmt.Sprintf("pi exited with error: %v", waitErr)
			}
		} else if lastTurnError != "" && finalStatus == "completed" {
			// Pi exits 0 after a turn it could not complete and did not retry,
			// and emits neither an `error` event nor `auto_retry_end`. Without
			// this the run reports success with no output.
			finalStatus = "failed"
			finalError = lastTurnError
		}

		b.cfg.Logger.Info("pi finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

		resCh <- Result{
			Status:     finalStatus,
			Output:     output.String(),
			Error:      finalError,
			DurationMs: duration.Milliseconds(),
			SessionID:  sessionPath,
			Usage:      usage,
		}
	}()

	return &Session{Messages: msgCh, Result: resCh}, nil
}

// ── Pi event types ──

// piStreamEvent is the union of fields we consume from Pi's JSON event
// stream. Fields that can be either string or object across event types
// (e.g. `message`, `result`) are held as json.RawMessage and decoded on
// demand by the switch arms.
type piStreamEvent struct {
	Type string `json:"type"`

	// message_update
	AssistantMessageEvent *piAssistantMessageEvent `json:"assistantMessageEvent,omitempty"`

	// tool_execution_start / tool_execution_end
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	IsError    bool            `json:"isError,omitempty"`

	// error: Message is a string. turn_end: Message is an object.
	Message json.RawMessage `json:"message,omitempty"`

	// auto_retry_end
	Success    bool   `json:"success,omitempty"`
	FinalError string `json:"finalError,omitempty"`
}

type piAssistantMessageEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}

type piMessage struct {
	Role  string   `json:"role,omitempty"`
	Model string   `json:"model,omitempty"`
	Usage *piUsage `json:"usage,omitempty"`

	// turn_end carries the terminal state of the turn. Pi sets StopReason to
	// "error" for a provider call it could not complete, whether or not it
	// goes on to retry, and puts the provider's message in ErrorMessage.
	StopReason   string `json:"stopReason,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
}

func decodePiMessage(raw json.RawMessage) *piMessage {
	if len(raw) == 0 {
		return nil
	}
	var m piMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

func decodePiString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

func decodePiResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// ── Arg builder ──

// piBlockedArgs are flags hardcoded by the daemon that must not be
// overridden by user-configured custom_args. Overriding these would
// break the daemon↔Pi communication protocol.
var piBlockedArgs = map[string]blockedArgMode{
	"-p":        blockedStandalone, // non-interactive mode
	"--print":   blockedStandalone, // alias for -p
	"--mode":    blockedWithValue,  // "json" event stream protocol
	"--session": blockedWithValue,  // daemon manages the session path
}

// buildPiArgs assembles the argv for a one-shot Pi invocation.
//
// Flags:
//
//	-p                          non-interactive mode (prompt is positional)
//	--mode json                 emit one JSON event per line on stdout
//	--session <path>            session log file (created upfront, reused on resume)
//	--model <selector>          model selector, passed through verbatim
//	--append-system-prompt <s>  extra system instructions
//
// Custom args appended before the positional prompt. The prompt is a
// positional argument and must be last.
func buildPiArgs(prompt, sessionPath string, opts ExecOptions, logger *slog.Logger) []string {
	args := []string{
		"-p",
		"--mode", "json",
	}
	if sessionPath != "" {
		args = append(args, "--session", sessionPath)
	}
	// The selector goes to --model whole, and --provider is never synthesized.
	// Pi's own resolver already accepts every shape we hold: a canonical
	// `provider/id`, a bare id, and — crucially — an id that itself contains a
	// slash, which is the normal case for gateway-style providers whose model
	// ids look like `claude/claude-opus-5`. Splitting on the first slash to
	// fill --provider turns that id into a provider name Pi has never heard of,
	// and an unknown --provider is a hard error ("Unknown provider ...") rather
	// than something Pi can recover from — whereas --model alone falls back to
	// matching the full string as a raw model id. Passing less is strictly more
	// capable here (MUL-6471, GH #7300).
	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	// Note: we intentionally do NOT pass --tools here. Omitting it lets
	// Pi use its full tool registry, including user-installed extension
	// tools. Passing --tools acts as a restrictive allowlist that
	// silently filters out extension-registered tools (#2379).
	// Users who want to restrict tools can do so via custom_args.
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	args = append(args, filterCustomArgs(opts.CustomArgs, piBlockedArgs, logger)...)
	args = append(args, prompt)
	return args
}

// ── Session path ──

// piSessionDir returns the directory where Pi session JSONL files live.
// Exported via a helper so the usage scanner (package usage) can point at
// the same location without duplicating the path construction.
func piSessionDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".multica", "pi-sessions"), nil
}

func newPiSessionPath() (string, error) {
	dir, err := piSessionDir()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s.jsonl", time.Now().UTC().Format("20060102T150405.000000000"))
	return filepath.Join(dir, name), nil
}

// ensurePiSessionFile creates an empty session file if one does not yet
// exist at path. Pi refuses to start when --session points at a missing
// file; paths that already exist (a resumed session) are left untouched.
func ensurePiSessionFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// PiSessionDir exposes piSessionDir to other packages in this module.
func PiSessionDir() (string, error) {
	return piSessionDir()
}
