package experimental

import (
	"strings"
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

// TestParseUserPluginContract pins the 0.5.88 P4 interaction-model
// contract rules: absent → auxiliary default (behavior-preserving —
// unclassified plugins never locked), invalid literals rejected,
// assignee REQUIRES a non-empty top-level leader_agent, and the legacy
// capabilities.leader block stays a read-side fallback.
func TestParseUserPluginContract(t *testing.T) {
	cases := []struct {
		name        string
		manifest    string
		wantModel   string
		wantLeader  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "empty manifest defaults to auxiliary",
			manifest:   `{}`,
			wantModel:  InteractionModelAuxiliary,
			wantLeader: "",
		},
		{
			name:       "absent interaction_model defaults to auxiliary",
			manifest:   `{"capabilities":{"agents":["a"]}}`,
			wantModel:  InteractionModelAuxiliary,
			wantLeader: "",
		},
		{
			name:       "explicit auxiliary accepted",
			manifest:   `{"interaction_model":"auxiliary"}`,
			wantModel:  InteractionModelAuxiliary,
			wantLeader: "",
		},
		{
			name:       "assignee with leader_agent accepted",
			manifest:   `{"interaction_model":"assignee","leader_agent":"my_leader"}`,
			wantModel:  InteractionModelAssignee,
			wantLeader: "my_leader",
		},
		{
			name:        "assignee without leader_agent rejected",
			manifest:    `{"interaction_model":"assignee"}`,
			wantErr:     true,
			errContains: "leader_agent is required",
		},
		{
			name:        "assignee with blank leader_agent rejected",
			manifest:    `{"interaction_model":"assignee","leader_agent":"   "}`,
			wantErr:     true,
			errContains: "leader_agent is required",
		},
		{
			name:        "invalid model literal rejected",
			manifest:    `{"interaction_model":"owner"}`,
			wantErr:     true,
			errContains: `interaction_model must be "assignee" or "auxiliary"`,
		},
		{
			name:       "legacy capabilities.leader resolves as fallback leader",
			manifest:   `{"capabilities":{"leader":"legacy_leader"}}`,
			wantModel:  InteractionModelAuxiliary,
			wantLeader: "legacy_leader",
		},
		{
			name:       "leader_agent preferred over capabilities.leader",
			manifest:   `{"interaction_model":"assignee","leader_agent":"new_leader","capabilities":{"leader":"old_leader"}}`,
			wantModel:  InteractionModelAssignee,
			wantLeader: "new_leader",
		},
		{
			name:       "malformed JSON resolves to auxiliary without error",
			manifest:   `{not json`,
			wantModel:  InteractionModelAuxiliary,
			wantLeader: "",
		},
		{
			name:       "empty blob resolves to auxiliary without error",
			manifest:   ``,
			wantModel:  InteractionModelAuxiliary,
			wantLeader: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseUserPluginContract([]byte(c.manifest))
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParseUserPluginContract(%s): expected error, got contract %+v", c.manifest, got)
				}
				if !strings.Contains(err.Error(), c.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), c.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseUserPluginContract(%s): unexpected error: %v", c.manifest, err)
			}
			if got.InteractionModel != c.wantModel {
				t.Errorf("InteractionModel = %q, want %q", got.InteractionModel, c.wantModel)
			}
			if got.LeaderAgent != c.wantLeader {
				t.Errorf("LeaderAgent = %q, want %q", got.LeaderAgent, c.wantLeader)
			}
			// The read-side helpers must agree with the parse result.
			if m := UserPluginInteractionModel([]byte(c.manifest)); m != c.wantModel {
				t.Errorf("UserPluginInteractionModel = %q, want %q", m, c.wantModel)
			}
			leader, ok := UserPluginLeaderAgent([]byte(c.manifest))
			if c.wantLeader == "" {
				if ok {
					t.Errorf("UserPluginLeaderAgent = (%q, true), want ok=false", leader)
				}
			} else if !ok || leader != c.wantLeader {
				t.Errorf("UserPluginLeaderAgent = (%q, %v), want (%q, true)", leader, ok, c.wantLeader)
			}
		})
	}
}

// TestUserPluginToFlagInteractionModel pins the row→Flag contract
// stamping: the manifest's interaction_model lands on
// Flag.InteractionModel (absent → auxiliary), so the registered flag
// space resolves user plugins exactly like built-ins.
func TestUserPluginToFlagInteractionModel(t *testing.T) {
	base := UserPluginRow{
		Slug:        "locky",
		FlagKey:     "user_locky",
		TriggerMode: "issue_select",
		RuntimeKind: "inline",
		Status:      "active",
	}

	flag := UserPluginToFlag(base)
	if flag.InteractionModel != InteractionModelAuxiliary {
		t.Errorf("absent manifest: InteractionModel = %q, want %q (default)", flag.InteractionModel, InteractionModelAuxiliary)
	}

	base.ManifestJSON = `{"interaction_model":"assignee","leader_agent":"locky_leader"}`
	flag = UserPluginToFlag(base)
	if flag.InteractionModel != InteractionModelAssignee {
		t.Errorf("assignee manifest: InteractionModel = %q, want %q", flag.InteractionModel, InteractionModelAssignee)
	}
	// Registry-level resolution (IsAssigneeModelLab over the registered
	// key) is pinned separately in TestUserPluginRegistryContractResolution.
}

// TestUserPluginRegistryContractResolution — 0.5.88 P4: once a stamped
// user-plugin flag is registered into the dynamic layer,
// InteractionModelOf and FlagByKey must resolve it like a built-in
// (this is the path boot registration and the plugin CRUD handlers
// exercise). Key-targeted cleanup: the registry is package-global
// state shared across the suite.
func TestUserPluginRegistryContractResolution(t *testing.T) {
	const key = "user_p4-contract-test"
	t.Cleanup(func() { UnregisterUserPlugin(key) })

	RegisterUserPlugins([]Flag{UserPluginToFlag(UserPluginRow{
		Slug:         "p4-contract-test",
		FlagKey:      key,
		TriggerMode:  "issue_select",
		RuntimeKind:  "inline",
		Status:       "active",
		ManifestJSON: `{"interaction_model":"assignee","leader_agent":"p4_leader"}`,
	})})

	if got := InteractionModelOf(key); got != InteractionModelAssignee {
		t.Errorf("InteractionModelOf(%q) = %q, want %q", key, got, InteractionModelAssignee)
	}
	if !IsAssigneeModelLab(key) {
		t.Errorf("IsAssigneeModelLab(%q) = false, want true (registered contract)", key)
	}
	f, ok := FlagByKey(key)
	if !ok {
		t.Fatalf("FlagByKey(%q) = ok=false, want true", key)
	}
	if f.InteractionModel != InteractionModelAssignee {
		t.Errorf("FlagByKey(%q).InteractionModel = %q, want %q", key, f.InteractionModel, InteractionModelAssignee)
	}

	// After unregistration the key resolves as unclassified — no stale
	// lock survives a plugin delete.
	UnregisterUserPlugin(key)
	if got := InteractionModelOf(key); got != "" {
		t.Errorf("InteractionModelOf after unregister = %q, want empty (unclassified)", got)
	}
}
