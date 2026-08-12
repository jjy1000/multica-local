---
name: ADR-0.5.6-catalog-cleanup
created: 2026-08-12T13:07:00Z
updated: 2026-08-12T13:07:00Z
type: decision
status: accepted
date: 2026-08-02
ship: 0.5.6
---

# ADR: Catalog cleanup — remove `agent_self_optimization` + `agent_creation_studio` literals (0.5.6)

## Status

Accepted — shipped in 0.5.6 (`b4b...` commit). Original PRs: 0.5.5 / 0.5.5.1 /
0.5.5.2 / 0.5.5.3 / 0.5.6 (5-ship sequence).

## Context

Two flags started as experiments (`agent_self_optimization` from 0.3.45.1,
`agent_creation_studio` from 0.3.45) and were promoted to product-level
resources in 0.5.5–0.5.5.1 (boot-provisioned, no flag gate). By 0.5.5.3 the
runtime was reachable without the flag, but the catalog literal still
existed, generating noise (Labs tab entry, install handler, visibility
seed, autopilot cron). The catalog entry was the last surface that still
required opt-in.

## Decision

**Delete the catalog `Flag` literal + `RegisterInstallHandler` entry for
both keys; migrate the SQL `experimental_resource_lock` /
`experimental_resource_visibility` rows to clean state; remove the flag
gate from the 4 product-level HTTP endpoints.**

Specific changes (all in `.omc/0.5.6-ship-2026-08-02.md`):

| Surface | Before | After |
|---|---|---|
| `server/internal/experimental/catalog.go` | 2 `Flag` literals | deleted |
| `server/cmd/server/router.go` | 2 `RegisterInstallHandler` rows | deleted |
| `server/internal/handler/agent_self_optimization.go` | 4 `experimentalFlagEnabled` gates | always-reachable + doc update |
| `server/internal/service/autopilot.go` | `agentSelfOptimizationAutopilotIDs` hidden-set gate | deleted (control moves to `autopilot.enabled`) |
| `server/migrations/237_product_level_cleanup.{up,down}.sql` | rows for retired sources | DELETE rows; down = `SELECT 1` (no-op, catalog literal gone) |
| `server/internal/experimental/{visibility,panic_context}_test.go` | tests pinning old behavior | renamed / updated to "removed" assertion |

## Six preserved product-level resources (100% retained)

1. `agent_creation_expert` agent (boot provision via `product_agent_creation_expert.go`)
2. `智能体优化专家` agent (self-opt service 0.3.45.1 boot wire)
3. 4 self-opt autopilot rows (`SkillOpt-Multica` daily × 2 + `智能体工程师团队 每3工作日` × 2)
4. `multica-creating-agents` builtin_skill (`server/internal/service/builtin_skills/multica-creating-agents/`)
5. `multica-lab-builder` builtin_skill (`server/internal/service/builtin_skills/multica-lab-builder/`)
6. LabPicker entry (issue-bound lab path; `lab_source='agent_creation_studio'` resolves to leader `agent_creation_expert`)

User control points unchanged: AssigneePicker / autopilot row enabled
toggle / builtin_skill auto-load / Labs tab toggle for OTHER flags.

## Alternatives considered

- **A) Keep the catalog literal with `DefaultVal=true`**. Rejected: the
  Labs tab entry would show up forever (even if the flag is now
  meaningless), and any future flag gate on this key would resolve
  `false` forever and silently kill the feature (see Known Stability
  Surface: `AgentTrustCorrectButton`).
- **B) Soft-delete the catalog literal via a feature toggle**. Rejected:
  one more toggle to maintain. The product-level resources are reachable
  without a flag, so the toggle adds no user value.
- **C) Move the keys into a separate `product_catalog.go`**. Rejected:
  single-source-of-truth (`Catalog` slice) is simpler; the Labs tab
  already filters by `IsKnownKey()`.

## Consequences

- **Positive**: Labs tab 2 fewer rows; install/rollback plumbing 2 fewer
  paths; future agents can't accidentally add a flag gate that resolves
  `false` and silently kills the product-level resources.
- **Negative**: `experimental_pref` rows for these keys accumulate per
  `user_id` (orphan rows harmless — never read by `ListExperimentalFlags`).
  The `experimental_resource_lock` CHECK still lists both values
  (constraint kept by design — fork pattern since 0.3.57).
- **Migration 237 down is `SELECT 1` (no-op)**: re-inserting the rows
  would create dangling entries pointing at non-existent catalog keys.

## References

- Ship log: `.omc/0.5.6-ship-2026-08-02.md`
- CLAUDE.md "Retired Features" section (locked by this ADR)
- Memory: `0.5.2-self-opt-ship-2026-08-01.md` (predecessor pattern)