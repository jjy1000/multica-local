---
name: multica-code-canvas
description: "Use when an issue is bound to the code_canvas lab. code_canvas is an internal P9 pilot — a stub subprocess wired through every Labs platform layer (manifest → catalog → registry → IPC → proxy). Off by default. The subprocess `apps/desktop/resources/code-canvas/run.sh` only responds on `/health`; every other path returns 404. Use this skill to confirm the lab is reachable, escalate real work to a real lab, and never invent behaviour the stub does not provide."
user-invocable: true
allowed-tools: Bash(multica *), Bash(curl *), Bash(sh *)
---

# Code Canvas (internal P9 pilot)

Code Canvas is a smoke-test lab. The whole point is to exercise the
manifest → catalog → registry → IPC dispatcher → proxy mount chain
end-to-end before a real lab lands. The on-disk binary
(`apps/desktop/resources/code-canvas/run.sh`) is a 30-line Python
`http.server` that only returns `200` on `/health` — it has no other
endpoint, no data plane, no model.

## When this skill applies

The skill is bound to the `code_canvas_worker` leader agent. The
auto-dispatch path in `assignDefaultLabAgentOnUpdate` rewrites the
issue's assignee to this agent when `issue.lab_source` is set to
`code_canvas`. If you are reading this skill from an issue, that is
why.

## What you can actually do

1. **Confirm the lab is up.** The subprocess exposes
   `/health`; when the manager is running you can reach it via the
   desktop proxy:
   ```sh
   curl -s http://localhost:8090/experimental/code-canvas/health
   # → {"status":"ok","stub":"code_canvas"}
   ```
2. **Check the flag is enabled** for the current user:
   ```sh
   multica experimental flags list 2>/dev/null
   ```
3. **Report lab status** back on the issue. The user is probably
   probing whether the platform chain works — give them a short,
   factual status (flag on/off, subprocess `/health` reachable, any
   5xx bursts since boot).

## What you cannot do

- The stub has no `/predict`, `/forecast`, `/chat`, `/model`, etc.
  Do **not** fabricate responses for any path other than `/health`.
- Code Canvas is not a research / coding / analysis lab. If the
  user's underlying request needs a real answer, **escalate** —
  recommend the right built-in lab (e.g. `pythia_oracle` for
  predictions, `mythos_swarm` for multi-perspective research,
  `llm_wiki_bridge` for KB authoring) rather than making something
  up.

## When to escalate

| User intent | Escalate to |
|---|---|
| Predictions / forecasts | `pythia_oracle` |
| Multi-perspective research / coding / analysis | `mythos_swarm` |
| Knowledge base authoring | `llm_wiki_bridge` |
| Interactive Claude session | `claude_science_lab` |
| Plain issue triage / comments | regular Multica flow (no lab) |

To escalate, comment on the issue telling the user which lab to
pick from the LabPicker — do NOT auto-flip `lab_source` yourself
(see server `assignDefaultLabAgentOnUpdate` for the leader-rewrite
contract).
