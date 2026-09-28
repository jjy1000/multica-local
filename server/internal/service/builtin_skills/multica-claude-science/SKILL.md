---
name: multica-claude-science
description: "科研实验室 / 多步研究任务首选 — 派工时优先用本 Skill, 不要自己扮演研究员或编造结论顶替. 触发词: 科研 / 研究 / 深入研究 / 调研 / 文献调研 / 综述 / 复现 / 复现论文 / 验证假设 / 实验设计 / 可行性分析 / 差异分析 / 跑个实验. 入口: `multica issue create --title … --description … --assignee research` 建 issue 交给 research leader(或在已有 issue 上继续), 逐步产出以顶层评论交付, 报告/表格/图落在该 issue 的实验 session 上. 要真跑代码见 multica-claude-science-runtime. 平台操作(建普通 issue / @mention / 派工)走 multica-working-on-issues 与 multica-mentioning."
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
  --title "科研: <short topic>" \
  --description "<expanded scope, hypotheses, success criteria>" \
  --assignee research \
  --output json
```

There is no `--slug` / `--workspace-id` flag: the workspace comes from
`MULTICA_WORKSPACE_ID` (the daemon injects it into every agent task) or
your local profile. `--assignee` is the key one — naming `research`
puts the lab leader on the issue, which is what makes the run pick up
the lab's skills.

The response carries `id` and `key`. Keep both.

**Binding the issue to the lab itself** is a separate step — the CLI has
no `--lab-source` flag, so use the API (your task token is in
`MULTICA_API_TOKEN`, and the API base is in `MULTICA_SERVER_URL`):

```sh
curl -sS -X PATCH "$MULTICA_SERVER_URL/api/issues/$ISSUE_ID" \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "X-Workspace-ID: $MULTICA_WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{"lab_source":"claude_science_lab"}'
```

The server rewrites the assignee to the lab leader and enqueues the
research task. If the issue is already lab-bound, skip this — setting
`lab_source` on an issue that already has it is a no-op.

## Step 2 — plan the steps

Before touching models, write a short plan into the issue as a comment so
the user (and any future agent) can see the shape of the work. The plan
should split into concrete steps (literature scan → design → experiments
→ synthesis), each with an explicit deliverable.

```sh
multica issue comment add "$ISSUE_ID" \
  --content "## 计划

1. ...
2. ...
3. ..."
```

For a long plan, write it to a file and pass `--content-file ./plan.md`
rather than fighting shell quoting.

The agent runtime picks up the comment, dispatches to a research-capable
agent (configured in workspace settings), and starts producing. Each step's
output appears as additional comments / status changes on the same issue.

## Step 2b — load domain skills on demand (0.5.106)

The lab ships ~294 research skills (biology, chemistry, physics,
ml-training, databases, quantum, …). Their bodies are NOT in your
context — discover and load only what the current research step needs:

```sh
# browse the catalogue (name / category / one-line description)
multica experimental claude-lab skills

# load the full SKILL.md body of a relevant skill, then follow it
multica experimental claude-lab skill anndata

# read a supporting reference or script
multica experimental claude-lab skill anndata \
  --file references/concatenation.md
```

Load a skill when the plan's current step falls into its domain; skip
the catalogue entirely for general tasks (literature scan, synthesis).
For code execution against the lab sandbox see the
`multica-claude-science-runtime` Skill.

## Step 3 — monitor and steer

The work runs in-band through the standard Multica agent runtime. To
observe progress, poll the issue or the inbox; do not invent a polling
loop of your own.

```sh
multica issue get "$ISSUE_ID"
multica issue comment list "$ISSUE_ID"
```

There is no `issue comments` (plural) command and neither verb takes
`--slug` / `--issue` — the id is positional.

To redirect (narrow scope, add a constraint, reject an early conclusion),
post a fresh comment — agents subscribed to the issue pick it up the same
way they pick up any user message.

## Step 4 — synthesize

**Report delivery is TOP-LEVEL (0.5.118).** The final research report is
the issue's answer — post it as a top-level comment on the issue
(`multica issue comment add "$ISSUE_ID" --content-file ./report.md`, no
`--parent`), exactly like the pythia/timesfm server-side report
writebacks. A report threaded under the trigger comment gets buried in a
collapsed reply chain and reads as a missing deliverable. Keep
conversational traffic (progress pings, questions, acks) in the trigger
thread with the usual `--parent` form.

When the agent signals completion (status change to "done" / "in review",
or a final summary comment), read back the issue, then write a short
coda comment so the reasoning chain stays in Multica:

```sh
multica issue comment add "$ISSUE_ID" \
  --content "## 结论摘要

…"
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

## Result envelope (0.3.40 v2 Claude Lab workbench contract)

When the research agent finishes a task, the Multica daemon writes the
final reply into `agent_task_queue.result` (jsonb). The Claude Lab
workbench (right-side strip under each lab issue) reads that jsonb and
renders structured deliverables inline — interactive charts,
prediction curves, fenced code, PNGs/SVGs, markdown reports — rather
than dumping one giant blob of text.

When you delegate to the `research` leader agent (or any of its
specialists), tell it explicitly to emit the structured envelope so
the workbench can render the outputs:

```json
{
  "output": "Scannable markdown report (heading + summary + conclusion).",
  "attachments": [
    {"kind": "interactive-chart", "name": "KS train vs test",
     "mime": "application/json",
     "data": {"schema": {"type": "scatter",
                         "x": {"field": "train_pkd", "label": "train pKd"},
                         "y": {"field": "test_pkd", "label": "test pKd"}},
              "data": [{"train_pkd": 5.2, "test_pkd": 5.1}]}},
    {"kind": "svg", "name": "decision-boundary.svg",
     "data": "<svg xmlns=\"http://www.w3.org/2000/svg\">...</svg>"},
    {"kind": "png", "name": "training-curve.png",
     "url": "/api/uploads/abc123"}
  ],
  "predictions": [
    {"round": 1, "scenario": "baseline",
     "probability": 0.42, "confidence": 0.71,
     "horizon": "week", "persona": "statistician"},
    {"round": 2, "scenario": "sensitivity",
     "probability": 0.58, "confidence": 0.66,
     "narrative": "Removing the LOMO-CV holdout shifts the distribution right.",
     "horizon": "week", "persona": "statistician"}
  ],
  "code_blocks": [
    {"language": "python", "filename": "ks.py",
     "code": "import scipy.stats..."},
    {"language": "javascript", "filename": "render-chart.js",
     "code": "const data = ..."}
  ]
}
```

`output` is required. `attachments[]` / `predictions[]` / `code_blocks[]`
are optional — older runs that emit only `output` still work, the
workbench falls back to markdown. But emitting the structured envelope
makes the lab view actually show charts and code instead of one
unreadable text blob. See
`apps/desktop/resources/claude-science/agents/research.txt` for the
canonical envelope contract the agent is told to follow.

### Field reference

The schema is enforced loosely — extra fields are preserved end-to-end
so the agent can attach metadata for the renderer or for later audit.
Fields the renderer actually keys on:

| Field            | Type        | Renderer behavior                                                                 |
|------------------|-------------|-----------------------------------------------------------------------------------|
| `attachments[].kind` | string  | Server-side allowlist (png, svg, interactive-chart, md, csv, json, txt, log). Unknown kinds are dropped before reaching the renderer. |
| `attachments[].data` | any     | Inline payload — base64 for png/jpg/webp/gif, JSON for interactive-chart, raw text for svg/md/csv/json/txt/log. Cap 4 MB per attachment. |
| `attachments[].url`  | string  | Server-stored reference (e.g. uploaded file). Goes through the renderer-side `safeHrefUrl` allowlist. |
| `predictions[].scenario`   | string | Rendered as a label on the Forecast chart legend.                       |
| `predictions[].probability` | number 0..1 | Y-axis value on the Forecast chart.                                |
| `predictions[].confidence`   | number 0..1 | Optional. Drives the confidence band shading on the chart.        |
| `predictions[].narrative`    | string | Optional. Tooltip text on the chart point.                          |
| `predictions[].round`        | int    | X-axis value on the Forecast chart (deliberation round).            |
| `predictions[].persona`      | string | Optional. Shown in the timeline pill ("statistician", "skeptic").   |
| `predictions[].horizon`      | string | Optional. Shown in the timeline pill ("week", "quarter", ...).      |
| `code_blocks[].language`     | string | Drives syntax highlighting (python / javascript / go / bash / ...). |
| `code_blocks[].filename`     | string | Optional. Shown above the fenced block.                             |
| `code_blocks[].code`         | string | Raw source. Renderer truncates display at ~2000 chars; full source remains in the agent result. |

## Research loop discipline (0.5.114)

Three standing rules for every run on a lab-bound issue:

1. **Freeze the target before you run.** Post a short admission comment
   FIRST — the claim under test, inputs, method, and budget — and do not
   revise it after results land. A reproduction that adjusts its target
   after seeing the result is not a reproduction.
2. **Deliver in the timeline.** The final synthesis is posted as a comment
   on the issue; the issue-first embed carries artifacts; the timeline
   carries conclusions.
3. **Expect a review.** A `critique` reviewer agent audits every completed
   run and posts a structured verdict (pass/warn/fail with findings).
   Address BLOCKING findings in a follow-up comment — either a correction
   or a reasoned rebuttal.

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
