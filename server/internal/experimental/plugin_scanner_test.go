package experimental

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsUserPluginKey pins the namespace contract: only keys carrying the
// "user_" prefix belong to the user plugin layer. Built-in catalog keys and
// empty strings must be rejected so lookups route to the right layer.
func TestIsUserPluginKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"user_foo", true},
		{"user_", true},
		{"user_multi_word_slug", true},
		{"foo", false},
		{"", false},
		{"User_foo", false}, // case-sensitive prefix
		{"prefix_user_foo", false},
	}
	for _, c := range cases {
		if got := IsUserPluginKey(c.key); got != c.want {
			t.Errorf("IsUserPluginKey(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

// TestUserPluginToFlag verifies the row→Flag mapping, especially the
// HideFromIssueLabPicker rule: "auto" trigger plugins run globally and must
// be hidden from the per-issue picker, while "issue_select" ones stay visible.
func TestUserPluginToFlag(t *testing.T) {
	row := UserPluginRow{
		Slug:          "my-plugin",
		FlagKey:       "user_my-plugin",
		TitleEn:       "My Plugin",
		TitleZh:       "我的插件",
		DescriptionEn: "does things",
		DescriptionZh: "做事情",
		TriggerMode:   "issue_select",
		RuntimeKind:   "inline",
		Status:        "active",
	}

	flag := UserPluginToFlag(row)
	if flag.Key != "user_my-plugin" {
		t.Errorf("Key = %q, want user_my-plugin", flag.Key)
	}
	if flag.DefaultVal {
		t.Error("DefaultVal = true, want false (user plugins default off)")
	}
	if flag.Title.En != "My Plugin" || flag.Title.Zh != "我的插件" {
		t.Errorf("Title = %+v, want localized pair", flag.Title)
	}
	if flag.Description.En != "does things" || flag.Description.Zh != "做事情" {
		t.Errorf("Description = %+v, want localized pair", flag.Description)
	}
	if flag.RuntimeKind != "inline" {
		t.Errorf("RuntimeKind = %q, want inline", flag.RuntimeKind)
	}
	if flag.HideFromIssueLabPicker {
		t.Error("HideFromIssueLabPicker = true for issue_select, want false")
	}

	row.TriggerMode = "auto"
	if !UserPluginToFlag(row).HideFromIssueLabPicker {
		t.Error("HideFromIssueLabPicker = false for auto trigger, want true")
	}
}

// TestUserPluginsToFlags verifies the batch conversion skips any row whose
// status is not exactly "active" (disabled/deleted rows must not surface).
func TestUserPluginsToFlags(t *testing.T) {
	rows := []UserPluginRow{
		{Slug: "a", FlagKey: "user_a", TriggerMode: "issue_select", Status: "active"},
		{Slug: "b", FlagKey: "user_b", TriggerMode: "issue_select", Status: "disabled"},
		{Slug: "c", FlagKey: "user_c", TriggerMode: "issue_select", Status: "deleted"},
		{Slug: "d", FlagKey: "user_d", TriggerMode: "auto", Status: "active"},
	}

	flags := UserPluginsToFlags(rows)
	if len(flags) != 2 {
		t.Fatalf("got %d flags, want 2 (only active rows)", len(flags))
	}
	got := map[string]bool{}
	for _, f := range flags {
		got[f.Key] = true
	}
	if !got["user_a"] || !got["user_d"] {
		t.Errorf("expected user_a and user_d, got %v", got)
	}
	if got["user_b"] || got["user_c"] {
		t.Errorf("non-active rows leaked into flags: %v", got)
	}
}

// TestUserPluginsToFlagsEmpty verifies an empty input yields a non-nil empty
// slice (callers range over it without a nil guard).
func TestUserPluginsToFlagsEmpty(t *testing.T) {
	flags := UserPluginsToFlags(nil)
	if flags == nil {
		t.Fatal("UserPluginsToFlags(nil) = nil, want empty non-nil slice")
	}
	if len(flags) != 0 {
		t.Fatalf("got %d flags, want 0", len(flags))
	}
}

// TestScanUserPluginDirMissing verifies a missing/unreadable directory is a
// soft skip (returns nil, no panic).
func TestScanUserPluginDirMissing(t *testing.T) {
	if flags := ScanUserPluginDir(filepath.Join(t.TempDir(), "does-not-exist")); flags != nil {
		t.Errorf("ScanUserPluginDir(missing) = %v, want nil", flags)
	}
}

// TestScanUserPluginDir exercises the filesystem discovery path: a valid
// manifest is parsed, a manifest-declared name overrides the directory name,
// a runtime kind defaults to "inline" when absent, non-directory entries and
// directories without a manifest are ignored, and invalid JSON is skipped.
func TestScanUserPluginDir(t *testing.T) {
	dir := t.TempDir()

	// Valid plugin, explicit runtime kind, manifest name overrides dir name.
	writePlugin(t, dir, "alpha-dir", `{
		"metadata": {
			"name": "alpha",
			"title": {"en": "Alpha", "zh": "阿尔法"},
			"description": {"en": "first", "zh": "第一"}
		},
		"spec": {"runtime": {"kind": "subprocess"}}
	}`)

	// Valid plugin, no runtime kind → defaults to "inline"; no name → dir name.
	writePlugin(t, dir, "beta", `{
		"metadata": {"title": {"en": "Beta"}}
	}`)

	// Directory without a manifest — ignored.
	if err := os.Mkdir(filepath.Join(dir, "no-manifest"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Directory with invalid JSON manifest — skipped.
	writePlugin(t, dir, "broken", `{not json`)

	// A plain file at the top level — ignored (not a directory).
	if err := os.WriteFile(filepath.Join(dir, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := ScanUserPluginDir(dir)
	if len(flags) != 2 {
		t.Fatalf("got %d flags, want 2 (alpha + beta)", len(flags))
	}

	byKey := map[string]Flag{}
	for _, f := range flags {
		byKey[f.Key] = f
	}

	alpha, ok := byKey["user_alpha"]
	if !ok {
		t.Fatalf("expected key user_alpha (manifest name override), got keys %v", keysOf(byKey))
	}
	if alpha.RuntimeKind != "subprocess" {
		t.Errorf("alpha RuntimeKind = %q, want subprocess", alpha.RuntimeKind)
	}
	if alpha.Title.En != "Alpha" || alpha.Title.Zh != "阿尔法" {
		t.Errorf("alpha Title = %+v, want localized pair", alpha.Title)
	}
	if alpha.DefaultVal {
		t.Error("alpha DefaultVal = true, want false")
	}

	beta, ok := byKey["user_beta"]
	if !ok {
		t.Fatalf("expected key user_beta (dir name), got keys %v", keysOf(byKey))
	}
	if beta.RuntimeKind != "inline" {
		t.Errorf("beta RuntimeKind = %q, want inline (default)", beta.RuntimeKind)
	}
}

// writePlugin creates dir/<name>/manifest.json with the given content.
func writePlugin(t *testing.T, root, name, manifest string) {
	t.Helper()
	pdir := filepath.Join(root, name)
	if err := os.Mkdir(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func keysOf(m map[string]Flag) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
