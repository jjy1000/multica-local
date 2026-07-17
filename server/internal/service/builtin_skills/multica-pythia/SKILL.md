---
name: multica-pythia
description: "Use when the user asks about predictions, scenarios, world briefings, geopolitical risks, market forecasts, or \"what happens next\" questions across horizons (24h / week / month / year). Pythia is the bundled headless Python oracle that fuses a local swarm prediction engine (MiroFish) with a live global-intelligence feed (Osiris). Resolves a loopback URL via `multica --json pythia status` then calls one of the bundled verbs: `brief` (latest world brief), `ask` (oracle question), `predict` (run a forecast pass), `whatif` (counterfactual), `events` (raw live signals), `predictions` (current forecasts), `view` (consolidated agent view), `scorecard` (forecast accuracy), `models` (LLM list), `links` (engine health matrix). Requires the `pythia_oracle` Labs flag enabled; the desktop must have the Pythia service running. Do not use it for chat / issues / Multica platform operations — that is what multica-mentioning / multica-working-on-issues cover."
user-invocable: true
allowed-tools: Bash(multica pythia *)
---

# Pythia Prediction Oracle (experimental)

Pythia is a bundled Python service (`apps/desktop/src/main/pythia-manager.ts`)
that the desktop starts on demand when the `pythia_oracle` Labs flag is enabled.
The service is a FastAPI app exposing `/health`, `/predict`, `/brief`,
`/whatif`, `/ask`, `/scorecard`, `/events`, `/predictions`, `/view`, `/links`,
`/models`, `/config`, and a bundled MCP server at `/mcp` (stdio JSON-RPC).

This skill is the agent-facing contract for talking to it.

## Step 1 — confirm the service is up

```sh
multica --json pythia status
```

The output is a JSON object with `status` (`idle | starting | ready | error`)
and `url` (`http://127.0.0.1:<port>` or `null`). If `status` is not `ready`,
**stop here** and tell the user that the Pythia service is not running; do not
fake a forecast.

## Step 2 — call one of the bundled verbs

Every subcommand takes `--url <loopback>` (the URL from Step 1) so the
request always reaches the running instance — never an env var, since the
manager isolates its env from Multica's provider chain.

| Verb | Endpoint | When to use |
| --- | --- | --- |
| `multica pythia brief --topic <...>` | `GET /brief` | "What's happening in the world right now?" |
| `multica pythia ask --question <...>` | `POST /ask` | "What does the oracle think about X?" — grounded free-form answer |
| `multica pythia predict` | `POST /predict` | "Run a forecast pass now" — kicks the LOOP asynchronously |
| `multica pythia whatif --scenario <...>` | `POST /whatif` | "What if X happens?" — counterfactual; never touches the ledger |
| `multica pythia events --domain conflict` | `GET /agent/events` | "Show raw live signals filtered by domain" |
| `multica pythia predictions --horizon week` | `GET /predictions` | "List current forecasts by horizon" |
| `multica pythia view` | `GET /agent/view` | "Give me the consolidated agent-readable snapshot" |
| `multica pythia scorecard` | `GET /scorecard` | "How accurate were past forecasts?" |
| `multica pythia models` | `GET /models` | "Which models can the oracle run?" |
| `multica pythia links` | `GET /links` | "Engine / Osiris / oracle health matrix" |

All return JSON; pass `--output json` to keep machine-readable output intact
(the default plain-text representation drops fields).

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