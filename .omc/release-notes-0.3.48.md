---
name: 0.3.48 release notes
created: 2026-07-19T23:35:00Z
updated: 2026-07-19T23:35:00Z
status: shipped
---

# 0.3.48 — mythos supervise polling race + getIssue schema + manager-factory parity

## Why this ship

A 5-surface audit of the 0.3.47 lab-class code base surfaced two
correctness bugs and one catalogue-drift gap. All three were caught in
production code that the 0.3.47 ship did not exercise directly, so the
release notes from 0.3.47 flagged them as "no risk" — but the audit
verified they were real regressions of contracts the previous ships
established:

| Sev | Where | Impact |
|-----|-------|--------|
| HIGH | `mythos-supervise-state` poll race | Server-side `superviseLoop` ticks every 30 s; the client polled at the same 30 s cadence. A fetch that landed just after a server tick would miss the next tick entirely. The Mythos enhance panel could lag an entire supervision snapshot per fetch. |
| HIGH | `api.getIssue` no parseWithFallback | Older server builds missing `lab_source` / `lab_mode` / `assignee_type` propagated `undefined` into `LabAgentFromIssue` and `IssueLabsSection`. A `false === undefined` branch failed silently, leaving the lab badge without a leader. |
| MEDIUM | `agent_creation_studio` missing from `staticFlagDescriptors` | The 0.3.45 action-type lab hit "no handler registered" on cold boot before `loadFlagDescriptors()` resolved the `/api/experimental-flags` fetch. |

## What changed

### HIGH — `MythosEnhancerSupervisePanel` polling rewrite

`packages/views/issues/components/issue-labs-section.tsx`

- Deleted `useState(30_000)` + the `useEffect(() => { setPollMs(10_000); setTimeout(...) })` hack that rode on `tick.isSuccess`.
- Added `LIVE_MYTHOS_PHASES = new Set(["preparing", "planning", "supervising"])`.
- Replaced both `runsQuery.refetchInterval` and `stateQuery.refetchInterval` with the canonical `(query) => isLive(...) ? 5_000 : 60_000` shape (the same lineage 0.3.45.7 → 0.3.45.9 used for `autopilot_run`).
  - `runsQuery`: `data?.length > 0 ? 5_000 : 60_000` — once a run materialises, poll at 5 s; while still in `preparing` fall back to 60 s so we don't hammer the server.
  - `stateQuery`: `LIVE_MYTHOS_PHASES.has(snapshot.phase) ? 5_000 : 60_000` — live phases (server-tick-aligned) get the second-line 5 s guarantee; terminal (`done` / `aborted` / `degraded`) fall back to 60 s.
- The unused `useEffect` import is also gone (no other consumer in the file).

Why this is correct now (vs. before): the server-side `superviseLoop`
writes `mythos_run.supervision_state` every 30 s. With a 5 s client
poll and a 30 s server tick, the client now catches every tick
within at most 5 s; with the old 30 s client poll, the client could
miss two consecutive ticks if its fetch landed just after a server
write.

### HIGH — `api.getIssue` now routes through `parseWithFallback`

`packages/core/api/client.ts` + `packages/core/api/schemas.ts` +
`packages/core/api/client.test.ts`

- **`IssueSchema` widened** with `lab_source: z.string().nullable().default(null)` and `lab_mode: z.enum(["sole", "enhancer"]).nullable().default(null)`. These two fields exist on the `Issue` TypeScript interface (since 0.3.22 lab lock row + 0.3.31 mythos dual-mode) but were never added to the schema, so any future `parseWithFallback` call would silently strip them.
- New `EMPTY_ISSUE` sentinel — typed `Issue` with inert enum members (`status: "backlog"`, `priority: "none"`, `creator_type: "member"`) so downstream consumers can rely on the typed union without crashing on `undefined`. `id`, `workspace_id`, `created_at` are empty strings as the universal "no data" signal.
- `api.getIssue(id)` rewrite — now `parseWithFallback(raw, IssueSchema, EMPTY_ISSUE, { endpoint: "GET /api/issues/:id" })`. Sister methods (`listIssues`, `listComments`, `listTimeline`, `getAttachment`) already use this shape — `getIssue` was the lone outlier (0.3.47 LOW #4 known gap).
- 3 new `getIssue` unit tests covering: well-formed / drift-prone (older build omitting `lab_source`) / JSON-shaped-but-invalid-payload (server drift).

### MEDIUM — `agent_creation_studio` joins `staticFlagDescriptors`

`apps/desktop/src/main/experimental/manager-factory.ts`

- Added `{ flagKey: "agent_creation_studio", kind: "inline", label: "experimental_agent_creation_studio" }` as the 9th entry.
- Updated the comment block to say "8 + 0.3.45's `agent_creation_studio` action-type lab = 9".

Why this matters even though `loadFlagDescriptors()` merges any
remote-only additions post-boot: the renderer can mount a Labs tab
_before_ the first `/api/experimental-flags` fetch resolves. During
that window the static list is the only source of truth for the IPC
dispatcher — and `LabPicker.onAction` (`router.push("/experimental/agent-creation-studio?...")`)
exercises an IPC verb that goes through that dispatcher.

## What did NOT change

- **No schema migration** — the database side already carries `lab_source` (mig 155) and `lab_mode` (mig 157). The 0.3.48 schema additions are renderer-side validation only.
- **No IPC change** — same channels, same verbs.
- **No new flag** — `agent_creation_studio` (added 0.3.45) was already in the server catalog; this just aligns the desktop-side static list with reality.

## Verification

```
cd packages/core && pnpm vitest run api/   # 27 passed
cd packages/views && pnpm vitest run       # 141 files / 1280 tests pass in 72 s
cd server && go test -race -count=1 -timeout 180s ./internal/handler/  # 16.0 s, all green
tsc --noEmit -p tsconfig.json              # core + views: 0 errors
tsc --noEmit -p tsconfig.node.json --composite false  # desktop node-side: 0 errors
```

The desktop renderer-side `tsc --noEmit -p tsconfig.web.json` emits
two pre-existing TS6307 errors about `src/shared/...` files (daemon
hooks, pageview-tracker) — both predate this ship by 3+ versions. Not
in scope for 0.3.48.

## Diff size

| File | Change |
|------|--------|
| `apps/desktop/src/main/experimental/manager-factory.ts` | +9 / -0 |
| `packages/core/api/client.ts` | +13 / -2 |
| `packages/core/api/schemas.ts` | +58 / -0 |
| `packages/core/api/client.test.ts` | +93 / -0 |
| `packages/views/issues/components/issue-labs-section.tsx` | +37 / -18 |
| `apps/desktop/package.json` | version bump |

Net: **+211 / -20** across 6 files. Single atomic commit `fix(labs): 0.3.48 — mythos supervise poll race + getIssue parseWithFallback + agent_creation_studio parity`.
