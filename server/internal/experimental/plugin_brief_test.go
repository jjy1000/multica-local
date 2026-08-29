package experimental

// plugin_brief_test.go — DB-less pins for the 0.5.89 provisioning contract:
// the briefing constant's verb surface, capabilities.agents_inline /
// skills_inline validation, and the skills_visibility literal handling.

import (
	"regexp"
	"strings"
	"testing"
)

// TestPluginManagementBriefVerbsAreDeclared ties the briefing to a fixed
// verb set: an edit that adds a command line here without adding the CLI
// verb fails the cmd/multica contract test; an edit that renames a verb
// here fails THIS test until the set is consciously updated on both sides.
func TestPluginManagementBriefVerbsAreDeclared(t *testing.T) {
	re := regexp.MustCompile(`multica lab ([a-z|-]+)`)
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(PluginManagementBrief, -1) {
		// "enable|disable" is one shorthand line advertising two verbs.
		for _, part := range strings.Split(m[1], "|") {
			found[part] = true
		}
	}
	allowed := map[string]bool{
		"create": true, "list": true, "inspect": true,
		"enable": true, "disable": true, "delete": true,
	}
	if len(found) == 0 {
		t.Fatalf("briefing advertises no verbs at all: %q", PluginManagementBrief)
	}
	for verb := range found {
		if !allowed[verb] {
			t.Errorf("briefing advertises verb %q which is outside the declared five-verb surface; update both the briefing and the CLI together", verb)
		}
	}
	for _, required := range []string{"create", "list", "inspect", "enable", "disable", "delete"} {
		if !found[required] {
			t.Errorf("briefing must advertise the %q verb (the conversational management contract)", required)
		}
	}
	if !strings.Contains(PluginManagementBrief, "--dry-run") || !strings.Contains(PluginManagementBrief, "--confirm") {
		t.Errorf("briefing must teach the two-step delete protocol (--dry-run then --confirm)")
	}
}

func TestUserPluginInlineAgentsValidation(t *testing.T) {
	valid := []byte(`{"capabilities":{"agents_inline":[
		{"name":"lab-lead","instructions":"do science","model":"claude"},
		{"name":"lab-helper","description":"assistant"}]}}`)
	specs, err := UserPluginInlineAgents(valid)
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if len(specs) != 2 || specs[0].Name != "lab-lead" || specs[0].Instructions != "do science" {
		t.Fatalf("unexpected specs: %+v", specs)
	}

	if _, err := UserPluginInlineAgents([]byte(`{"capabilities":{"agents_inline":[{"name":"  "}]}}`)); err == nil {
		t.Errorf("empty name must be rejected")
	}
	if _, err := UserPluginInlineAgents([]byte(`{"capabilities":{"agents_inline":[{"name":"a"},{"name":"a"}]}}`)); err == nil {
		t.Errorf("duplicate names must be rejected")
	}
	if _, err := UserPluginInlineAgents([]byte(`{"capabilities":{"agents_inline":[{"name":"` + strings.Repeat("x", 65) + `"}]}}`)); err == nil {
		t.Errorf("names over 64 chars must be rejected")
	}
	specs, err = UserPluginInlineAgents([]byte(`{}`))
	if err != nil || len(specs) != 0 {
		t.Errorf("absent block must resolve to empty, no error; got %v, %v", specs, err)
	}
}

func TestUserPluginInlineSkillsValidation(t *testing.T) {
	specs, err := UserPluginInlineSkills([]byte(`{"capabilities":{"skills_inline":[{"name":"sim","content":"# Sim"}]}}`))
	if err != nil || len(specs) != 1 || specs[0].Name != "sim" {
		t.Fatalf("unexpected: %+v, %v", specs, err)
	}
	if _, err := UserPluginInlineSkills([]byte(`{"capabilities":{"skills_inline":[{"name":"dup"},{"name":"dup"}]}}`)); err == nil {
		t.Errorf("duplicate skill names must be rejected")
	}
}

func TestUserPluginSkillsVisibilityContract(t *testing.T) {
	cases := []struct {
		manifest string
		want     string
		rawErr   bool
	}{
		{manifest: `{}`, want: PluginSkillsVisibilityGlobal},
		{manifest: `{"capabilities":{"skills_visibility":"global"}}`, want: PluginSkillsVisibilityGlobal},
		{manifest: `{"capabilities":{"skills_visibility":"lab_scoped"}}`, want: PluginSkillsVisibilityLabScoped},
		{manifest: `{"capabilities":{"skills_visibility":"sometimes"}}`, want: PluginSkillsVisibilityGlobal, rawErr: true},
	}
	for _, tc := range cases {
		if got := UserPluginSkillsVisibility([]byte(tc.manifest)); got != tc.want {
			t.Errorf("UserPluginSkillsVisibility(%s) = %q, want %q", tc.manifest, got, tc.want)
		}
		err := ValidateUserPluginSkillsVisibility(UserPluginRawSkillsVisibility([]byte(tc.manifest)))
		if tc.rawErr && err == nil {
			t.Errorf("raw literal for %s must fail write-side validation", tc.manifest)
		}
		if !tc.rawErr && err != nil {
			t.Errorf("raw literal for %s must pass write-side validation: %v", tc.manifest, err)
		}
	}
	// Absent stays distinct from an explicit "global" on the write side:
	// legacy manifests pass validation without being rewritten.
	if raw := UserPluginRawSkillsVisibility([]byte(`{}`)); raw != "" {
		t.Errorf("absent skills_visibility raw = %q, want empty", raw)
	}
}
