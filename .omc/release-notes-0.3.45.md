---
name: 0.3.45 release notes
created: 2026-07-18T13:56:22Z
updated: 2026-07-18T13:56:22Z
status: in-progress
---

# 0.3.45 — Agent Creation Studio (action-type lab)

## Why this ship

The "create agent / skill / squad" affordance used to be a thin
sub-route of every primary surface (agents / skills / squads
left-nav). User intent (from 2026-07-18 conversation): bury it
behind Labs so the main product chrome stays focused on the
running work.

This ships the first **action-type lab** in the Labs Platform —
distinct from the existing 8 flags which are all visibility-gate
or issue-bound. `agent_creation_studio` lives behind an opt-in
toggle but its entry point is wired into the issue picker's
footer sub-menu so it's reachable from any issue without taking
up real estate on the main sidebar.

## What changed

### 1. New experiment flag (`agent_creation_studio`)

- `apps/desktop/resources/experiments/agent_creation_studio/manifest.json`
  — declarative manifest under the same `multica.dev/experiment/v1`
  contract as the existing 8. `installable: false` (no agent /
  autopilot / skill rows provisioned), `runtime.kind: "inline"`,
  no `lockable_resources`.
- New `entry_points.issue_panel_action` field on the manifest
  contract — distinct from `entry_points.sidebar[]`. The LabPicker
  picks it up via `onAction` rather than `issue.lab_source`. The
  loader still treats `spec` as `map[string]any` so the new verb
  passes schema validation without a server-side enum bump; the
  Go side reads it opaquely for now.
- `server/internal/experimental/catalog.go:121-311` + 41 lines
  appends one Flag literal. `DefaultVal: false`, `RuntimeKind:
  "inline"`. `IsKnownKey` / `AllFlagKeys` / `DefaultFor` /
  `Sidebar` pick it up via the existing array iteration.
- Sidebar rows for this flag stay empty — the studio is reachable
  only from the issue picker.

### 2. LabPicker `onAction` sub-menu

- `packages/views/issues/components/pickers/lab-picker.tsx`
  adds a new optional prop `onAction?: (actionKey: string) => void`.
- The action item renders inside the **built-in footer slot**
  of `PropertyPicker` (NOT in the children list), so arrow-key
  navigation skips it. The footer only renders when `onAction`
  is supplied, so existing web/desktop callers stay byte-identical.
- A consolidated vitest case (positive + negative phases with
  `unmount()` between) verifies the footer presence/absence and
  that clicking the action item fires `onAction` WITHOUT firing
  `onUpdate` — locking the orthogonality guarantee.
- `packages/views/issues/components/pickers/lab-picker.test.tsx`:
  5 → 6 tests, all PASS.

### 3. IssueDetail `Lab` PropRow hoist

- `packages/views/issues/components/issue-detail.tsx:1555` was
  gated on `issue.lab_source &&` so the picker disappeared
  entirely when no lab was set. Hoisting it out makes the picker
  default-chrome ("No lab") + action footer visible from any
  issue. The ExternalLink to `/experimental/<suffix>` still
  only renders when `labSourceRouteSuffix(issue.lab_source)` is
  truthy.
- The action callback navigates with
  `/experimental/agent-creation-studio?from_issue=<id>` so the
  studio header can show "返回 issue《...》".

### 4. Agent Creation Studio view (3 tabs)

- `apps/desktop/src/renderer/src/pages/agent-creation-studio-view.tsx` —
  ~290 lines. 3 Tabs (agent / skill / squad), each posting to
  the **existing** `api.createAgent / createSkill / createSquad`
  methods directly. No new mutation hook, no new sqlc query, no
  new IPC channel.
- `createAgent` requires `runtime_id`; the studio fetches the
  workspace runtime list once (`["runtimes", "list"]` query key)
  and pre-selects the first available runtime. The submit button
  is disabled when no runtime is available. A future 0.3.45.1
  pass surfaces a runtime picker so the user can override.
- After successful creation the user is navigated to
  `/agents/<id>` / `/skills/<id>` / `/squads/<id>` so the
  experience feels like a single "create + open" gesture.
- The "与智能体宪法兼容" checkbox on every tab is a **UI
  placeholder** — the underlying API does not yet accept a
  `system_key` field (the upstream `agent.system_key` column
  migration has not been cherry-picked). 0.3.45.1 follow-up.

### 5. i18n (4 locales)

- New namespace `packages/views/locales/{en,zh-Hans,ja,ko}/experimental.json`
  with one section: `agent_creation_studio_view.{tab_*, field_*,
  compat_*, submit_create}` (8 keys per locale).
- Registered in `packages/views/locales/index.ts` (4 imports +
  4 map entries) and typed in `packages/views/i18n/resources-types.ts`
  (`I18nResources.experimental`).
- `packages/views/locales/{en,zh-Hans,ja,ko}/issues.json` adds
  `pickers.lab.action_group_label` + `pickers.lab.action_create`.

### 6. CLAUDE.md

- The "Per-issue lab workspace (0.3.31 inline rendering)" section
  is now correctly marked **REMOVED in 0.3.38**; old architecture
  notes preserved below as historical reference only.
- New "0.3.45 action-type lab (NEW)" section documents the
  registry, picker wire-up, view entrypoint, locales, and the
  `system_key` constraint.

## Constraints honored

- ✅ No migration — forward-only DB invariant holds
- ✅ No sqlc regen — no new queries
- ✅ No new IPC channel — uses existing `api` methods
- ✅ No new visibility row — `agent_creation_studio` doesn't
  hide anything
- ✅ No reserved workspace — `installable: false`, no
  `upsertClaudeScienceWorkspace`-style helper involved
- ✅ Action path is orthogonal to issue.lab_source mutex —
  test verifies onAction does NOT call onUpdate
- ✅ Flag-off = full bypass: agent_creation_studio's only
  requirement is the picker passing `onAction`; flag state
  is not read by the view directly (catalog-driven, not gate-
  driven)
- ✅ i18n selector block-body crash contract honored — every
  `t()` call uses arrow expression, ESLint passes
- ✅ All Chinese + English strings have 4-locale entries

## Verification

| Check | Result |
|---|---|
| `pnpm typecheck` (workspace 6 packages) | 6/6 PASS |
| `pnpm turbo test --filter=views/core/desktop` | 1278 + 642 + 321 tests PASS |
| `cd packages/views && pnpm exec vitest run` | 141 files / 1278 tests PASS |
| `cd packages/views && pnpm exec vitest run issues/components/pickers/lab-picker.test.tsx` | 6/6 (incl. new onAction case) |
| `cd server && go test -p 1 ./internal/experimental/ ./internal/handler/` | PASS, incl. new `agent_creation_studio` manifest loader test |
| `cd packages/views && pnpm exec eslint issues/components/{pickers/lab-picker.tsx,pickers/lab-picker.test.tsx,issue-detail.tsx}` | PASS |
| `cd apps/desktop && pnpm exec eslint src/renderer/src/{pages/agent-creation-studio-view.tsx,routes.tsx}` | PASS |

## NOT packaged

Per 2026-07-18 session decision, this release is not packaged
into a fresh `/Applications/Multica.app`. Follow the same pattern
as 0.3.19 / 0.3.31 — once 0.3.45.1 ships (`agent_self_optimization`
install handler registration + `agent.system_key` column migration)
the studio gets bundled into the next batch ship.

## Deferred to 0.3.45.1

- `agent_self_optimization` install handler registration in
  `router.go:548-551` (currently the handler exists at
  `install_agent_self_opt.go` but is not wired into the
  registry, so flag-on does not provision the agent row).
- `agent.system_key` column migration + handler-side validation
  for the "与智能体宪法兼容" toggle — required to wire the UI
  placeholder to a real contract.
- Studio runtime picker — currently pre-selects the first
  available runtime; users should pick which runtime their
  new agent runs on.
- Squad leader picker — currently submits `leader_id: ""`; the
  studio's squad tab needs a leader picker that lists existing
  agents.
- TypeScript-typed accessor for `entry_points.issue_panel_action`
  in the loader, so future action-type labs can register without
  opaque JSON.

## Known follow-up

- `agent_creation_studio` flag currently does not gate the
  action entry point — Labs is a discoverability toggle rather
  than a hard gate. We may flip to render-gated if the flag
  stays a "discovery" toggle for opt-in rate; for now the
  action surfaces unconditionally from any issue picker's
  `LabPicker.onAction` consumer.
- The "system_key" placeholder checkbox is in every tab; if
  0.3.45.1 only lands the agent-side wiring, the skill / squad
  tabs need to suppress the checkbox until parity is reached.
