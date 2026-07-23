package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidateUserPluginSlug pins the slug contract: 2-64 chars, lowercase
// alphanumeric segments joined by single hyphens, no leading/trailing hyphen.
func TestValidateUserPluginSlug(t *testing.T) {
	cases := []struct {
		slug string
		want bool
	}{
		{"ab", true},
		{"my-plugin", true},
		{"plugin123", true},
		{"a-b-c-d", true},
		{strings.Repeat("a", 64), true},

		{"", false},
		{"a", false},                     // too short
		{strings.Repeat("a", 65), false}, // too long
		{"-plugin", false},               // leading hyphen
		{"plugin-", false},               // trailing hyphen
		{"plu--gin", false},              // double hyphen
		{"Plugin", false},                // uppercase
		{"my_plugin", false},             // underscore
		{"my plugin", false},             // space
		{"plugin!", false},               // punctuation
	}
	for _, c := range cases {
		if got := validateUserPluginSlug(c.slug); got != c.want {
			t.Errorf("validateUserPluginSlug(%q) = %v, want %v", c.slug, got, c.want)
		}
	}
}

// TestIsValidTriggerMode pins the two accepted trigger modes.
func TestIsValidTriggerMode(t *testing.T) {
	valid := []string{"auto", "issue_select"}
	invalid := []string{"", "Auto", "manual", "issue-select", "select"}
	for _, s := range valid {
		if !isValidTriggerMode(s) {
			t.Errorf("isValidTriggerMode(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isValidTriggerMode(s) {
			t.Errorf("isValidTriggerMode(%q) = true, want false", s)
		}
	}
}

// TestIsValidRuntimeKind pins the three accepted runtime kinds.
func TestIsValidRuntimeKind(t *testing.T) {
	valid := []string{"none", "inline", "subprocess"}
	invalid := []string{"", "None", "process", "sub-process", "docker"}
	for _, s := range valid {
		if !isValidRuntimeKind(s) {
			t.Errorf("isValidRuntimeKind(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isValidRuntimeKind(s) {
			t.Errorf("isValidRuntimeKind(%q) = true, want false", s)
		}
	}
}

// TestIsValidPluginStatus pins the three accepted lifecycle statuses.
func TestIsValidPluginStatus(t *testing.T) {
	valid := []string{"active", "disabled", "deleted"}
	invalid := []string{"", "Active", "enabled", "removed", "pending"}
	for _, s := range valid {
		if !isValidPluginStatus(s) {
			t.Errorf("isValidPluginStatus(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isValidPluginStatus(s) {
			t.Errorf("isValidPluginStatus(%q) = true, want false", s)
		}
	}
}

// TestNormalizeManifest verifies the storage-safety contract: an empty
// manifest defaults to "{}", a valid blob passes through verbatim, and an
// invalid blob is rejected so the JSONB insert can never fail server-side.
func TestNormalizeManifest(t *testing.T) {
	// Empty/absent → "{}".
	got, ok := normalizeManifest(nil)
	if !ok {
		t.Fatal("normalizeManifest(nil) ok = false, want true")
	}
	if string(got) != "{}" {
		t.Errorf("normalizeManifest(nil) = %q, want {}", string(got))
	}

	got, ok = normalizeManifest(json.RawMessage(""))
	if !ok || string(got) != "{}" {
		t.Errorf("normalizeManifest(empty) = %q, %v; want {}, true", string(got), ok)
	}

	// Valid JSON passes through unchanged.
	valid := json.RawMessage(`{"capabilities":{"agents":["x"]}}`)
	got, ok = normalizeManifest(valid)
	if !ok {
		t.Fatal("normalizeManifest(valid) ok = false, want true")
	}
	if string(got) != string(valid) {
		t.Errorf("normalizeManifest(valid) = %q, want verbatim %q", string(got), string(valid))
	}

	// Invalid JSON is rejected.
	for _, bad := range []string{"{not json", "", `{"a":}`, "}{"} {
		if bad == "" {
			continue // empty is the default-to-{} case, tested above
		}
		if got, ok := normalizeManifest(json.RawMessage(bad)); ok {
			t.Errorf("normalizeManifest(%q) ok = true (got %q), want false", bad, string(got))
		}
	}
}
