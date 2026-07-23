---
name: multica-lab-builder
description: "Use when the user wants to create, modify, or delete an experimental lab plugin. Handles the full lifecycle: design the plugin structure, create it via the user-plugins API, provision any agents/skills/autopilots it needs, and report back. Also handles listing existing plugins and updating their configuration."
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

## When to use

Use this skill when the user wants to:

- create a new experimental lab / plugin,
- change an existing plugin's title, description, trigger mode, runtime, or manifest,
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
- `"subprocess"` — an external service the desktop app spawns (Python / Node
  script, local HTTP server) with a health check.
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
      "capabilities": {"skills": [], "agents": [], "squads": []},
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

## Step 5 — provision resources (optional)

If the lab needs agents / skills / squads, create them with the standard CLI and
record their IDs/names back into `manifest.capabilities`:

```sh
multica agent create --slug "$WORKSPACE_SLUG" --name "my-lab-agent" --output json
multica skill create --slug "$WORKSPACE_SLUG" --name "my-lab-skill" --output json
```

Then update the plugin manifest (Step "Modify" below) so the capabilities block
references the provisioned resources. For `runtime_kind: "subprocess"`, describe
the binary / health path in the manifest instead.

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
in `~/.multica/plugins/<slug>/runs.json`. `runtime_kind: "none"` → 400,
`"subprocess"` → 501 (reserved upgrade slot). See
[references/runtime-example.md](references/runtime-example.md) for a full
create → set code → run → verify walkthrough.

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
