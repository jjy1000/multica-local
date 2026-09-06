# Release Notes — 0.5.100 (upstream port batch + ship-chain hardening)

**Date**: 2026-09-06
**Baseline**: 0.5.99 (HEAD 77c855458; H9 import fix shipped)
**Head**: `13223e680 chore(release): bump 0.5.99 → 0.5.100`
**Branch**: `epic/0.5.72-followups` (22 commits ahead of origin)
**Scope**: 3 upstream MUL ports (MUL-6942, MUL-7053, MUL-6923) + 1 ship-chain
hardening. Fork and upstream are fully divergent (merge-base empty); all three
ports are manual diff transplants, NOT `git cherry-pick`.

##Gates
- `pnpm typecheck` → 6/6 packages clean (after wiping stale `apps/web/.next/types/`
  that still referenced the deleted contact-sales page)
- `pnpm test` → 1832 passed, 33 skipped (parked MUL-6632 inbox family, no change)
- `cd server && go test -count=1 ./internal/... ./pkg/agent/...` → all PASS
- `pnpm --filter @multica/desktop test` → 384 passed (47 files)
- `pnpm --filter @multica/desktop typecheck` → clean

## TL;DR

1. **`chore(ship)`** — make `ship-mac.sh` step 7 cold-start verification fatal
   with belt-and-suspenders post-verify checks. Closes the 0.5.98 ship
   regression class: a ReferenceError in pickEnvForSpawn prevented the
   server from binding :8090, but the verify script reported PASS (or was
   absent) and ship-mac.sh exited 0. New step 7 refuses to ship when (a)
   the verify script is missing, (b) the verify script exits non-zero, or
   (c) a server PID is not bound on :8090 after the script claims PASS, or
   (d) `/health` is not `{"status":"ok"}` after the script claims PASS.
   Each has its own `die` with a specific message.
2. **`fix(views)`** — MUL-6942. Let long issue identifiers expand beyond 4rem
   (the upstream `w-16` → `min-w-16` change). Fork has no `table-view.tsx`
   equivalent; only `list-row.tsx:105` carries the pattern, so the change is
   single-file. Skeleton `<Skeleton>` `w-16` usages in `issue-detail.tsx`
   left untouched (loading placeholders, not identifier spans).
3. **`fix(daemon)`** — MUL-7053. Explain codex retired-compaction 404 with a
   path-keyed hint appended to the error text. The 404 from `responses/compact`
   on Codex 26.901+ names the URL but never the setting (`remote_compaction_v2`)
   that selected it; the reporter in GH #8000 had to open an issue to learn
   the fix was deleting one line. The hint names every place the setting can
   be off (config file + launch arguments on agent/daemon/profile) and
   explicitly excludes the generated per-task config copy. Fork does not
   have the Hermes annotation (`annotateHermesProviderUnconfigured`) that sits
   next to this line upstream, so only the codex leg is added — call site
   comment names the divergence so a future port does not think the line was
   missed.
4. **`feat(shortcuts)` + 2× `feat(desktop)`** — MUL-6923 (3 of 4 sub-commits).
   Cmd/Ctrl+1..9 now selects a browser-style tab position: 1..8 pick the
   exact one-based slot in the current workspace, 9 always picks the last
   tab (Chrome / Firefox / Safari semantics). The chord is owned by the main
   process so it works while focus is inside editors, inputs, menus, or
   dialogs, and matches the existing tab:close-active direct-send pattern
   (rather than upstream's `dispatchToMainRenderer` helper which this fork
   does not use).

## Port strategy recap (recurring for future upstream work)

Fork and upstream `multica-ai/multica` share **zero commits** (`git merge-base
HEAD upstream/main` returns empty). Every upstream port is a manual diff
transplant:

1. `git show <sha>` to read the upstream commit
2. find the fork-side equivalent (often renamed or restructured)
3. port only the lines needed to implement the change — never
   `git checkout --theirs` (0.5.36 wholesale-adoption trap, replaces the
   fork file with the entire upstream file and silently destroys
   localization)
4. drop everything fork has deleted (OAuth/cloud/billing/telemetry)
5. add new code at the bottom of the fork file when no anchor matches
6. verify with focused tests + per-file diff stat vs upstream stat

The 0.5.100 ports all kept per-file diff under 5× upstream's stat (the
maximum is the 8 LOC `pickEnvForSpawn` retry-fix at `0.5.99`, not in this
batch).

## Files (0.5.100)

| File | Change | LOC |
|---|---|---|
| `scripts/ship-mac.sh` | verify fatal + belt-and-suspenders | +29 / -7 |
| `packages/views/issues/components/list-row.tsx` | w-16 → min-w-16 | +1 / -1 |
| `server/internal/daemon/daemon.go` | annotateCodexRetiredCompaction call + def | +76 / -0 |
| `server/pkg/agent/codex.go` | CodexRetiredCompactionError + consts (append) | +66 / -0 |
| `server/pkg/agent/codex_compaction_test.go` | NEW — table test | +85 / -0 |
| `packages/core/shortcuts/definitions.ts` | PRIMARY_RESERVED_KEYS += 1-9 | +5 / -0 |
| `packages/views/locales/{en,ja,ko,zh-Hans}/settings.json` | +2 keys | +2 / -0 each |
| `apps/desktop/src/shared/main-renderer-messages.ts` | TAB_SELECTION_SHORTCUT_CHANNEL + types | +17 / -0 |
| `apps/desktop/src/main/keyboard-shortcuts.ts` | select-tab action | +25 / -2 |
| `apps/desktop/src/main/keyboard-shortcuts.test.ts` | describe block + helper ext | +69 / -0 |
| `apps/desktop/src/main/index.ts` | before-input-event handler | +16 / -0 |
| `apps/desktop/src/preload/index.ts` | onSelectTabShortcut | +20 / -0 |
| `apps/desktop/src/preload/index.d.ts` | DesktopAPI member | +7 / -0 |
| `apps/desktop/src/renderer/src/App.tsx` | useCmdNumberTabSelect inline | +31 / -0 |
| `apps/desktop/package.json` | 0.5.99 → 0.5.100 | +1 / -1 |

Total: **18 files, +566 / -10** (after the version bump +27 / -1 lands).

## NOT ported (documented for future)

- **MUL-6923 settings UI** (C3.4 of the agent's 9-sub-commit plan). Fork has
  no `packages/views/settings/components/keyboard-shortcuts-tab.tsx`
  component, no `FixedShortcutRow` helper, and `createShortcutChord` is
  only used in `thread-nav-panel.tsx` / `zoom-canvas.tsx`. Creating the
  full settings tab from scratch is out of scope for 0.5.100. The 8
  i18n keys (`select_tab_1_to_8` + `select_last_tab` × 4 locales) added
  in C3.1 are therefore orphan strings — they document the intended UI
  surface and will be wired to the row when the tab is ported.
- **MUL-7053 launch-args test** (`TestCodexLaunchArgsCanSelectTheRetired
  CompactionRoute`) and **daemon annotation test** (`codex_compaction
  _annotation_test.go`). Both depend on `NormalizeCodexLaunchArgs`,
  `codexFastServiceTier`, `FilterLaunchPrefix`, `stripCodexFastMode
  Conflicts`, `classifyResumeUnsafeTransport`, and `service.ResumeUnsafe
  Failure` — none of which exist in fork. Carrying partially-adapted
  versions would give false confidence. When fork's launch-args
  infrastructure lands, the two tests should be ported together with
  that work (the hint's "launch arguments are a real source" claim is
  what they verify).
- **Upstream `MUL-6665`** (agent task → runs rename), **MUL-6986** (skill
  system merge), **MUL-6904/6684** (OpenClaw MCP isolation), **MUL-7016**
  (read replica). Either out of scope, dormant in fork, or fork-absent
  infra.

## Deferred (carries forward to 0.5.101 / 0.5.102)

| PR | MUL | Complexity | Strategy | Notes |
|---|---|---|---|---|
| B1-1 | MUL-7051 autopilot subscribe | MED | surgical-port | sqlc regen; PG17 CTE |
| B1-2 | MUL-7008 WS claim poll 3m | HIGH | surgical-port + new wsrpc.go | wsrpc.go doesn't exist in fork; 4-locale docs |
| B1-3 | MUL-7002 heartbeat | HIGH | surgical-port | RuntimeLease placement |
| B1-4 | MUL-7002 invalidation | HIGH | surgical-port (after B1-3) | 5 deletion sites |
| B1-5 | MUL-6951 trigger auth | MED | **SKIP** (audited, no callers) | audit agent confirmed attribution.TriggerOwner / RuleOwner have zero reachable callers outside their own test file; bug class is dormant in fork |
| B2-1 | MUL-7028 session expire | MED | surgical-port (5 sub) | auth/index.ts vs auth/store.ts path divergence |
| B2-3 | MUL-6833 error page i18n | HIGH | scaffolding-only | OAuth dead code SKIP |

The 0.5.100 → 0.5.101 batch (B1-3 + B1-4 ordered pair + B1-1 + B1-2 + B1-5
audit) is the high-risk perf/security batch. **B1-5's audit result is
binding: SKIP for 0.5.101, revisit only when an upstream PR adds the
autopilot dispatch-side caller that triggers the carry-authorization
behavior.**

## Lessons (rolled into future work)

1. **Ship-chain verify is now fatal.** The 0.5.98 regression class (silent
   ship of broken app) is closed by `de3dffac9`. A future audit cycle
   that breaks the verify must re-add the `die` call.
2. **`.next/types/` cache trap.** `apps/web/.next/types/` retains type
   information for deleted pages (e.g., the fork-removed contact-sales).
   `pnpm typecheck` fails with `TS2307 Cannot find module` until the cache
   is wiped. `rm -rf apps/web/.next` before typecheck in any session
   that has run the dev server since removing a page.
3. **Wholesale-adoption trap still active.** Per 0.5.36: never
   `git checkout --theirs` on a cherry-pick file. The 0.5.100 ports all
   kept per-file diff under 5× upstream's stat. Compare:
   ```
   git show <upstream-sha> --stat | head
   git diff --stat HEAD -- <file>
   ```
4. **i18n keys as documentation.** The orphan `select_tab_1_to_8` /
   `select_last_tab` keys are intentional — they document the UI surface
   the MUL-6923 port is meant to enable. Drop them only if the upstream
   feature itself is dropped.

## Next steps

1. **Ship**: `bash scripts/ship-mac.sh --yes` (after this notes commit lands).
3. **0.5.101 plan**: B1-3 (heartbeat) + B1-4 (invalidation) as ordered pair,
   then B1-1 + B1-2 in parallel. B1-5 SKIP until dispatch-side callers arrive
   upstream.
4. **0.5.102 plan**: B2-1 (session expire) + B2-3 (error page scaffolding).
5. **Memory**: persist the 0.5.100 lessons (verify-fatal pattern,
   `.next` cache trap, MUL-6923 settings UI gap) to cross-session memory.