// Package handler — runtime_llm_call.go
//
// /api/runtime/llm-call is the loopback bridge Pythia (and any future
// headless experimental service) uses to ask Multica to run an LLM call.
//
// Contract:
//
//   POST /api/runtime/llm-call
//   Headers:
//     Authorization: Bearer <user JWT or daemon token>
//     X-Pythia-Source: pythia-oracle   (optional, recorded in audit log)
//   Body:
//     {
//       "prompt": "...",                 // required
//       "system": "...",                 // optional
//       "model":  "...",                 // optional override; otherwise default
//       "max_tokens": 900,               // optional, default 900
//       "temperature": 0.5               // optional, default 0.5
//     }
//
//   200 {"text": "...", "model": "..."}
//   401 missing/invalid bearer
//   403 source IP not in 127.0.0.0/8
//   429 rate-limited (>60 req/min per source)
//   502 no provider CLI on PATH
//   504 provider CLI timed out or returned non-zero
//
// The handler is intentionally minimal: it locates the first available
// provider CLI the daemon would have used (MULTICA_CLAUDE_PATH /
// MULTICA_CODEX_PATH / etc., then fallback to PATH lookup), constructs a
// single-turn invocation, pipes the system+user prompt through stdin, and
// returns the trimmed stdout. This keeps the loopback contract the same
// whether the call originates from PYTHIA's `_complete()`, Claude
// Science's skill matcher, or a future Mythos Swarm sub-agent.
//
// Source-IP gate: 127.0.0.0/8 only. We do NOT honour X-Forwarded-For here;
// the endpoint is meant to be hit from a process on the same host. If you
// ever need to expose it through a proxy, add a separate
// `TrustedProxies` carve-out instead of flipping the default — the same
// discipline `clientIPForRateLimit` uses.

package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
)

// LLMCallRequest is the wire shape PYTHIA posts to /api/runtime/llm-call.
// We accept OpenAI-style "messages" too, so any OpenAI-compatible client
// can dial the endpoint without translation.
type LLMCallRequest struct {
	Prompt      string           `json:"prompt,omitempty"`
	System      string           `json:"system,omitempty"`
	Messages    []LLMCallMessage `json:"messages,omitempty"`
	Model       string           `json:"model,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
}

type LLMCallMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMCallResponse struct {
	Text  string `json:"text"`
	Model string `json:"model"`
}

// runtimeLLMCallRateLimit caps the per-source-IP call rate. 60/min is
// enough for a single Pythia brief/predict loop but low enough to keep
// an accidental fork-bomb from piling up cost. Buckets are kept
// in-process; restart drops them, which is acceptable for a loopback
// bridge that only one process can talk to at a time.
const runtimeLLMCallRateLimit = 60

var (
	runtimeLLMCallBucketsMu sync.Mutex
	runtimeLLMCallBuckets   = map[string]*runtimeLLMCallBucket{}
)

type runtimeLLMCallBucket struct {
	windowStart time.Time
	count       int
}

// LLMCallHandler returns the HTTP handler bound on the public router.
// Kept as a method on *Handler so it can reuse auth + cfg the same way
// the rest of the package does. The handler does NOT touch the database —
// it is a pure bridge from local loopback to a provider CLI subprocess.
func (h *Handler) LLMCallHandler(w http.ResponseWriter, r *http.Request) {
	// Source-IP gate: only loopback callers may reach this endpoint.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		writeError(w, http.StatusForbidden, "could not determine source address")
		return
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		writeError(w, http.StatusForbidden,
			"/api/runtime/llm-call is only available on the loopback interface")
		return
	}

	// Bearer token. Two caller classes dial this bridge: local tooling
	// holding a user JWT, and local subprocesses (the PYTHIA engine) holding
	// the desktop PAT — "mul_…" is the only credential pythiaRuntimeEnv
	// injects into config.json. 0.5.104: accept BOTH. The bare jwt.Parse
	// below rejected the PAT with 401, so once the engine env fix let the
	// bridge calls through, every one of them still failed auth and the
	// deliberation degraded to fallback data. PAT validation mirrors
	// middleware/auth.go (hash → lookup → expiry) minus the TTL cache —
	// this endpoint is rate-limited at 60 req/min, so the extra SELECT is
	// negligible. We do not require workspace membership: the caller is a
	// local service on behalf of a user.
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	if strings.HasPrefix(token, "mul_") {
		if h.Queries == nil {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		pat, err := h.Queries.GetPersonalAccessTokenByHash(r.Context(), auth.HashToken(token))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		if pat.ExpiresAt.Valid && !time.Now().Before(pat.ExpiresAt.Time) {
			writeError(w, http.StatusUnauthorized, "expired bearer token")
			return
		}
	} else {
		parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return auth.JWTSecret(), nil
		})
		if err != nil || parsed == nil || !parsed.Valid {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
	}

	// Rate limit per source IP.
	if !runtimeLLMCallAllow(host) {
		writeError(w, http.StatusTooManyRequests,
			"too many llm-call requests; try again in a minute")
		return
	}

	// Body parse. Cap at 256KB — prompts shouldn't exceed that, and an
	// unbounded read would let a runaway caller hold the goroutine.
	body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()
	var req LLMCallRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.Messages = mergeMessagesAndPrompt(req.Messages, req.Prompt, req.System)
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "prompt or messages required")
		return
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 900
	}
	if req.Temperature == nil {
		def := 0.5
		req.Temperature = &def
	}

	// Resolve provider CLI.
	bin, model, ok := pickProviderCLI(req.Model)
	if !ok {
		writeError(w, http.StatusBadGateway,
			"no provider CLI on PATH; install claude/codex/etc. or set MULTICA_*_PATH")
		return
	}

	// Build the single-turn invocation. Each provider CLI accepts the
	// prompt on stdin differently; we serialise to a single user turn
	// containing the concatenated conversation so all known providers
	// can handle it via --print / equivalent.
	combined := renderMessages(req.Messages)
	args := buildProviderArgs(bin, model)
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(combined)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "provider timed out")
			return
		}
		writeError(w, http.StatusBadGateway,
			fmt.Sprintf("provider failed: %v; stderr=%s", err, stderr.String()))
		return
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		text = strings.TrimSpace(stderr.String())
	}
	writeJSON(w, http.StatusOK, LLMCallResponse{Text: text, Model: model})
}

// mergeMessagesAndPrompt folds legacy PYTHIA-style {prompt, system} into a
// OpenAI-style messages array so old callers still work after we added
// messages support.
func mergeMessagesAndPrompt(msgs []LLMCallMessage, prompt, system string) []LLMCallMessage {
	if len(msgs) > 0 {
		return msgs
	}
	out := make([]LLMCallMessage, 0, 2)
	if system != "" {
		out = append(out, LLMCallMessage{Role: "system", Content: system})
	}
	if prompt != "" {
		out = append(out, LLMCallMessage{Role: "user", Content: prompt})
	}
	return out
}

// renderMessages joins an OpenAI-style messages array into a single
// transcript the various provider CLIs accept on stdin. We pick
// "### system:\n...\n\n### user:\n..." because every supported CLI
// (claude --print, codex exec, cursor-agent, kimi, kiro-cli, agy) treats
// stdin as a raw user message — they don't read role frames — so the
// system turn is prepended in plain text.
func renderMessages(msgs []LLMCallMessage) string {
	var b strings.Builder
	hadSystem := false
	for _, m := range msgs {
		switch m.Role {
		case "system":
			if !hadSystem {
				b.WriteString("### system:\n")
				hadSystem = true
			}
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("### assistant:\n")
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		default:
			b.WriteString("### user:\n")
			b.WriteString(m.Content)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// pickProviderCLI honours MULTICA_*_PATH overrides (matching the daemon's
// probe loop) and falls back to PATH lookup. The first match wins; we do
// NOT chain — PYTHIA wants one stable provider per call.
func pickProviderCLI(modelOverride string) (bin, model string, ok bool) {
	overrides := []struct {
		env, cmd, modelEnv string
	}{
		{"MULTICA_CLAUDE_PATH", "claude", "MULTICA_CLAUDE_MODEL"},
		{"MULTICA_CODEX_PATH", "codex", "MULTICA_CODEX_MODEL"},
		{"MULTICA_OPENCODE_PATH", "opencode", "MULTICA_OPENCODE_MODEL"},
		{"MULTICA_OPENCLAW_PATH", "openclaw", "MULTICA_OPENCLAW_MODEL"},
		{"MULTICA_HERMES_PATH", "hermes", "MULTICA_HERMES_MODEL"},
		{"MULTICA_PI_PATH", "pi", "MULTICA_PI_MODEL"},
		{"MULTICA_CURSOR_PATH", "cursor-agent", "MULTICA_CURSOR_MODEL"},
		{"MULTICA_COPILOT_PATH", "copilot", "MULTICA_COPILOT_MODEL"},
		{"MULTICA_KIMI_PATH", "kimi", "MULTICA_KIMI_MODEL"},
		{"MULTICA_KIRO_PATH", "kiro-cli", "MULTICA_KIRO_MODEL"},
		{"MULTICA_CODEBUDDY_PATH", "codebuddy", "MULTICA_CODEBUDDY_MODEL"},
		{"MULTICA_AGY_PATH", "agy", "MULTICA_AGY_MODEL"},
	}
	for _, o := range overrides {
		path := strings.TrimSpace(os.Getenv(o.env))
		if path == "" {
			lp, err := exec.LookPath(o.cmd)
			if err != nil {
				continue
			}
			path = lp
		}
		m := strings.TrimSpace(os.Getenv(o.modelEnv))
		if modelOverride != "" {
			m = modelOverride
		}
		return path, m, true
	}
	return "", "", false
}

// buildProviderArgs returns the CLI args that ask the provider to read a
// prompt from stdin and print a single text response. We use --print
// everywhere because every supported provider exposes it; the runtime
// contract from PYTHIA's side is just "give me text".
func buildProviderArgs(_, model string) []string {
	args := []string{"--print"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// runtimeLLMCallAllow returns true iff host has not exceeded
// runtimeLLMCallRateLimit in the trailing 60s window. Buckets are reset
// when the window expires; we don't do sliding-window smoothing because
// a Pythia brief loop already batches its work.
func runtimeLLMCallAllow(host string) bool {
	now := time.Now()
	runtimeLLMCallBucketsMu.Lock()
	defer runtimeLLMCallBucketsMu.Unlock()
	b, exists := runtimeLLMCallBuckets[host]
	if !exists || now.Sub(b.windowStart) > time.Minute {
		runtimeLLMCallBuckets[host] = &runtimeLLMCallBucket{windowStart: now, count: 1}
		return true
	}
	if b.count >= runtimeLLMCallRateLimit {
		return false
	}
	b.count++
	return true
}

// Ensure bufio import survives goimports even though we don't directly
// reference it yet — left as a placeholder for an upcoming streaming
// variant that pipes provider output back to the client.
var _ = bufio.NewReader
