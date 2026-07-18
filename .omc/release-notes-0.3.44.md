---
name: 0.3.44 release notes
created: 2026-07-18T12:20:04Z
updated: 2026-07-18T12:20:04Z
---

# 0.3.44 — Upstream non-cloud cherry-pick (6 features)

## Why this ship

The fork's working tree at end of 0.3.43 sat at a stable point (post
`84023bc chore(wip): snapshot pre-integration baseline`). This batch
integrates the non-cloud, non-CloudFront, non-Slack, non-Discord, non-PostHog
features from upstream's recent (migration 158→201) mainline that are
still useful in the single-user / self-hosted scope.

The integration follows strict rules from `multica-fork-vs-upstream-divergence-map.md`:

- Skip cloud billing / cloud runtime / CloudFront CDN / Cloud PAT / Contact Sales /
  Slack / Composio / Discord / HelpLauncher / Feedback modal / PostHog telemetry
  (CLAUDE.md strips these permanently).
- Skip Agent Builder (deferred — needs new schema migrations + hidden system
  agent pattern; see 0.3.45 plan).
- Skip Property system (deferred — 1032 LOC handler + 7 types + 8 sqlc
  queries + 2 migrations; too large for one session without risking ship
  chain slip; see 0.3.45 plan).
- Adapt upstream tests to LOCAL API surface where the upstream surface
  is rejected or reshaped by the local fork.

## What changed

### Project start_date / due_date (MUL-4513)

Calendar-day start / due dates on `project` table, mirroring the
issue.due_date contract. Lifecycle: create echoes, GET persists,
update with key-present-but-empty clears, absent key untouched.
Invalid format → 400 with parseable message.

- `server/migrations/158_project_dates.up.sql` — ADD COLUMN IF NOT EXISTS
  `start_date DATE / due_date DATE`, plus partial indexes for the
  "projects due this week / overdue" UI surfaces.
- `server/pkg/db/queries/project.sql` — `CreateProject` + `UpdateProject`
  carry StartDate + DueDate (sqlc regen).
- `server/internal/handler/project.go` — `CreateProjectRequest` /
  `UpdateProjectRequest` gain `start_date / due_date`; parse via
  `util.ParseCalendarDate` (same machinery as issue dates); key-absent
  vs key-empty-clear semantics.
- `packages/views/projects/components/project-start-date-picker.tsx` /
  `project-due-date-picker.tsx` — picker components forked from the
  issue versions.
- `packages/core/types/project.ts` — `UpdateProjectRequest` adds
  `start_date / due_date` (null to clear).
- 4-locale `projects.json` gets `pickers.start_date / due_date`
  namespace (en / zh-Hans / ja / ko).

### Chat title auto-generation (MUL-4295)

Best-effort LLM-driven chat title generation, adapted from upstream
`chat_title.go`. The async entry point short-circuits to a no-op when
no provider is configured, so self-hosted deployments with no API key
keep their pre-feature behavior unchanged.

- `server/internal/handler/chat_title.go` (292 LOC) — full upstream
  logic: async goroutine on `context.Background()` with 20s timeout,
  panic containment, `sanitizeChatTitle` (19-case matrix), CAS write
  via `UpdateChatSessionTitleIfCurrent`.
- `server/pkg/db/queries/chat.sql` — new sqlc query
  `UpdateChatSessionTitleIfCurrent` (compare-and-swap on id + title).
- `server/internal/handler/handler.go` — new optional field
  `ChatTitleProvider` (nil → feature off).
- `server/internal/handler/chat.go` — `SendChatMessage` triggers the
  async generator exactly once per session (zero prior user messages).
- 6 new tests: nil / disabled / empty / error / success / concurrent
  rename (CAS miss).

**Local adaptation note**: upstream wires the LLM through `h.LLM`, a
`*llm.Client` field on the Handler. The local fork has no such field —
LLM calls go through the runtime bridge and Pythia lab. The local
provider is therefore an injectable `ChatTitleProvider` interface; nil
means "feature off" and the rest of the upstream logic (sanitize /
CAS / panic containment / publish) is preserved verbatim.

### Search SQLSTATE 57014 → 503 mapping

Layered on top of the existing 5s Go-context timeout (added in 0.3.5
for MUL-4059 "search hangs forever"). Two failure modes now distinguishable:

- `SQLSTATE 57014` (Postgres-side statement_timeout) → **503 Service Unavailable**
- `context.DeadlineExceeded` (Go-side)         → **504 Gateway Timeout**
- everything else                               → 500 Internal Server Error

Frontend can tell apart "Postgres killed the query" from "handler slow".

Also closes a pre-0.3.5 hole: `SearchProjects` was never guarded by a
deadline — stuck pg queries on `/api/search/projects` would hang the
request forever. Now mirrors `SearchIssues` with the same 5s cap + 503/504
classification.

- `server/internal/handler/search_503.go` (65 LOC) — `searchStatementTimeout = 5s`
  (kept at 5s vs upstream's 3s because self-hosted without pg_bigm /
  pg_trgm indexes needs the wider cap), `searchStatementTimeoutOverride`
  testability hook, `isSearchStatementTimeout` via `pgconn.PgError` +
  `errors.As`.
- 3 new tests: `TestIsSearchStatementTimeout` (SQLSTATE mapping
  including wrapped errors), `TestEffectiveSearchStatementTimeout`
  (override hook), `TestSearchStatementTimeoutDefault` (5s guard).

### Comment NUL byte sanitization (GH #5388)

PostgreSQL TEXT rejects 0x00 with SQLSTATE 22021. A CLI `--content-file`
round trip can smuggle an embedded NUL byte that survives the JSON parse;
the upstream `multica issue comment create` path then hits an opaque 500
the CLI mis-renders as "server unavailable" and retries forever.

- `server/internal/handler/comment.go` — `CreateComment` + `UpdateComment`
  strip NUL bytes BEFORE the empty check, then re-validate. An all-NUL
  content (which would sanitize to '') returns 400 "content is required"
  rather than silently writing an empty row.
- 3 new tests: `TestCreateComment_StripsNullBytesInsteadOf500`,
  `TestCreateComment_AllNulContentRejected`,
  `TestUpdateComment_StripsNullBytesInsteadOf500` (edit-path parity).

### Issue sort + labels test cherry-picks (local-adapted)

`issue_sort_test.go` + `issue_labels_test.go` from upstream, adapted to
the LOCAL API surface:

- **sort columns**: upstream uses `sort=status / sort=updated_at`, which
  the LOCAL whitelist (server/internal/handler/issue.go:924) explicitly
  rejects. Adapted to `sort=created_at + sort=priority`. The 400-on-
  unknown-sort / bad-direction rejection tests are preserved verbatim —
  those are security / API-stability contracts the local fork cares
  about too.
- **label attach flow**: upstream has a `label_ids` body field on
  `CreateIssue` and tests atomic attach at create time. The LOCAL
  build intentionally keeps labels OUT of the create path (the nil
  pointer + omitempty contract is a deliberate optimization) and routes
  attach through `POST /api/issues/{id}/labels + DELETE /api/issues/{id}/labels/{labelId}`.
  `TestIssueLabelAttachDetach_FullFlow` exercises that full local flow
  including detach.
- **NOT cherry-picked** (intentionally): `issue_cancel_status_no_cancel_test.go`
  + `issue_reassign_no_cancel_test.go` — upstream MUL-4465 / MUL-4113
  assert that `status=cancelled` and reassignment do NOT cancel in-flight
  agent tasks. The local fork (server/internal/handler/issue.go:2881-2890 +
  3520-3521) explicitly KEEPS user-initiated cancel-on-cancelled
  semantics: "cancellation is a user-initiated terminal action that
  should stop execution". These two product decisions are mutually
  exclusive — porting the upstream tests would break the local 0.3.5
  ship contract.

5 new tests: `TestListIssuesSortsByCreatedAt`,
`TestListIssuesRejectsUnknownSort`, `TestListIssuesRejectsBadDirection`,
`TestCreateIssueResponseOmitsLabels`, `TestIssueLabelAttachDetach_FullFlow`.

### Admission contract types (MUL-4525) — shape only

`server/internal/handler/admission.go` (179 LOC) introduces the unified
execution-admission contract surface (`DispatchStatus / DispatchReasonCode
/ DispatchTarget / DispatchOutcome`) so the same shape can be reused
across comment mention, autopilot run-now, lab dispatch, and chat mention
paths. Plus the extracted `decidePostMergeMiss` decision rule and
`writeDispatchBlocked` helper.

**Local adaptation**: the upstream extract puts these behind a new
`server/internal/dispatch/` package. The local fork keeps the types in
`handler/` because (a) the existing call sites already live in handler/
and (b) introducing a new internal package is more invasive than warranted
for the single-user / self-hosted scope. Wire format matches the
upstream shape verbatim.

**NOT YET WIRED** into the actual dispatch paths:
- `CreateComment` / `UpdateComment` mention (TaskService.EnqueueTaskForMention)
- autopilot manual run-now
- `claude_science_lab` dispatch (handler/issue_lab_dispatch.go)
- `mythos_supervise` path (special-cased)

Each of those gets a follow-up commit that surfaces the outcome on the
wire. Doing them in one shot would touch 4 call sites + 4 response
schemas + frontend deserialization — a much larger blast radius than
necessary for the contract definition itself.

8 admission test cases + 2 dispatch-outcome JSON shape tests + 1
reason-code wire-values lock test = 17 new subtests.

## Verification

- `pnpm typecheck` → 6/6 packages green
- `pnpm turbo test --filter='@multica/views' --filter='@multica/core'`
  → 141 files / 1277 tests + 63 files / 642 tests, all pass
- `cd server && go test -p 1 ./internal/handler/` → 7.9s, all green
  (one flaky failure under `-p 4` due to test ordering + shared DB
  rows; passes under `-p 1` and standalone runs).
- Migration 158 applied to live DB; columns + indexes present.
- New test files compile clean; no new lint regressions.

## Deferred to 0.3.45

- **Agent Builder** (`handler/agent_builder.go` + `agent-creation-studio.tsx`):
  needs new schema migration for hidden system agent + `CreateAgentBuilder`
  sqlc query + feature-flag registration. Local feature-flag path is
  `server/internal/featureflagdispatch/` (not the upstream
  `featureflags` package). Scope: 1 session.
- **Property system (lightweight v1)**: 1032 LOC handler + 7 types + 8
  sqlc queries + 2 migrations (`issue_property_definition` +
  `issue_property_value`) + 2 frontend pickers + i18n + 4-language types.
  V1 starts with text + select + date only. Scope: 1-2 sessions.
- **Comment reconcile / merge / reply-authz** test cherry-picks: blocked
  on admission contract call-site wiring (above).

## Risk notes

- No external schema migrations beyond 158 (forward-only, ADD COLUMN IF
  NOT EXISTS, no DROP).
- No breaking changes to existing handlers' wire shapes — every new
  field is either additive (`omitempty`) or gated by feature flag.
- Labs mutex contract preserved: `issue.go:2153` and migrations 155 / 157
  untouched.
- Forward-only migration policy preserved: 158 is purely additive.
- i18n selector block-body crash contract preserved (only arrow expressions
  in new code; verified by ESLint).
- Pre-update snapshot mandatory before any DMG rebuild (see Task #12).
## Ship chain results (2026-07-18)

- **pre-update-snapshot** → app 767M / 13 tables / 80 configs / 817 KB
  files / `git tag pre-update-20260718-202107`
- **migrate up** → 158_project_dates applied
- **bundle-cli** → 3 Go binaries + 158 migration + PG manifest + Pythia
  + claude-science + llm-wiki + code-canvas into `resources/`
- **electron-vite build** → clean
- **electron-builder + hdiutil** → 290M DMG at
  `dist/multica-desktop-0.3.44-mac-arm64.dmg`
- **cold-start three-check** (post `open /Applications/Multica.app`):
  - `lsof -nP -iTCP:5432 -sTCP:LISTEN` → `postgres` listening
  - `lsof -nP -iTCP:8090 -sTCP:LISTEN` → `server` listening
  - `curl -s http://localhost:8090/health` → `{"status":"ok"}` in 5s
- **`/Applications/Multica.app` Info.plist**:
  - `CFBundleShortVersionString = 0.3.44`
  - server binary ldflag: `-X main.version=0.3.44`
- **Row parity** (vs 0.3.43 gold baseline):
  - workspace 1 (unchanged)
  - issue 200 (drift +116 from local usage; expected)
  - comment 1022 (drift +545; expected)
  - agent 85 (drift +47 from 0.3.32 mythos + claude_science squads)
  - project 10 (new column; expected)

## Known follow-up

- **`package.mjs::deriveVersion`** in the localized fork has a sparse-git
  fallback gap: when `git describe --tags --always --dirty` returns the
  pre-update marker tag (e.g. `pre-update-20260718-202107-dirty`) instead
  of a real `v0.3.44`, `electron-builder` ends up rendering
  `0.0.0-gpre-update-...` in the DMG filename and the raw Info.plist
  template. A fix in `package.mjs` (mirror of `bundle-cli.mjs`'s
  pre-update-tag guard) is in the working tree but did not survive the
  `electron-builder` rerun in this session — the DMG was rebuilt with
  `hdiutil` after Info.plist was patched to `0.3.44` so the shipped
  artifact is correct. Next ship: integrate the package.mjs fix into
  the standard `pnpm --filter @multica/desktop package` flow.
  See `apps/desktop/scripts/package.mjs` working tree.
