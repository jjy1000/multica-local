---
name: multica-claude-science-runtime
description: "在科研实验室沙箱里真跑 Python 并回收产物(PNG/SVG/HTML/JSON/CSV/MD),以及按需加载 294 个研究技能 — 需要真实计算/绘图/跑包时用本 Skill, 不要用 Bash(python3 …) 内联替代. 触发词: 跑代码 / 执行代码 / 跑脚本 / 跑数据 / 画图 / 出图 / 绘图 / 算一下 / 统计 / 数据分析 / 模拟 / 跑实验 / 加载研究技能. 入口: `multica experimental claude-lab execute --code-file <path>`, 技能目录 `… claude-lab skills`, 加载单个 `… claude-lab skill <name>`. 受 claude_science_lab 开关控制, 关闭时拒绝执行并退回普通 plan."
user-invocable: true
allowed-tools: Bash(multica *), Bash(git *)
---

# Claude Science Runtime (0.3.19+, reworked 0.5.106)

The runtime is a sandboxed Python executor that complements the
`multica-claude-science` Skill: where the Skill drives
multi-step **research planning** via the standard Multica agent
runtime, the runtime gives the same agent a way to **run code**,
**collect artifacts**, and **load research skills on demand**.

Surface — five verbs. **The subcommand is `claude-lab`, not
`claude-science-runtime`** (renamed by the 0.3.22 lab consolidation;
`Use:` in `cmd_experimental.go:76`). `multica experimental claude-lab
--help` lists the live set — treat that output as authoritative over
this table if they ever disagree.

- `multica experimental claude-lab execute --workspace-id <uuid>
   --agent-id <id> --code-file <path> [--timeout-ms <n>] [--session <uuid>]`
- `multica experimental claude-lab sessions --workspace-id <uuid>`
- `multica experimental claude-lab artifacts --session-id <uuid>`
- `multica experimental claude-lab skills [--workspace-id <uuid>]`
- `multica experimental claude-lab skill <name> [--file <path>] [--workspace-id <uuid>]`

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
multica experimental claude-lab execute \
  --workspace-id "$WORKSPACE_ID" \
  --agent-id "$AGENT_ID" \
  --code-file /tmp/multica-experiment.py \
  --timeout-ms 60000 \
  --output json
```

`--agent-id` is the real flag (there is no `--agent`). It defaults to
the `MULTICA_AGENT_ID` the daemon injects, so inside an agent task you
can usually omit it.

The response carries `session_id`, `root_session_id`, `status`,
`exit_code`, `stdout`, `stderr`, `duration_ms`, and `artifacts[]`.
Each artifact has `id`, `name`, `kind`, `bytes`, `sha256`, and `url`.

## Step 3 — narrate + attach

In the issue thread, post a comment that references the artifacts:

```sh
multica issue comment add "$ISSUE_ID" \
  --content "## 实验运行 #$SESSION_ID (status=$STATUS, exit=$EXIT_CODE, $DURATION_MS ms)

\$STDOUT

\$(artifact list)

下载: <url for each artifact>"
```

`multica issue comment` is a command GROUP (`add` / `delete` / `list` /
`resolve` / `unresolve`) and carries no `--slug` / `--issue` / `--body`
flags of its own — the issue id is a positional argument and the body
is `--content` (or `--content-file` / `--content-stdin`). Use
`--content-file ./body.md` for long multi-line reports.

The Claude Lab workbench picks up the artifacts on the next refetch
and renders PNG/SVG/JSON/CSV/MD inline.

## Step 4 — cleanup

If the agent's run is a one-off (no follow-up), drop the session
so the GC walk can purge the artifact directory:

```sh
multica experimental claude-lab delete --session-id "$SESSION_ID"
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
