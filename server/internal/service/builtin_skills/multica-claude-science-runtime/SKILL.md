---
name: multica-claude-science-runtime
description: "Use when the user wants to execute Python code inside the Claude Science research workspace, capture stdout, and surface generated artifacts (PNG / SVG / HTML / JSON / CSV / MD). Also use to load a specific research skill from the lab's 294-skill catalogue on demand. The runtime is gated behind the `claude_science_lab` Labs flag — when the flag is off, this Skill must refuse to run code and fall back to the standard in-band agent flow (issue + comments). Do NOT use for chat / issue / platform operations; that is what multica-mentioning / multica-working-on-issues cover."
user-invocable: true
allowed-tools: Bash(multica *), Bash(git *)
---

# Claude Science Runtime (0.3.19+, reworked 0.5.106)

The runtime is a sandboxed Python executor that complements the
`multica-claude-science` Skill: where the Skill drives
multi-step **research planning** via the standard Multica agent
runtime, the runtime gives the same agent a way to **run code**,
**collect artifacts**, and **load research skills on demand**.

Surface — five verbs:

- `multica experimental claude-science-runtime execute --workspace-id <uuid>
   --agent <id> --code-file <path> [--timeout-ms <n>] [--session <uuid>]`
- `multica experimental claude-science-runtime sessions --workspace-id <uuid>`
- `multica experimental claude-science-runtime artifacts --session-id <uuid>`
- `multica experimental claude-science-runtime skills [--workspace-id <uuid>]`
- `multica experimental claude-science-runtime skill <name> [--file <path>] [--workspace-id <uuid>]`

The CLI dispatcher lives at `server/cmd/multica/cmd_experimental.go`.

## Hard rule — flag check before any execute call

Before issuing any execute call, **check the flag**:

```
multica experimental flags list 2>/dev/null
```

If `claude_science_lab` is `false` (or the flag is missing),
**refuse to run code** and tell the user:

> 实验运行环境未启用。请在 Settings → Labs 打开「Claude Science
> Lab」开关。关闭后请继续以普通多步骤 plan 的方式完成研究。

Do NOT fall back to running the code inline with `Bash(python3 ...)` —
that's exactly the on-disk execution path the labs safety net keeps
out of the labs surface.

## Session continuation (0.5.106, notebook-style)

By default each `execute` call gets a fresh working directory. Pass
`--session <uuid>` to **reuse a prior run's working directory**: files
written by earlier runs in that workspace are visible to the new run,
so multi-step experiments can split work across calls (download in one
run, analyse in the next). The execute response carries
`root_session_id` — pass THAT value to continue in the same workspace.
An expired root (30-day TTL) answers 410; start a fresh session
instead.

## On-demand skill loading (0.5.106)

The lab ships ~294 research skills across categories (biology,
chemistry, ml-training, databases, quantum, …). Their bodies are NOT
injected into your context — load what you need, when you need it:

1. **Discover**: `… claude-science-runtime skills` — name, category,
   one-line description per skill.
2. **Load**: `… claude-science-runtime skill anndata` — prints the
   full SKILL.md body plus its supporting-file list. **Follow the
   loaded skill's instructions** for the domain task.
3. **Supporting files**: `… claude-science-runtime skill anndata
   --file references/concatenation.md` — prints a specific reference /
   script file (text formats only).

Load a skill when the research domain matches its category or name;
do not load more than the task needs.

## Step 1 — capture the snippet

When the agent decides it needs to run code, save the snippet to a
short-lived file (e.g. `/tmp/multica-experiment.py`) so the CLI can
pass it through without base64 gymnastics. Keep the snippet under
64 KiB; the handler enforces this and returns 413 over the limit.

## Step 2 — execute

```sh
multica experimental claude-science-runtime execute \
  --workspace-id "$WORKSPACE_ID" \
  --agent "$AGENT_ID" \
  --code-file /tmp/multica-experiment.py \
  --timeout-ms 60000 \
  --output json
```

The response carries `session_id`, `root_session_id`, `status`,
`exit_code`, `stdout`, `stderr`, `duration_ms`, and `artifacts[]`.
Each artifact has `id`, `name`, `kind`, `bytes`, `sha256`, and `url`.

## Step 3 — narrate + attach

In the issue thread, post a comment that references the artifacts:

```sh
multica issue comment \
  --slug "$WORKSPACE_SLUG" \
  --issue "$ISSUE_ID" \
  --body "## 实验运行 #$SESSION_ID (status=$STATUS, exit=$EXIT_CODE, $DURATION_MS ms)

\$STDOUT

\$(artifact list)

下载: <url for each artifact>" \
  --output json
```

The Claude Lab workbench picks up the artifacts on the next refetch
and renders PNG/SVG/JSON/CSV/MD inline.

## Step 4 — cleanup

If the agent's run is a one-off (no follow-up), drop the session
so the GC walk can purge the artifact directory:

```sh
multica experimental claude-science-runtime delete --session-id "$SESSION_ID"
```

User-driven deletion is idempotent; expired sessions (TTL = 30 days)
are auto-collected by the background GC
(`server/internal/experimental/runtime_gc.go`).

## Hard rules

- **Always check the flag first.** Refuse silent fallback to inline
  python invocations.
- **Do not exceed the 64 KiB snippet cap.** Trim or split if you hit
  it; the 413 error is intentional.
- **Do not write outside `~/.multica/experimental/claude-science/
  runtime/<root_session_id>/`.** Any file you want the user to keep
  must be re-posted as a Multica comment attachment; the runtime
  directory itself is GC'd.
- **Do not loop forever.** The handler enforces a 120 s ceiling
  per run; the wall-clock `timeout_ms` you choose is the cap.
- **Do not include secrets in the snippet.** stdout is recorded
  into the session row and may be re-rendered in the issue thread.
- **Do not re-implement the runtime as a Skill.** The Skill is the
  bus; the Go handler in
  `server/internal/handler/claude_science_runtime.go` is the
  executor.

## Where to look

- Catalog flag: `claude_science_lab` (server/internal/experimental/catalog.go;
  the pre-0.3.22 `claude_science_runtime` key was consolidated away)
- Handler: `server/internal/handler/claude_science_runtime.go`
- Skill catalogue/loading: `server/internal/handler/claude_science_skills.go`
- SQLC queries: `pkg/db/queries/experimental_claude_runtime.sql`
- Migration: `server/migrations/151_experimental_claude_runtime.up.sql`
