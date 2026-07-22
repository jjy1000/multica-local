package service

import (
	"testing"
)

// (0.3.57: constitution_agent lab retired alongside migration 165.
// The system_key binding test was for the
// multica-constitution-agent Skill that shipped in 0.3.51; with the
// Skill directory removed, LoadBuiltinSkillByName now returns
// (zero, false) for that name and unknown-name test coverage remains
// intact. The framework test below mirrors the unknown-name path
// against a non-existent skill to keep regression coverage tight.)
func TestLoadBuiltinSkillByName(t *testing.T) {
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