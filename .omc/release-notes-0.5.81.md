# 0.5.81 — Lab UX Consistency (Work Line 1 of 3-work-line roadmap)

== Summary ==
First of three sequential ships (0.5.81 → 0.5.82 → 0.5.83) that bring
the lab ecosystem to issue-driven interaction per the ICP-1..6 design
law + complete the user's "实验室界面仅显示历史记录或者运行记录"
directive. This release:

  - Closes P1: every lab view (8 surfaces, 7 had no back-link) now
    shows an `<IssueBreadcrumb />` first-class back-link to the bound
    issue via the workspace-singleton release-guard arming path.
  - Closes P2: `<LabPicker>` now confirms before silently clobbering a
    manually-picked assignee on lab_source flip (Active Contract #2
    documented in the UI; mutex labs skip the dialog).
  - Closes P3: `pythia_oracle` no longer auto-fires 10 SSE rounds on
    every `lab_source='pythia_oracle'` create (~50s of blocking UI).
    User triggers forecasts from the Pythia panel / @mention / CLI.
  - Closes P4: `claude_science_lab` reverts from opt-out auto-dispatch
    back to default; the Run research button stays as a manual
    re-trigger surface.
  - Closes P5: `<LabLastResultChip />` renders above each
    `<LabOutputPanel />` reader with ≤280-char summary + click-to-expand
    + lab record deep-link via the new ICP-3 out-bound half.
  - Closes ICP-3 out-bound half: `<LabRunLink />` + `useDeepLinkRun()`
    + `labRunHref()` helpers built on the existing FLAG_ROUTE_SUFFIX
    map. Records are now stable-deep-linkable
    (`/experimental/<suffix>?issue=<id>&run=<id>`).

== Atomic commits (7) ==
  b897aab5c feat(views): C1 — unified IssueBreadcrumb back-link across all lab views
  a3ffde24e feat(experimental): trigger defaults — pythia_oracle opt-out, claude_science_lab default
  ee6b3869c feat(views): C2 — lab record deep links (ICP-3 out-bound half)
  f457e939b fix(desktop): C3 — LabPicker leader-rewrite confirmation
  6174dbfc3 feat(views): C6 — consistent last-result chip in IssueLabsSection
  f03637ff3 test(views): IssueBreadcrumb NavStub type fix (C1 carryover)
  6bad73469 test(handler): flip 5 AutoDispatch regression pins to match catalog
  + chore(release): bump 0.5.80 → 0.5.81 (this commit)

== Upgrade notes ==
- The 5 handler regression pins for lab leader-rewrite semantics have
  been flipped to match the new catalog AutoDispatch values. If you ran
  the test suite locally with -p N > 1, you may see
  `TestQuickCreateIssueParentTrustBoundary` flake (CLAUDE.md
  "Pre-existing flaky test" — race with shared `testRuntimeID` row,
  NOT a regression from this ship).
- The pythia_oracle opt-out means new `lab_source='pythia_oracle'`
  issues no longer auto-fire 10 forecast rounds. If your workflow
  relied on this, use the per-issue Pythia panel's Run forecast button,
  comment with `@pythia_oracle`, or `multica experimental ...` CLI.
- No migration; AutoDispatch is catalog-derived (Go-side), no DB schema
  change.
- No desktop packaging change in this commit (worktree changes only);
  the next ship will pick up the renderer for desktop packaging.

== Files of interest ==
- Roadmap: .omc/plans/0.5.81-0.5.83-labs-evolution-roadmap.md
- Ship log: .omc/0.5.81-ship-2026-08-27.md
- Memory: ~/.claude/projects/.../memory/0.5.81-0.5.83-labs-evolution-roadmap-2026-08-27.md

== Backlinks ==
- 0.5.80 ship (baseline): 93d78ef26
- 0.5.79 ship: c59ed6cf5
- Roadmap v2: includes the v2-design-law §ICP-1..6 + ICP-3 bidirectional
  deep-linking + the 3 hidden-agent / 1 autopilot hidden agent surface
  for Work Line 3.
