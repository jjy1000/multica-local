---
name: multica-lab-builder
description: "Use when the user wants to create, modify, delete, or enable/disable (pause/resume) an experimental lab plugin. Handles the full lifecycle: design the plugin structure, create it via the user-plugins API, provision any agents/skills/autopilots it needs, toggle it on/off, and report back. Also handles listing existing plugins and updating their configuration."
user-invocable: true
allowed-tools: Bash(multica *), Bash(curl *)
---

# Lab builder (user plugins, 0.3.60+)

A *lab plugin* (user plugin) is a user-defined experimental feature that appears in
the Multica Labs tab and, when task-bound, in the issue LabPicker. Unlike the
built-in catalog flags, users create and own these at runtime through a CRUD
API. This skill drives that full lifecycle.

Plugins are managed over the local HTTP API at `http://localhost:8090`. Auth is a
Bearer token from the `MULTICA_API_TOKEN` environment variable — never hardcode
a token. Resource provisioning (agents / skills / squads) goes through the
standard `multica` CLI.

## Your environment & capabilities

You are running **inside the Multica Labs environment** (the Labs sandbox). When
an issue is tagged `[实验室创建]` you have been dispatched specifically to work on
lab plugins, and you hold a task-scoped `MULTICA_API_TOKEN` that authorizes the
full plugin lifecycle over the local API. Concretely, you can:

- **Survey** — list every lab (built-in flags + user plugins) with their enabled
  state, and read any user plugin's full manifest.
- **Create** — author a new user plugin (`POST /api/user-plugins`).
- **Edit** — change any user plugin's title / description / trigger / runtime /
  manifest (`PUT /api/user-plugins/{slug}`).
- **Delete** — remove a user plugin (`DELETE /api/user-plugins/{slug}`).
- **Enable / disable (pause / resume)** — toggle any lab on or off
  (`PATCH /api/experimental-flags/{flag_key}`), including built-in labs.
- **Run** — execute an inline plugin's code and collect artifacts
  (`POST /api/user-plugins/{slug}/run`).

**Boundary:** the 6 **built-in** catalog labs are developer-maintained — you can
enable/disable them but you cannot create, edit, or delete their definitions.
**User plugins** are the objects you fully own (create / edit / delete).
Everything runs on `http://localhost:8090`; never reach outside the machine, and
never hardcode the token.

**Bias to action:** surveying is only the first step, not the deliverable.
Unless the request explicitly asks for a survey only, do not stop after listing
labs and do not claim you "lack permission" to change them — you hold a
task-scoped token that authorizes the full user-plugin lifecycle. Go ahead and
actually create / edit / delete user plugins (`POST` / `PUT` / `DELETE
/api/user-plugins`) to fulfil the intent, then report the concrete changes on
the anchoring issue.

## When to use

Use this skill when the user wants to:

- create a new experimental lab / plugin,
- change an existing plugin's title, description, trigger mode, runtime, or manifest,
- enable or disable (pause / resume) a lab,
- delete a plugin,
- list what plugins exist.

Typical triggers: an issue tagged with lab-creation intent, or a direct request
like "build me a lab that…", "add a plugin for…", "delete the X lab".

Do NOT use this for built-in catalog flags (`claude_science_lab`, `pythia_oracle`,
etc.) — those are developer-only and cannot be created via this API.

## Step 0 — survey the current lab landscape

Before designing anything, read what already exists so you don't duplicate a lab
or collide with a slug. Two reads give the full picture:

```sh
# Built-in labs + user plugins, with enabled state. User plugins carry
# "is_user_plugin": true and a "user_" flag_key prefix.
curl -s http://localhost:8090/api/experimental-flags \
  -H "Authorization: Bearer $MULTICA_API_TOKEN"

# The user-plugin CRUD records (full manifests, slugs, runtimes).
curl -s http://localhost:8090/api/user-plugins \
  -H "Authorization: Bearer $MULTICA_API_TOKEN"
```

From these you can answer "what labs exist right now?", "which are enabled?",
and "what plugins are already built?" — then decide what kind of plugin to
create. When an issue is tagged `[实验室创建]`, run this survey first and
summarize the current state before proposing the new plugin.

## Step 1 — understand the intent

Before writing anything, pin down what kind of lab the user wants. Ask or infer:

- **What does it do?** background automation, issue-bound research, a data
  pipeline, a pure UI surface?
- **Who triggers it?** the system on a schedule, or the user picking it on an issue?
- **What runs it?** Multica's own agents/skills, or an external process?

Anchor durable work in an issue so the user can follow along:

```sh
multica issue create \
  --slug "$WORKSPACE_SLUG" \
  --title "Lab: <short name>" \
  --description "<what the lab does, success criteria>" \
  --output json
```

## Step 2 — choose `trigger_mode`

- `"auto"` — background / self-driven plugin. Runs on its own (schedule or
  internal trigger). Does NOT appear in the issue LabPicker.
- `"issue_select"` — task-bound plugin. The user selects it from the LabPicker on
  an issue; the lab then owns that issue's work.

## Step 3 — choose `runtime_kind`

- `"inline"` — pure Multica resources: agents, skills, autopilots, squads, and
  optional Python code executed in the plugin's persistent env (see Step 7).
  Most labs are `inline`.
- `"subprocess"` — run an external command in the plugin's sandbox env. The
  manifest's `runtime.command` + `runtime.args` are executed directly (argv, no
  shell) with the same minimal env / pinned HOME / timeout as `inline`, and
  files the process emits into the env dir are ingested as artifacts. Use it
  when the work is a binary or script that `python3 -I entry.py` cannot express
  (a Node script, a compiled tool, a shell one-liner via an explicit
  interpreter).
- `"none"` — UI-only toggle. No backend runtime; the manifest just describes a
  surface.

## Step 4 — create the plugin

```sh
curl -s -X POST http://localhost:8090/api/user-plugins \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "slug": "my-lab-name",
    "title": {"en": "My Lab", "zh": "我的实验室"},
    "description": {"en": "What it does", "zh": "功能描述"},
    "trigger_mode": "issue_select",
    "runtime_kind": "inline",
    "manifest": {
      "capabilities": {"skills": [], "agents": [], "autopilots": [], "squads": [], "leader": ""},
      "runtime": {
        "kind": "inline",
        "entry_code": "open('index.html','w').write('<h1>hello lab</h1>')\nprint('done')",
        "timeout_ms": 30000
      },
      "ui": {
        "shell": "standard",
        "tabs": [
          {"key": "chat", "kind": "chat", "label": {"en": "Chat", "zh": "对话"}}
        ]
      }
    }
  }'
```

The server returns the created plugin. `flag_key` is auto-generated as
`"user_" + slug` — you do not set it.

For a `runtime_kind: "subprocess"` plugin, declare the command in the manifest
instead of `entry_code` — `POST /run` executes `runtime.command` +
`runtime.args` directly (argv, no shell) in the same sandbox env:

```json
{
  "runtime_kind": "subprocess",
  "manifest": {
    "runtime": {
      "kind": "subprocess",
      "command": "node",
      "args": ["report.js"],
      "timeout_ms": 30000
    },
    "ui": {"tabs": [{"key": "artifacts", "kind": "artifacts", "label": {"en": "Artifacts", "zh": "产物"}}]}
  }
}
```

## Step 5 — provision resources (optional)

If the lab needs agents / skills / autopilots / squads, create them with the
standard CLI and record their names back into `manifest.capabilities`:

```sh
multica agent create --slug "$WORKSPACE_SLUG" --name "my-lab-agent" --output json
multica skill create --slug "$WORKSPACE_SLUG" --name "my-lab-skill" --output json
```

Then update the plugin manifest (Step "Modify" below) so the capabilities block
references the provisioned resources. For `runtime_kind: "subprocess"`, describe
the binary / health path in the manifest instead.

### What each capability slot means (visibility contract)

The `capabilities` block is not decorative — the server reads it on create to
wire up the lab's resource visibility so a lab is a self-contained container
that does **not** pollute Multica's own rosters:

- **`agents`** — names of agents this lab owns. Each is **hidden** from the
  regular agent picker (a visibility row is seeded). Lab agents live only
  inside the lab; they surface on an issue via the `leader` dispatch below,
  never in the global agent list. This keeps Multica's own agents clean.
- **`autopilots`** — **titles** (autopilots key off `title`, not `name`) of the
  lab's automations. Each is **hidden** from the regular autopilot list the
  same way. Use this for background jobs the lab drives on its own.
- **`squads`** — names of squads the lab owns; also hidden from pickers.
- **`skills`** — names of skills the lab contributes. Skills are **NOT hidden**,
  and (0.3.60+) they are **auto-bound globally**: while the plugin flag is
  enabled, every skill named here is loaded for **every agent in the workspace**
  at task-claim time — no per-agent binding row needed. Disable the plugin and
  the skills stop being injected. This is what makes a **no-agent "tool lab"**
  useful: declare `skills` (+ optional inline runtime / autopilots), leave
  `agents`/`leader` empty, and any Multica agent can call those skills to get
  work done. The skill name must match a real workspace skill row (provision it
  via `multica skill create` first). MCP servers follow the same idea and are
  configured through the agent's own MCP config, not this block.
- **`leader`** — the single agent name (must be one of `agents`) that the lab
  auto-dispatches to. When `trigger_mode: "issue_select"` and a user binds an
  issue to this lab, the server auto-assigns this hidden agent as the issue's
  assignee and enqueues its task — the same mechanism the built-in
  `claude_science_lab → research` dispatch uses. Leave `""` for labs with no
  per-issue agent (pure automation, UI-only, or squad-driven labs).

A typical issue-bound lab therefore declares one hidden `leader` agent plus
whatever skills/MCP it calls:

```json
"capabilities": {
  "agents": ["my-lab-agent"],
  "autopilots": ["My Lab Nightly Sync"],
  "skills": ["my-lab-skill"],
  "squads": [],
  "leader": "my-lab-agent"
}
```

### Two archetypes: tool-lab vs. agent-lab

The capability slots let you build two complementary kinds of lab, and any
Multica agent can use both:

- **Tool-lab (no agent).** A composite of `skills` + optional `autopilots` +
  optional inline runtime, with **no `agents` and empty `leader`**. Its skills
  auto-bind to every agent while enabled (see the `skills` slot above), so it
  acts as a shared capability pack: any agent/team calls the skills inline to
  get something done — no delegation, no separate run. Reach for this when the
  lab is "a thing agents use," not "an agent that runs on its own."
- **Agent-lab (with a leader).** Declares one or more hidden `agents` and a
  `leader`. It runs as its own specialist: a caller **delegates** a
  self-contained sub-task to the leader, which executes it in the lab and
  returns a deliverable (see "Delegating a sub-task to a lab agent" below).
  Reach for this when the work is a genuine independent run — a simulation, a
  deep analysis, a scenario war-game — whose result feeds back into the
  caller's task.

## Step 6 — report back

Summarize the result on the anchoring issue so the user has a durable record:

```sh
multica issue comment \
  --slug "$WORKSPACE_SLUG" \
  --issue "$ISSUE_ID" \
  --body "Created lab **my-lab-name** (flag_key: user_my-lab-name, trigger: issue_select, runtime: inline)." \
  --output json
```

## Step 7 — run the lab (inline runtime)

An `inline` plugin can execute Python code in a **persistent per-plugin env**
at `~/.multica/plugins/<slug>/env/`. Files the run emits (png/svg → image,
html → html, everything else → file) are ingested into the plugin's artifact
store and show up in the panel's 产物 (Artifacts) tab automatically.

**The env is a container-like, on-demand workspace (no Docker).** It runs only
when triggered, but the dir persists, so the lab gets private stateful storage.
The run process receives these environment variables:

- `MULTICA_PLUGIN_SLUG` — the plugin slug.
- `MULTICA_PLUGIN_ENV` — the persistent env dir (also the process cwd).
- `MULTICA_PLUGIN_DB` — a ready-to-use **SQLite** path (`env/data.db`). Open it
  with the stdlib `sqlite3` module; state accumulates across runs (e.g. store a
  keymap graph, then render it next run). Zero install, per-plugin isolated.

The database file and its `-wal`/`-shm`/`-journal` sidecars, plus dotfiles and
`.pyc`, stay **private** — they are never ingested as artifacts. Only real
deliverables (the html/png/etc. you write) surface in the panel.

Code source priority: request body `code` → `manifest.runtime.entry_code` →
the `env/entry.py` written by a previous run. Declare `entry_code` in the
manifest so the lab is reproducible from scratch:

```sh
curl -s -X PUT http://localhost:8090/api/user-plugins/my-lab-name \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime_kind": "inline",
    "manifest": {
      "runtime": {
        "kind": "inline",
        "entry_code": "with open(\"index.html\",\"w\") as f: f.write(\"<h1>hello lab</h1>\")\nprint(\"done\")",
        "timeout_ms": 30000
      },
      "ui": {"tabs": [{"key": "artifacts", "kind": "artifacts", "label": {"en": "Artifacts", "zh": "产物"}}]}
    }
  }'
```

Then run it (empty body re-runs the persisted `entry.py` / manifest code):

```sh
curl -s -X POST http://localhost:8090/api/user-plugins/my-lab-name/run \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

The response is `{status, exit_code, stdout, stderr, duration_ms, artifacts}`
where `status` ∈ `completed | failed | timeout`. A run history summary is kept
in `~/.multica/plugins/<slug>/runs.json`. `runtime_kind: "none"` → 400;
`"subprocess"` runs `manifest.runtime.command` + `args` (argv, no shell) in the
same sandbox env and ingests the files it emits. See
[references/runtime-example.md](references/runtime-example.md) for a full
create → set code → run → verify walkthrough.

### Step 7b — run a subprocess plugin

For `runtime_kind: "subprocess"`, `POST /run` executes the manifest's
`runtime.command` + `runtime.args` directly in the plugin env dir (cwd) under
the same sandbox env — no `entry.py`, no shell. The process must terminate on
its own; the `timeout_ms` applies. Files it writes into the env dir are
ingested as artifacts exactly like inline runs:

```sh
curl -s -X PUT http://localhost:8090/api/user-plugins/my-lab-name \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime_kind": "subprocess",
    "manifest": {
      "runtime": {
        "kind": "subprocess",
        "command": "python3",
        "args": ["-c", "open('report.txt','w').write('subprocess-ok')"],
        "timeout_ms": 30000
      },
      "ui": {"tabs": [{"key": "artifacts", "kind": "artifacts", "label": {"en": "Artifacts", "zh": "产物"}}]}
    }
  }'

curl -s -X POST http://localhost:8090/api/user-plugins/my-lab-name/run \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

The command is resolved on `PATH` (or as an absolute path); `runtime.args` is
passed verbatim as argv — the server never invokes a shell, so shell
metacharacters are rejected at the declaration layer. A `subprocess` plugin
without `runtime.command` → 400.

## Delegating a sub-task to a lab agent (`multica lab delegate`)

An **agent-lab** (one that declares a `leader`) can be called by any other
Multica agent or team as a synchronous specialist. This is the mechanism behind
"a team agent, mid-task, hands a self-contained run to a lab and continues once
the result comes back" — e.g. a planning team that needs a virtual event
simulation delegates it to a simulation lab, waits, then folds the outcome into
its plan.

The verb is a single blocking CLI call the caller runs from inside its own task:

```sh
# <lab> is the plugin slug (or its user_<slug> flag key); <task> is the
# instruction + the result you expect back. Blocks until the lab agent finishes.
multica lab delegate my-lab-name "Run a 3-round war-game of scenario X and return the win/loss table plus key turning points."
```

What happens under the hood (no separate API to call):

1. The command creates a lab-bound issue (`lab_source = user_<slug>`). The
   server auto-assigns the lab's `leader` agent and enqueues its run — the same
   dispatch path as `trigger_mode: "issue_select"`.
2. The command **blocks**, polling the run until it reaches a terminal state.
3. On success it prints the agent's final reply. `--output json` (default)
   emits `{ok, issue_id, identifier, task_id, status, output, error}`; `--output
   plain` prints just the reply text so the caller can capture it directly.

Useful flags: `--timeout` (default `15m`), `--poll-interval` (default `3s`),
`--title` (defaults to a snippet of the task), `--status` (default `todo` — must
be non-backlog so the run dispatches).

**Prerequisites for a lab to be delegable:** it must be **enabled**, declare a
`capabilities.leader` agent, and that agent must be bound to a running daemon
runtime. If no run is dispatched within a short grace window the command exits
with a clear "no run was dispatched" error rather than hanging. Because the run
is a normal issue-bound task, it is fully observable in the UI (transcript,
usage) — delegation is a visible run, not a hidden RPC.

When you build an agent-lab meant to be delegated to, make its `leader` agent's
instructions explicit about **returning a self-contained deliverable as the
final reply** (the caller reads exactly that text), and keep the run
self-contained so it terminates without waiting on human input.

## Listing plugins

```sh
curl -s http://localhost:8090/api/user-plugins \
  -H "Authorization: Bearer $MULTICA_API_TOKEN"
```

Read this before creating (to avoid slug collisions) and before modifying or
deleting (to confirm the target exists).

## Modifying a plugin

Read the current record, change the fields you need, and send the whole updated
object back via `PUT`:

```sh
curl -s -X PUT http://localhost:8090/api/user-plugins/my-lab-name \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": {"en": "My Lab v2", "zh": "我的实验室 v2"},
    "description": {"en": "Updated", "zh": "已更新"},
    "trigger_mode": "auto",
    "runtime_kind": "inline",
    "manifest": { "...": "..." }
  }'
```

## Deleting a plugin

```sh
curl -s -X DELETE http://localhost:8090/api/user-plugins/my-lab-name \
  -H "Authorization: Bearer $MULTICA_API_TOKEN"
```

Deletion removes the plugin definition. Resources it provisioned (agents/skills)
are separate rows — delete them explicitly via the CLI if the user wants them gone.

## Enabling / disabling a plugin (pause / resume)

Toggling a lab on or off is a **separate axis** from create/edit/delete: it flips
the enabled state without touching the plugin definition or its artifacts. Use it
to pause a lab (disable) or bring it back (enable). This works for user plugins
AND built-in labs. The path key is the `flag_key` — for a user plugin that is
`"user_" + slug` (e.g. slug `my-lab-name` → `user_my-lab-name`).

```sh
# Disable (pause) — hides the lab's resources, keeps the definition
curl -s -X PATCH http://localhost:8090/api/experimental-flags/user_my-lab-name \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'

# Enable (resume) — reveals/provisions the lab's resources again
curl -s -X PATCH http://localhost:8090/api/experimental-flags/user_my-lab-name \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

For an installable lab, enabling also provisions/restores its agents/skills/
squads and disabling hides them. Either way the definition and artifacts are
preserved — disable is a pause, not a delete. The same endpoint accepts a
built-in `flag_key` (e.g. `mythos_swarm`) to toggle a built-in lab.

## Self-test checklist

Before reporting a lab as done, walk the full lifecycle against a live server
(`$MULTICA_API_TOKEN` must be set; the desktop app must be running on
`http://localhost:8090`). `scripts/lab-plugin-smoke.sh` automates steps 1–4 and
6 — run it first, then do the delegate leg manually if you have a leader-bound
agent:

1. **Create** — `POST /api/user-plugins` with a unique slug; assert
   `flag_key = user_<slug>` comes back.
2. **Set entry** — `PUT` the plugin with `runtime_kind: "inline"` +
   `manifest.runtime.entry_code` that writes at least one file.
3. **Run inline** — `POST /api/user-plugins/<slug>/run` (empty body); assert
   `status == "completed"`.
4. **Run subprocess** — `PUT` to `runtime_kind: "subprocess"` +
   `runtime.command`/`args`, then `POST /run` again; assert `status ==
   "completed"` and the emitted file shows up in `artifacts[]`.
5. **Delegate** — if the plugin declares `capabilities.leader` (an agent-lab),
   call `multica lab delegate <slug> "<task>"` and assert it returns the
   leader's final reply.
6. **Assert artifacts** — `GET /api/user-plugins/<slug>/artifacts`; the files
   from both runs must be present and raw-fetchable.

## Rules

- `slug` must be lowercase alphanumeric + hyphens, 2–64 chars. It is the stable
  identity of the plugin; choose it carefully.
- `flag_key` is auto-generated as `"user_" + slug`. Never set it yourself.
- Plugins are hidden from the normal agent / squad pickers by default (the
  `lab_managed` contract) so lab internals do not leak into regular selection.
- `trigger_mode: "auto"` plugins do NOT appear in the issue LabPicker.
- `manifest.ui.tabs` supports these `kind`s:
  - `"chat"` — chat pane
  - `"artifacts"` — file / image / chart display
  - `"table"` — data grid
  - `"iframe"` — custom HTML
  - `"code"` — code display
- Keep the manifest flexible — users define their own lab structure. Do not
  over-constrain the shape beyond what the API requires.
- Keep `title` and `description` bilingual (`en` + `zh`) to match the product's
  localization contract.
