# Release Notes — 0.5.51

**2026-08-21** (branch `epic/0.5.13-integration`, 5 commits on top of 0.5.50: MUL-6310 closure — protocol align + taskfailure comment fix + NUL regression test port — plus the MUL-6471 opencode/pi custom-provider fix). `go build` + `go vet` clean; `pkg/agent` + `internal/daemon` suites + 3 DB-backed NUL handler tests pass. Ship not run (user paused porting after MUL-6471).

## Summary

**MUL-6310 thread closed with an honest verdict.** Deep-dive this session established that the fork's NUL-byte sanitization (the security fix at the heart of MUL-6310) is **fully landed and now regression-pinned**, and that the remaining "caller integration" blockers named in the 0.5.50 notes are **SKIP-DEAD-CASE** — they require upstream chat architecture the fork does not have.

## Changes

### 1. `feat(protocol)` `6c092cd54` — add ChatCancelFinalizedPayload + ReasonSkillBundleUnavailable (MUL-6310 prereq)

Two stub prerequisites for the cancelled-task work-pointer delivery: `protocol.ChatCancelFinalizedPayload` and `taskfailure.ReasonSkillBundleUnavailable`. Also syncs `apps/desktop/resources/server/migrations/` to `server/migrations/` (261-272; 261-268 were bundle-cli output never committed, 269-272 were never copied since 0.5.49/0.5.50 shipped schema-only).

### 2. `fix(protocol)` `36dbddecf` — align ChatCancelFinalizedPayload + ReasonSkillBundleUnavailable with upstream

The first stub used a `TaskID/WorkflowID/Outcome/StreamedMsg` shape that does **not** match upstream (MUL-5378 #6016). Replaced with the exact upstream payload: `Outcome + ChatSessionID + TaskID + InitiatorUserID (omitted = 'not me')` + the persisted `'Stopped.'` row fields (`MessageID/Content/MessageKind/CreatedAt/ElapsedMs`), plus `ChatCancelOutcomeStopped`/`ChatCancelOutcomeRestored` constants. Also corrected the taskfailure doc comment — it claimed a `classifySkillBundleUnavailable` branch in handler.daemon.go that does not exist in the fork.

### 3. `test(handler)` `915f2f937` — port upstream MUL-6310 NUL-byte regression tests (#7124)

The fork's sanitization (`util.SanitizeTextForPostgres`/`SanitizeJSONForPostgres`, wired into `/complete`, `/fail`, `/messages` since 0.5.45-0.5.46) had **zero regression coverage** — deleting a sanitize call would pass silently. Ported upstream's `task_payload_nul_test.go` (260 → 216 lines after adaptation):

- **Kept**: `seedNULTask` (agent/issue/task fixture, fork-column-compatible), `TestCompleteTaskCallbackWithNULSucceeds`, `TestReportTaskMessagesCallbackWithNULSucceeds` (3 poisoned places incl. nested JSONB input), `TestPostgresRejectsUnsanitizedTaskPayloads` (the control proving PG genuinely rejects NUL escapes).
- **Adapted**: dropped `DurableWorkDir` (fork's `TaskCompleteRequest` has only PRURL/Output/SessionID/WorkDir).
- **Dropped**: `TestReportTaskMessagesCreatesUUIDv7` — fork's `task_message.id` defaults to `gen_random_uuid()` (v4); UUIDv7 adoption is a separate upstream feature.

**Verified**: `go vet ./internal/handler/` clean; all 3 tests pass against the running PG (handler package 1.133s).

### 4. `feat(agent)` `603c0765d` — let opencode reach custom providers + pass pi model selectors whole (MUL-6471)

Minimal fork-style port of upstream #7312. Gateway-style providers register model ids that themselves contain a slash (`claude/claude-opus-5` under provider `multica-anthropic`); both runtimes treated that slash as a provider boundary and failed before any provider call (GH #7300). **The user's opencode runtime is actively used (online in the local DB), so this is directly load-bearing.**

- **pi** (`buildPiArgs`): the selector now goes to `--model` WHOLE and `--provider` is never synthesized. Pi's own resolver accepts `provider/id`, a bare id, and an id containing a slash — passing less is strictly more capable. Removed the now-dead `splitPiModel`.
- **opencode** (daemon): `--model` is `provider/model` and a bare id is rejected outright. Added `agent.ModelSelectorMustBeProviderQualified` (opencode-only in this fork; no deveco/omp/`ProtocolFamily` registry) + `agent.QualifyModelID` (fork-local, operates on `[]Model` since `ListModels` returns `[]Model`, not the upstream `Catalog` wrapper). The daemon qualifies a pinned model against the runtime catalog after two-tier resolution and **before** thinking-level validation (which matches on the catalog's canonical id). The `starting agent` log now shows the resolved model instead of `entry.Model`.
- **Not ported** (fork lacks the foundation): `Catalog.Fallback`, `BuiltinRuntimeByID`/`ProtocolFamily`, the loader-sharing single-read refactor (`ValidateThinkingLevelWith`/`ValidateServiceTierWith`), and the pi-RPC `piModelsFromRPC`/`piThinkingSupports` tests. The fork's healthy-runtime catalog reads are memoized by `cachedDiscovery`, so the double-read concern that drove the upstream refactor is negligible here.

**Verified**: gofmt/vet clean; `pkg/agent` 13.3s + `internal/daemon` 23.5s pass incl. new `TestQualifyModelID` / `TestModelSelectorMustBeProviderQualifiedIsAnExecutionContract` / `TestBuildPiArgsSlashShapedModelStaysWhole`.

## MUL-6310 verdict (corrected record)

| Piece | Status |
|---|---|
| `util.SanitizeTextForPostgres` / `SanitizeJSONForPostgres` | ✅ LANDED 0.5.45-0.5.46, byte-identical to upstream MUL-6310 |
| `/complete` sanitization | ✅ LANDED (fork's full request shape scrubbed, before the context-exhaustion re-route) |
| `/fail` sanitization | ✅ LANDED (fork's full request shape scrubbed) |
| `/messages` sanitization | ✅ LANDED (incl. `type` + JSONB Input deep-walk) |
| NUL regression tests | ✅ LANDED 0.5.51 (`915f2f937`) |
| `protocol.ChatCancelFinalizedPayload` | ✅ LANDED 0.5.51, upstream-aligned |
| `taskfailure.ReasonSkillBundleUnavailable` | ✅ LANDED 0.5.51, upstream-aligned |
| `TaskService.RebroadcastCancelledTask` / `FinalizeDeferredCancelledChat` | **SKIP-DEAD-CASE** — built on upstream's direct-chat claim/ownership subsystem (chat_input_ownership, Reanchor*, visible-head selectors). Fork's `chat.sql` is **219 lines vs upstream 1491**; the subsystem is absent. |
| `AckTaskCancelled` endpoint + cancel-ack | **SKIP-DEAD-CASE** — fork daemon uses poll-based cancellation (`cancelPollInterval`, server flips status → daemon notices on poll). No daemon→server cancel-ack protocol, no worktree branch production (`setupGitWorktree` has zero callers; `result.BranchName` is never populated, so `/complete` never receives a non-empty `branch_name`). |
| `agent_task_queue.branch_name` column + `SetAgentTaskBranchName`/`SetAgentTaskErrorIfEmpty` queries | ⏸️ **DORMANT** — forward-looking prerequisites for worktree-mode integration that isn't wired. Zero consumers. Left in place (upstream-aligned, harmless); document as dormant rather than removing. |

**Why SKIP-DEAD-CASE is the right call for a single-user local fork**: the deferred chat-cancel chain exists to rebroadcast a cancelled chat task's outcome to a *multi-client* fleet (restore the initiator's draft on a different device). The fork has one local user, direct chat, and a leaner persistence model. Porting the upstream chat direct-input subsystem (~1300 lines of SQL + task.go machinery + daemon chat flow) would be a multi-day, high-risk integration with no user-visible benefit in the localized fork. The poll-based cancel already works.

## SKIP reconciliation

| Skip (from 0.5.49) | 0.5.51 progress | Status |
|---|---|---|
| MUL-6472 dispatch leak | LANDED 0.5.47 | ✅ cleared |
| MUL-6310 NUL bytes | sanitization LANDED + regression-pinned; remaining blockers SKIP-DEAD-CASE | ✅ **closed** (security fix complete; architectural remainder intentionally not ported) |
| CJK markdown | LANDED 0.5.48 | ✅ cleared |
| MUL-6417 follow-ups | **Assessed and deferred** — fork's `runtime_config_sections.go` is 817 lines divergent from upstream pre-MUL-6417, lacks `ChatChannelType`/`kindIssue` (fork uses `kindCommentTriggered`/`kindAssignmentTriggered`). A manual rewrite, not a port. | ⏸️ deferred (highest effort/lowest ROI of the remaining batch) |

## Verification

| Gate | Result |
|---|---|
| `go build ./...` | PASS |
| `go vet ./internal/handler/` + `./pkg/agent/` + `./internal/daemon/` | PASS |
| NUL regression tests (DB-backed) | PASS (3 tests, 1.133s) |
| `pkg/agent` suite (incl. new QualifyModelID / provider-qualification / slash-shaped pi tests) | PASS (13.3s) |
| `internal/daemon` suite | PASS (23.5s) |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded categories, re-run after header edits) |
| `pnpm typecheck` | not re-run this batch — MUL-6471 is Go-only, no TS changed |

## Files changed (5 commits)

| Commit | Files | Insertions |
|---|---:|---:|
| `6c092cd54` protocol + taskfailure stubs + migration sync | 26 | 204 |
| `36dbddecf` upstream alignment | 2 | 43 |
| `915f2f937` NUL regression tests | 1 | 261 |
| `603c0765d` MUL-6471 opencode/pi custom providers | 5 | 289 |
| `010ea1a3c` 0.5.51 release notes + 0.5.50 correction | 2 | 78 |
| **Total** | **36** | **875** |

## Strategic significance

Two threads closed this release:

1. **MUL-6310 honestly closed** — the security fix is complete and regression-pinned; the architectural remainder is SKIP-DEAD-CASE (upstream chat direct-input subsystem + daemon cancel-ack/worktree, both absent in the fork).
2. **MUL-6471 landed** — the user's actively-used opencode runtime now reaches custom gateway providers (bare model ids qualified against the runtime catalog), and pi passes model selectors whole. This is the first port this release line that fixes the user's actual running runtime.

**Candidate assessment (research complete)**: the remaining upstream batch has converged to a small high-value subset. MUL-6368 (cross-agent contamination, security, 858 lines, daemon/execenv divergence) is the next worth-doing item. MUL-6463 (run-confirm gate, partial — drop the fork-missing table-view surface) is medium value. MUL-6458 (realtime status sync) has ~zero single-user value. MUL-6146 (rerun cancel) needs the rerun-queue/successor sqlc foundation the fork lacks. **Full upstream parity is a moving target (579 unported commits, upstream 0.4.31 vs fork 0.5.51) and is deliberately not chased** — the fork's own release line is the source of truth.
