package service

import (
	"strings"
	"testing"

	skillpkg "github.com/multica-ai/multica/server/internal/skill"
)

// Lab-capability evals. Incident #505 (2026-09-28): a squad leader was asked to
// "委托群体推演实验功能对该方案做假想推定" and answered that the workspace had no
// such plugin, then role-played four scenarios instead of invoking the engine.
//
// The cause was NOT a missing capability — `multica pythia issue-forecast`
// existed the whole time and needs no lab binding. The cause was the skill's
// FRONTMATTER DESCRIPTION, which is what the runtime shows the model when
// deciding whether to load a skill: the daemon re-parses it out of the skill
// body on the agent machine (internal/daemon/local_skills.go) and writes the
// skill to disk, where the provider CLI loads it natively. That description
// was (a) English-only, so a Chinese request never matched, and (b) carried
// "Do not use it for chat / issues", which contradicts the issue-forecast path
// in the same file and reads to the model as "issues are excluded".
//
// NOTE: builtin AgentSkillData.Description is deliberately empty — loadBuiltinSkill
// fills Name + Content only, and the description is recovered downstream from the
// body. These tests therefore parse the frontmatter, exactly as the daemon does,
// instead of reading the struct field.

// builtinSkillFrontmatter returns (name, description) for a loaded built-in
// skill, parsed the way the daemon parses it on the agent machine.
func builtinSkillFrontmatter(t *testing.T, name string) string {
	t.Helper()
	for _, s := range loadBuiltinSkills() {
		if s.Name != name {
			continue
		}
		if s.Description != "" {
			return s.Description
		}
		_, desc := skillpkg.ParseSkillFrontmatter(s.Content)
		return desc
	}
	t.Fatalf("built-in skill %q missing — the loader walks the skill directory, so a rename drops it from every agent", name)
	return ""
}

// TestLabSkillsCarryChineseTriggerWords pins that the two lab-facing skills
// advertise their capability in the user's language. Without CJK trigger
// words the description never matches a Chinese request like "推演一下这个方案".
func TestLabSkillsCarryChineseTriggerWords(t *testing.T) {
	for _, name := range []string{"multica-pythia", "multica-labs"} {
		desc := builtinSkillFrontmatter(t, name)
		if desc == "" {
			t.Errorf("%s has no frontmatter description; the runtime cannot decide when to load it", name)
			continue
		}
		if !strings.Contains(desc, "推演") {
			t.Errorf("%s description has no Chinese trigger word (推演); a Chinese request will never match it.\n got: %s", name, desc)
		}
	}
}

// TestPythiaSkillDescriptionAdvertisesTheIssueForecastCommand pins that the
// agent can find the one command that answers a 推演 request WITHOUT a lab
// binding. If this regresses, agents fall back to role-playing again.
func TestPythiaSkillDescriptionAdvertisesTheIssueForecastCommand(t *testing.T) {
	desc := builtinSkillFrontmatter(t, "multica-pythia")
	if !strings.Contains(desc, "multica pythia issue-forecast") {
		t.Errorf("multica-pythia description must name the issue-forecast command verbatim; it is the only path that needs no lab binding.\n got: %s", desc)
	}
}

// TestPythiaSkillDescriptionDoesNotExcludeIssues pins the exact regression from
// incident #505. The old description told the agent not to use Pythia "for
// chat / issues" while the same file documents an issue-bound forecast verb —
// the model reads the exclusion and skips the skill while working on an issue.
func TestPythiaSkillDescriptionDoesNotExcludeIssues(t *testing.T) {
	desc := builtinSkillFrontmatter(t, "multica-pythia")
	if strings.Contains(desc, "Do not use it for chat / issues") {
		t.Errorf("multica-pythia description must not exclude issues; it contradicts its own issue-forecast verb and blinds the agent (incident #505).\n got: %s", desc)
	}
}

// TestPythiaSkillBodyDoesNotGateIssueForecastOnStatusProbe pins the second half
// of incident #505. The body instructed the agent to run `multica pythia status`
// first and STOP if the answer was not `ready` — but from the CLI that command
// always reports `unknown` by design (cmd_pythia.go: the CLI cannot introspect
// the desktop-managed subprocess). Following the instruction literally made the
// agent abandon every forecast. The issue-forecast path needs no status probe.
func TestPythiaSkillBodyDoesNotGateIssueForecastOnStatusProbe(t *testing.T) {
	for _, s := range loadBuiltinSkills() {
		if s.Name != "multica-pythia" {
			continue
		}
		if !strings.Contains(s.Content, "skip the status probe") {
			t.Errorf("multica-pythia body must tell the agent to skip the status probe for issue work; the CLI probe always reports unknown by design (incident #505)")
		}
		if !strings.Contains(s.Content, "not evidence that Pythia is stopped") {
			t.Errorf("multica-pythia body must state that `status=unknown` from the CLI is expected, not a stopped service (incident #505)")
		}
		return
	}
	t.Fatal("multica-pythia not loaded")
}

// TestLabsCatalogSkillAdvertisesClaudeScienceDelegation pins the 2026-09-29
// delegation-discovery incident. The catalog skill told agents that
// claude_science_lab "需用户绑定 / 不要试图用 CLI 驱动" while the CLI's
// `multica lab delegate` (0.5.88) and `issue create/update --lab-source`
// (0.5.126) both bind and dispatch the lab without any user action — and the
// claim-time delegation brief was simultaneously empty because its
// prefs-only source query missed the default-on flag. An agent that read
// this skill thus refused to delegate and asked the user to click instead.
// If this regresses, agents fall back to asking the user to bind manually.
func TestLabsCatalogSkillAdvertisesClaudeScienceDelegation(t *testing.T) {
	desc := builtinSkillFrontmatter(t, "multica-labs")
	if !strings.Contains(desc, "multica lab delegate") {
		t.Errorf("multica-labs description must name the lab delegate command for claude_science_lab; it is the one-shot agent-initiated delegation path.\n got: %s", desc)
	}

	var body string
	for _, s := range loadBuiltinSkills() {
		if s.Name == "multica-labs" {
			body = s.Content
			break
		}
	}
	if body == "" {
		t.Fatal("multica-labs not loaded")
	}
	if !strings.Contains(body, `multica lab delegate --parent`) || !strings.Contains(body, "claude_science_lab") {
		t.Errorf("multica-labs body must teach `multica lab delegate --parent <issue-id> claude_science_lab \"<task>\"`; an agent that cannot find the command will ask the user to bind the lab manually")
	}
	// Needles are chosen so the pythia phrase 无需用户绑定 cannot
	// substring-match them: the stale claims being pinned are the
	// ⚠️ table row and the "CLI 已废弃 / 不要试图用 CLI 驱动" clause.
	for _, stale := range []string{"⚠️ 需用户绑定", "不要试图用 CLI 驱动", "CLI 已废弃"} {
		if strings.Contains(body, stale) {
			t.Errorf("multica-labs body still claims claude_science_lab needs user binding / refuses CLI (%q) — the delegate + --lab-source paths contradict it and the agent will follow the stale claim (2026-09-29 incident)", stale)
		}
	}
}
