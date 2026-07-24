package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/experimental"
)

// ---------------------------------------------------------------------------
// lab commands — delegate a self-contained sub-task to a lab plugin's agent
// and block until it returns a result.
//
// This is the "synchronous blocking delegation" primitive (Type 2 labs): a
// team agent that decides mid-task it needs a specialist run (e.g. a virtual
// event simulation) calls `multica lab delegate <lab> "<task>"`, waits for the
// lab's leader agent to execute the run, and reads the delivered result back
// on stdout — then continues its own task with that result in hand.
//
// It is implemented entirely on top of existing primitives, so it needs no
// new server endpoint:
//   1. POST /api/issues with lab_source=user_<slug> — the create path resolves
//      the plugin's leader agent (capabilities.leader) via assignDefaultLabAgent
//      and enqueues a task through maybeEnqueueOnAssign.
//   2. Poll GET /api/issues/{id}/task-runs until the enqueued task reaches a
//      terminal state.
//   3. On success, read result.output (the agent's final reply) and print it.
//
// The delegated run is a normal issue-bound task, so it shows up in the UI and
// carries full transcript/usage — delegation is observable, not a hidden RPC.
// ---------------------------------------------------------------------------

var labCmd = &cobra.Command{
	Use:   "lab",
	Short: "Delegate a sub-task to a lab plugin's agent and wait for the result",
}

var labDelegateCmd = &cobra.Command{
	Use:   "delegate <lab> <task>",
	Short: "Delegate a self-contained sub-task to a lab agent and block until it returns a result",
	Long: `Delegate a self-contained sub-task to a lab plugin's leader agent and block
until it returns a result.

<lab> is the plugin slug (e.g. "event-sim") or its full flag key
("user_event-sim"). <task> is the natural-language instruction for the lab
agent — describe the sub-task and what result you expect back.

The command creates a lab-bound issue, lets the lab's leader agent run it, and
prints the agent's final reply once the run completes. Use this from inside a
team/agent task when you need a specialist lab (simulation, analysis, etc.) to
produce a deliverable you can continue working from.

Prerequisites: the target lab plugin must be enabled and declare a
capabilities.leader agent that is bound to a running daemon runtime; otherwise
no run is dispatched and the command times out.`,
	Args: exactArgs(2),
	RunE: runLabDelegate,
}

func init() {
	labCmd.AddCommand(labDelegateCmd)

	labDelegateCmd.Flags().String("title", "", "Issue title for the delegated run (default: derived from the task text)")
	labDelegateCmd.Flags().String("status", "todo", "Initial issue status; must be a non-backlog status so the run dispatches")
	labDelegateCmd.Flags().Duration("timeout", 15*time.Minute, "Maximum time to wait for the delegated run to finish")
	labDelegateCmd.Flags().Duration("poll-interval", 3*time.Second, "How often to poll the delegated run's status")
	labDelegateCmd.Flags().String("output", "json", "Output format: json (default) or plain (the agent's reply text only)")

	labCmd.GroupID = groupExperimental
}

// runLabDelegate creates a lab-bound issue, waits for the lab agent's task to
// finish, and returns the delivered result. It blocks up to --timeout.
func runLabDelegate(cmd *cobra.Command, args []string) error {
	labArg := strings.TrimSpace(args[0])
	task := args[1]
	if labArg == "" {
		return fmt.Errorf("lab is required (plugin slug or user_<slug> flag key)")
	}
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("task is required (the instruction for the lab agent)")
	}

	flagKey := labArg
	if !experimental.IsUserPluginKey(flagKey) {
		flagKey = experimental.UserPluginPrefix + flagKey
	}

	statusFlag, _ := cmd.Flags().GetString("status")
	if statusFlag == "" {
		statusFlag = "todo"
	}
	if err := validateIssueStatus(statusFlag); err != nil {
		return err
	}
	if statusFlag == "backlog" {
		return fmt.Errorf("--status backlog will not dispatch a run; use todo or in_progress")
	}

	timeout, _ := cmd.Flags().GetDuration("timeout")
	pollInterval, _ := cmd.Flags().GetDuration("poll-interval")
	if pollInterval <= 0 {
		pollInterval = 3 * time.Second
	}
	output, _ := cmd.Flags().GetString("output")

	title, _ := cmd.Flags().GetString("title")
	if strings.TrimSpace(title) == "" {
		title = deriveDelegateTitle(task)
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	// Create the lab-bound issue. The server resolves the plugin leader and
	// enqueues the run; lab_source is validated against the flag catalog, so
	// an unknown/disabled plugin fails here with a clear message.
	createCtx, cancelCreate := context.WithTimeout(context.Background(), cli.APITimeout())
	defer cancelCreate()

	body := map[string]any{
		"title":       title,
		"description": task,
		"status":      statusFlag,
		"lab_source":  flagKey,
	}
	var created map[string]any
	if err := client.PostJSON(createCtx, "/api/issues", body, &created); err != nil {
		return fmt.Errorf("delegate: create lab issue (lab_source=%s): %w", flagKey, err)
	}
	issueID := strVal(created, "id")
	identifier := strVal(created, "identifier")
	if issueID == "" {
		return fmt.Errorf("delegate: server did not return an issue id")
	}

	// Poll for the enqueued run to reach a terminal state.
	result, err := waitForDelegatedResult(client, issueID, flagKey, timeout, pollInterval)
	if err != nil {
		return err
	}

	if output == "plain" {
		fmt.Fprintln(os.Stdout, result.Output)
		return nil
	}
	return cli.PrintJSON(os.Stdout, map[string]any{
		"ok":         result.Status == "completed",
		"lab_source": flagKey,
		"issue_id":   issueID,
		"identifier": identifier,
		"task_id":    result.TaskID,
		"status":     result.Status,
		"output":     result.Output,
		"error":      result.Error,
	})
}

// delegatedResult is the flattened outcome of a delegated run.
type delegatedResult struct {
	TaskID string
	Status string
	Output string
	Error  string
}

// waitForDelegatedResult polls an issue's task-runs until one reaches a
// terminal state (completed / failed / cancelled) or the timeout elapses. It
// returns an error on failure/cancel/timeout so the calling agent sees a
// non-zero exit and can react, rather than silently continuing on a bad run.
func waitForDelegatedResult(client *cli.APIClient, issueID, flagKey string, timeout, pollInterval time.Duration) (delegatedResult, error) {
	deadline := time.Now().Add(timeout)
	sawTask := false
	// Grace window: if no run appears shortly after create, the lab has no
	// dispatchable leader (missing capabilities.leader, agent not installed,
	// or no bound runtime). Surface that as a distinct, actionable error
	// instead of making the caller wait out the full timeout.
	noTaskDeadline := time.Now().Add(30 * time.Second)

	for {
		reqCtx, cancel := context.WithTimeout(context.Background(), cli.APITimeout())
		var tasks []map[string]any
		err := client.GetJSON(reqCtx, "/api/issues/"+issueID+"/task-runs", &tasks)
		cancel()
		if err != nil {
			return delegatedResult{}, fmt.Errorf("delegate: poll task status: %w", err)
		}

		if len(tasks) > 0 {
			sawTask = true
			latest := latestTask(tasks)
			status := strVal(latest, "status")
			switch status {
			case "completed":
				return delegatedResult{
					TaskID: strVal(latest, "id"),
					Status: status,
					Output: extractTaskOutput(latest),
				}, nil
			case "failed", "cancelled":
				res := delegatedResult{
					TaskID: strVal(latest, "id"),
					Status: status,
					Output: extractTaskOutput(latest),
					Error:  strVal(latest, "error"),
				}
				msg := res.Error
				if msg == "" {
					msg = "no error detail reported"
				}
				return res, fmt.Errorf("delegate: lab run %s (%s): %s", status, res.TaskID, msg)
			}
		}

		if !sawTask && time.Now().After(noTaskDeadline) {
			return delegatedResult{}, fmt.Errorf(
				"delegate: no run was dispatched for lab %s — the plugin must be enabled and declare a "+
					"capabilities.leader agent bound to a running runtime", flagKey)
		}
		if time.Now().After(deadline) {
			return delegatedResult{}, fmt.Errorf(
				"delegate: timed out after %s waiting for lab %s to finish (issue still running)", timeout, flagKey)
		}
		time.Sleep(pollInterval)
	}
}

// latestTask returns the task-run with the most recent created_at, falling
// back to the last element when timestamps are missing/equal. A delegated
// issue normally has a single run, but retries can add more.
func latestTask(tasks []map[string]any) map[string]any {
	latest := tasks[0]
	latestTS := strVal(latest, "created_at")
	for _, t := range tasks[1:] {
		ts := strVal(t, "created_at")
		if ts >= latestTS {
			latest = t
			latestTS = ts
		}
	}
	return latest
}

// extractTaskOutput pulls the agent's final reply from a task-run's result
// envelope. The result column stores {"output": "...", ...}; older/edge rows
// may omit it, in which case the empty string is returned.
func extractTaskOutput(task map[string]any) string {
	res, ok := task["result"].(map[string]any)
	if !ok {
		return ""
	}
	if out, ok := res["output"].(string); ok {
		return out
	}
	return ""
}

// deriveDelegateTitle builds a concise issue title from the task text when the
// caller didn't pass --title. It takes the first non-empty line and truncates
// to a readable length so the delegated run is legible in the issue list.
func deriveDelegateTitle(task string) string {
	line := task
	if idx := strings.IndexByte(task, '\n'); idx >= 0 {
		line = task[:idx]
	}
	line = strings.TrimSpace(line)
	const maxLen = 80
	// Truncate by rune, not byte, so multi-byte UTF-8 (e.g. CJK) titles are
	// not split mid-character into invalid sequences that render as U+FFFD.
	if runes := []rune(line); len(runes) > maxLen {
		line = strings.TrimSpace(string(runes[:maxLen])) + "…"
	}
	if line == "" {
		line = "Delegated lab run"
	}
	return line
}
