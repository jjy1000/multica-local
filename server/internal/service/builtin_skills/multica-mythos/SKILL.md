---
name: multica-mythos
description: "Use when the user wants to decompose an open-ended research problem into parallel sub-tasks and iterate until consensus. Mythos Swarm maps the OpenMythos RDT (Recurrent-Depth Transformer) three-stage topology onto Multica squads: a prelude agent plans the work, N parallel loop agents iterate (cosine ≥ 0.95 convergence), and a coda agent synthesises. Requires the `mythos_swarm` Labs flag enabled and the install handler to have provisioned the mythos-swarm workspace + agents. Do NOT use for chat / issues / platform operations — that is what multica-mentioning / multica-working-on-issues cover."
user-invocable: true
allowed-tools: Bash(multica *), Bash(git *)
---

# Mythos Swarm Topology (experimental)

Mythos Swarm provisions a dedicated `mythos-swarm` workspace with 5 agents
and 1 squad. The prelude agent breaks the problem into sub-tasks, parallel
loop agents iterate each sub-task independently (convergence measured by
cosine similarity ≥ threshold), and the coda agent synthesises the final
answer. Every loop turn can invoke Claude Science skills from the lab's
catalogue.

## Step 0 — confirm the flag is on

```
multica experimental flags list 2>/dev/null
```

If `mythos_swarm` is `false` (or missing), **stop** and tell the user:

> Mythos Swarm 未启用。请在 Settings → Labs 打开「Mythos Swarm Topology」
> 然后点击安装。

## Step 1 — anchor the question in an issue

Create an issue in the `mythos-swarm` workspace:

```sh
multica issue create \
  --slug mythos-swarm \
  --title "Mythos: <short topic>" \
  --description "<expanded scope, constraints, success criteria>" \
  --output json
```

The response carries `id` and `key`. Keep both.

## Step 2 — run the mythos decomposition

```sh
multica experimental mythos run \
  --workspace mythos-swarm \
  --issue "$ISSUE_ID" \
  --max-loops 16 \
  --convergence 0.95 \
  --output json
```

The response carries `status`, `iterations_run`, `convergence_history[]`,
and `coda_summary`.

## Agent roster

| Role | Agent name | What it does |
|------|-----------|-------------|
| prelude | `mythos_prelude` | Problem decomposition, task splitting |
| loop | `mythos_loop_researcher` | Literature and research |
| loop | `mythos_loop_coder` | Code and experiments |
| loop | `mythos_loop_analyst` | Data analysis |
| coda | `mythos_coda` | Multi-round fusion and summary |

The agents operate in a `Claude Science 联合体` squad with the prelude as
leader. The user's existing squads and agents are never touched.

## Hard rules

- **Do not re-implement the RDT runner.** The CLI dispatcher at
  `server/cmd/multica/cmd_experimental.go` is the entry point. Re-
  implementing it bypasses the convergence tracking and is what the
  prelude/loop/coda topology was explicitly designed to prevent.
- **Do not skip the flag check.** When the flag is off, the workspace
  and agents may still exist but the runner refuses. Fall back to
  in-band Multica issue planning.
- **Respect the convergence threshold.** The default is 0.95. Lowering
  it produces faster but noisier results. Raising it delays completion
  without guaranteed improvement.
- **Do not fabricate convergence data.** If the runner is still in
  progress, report the last observed iteration count honestly.

## Where to look

- Catalog flag: `mythos_swarm` (server/internal/experimental/catalog.go)
- Install handler: `server/internal/handler/install_mythos.go`
- Runner: `server/internal/handler/mythos_runner.go`
- Renderer view: `apps/desktop/src/renderer/src/pages/mythos-view.tsx`
