# 0.5.60 — Labs × issue integration audit batch: 3 P0 + 5 P1/P2 fixes

Released 2026-08-23, branch `epic/0.5.13-integration`, 9 atomic commits +
1 version bump on top of 0.5.59. Driven by the full-scope audit at
`.omc/audit/2026-08-23-labs-issue-integration-audit.md` (6 parallel
read-only auditors + live DB/deploy ground truth).

## Headline

The audit verdict: labs platform chain is structurally sound (8/8 flags,
binding→trigger→progress→persist→GC all wired in code) but had one
bypassable enforcement gap, one contract-violating bare fetch on the
primary target, and an unbounded DB leak — plus a fleet of unpinned
contract corners. 0.5.60 closes all three P0s and five P1/P2s.

## Fixes

1. **P0-1 BatchUpdateIssues swarm mutex parity** (`ce3ea85df`). The 0.5.21
   swarm mutex extension never reached the batch switch — a batch PATCH
   flipping `lab_source=swarm_topology` onto an assigned issue persisted
   silently while Create/Update 400. Added the case + 3 pinned tests
   (batch×swarm 3 subtests, UpdateIssue×swarm, swarm×enhancer create+update).
2. **P0-2 semantica explorer bare fetch** (`ab5c9ba97`).
   `semantica-explorer-view.tsx` called `/decisions` site-relative with
   `credentials:include` — unreachable from packaged desktop (`file://`,
   token auth), so the team-mode banner fell back to "individual" forever.
   Now `api.rawRequest` per the 0.3.30 contract.
3. **P0-3 orphaned lock/visibility sweep** (`7341a423a`). 3401/3418 lock
   rows + 408/434 visibility rows referenced hard-deleted resources
   (runtime-teardown cascade + workspace CASCADE never release locks;
   only swarm_gc deleted any). Migration 274 one-shot cleanup
   (locks 3418→312, visibility 434→26, orphans 0) +
   `SweepOrphanedExperimentalResources` wired into the SwarmGC tick as a
   separate call (sweep()'s no-swarm-runs early return must not starve
   it). Pinned unit test for counts/wrapping/fail-fast.
4. **P1-1/P1-2 lab_mode validation** (`8d760a296`). Create accepted
   `enhancer` on any lab (201) while Update rejected it (400) — such rows
   were un-PATCHable; both paths now agree (enhancer = mythos-only).
   Bogus `lab_mode` now 400s at the boundary instead of dying on the
   mig-157 CHECK as 500. P1-3 (two-step write warn-only) accepted as-is,
   rationale in commit message.
5. **P1-4 pythia stamp key** (`9567cc301`). The 0.5.59 trigger stamp fell
   back to a fabricated `"ws"` segment the reader never matches; now it
   skips the stamp instead of writing an unmatched key.
6. **P1-5 + P2-6 + P3-4 catalog/manifest alignment** (`6c117433c`).
   code_canvas manifest flipped to `installable:true` (its handler exists
   and provisions `code_canvas_worker`); chat_pin_ui gains
   `HideFromIssueLabPicker` (dead binding — no leader, no dispatch);
   retired the 0.3.19 "stub" comments (real stdlib service since 0.5.18);
   pythia manifest gained the missing `resources` block.
7. **P2-2 + P2-3 desktop gaps** (`fa178e9e3`). swarm_topology added to
   `staticFlagDescriptors` (cold-boot IPC hole); mythos extension picker
   now filters `lab_managed`.
8. **Ship-integrity hole** (`286ca8f71`). At HEAD the bundled
   `resources/semantica/run.sh` was the STALE pre-wheel version and the
   bundled copy of migration 273 was untracked — a clean-tree ship would
   have bundled the old runtime. Committed the byte-identical parity fix
   (diff-verified against vendor/ and server/migrations/).
9. **Doc drift** (`8e3330f0a`). CLAUDE.md: self-opt control-point wording
   (one weekly `[自进化]` autopilot row, `status` not `enabled`),
   code_canvas installability, F-006 signed-URL evolution, lab-picker
   path. Audit report landed at `.omc/audit/2026-08-23-labs-issue-integration-audit.md`.

## Verification

- `pnpm typecheck` — 6/6 ok
- `go build ./...` clean; sqlc regenerated (2 new queries)
- New tests: batch×swarm mutex (3 subtests), UpdateIssue×swarm,
  swarm×enhancer (create+update), non-mythos enhancer rejection,
  lab_mode enum boundary (create+update), orphan sweep unit test — all
  green with `-race`
- Migration 274 applied to the live DB; orphan counts verified 0
- Full `go test ./internal/... ./pkg/agent/...`: green EXCEPT two
  pre-existing failures bisected to base `a410baa00` (NOT this batch):
  `TestDashboardPerAgentRollupsUseExactWindow` (time-window assertion,
  fails deterministically at this local hour) and the known mythos
  `TestTickSupervision_CompletionByFinalIssueStatus` panic;
  `TestQuickCreateIssueParentTrustBoundary` is flaky under the full
  suite (passes in isolation and on rerun; also flaky pre-batch).

## Deferred (see audit report §修复落地记录)

- P1-6: every run table is empty on this install — no lab has ever
  completed an end-to-end run here; pythia persist still needs a real
  trigger to close the loop.
- P2-4 swarm leader-created skills/squads get no visibility rows
  (transient, LOW). P2-5 experimental_pref orphan user_ids (known
  username-login contract derivative, inert).

## Post-ship root cause (the 0.5.59 incident, finally closed)

After shipping, an API trigger reproduced "frames stream, zero rows" and
the 0.5.59 diagnostics caught it live: `persist skipped — zero
envelopes`. The real root cause is Go's defer-argument evaluation —
`defer persistIssueForecastRun(r, ifc, collected)` captured the EMPTY
slice header at the defer statement; appends never reached it. Fixed by
deferring a closure (`6bdf5fd63`) + end-to-end pin
`TestIssueForecastStreamPersistsCollectedRounds`. Verified live:
3-round full consume → `persist run OK rounds=3` + 3-envelope row;
early disconnect → partial persist (by design). Also built + committed
the missing semantica wheel (`8fe19fd10`) — the offline install chain
was missing it entirely on this machine. Two more fixes followed the
run-lifecycle auditor's final report: semantica ACL upsert failures are
now logged instead of discarded (`4cafa0f18`), and mythos/swarm panels
gained "never started" affordances so an empty run table no longer reads
as "broken" (`eda379eab`, 4 locales).

## Ship chain

Standard: snapshot → migrate up (274 already applied) → bundle-cli →
electron-vite build → `electron-builder --mac --dir` from `apps/desktop/`
→ cp -R → `scripts/desktop-sign-nested-binaries.sh` → cold-start verify.
