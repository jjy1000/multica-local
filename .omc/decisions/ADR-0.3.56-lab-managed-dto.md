---
name: ADR-0.3.56-lab-managed-dto
created: 2026-08-12T13:07:00Z
updated: 2026-08-12T13:07:00Z
type: decision
status: accepted
date: 2026-07-20
ship: 0.3.56
---

# ADR: `lab_managed` DTO marker — hide retired-lab agents/squads from regular pickers

## Status

Accepted — shipped 0.3.56. Audit methodology + 6 missed surfaces
catalogued in `0.3.56-lab-managed-marker-2026-07-20.md`.

## Context

When an `experimental_resource_visibility` row exists for a flag, ALL
agents/squads owned by that flag must be hidden from regular
selection surfaces (assignee picker, project lead picker, quick-create
issue, squad member picker, issues-header filter, issue-detail
subscribers, etc.). Pre-0.3.56 the catalog-level `HideFromIssueLabPicker`
flag was the only layer — but it only hid the lab itself, not its
child resources. Users could still pick e.g. `mythos_prelude` directly
from AssigneePicker, bypassing the lab entirely.

## Decision

**Stamp `lab_managed?: boolean` on every `Agent` and `Squad` DTO,
derived from `experimental_resource_visibility` set-contains (NOT a
column). Every selection surface MUST filter on this marker.**

### Server side

- `server/internal/handler/agent.go::ListAgents` and
  `server/internal/handler/squad.go::ListSquads` stamp `lab_managed`
  via `filterLabsHiddenByDefault` (5 inline filter blocks → 1 generics
  helper in `labs_visibility_filter.go`).
- Schema: `lab_managed: z.boolean().optional().default(false)` in
  `packages/core/api/schemas.ts`.
- Types: `packages/core/types/agent.ts` and `squad.ts`.

### Renderer side — selection vs display

Every `agentListOptions` / `squadListOptions` consumer is classified as
**selection** (must filter) or **display** (must NOT filter, since
comment authors and member names need full lists).

Selection surfaces (MUST pass `lab_managed: false` to query hook OR
filter the resulting list):

1. `AssigneePicker`
2. Project lead picker
3. Quick-create issue
4. Squad member picker
5. Issues-header filter chips
6. Issue-detail subscribers list

Display surfaces (must NOT filter): `useActorName`, comment author
rendering, member name lookup.

## Audit methodology

When adding a new agent/squad picker, enumerate all
`agentListOptions` / `squadListOptions` consumers, classify each as
*selection* vs *display*. Selection surfaces without `lab_managed`
filtering are bugs.

Past audit (0.3.56) caught 6 missed surfaces in one pass. Re-audit on
any new picker.

## UX marker

Set `disabled` on the trigger + render a Tooltip with
`pickers.assignee.lab_managed_tooltip` (4 locales: en / zh-Hans / ja / ko).

## Alternatives considered

- **A) Add a `lab_managed` column to `agent` and `squad` tables**.
  Rejected: redundant — the visibility row already encodes the
  membership; a column would need backfill and a sync mechanism on
  every visibility change.
- **B) Filter only at the catalog level (`IsKnownKey` + visible filter
  on lab agents)**. Rejected: catalog-level filter is too coarse —
  `useActorName` shares the same query and needs full lists for
  display paths.
- **C) Server-side hard 400 on `lab_managed=true` agents**. Rejected:
  display paths need to render them (comment authors, etc.); the
  distinction is selection vs display, not server enforcement.

## Consequences

- **Positive**: Users can never pick a `mythos_*` agent directly,
  preserving the "lab owns the roster" semantic. Display paths still
  show full names.
- **Negative**: Selection surfaces must explicitly opt in to filtering.
  A new picker added without filtering is a silent bug (picks the
  hidden agent without UI feedback).
- **Do NOT remove the `ListAgents` lab_managed stamp even if
  `useActorName` shares the same query** — display paths need full
  lists. Cleanest pattern: `useActorName` calls a
  `with_archived=true, include_lab=true` variant while pickers call the
  default-filtered variant.

## References

- Ship log: `.omc/release-notes-0.3.56.md`
- Memory: `0.3.56-lab-managed-marker-2026-07-20.md`
- CLAUDE.md "Active Contracts #4" section