---
name: multica-claude-science
description: "Use when the user wants a long-running scientific research session — multi-step investigation across biology / physics / ML / literature, with a structured plan of evidence gathering and synthesis. The work executes through Multica's standard agent runtime: the agent creates the issue (or picks up an existing one), assigns a research-capable member, and the Multica LLM provider configured in Settings → 模型 powers every step. Do NOT use for chat / issue / platform operations; that is what multica-mentioning / multica-working-on-issues cover."
user-invocable: true
allowed-tools: Bash(multica *), Bash(git *)
---

# Claude Science workspace (0.3.14+, Multica-native)

0.3.14 retired the bundled OpenScience SolidJS renderer and its separate BYOK
HTTP service. The lab now lives entirely inside Multica — the same agent
runtime the user already trusts for chat / issues / squads, the same model
configured in Settings → 模型. There is no longer a loopback URL to query and
no separate binary to manage.

## Step 1 — anchor the question in an issue

Research work in Multica is durable: it lives on an issue, gets comments,
shows up in the squad's queue. Before kicking off anything, decide whether
the user already has an open issue to attach to. If yes, read it first. If
no, create one and capture the topic in the title.

```sh
multica issue create \
  --slug "$WORKSPACE_SLUG" \
  --title "科研: <short topic>" \
  --description "<expanded scope, hypotheses, success criteria>" \
  --output json
```

The response carries `id` and `key`. Keep both.

## Step 2 — plan the steps

Before touching models, write a short plan into the issue as a comment so
the user (and any future agent) can see the shape of the work. The plan
should split into concrete steps (literature scan → design → experiments
→ synthesis), each with an explicit deliverable.

```sh
multica issue comment \
  --slug "$WORKSPACE_SLUG" \
  --issue "$ISSUE_ID" \
  --body "## 计划\n\n1. ...\n2. ...\n3. ..." \
  --output json
```

The agent runtime picks up the comment, dispatches to a research-capable
agent (configured in workspace settings), and starts producing. Each step's
output appears as additional comments / status changes on the same issue.

## Step 3 — monitor and steer

The work runs in-band through the standard Multica agent runtime. To
observe progress, poll the issue or the inbox; do not invent a polling
loop of your own.

```sh
multica issue get --slug "$WORKSPACE_SLUG" --issue "$ISSUE_ID"
multica issue comments --slug "$WORKSPACE_SLUG" --issue "$ISSUE_ID"
```

To redirect (narrow scope, add a constraint, reject an early conclusion),
post a fresh comment — agents subscribed to the issue pick it up the same
way they pick up any user message.

## Step 4 — synthesize

When the agent signals completion (status change to "done" / "in review",
or a final summary comment), read back the issue, then write a short
coda comment so the reasoning chain stays in Multica:

```sh
multica issue comment \
  --slug "$WORKSPACE_SLUG" \
  --issue "$ISSUE_ID" \
  --body "## 结论摘要\n\n…"
```

## Hard rules

- **Do not re-implement a research agent.** Delegating to the existing
  Multica agent runtime (via the issue + skill path above) IS the work.
  Re-implementing it bypasses the user's auth, model configuration, and
  audit trail, and is what 0.3.14 specifically removed.
- **Do not pass BYOK keys.** Claude Science no longer ships its own key
  store. The model's API key lives in Settings → 模型 — the same place
  that backs chat and issue-side agent activity.
- **Do not fabricate results.** If the agent's run is still in progress,
  say so honestly with the most recent observation timestamp. If it
  stalls, post a polite nudge comment rather than guessing.
- **Do not assume sub-domain mapping.** biology / physics / ml / plan are
  not separate binaries in 0.3.14; they are agent capabilities surfaced
  through the workspace's member roster. Use whichever member the
  workspace admin has marked as the right fit.

## Surface (this version)

- Catalog flag: `claude_science_lab` (server/internal/experimental/catalog.go,
  0.3.22+ — replaces the 0.3.20 `claude_science` + `claude_science_runtime`
  pair per 0.3.22 lab consolidation).
- Inline runtime; no desktop subprocess manager.
- Wire path: `/api/experimental/claude-science-runtime/*` (the 0.3.20
  route prefix is preserved for wire-compat with this Skill adapter).
- Renderer page: `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx`
  (consolidated 0.3.22 lab view replacing the retired
  `claude-science-view.tsx`).
- Wire contract source map: see `references/` siblings if present; if not,
  the SKILL.md above and the catalog comment block are ground truth.
