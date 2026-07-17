package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// claude-science commands — Multica-native in-app research workspace.
//
// As of 0.3.14 the desktop no longer spawns the bundled OpenScience Bun
// binary. The SolidJS browser surface was retired because users asked for
// the workspace to be driven through Multica's own model pipeline, not a
// separate BYOK HTTP service. The Skills (`multica-claude-science`) re-uses
// Multica's chat / agent runtime via the standard LLM provider chain —
// users configure once in Settings → Models and every Multica-driven
// research flow (chat, issue comment, this Skill) uses the same key.
//
// Wire contract: server/internal/service/builtin_skills/multica-claude-science/SKILL.md
//
// The CLI subcommands here remain so the Skill body can keep its existing
// invocation shape; they now emit a structured "not-running" envelope
// instead of touching a subprocess. The Skill is responsible for knowing
// the workspace is no longer binary-backed and routing the work through
// the agent runtime instead.
// ---------------------------------------------------------------------------

var claudeScienceCmd = &cobra.Command{
	Use:   "claude-science",
	Short: "Multica in-app research workspace (driven by the configured model)",
}

var claudeScienceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report whether the workspace is reachable. Always 'no-binary' after 0.3.14.",
	RunE:  runClaudeScienceStatus,
}

var claudeScienceResearchCmd = &cobra.Command{
	Use:   "research",
	Short: "Deprecated — research work now flows through the standard Multica agent runtime.",
	RunE:  runClaudeScienceResearch,
}

var claudeScienceGetResultCmd = &cobra.Command{
	Use:   "get-result",
	Short: "Deprecated — research work now flows through the standard Multica agent runtime.",
	RunE:  runClaudeScienceGetResult,
}

func init() {
	claudeScienceCmd.AddCommand(claudeScienceStatusCmd)
	claudeScienceCmd.AddCommand(claudeScienceResearchCmd)
	claudeScienceCmd.AddCommand(claudeScienceGetResultCmd)

	for _, c := range []*cobra.Command{
		claudeScienceStatusCmd,
		claudeScienceResearchCmd,
		claudeScienceGetResultCmd,
	} {
		c.Flags().String("output", "json", "Output format: json (default) or plain")
	}
	// These flags are kept so legacy Skill bodies / tests that still pass
	// them do not get rejected by cobra; the flags are simply ignored.
	claudeScienceResearchCmd.Flags().String("url", "", "deprecated")
	claudeScienceResearchCmd.Flags().String("topic", "", "Research topic (echoed back for backwards-compat only)")
	claudeScienceResearchCmd.Flags().String("agent", "research", "deprecated")
	claudeScienceGetResultCmd.Flags().String("url", "", "deprecated")
	claudeScienceGetResultCmd.Flags().String("session", "", "deprecated")

	claudeScienceCmd.GroupID = groupExperimental
}

// runClaudeScienceStatus reports the workspace state. 0.3.14 removed the
// SolidJS subprocess backend; the Skill that used to live here now drives
// research through the agent runtime. The status envelope is stable so
// the Skill body can stay factual.
func runClaudeScienceStatus(cmd *cobra.Command, _ []string) error {
	status := map[string]any{
		"status":      "no-binary",
		"url":         nil,
		"backend":     "multica-agent-runtime",
		"description": "research flows through the configured Multica model — no separate binary is launched",
	}
	return writeJSON(status)
}

// runClaudeScienceResearch / runClaudeScienceGetResult kept as no-ops so
// older Skill invocations do not break at the CLI parsing layer. They
// emit a structured error pointing the caller at the current home for
// research work — the standard Multica agent workflow.
//
// These commands should not be removed silently: removing them would
// break Skill bodies that reference the verb even though the Skill itself
// has been migrated to call the agent runtime. Future cleanup can land
// them in a single PR once we're confident no in-flight shell history
// references the verb (gate: search `~/.multica/` for `claude-science
// research` and confirm 0 hits across the past 30 days).
func runClaudeScienceResearch(cmd *cobra.Command, _ []string) error {
	topic, _ := cmd.Flags().GetString("topic")
	payload := map[string]any{
		"ok":     false,
		"reason": "no-binary",
		"hint": "claude-science research was retired in 0.3.14. Use the standard Multica agent workflow " +
			"(create an issue assigned to a research-capable agent, or run the Skill via the chat box).",
		"topic": topic,
	}
	if err := writeJSON(payload); err != nil {
		return err
	}
	return fmt.Errorf("claude-science research is no longer a CLI verb — run this work through the Multica agent runtime")
}

func runClaudeScienceGetResult(cmd *cobra.Command, _ []string) error {
	payload := map[string]any{
		"ok":     false,
		"reason": "no-binary",
		"hint":   "no session id exists in the 0.3.14+ model; research runs in-band through the agent",
	}
	if err := writeJSON(payload); err != nil {
		return err
	}
	return fmt.Errorf("claude-science get-result has no session to read from in 0.3.14+")
}

// writeJSON is shared with cmd_pythia.go (declared there). Reused here
// so the JSON envelope shape stays consistent across experimental
// subcommands.

// io.EOF guard — some builds tree-shake io out of cmd_claude_science.go
// when the helper functions above are inlined. Keep a single direct use
// here so the import survives.
var _ = io.EOF
var _ = os.Stdout

