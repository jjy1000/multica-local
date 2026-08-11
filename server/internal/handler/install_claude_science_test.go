package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestClaudeScienceLabManifestSkillReferences_AllExist is a regression
// test for the "manifest claims a skill we never shipped" class of
// bugs. The lab manifest's capabilities.skills array is the
// declarative contract the boot loader reads (see
// server/internal/service/task.go::enabledPluginSkillNames and the
// experiment skill loader in builtin_skills.go::scanExperimentSkills):
// every name there must resolve to a real builtin skill directory,
// otherwise the renderer / agent runtime silently lose the skill with
// no error path.
//
// 2026-08-11 incident: claude_science_lab/manifest.json referenced
// "multica-claude-literature" and "multica-claude-reviewer" — neither
// directory exists under server/internal/service/builtin_skills/, so
// the contract was a lie. The dead references were dropped from the
// manifest; this test fails loudly if any future contributor re-adds
// a name that does not have a matching directory on disk.
//
// Test placement note: this is a manifest contract test, not a
// handler behavioural test — it does not require testPool / testHandler
// and runs in any environment where the manifest + builtin_skills
// tree are present.
func TestClaudeScienceLabManifestSkillReferences_AllExist(t *testing.T) {
	// Resolve the lab manifest relative to the package directory
	// (server/internal/handler/ → ../../../ → repo root).
	manifestPath, err := filepath.Abs(filepath.Join(
		"..", "..", "..",
		"apps", "desktop", "resources",
		"experiments", "claude_science_lab", "manifest.json",
	))
	if err != nil {
		t.Fatalf("abs manifest path: %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Skipf("lab manifest not present at %s: %v", manifestPath, err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read lab manifest: %v", err)
	}
	var doc struct {
		Spec struct {
			Capabilities struct {
				Skills []string `json:"skills"`
			} `json:"capabilities"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse lab manifest: %v", err)
	}
	if len(doc.Spec.Capabilities.Skills) == 0 {
		t.Fatal("lab manifest declares no skills — install would have nothing to inject")
	}

	// Resolve the builtin_skills root similarly.
	// server/internal/handler/ → ../../internal/service/builtin_skills
	skillsRoot, err := filepath.Abs(filepath.Join(
		"..", "..", "internal", "service", "builtin_skills",
	))
	if err != nil {
		t.Fatalf("abs builtin_skills path: %v", err)
	}
	for _, name := range doc.Spec.Capabilities.Skills {
		if _, err := os.Stat(filepath.Join(skillsRoot, name)); err != nil {
			t.Errorf("dead skill reference %q: no directory at %s (%v) — "+
				"either port the skill into server/internal/service/builtin_skills/ "+
				"or drop it from the manifest's capabilities.skills array",
				name, filepath.Join(skillsRoot, name), err)
		}
	}
}