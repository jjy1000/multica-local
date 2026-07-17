---
name: multica-claude-science-runtime
description: "Use when the user wants to execute Python code inside the Claude Science research workspace, capture stdout, and surface generated artifacts (PNG / SVG / HTML / JSON / CSV / MD). The runtime is gated behind the `claude_science_runtime` Labs flag — when the flag is off, this Skill must refuse to run code and fall back to the standard in-band agent flow (issue + comments). Do NOT use for chat / issue / platform operations; that is what multica-mentioning / multica-working-on-issues cover."
user-invocable: true
allowed-tools: Bash(multica *), Bash(git *)
---

# Claude Science Runtime (0.3.19+)

The runtime is a sandboxed Python executor that complements the
`multica-claude-science` Skill: where the Skill drives
multi-step **research planning** via the standard Multica agent
runtime, the runtime gives the same agent a way to **run code** and
collect artifacts.

It is intentionally small in surface — three verbs:

- `multica experimental claude-science-runtime execute <workspace>
   --agent <id> --code-file <path> [--timeout-ms <n>]`
- `multica experimental claude-science-runtime sessions <workspace>`
- `multica experimental claude-science-runtime artifacts <session>`

The CLI dispatcher lives at
`server/cmd/multica/cmd_experimental.go` (PR-8 / 0.3.19); for the
0.3.19 first cut the minimal shape is enough.

## Hard rule — flag check before any execute call

Before issuing any execute call, **check the flag**:

```
multica experimental flags list 2>/dev/null
```

If `claude_science_runtime` is `false` (or the flag is missing),
**refuse to run code** and tell the user:

> 实验运行环境未启用。请在 Settings → Labs 打开「Claude Science
> Runtime Sandbox」开关。关闭后请继续以普通多步骤 plan 的方式完成
> 研究。

Do NOT fall back to running the code inline with `Bash(python3 ...)` —
that's exactly the on-disk execution path 0.3.18's safety net was
built to keep out of the labs surface.

## Step 1 — capture the snippet

When the agent decides it needs to run code, save the snippet to a
short-lived file (e.g. `/tmp/multica-experiment.py`) so the CLI can
`-F` it through without base64 gymnastics. Keep the snippet under
64 KiB; the handler enforces this and returns 413 over the limit.

## Step 2 — execute

```sh
multica experimental claude-science-runtime execute "$WORKSPACE_SLUG" \
  --agent "$AGENT_ID" \
  --issue "$ISSUE_ID" \
  --code-file /tmp/multica-experiment.py \
  --timeout-ms 60000 \
  --output json
```

The response carries `status`, `exit_code`, `stdout`, `stderr`,
`duration_ms`, and `artifacts[]`. Each artifact has `id`, `name`,
`kind`, `bytes`, `sha256`, and `url`.

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

The renderer panel at `/experimental/claude-science` will pick up
the artifacts on the next refetch and render PNG/SVG/HTML/JSON/CSV/MD
inline.

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
  runtime/<session_uuid>/`.** Any file you want the user to keep
  must be re-posted as a Multica comment attachment (attachment
  subsystem already handles it for chat messages); the runtime
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

- Catalog flag: `claude_science_runtime` (server/internal/experimental/catalog.go)
- Handler: `server/internal/handler/claude_science_runtime.go`
- SQLC queries: `pkg/db/queries/experimental_claude_runtime.sql`
- Migration: `server/migrations/151_experimental_claude_runtime.up.sql`
- 0.3.19 platform blueprint: `.omc/plans/multica-labs-platform-blueprint-0.3.19.html`
  (P4 "runtime 沙箱" — this Skill is the runtime Adapter)
