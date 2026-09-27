package agent_trust

import (
	"strings"
	"testing"
)

// The two tables below duplicate the allowlist inside providerCLIEnv
// (review.go) AND its deliberate mirror in handler/runtime_llm_call.go.
// The two functions cannot share code — this package must not import
// handler — so their parity is pinned instead: the SAME tables live in
// handler's runtime_llm_call_env_test.go, and each test asserts its local
// implementation never returns a key outside them. Editing one allowlist
// without the other fails exactly one of the two tests. Keep the tables
// byte-identical.
var providerCLIEnvAllowedExact = []string{
	"PATH", "HOME", "LANG", "LC_ALL", "TZ",
	"TMPDIR", "TERM", "SHELL", "USER", "LOGNAME",
	"CODEX_HOME",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
}

var providerCLIEnvAllowedPrefixes = []string{
	"ANTHROPIC_", "OPENAI_", "CLAUDE_", "GEMINI_", "GOOGLE_", "AWS_", "AZURE_",
	"OPENROUTER_", "MOONSHOT_", "KIMI_", "DEEPSEEK_", "DASHSCOPE_", "ZHIPUAI_",
	"GLM_", "XAI_", "GROQ_", "MISTRAL_", "PERPLEXITY_", "OLLAMA_",
}

// TestProviderCLIEnvAllowlist pins the subprocess environment boundary for
// the agent-trust review CLI child: server-carried secrets (DATABASE_URL,
// the daemon token, JWT material) must never reach a child, provider
// credentials must pass through by prefix, and nothing outside the pinned
// tables may slip through (0.5.107 rule: never hand a handler-spawned
// subprocess the server's full environment).
func TestProviderCLIEnvAllowlist(t *testing.T) {
	t.Setenv("HOME", "/tmp/provider-cli-home")
	t.Setenv("CODEX_HOME", "/tmp/codex-home")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("GLM_API_KEY", "glm-test")
	t.Setenv("DATABASE_URL", "postgres://secret@127.0.0.1/secret")
	t.Setenv("MULTICA_API_TOKEN", "mat_or_pat_secret")
	t.Setenv("MULTICA_JWT_SECRET", "jwt-signing-secret")
	t.Setenv("NODE_OPTIONS", "--inspect")

	got := map[string]string{}
	for _, entry := range providerCLIEnv() {
		k, v, _ := strings.Cut(entry, "=")
		got[k] = v
	}

	for _, tc := range []struct{ key, want string }{
		{"HOME", "/tmp/provider-cli-home"},
		{"CODEX_HOME", "/tmp/codex-home"},
		{"HTTP_PROXY", "http://127.0.0.1:1"},
		{"ANTHROPIC_API_KEY", "sk-ant-test"},
		{"GLM_API_KEY", "glm-test"},
	} {
		if got[tc.key] != tc.want {
			t.Errorf("providerCLIEnv[%q] = %q, want %q", tc.key, got[tc.key], tc.want)
		}
	}

	for _, k := range []string{"DATABASE_URL", "MULTICA_API_TOKEN", "MULTICA_JWT_SECRET", "NODE_OPTIONS"} {
		if v, ok := got[k]; ok {
			t.Errorf("providerCLIEnv leaked %q = %q", k, v)
		}
	}

	for k := range got {
		covered := false
		for _, exact := range providerCLIEnvAllowedExact {
			if k == exact {
				covered = true
				break
			}
		}
		if !covered {
			for _, p := range providerCLIEnvAllowedPrefixes {
				if strings.HasPrefix(k, p) {
					covered = true
					break
				}
			}
		}
		if !covered {
			t.Errorf("providerCLIEnv returned %q which the pinned allowlist tables do not cover — update both mirrors and their test tables together", k)
		}
	}
}
