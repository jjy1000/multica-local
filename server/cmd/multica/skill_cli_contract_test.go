package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/multica-ai/multica/server/internal/skill"
)

// Builtin skills are the agent's ONLY documentation of the `multica`
// CLI. When a skill names a subcommand or flag that no longer exists,
// the agent does not degrade — it follows the doc, hits
// `unknown command`, and gives up on the lab.
//
// That is not hypothetical. `multica experimental claude-science-runtime`
// was renamed to `claude-lab` by the 0.3.22 lab consolidation; the skill
// kept the old name in six places, so the entire sandbox-execution path
// was unreachable. `--agent` never existed (`--agent-id` does), and
// `issue comment --slug --issue --body` was never a valid form (the verb
// is a command group; the id is positional and the body is `--content`).
//
// This test closes that class of bug permanently: every `multica …`
// line inside a ```sh block of a lab skill is resolved against the live
// cobra tree, and every `--flag` on that line against that command's own
// flag set. Renaming a command or flag in this package turns the
// corresponding skill red here before it ever ships.

const builtinSkillsDir = "../../internal/service/builtin_skills"

// labSkillsUnderContract — the skills that instruct an agent to shell
// out to the CLI. Widen deliberately: a new CLI-facing skill belongs
// here on day one, not after its first stale command ships.
var labSkillsUnderContract = []string{
	"multica-claude-science",
	"multica-claude-science-runtime",
}

// shBlockRE captures fenced ```sh blocks only. Prose that merely names a
// command is not a contract; a command the agent is told to run is.
var shBlockRE = regexp.MustCompile("(?s)```sh\n(.*?)```")

// multicaCmdRE matches a runnable line: `multica <verb> [<verb> …]`.
// The verb class excludes shell metacharacters, digits and `$` so a
// line like `multica issue comment add "$ISSUE_ID" \` stops at `add`
// and the positional argument never becomes part of the path.
var multicaCmdRE = regexp.MustCompile(`^\s*multica\s+([a-z][a-z0-9-]*(?:\s+[a-z][a-z0-9-]*)*)`)

var flagRE = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// registeredCommands flattens the cobra tree into
// "issue comment add" -> that command's flags, plus whether it still
// accepts positional arguments. The last matters because skills spell
// the positional explicitly — `… claude-lab skill anndata` — and
// `anndata` is an argument to `skill`, not a subcommand of it.
func registeredCommands() map[string]registeredCommand {
	out := map[string]registeredCommand{}
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			path := sub.Name()
			if prefix != "" {
				path = prefix + " " + sub.Name()
			}
			flags := map[string]bool{}
			add := func(f *pflag.Flag) { flags["--"+f.Name] = true }
			sub.LocalFlags().VisitAll(add)
			sub.InheritedFlags().VisitAll(add)
			sub.PersistentFlags().VisitAll(add)
			out[path] = registeredCommand{Flags: flags, TakesArgs: sub.Args != nil}
			walk(sub, path)
		}
	}
	walk(rootCmd, "")
	return out
}

type registeredCommand struct {
	Flags     map[string]bool
	TakesArgs bool
}

// skillShCommands returns every `multica …` invocation in the skill's
// fenced shell blocks, with the command path and the flags used on that
// logical line. Backslash continuations are folded first so a flag on a
// wrapped line is still attributed to its command.
func skillShCommands(t *testing.T, name string) []struct {
	Path  string
	Flags []string
	Line  string
} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(builtinSkillsDir, name, "SKILL.md"))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var found []struct {
		Path  string
		Flags []string
		Line  string
	}
	for _, block := range shBlockRE.FindAllStringSubmatch(string(raw), -1) {
		body := strings.ReplaceAll(block[1], "\\\n", " ")
		for _, line := range strings.Split(body, "\n") {
			m := multicaCmdRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			var flags []string
			for _, f := range flagRE.FindAllString(line, -1) {
				flags = append(flags, f)
			}
			found = append(found, struct {
				Path  string
				Flags []string
				Line  string
			}{Path: strings.TrimSpace(m[1]), Flags: flags, Line: strings.TrimSpace(line)})
		}
	}
	return found
}

func TestBuiltinSkillCommandsExistInCLI(t *testing.T) {
	registered := registeredCommands()
	for _, skillName := range labSkillsUnderContract {
		t.Run(skillName, func(t *testing.T) {
			cmds := skillShCommands(t, skillName)
			if len(cmds) == 0 {
				t.Fatalf("no `multica …` commands found in fenced sh blocks — " +
					"the extractor or the skill drifted; a skill that teaches no " +
					"command has nothing to reconcile")
			}
			for _, c := range cmds {
				// Resolve longest-first so `experimental claude-lab skill
				// anndata` binds to the `skill` command (which takes a
				// positional name) rather than demanding an `anndata`
				// subcommand. Fall back one word at a time.
				parts := strings.Fields(c.Path)
				resolved := ""
				var rc registeredCommand
				for n := len(parts); n >= 1; n-- {
					cand := strings.Join(parts[:n], " ")
					if got, ok := registered[cand]; ok {
						resolved, rc = cand, got
						break
					}
				}
				if resolved == "" {
					t.Errorf("skill %s line %q names command %q, which is not registered — "+
						"`multica %s --help` would print unknown command. Fix the skill, or "+
						"rename the command and update every call site.",
						skillName, c.Line, c.Path, c.Path)
					continue
				}
				// The tail we did not match must be a positional the
				// resolved command actually accepts.
				tail := len(parts) - len(strings.Fields(resolved))
				if tail > 0 && !rc.TakesArgs {
					t.Errorf("skill %s line %q passes %d extra argument(s) after `multica %s`, "+
						"which takes none. Known flags here: %s",
						skillName, c.Line, tail, resolved, sortedKeys(rc.Flags))
					continue
				}
				for _, f := range c.Flags {
					if !rc.Flags[f] {
						t.Errorf("skill %s line %q uses flag %s, but `multica %s` has no such flag. "+
							"Known flags here: %s", skillName, c.Line, f, resolved, sortedKeys(rc.Flags))
					}
				}
			}
		})
	}
}

func sortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return strings.Join(keys, " ")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestLabSkillDescriptionsAreUsableByAgents guards the 0.5.126
// contract that killed the pythia lab: a description which tells the
// agent NOT to use the skill for the very thing the body does is worse
// than no description, because the agent then improvises — in 0.5.125
// the squad leader role-played a forecast instead of calling Pythia.
//
// Two failure modes are pinned:
//   - a self-excluding clause naming "issue" (the body creates and
//     comments on issues, so excluding them is self-contradictory)
//   - an all-English description, which scores zero against the Chinese
//     requests this product's users actually send
func TestLabSkillDescriptionsAreUsableByAgents(t *testing.T) {
	const (
		// CJK ideographs / kana / hangul, as a character class so it
		// means "any of" — writing the four classes bare would
		// concatenate them and demand all four in order, which never
		// matches real text.
		cjk = `[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]`
	)
	cjkRE := regexp.MustCompile(cjk)
	// A clause that both disclaims and names issue-shaped work.
	selfExcludingRE := regexp.MustCompile(
		`(?i)do not use[^.]*\bissues?\b|do NOT use[^.]*\bissues?\b`)

	for _, skillName := range labSkillsUnderContract {
		t.Run(skillName, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(builtinSkillsDir, skillName, "SKILL.md"))
			if err != nil {
				t.Fatalf("read %s: %v", skillName, err)
			}
			// Parse through the real frontmatter reader the daemon
			// mirrors, not a hand-rolled regex — the assertion must
			// fail if the YAML shape ever stops parsing the way an
			// agent would see it.
			name, description := skill.ParseSkillFrontmatter(string(raw))
			if description == "" {
				t.Fatalf("skill %s has no description — the agent cannot "+
					"decide to load it", skillName)
			}
			if name != skillName {
				t.Errorf("frontmatter name %q != directory %q", name, skillName)
			}
			if selfExcludingRE.MatchString(description) {
				t.Errorf("skill %s description tells the agent not to use it for "+
					"issue work, but its body creates and comments on issues. "+
					"An agent that reads this will skip the skill and improvise "+
					"(the 0.5.125 pythia failure). Description: %q",
					skillName, description)
			}
			if !cjkRE.MatchString(description) {
				t.Errorf("skill %s description contains no CJK trigger words, so a "+
					"Chinese-language request cannot match it. The model only sees "+
					"the description when choosing which skill to load. Description: %q",
					skillName, description)
			}
		})
	}
}
