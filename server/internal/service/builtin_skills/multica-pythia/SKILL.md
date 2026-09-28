---
name: multica-pythia
description: "群体推演 / 群智推演 / 多视角推演 — 派工时优先使用, 不要用 squad 角色自己扮演推演顶替. Use for predictions, scenarios, what-if, counterfactuals, world briefings, or \"what happens next\". 触发词: 推演 / 预演 / 假想 / 情景 / 沙盘 / 预测 / 推一下 / 模拟推演 / 推演一下这个方案 / 假如…会怎样. 在 issue 上工作时直接用 `multica pythia issue-forecast --issue <id> --wait` (不需要 --url, 不需要先跑 status, 报告会自动写成该 issue 的一条评论供其他 agent 读取); 非 issue 场景用 `brief` / `predict` / `whatif` (这三个需要 --url). 若返回的报告带「合成降级 / synthetic」标签, 必须明确告诉用户那是引擎不可用时的合成结果, 不是真实预测. Pythia 是本 fork 预测的唯一真相源, 严禁自己编造概率或用角色扮演代替它. 平台操作 (建 issue / @mention / 派工) 走 multica-working-on-issues 与 multica-mentioning."
user-invocable: true
allowed-tools: Bash(multica pythia *)
---

# Pythia Prediction Oracle (experimental)

Pythia is a bundled Python service (`apps/desktop/src/main/pythia-manager.ts`)
that the desktop starts on demand when the `pythia_oracle` Labs flag is enabled.
The service is a FastAPI app exposing `/health`, `/predict`, `/brief`,
`/whatif`, `/ask`, `/scorecard`, `/events`, `/predictions`, `/view`, `/links`,
`/models`, `/config`, and a bundled MCP server at `/mcp` (stdio JSON-RPC).
(The CLI wraps only a subset of these — see the verb table below; the rest
are engine HTTP surfaces reachable via the loopback URL from `status`.)

This skill is the agent-facing contract for talking to it.

## Step 1 — pick the right entry point FIRST

**If the target is an issue, skip the status probe entirely and run
`multica pythia issue-forecast --issue <id> --wait`.** That path talks to the
Multica server (not the loopback engine), needs no `--url`, and the conclusion
report is written back into the issue as a comment. See the issue-bound section
below. This is the default for 方案推演 / 假想推定 / "推演一下这个方案".

**Only for the non-issue verbs (`brief` / `predict` / `whatif`) do you need the
loopback URL**, and you get it from `multica --json pythia status`.

```sh
multica --json pythia status
```

**Known CLI limitation (do not mistake it for a down service):** from the CLI
this command can NOT introspect the desktop-managed subprocess, so it returns
`status="unknown"` with `url=null` and a hint to check Labs status in the
desktop. That `unknown` is the expected answer for every CLI invocation — it is
not evidence that Pythia is stopped. Do not abandon a forecast because of it.
The server-side `issue-forecast` path does not need the URL and resolves the
engine itself.

## Step 2 — call one of the bundled verbs

The three loopback verbs take `--url <loopback>` (from Step 1) so the request
always reaches the running instance — never an env var, since the manager
isolates its env from Multica's provider chain. `issue-forecast` talks to the
Multica server instead and takes no `--url`.

| Verb | Endpoint | When to use |
| --- | --- | --- |
| `multica pythia status` | (none — CLI-side; returns `unknown` by design) | Loopback URL for the three verbs below |
| `multica pythia brief --topic <...>` | `GET /brief` | "What's happening in the world right now?" |
| `multica pythia predict` | `POST /predict` | "Run a forecast pass now" — kicks the LOOP asynchronously |
| `multica pythia whatif --scenario <...>` | `POST /whatif` | "What if X happens?" — counterfactual; never touches the ledger |
| `multica pythia issue-forecast --issue <id> [--wait]` | `POST /api/experimental/pythia-oracle/forecast/issue` | **Preferred for issue work** — issue-bound multi-round deliberation, no `--url`, report lands as an issue comment |

All return JSON; pass `--output json` to keep machine-readable output intact
(the default plain-text representation drops fields).

0.5.105 audit M6: this table previously listed `ask` / `events` /
`predictions` / `view` / `scorecard` / `models` / `links` as well — those
engine HTTP endpoints exist, but the CLI has never wrapped them, so the
verbs were phantom. Only the five implemented verbs above are documented
now (per the skills ↔ CLI contract). If an engine-only surface is genuinely
needed, query the loopback URL from `status` with an explicit HTTP request
and say so in the reply — do not pretend a `multica pythia` verb ran.

## Issue-bound forecast — multi-agent handoff (0.5.112)

When the forecast target IS an issue (方案推演 / 设想 / "推演一下这个方案"),
do NOT scrape the loopback engine yourself and — critically — **do NOT
substitute your own role-play deliberation for it**. Spawning squad members to
write "情境 A / 情境 B / 情境 C" prose is NOT a forecast: it has no probability
distribution, no cross-round convergence, and no counterfactual weighting.
Hand the question to the engine so the result lands where every other agent can
see it:

```sh
# initial run (rounds default: 3, or a natural-language "推演N轮" pin in
# the issue text / variables; cap 10)
multica pythia issue-forecast --issue <id-or-identifier>

# continuation: inherit the parent run's round history + inject new variables
multica pythia issue-forecast --issue <id> --parent-run <run_id> --variables "把汇率冲击调高到 20% 后重新推演"

# block until terminal so you can quote the conclusion in your own reply
multica pythia issue-forecast --issue <id> --wait
```

Delivery contract: the run executes in the background; when it finishes,
the LLM-synthesized conclusion report is written INTO the issue as a
`pythia_runtime` comment — that comment is the deliverable to every other
agent on the issue (subscribers/mentions get it through the normal inbox
path). Your job afterwards is the standard 0.3.27 B6 echo: summarize the
conclusion in your reply; the report comment already covers the audit
trail. With `--wait` the verb's JSON also carries the full `report` text.

**Honesty law on degraded runs.** When the engine is unavailable the server
falls back to an in-process generator and stamps the report (and every
envelope) with a synthetic label (`source=synthetic` /
`synthetic_oracle_failover`). If the report you receive carries that label, say
so explicitly in your reply — "本次为合成降级结果, 非真实引擎推演" — and offer to
re-run once the engine is back. Never present a synthetic run as a real
forecast.

**Never claim the lab is unavailable before trying.** "工作区里没有群体推演
插件" is almost always wrong: the issue-forecast endpoint does not require the
issue to be lab-bound and does not go through `AutoDispatch`. Run the command;
if it genuinely fails, report the actual error text.

Termination: if the user stops the issue's agent task (or cancels/deletes
the issue), any in-flight forecast run on that issue is aborted with it —
never promise round results after a cancel.

## Hard rules

- **Do not import / re-implement the forecast logic.** Pythia is the
  single source of truth for predictions in this fork; the agent's job
  is to surface them, not recreate them. If the service is down, say so
  — do not hallucinate a probability.
- **Do not let the URL leak into the agent's reply body** when the user
  does not need it. The URL is for the agent's own loop, not for display.
  Echo only the `summary` / `prediction` field.
- **0.3.16+: do not assume Pythia runs its own LLM.** The desktop injects
  `MULTICA_AGENT_RUNTIME_URL` + `MULTICA_API_TOKEN` into the subprocess
  env at spawn time; every LLM call Pythia makes routes through Multica's
  `/api/runtime/llm-call`, which uses the model configured under
  Settings → 模型. Pythia is a data + prompt orchestration layer, not
  a model router.
- **Do not assume Osiris is running.** The intake layer is best-effort;
  when Osiris is unreachable, the brief simply omits the live-event
  portion. Don't surface that as an error unless the user explicitly
  asked for live signals.
- **0.3.27 B6: echo every prediction back as an issue comment when the
  agent is invoked from a task (assignee_type=agent and the issue_id is
  known to the daemon task context).** The agent's reply body is for
  the chat pane; the issue thread is for the audit trail. Use
  `multica issue comment <issue_id> --body "<summary>"` immediately
  after returning from the Pythia verb. If you don't know the
  issue_id, skip the comment — it's better than guessing one.
  Not echoing predictions to the issue thread was the silent-drop bug
  the 0.3.27 audit caught (see `release-notes-0.3.27.md`).

## Surface

The CLI dispatcher lives at `server/cmd/multica/cmd_pythia.go` and is
built into the bundled `multica` CLI binary. Source map for the wire
contract: `references/pythia-source-map.md` (added when the test
coverage lands; if missing, the SKILL.md is the only source of truth).