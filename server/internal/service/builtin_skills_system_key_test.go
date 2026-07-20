package service

import (
	"strings"
	"testing"
)

// TestLoadBuiltinSkillByName covers the 0.3.51 public helper used by
// handler.daemon.go::loadSystemPromptBinding to look up the Skill body
// that should be prepended to an agent's Instructions when
// agent.system_key is set.
//
// The contract has two sides:
//   1. Known Skill names return their SKILL.md body verbatim.
//   2. Unknown / malformed names return (zero, false) so the daemon
//      can log a warning and skip the binding rather than silently
//      stripping it.
//
// Regression coverage: 0.3.51 added multica-constitution-agent as
// the first system-prompt binding. If a future refactor moves the
// Skill out of builtin_skills/multica-constitution-agent/ this test
// fails loudly — better here than as a silent "constitution_agent
// binding no longer applies" user-visible regression.
func TestLoadBuiltinSkillByName(t *testing.T) {
	t.Run("known skill returns body", func(t *testing.T) {
		skill, ok := LoadBuiltinSkillByName("multica-constitution-agent")
		if !ok {
			t.Fatal("expected multica-constitution-agent to be loadable")
		}
		if skill.Name != "multica-constitution-agent" {
			t.Errorf("skill.Name = %q, want multica-constitution-agent", skill.Name)
		}
		if skill.Content == "" {
			t.Error("expected non-empty SKILL.md body")
		}
		// Sanity-check the body — it should declare the binding trigger
		// so a future re-writer of SKILL.md doesn't accidentally drop
		// the contract reference.
		if !strings.Contains(skill.Content, "system_key") {
			t.Error("constitution skill body should reference system_key; got body without the binding trigger")
		}
	})

	t.Run("unknown skill returns false", func(t *testing.T) {
		_, ok := LoadBuiltinSkillByName("multica-does-not-exist")
		if ok {
			t.Error("expected unknown skill to return (zero, false)")
		}
	})

	t.Run("empty name returns false", func(t *testing.T) {
		_, ok := LoadBuiltinSkillByName("")
		if ok {
			t.Error("expected empty name to return (zero, false)")
		}
	})
}