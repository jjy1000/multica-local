package experimental

// plugin_brief.go — the claim-time "Lab Plugin Management" briefing
// section (0.5.89 WS1: conversational plugin creation from ANY issue).
//
// A working agent had no way to discover that it could create/manage user
// lab plugins on the user's behalf: the knowledge lived in the
// multica-lab-builder skill (only injected when bound) and in the Labs
// settings tab. This constant is the single source of truth for that
// capability advertisement; daemon.go::ClaimTaskByRuntime appends it to
// the agent instructions next to the delegation brief.
//
// Standing law (extended from 0.5.88): the briefing and the CLI must
// never disagree. Every `multica lab <verb>` command line written below
// MUST exist in cmd/multica/cmd_lab.go's cobra tree — pinned by
// TestPluginManagementBriefVerbsMatchCLI so a briefing edit can never
// advertise a verb the CLI does not ship (the 0.5.88 live verification
// caught exactly this class of drift for the delegate verb).
//
// Keep it small (~650 bytes): it rides EVERY issue-bound claim. It is
// static by design — no DB enumeration, no error path, no budget.
const PluginManagementBrief = `## Lab Plugin Management

You can create and manage user lab plugins for the user from any conversation:

- Create:  multica lab create --slug <slug> --title-zh "<中文名>" --title-en "<name>" [--interaction-model assignee|auxiliary] [--leader <agent-name>]
- Inspect: multica lab list ; multica lab inspect <slug>
- Manage:  multica lab enable|disable <key> (user plugins only) ; multica lab delete <slug> --dry-run (review the reclaim plan, then re-run with --confirm)

Plugin-internal agents/skills/automation stay hidden from Multica and are reclaimed when the plugin is deleted. After creating or changing a plugin, post a "[lab plugin]" comment describing what changed.`
