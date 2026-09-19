package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildPiArgsNoToolAllowlist(t *testing.T) {
	// Extension tools registered via Pi's registerTool() must not be
	// filtered out by a hardcoded --tools allowlist. Omitting --tools
	// lets Pi use its full tool registry. See #2379.
	args := buildPiArgs("test prompt", "/tmp/session.jsonl", ExecOptions{}, slog.Default())
	for i, arg := range args {
		if arg == "--tools" {
			t.Errorf("buildPiArgs emits --tools %q; should not restrict tool registry (see #2379)", args[i+1])
		}
	}
}

func TestBuildPiArgsBasicFlags(t *testing.T) {
	args := buildPiArgs("hello world", "/tmp/s.jsonl", ExecOptions{
		Model:        "anthropic/claude-sonnet-4-20250514",
		SystemPrompt: "be helpful",
	}, slog.Default())

	joined := strings.Join(args, " ")
	for _, want := range []string{"-p", "--mode json", "--session /tmp/s.jsonl", "--model anthropic/claude-sonnet-4-20250514", "--append-system-prompt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in args, got: %v", want, args)
		}
	}
	// --provider is never synthesized: the selector goes to --model whole and
	// pi's own resolver accepts `provider/id`, a bare id, and an id containing
	// a slash (MUL-6471, GH #7300).
	for _, arg := range args {
		if arg == "--provider" {
			t.Fatalf("buildPiArgs emits --provider; the model selector must stay whole: %v", args)
		}
	}

	// Prompt must be the last positional argument.
	if args[len(args)-1] != "hello world" {
		t.Errorf("prompt should be last arg, got %q", args[len(args)-1])
	}
}

// TestBuildPiArgsSlashShapedModelStaysWhole is the GH #7300 regression: a
// gateway-style provider persists a model id that itself contains a slash
// (`claude/claude-opus-5` under provider `multica-anthropic`). Splitting it to
// fill --provider would hand pi a provider name it has never heard of; the
// whole selector must reach --model verbatim (MUL-6471).
func TestBuildPiArgsSlashShapedModelStaysWhole(t *testing.T) {
	args := buildPiArgs("hello world", "/tmp/s.jsonl", ExecOptions{
		Model: "claude/claude-opus-5",
	}, slog.Default())

	var modelArgs []string
	for i, arg := range args {
		if arg == "--provider" {
			t.Fatalf("buildPiArgs emits --provider for a slash-shaped model: %v", args)
		}
		if arg == "--model" && i+1 < len(args) {
			modelArgs = append(modelArgs, args[i+1])
		}
	}
	if len(modelArgs) != 1 || modelArgs[0] != "claude/claude-opus-5" {
		t.Errorf("--model = %v, want exactly [claude/claude-opus-5] (whole): %v", modelArgs, args)
	}
}

func TestBuildPiArgsCustomArgsAppended(t *testing.T) {
	// Users can still restrict tools via custom_args if desired.
	args := buildPiArgs("prompt", "/tmp/s.jsonl", ExecOptions{
		CustomArgs: []string{"--tools", "read,bash"},
	}, slog.Default())

	found := false
	for i, arg := range args {
		if arg == "--tools" && i+1 < len(args) && args[i+1] == "read,bash" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom --tools should pass through via custom_args, got: %v", args)
	}
}

// TestPiExecuteAttachesStdinPipe verifies that the Pi backend spawns the
// child with an explicit stdin pipe (FIFO) instead of leaving cmd.Stdin
// nil. Without an explicit pipe, Pi has been observed to block under
// systemd waiting for stdin events (#2188); attaching and immediately
// closing a pipe delivers a clean EOF on a FIFO and unblocks Pi.
//
// The probe is structural rather than behavioral: a shell script in
// place of `pi` inspects /proc/self/fd/0 and only emits a valid event
// stream if stdin is a FIFO. If the fix regresses (stdin nil → /dev/null
// char device), the fake exits non-zero and the test fails.
func TestPiExecuteAttachesStdinPipe(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		// /proc/self/fd/0 is Linux-specific; skipping elsewhere keeps
		// the assertion portable without losing CI coverage.
		t.Skip("stdin fd inspection relies on /proc/self/fd/0")
	}

	fakePath := filepath.Join(t.TempDir(), "pi")
	script := "#!/bin/sh\n" +
		"kind=$(stat -c '%F' -L /proc/self/fd/0 2>/dev/null || echo unknown)\n" +
		"case \"$kind\" in\n" +
		"  fifo|*pipe*)\n" +
		"    printf '%s\\n' '{\"type\":\"agent_start\"}'\n" +
		"    printf '%s\\n' '{\"type\":\"turn_end\",\"message\":{\"role\":\"assistant\",\"model\":\"test\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2}}}'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"printf 'stdin was %s; expected fifo\\n' \"$kind\" >&2\n" +
		"exit 1\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "completed" {
			t.Fatalf("expected status=completed (stdin attached as fifo), got %q (error=%q)", result.Status, result.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestStripPiToolCallMarkup(t *testing.T) {
	tests := map[string]string{
		`before call:bash{command:<|"|>cd repo/path && ls -F<|"|>}<tool_call|> after`:                           "before  after",
		`before call:read{path:<|"|>repo/path/roles/example/verify.yml<|"|>} after`:                             "before  after",
		`before response:bash{command:<|"|>multica issue comment list issue-id --all --output json<|"|>} after`: "before  after",
		`before call:bash{command:<|"|>printf '{"key":"value"}'<|"|>} after`:                                    "before  after",
		`before <|turn>model after`: "before  after",
	}
	for in, want := range tests {
		got := stripPiToolCallMarkup(in)
		if got != want {
			t.Fatalf("unexpected stripped text: %q, want %q", got, want)
		}
	}
}

func TestDrainPiTextBufferSplitToolCall(t *testing.T) {
	chunks := []string{
		"before ca",
		`ll:bash{command:<|"|>ls -R repo/path`,
		`/roles/example<|"|>}`,
		" after",
	}
	var buf strings.Builder
	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(drainPiTextBuffer(&buf, chunk))
	}
	got.WriteString(flushPiTextBuffer(&buf))
	if got.String() != "before  after" {
		t.Fatalf("unexpected streamed text: %q", got.String())
	}
}

func TestDrainPiTextBufferSplitControlToken(t *testing.T) {
	chunks := []string{"before <|tu", "rn>model after"}
	var buf strings.Builder
	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(drainPiTextBuffer(&buf, chunk))
	}
	got.WriteString(flushPiTextBuffer(&buf))
	if got.String() != "before  after" {
		t.Fatalf("unexpected streamed text: %q", got.String())
	}
}

func TestFlushPiTextBufferKeepsUnmatchedToolPrefixes(t *testing.T) {
	tests := []string{
		"plain response: see below",
		"plain call: see below",
		`plain call:bash{command:<|"|>unterminated`,
	}
	for _, want := range tests {
		var buf strings.Builder
		got := drainPiTextBuffer(&buf, want)
		got += flushPiTextBuffer(&buf)
		if got != want {
			t.Fatalf("unexpected flushed text: %q, want %q", got, want)
		}
	}
}

// piEventStreamScript builds a sh script that prints each JSON event on
// its own stdout line. Fixtures must not contain single quotes.
func piEventStreamScript(events []string) string {
	return piEventStreamScriptWithExit(events, 0)
}

// piEventStreamScriptWithExit is piEventStreamScript plus an explicit
// process exit code. Real Pi (and pi-print-clean-exit) exits 1 after a
// turn whose last assistant stopReason is "error".
func piEventStreamScriptWithExit(events []string, exitCode int) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	// Real Pi drains its stdin FIFO before emitting events. This fork passes
	// the prompt as a positional argv and closes stdin immediately after
	// Start, so the drain resolves at once, but keeping it mirrors the real
	// stdin contract.
	b.WriteString("cat > /dev/null\n")
	for _, e := range events {
		b.WriteString("printf '%s\\n' '")
		b.WriteString(e)
		b.WriteString("'\n")
	}
	if exitCode != 0 {
		b.WriteString(fmt.Sprintf("exit %d\n", exitCode))
	}
	return b.String()
}

func newPiTestBackend(t *testing.T, script string, turnErrorGrace time.Duration) *piBackend {
	t.Helper()
	fakePath := filepath.Join(t.TempDir(), "pi")
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("pi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new pi backend: %v", err)
	}
	pi, ok := backend.(*piBackend)
	if !ok {
		t.Fatalf("New(pi) returned %T, want *piBackend", backend)
	}
	pi.turnErrorGrace = turnErrorGrace
	return pi
}

func waitPiResult(t *testing.T, session *Session, timeout time.Duration) Result {
	t.Helper()
	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		return result
	case <-time.After(timeout):
		t.Fatal("timeout waiting for Pi result")
		return Result{}
	}
}

// TestPiExecutePreservesTurnErrorWhenCancelled pins that cancelling the run
// after a provider-level turn error surfaces the provider message instead of
// the generic local cancellation (MUL-7467).
func TestPiExecutePreservesTurnErrorWhenCancelled(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	const providerError = "OpenAI API error (413): Failed to buffer the request body: length limit exceeded"
	script := piEventStreamScript([]string{
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"turn_end","message":{"role":"assistant","model":"test","stopReason":"error","errorMessage":"` + providerError + `"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"post-error activity"}}`,
	}) + "exec sleep 300\n"
	backend := newPiTestBackend(t, script, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{
		ResumeSessionID: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for msg := range session.Messages {
		if msg.Type == MessageThinking && msg.Content == "post-error activity" {
			cancel()
			break
		}
	}

	result := waitPiResult(t, session, 5*time.Second)
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed (error=%q)", result.Status, result.Error)
	}
	if result.Error != providerError {
		t.Fatalf("error = %q, want original provider error %q", result.Error, providerError)
	}
}

func TestPiExecuteCancellationWithoutTurnErrorKeepsAbortedResult(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	script := piEventStreamScript([]string{`{"type":"agent_start"}`}) + "exec sleep 300\n"
	backend := newPiTestBackend(t, script, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{
		ResumeSessionID: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for msg := range session.Messages {
		if msg.Type == MessageStatus {
			cancel()
			break
		}
	}

	result := waitPiResult(t, session, 5*time.Second)
	if result.Status != "aborted" || result.Error != "execution cancelled" {
		t.Fatalf("result = %+v, want the existing no-error cancellation result", result)
	}
}

func TestPiExecuteEndsSilentTurnErrorAfterGraceOnce(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	const providerError = "OpenAI API error (413): request body too large"
	const grace = 80 * time.Millisecond
	script := piEventStreamScript([]string{
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"turn_end","message":{"role":"assistant","model":"test","stopReason":"error","errorMessage":"` + providerError + `"}}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
	}) + "exec sleep 300\n"
	backend := newPiTestBackend(t, script, grace)

	started := time.Now()
	session, err := backend.Execute(context.Background(), "prompt-ignored", ExecOptions{
		Timeout:         15 * time.Second,
		ResumeSessionID: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result := waitPiResult(t, session, 20*time.Second)
	if result.Status != "failed" || result.Error != providerError {
		t.Fatalf("result = %+v, want one failed result with the provider error", result)
	}
	for msg := range session.Messages {
		if msg.Type == MessageError {
			t.Fatalf("turn-error grace emitted an error message and would refresh the daemon watchdog: %+v", msg)
		}
	}
	if elapsed := time.Since(started); elapsed < grace/2 || elapsed > 10*time.Second {
		t.Fatalf("error grace ended after %s, want approximately %s", elapsed, grace)
	}
	if _, ok := <-session.Result; ok {
		t.Fatal("result channel produced more than one terminal result")
	}
}

func TestPiExecuteTurnErrorActivityAndRetryRecoveryCancelTimer(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	const grace = 100 * time.Millisecond
	script := "#!/bin/sh\n" +
		"cat > /dev/null\n" +
		`printf '%s\n' '{"type":"agent_start"}'` + "\n" +
		`printf '%s\n' '{"type":"turn_start"}'` + "\n" +
		`printf '%s\n' '{"type":"turn_end","message":{"role":"assistant","model":"test","stopReason":"error","errorMessage":"temporary provider error"}}'` + "\n" +
		"sleep 0.06\n" +
		`printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"retrying"}}'` + "\n" +
		"sleep 0.06\n" +
		`printf '%s\n' '{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":1}'` + "\n" +
		"sleep 0.15\n" +
		`printf '%s\n' '{"type":"turn_start"}'` + "\n" +
		`printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"recovered"}}'` + "\n" +
		`printf '%s\n' '{"type":"turn_end","message":{"role":"assistant","model":"test"}}'` + "\n"
	backend := newPiTestBackend(t, script, grace)

	session, err := backend.Execute(context.Background(), "prompt-ignored", ExecOptions{
		Timeout:         15 * time.Second,
		ResumeSessionID: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result := waitPiResult(t, session, 20*time.Second)
	if result.Status != "completed" || result.Output != "recovered" || result.Error != "" {
		t.Fatalf("result = %+v, want successful recovered turn", result)
	}
}

func TestPiExecuteTurnErrorGraceWaitsForInFlightTool(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	const grace = 60 * time.Millisecond
	script := "#!/bin/sh\n" +
		"cat > /dev/null\n" +
		`printf '%s\n' '{"type":"turn_start"}'` + "\n" +
		`printf '%s\n' '{"type":"tool_execution_start","toolCallId":"call-1","toolName":"bash","args":{}}'` + "\n" +
		`printf '%s\n' '{"type":"turn_end","message":{"role":"assistant","model":"test","stopReason":"error","errorMessage":"temporary provider error"}}'` + "\n" +
		"sleep 0.15\n" +
		`printf '%s\n' '{"type":"tool_execution_end","toolCallId":"call-1","toolName":"bash","result":"ok"}'` + "\n" +
		`printf '%s\n' '{"type":"turn_start"}'` + "\n" +
		`printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"done"}}'` + "\n" +
		`printf '%s\n' '{"type":"turn_end","message":{"role":"assistant","model":"test"}}'` + "\n"
	backend := newPiTestBackend(t, script, grace)

	session, err := backend.Execute(context.Background(), "prompt-ignored", ExecOptions{
		Timeout:         15 * time.Second,
		ResumeSessionID: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result := waitPiResult(t, session, 20*time.Second)
	if result.Status != "completed" || result.Output != "done" {
		t.Fatalf("result = %+v, want tool completion and recovered turn", result)
	}
}

func TestPiExecuteNormalSilenceDoesNotArmTurnErrorGrace(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	const grace = 50 * time.Millisecond
	script := "#!/bin/sh\n" +
		"cat > /dev/null\n" +
		`printf '%s\n' '{"type":"agent_start"}'` + "\n" +
		"sleep 0.15\n" +
		`printf '%s\n' '{"type":"turn_start"}'` + "\n" +
		`printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"healthy"}}'` + "\n" +
		`printf '%s\n' '{"type":"turn_end","message":{"role":"assistant","model":"test"}}'` + "\n"
	backend := newPiTestBackend(t, script, grace)

	session, err := backend.Execute(context.Background(), "prompt-ignored", ExecOptions{
		Timeout:         15 * time.Second,
		ResumeSessionID: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result := waitPiResult(t, session, 20*time.Second)
	if result.Status != "completed" || result.Output != "healthy" {
		t.Fatalf("result = %+v, want normal silent run to complete", result)
	}
}
