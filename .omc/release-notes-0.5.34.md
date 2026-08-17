---
name: release-notes-0.5.34
created: 2026-08-18T09:30:00Z
updated: 2026-08-18T09:30:00Z
---

# 0.5.34 Release Notes — MUL-6243 Frontend Complete (2026-08-18)

Closes the MUL-6243 (per-workspace custom issue statuses) frontend half. The feature is now end-to-end shippable. Backend shipped in 0.5.33, frontend lands here. 4 atomic commits, all green.

## Highlights

### TS — `feat(core)` `18198caf4`
- `packages/core/types/issue-status.ts` (new): `IssueStatusCategory` 7-key union + `IssueStatusEntry` + `ListIssueStatusesResponse` envelope + Create/Update requests
- `packages/core/api/schemas.ts`: `IssueStatusEntrySchema` (`.loose()` + defaults: `color="#6b7280"`, `is_system=false`, `position=0`, `description=""`, `archived_at=null`) + `ListIssueStatusesResponseSchema` + `EMPTY_LIST_ISSUE_STATUSES_RESPONSE` (7 built-in categories seeded for older backends)
- `packages/core/api/client.ts`: 4 methods (`listIssueStatuses` / `createIssueStatus` / `updateIssueStatus` / `archiveIssueStatus`) with bearer + workspace headers
- `packages/core/api/schemas.test.ts`: 8 new test cases (load-bearing zod defensive parsing)

### CLI — `feat(cli)` `394a53cb0`
- `validateIssueStatus` → format-only (lowercase + `[a-z0-9_]+` + 1-64 chars). Server's `issuestatus.Resolve()` is the workspace-truth source.
- `cmd_issue_test.go`: 3 new tests (built-ins + custom + malformed)
- `cmd_lab.go`: `multica lab delegate --status` flag (default `todo`); reuses format-only validator
- `cmd_lab_test.go`: `TestLabDelegateValidateIssueStatus` covers 7 built-ins + 2 customs + 1 malformed

### SKILL — `docs(skills)` `da1225de8`
- `multica-working-on-issues/SKILL.md`: added "Read each sub-issue's `status_category`" paragraph (children sub-issue per-stage done counter is category-aware via `issuestatus.Effective()`)
- `working-on-issues-source-map.md`: distinguished "CLI counter at `cmd_issue.go:802` still reads literal `status` (fork CLI scope was `validateIssueStatus`)" from "Go stage barrier at `issue_child_done.go:251` IS category-aware via `isTerminalChildStatus(issuestatus.Effective(...))`" + API field `issue.go:46` `StatusCategory` shipped in 0.5.33

### Schema fallback — `fix(core)` `f0eb6bd6c`
- `EMPTY_LIST_ISSUE_STATUSES_RESPONSE.categories` seeded with the 7 canonical built-ins so a client talking to a server that predates the endpoint still has the canonical list. Test switched to `parseWithFallback` (zod `.loose()` only allows extra fields on objects — non-object inputs still throw; matches upstream `446080bd5:1480-1494`). Separate fixup commit per the 0.5.15 lesson (slim/legacy mirror).

## Verification
- `pnpm typecheck --force` 6/6 (0 cached, 34.6s)
- `go build ./...` exit 0
- `go test ./cmd/multica/` ok 1.041s
- `cd packages/core && npx vitest run api/schemas.test.ts` 63/63 passed
- Ship chain: 4a/7 integrity check PASS → cold-start PASS, server 0.5.34

## MUL-6243 now end-to-end
With `FF_CUSTOM_ISSUE_STATUSES=true`, workspace admins can:
- `GET /api/issue-statuses?include_archived=` → see the 7 built-ins + any customs
- `POST /api/issue-statuses` → create a custom status in a category (backed by the 7-category CHECK + format constraint)
- `PATCH /api/{id}` → update name/description/color
- `DELETE /api/{id}` → archive (idempotent; refuses if issues still use the status — owner/admin reassign first)
- Frontend: `IssueSchema.status_category` carries the canonical category; `EMPTY_LIST_ISSUE_STATUSES_RESPONSE` keeps the picker usable against older backends
- CLI: `multica issue create --status in_qa` + `multica lab delegate --status ready_to_merge` accept any format-valid key; server resolves against the workspace catalog

Deferred (not blocking MUL-6243 usability): UI settings surface to manage the catalog (`/api/issue-statuses` CRUD in the fork's labs/settings UI). Upstream shipped zero views changes for the same reason — matches upstream scope.

## Deferred to 0.5.35+
- MUL-6286 (actor/multi_actor properties) — needs `@multica/core/properties` base
- MUL-5991 (jcode ACP effort) + 0c69f1f95 (Hermes resume-auth) — user-deferred
- Upstream new commit scan (last scan was `9d6c0c81e` + 12 commits through 2026-08-17)

Related memory: `0.5.33-mul6243-backend-mul6291-2026-08-18.md`.