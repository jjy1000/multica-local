# Release Notes — 0.5.41

**Shipped 2026-08-19** (branch `epic/0.5.13-integration`, 4 atomic commits on top of 0.5.40: `30d782d2b` → `ccc7f21c3` → `ec06e1eff` → `696f5c71c`). Destructive ship chain (`pre-update-snapshot → bundle-cli → electron-vite build → electron-builder --dir → install → desktop-sign-nested-binaries → verify-desktop-cold-start`) ran end-to-end at 22:04 local via `bash scripts/ship-mac.sh --yes`. /Applications/Multica.app = **0.5.41 (commit: 696f5c71c, built: 2026-08-19T14:03:48Z)**, server PID 18423, cold start ~4s, row parity 7/346/2469/119 (matches 0.5.40 baseline — pure-frontend release, no DB churn expected).

## Summary

3 atomic commits closing the **status-glyph custom-color thread** across the issues surface. The StatusIcon component (added in MUL-6243, 0.5.33-0.5.36) already supports `category` + `color` props; 0.5.41 wires the catalog entry's color into three more call sites so custom statuses render with their own `#rrggbb` instead of the built-in's semantic token:

1. **MUL-6413** — `issue-detail.tsx` ActivityBlock timeline (status-changed rows now show custom colour)
2. **MUL-7214** — `inbox-list-item.tsx` row glyph (moving between two same-category statuses used to leave the row pixel-identical)
3. **MUL-6399** — `issues-header.tsx` status filter list (drop the doubled-height category headings; StatusIcon in the flat list now carries the colour)

**Zero migrations, zero schema drift.** Total: 4 files changed, ~110 LOC net add.

## Changes

### 1. `fix(issues)` `30d782d2b` — give the timeline's status glyph the custom status's identity (MUL-6413, upstream #7231)

Fork-port of upstream `4ba9b40d5`. ActivityBlock timeline resolver now hands StatusIcon the catalog entry's colour so a status-changed entry for a custom status renders with the custom colour, not the built-in's semantic token.

- `packages/views/issues/components/issue-detail.tsx` (+21/-1): new `resolveStatusColor: (statusKey: string) => string | null` prop on ActivityBlock (computed via `useIssueStatuses.entryOf` + `is_system` guard); passed through to the `StatusIcon` call.

**No fork deviation.**

### 2. `fix(inbox)` `ccc7f21c3` — give the inbox row's status glyph the custom status's identity (MUL-7214 / MUL-6395, upstream #7214)

Fork-port of upstream `a67bcb1f4`. The row's only status affordance is one glyph, so it alone cannot tell "In Review" from a custom "Human Review" — moving between two same-category statuses left the row pixel-identical. The status-changed detail label already used the catalog colour; the row now does the same, and carries the resolved name as `title`.

- `packages/views/inbox/components/inbox-list-item.tsx` (+31/-1): imports `useIssueStatuses` + `useStatusLabel`; computes `statusEntry`/`statusColor` from `useIssueStatuses(item.workspace_id)`; StatusIcon wrapped in `<span title={statusLabelOf(item.issue_status)}>` with `category={statusCategoryOf(item.issue_status)}` + `color={statusColor}`.

**Fork deviations from upstream's commit:**
- Fork's pre-state had NO `useIssueStatuses`/`statusCategoryOf` usage in this component (the file is simpler than upstream's pre-state), so the full hook + `entryOf`/`categoryOf` destructuring + `statusColor` derivation are added here. Upstream's patch only added `entryOf` + `statusLabel` + the wrap.
- The test file `inbox-list-item.test.tsx` is missing in fork (fork never had an inbox-list-item test); upstream's added test is therefore skipped. Regression coverage for this fix comes from the visual contract — `CustomStatusChip` renders the same colour in board + list views (MUL-6243 surface).

### 3. `fix(issues)` `ec06e1eff` — drop category headings from the status filter list (MUL-6399, upstream #7217)

Fork-port of upstream `5475a6eae`. The status filter menu showed a category heading above every status once a workspace held one custom status (7 headings over 8 rows, doubled popover height). One flat list in canonical category order; the icon already carries the category, so the heading added no information.

- `packages/views/issues/components/issues-header.tsx` (+32/-40): useStatusOptions() destructured to `{ options }` (the flat list); removed the per-group Fragment + DropdownMenuLabel wrap; removed Fragment import; StatusIcon now passes `category={option.category}` + `color={option.color}` (fork pre-state omitted `color` because all callers stayed inside `group.options.map` where category was implicit).
- `packages/views/issues/utils/status-options.ts` (+4/-1): extended `StatusOption` interface with `category` field so the flat-list map can pass it through.

**Fork deviations from upstream's commit:**
- **109b67790** (predecessor fix wrapping `DropdownMenuLabel` in `DropdownMenuGroup` to fix the MUL-4819/MUL-6393 Menu.GroupLabel THROW) is NOT ported. The fix becomes unnecessary once categories are dropped (no DropdownMenuLabel → no Menu.GroupLabel → no THROW). That commit also added a `fixed = viewBaseline?.status.has(...)` check that depends on a `viewBaseline` field the fork's view store never ported. Porting it would require first adding `viewBaseline` to `view-store.ts` (~30 LOC) + a `fixedTitle` constant; that's out of scope for this surgical port. **Current fork behaviour is correct** because MUL-6399's collapse renders the 109b67790 fix obsolete.

## Deferred + SKIPs added this round

- **MUL-6363 reuse custom_property.none i18n key** (upstream `f9b7bf52e`, +3/-7, 5 files) — **SKIP-NO-SURFACE**: fork's `packages/views/locales/*/issues.json` does NOT have a `pickers.custom_property` namespace. The prerequisite commit `66af64b9a6f4` (feat: support filtering custom properties by "no value", +294 LOC, ~2-week-old) is still deferred. SKIP until `66af64b9a6f4` lands.
- **109b67790 status filter category heading crash fix** (upstream `109b67790541`, +191/-35, 2 files) — **SKIP-DEPENDENCY**: depends on `viewBaseline` field in the view store (fork lacks). Additionally, the fix becomes **obsolete** once MUL-6399 drops the category headings entirely (this commit). Either way, no work needed.
- **MUL-6409 cards in category column** (upstream `c1d07a1ed`, +298/-43, 12 files) — **SKIP-NO-SURFACE**: fork has no `packages/views/issues/surface/use-issue-status-branches.ts`. The fork's `packages/views/issues/surface/` directory is empty.

## Deferred (carried from 0.5.40 — unchanged)

- MUL-6286 (actor properties — port WITH MUL-6305 fail-closed migration first)
- MUL-5651 (port MUL-4257/4302/4304/4351 first)
- MUL-6321 OpenClaw slow hosts
- MUL-6063 delegated task failures
- MUL-5991 (jcode)
- 0c69f1f95 (Hermes resume-auth)
- MUL-6350 plugin rebuild — 3/4 hook-engine branch: **SKIP-DEAD-CASE for whole series**, ~30k LOC wholesale, fork plugin is local subprocess + sandbox-isolated
- MUL-6323 worktree-gate blame redirect — agent died 429 mid-port; retry = fresh session + quota + small-chunk pattern
- MUL-6396 perf realtime fanout flood (977 LOC, 3 changes — needs deep-eval)
- MUL-6343 semantic activity timestamps (2366 LOC, deserves own ship)
- 66af64b9a6f4 feat: filtering custom properties by "no value" (294 LOC, TS-only)
- ea07a29138 perf: task message UUIDv7 (56 LOC)
- 9bd556785fba inbox desktop shell nav feedback (287 LOC)

## SKIPs (carried from 0.5.40 — unchanged)

- MUL-6350 hook-engine series (SKIP-DEAD-CASE for whole 1/4-4/4), MUL-6342 entitlement quotas (SKIP-DEAD-CASE), MUL-6403 changelog (SKIP-DOCS), 343914606 Vercel (SKIP-NO-ENDPOINT), MUL-6327 mcode logo (SKIP-DEAD-CASE), MUL-6335 skills bulk update (SKIP-NO-ENDPOINT), OpenClaw discovery cache (SKIP-DEAD-CASE), MUL-6364 LLM retry budget (SKIP-DEAD-CASE), MUL-6341 entitlement policy (SKIP-DEAD-CASE), **plus MUL-6264 + MUL-6394 + MUL-6363 + 109b67790 + MUL-6409 (added this round)**.

## Verification (run on commit `ec06e1eff`, before this version bump)

- **pnpm typecheck (full turbo)**: 6/6 packages clean (31.5s).
- **`cd packages/views && pnpm exec vitest run issues/utils/status-options.test.tsx`**: 7/7 pass.
- **Diff stat per-file**: matches upstream closely. MUL-6413 (+21/-1 vs upstream +133/+2 — fork's pre-state already had the basic useIssueStatuses call so the entryOf addition collapses). MUL-7214 (+31/-1 vs upstream +146/+20 — fork pre-state was simpler, no category prop existed). MUL-6399 (+36/-41 across 2 files vs upstream +149/+184 across 6 files — fork pre-state lacked the picker/property-picker test changes that upstream batched into the same commit).
- **Per-file fork diffs vs upstream**: MUL-6413 has no fork deviation. MUL-7214 documented deviation: fork pre-state simpler + test file skip. MUL-6399 documented deviations: 109b67790 fix made obsolete + fork StatusOption interface extension for `category` field.

## Memory + Notes

- No new memory file this round (single-session port, lessons are captured in CLAUDE.md header + commit bodies).
- CLAUDE.md header refreshed to reflect 0.5.41 as current release.
- `apps/desktop/package.json` bumped 0.5.40 → 0.5.41 (canonical version source per CLAUDE.md "Version source").