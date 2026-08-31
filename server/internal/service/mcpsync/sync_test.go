package mcpsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractMcpServers(t *testing.T) {
	doc := `{
		"numStartups": 42,
		"mcpServers": {
			"github": {"type": "stdio", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"]},
			"amap-sse": {"type": "sse", "url": "https://mcp.amap.com/sse"}
		},
		"projects": {}
	}`
	servers, err := ExtractMcpServers([]byte(doc))
	if err != nil {
		t.Fatalf("ExtractMcpServers: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(servers))
	}
	if _, ok := servers["github"]; !ok {
		t.Error("github server missing")
	}
}

func TestExtractMcpServersNoKeyIsEmptySnapshot(t *testing.T) {
	// A valid config without mcpServers means "zero servers" — unlike a
	// missing file, this legitimately marks everything removed.
	servers, err := ExtractMcpServers([]byte(`{"numStartups": 1}`))
	if err != nil {
		t.Fatalf("ExtractMcpServers: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("want empty snapshot, got %d", len(servers))
	}
}

func TestExtractMcpServersRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"invalid json":       `{"mcpServers": {`,
		"non-object def":     `{"mcpServers": {"x": "npx"}}`,
		"null def":           `{"mcpServers": {"x": null}}`,
		"empty name":         `{"mcpServers": {"": {"command": "x"}}}`,
		"non-object servers": `{"mcpServers": []}`,
	}
	for name, doc := range cases {
		if _, err := ExtractMcpServers([]byte(doc)); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}

func TestCanonicalHashIgnoresFormattingAndKeyOrder(t *testing.T) {
	a := map[string]json.RawMessage{
		"github": json.RawMessage(`{"command":"npx","type":"stdio"}`),
		"memory": json.RawMessage(`{"command":"mcp-server-memory"}`),
	}
	b := map[string]json.RawMessage{
		"memory": json.RawMessage("{\n  \"type_missing\": false,\n  \"command\": \"mcp-server-memory\"\n}"),
		"github": json.RawMessage("{\n  \"type\": \"stdio\",\n  \"command\": \"npx\"\n}"),
	}
	// b's memory entry differs semantically (extra key), so only the shared
	// subset must match. Hash per-server by feeding single-server maps.
	aOnly := map[string]json.RawMessage{"github": a["github"]}
	bOnly := map[string]json.RawMessage{"github": b["github"]}
	if CanonicalHash(aOnly) != CanonicalHash(bOnly) {
		t.Error("hash must ignore whitespace and key order")
	}
	if CanonicalHash(a) == CanonicalHash(b) {
		t.Error("semantically different snapshots must hash differently")
	}

	changed := map[string]json.RawMessage{
		"github": json.RawMessage(`{"command":"npx","type":"stdio","env":{"T":"1"}}`),
		"memory": a["memory"],
	}
	if CanonicalHash(a) == CanonicalHash(changed) {
		t.Error("a value change must change the hash")
	}
}

func TestReadSourceFileMissing(t *testing.T) {
	if _, err := ReadSourceFile(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("missing source file must error (never wipe the mirror)")
	}
}

func TestReadSourceFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"memory":{"command":"mcp-server-memory"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := ReadSourceFile(path)
	if err != nil {
		t.Fatalf("ReadSourceFile: %v", err)
	}
	if _, ok := servers["memory"]; !ok {
		t.Fatal("memory server missing")
	}
}

func TestMergeForClaim(t *testing.T) {
	synced := map[string]json.RawMessage{
		"playwright": json.RawMessage(`{"command":"npx"}`),
		"github":     json.RawMessage(`{"command":"npx-synced"}`),
	}

	t.Run("nil manual becomes full mirror", func(t *testing.T) {
		merged, overridden := MergeForClaim(nil, synced)
		if merged == nil || overridden != 0 {
			t.Fatalf("want merged config, got %v (%d overridden)", merged, overridden)
		}
		var doc struct {
			McpServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(merged, &doc); err != nil {
			t.Fatalf("merged is not an object: %v", err)
		}
		if len(doc.McpServers) != 2 {
			t.Fatalf("want 2 servers, got %d", len(doc.McpServers))
		}
	})

	t.Run("manual wins on collision and other keys survive", func(t *testing.T) {
		manual := json.RawMessage(`{"mcpServers":{"github":{"command":"manual"}},"thinkingLevel":"high"}`)
		merged, overridden := MergeForClaim(manual, synced)
		if overridden != 1 {
			t.Fatalf("want 1 overridden, got %d", overridden)
		}
		var doc struct {
			McpServers map[string]struct {
				Command string `json:"command"`
			} `json:"mcpServers"`
			ThinkingLevel string `json:"thinkingLevel"`
		}
		if err := json.Unmarshal(merged, &doc); err != nil {
			t.Fatalf("unmarshal merged: %v", err)
		}
		if doc.McpServers["github"].Command != "manual" {
			t.Errorf("manual entry must win, got %q", doc.McpServers["github"].Command)
		}
		if _, ok := doc.McpServers["playwright"]; !ok {
			t.Error("synced-only entry missing")
		}
		if doc.ThinkingLevel != "high" {
			t.Errorf("manual top-level key lost: %q", doc.ThinkingLevel)
		}
	})

	t.Run("empty synced returns nil", func(t *testing.T) {
		merged, overridden := MergeForClaim(json.RawMessage(`{"mcpServers":{}}`), nil)
		if merged != nil || overridden != 0 {
			t.Fatalf("want nil merge, got %v", merged)
		}
	})

	t.Run("unparsable manual left untouched", func(t *testing.T) {
		merged, _ := MergeForClaim(json.RawMessage(`{not json`), synced)
		if merged != nil {
			t.Fatalf("want nil for unparsable manual, got %v", merged)
		}
	})
}

func TestRedactServerDefinition(t *testing.T) {
	def := json.RawMessage(`{
		"type": "stdio",
		"command": "node",
		"env": {"LLM_WIKI_API_TOKEN": "super-secret", "HOME": "/Users/x"},
		"headers": {"Authorization": "Bearer xyz"}
	}`)
	out := RedactServerDefinition(def)
	var doc struct {
		Command string            `json:"command"`
		Env     map[string]string `json:"env"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("redacted definition invalid: %v", err)
	}
	if doc.Command != "node" {
		t.Errorf("non-secret field lost: %q", doc.Command)
	}
	if doc.Env["LLM_WIKI_API_TOKEN"] != SecretMask || doc.Env["HOME"] != SecretMask {
		t.Errorf("env values not masked: %v", doc.Env)
	}
	if doc.Headers["Authorization"] != SecretMask {
		t.Errorf("header values not masked: %v", doc.Headers)
	}
	if strings.Contains(string(out), "super-secret") || strings.Contains(string(out), "Bearer") {
		t.Error("plaintext secret leaked through redaction")
	}

	if string(RedactServerDefinition(json.RawMessage(`not json`))) != "{}" {
		t.Error("unparsable definition must fail closed to {}")
	}
}

func TestConfigWithDefaults(t *testing.T) {
	cfg, err := Config{}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.SourcePath, ".claude.json") {
		t.Errorf("default source path wrong: %q", cfg.SourcePath)
	}
	if cfg.Interval != DefaultInterval {
		t.Errorf("default interval wrong: %v", cfg.Interval)
	}

	tiny, err := Config{Interval: time.Millisecond}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if tiny.Interval != MinInterval {
		t.Errorf("interval floor not applied: %v", tiny.Interval)
	}

	custom := filepath.Join(t.TempDir(), "custom.json")
	explicit, err := Config{SourcePath: custom}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if explicit.SourcePath != custom {
		t.Errorf("explicit SourcePath must be honored: %q", explicit.SourcePath)
	}
}
