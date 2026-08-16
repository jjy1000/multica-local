package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// ---------------------------------------------------------------------------
// swarm commands — CLI bridge to the swarm-topology Labs surface
// (/api/experimental/swarm-topology/*).
//
// Mirrors cmd_lab.go: a thin wrapper that posts against the same HTTP
// endpoints the desktop UI uses, so an agent (or the user) can drive a
// swarm run from the terminal without opening the GUI.
//
//   multica swarm bootstrap  --issue-id <uuid> --problem <text> [--max-runtime-hours N]
//   multica swarm list       --workspace-id <uuid>
//   multica swarm status     --run-id <uuid>
//   multica swarm cancel     --run-id <uuid>
//   multica swarm interrupt  --run-id <uuid> --kind <pause|resume|redirect|inject_message> [--message <text>]
//
// All verbs are membership-gated + flag-gated upstream, so when the
// swarm_topology Labs flag is off the server returns 404 and the CLI
// surfaces a clear "flag off / endpoint not registered" error.
// ---------------------------------------------------------------------------

var swarmCmd = &cobra.Command{
	Use:   "swarm",
	Short: "Drive the swarm-topology Labs surface from the terminal",
}

var (
	swarmBootstrapIssueID  string
	swarmBootstrapProblem  string
	swarmBootstrapMaxHours int
	swarmListWorkspaceID   string
	swarmStatusRunID       string
	swarmCancelRunID       string
	swarmInterruptRunID    string
	swarmInterruptKind     string
	swarmInterruptMessage  string
	swarmOutputJSON        bool
)

func init() {
	swarmCmd.AddCommand(swarmBootstrapCmd, swarmListCmd, swarmStatusCmd, swarmCancelCmd, swarmInterruptCmd)

	swarmBootstrapCmd.Flags().StringVar(&swarmBootstrapIssueID, "issue-id", "", "Root issue UUID for the swarm run (required)")
	swarmBootstrapCmd.Flags().StringVar(&swarmBootstrapProblem, "problem", "", "Problem statement the swarm will work on (required)")
	swarmBootstrapCmd.Flags().IntVar(&swarmBootstrapMaxHours, "max-runtime-hours", 72, "Hard cap on run lifetime in hours (1..168; orchestrator flips status='failed' on overrun)")
	swarmBootstrapCmd.Flags().BoolVar(&swarmOutputJSON, "output-json", false, "Emit JSON envelope instead of pretty text")

	swarmListCmd.Flags().StringVar(&swarmListWorkspaceID, "workspace-id", "", "Workspace UUID (required)")
	swarmListCmd.Flags().BoolVar(&swarmOutputJSON, "output-json", false, "Emit JSON envelope instead of pretty text")

	swarmStatusCmd.Flags().StringVar(&swarmStatusRunID, "run-id", "", "Swarm run UUID (required)")
	swarmStatusCmd.Flags().BoolVar(&swarmOutputJSON, "output-json", false, "Emit JSON envelope instead of pretty text")

	swarmCancelCmd.Flags().StringVar(&swarmCancelRunID, "run-id", "", "Swarm run UUID to cancel (required)")

	swarmInterruptCmd.Flags().StringVar(&swarmInterruptRunID, "run-id", "", "Swarm run UUID (required)")
	swarmInterruptCmd.Flags().StringVar(&swarmInterruptKind, "kind", "", "Interrupt kind: pause|resume|redirect|inject_message (required)")
	swarmInterruptCmd.Flags().StringVar(&swarmInterruptMessage, "message", "", "Optional free-form message (used by inject_message)")

	swarmCmd.GroupID = groupExperimental
}

var swarmBootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap a new swarm run for an issue",
	Long: `Bootstrap a self-organising multi-agent swarm on the given root issue.

The orchestrator authors role-agents + skills + a coordinating squad on
bootstrap, then walks a 5-phase machine (research → design → implement
→ review → done). The leader agent (multica-creating-swarms skill) fills
in topology_spec during the planning phase.

The root_issue_id has a UNIQUE index on the server, so re-calling with
the same issue is idempotent — the existing run is returned.`,
	RunE: runSwarmBootstrap,
}

var swarmListCmd = &cobra.Command{
	Use:   "list",
	Short: "List swarm runs for a workspace",
	RunE:  runSwarmList,
}

var swarmStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print live state (status + phase + role table) for a swarm run",
	RunE:  runSwarmStatus,
}

var swarmCancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Cancel a swarm run synchronously (flips status to aborted immediately)",
	RunE:  runSwarmCancel,
}

var swarmInterruptCmd = &cobra.Command{
	Use:   "interrupt",
	Short: "Send a non-cancel interrupt (pause / resume / redirect / inject_message) to a swarm run",
	RunE:  runSwarmInterrupt,
}

func runSwarmBootstrap(cmd *cobra.Command, _ []string) error {
	if strings.TrimSpace(swarmBootstrapIssueID) == "" {
		return fmt.Errorf("--issue-id is required")
	}
	if strings.TrimSpace(swarmBootstrapProblem) == "" {
		return fmt.Errorf("--problem is required")
	}
	if swarmBootstrapMaxHours < 1 || swarmBootstrapMaxHours > 168 {
		return fmt.Errorf("--max-runtime-hours must be between 1 and 168")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	body := map[string]any{
		"root_issue_id":     swarmBootstrapIssueID,
		"problem":           swarmBootstrapProblem,
		"max_runtime_hours": swarmBootstrapMaxHours,
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	var out map[string]any
	if err := client.PostJSON(reqCtx, "/api/experimental/swarm-topology/runs", body, &out); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	if swarmOutputJSON {
		return writeJSONOutput(out)
	}
	fmt.Printf("run_id=%s status=%s phase=%s\n",
		strVal(out, "id"), strVal(out, "status"), strVal(out, "current_phase"))
	return nil
}

func runSwarmList(cmd *cobra.Command, _ []string) error {
	if strings.TrimSpace(swarmListWorkspaceID) == "" {
		return fmt.Errorf("--workspace-id is required")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	var raw json.RawMessage
	path := "/api/experimental/swarm-topology/runs?workspace_id=" + swarmListWorkspaceID
	if err := client.GetJSON(reqCtx, path, &raw); err != nil {
		return fmt.Errorf("list: %w", err)
	}

	if swarmOutputJSON {
		fmt.Println(string(raw))
		return nil
	}

	// 0.5.22 audit fix (P0): the server's GetSwarmRunsByWorkspace returns a
	// TOP-LEVEL JSON array (swarm_run.go:432 writeJSON(w, 200, []SwarmRunResponse)),
	// NOT an envelope {"runs":[...]}. The previous code decoded into a
	// struct{Runs []map[string]any} which always unmarshalled empty
	// (encoding/json returns UnmarshalTypeError "cannot unmarshal array
	// into Go struct"), so `multica swarm list` printed "no swarm runs"
	// even when the workspace had runs.
	var parsed []map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("list: decode: %w", err)
	}
	if len(parsed) == 0 {
		fmt.Println("no swarm runs in this workspace")
		return nil
	}
	fmt.Printf("%-36s %-12s %-14s %s\n", "RUN_ID", "STATUS", "PHASE", "STARTED_AT")
	for _, r := range parsed {
		fmt.Printf("%-36s %-12s %-14s %s\n",
			strVal(r, "id"), strVal(r, "status"), strVal(r, "current_phase"), strVal(r, "started_at"))
	}
	return nil
}

func runSwarmStatus(cmd *cobra.Command, _ []string) error {
	if strings.TrimSpace(swarmStatusRunID) == "" {
		return fmt.Errorf("--run-id is required")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	path := "/api/experimental/swarm-topology/runs/" + swarmStatusRunID + "/state"
	var out map[string]any
	if err := client.GetJSON(reqCtx, path, &out); err != nil {
		return fmt.Errorf("status: %w", err)
	}

	if swarmOutputJSON {
		return writeJSONOutput(out)
	}
	fmt.Printf("run_id=%s status=%s phase=%s active=%v completed=%v\n",
		strVal(out, "run_id"), strVal(out, "status"), strVal(out, "current_phase"),
		strVal(out, "active_role_count"), strVal(out, "completed_role_count"))

	roles, _ := out["roles"].([]any)
	if len(roles) > 0 {
		fmt.Printf("%-30s %-12s %s\n", "ROLE", "STATUS", "CURRENT_STEP")
		for _, r := range roles {
			m, _ := r.(map[string]any)
			fmt.Printf("%-30s %-12s %s\n",
				strVal(m, "role_name"), strVal(m, "status"), strVal(m, "current_step"))
		}
	}
	return nil
}

func runSwarmCancel(cmd *cobra.Command, _ []string) error {
	if strings.TrimSpace(swarmCancelRunID) == "" {
		return fmt.Errorf("--run-id is required")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	path := "/api/experimental/swarm-topology/runs/" + swarmCancelRunID + "/interrupt"
	body := map[string]any{"kind": "cancel"}
	if err := client.PostJSON(reqCtx, path, body, nil); err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	fmt.Printf("run_id=%s cancel requested (status will flip to aborted)\n", swarmCancelRunID)
	return nil
}

func runSwarmInterrupt(cmd *cobra.Command, _ []string) error {
	if strings.TrimSpace(swarmInterruptRunID) == "" {
		return fmt.Errorf("--run-id is required")
	}
	switch swarmInterruptKind {
	case "pause", "resume", "redirect", "inject_message":
	default:
		return fmt.Errorf("--kind must be pause|resume|redirect|inject_message")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancel()

	body := map[string]any{"kind": swarmInterruptKind}
	if swarmInterruptKind == "inject_message" && strings.TrimSpace(swarmInterruptMessage) != "" {
		body["payload"] = swarmInterruptMessage
	}

	path := "/api/experimental/swarm-topology/runs/" + swarmInterruptRunID + "/interrupt"
	if err := client.PostJSON(reqCtx, path, body, nil); err != nil {
		return fmt.Errorf("interrupt: %w", err)
	}
	fmt.Printf("run_id=%s kind=%s queued (picked up on next orchestrator tick)\n", swarmInterruptRunID, swarmInterruptKind)
	return nil
}

// silence unused-import lint for the rare `os` import if every helper above
// stops touching it in a future edit.
var _ = os.Stdout
