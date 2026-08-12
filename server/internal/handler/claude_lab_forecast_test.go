// Package handler — claude_lab_forecast_test.go (0.3.24+)
//
// Smoke test for the Claude Lab forecast SSE handler. We invoke
// forecastStream against a custom ResponseWriter shim that
// captures each Write call so we can assert the wire shape
// without racing a real network connection.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureWriter is an in-memory http.ResponseWriter that satisfies
// http.Flusher and stores the full response body for inspection.
type captureWriter struct {
	mu      sync.Mutex
	header  http.Header
	body    []byte
	flushed int
}

func (c *captureWriter) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}
	return c.header
}
func (c *captureWriter) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = append(c.body, b...)
	return len(b), nil
}
func (c *captureWriter) WriteHeader(statusCode int) { /* httptest semantics */ }
func (c *captureWriter) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushed++
}

func TestForecastStreamEmitsValidFrames(t *testing.T) {
	t.Parallel()

	w := &captureWriter{}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/forecast?seed=42", nil)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}

	done := make(chan struct{})
	go func() {
		forecastStream(w, req)
		close(done)
	}()

	// Wait for at least one frame to land.
	deadline := time.Now().Add(2 * forecastInterval)
	var snapshot string
	for {
		w.mu.Lock()
		snapshot = string(w.body)
		flushed := w.flushed
		w.mu.Unlock()
		if flushed > 0 && strings.Contains(snapshot, "\n\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for first frame; body=%q", snapshot)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The first complete frame should be an "event: prediction".
	idx := strings.Index(snapshot, "\n\n")
	frame := snapshot[:idx+2]
	if !strings.HasPrefix(frame, "event: prediction\ndata: ") {
		t.Fatalf("missing prediction prefix: %q", frame)
	}
	payload := strings.TrimPrefix(frame, "event: prediction\ndata: ")
	var env forecastEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatalf("decode frame: %v (payload=%q)", err, payload)
	}
	if env.Scenario == "" || env.Horizon == "" || env.Persona == "" {
		t.Fatalf("envelope fields empty: %+v", env)
	}
	if env.Probability < 0 || env.Probability > 1 {
		t.Fatalf("probability out of [0,1]: %f", env.Probability)
	}

	// Cancel via request context — handler should exit promptly.
	// (We use a separate cancellable context for the test.)
	cancelCtx, cancel := context.WithCancel(context.Background())
	req2, _ := http.NewRequestWithContext(cancelCtx, http.MethodGet, "/forecast?seed=7", nil)
	go forecastStream(w, req2)
	time.Sleep(50 * time.Millisecond)
	cancel()
	// The handler holds its own derived context; we can't wait
	// for it to exit here, but cancel() must not panic.
}

// TestBuildForecastEnvelope_LLMOK exercises the happy path: llmForecast
// reaches a mock runtime bridge and the helper passes the envelope
// through with OracleSource="llm" + LabSource="llm" + no failover flag.
// Mirrors TestLLMForecast_OK in claude_lab_llm_forecast_test.go so the
// env-var + httptest contract is identical.
func TestBuildForecastEnvelope_LLMOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"scenario":"oracle scenario","narrative":"oracle narrative","probability":0.42,"confidence":0.71,"horizon":"week","persona":"strategist"}`))
	}))
	defer srv.Close()

	t.Setenv("MULTICA_AGENT_RUNTIME_URL", srv.URL)
	t.Setenv("MULTICA_API_TOKEN", "test-token")

	env := buildForecastEnvelope(context.Background(), "strategist", "week", "ctx", 42)

	if env.OracleSource != "llm" {
		t.Errorf("OracleSource=%q, want llm", env.OracleSource)
	}
	if env.LabSource != "llm" {
		t.Errorf("LabSource=%q, want llm", env.LabSource)
	}
	if env.SyntheticOracleFailover {
		t.Errorf("SyntheticOracleFailover=true on happy path, want false")
	}
	if env.Scenario != "oracle scenario" {
		t.Errorf("Scenario=%q, want %q", env.Scenario, "oracle scenario")
	}
	if env.Narrative != "oracle narrative" {
		t.Errorf("Narrative=%q, want %q", env.Narrative, "oracle narrative")
	}
	if env.Probability != 0.42 {
		t.Errorf("Probability=%f, want 0.42", env.Probability)
	}
	if env.ScenarioContext != "ctx" {
		t.Errorf("ScenarioContext=%q, want %q", env.ScenarioContext, "ctx")
	}
	if env.ID == "" {
		t.Errorf("ID empty, want non-empty")
	}
	if env.CreatedAt == "" {
		t.Errorf("CreatedAt empty, want RFC3339 timestamp")
	}
}

// TestBuildForecastEnvelope_LLMFail exercises the fallback path: env
// unset → llmForecast errors → syntheticForecast runs → envelope is
// flagged OracleSource="synthetic" + SyntheticOracleFailover=true +
// narrative carries the oracle error suffix so postmortem can tell
// genuine synthetic from a real oracle outage.
func TestBuildForecastEnvelope_LLMFail(t *testing.T) {
	// Leave env unset → llmForecast returns
	// "MULTICA_AGENT_RUNTIME_URL or MULTICA_API_TOKEN not set".
	t.Setenv("MULTICA_AGENT_RUNTIME_URL", "")
	t.Setenv("MULTICA_API_TOKEN", "")

	env := buildForecastEnvelope(context.Background(), "strategist", "week", "", 7)

	if env.OracleSource != "synthetic" {
		t.Errorf("OracleSource=%q, want synthetic", env.OracleSource)
	}
	if env.LabSource != "synthetic" {
		t.Errorf("LabSource=%q, want synthetic", env.LabSource)
	}
	if !env.SyntheticOracleFailover {
		t.Errorf("SyntheticOracleFailover=false on fallback, want true")
	}
	if !strings.Contains(env.Narrative, "[oracle fallback:") {
		t.Errorf("Narrative=%q missing oracle-fallback suffix", env.Narrative)
	}
	// Seed-driven PRNG must still produce a valid envelope.
	if env.Scenario == "" || env.Horizon == "" || env.Persona == "" {
		t.Errorf("fallback envelope fields empty: %+v", env)
	}
	if env.Probability < 0 || env.Probability > 1 {
		t.Errorf("probability out of [0,1]: %f", env.Probability)
	}
}
