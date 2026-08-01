// Package agent_trust — review.go (0.5.2).
//
// Production Reviewer implementation. It runs the provider CLI the same way
// the runtime LLM bridge does (/api/runtime/llm-call, runtime_llm_call.go):
// resolve MULTICA_*_PATH overrides or PATH lookup, pipe the English review
// prompt through stdin, parse a strict JSON verdict. The prompt is in
// English (per the user's language contract: LLM-facing text is English,
// UI copy is Chinese) and asks for a single JSON object:
//
//	{"verdict": "pass"|"fail", "reason": "<one line>"}
//
// The reviewer is intentionally permissive on JSON (falls back to scanning
// for the word "fail"/"pass") and returns VerdictSkip on any provider
// failure so the trust gate never blocks task completion.
package agent_trust

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// CLIReviewer is the production Reviewer: one LLM call per review, JSON
// verdict, English prompt. It is stateless and goroutine-safe (each Review
// builds its own exec.Command).
type CLIReviewer struct{}

// ReviewTimeout bounds the provider CLI call so a hung CLI cannot stall the
// completion path indefinitely. 120s matches runtime_llm_call.go's provider
// timeout — a real claude/codex single-turn call routinely takes 60-100s.
const ReviewTimeout = 120 * time.Second

// Review implements Reviewer.
func (r *CLIReviewer) Review(ctx context.Context, task db.AgentTaskQueue, result []byte) ReviewVerdict {
	output := strings.TrimSpace(string(result))
	if output == "" {
		// Nothing to review — skip. Empty output is a completion signal,
		// not a reviewable artifact.
		return VerdictSkip
	}

	prompt := buildReviewPrompt(task, output)
	text, err := callProviderCLI(ctx, prompt)
	if err != nil {
		// Provider missing / timeout / non-zero exit — fail open.
		return VerdictSkip
	}
	switch classifyVerdict(text) {
	case "pass":
		return VerdictPass
	case "fail":
		return VerdictFail
	default:
		return VerdictSkip
	}
}

// buildReviewPrompt composes the English review prompt. The output is
// truncated to 8k runes so a giant artifact cannot blow the provider stdin.
func buildReviewPrompt(task db.AgentTaskQueue, output string) string {
	truncated := output
	if r := []rune(output); len(r) > 8000 {
		truncated = string(r[:8000]) + "\n…[truncated]"
	}
	var b strings.Builder
	b.WriteString(`You are a meticulous senior reviewer for a task-management AI system. `)
	b.WriteString(`A sub-agent has completed a task and produced the output below. `)
	b.WriteString(`Your job is to decide whether the output is correct and complete enough to be accepted as-is. `)
	b.WriteString(`Reply with ONLY a JSON object, no prose:\n{"verdict": "pass"|"fail", "reason": "<one line, English>"}\n`)
	b.WriteString(`Verdict rules:\n- "pass" when the output fully addresses the task and contains no factual or structural errors.\n- "fail" when the output is wrong, incomplete, contradicts the task, or contains errors that would mislead a user.\n- When in doubt, prefer "fail": an unverified artifact must not be accepted silently.\n\n`)
	b.WriteString(fmt.Sprintf("Task id: %s\nIssue id: %s\nTask status: %s\n\n",
		taskIDString(task.ID), taskIDString(task.IssueID), task.Status))
	b.WriteString("--- sub-agent output start ---\n")
	b.WriteString(truncated)
	b.WriteString("\n--- sub-agent output end ---\n")
	return b.String()
}

// classifyVerdict parses the provider text into a verdict. Strict JSON first;
// falls back to scanning the first word for "fail"/"pass" (some CLIs wrap
// the JSON in prose).
func classifyVerdict(text string) string {
	var parsed struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		switch strings.ToLower(strings.TrimSpace(parsed.Verdict)) {
		case "pass":
			return "pass"
		case "fail":
			return "fail"
		}
	}
	// Fuzzy fallback: look for the verdict word inside the first 200 chars.
	head := strings.ToLower(text)
	if len(head) > 200 {
		head = head[:200]
	}
	if strings.Contains(head, "fail") {
		return "fail"
	}
	if strings.Contains(head, "pass") {
		return "pass"
	}
	return "unknown"
}

// providerEnv is the ordered provider override list, mirroring
// runtime_llm_call.go::pickProviderCLI. Kept small (the daemon probes the
// same envs); first match wins.
var providerEnv = []struct{ env, cmd string }{
	{"MULTICA_CLAUDE_PATH", "claude"},
	{"MULTICA_CODEX_PATH", "codex"},
	{"MULTICA_OPENCODE_PATH", "opencode"},
	{"MULTICA_OPENCLAW_PATH", "openclaw"},
	{"MULTICA_HERMES_PATH", "hermes"},
	{"MULTICA_PI_PATH", "pi"},
	{"MULTICA_KIMI_PATH", "kimi"},
	{"MULTICA_KIRO_PATH", "kiro-cli"},
	{"MULTICA_AGY_PATH", "agy"},
}

// RunProviderLLM runs a single-turn provider CLI call with the given
// system + user prompts and returns the trimmed stdout. Exported so the
// agent_self_optimization optimizer can reuse the same provider resolution
// for its proposal + validation calls. Errors are surfaced verbatim (the
// caller maps them to skip / fallback).
func RunProviderLLM(ctx context.Context, system, prompt string) (string, error) {
	combined := system
	if combined != "" {
		combined += "\n\n"
	}
	combined += prompt
	return callProviderCLI(ctx, combined)
}

// callProviderCLI runs the first available provider CLI with the prompt on
// stdin and returns its trimmed stdout. Errors return err (caller maps to
// VerdictSkip). Context deadline → error.
func callProviderCLI(ctx context.Context, prompt string) (string, error) {
	bin, ok := resolveProviderBin()
	if !ok {
		return "", fmt.Errorf("no provider CLI on PATH (set MULTICA_CLAUDE_PATH etc.)")
	}
	ctx, cancel := context.WithTimeout(ctx, ReviewTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "--print")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("provider %s failed: %w (stderr=%s)", bin, err, truncateForLog(stderr.String()))
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		text = strings.TrimSpace(stderr.String())
	}
	return text, nil
}

// resolveProviderBin returns the first provider binary found via env
// override or PATH lookup.
func resolveProviderBin() (string, bool) {
	for _, p := range providerEnv {
		if path := strings.TrimSpace(os.Getenv(p.env)); path != "" {
			if _, err := os.Stat(path); err == nil {
				return path, true
			}
			// Env points at a missing file — fall through to PATH.
		}
		if _, err := exec.LookPath(p.cmd); err == nil {
			return p.cmd, true
		}
	}
	return "", false
}

// truncateForLog bounds a stderr dump to 500 chars for the warning log.
func truncateForLog(s string) string {
	if r := []rune(s); len(r) > 500 {
		return string(r[:500]) + "…"
	}
	return s
}

// taskIDString is a nil-safe UUID stringifier for the prompt.
func taskIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return util.UUIDToString(id)
}
