package mcpsync

import (
	"bytes"
	"encoding/json"
)

// merge.go holds the pure claim-time merge and redaction helpers. They are
// exported so internal/handler can use them and unit tests can pin the
// precedence rules without a database.

// MergeForClaim overlays the synced Claude Code mirror beneath the agent's
// manual mcp_config for claim dispatch.
//
// Precedence: manual entries win on name collisions — an explicit per-agent
// config is more specific than the workspace-wide mirror. The manual
// document's other top-level keys are preserved as-is.
//
// Returns (nil, 0) when there is nothing to merge (no synced servers), so
// the claim payload keeps omitting mcp_config for agents that never
// configured one, and (nil, 0) when the manual config is unparsable —
// forwarding garbage merged with the mirror would turn a config the CLI
// already rejects into one that silently runs, so we leave the manual value
// untouched and let the existing CLI-side failure surface it. The second
// return is the number of synced server names the manual config overrides
// (call-site logging only).
func MergeForClaim(manual json.RawMessage, synced map[string]json.RawMessage) (json.RawMessage, int) {
	if len(synced) == 0 {
		return nil, 0
	}

	trimmed := bytes.TrimSpace(manual)
	var doc map[string]json.RawMessage
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(trimmed, &doc); err != nil || doc == nil {
			return nil, 0
		}
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}

	var servers map[string]json.RawMessage
	if raw, ok := doc["mcpServers"]; ok {
		// A non-object mcpServers value is a broken manual config; treat it
		// as absent for merging — same policy as the unparsable document.
		_ = json.Unmarshal(raw, &servers)
	}
	if servers == nil {
		servers = map[string]json.RawMessage{}
	}

	overridden := 0
	for name, def := range synced {
		if _, exists := servers[name]; exists {
			overridden++
			continue
		}
		servers[name] = def
	}

	doc["mcpServers"] = mustMarshalSorted(servers)
	return mustMarshalSorted(doc), overridden
}

// SecretMask replaces env / header values in redacted API responses. Keys
// stay visible so the settings tab can show what a server needs without the
// values — the DB-side plaintext never crosses the API boundary for the
// mirror (the agent mcp_config redaction contract, by contrast, drops the
// whole config; the mirror's list UI needs the shape, so it masks per-field).
const SecretMask = "********"

// secretMapKeys are the definition fields whose VALUES are credentials.
// Nested or unusually-named fields are not secret-bearing by MCP convention;
// new transport fields carrying secrets must be added here.
var secretMapKeys = []string{"env", "headers"}

// RedactServerDefinition masks env / header values in one synced server
// definition. Fail-closed: anything that doesn't parse returns an empty
// object, never the input.
func RedactServerDefinition(raw json.RawMessage) json.RawMessage {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return json.RawMessage("{}")
	}
	for _, key := range secretMapKeys {
		value, ok := doc[key]
		if !ok {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(value, &m); err != nil || m == nil {
			continue
		}
		for k := range m {
			m[k] = json.RawMessage(`"` + SecretMask + `"`)
		}
		doc[key] = mustMarshalSorted(m)
	}
	return mustMarshalSorted(doc)
}

// mustMarshalSorted marshals with sorted keys and no HTML escaping. Inputs
// here are json-decoded values or RawMessage maps, so Marshal cannot fail;
// on the impossible path the raw bytes are the least-bad fallback.
func mustMarshalSorted(v any) json.RawMessage {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}
