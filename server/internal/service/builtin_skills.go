package service

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed builtin_skills
var builtinSkillsFS embed.FS

const builtinSkillsRoot = "builtin_skills"

// experimentSkillDirEnv is the env var the desktop main process
// injects when it spawns the bundled server (see
// apps/desktop/src/main/server-manager.ts). The boot-time scan reads
// from this path; an empty value skips the experimental skill scan
// entirely (default for dev / unit tests). Mirrors ManifestResourceDirEnv
// in handler/install_claude_science.go.
const experimentSkillDirEnv = "MULTICA_RESOURCES_DIR"

// experimentSkillSubdir is the relative directory under
// MULTICA_RESOURCES_DIR where experiment skills live. The catalog
// entry's `capabilities.skills` field lists the names; the on-disk
// layout is `<resources>/skills/<flagKey>/<skillName>/SKILL.md`.
const experimentSkillSubdir = "skills"

// BuiltinSkills returns the platform's built-in skills, embedded at compile
// time. Every agent receives these on top of its workspace-bound skills, so
// they teach platform-wide "how to" workflows (e.g. mentioning) that the
// runtime brief intentionally leaves to skills.
//
// Layout: builtin_skills/<name>/SKILL.md plus optional supporting files. The
// <name> directory carries a "multica-" prefix so its on-disk slug can never
// collide with a workspace skill a user authored (see writeSkillFiles, which
// derives the skill directory from AgentSkillData.Name).
func (s *TaskService) BuiltinSkills() []AgentSkillData {
	return loadBuiltinSkills()
}

// LoadBuiltinSkillByName resolves a builtin Skill by its directory
// name (e.g. "multica-constitution-agent"). Returns (skill, true) on
// hit; (zero, false) when the Skill is not embedded or has no
// SKILL.md. Used by the 0.3.51 system-prompt binding layer in
// daemon.go::loadSystemPromptBinding to look up the body that should
// be prepended to an agent's Instructions when agent.system_key is
// set.
//
// Cheap: the underlying loadBuiltinSkill reads from the in-memory
// embed.FS, so a second call within the same process is essentially
// free. If the Skill set grows large enough that this matters we can
// add a sync.Once cache — at 13 skills (0.3.50) it doesn't.
func LoadBuiltinSkillByName(name string) (AgentSkillData, bool) {
	return loadBuiltinSkill(name)
}

func loadBuiltinSkills() []AgentSkillData {
	skills := loadMainProductSkills()
	skills = append(skills, loadExperimentSkills()...)
	return skills
}

func loadMainProductSkills() []AgentSkillData {
	entries, err := fs.ReadDir(builtinSkillsFS, builtinSkillsRoot)
	if err != nil {
		return nil
	}
	var skills []AgentSkillData
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if skill, ok := loadBuiltinSkill(entry.Name()); ok {
			skills = append(skills, skill)
		}
	}
	return skills
}

func loadBuiltinSkill(name string) (AgentSkillData, bool) {
	dir := path.Join(builtinSkillsRoot, name)
	content, err := fs.ReadFile(builtinSkillsFS, path.Join(dir, "SKILL.md"))
	if err != nil {
		// A skill directory without a SKILL.md is malformed — skip it rather
		// than ship an empty skill.
		return AgentSkillData{}, false
	}
	skill := AgentSkillData{Name: name, Content: string(content)}
	// Any other file in the directory becomes a supporting file, preserving
	// its relative path so subdirectories (e.g. rules/styling.md) survive.
	_ = fs.WalkDir(builtinSkillsFS, dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if rel == "SKILL.md" {
			return nil
		}
		data, readErr := fs.ReadFile(builtinSkillsFS, p)
		if readErr != nil {
			return nil
		}
		skill.Files = append(skill.Files, AgentSkillFileData{Path: rel, Content: string(data)})
		return nil
	})
	return skill, true
}

// experimentSkillCache memoises the on-disk scan so concurrent
// loadBuiltinSkills() calls don't re-walk the resources tree. The
// scan is intentionally read-only; the cache is invalidated only on
// process restart. We hold a sync.Once so the common case (cache
// hit after first call) costs one atomic load.
var (
	experimentSkillOnce sync.Once
	experimentSkillList []AgentSkillData
	experimentSkillErr  error
)

// loadExperimentSkills scans the bundled resource dir for Skills
// that ship with experiment manifests and returns them as if they
// were embedded skills. The 0.3.18 embed path is unchanged for the
// 9 main product skills — this function appends experiment skills
// on top.
//
// Safety contract (R11 mitigation):
//   - The function NEVER falls through to the embed path. If the
//     resources dir is missing or unreadable, it returns an empty
//     list and the main product skills still ship.
//   - The function NEVER mutates the embed FS.
//   - flag=off is enforced by the dispatcher (the install handler
//     hides the experiment's lock rows); this loader reads all
//     bundled experiment skills regardless of flag because the
//     visibility filter runs downstream.
func loadExperimentSkills() []AgentSkillData {
	experimentSkillOnce.Do(func() {
		experimentSkillList, experimentSkillErr = scanExperimentSkills()
	})
	if experimentSkillErr != nil {
		// soft fail: missing dir / unreadable / etc. Log once and
		// return empty so the main product skills still ship.
		slog.Warn("experimental skills: skipping scan", "err", experimentSkillErr)
		return nil
	}
	return experimentSkillList
}

// scanExperimentSkills walks <MULTICA_RESOURCES_DIR>/skills/<flagKey>/
// looking for SKILL.md files. Each skill's frontmatter must declare
// `multica.experiment: <flagKey>` to be considered part of the
// experiment; mismatches are logged and skipped (defensive — a
// future contributor who drops a skill in the wrong directory does
// not pollute the agent's skill list).
func scanExperimentSkills() ([]AgentSkillData, error) {
	root := os.Getenv(experimentSkillDirEnv)
	if root == "" {
		// No env var → no bundled experiments to load. This is the
		// normal dev / unit-test path; returning an empty list is
		// the right answer.
		return nil, nil
	}
	base := filepath.Join(root, experimentSkillSubdir)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", base, err)
	}
	var out []AgentSkillData
	for _, flagEntry := range entries {
		if !flagEntry.IsDir() {
			continue
		}
		flagKey := flagEntry.Name()
		flagDir := filepath.Join(base, flagKey)
		skillEntries, err := os.ReadDir(flagDir)
		if err != nil {
			slog.Warn("experimental skills: skipping flag", "flag", flagKey, "err", err)
			continue
		}
		for _, skillEntry := range skillEntries {
			if !skillEntry.IsDir() {
				continue
			}
			skill, ok := readExperimentSkill(flagDir, skillEntry.Name(), flagKey)
			if !ok {
				continue
			}
			out = append(out, skill)
		}
	}
	return out, nil
}

// readExperimentSkill loads a single skill from the experiment
// resource tree. Returns false when the directory is malformed
// (missing SKILL.md, frontmatter flag mismatch, etc.) so the caller
// can log and continue.
func readExperimentSkill(flagDir, skillName, expectedFlag string) (AgentSkillData, bool) {
	dir := filepath.Join(flagDir, skillName)
	skillPath := filepath.Join(dir, "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		slog.Warn("experimental skills: missing SKILL.md", "path", skillPath, "err", err)
		return AgentSkillData{}, false
	}
	flag := parseExperimentFrontmatter(string(raw))
	if flag == "" {
		// No frontmatter at all. Treat as malformed rather than
		// assume "this is for the current flag" — a future
		// contributor who drops a SKILL.md without a flag will
		// pollute every catalog.
		slog.Warn("experimental skills: SKILL.md missing multica.experiment frontmatter", "path", skillPath)
		return AgentSkillData{}, false
	}
	if flag != expectedFlag {
		slog.Warn("experimental skills: SKILL.md flag mismatch", "path", skillPath, "want", expectedFlag, "got", flag)
		return AgentSkillData{}, false
	}
	skill := AgentSkillData{Name: skillName, Content: string(raw)}
	// supporting files
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || e.Name() == "SKILL.md" {
				continue
			}
			data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
			if rerr != nil {
				continue
			}
			skill.Files = append(skill.Files, AgentSkillFileData{Path: e.Name(), Content: string(data)})
		}
	}
	return skill, true
}

// parseExperimentFrontmatter extracts the `multica.experiment` field
// from a minimal YAML frontmatter block. We do NOT use a real YAML
// parser because the SKILL.md frontmatter is small + the loader is
// only consulted at boot — a parser dependency for one field is
// overkill. If the frontmatter grows past a handful of fields,
// switch to gopkg.in/yaml.v3 (already a dependency of the skill
// evals in builtin_skills_test.go).
func parseExperimentFrontmatter(body string) string {
	const sep = "---"
	// fast path: no frontmatter delimiter
	if !strings.HasPrefix(body, sep) {
		return ""
	}
	// scan to closing ---
	scanner := bufio.NewScanner(strings.NewReader(body))
	// first line is the opening ---; skip
	if !scanner.Scan() {
		return ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == sep {
			// reached the closing --- without finding the field
			return ""
		}
		// match "multica.experiment: <value>"
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, "\"'")
		if key == "multica.experiment" {
			return val
		}
	}
	return ""
}
