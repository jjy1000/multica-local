---
name: autoscientists-long-running
description: Long-running multi-agent experimentation orchestrator distilled from the `AutoScientists` arXiv preprint (Gao / Fang / Zitnik, 2026). The skill implements the paper's 3-analyst + 6-experiment team pattern over Multica's autopilot + agent_task_queue infrastructure. Used when the user tags an issue with `lab_source=claude_science_lab` AND sets a long-running experiment label — the lab spins up an autopilot that drives a `research` agent through 50–100 iterations while two analyst sub-agents watch the shared state, recommend pivots when the champion stalls, and write the model card at the end. Hidden from main picker; lab-only.
category: orchestration
allowed-tools: [Read, Write, Edit, Bash]
---

# AutoScientists — Long-running experiment orchestrator

Distilled from the AutoScientists preprint (Gao, Fang, Zitnik 2026,
`arXiv:2026.AutoScientists`). The lab treats the paper as a **team
topology**, not a model — it's the orchestration pattern that lets a
small workspace stay productive when an experiment outlasts one
prompt context.

## When to Use

The user creates an issue with `lab_source=claude_science_lab` AND one
of these flags (encoded in the issue body, parsed by the issue-create
hook):

- `[autoscientists]` — turn on the multi-agent loop for this issue
- `[long-running]` — admit this is a 30-iteration+ job, not a single prompt
- `[champion-tracking]` — track per-iteration best metric and emit a
  model card at the end

When all three flags are set the autopilot `claude-science-autopilot`
takes over and runs the team pattern below.

## Team Topology (mirrors the paper)

| Role | Multica mapping | Count |
|---|---|---|
| Analyst agent | A second `research` agent, prompt tuned for log reading | 3 |
| Experiment agent | A `ml` agent (or `physics`/`biology` per domain) | 6 |

All 9 share the issue's working directory. The orchestrator (this
skill) is the deterministic monitor that the paper describes — it is
the *only* code path with write access to the shared state file.

## Shared State (the lab's white-board)

The orchestrator writes `~/.multica/labs/claude-science/<issue-id>/state.json`:

```json
{
  "champion": { "iteration": 17, "metric": 0.978, "params": {...} },
  "log": [ { "iter": 17, "agent": "ml#2", "params": {...}, "result": 0.978 } ],
  "dead_ends": [ "PPR-personalised", "LightGBM-on-features" ],
  "team_queue": [ { "proposer": "research#1", "experiment": {...}, "priority": 7 } ]
}
```

Each iteration:
1. **analyst passes** — analyst agents read the log + champion, write
   new proposals into `team_queue`.
2. **experiment passes** — experiment agents claim one proposal each,
   run it, append to `log`, possibly update `champion`.
3. **stall check** — if `champion.metric` did not improve for 12
   consecutive iterations, the orchestrator marks the current
   direction as `dead_end` and triggers a **re-organize pass**: the
   analysts re-rank proposals, and 2 experiment agents rotate to the
   next most-promising direction.
4. **champion pollution guard** — if a new "improvement" is within
   0.002 of the champion's metric, the orchestrator requires the
   agent to re-run with a different seed and average the result
   before accepting the update.

## Model Card (output)

When the issue hits the iteration cap (default 50, configurable via
`[iterations=N]` flag) or the user closes the issue, the orchestrator
emits a model card at `~/.multica/labs/claude-science/<issue-id>/model-card.md`,
matching the paper's Figure 2 structure:

- Model details (params, training data, runtime)
- Intended use
- Evaluation metrics + per-seed variance
- Ablation: which team-member contributed what improvement
- Failure modes + safe-failure observations
- Reproduction: exact `state.json` snapshot

A `comment` of `type="model_card"` is then written to the issue so
the user sees it inline in the issue detail.

## Why This Skill Lives in the Lab (not the Main Agent Picker)

The team pattern is expensive (9 concurrent agent slots, 50+
iterations). Surfacing it in the main picker would let users trigger
it casually, costing compute + tokens. Hiding it behind the
`claude_science_lab` install + the `[autoscientists]` issue flag is
the contract: **the lab's `research` leader can launch it on its
own initiative; the user has to opt in explicitly via the issue body
flag for direct runs.**

## Failure Modes

- 12-iteration stall with no `dead_end` candidates → log a warning,
  continue, don't crash.
- Experiment agent crashes mid-run → orchestrator marks the iteration
  `crashed` in the log; analyst gets one re-attempt with a fresh seed.
- Workspace offline / daemon died → orchestrator pauses; resumes from
  `state.json` on the next daemon poll (resume contract is the same
  as the existing `mythos supervise` goroutine).

## Related Skills

- `multica-claude-science-runtime` (Python executor) — runs each
  experiment agent's code in an isolated `python3 -I` session.
- `mythos_swarm` lab's `mythos/runner.go` — closest existing
  long-running pattern in Multica; we mirror its `ResumeSupervision`
  semantics for daemon-restart recovery.