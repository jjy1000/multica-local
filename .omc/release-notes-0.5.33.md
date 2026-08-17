---
name: release-notes-0.5.33
created: 2026-08-18T08:30:00Z
updated: 2026-08-18T08:30:00Z
---

# 0.5.33 Release Notes — MUL-6243 Per-Workspace Custom Issue Statuses (Backend) + MUL-6291 (2026-08-18)

Per-workspace custom issue statuses BACKEND (TS/CLI deferred to 0.5.34) + jsdom removal in pure-logic test suites.

## Highlights

### MUL-6243 — per-workspace custom issue statuses (BACKEND port, 3 atomic commits)
The fork's 7-canonical-status CHECK constraint now lives alongside a per-workspace `issue_status` catalog. The 7 built-in statuses are still the defaults; the catalog adds custom statuses that share a category with a built-in. Off by default — flag-gated.

**3 atomic commits** (research plan task `add43ff547c2fbc9b`):
- `b7da0f3f5` `chore(migrations)` — 9 fork migrations 250–258 (verbatim upstream 332–340, renumbered). Adds the `issue_status` catalog table + indexes, drops the inline `issue_status_check` to a format-only CHECK, seeds 7 built-in statuses per existing workspace, installs the `issue_effective_status(UUID, TEXT)` SQL function. Migration 255 deliberately fails the down direction if any custom status exists (refuse, don't destroy data). Concurrent-index cleanup entries (MUL-6288 machinery, already in fork 0.5.32) cover 3 new indexes.
- `0800b5bdf` `feat(issue-status)` — `server/internal/issuestatus/` package (model + Resolve/Effective/Ensure with per-workspace cache, fail-open for built-ins, fail-closed for archived customs) + `handler/issue_status.go` (GET/POST/PATCH/DELETE with CustomIssueStatuses flag gate, default off, owner/admin only) + `cmd_issue_status` route + featureflag key (FF_CUSTOM_ISSUE_STATUSES, `pkg/featureflag` fork-divergence).
- `6fdb57def` `fix(handlers)` — 6 Go consumers normalized (`issue.go` write-path with `resolveIssueStatusKey` + `runWithIssueStatusGuard` + 3-way archive-race guard, `issue_child_done.go` stage barrier, `daemon.go` GC + CompleteTask flip, `github.go`+`vcs_webhook.go` PR terminal, `workspace.go` seed-on-create, `service/issue.go` Create guard, `service/issue_trigger.go` WillEnqueueRun, `service/task.go` HandleFailedTasks, `service/autopilot.go` SyncRunFromIssue, `cmd/server/{autopilot_listeners,notification_listeners,router,runtime_sweeper}.go`). **2 fork-local deviations** (user-approved): `mythos/supervise.go::isTerminalIssueStatus` and `handler/daemon.go:2587` CompleteTask flip both route through `Effective()` so custom done/review statuses don't get stuck.

**NOT included in 0.5.33** (deferred to 0.5.34):
- **TS**: `packages/core/types/issue-status.ts` + `schemas.ts` `IssueStatusEntrySchema` + `client.ts` 4 methods + `schemas.test.ts` (frontend can't enumerate custom statuses without these)
- **CLI**: `cmd/multica/cmd_issue.go::validateIssueStatus` format-only refactor + `cmd_lab.go` status flag path
- **SKILL.md** + `multica-semantica-decision-advisor` skill updates
- **Tests**: adapted upstream tests (`issuestatus_test.go` 311 + `issue_status_test.go` 957 + `issue_child_done_stage_test.go` + `cmd_issue_test.go` flip) + `Effective` pins for the 2 fork deviations

**Safe to ship backend-only**: the feature is flag-gated off (default); no existing behavior changes for users without `FF_CUSTOM_ISSUE_STATUSES=true`. The new tables/migrations exist and are seeded for all 7 existing workspaces (per migration 257). Explicit API calls work server-side.

### MUL-6291 — `aafef2d82` jsdom removal in pure-logic suites (mechanical)
122 pure-logic test files annotated with `// @vitest-environment node` at line 1 + 3 companion `setup.ts` guard wraps (upstream pattern for jsdom-gap patches wrapped in `if (typeof window !== "undefined")`). Every annotated file passed the grep gate (`document\.|window\.|localStorage|getElementById|render(` zero hits), with 2 deliberate de-annotations (use-viewing-timezone + tab-store — they touch DOM/Window via production imports invisible to the file grep). Measured upstream: env 53s→4ms, wall 8.7s→2.0s per file. **Pre-existing flaky test NOT caused by this change** (verified on main HEAD): `TestDashboardPerAgentRollupsUseExactWindow` at dashboard_test.go:1520-1531 (date-boundary) fails identically before and after the diff.

## Verification
- `go build ./...` exit 0
- `go test ./internal/issuestatus/` ok (catalog model)
- `go test ./internal/handler/ -run "TestIssue|TestIssueChild|TestIssueStatus"` ok (consumer normalization)
- Migration 250–258 applied on dev DB
- Ship chain: 4a/7 integrity check PASS → cold-start PASS, server 0.5.33
- /Applications/Multica.app = 0.5.33

## Deferred to 0.5.34
- MUL-6243 frontend (TS schemas/types/client) + CLI (cmd_issue validation) + SKILL.md + tests
- 4 upstream commits still deferred: MUL-6243 UI work, MUL-6286 actor properties (needs properties base), MUL-5991/0c69f1f95 (user-deferred)

## Process notes (carry-over for next session)
- MUL-6243 was 3500-LOC port; 4 agent attempts hit autocompact thrash on the 17-file Step 4 (consumer normalization). **Main-thread intervention worked**: the 17-file diff was visible via `git diff`, committed manually as a single atomic commit, then cherry-picked to main. Lesson: for ports this large, the agent can do the heavy reading + editing (small chunks), but the main thread should do the final commit + ship — agents thrash on `git add` + `git commit` chains across 17 files.
- SendMessage "status check" nudge worked to recover an agent that was stuck in test-fix loops (MUL-6243 3rd attempt).
- The pre-existing `TestDashboardPerAgentRollupsUseExactWindow` flake is the same test that has been flaky since 0.5.22 — date-boundary test running across midnight.

Related memory: `0.5.26-upstream-integration-ship-2026-08-17.md`, `0.5.31-auth-token-gc-2026-08-17.md`, `0.5.32-mul6233-revert-mul6288-2026-08-17.md`.