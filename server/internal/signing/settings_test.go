package signing

import (
	"encoding/json"
	"testing"
)

func TestEnabledFromSettings(t *testing.T) {
	cases := []struct {
		name string
		json string
		want bool
	}{
		{"missing key", `{}`, false},
		{"empty object", `{}`, false},
		{"nil payload", ``, false},
		{"explicit false", `{` + `"signature_authorization_enabled":false}`, false},
		{"explicit true", `{` + `"signature_authorization_enabled":true}`, true},
		{"true alongside other keys", `{"default_runtime_id":"abc",` + `"signature_authorization_enabled":true}`, true},
		// The switch is boolean-true ONLY — stringy/truthy shapes read as
		// disabled so a sloppy writer can never arm the feature by accident.
		{"string true", `{` + `"signature_authorization_enabled":"true"}`, false},
		{"number one", `{` + `"signature_authorization_enabled":1}`, false},
		{"null", `{` + `"signature_authorization_enabled":null}`, false},
		{"malformed json", `not-json`, false},
		{"non-object json", `[]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EnabledFromSettings([]byte(tc.json)); got != tc.want {
				t.Fatalf("EnabledFromSettings(%q) = %v, want %v", tc.json, got, tc.want)
			}
		})
	}
}

func TestWithEnabledPreservesUnknownKeys(t *testing.T) {
	out, err := WithEnabled(map[string]any{"default_runtime_id": "abc"}, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !EnabledFromSettings(raw) {
		t.Fatal("merged payload must read back enabled")
	}
	if string(raw) == "" || !json.Valid(raw) {
		t.Fatal("merged payload must be valid json")
	}
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	if parsed["default_runtime_id"] != "abc" {
		t.Fatal("unknown keys must survive the merge (append-only settings law)")
	}

	// Disabling must write an explicit false (auditability), not delete the key.
	out, _ = WithEnabled(out, false)
	raw, _ = json.Marshal(out)
	if EnabledFromSettings(raw) {
		t.Fatal("disabled payload must read as disabled")
	}
	if _, ok := out[SettingsKey]; !ok {
		t.Fatal("disable must keep the key with an explicit false")
	}
}
