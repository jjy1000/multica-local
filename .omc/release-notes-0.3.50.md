---
name: 0.3.50 release notes
created: 2026-07-20T00:20:00Z
updated: 2026-07-20T00:20:00Z
status: shipped
---

# 0.3.50 — `VIEW_LAB_SOURCES` → server catalog migration

## Why this ship

The 0.3.49 audit locked a 0.3.49-deferred 5-file refactor: split the
renderer-side hardcoded `VIEW_LAB_SOURCES` Set (in
`packages/views/issues/components/issue-labs-section.tsx`) into a
proper catalog field that flows end-to-end `Flag` struct → wire JSON
→ `ExperimentalFlag` TS type → consumer check.

Two consumers use it:

1. **`IssueLabsSection` (workbench link gate)** — controls whether
   the "在实验插件中查看" link appears on the issue detail sidebar.
2. **`IssueDetail` (timeline comment filter)** — controls whether
   the lab's agent deliverable comments stay in the plain issue
   timeline (default) or get hidden in favour of the lab's
   workspace-scoped view (workbench).

Pre-0.3.49.1 both consumers hard-referenced a 7-element hardcoded
Set. A future lab had to ship a TS release to opt in. 0.3.49.1
moved the source of truth into the catalog so a new flag declares
"owns a workbench view" in `Flag.HidesDeliverableInIssueTimeline`
once and inherits the choice everywhere.

## What changed

### Server — `Flag.HidesDeliverableInIssueTimeline` (catalog)

`server/internal/experimental/catalog.go`

- New `Flag.HidesDeliverableInIssueTimeline bool` field with the
  json tag `hides_deliverable_in_issue_timeline,omitempty`.
- Seeded `true` for the same 7 flags that were in
  `VIEW_LAB_SOURCES`:
  - `claude_science_lab`
  - `pythia_oracle`
  - `mythos_swarm`
  - `llm_wiki_bridge`
  - `code_canvas`
  - `agent_self_optimization`
  - `constitution_agent`
- All others (`chat_pin_ui`, `agent_creation_studio`) default to
  `false` via Go's bool zero value, so the field is omitted from
  the wire JSON.

### Server — wire mirror on `ExperimentalFlagResponse`

`server/internal/handler/experimental_flags.go`

- Added `HidesDeliverableInIssueTimeline bool` to
  `ExperimentalFlagResponse` and `f.HidesDeliverableInIssueTimeline`
  assignment in `ListExperimentalFlags`.
- JSON tag: `hides_deliverable_in_issue_timeline,omitempty`. Old
  builds ignoring the field stay backward compatible.

### Core — `ExperimentalFlag.hides_deliverable_in_issue_timeline?`

`packages/core/types/experimental.ts`

- New optional boolean field on `ExperimentalFlag`. Doc-comment cites
  the 0.3.49 audit's "two-set trap" so future readers do not propose
  the wrong migration.

### Core — schema passthrough

`packages/core/api/schemas.ts`

- Extended `ExperimentalFlagSchema` with optional `runtime_kind`,
  `sidebar_entries`, `hide_from_issue_lab_picker`, and
  `hides_deliverable_in_issue_timeline`. All `.optional()` so the
  schema stays forward-compat with older server builds that omit the
  later fields.

### Views — consumers switched to server-derived field

`packages/views/issues/components/issue-labs-section.tsx`

- `hasWorkspaceView` now reads
  `(flags ?? []).some(f => f.key === labSource && f.hides_deliverable_in_issue_timeline === true)`
  instead of `VIEW_LAB_SOURCES.has(labSource)`.
- `VIEW_LAB_SOURCES` export kept with `@deprecated` JSDoc so any
  out-of-tree import still resolves during the rollout window. New
  code MUST NOT reference it.

`packages/views/issues/components/issue-detail.tsx`

- `hideLabAgentComments` now reads `flagCatalog` from a local
  `useExperimentalFlags()` call in the `IssueDetail` scope (the
  existing one lives inside the `ExperimentalSection` subcomponent,
  which is a different scope).
- Dropped the `VIEW_LAB_SOURCES` import.

### Tests — 2 paths pinned

`packages/views/issues/components/issue-detail.test.tsx`

- `hides agent deliverable comments when the lab catalog declares hides_deliverable_in_issue_timeline`: with `hides=true` and `lab_source="claude_science_lab"`, the agent-authored "I can help with this" comment is filtered out while the member-authored "Started working on this" comment stays visible.
- `keeps agent deliverable comments when the lab catalog field is absent`: with the field missing (older server / pre-0.3.49.1 build), the agent deliverable remains visible — pins the forward-compat path so a server rollback to a pre-0.3.49.1 build does not silently strip deliverable threads.
- Added `listExperimentalFlags` to `mockApiObj` so the tests can wire the response.

## What did NOT change

- **No schema migration** — pure Go struct + TS type addition.
- **No IPC change** — channels unchanged.
- **Behaviour preservation** — every flag that was hardcoded in
  `VIEW_LAB_SOURCES` is now seeded with
  `HidesDeliverableInIssueTimeline: true` in the catalog, so the
  two consumers see the same `true` they did before. The
  migration is invisible to the running app.
- `VIEW_LAB_SOURCES` is kept exported (deprecated) so the rollback
  path still works — a TS roll back to 0.3.49 references the same
  Set, a server roll back to 0.3.49 still ships a field the new
  clients ignore.

## Verification

```
cd packages/core   && pnpm vitest run          # 63 files / 645 tests pass in 2.29 s
cd packages/views  && pnpm vitest run issues/ # 25 files / 244 tests pass in 9.66 s
cd server          && go test -race -count=1 ./internal/handler/ -run TestUpdateIssueLabSource
                                                 # 0 fails (0.3.47 batch parity preserved)
tsc --noEmit -p tsconfig.json   (core + views + desktop-node)
                                                 # 0 errors
go build ./...                                    # 0 errors
```

Note: a single pre-existing `TestExperimentalResourcesRoundTrip_InstalledThenHidden`
fixture (`server/internal/handler/experimental_resources_test.go`)
fails because `MULTICA_RESOURCES_DIR` is not set in this shell
environment — the install handler can't resolve the bundled
`claude_science` manifest. The fixture failure is **independent of
the 0.3.49.1 changes** (the install path fires before reaching any
new flag field), and the test was already failing in 0.3.48 and
0.3.49 ships — leaving the fix for a follow-up that wires
`MULTICA_RESOURCES_DIR` into the test harness.

## Diff size

| File | Change |
|------|--------|
| `server/internal/experimental/catalog.go` | +39 / -6 |
| `server/internal/handler/experimental_flags.go` | +18 / -6 |
| `packages/core/types/experimental.ts` | +10 / -0 |
| `packages/core/api/schemas.ts` | +16 / -0 |
| `packages/views/issues/components/issue-labs-section.tsx` | +18 / -2 |
| `packages/views/issues/components/issue-detail.tsx` | +16 / -4 |
| `packages/views/issues/components/issue-detail.test.tsx` | +67 / -0 |
| `apps/desktop/package.json` | version bump |

Net: **+185 / -18** across 8 files. Single atomic commit
`fix(labs): 0.3.49.1 — VIEW_LAB_SOURCES → catalog field migration`.
