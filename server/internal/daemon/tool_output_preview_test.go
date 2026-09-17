package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestToolOutputPreview(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"short unicode", "执行完成 😀\n", "执行完成 😀\n"},
		{"under budget", strings.Repeat("a", 8191), strings.Repeat("a", 8191)},
		{"exact budget", strings.Repeat("a", 8192), strings.Repeat("a", 8192)},
		{"over budget", strings.Repeat("a", 8193), strings.Repeat("a", 8192)},
		{"chinese", strings.Repeat("中", 5000), strings.Repeat("中", 2730)},
		{"emoji", strings.Repeat("😀", 3000), strings.Repeat("😀", 2048)},
		// Server-side redaction may expand this input. That is not source truncation.
		{"redaction growth", strings.Repeat("TOKEN=x ", 1024), strings.Repeat("TOKEN=x ", 1024)},
		{"nul", "before\x00after", "beforeafter"},
		{"invalid utf8", "a\xffb", "a\uFFFDb"},
		{"normalization growth", strings.Repeat("a", 8191) + "\xff", strings.Repeat("a", 8191)},
		{"normalization shrink", strings.Repeat("a", 8191) + "\x00b", strings.Repeat("a", 8191) + "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolOutputPreview(tt.input); got != tt.want {
				t.Fatalf("preview differs from expected text: got %d bytes, want %d", len(got), len(tt.want))
			}
		})
	}
}

func TestToolOutputPreviewRuneBoundaries(t *testing.T) {
	t.Parallel()
	// Include every cut inside 2-, 3-, and 4-byte runes, plus both exact
	// boundaries. A literal replacement character is valid text to preserve.
	for _, char := range []string{"é", "中", "😀", "\uFFFD"} {
		for kept := 0; kept <= len(char); kept++ {
			t.Run(fmt.Sprintf("%s/%d", char, kept), func(t *testing.T) {
				prefix := strings.Repeat("a", 8192-kept)
				want := prefix
				if kept == len(char) {
					want += char
				}
				if got := toolOutputPreview(prefix + char + "tail"); got != want {
					t.Fatalf("cut must retain exactly the complete runes: got %d bytes, want %d", len(got), len(want))
				}
			})
		}
	}
}

// toolResultBackend emits exactly one tool_result message before completing.
type toolResultBackend struct {
	output string
}

func (b *toolResultBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	messages := make(chan agent.Message, 1)
	messages <- agent.Message{
		Type: agent.MessageToolResult, Tool: "bash", Output: b.output,
	}
	// Stage the Result behind a short delay. executeAndDrain returns the
	// moment Result lands and its deferred cancels close the drain context;
	// if the message and the Result are ready in the same instant, the
	// drain goroutine's select may take the Done branch first and drop the
	// buffered message. Real backends never emit a result before the
	// preceding stream events, so mirror that ordering here.
	results := make(chan agent.Result, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		results <- agent.Result{Status: "completed"}
		close(results)
	}()
	close(messages)
	return &agent.Session{Messages: messages, Result: results}, nil
}

// Exercise the reporting path: JSON encoding must not repair a split rune by
// inserting replacement characters into an otherwise valid tool result.
//
// Fork harness note: upstream captures the reported message from an injected
// transcript recorder; the fork's executeAndDrain reports batches through
// d.client.ReportTaskMessages, so the test captures that HTTP call instead.
func TestExecuteAndDrain_ToolOutputUTF8Boundary(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var reported []TaskMessageData
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			var body struct {
				Messages []TaskMessageData `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode reported messages: %v", err)
			}
			mu.Lock()
			reported = append(reported, body.Messages...)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}

	if _, _, _, err := d.executeAndDrain(context.Background(), &toolResultBackend{
		output: strings.Repeat("中", 2731),
	}, "p", agent.ExecOptions{}, slog.Default(), "task-utf8-preview"); err != nil {
		t.Fatalf("executeAndDrain: %v", err)
	}
	// executeAndDrain returns as soon as the result lands; the drain
	// goroutine flushes the message batch concurrently, so wait for it.
	deadline := time.Now().Add(5 * time.Second)
	mu.Lock()
	for len(reported) == 0 && time.Now().Before(deadline) {
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
	}
	defer mu.Unlock()
	if len(reported) != 1 || reported[0].Type != "tool_result" || reported[0].Tool != "bash" {
		t.Fatalf("expected one bash tool_result, got %+v", reported)
	}
	want := strings.Repeat("中", 2730)
	if got := reported[0].Output; got != want {
		t.Fatalf("reported output differs from the complete-rune prefix: got %d bytes, want %d", len(got), len(want))
	}
}

func BenchmarkToolOutputPreview(b *testing.B) {
	for _, tt := range []struct{ name, input string }{
		{"8KiB", strings.Repeat("ordinary log line\n", 512)[:8192]},
		{"300KiBChinese", strings.Repeat("中", 100*1024)},
	} {
		b.Run(tt.name, func(b *testing.B) {
			b.SetBytes(int64(len(tt.input)))
			b.ReportAllocs()
			for b.Loop() {
				toolOutputPreview(tt.input)
			}
		})
	}
}
