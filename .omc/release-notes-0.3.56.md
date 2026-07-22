---
name: release-notes-0.3.56
created: 2026-07-20T13:56:41Z
updated: 2026-07-22T10:24:00Z
status: complete
---

# Multica 0.3.56 — lab agents/squads no longer selectable standalone

## Problem
Lab-internal agents and squads (Claude Science `research/biology/physics/ml`
+ its 5 squads, the Mythos 5-agent roster + `Mythos Swarm` squad, the
`智能体优化专家` / constitution actors) leaked into the regular frontend as
normal, standalone-selectable rows — visible in the Squads/Agents browse
pages and pickable in every assignee / lead / member / subscriber picker —
even though they are Labs infrastructure that is only meant to run when the
user selects the owning lab on an issue. This violated the lab contract
"lab infra is invisible and not standalone-usable in the frontend".

## Root cause
`labs_visibility_filter.go::filterLabsHiddenByDefault` early-returns the
list UNFILTERED whenever the flag is ON ("ON = show everything"), so an
enabled lab's seeded rows flowed straight into `ListAgents`/`ListSquads`
and from there into every picker/browse surface fed by `agentListOptions`/
`squadListOptions`.

## Why not "just filter them out of the list endpoints"
`useActorName` (`packages/core/workspace/hooks.ts`) — the sync getter that
20+ surfaces use to render an actor's name/avatar by id (list-row assignee,
comment authors, picker triggers, hover cards) — reads the **same**
`agentListOptions`/`squadListOptions`. Dropping a lab actor from those lists
would blank its name everywhere it is referenced by id. (The renderer
already calls these options with `include_archived=true`, which the server
routes to the **unfiltered** query — so lab actors resolve correctly for
display today; see "Note on the display path" below.)

## The fix — `lab_managed` marker (no migration)
Keep lab actors **in** the list payload, tag them, and let the UI hide/grey
them per surface:

- **Server**: new sqlc query `ListLabManagedResourceIDs(resource_type)` =
  `SELECT resource_id FROM experimental_resource_visibility WHERE
  resource_type=$1` (existence-based; ignores the `hidden` column and flag
  state). `labManagedSet()` helper (fail-open). `ListAgents`/`ListSquads`
  stamp `lab_managed` per row. List responses only.
- **Types**: `lab_managed?: boolean` on `Agent` + `Squad`; `SquadSchema`
  gains `lab_managed: z.boolean().optional().default(false)` (+ test).
  (`listAgents` has no zod schema — plain fetch — so the field passes
  through; `listSquads` uses `SquadSchema.loose()`.)
- **Browse pages — HIDE**: `agents-page.tsx` + `squads-page.tsx` rebind the
  query result (`allX.filter(x => !x.lab_managed)`) so the list, the scope
  counts and the header badge all drop lab rows at one chokepoint.
- **Issue assignee picker — GREY + DISABLE** + tooltip
  (`pickers.assignee.lab_managed_tooltip`, 4 locales) + `FlaskConical`
  trailing icon, for both agents and squads. (This is the lab/assignee-mutex
  surface, where the explanation matters most.)
- **Every other standalone selection surface — HIDE** via option-source
  filters (never touching the arrays that feed a `.find()` for the current
  value, so legacy current-values keep resolving): `autopilots/.../agent-
  picker.tsx`, `projects/project-lead-picker.tsx` + `project-detail.tsx`
  (the inline lead filter is a separate list), `modals/create-squad.tsx`,
  `modals/create-project.tsx`, `modals/quick-create-issue.tsx`, `chat/
  chat-window.tsx` (filteredMine/filteredOthers, NOT `availableAgents`),
  `issues/issues-header.tsx` (assignee filter dropdown), `issues/issue-
  detail.tsx` (the subscriber popover; widened its narrowed `agents` prop
  type to carry `lab_managed`), `squads/squad-detail-page.tsx` (the add-
  member popover's `availableAgents`).
- **Data**: removed the leftover test row `fixture-research-squad` (+ 1
  member + 1 visibility row) from the dev DB. No repo reference → won't
  recur; no migration needed.

The final selection-surface audit (every `agentListOptions`/
`squadListOptions` consumer classified as selection-vs-display) reports
**zero** selection surfaces without a `lab_managed` gate; display-only
consumers (profile cards, hover cards, runtime pages, `useActorName`) are
left untouched so lab actors keep resolving by id.

## Note on the display path (audit-discovered, no regression)
The renderer's `agentListOptions` passes `include_archived=true`, which the
server routes to the **unfiltered** `ListAgents` (the same branch as the
`include_all` opt-out). So the renderer's list — and therefore `useActorName`
— already **includes** lab agents (now stamped `lab_managed=true`), which is
why auto-assigned lab leaders and lab-agent comment authors render correctly
and why the browse/picker hides above are what enforce the contract on the
client. A raw `/api/agents` call *without* `include_archived` returns the
server-filtered set (lab agents absent) — that path is **not** what the UI
uses; an early probe against it falsely suggested a "blank lab-assignee"
regression that does not exist. No code change was needed for it.

## Verification
`gofmt` / `go build` / `go vet` ✓ · `sqlc generate` ✓ ·
`go test -race ./internal/handler` ✓ · core+views `typecheck` ✓ ·
`packages/core` schemas test (incl. new marker test) ✓ ·
`packages/views` vitest for all touched areas (43 files / 309 tests) ✓ ·
`pre-update-snapshot.sh` ✓ · `bundle-cli` ✓ · `electron-vite build` ✓ ·
`electron-builder --mac --dir` ✓ · cold start: 5432 + 8090 listen,
`/health` `{"status":"ok"}` · authenticated probe: `/api/squads` returns the
6 lab squads with `lab_managed=true` and user squads `=false`;
`/api/agents?include_archived=true` returns the 10 lab agents with
`lab_managed=true` · installed `app.asar` carries the marker + 4-locale
tooltip · row parity workspace=1 / agent=91 (stable) / issue=209 / squad=18
(`fixture-research-squad` confirmed absent).

## Out of scope / follow-ups
- A dedicated **unfiltered actor source** for `useActorName` is *not* needed
  today (the `include_archived=true` path already supplies it); revisit only
  if a future change makes the renderer's list filtered.
- `pythia_runtime` / `code_canvas_worker` are not installed in this
  workspace, so they have no rows to mark (handled correctly — absence is
  fine).
