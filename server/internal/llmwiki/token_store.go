// Package llmwiki — token_store.go (0.5.92)
//
// Multica-side persistence for the LLM Wiki desktop API bearer
// token. The user generates a key in LLM Wiki.app (Settings → API +
// MCP) and pastes it into Multica — the Labs → LLM Wiki page, or
// `multica experimental llm-wiki token --set` — and it lands in
// ~/.multica/llm-wiki.json. DiscoverToken reads this store BEFORE
// the app's own app-state file, so an explicit Multica-side paste
// always wins over whatever the app last persisted.
//
// The store only ever holds the user-pasted token; clearing it
// falls discovery back through env var → app app-state → legacy
// auth.json layouts.

package llmwiki

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	storeMu sync.Mutex
	// storePathFn is a var so tests can redirect the store into a
	// temp directory (SetTokenStorePath). Production resolves the
	// real home directory once per call.
	storePathFn = defaultTokenStorePath
	// homeFn lives in client.go (shared with DiscoverToken).
)

func defaultTokenStorePath() string {
	home, err := homeFn()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".multica", "llm-wiki.json")
}

// TokenStorePath returns the file the user-pasted token persists to.
// Empty string means the home directory could not be resolved.
func TokenStorePath() string { return storePathFn() }

// SetTokenStorePath redirects the user-token store. Empty string
// restores the production path. Tests only.
func SetTokenStorePath(p string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if p == "" {
		storePathFn = defaultTokenStorePath
		return
	}
	storePathFn = func() string { return p }
}

// SetHomeDirForTest redirects the home-directory lookup the token
// discovery chain uses (DiscoverToken / TokenSource). Empty string
// restores os.UserHomeDir. Tests only — without it, a handler test
// asserting source "none" would read whatever the developer's real
// com.llmwiki.app app-state happens to hold.
func SetHomeDirForTest(dir string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if dir == "" {
		homeFn = os.UserHomeDir
		return
	}
	homeFn = func() (string, error) { return dir, nil }
}

// userTokenFile mirrors the on-disk shape of ~/.multica/llm-wiki.json.
type userTokenFile struct {
	Token     string `json:"token"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// SaveUserToken persists the user-pasted token atomically (write to
// a sibling tmp file, then rename) so a crash mid-write can never
// leave a truncated token behind.
func SaveUserToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("token must not be empty")
	}
	p := TokenStorePath()
	if p == "" {
		return errors.New("cannot resolve token store path")
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(userTokenFile{
		Token:     token,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ClearUserToken removes the user-pasted token. Absent file is a
// no-op success; the env-var / app-side discovery sources remain
// active afterwards.
func ClearUserToken() error {
	p := TokenStorePath()
	if p == "" {
		return nil
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	err := os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// TokenSource reports where DiscoverToken currently resolves the
// bearer token from — "user" (Multica store), "env"
// (LLM_WIKI_API_TOKEN), "app" (com.llmwiki.app app-state), "legacy"
// (pre-0.6 auth.json layouts), or "none". It never returns the
// token itself, so it is safe to surface from diagnostics endpoints.
func TokenSource() string {
	if os.Getenv("LLM_WIKI_API_TOKEN") != "" {
		return "env"
	}
	home, err := homeFn()
	if err != nil {
		return "none"
	}
	labels := []string{"user", "app", "legacy", "legacy", "legacy"}
	for i, p := range TokenCandidatePaths(home) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if parseTokenJSON(b) != "" {
			if i >= len(labels) {
				return "legacy"
			}
			return labels[i]
		}
	}
	return "none"
}
