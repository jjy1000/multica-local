package main

import (
	"context"
	"encoding/json"
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

Pass --parent (an issue key like MUL-12, a full UUID, or a UUID prefix) to
record the delegation as a sub-issue of the calling issue. The child carries
parent_issue_id + lab_source, so the platform's child-done channel wakes the
parent's agent the moment the lab run reaches a terminal state, and a result
summary comment is posted back on the parent (best-effort — a comment failure
warns on stderr but never fails the delegation).

Prerequisites: the target lab plugin must be enabled and declare a
capabilities.leader agent that is bound to a running daemon runtime; otherwise
no run is dispatched and the command times out.`,
	Args: exactArgs(2),
	RunE: runLabDelegate,
}

func init() {
	labCmd.AddCommand(labDelegateCmd)
	labCmd.AddCommand(labListCmd)
	labCmd.AddCommand(labInspectCmd)
	labCmd.AddCommand(labCreateCmd)
	labCmd.AddCommand(labEnableCmd)
	labCmd.AddCommand(labDisableCmd)
	labCmd.AddCommand(labDeleteCmd)

	labDelegateCmd.Flags().String("title", "", "Issue title for the delegated run (default: derived from the task text)")
	labDelegateCmd.Flags().String("status", "todo", "Initial issue status; must be a non-backlog status so the run dispatches")
	labDelegateCmd.Flags().Duration("timeout", 15*time.Minute, "Maximum time to wait for the delegated run to finish")
	labDelegateCmd.Flags().Duration("poll-interval", 3*time.Second, "How often to poll the delegated run's status")
	labDelegateCmd.Flags().String("output", "json", "Output format: json (default) or plain (the agent's reply text only)")
	labDelegateCmd.Flags().String("parent", "", "Parent issue the delegation is recorded under (issue key, full UUID, or UUID prefix). A result summary comment is posted back on the parent after a successful run")
	labDelegateCmd.Flags().Int("stage", 0, "Stage ordinal (>=1) grouping this delegated sub-issue into an ordered barrier group under its parent; omit for unstaged")

	labCreateCmd.Flags().String("slug", "", "Plugin slug (2-64 chars, lowercase alphanumeric + hyphens); drives the user_<slug> flag key")
	labCreateCmd.Flags().String("title-zh", "", "Chinese title")
	labCreateCmd.Flags().String("title-en", "", "English title")
	labCreateCmd.Flags().String("desc-zh", "", "Chinese description")
	labCreateCmd.Flags().String("desc-en", "", "English description")
	labCreateCmd.Flags().String("trigger-mode", "issue_select", "auto (self-driven) or issue_select (task-bound)")
	labCreateCmd.Flags().String("runtime-kind", "none", "none, inline (python3 -I), or subprocess")
	labCreateCmd.Flags().String("interaction-model", "", "assignee (independent worker with a leader) or auxiliary (assistant); default auxiliary")
	labCreateCmd.Flags().String("leader", "", "Leader agent name (required when --interaction-model assignee); must already exist — provision hidden lab agents via manifest capabilities.agents_inline")
	labCreateCmd.Flags().StringArray("skill", nil, "Workspace skill name to declare in capabilities.skills (repeatable)")
	labCreateCmd.Flags().String("skills-visibility", "", "global (default) or lab_scoped — lab_scoped injects the skills only into this lab's own runs")
	labCreateCmd.Flags().String("manifest-file", "", "Path to a JSON file merged under 'manifest' (authoritative for keys it sets)")
	labCreateCmd.Flags().String("output", "json", "Output format: json (default) or plain")

	labEnableCmd.Flags().String("output", "json", "Output format: json (default) or plain")
	labDisableCmd.Flags().String("output", "json", "Output format: json (default) or plain")

	labDeleteCmd.Flags().Bool("dry-run", false, "Print the reclaim plan and exit without deleting")
	labDeleteCmd.Flags().Bool("confirm", false, "Actually delete; without this flag the command prints the plan and exits 1 (two-step conversational protocol)")
	labDeleteCmd.Flags().String("output", "json", "Output format: json (default) or plain")

	labCmd.GroupID = groupExperimental
}

// ---------------------------------------------------------------------------
// lab list / inspect
// ---------------------------------------------------------------------------

var labListCmd = &cobra.Command{
	Use:   "list",
	Short: "List built-in labs and user plugins with their interaction model and state",
	Long: `List every lab the server knows: built-in catalog flags plus user plugins.

Each row shows the flag key, interaction model (assignee labs lock the
assignee slot; auxiliary labs assist), enabled state, and markers (frozen,
user plugin). This is the discovery surface the delegation/management
briefings point at.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()
		// The endpoint wraps the list in {"flags": [...]} (ExperimentalFlagsList);
		// accept a bare array too so a wire-shape drift cannot break discovery.
		var envelope struct {
			Flags []map[string]any `json:"flags"`
		}
		var bare []map[string]any
		if err := client.GetJSON(ctx, "/api/experimental-flags", &envelope); err != nil {
			return fmt.Errorf("list labs: %w", err)
		}
		flags := envelope.Flags
		if flags == nil {
			if err := client.GetJSON(ctx, "/api/experimental-flags", &bare); err == nil {
				flags = bare
			}
		}
		if output, _ := cmd.Flags().GetString("output"); output == "plain" {
			for _, f := range flags {
				key := strVal(f, "key")
				model := strVal(f, "interaction_model")
				if model == "" {
					model = "-"
				}
				state := "off"
				if b, ok := f["enabled"].(bool); ok && b {
					state = "on"
				}
				markers := ""
				if b, ok := f["frozen"].(bool); ok && b {
					markers += " frozen"
				}
				if b, ok := f["is_user_plugin"].(bool); ok && b {
					markers += " user"
				}
				fmt.Fprintf(os.Stdout, "%-28s %-9s %-4s%s\n", key, model, state, markers)
			}
			return nil
		}
		return cli.PrintJSON(os.Stdout, flags)
	},
}

var labInspectCmd = &cobra.Command{
	Use:   "inspect <lab>",
	Short: "Show one lab's metadata plus its teardown-ledger resource plan",
	Long: `Show one lab's catalog metadata (interaction model, leader, runtime kind)
and, for user plugins, the reclaim plan: every resource the plugin
provisions or declares, what delete would do to it, and how many issues
are still bound. <lab> is a slug or full flag key.`,
	Args: exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		flagKey := resolveLabFlagKey(args[0])
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()

		var plugins []map[string]any
		if err := client.GetJSON(ctx, "/api/user-plugins", &plugins); err != nil {
			return fmt.Errorf("inspect: list user plugins: %w", err)
		}
		var plugin map[string]any
		for _, p := range plugins {
			if strVal(p, "flag_key") == flagKey || strVal(p, "slug") == flagKey {
				plugin = p
				break
			}
		}
		out := map[string]any{"flag_key": flagKey}
		if plugin != nil {
			out["plugin"] = plugin
			slug := strVal(plugin, "slug")
			var plan map[string]any
			if err := client.GetJSON(ctx, "/api/user-plugins/"+slug+"/reclaim-plan", &plan); err != nil {
				fmt.Fprintf(os.Stderr, "lab inspect: reclaim plan unavailable: %v\n", err)
			} else {
				out["reclaim_plan"] = plan
			}
		} else if f, ok := experimental.FlagByKey(flagKey); ok {
			out["builtin"] = map[string]any{
				"key":               f.Key,
				"interaction_model": f.InteractionModel,
				"runtime_kind":      f.RuntimeKind,
				"frozen":            f.Frozen,
				"successor_key":     f.SuccessorKey,
			}
		} else {
			return fmt.Errorf("lab %s not found (not a user plugin, not a built-in)", flagKey)
		}
		return cli.PrintJSON(os.Stdout, out)
	},
}

// ---------------------------------------------------------------------------
// lab create
// ---------------------------------------------------------------------------

var labCreateCmd = &cobra.Command{
	Use:   "create --slug <slug> --title-zh <名> --title-en <name>",
	Short: "Create a user lab plugin from the conversation (provenance-stamped)",
	Long: `Create a user plugin without leaving the issue conversation.

Convenience flags assemble a minimal manifest:
  --interaction-model assignee|auxiliary   (default auxiliary)
  --leader <agent>                          required for assignee
  --skill <name>                            repeatable; capabilities.skills
  --skills-visibility global|lab_scoped     lab_scoped = skills ride this
                                            lab's runs only

Pass --manifest-file for full control (capabilities.agents_inline /
skills_inline provisioning, runtime commands); file keys win over the
convenience flags. When run inside an agent task (MULTICA_TASK_ID set) the
plugin is stamped with created_by_issue / created_by_task provenance and
the Labs settings page shows where it came from.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, _ := cmd.Flags().GetString("slug")
		titleZh, _ := cmd.Flags().GetString("title-zh")
		titleEn, _ := cmd.Flags().GetString("title-en")
		if slug == "" {
			return fmt.Errorf("--slug is required")
		}
		if titleZh == "" && titleEn == "" {
			return fmt.Errorf("at least one of --title-zh / --title-en is required")
		}

		manifest := map[string]any{}
		interactionModel, _ := cmd.Flags().GetString("interaction-model")
		leader, _ := cmd.Flags().GetString("leader")
		if interactionModel != "" {
			manifest["interaction_model"] = interactionModel
		}
		if leader != "" {
			manifest["leader_agent"] = leader
		}
		skills, _ := cmd.Flags().GetStringArray("skill")
		skillsVis, _ := cmd.Flags().GetString("skills-visibility")
		if len(skills) > 0 || skillsVis != "" {
			caps, _ := manifest["capabilities"].(map[string]any)
			if caps == nil {
				caps = map[string]any{}
			}
			if len(skills) > 0 {
				caps["skills"] = skills
			}
			if skillsVis != "" {
				caps["skills_visibility"] = skillsVis
			}
			manifest["capabilities"] = caps
		}
		if mf, _ := cmd.Flags().GetString("manifest-file"); mf != "" {
			raw, err := os.ReadFile(mf)
			if err != nil {
				return fmt.Errorf("read --manifest-file: %w", err)
			}
			var fileManifest map[string]any
			if err := json.Unmarshal(raw, &fileManifest); err != nil {
				return fmt.Errorf("parse --manifest-file: %w", err)
			}
			for k, v := range fileManifest {
				manifest[k] = v // file is authoritative for keys it sets
			}
		}
		manifestRaw, err := json.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("encode manifest: %w", err)
		}

		body := map[string]any{
			"slug":     slug,
			"title":    map[string]any{"en": titleEn, "zh": titleZh},
			"manifest": json.RawMessage(manifestRaw),
		}
		if v, _ := cmd.Flags().GetString("desc-en"); v != "" {
			body["description"] = map[string]any{"en": v}
		}
		if v, _ := cmd.Flags().GetString("desc-zh"); v != "" {
			desc, _ := body["description"].(map[string]any)
			if desc == nil {
				desc = map[string]any{}
			}
			desc["zh"] = v
			body["description"] = desc
		}
		if v, _ := cmd.Flags().GetString("trigger-mode"); v != "" {
			body["trigger_mode"] = v
		}
		if v, _ := cmd.Flags().GetString("runtime-kind"); v != "" {
			body["runtime_kind"] = v
		}
		// Conversational provenance: the daemon injects these for every
		// agent task; outside an agent context they stay unset (UI parity).
		if inAgentExecutionContext() {
			if v := os.Getenv("MULTICA_ISSUE_ID"); v != "" {
				body["created_by_issue"] = v
			}
			if v := os.Getenv("MULTICA_TASK_ID"); v != "" {
				body["created_by_task"] = v
			}
		}

		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()
		var created map[string]any
		if err := client.PostJSON(ctx, "/api/user-plugins", body, &created); err != nil {
			return fmt.Errorf("create plugin: %w", err)
		}
		if output, _ := cmd.Flags().GetString("output"); output == "plain" {
			fmt.Fprintf(os.Stdout, "created %s (%s)\n", strVal(created, "slug"), strVal(created, "flag_key"))
			for _, p := range toOutcomeList(created["provisioning"]) {
				fmt.Fprintf(os.Stdout, "  %-6s %-24s %s\n", p["type"], p["name"], p["action"])
			}
			fmt.Fprintln(os.Stdout, "enable it with: multica lab enable", strVal(created, "flag_key"))
			return nil
		}
		return cli.PrintJSON(os.Stdout, created)
	},
}

// toOutcomeList coerces the server's []any provisioning/reclaim outcome
// rows into []map[string]any (empty when absent or shaped unexpectedly).
func toOutcomeList(v any) []map[string]any {
	rows, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// lab enable / disable
// ---------------------------------------------------------------------------

// labToggleRunE backs enable/disable. Inside an agent execution context the
// BUILT-IN keys are refused: their toggle also drives install/rollback
// (Restore/Hide + RunInstall) — heavy, user-owned machinery an agent must
// not flip mid-task. User plugins toggle freely (plain per-user pref).
func labToggleRunE(enable bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return fmt.Errorf("lab key is required")
		}
		flagKey := resolveLabFlagKey(args[0])
		if inAgentExecutionContext() && !experimental.IsUserPluginKey(flagKey) {
			return fmt.Errorf("refusing to toggle built-in lab %s from inside an agent task — built-in lab enable/disable drives install/rollback and belongs to the user (Settings → Labs); ask the user to flip it", flagKey)
		}
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()
		var resp map[string]any
		if err := client.PatchJSON(ctx, "/api/experimental-flags/"+flagKey, map[string]any{"enabled": enable}, &resp); err != nil {
			verb := "disable"
			if enable {
				verb = "enable"
			}
			return fmt.Errorf("%s %s: %w", verb, flagKey, err)
		}
		if output, _ := cmd.Flags().GetString("output"); output == "plain" {
			state := "disabled"
			if enable {
				state = "enabled"
			}
			fmt.Fprintf(os.Stdout, "%s %s\n", flagKey, state)
			return nil
		}
		return cli.PrintJSON(os.Stdout, resp)
	}
}

var labEnableCmd = &cobra.Command{
	Use:   "enable <lab>",
	Short: "Enable a user lab plugin (built-ins are refused inside agent tasks)",
	Args:  exactArgs(1),
	RunE:  labToggleRunE(true),
}

var labDisableCmd = &cobra.Command{
	Use:   "disable <lab>",
	Short: "Disable a user lab plugin (built-ins are refused inside agent tasks)",
	Args:  exactArgs(1),
	RunE:  labToggleRunE(false),
}

// ---------------------------------------------------------------------------
// lab delete — two-step conversational protocol
// ---------------------------------------------------------------------------

var labDeleteCmd = &cobra.Command{
	Use:   "delete <slug>",
	Short: "Delete a user plugin and reclaim its resources (two-step: --dry-run, then --confirm)",
	Long: `Delete a user lab plugin and reclaim everything it owns.

The two-step protocol keeps conversational deletes auditable:
  1. multica lab delete <slug>            → prints the reclaim plan, exits 1
     (post the plan to the issue so the user sees what will be reclaimed)
  2. multica lab delete <slug> --confirm  → deletes, prints the per-resource
     reclaim report

--dry-run prints the plan and exits 0 (pure inspection). Provisioned
agents/squads archive, provisioned autopilots pause, provisioned skills are
removed, the plugin's on-disk env moves to ~/.multica/plugins/.trash/;
declared (pre-existing) resources are kept and reported as such. A plugin
with non-terminal bound issues is refused (409).`,
	Args: exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := strings.TrimSpace(args[0])
		slug = strings.TrimPrefix(slug, experimental.UserPluginPrefix)
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		confirm, _ := cmd.Flags().GetBool("confirm")

		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()

		var plan map[string]any
		if err := client.GetJSON(ctx, "/api/user-plugins/"+slug+"/reclaim-plan", &plan); err != nil {
			return fmt.Errorf("delete %s: %w", slug, err)
		}
		renderReclaimPlan(os.Stdout, plan)

		if dryRun {
			return nil
		}
		if !confirm {
			return fmt.Errorf("review the plan above, then re-run with --confirm to delete (or post it to the issue first — the two-step protocol keeps conversational deletes auditable)")
		}

		var report map[string]any
		if err := client.DeleteJSONResponse(ctx, "/api/user-plugins/"+slug, &report); err != nil {
			return fmt.Errorf("delete %s: %w", slug, err)
		}
		if output, _ := cmd.Flags().GetString("output"); output == "plain" {
			fmt.Fprintf(os.Stdout, "deleted %s\n", slug)
			for _, r := range toOutcomeList(report["reclaim"]) {
				fmt.Fprintf(os.Stdout, "  %-9s %-24s %-9s %s\n", r["type"], r["name"], r["origin"], r["action"])
			}
			return nil
		}
		return cli.PrintJSON(os.Stdout, report)
	},
}

// renderReclaimPlan prints a human-readable reclaim plan (dry-run + the
// two-step pre-confirm view share one renderer).
func renderReclaimPlan(w *os.File, plan map[string]any) {
	fmt.Fprintf(w, "reclaim plan for %s (%s)\n", strVal(plan, "slug"), strVal(plan, "flag_key"))
	fmt.Fprintf(w, "  active bound issues: %v\n", plan["active_issues"])
	fmt.Fprintf(w, "  env dir size: %v bytes\n", plan["dir_bytes"])
	resources := toOutcomeList(plan["resources"])
	if len(resources) == 0 {
		fmt.Fprintln(w, "  no ledgered resources — delete only removes the plugin row, its visibility rows, and the flag preference")
		return
	}
	for _, r := range resources {
		name := strVal(r, "name")
		if name == "" {
			name = strVal(r, "id")
		}
		verb := "reclaim"
		if strVal(r, "origin") == "declared" {
			verb = "keep   "
		}
		fmt.Fprintf(w, "  %-9s %-28s %s (%s)\n", r["type"], name, verb, strVal(r, "origin"))
	}
}

// labDelegateNoTaskGrace is how long waitForDelegatedResult waits for a run to
// be dispatched before failing fast with "no run was dispatched". Package-level
// so tests can shorten it; the 30s default keeps the common case (a missing
// leader / offline runtime) from hanging for the full --timeout.
var labDelegateNoTaskGrace = 30 * time.Second

// resolveLabFlagKey normalizes the <lab> argument into the flag key the
// server will accept on POST /api/issues {"lab_source": ...}.
//
// Resolution order (each step short-circuits):
//  1. Already-prefixed user plugin key ("user_event-sim") → pass through.
//  2. Built-in catalog key (e.g. "semantica", "pythia_oracle") → pass
//     through. Pre-Phase 2 this branch was missing, so any built-in
//     lab alias got silently rewritten to "user_<slug>" and the server
//     rejected the create with "lab_source must match a known flag key".
//  3. Bare slug → prepend the "user_" namespace.
//
// Whitespace is trimmed first so `"  semantica  "` resolves the same as
// `"semantica"`.
func resolveLabFlagKey(labArg string) string {
	flagKey := strings.TrimSpace(labArg)
	if experimental.IsUserPluginKey(flagKey) {
		return flagKey
	}
	if experimental.IsKnownKey(flagKey) {
		return flagKey
	}
	flagKey = experimental.UserPluginPrefix + flagKey
	return flagKey
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

	flagKey := resolveLabFlagKey(labArg)

	// AutoDispatch=false labs (pythia_oracle, timesfm — the 0.5.81
	// records-only opt-out) never enqueue a run on issue assignment, so
	// the wait loop below could only ever die in the 30s "no run was
	// dispatched" grace. Fail fast with the reason instead. The 0.5.88
	// live verification caught an agent following the delegation
	// briefing into exactly this dead end before its filter existed.
	if f, ok := experimental.FlagByKey(flagKey); ok && f.AutoDispatch != nil && !*f.AutoDispatch {
		return fmt.Errorf("lab %s opts out of auto-dispatch (AutoDispatch=false): its runs are triggered from the lab panel, not by issue assignment, so it cannot be delegated to", flagKey)
	}

	// Frozen labs (swarm_topology, consolidated into mythos_swarm in 0.5.86)
	// fail fast too — their leader agent row only exists during a Phase-1
	// bootstrap that a fresh delegation never triggers, so the create would
	// land on a lab that can never dispatch. Never-disagree parity with
	// BuildDelegateBrief (which skips f.Frozen): the CLI must reject the same
	// lab set the briefing advertises.
	if f, ok := experimental.FlagByKey(flagKey); ok && f.Frozen {
		if f.SuccessorKey != "" {
			return fmt.Errorf("lab %s is frozen and superseded by %s: use %s instead (a frozen lab cannot accept a delegation)", flagKey, f.SuccessorKey, f.SuccessorKey)
		}
		return fmt.Errorf("lab %s is frozen: it cannot accept a delegation", flagKey)
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
	// --parent / --stage mirror `issue create` exactly (cmd_issue.go): the
	// parent accepts an issue key, full UUID, or UUID prefix through
	// resolveIssueRef, and stage requires >= 1. One resolution scheme, no
	// second dialect.
	parentRef := resolvedID{}
	if v, _ := cmd.Flags().GetString("parent"); v != "" {
		parent, err := resolveIssueRef(createCtx, client, v)
		if err != nil {
			return fmt.Errorf("resolve parent issue: %w", err)
		}
		body["parent_issue_id"] = parent.ID
		parentRef = parent
	}
	if cmd.Flags().Changed("stage") {
		stage, _ := cmd.Flags().GetInt("stage")
		if stage < 1 {
			return fmt.Errorf("--stage must be >= 1")
		}
		body["stage"] = stage
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

	// 0.5.88 delegation loop: with --parent, post the delivered result back
	// on the parent issue so the calling agent (and any human reader) sees
	// the outcome without polling the child. Strictly best-effort — a
	// comment failure warns on stderr and the exit code still reflects the
	// delegation result. Never runs on a failed/cancelled run (the error
	// above already returned).
	if parentRef.ID != "" {
		if cerr := postDelegateParentComment(client, parentRef.ID, flagKey, identifier, title, result.Output); cerr != nil {
			fmt.Fprintf(os.Stderr, "lab delegate: warning: failed to post result comment on parent issue %s: %v\n", parentRef.ID, cerr)
		} else {
			fmt.Fprintf(os.Stderr, "lab delegate: result comment posted on parent issue %s.\n", parentRef.Display)
		}
	}

	if output == "plain" {
		// Keep stdout as the agent's reply text only — the child issue key
		// goes to stderr so callers can still reference it without
		// polluting the reply.
		fmt.Fprintf(os.Stderr, "lab delegate: child issue %s (%s).\n", identifier, issueID)
		fmt.Fprintln(os.Stdout, result.Output)
		return nil
	}
	// json (default): the child issue key rides "identifier" (and
	// "issue_id" carries the UUID) so callers can reference the delegation.
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

// delegateOutputMaxChars caps the result output embedded in the parent
// comment. ~2000 chars keeps the parent timeline readable; the full
// output stays on the child issue and in the task transcript.
const delegateOutputMaxChars = 2000

// postDelegateParentComment posts the delegation-result summary comment on
// the parent issue, using the same client path as `issue comment add`
// (POST /api/issues/{id}/comments). The body leads with a
// "[lab delegate]" header naming the lab + child issue so the timeline
// entry is greppable, then embeds the (capped) result output.
func postDelegateParentComment(client *cli.APIClient, parentID, flagKey, childKey, childTitle, output string) error {
	content := fmt.Sprintf("[lab delegate] %s · %s %s\n\n%s",
		flagKey, childKey, childTitle, truncateDelegateOutput(output))
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var result map[string]any
	return client.PostJSON(ctx, "/api/issues/"+parentID+"/comments", map[string]any{"content": content}, &result)
}

// truncateDelegateOutput caps output at delegateOutputMaxChars runes with a
// "…(truncated)" suffix. Truncation is rune-based (mirrors deriveDelegateTitle)
// so multi-byte UTF-8 results are never split mid-character into invalid
// sequences.
func truncateDelegateOutput(s string) string {
	runes := []rune(s)
	if len(runes) <= delegateOutputMaxChars {
		return s
	}
	return strings.TrimSpace(string(runes[:delegateOutputMaxChars])) + "…(truncated)"
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
	noTaskDeadline := time.Now().Add(labDelegateNoTaskGrace)

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
