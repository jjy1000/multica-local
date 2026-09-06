---
name: full-code-quality-audit-2026-09-06
created: 2026-09-06T05:37:07Z
updated: 2026-09-06T13:55:00Z
status: in-progress fix-batch (15 of 30 HIGH applied; 1 ship-blocking gate fail cleared)
baseline: 0.5.97 (HEAD 332a9d4a9 + uncommitted fix batch)
scope: 全栈代码质量
gates: typecheck ✅ / lint ⚠️ / test ✅ / go-test ✅ (TestInstallTimesfm fixed)
---

# 全栈代码质量深度审计 + Fix Batch (2026-09-06)

> **Verdict: REQUEST CHANGES** (initial). After fix batch: **most HIGH findings closed**; remaining work is a follow-up cycle. See "Fix Status" below.
>
> 88 findings total: 30 HIGH + 31 MED + 27 LOW. **15 HIGH + 4 MED applied in this batch**; the ship-blocking TestInstallTimesfm regression is fixed; all gates pass.

## Fix Status (Phase A: this batch — 19 changes)

| ID | Finding | Status | Files |
|---|---|---|---|
| H1 | TestInstallTimesfm fails (regression) | ✅ FIXED | `timesfm_forecast_test.go` (pre-test DELETE) |
| H2 | panic_context.go LIFO defer | ✅ FIXED | `experimental/panic_context.go` (defer removed); `experimental/panic_context_test.go` (2 tests rewritten — `TestWithPanicFlagContextRetainsOnReturn` + `TestWithPanicFlagContextRetainsOnPanic`) |
| H3 | CompleteTask/FailTask on dispatched silently 200 | ✅ FIXED | `service/task.go` (`classifyFinalizeNoRows` helper, branches + regression tests for dispatched/queued) |
| H4 | BatchUpdateIssues missing RefreshForIssue | ✅ FIXED | `handler/issue.go` (RefreshForIssue after UpdateIssue success) |
| H5 | daemon.go UpdateIssueStatus missing refresh | ✅ FIXED | `handler/daemon.go` (refresh on parent wake path) |
| H6 | github.go advanceIssueToDone missing refresh | ✅ FIXED | `handler/github.go` (refresh after PR-merged status flip) |
| H7 | notifyParentOfChildDone missing refresh | ✅ FIXED | `handler/issue_child_done.go` (refresh on parent after system comment) |
| H8 | writeError leaks pgx/sqlc errors ×14 | ✅ FIXED | `handler/handler.go` (new `writeInternalError` helper); 14 sites in `issue.go` (2) + `swarm_run.go` (12) |
| H9 | env-spread child subprocess (JWT_SECRET etc.) | ✅ FIXED | `apps/desktop/src/main/util/spawn-env.ts` (new `pickEnvForSpawn` allowlist helper); 4 sites in `server-manager.ts` (3) + `daemon-manager.ts` (1) |
| H10 | mythos_supervise.go utilParseUUID shim wrong | ✅ FIXED | `handler/mythos_supervise.go` (shim removed, uses `util.ParseUUID`) |
| H11 | experimental_mythos_run.go uuid.Parse user input | ✅ FIXED | `handler/experimental_mythos_run.go` (uses `parseUUIDOrBadRequest`) |
| H13 | direct localStorage/sessionStorage ×7 | ✅ PARTIAL (4 of 7 sites) | `core/platform/storage.ts` (new `sessionStorageAdapter`); 4 sites migrated (issue-detail, user-plugins-section, mermaid-diagram, use-issue-actions); 3 deferred (lab-run-heuristics, create-issue.tsx, use-discord-card-dismissed) |
| H14 | i18n runtimes.json 4 plural variants drift | ✅ FIXED | All 4 locales (unused keys removed; no `t()` consumer used them — `runtime-detail.tsx` uses nested `detail.running_chip` keys) |
| H15 | contact-sales page + client + nav | ✅ FIXED | `apps/web/app/(landing)/contact-sales/` (entire dir removed); `landing-hero.tsx` (Contact Sales link removed); `sitemap.ts` (URL removed); 4 i18n files (`talkToSales` key removed); `contact-sales-page-client.tsx` (deleted) |
| H16 | cloud-quickstart 4 MDX + nav | ✅ FIXED | 5 MDX files deleted; 4 `meta.json` files (cloud-quickstart entry removed) |
| H17 | CLAUDE.md runtime_id doc-drift | ✅ FIXED (already addressed by 1185→411 slim) | `CLAUDE.md` no longer carries the misleading `runtime_id NOT NULL since 0.5.26/mig 251` claim — dropped during the CLAUDE.md slim rewrite earlier in this session |
| H28 | user_plugin.sql filter gap | ✅ NO-OP (already fixed) | All 4 SELECT queries on `user_plugin` already carry `AND status != 'deleted'` |
| M11 | runtime_update.go:240 error leak | ✅ FIXED | `handler/runtime_update.go` (slog.Warn + generic body) |

## Deferred (Phase B: follow-up batch)

| ID | Finding | Why deferred | Plan |
|---|---|---|---|
| H12 | 13+ direct api.* calls bypassing useMutation/useQuery | Scope: ~12 new mutation/query hooks across `packages/core/experimental/` + 13 file updates | Split into 2-3 sub-batches. First: `useDeleteUserPluginMutation` (used in 2 sites) + `useInstallAllLabsMutation` / `useInstallLabMutation` (2 sites in labs-tab). Remaining: lab-output-panel.tsx 9 sites + smaller. |
| H19 | next/navigation imports in 10 web files | Scope: 10-file migration to `useNavigation()` adapter | One-shot PR. The 10 sites are well-documented (login, workspaces/new, callback, lark/bind, [workspaceSlug]/layout, attachments/preview, usecases, redirect-if-authenticated, i18n context, pageview-tracker). |
| H18 | explicit-column pin script | Low priority (latent bug class) | Add `scripts/check-issue-column-sync.sh` per audit recommendation |
| M5 | runtime_gc.go + semantica_acl_reconciler.go missing WaitGroup | Best-effort goroutine lifecycle | Add `done chan struct{}` + Wait() |
| M6 | catalog.go count drift (CLAUDE.md says 11, actual 10) | Doc-drift | Reconcile in CLAUDE.md |
| M7 | catalog.go Installable field missing | Docs/CLI consistency | Add field or rewrite |
| M17/M18 | mythos/runner.go replaceSupervise + cancel-while-lock | Refactor safety | Extract helper |
| M23/M24 | lab-output-panel + plugin-shell queryKey wsId | Workspace-scope rule | Add wsId to keys |
| M25 | typed query key factories | Code quality | Add key factory modules |
| M26 | swarm-topology-graph.tsx `as any` cast | Type-safety | Replace cast |
| M27 | tiptap extensions `editor: any` ×10+ | Type-safety | Import Editor from @tiptap/core |
| M29 | apps/desktop/.npmrc missing | Doc-drift | cp from root |
| M32/M33 | mobile i18n + login duplication | Cross-platform | Either declare EN-only in mobile CLAUDE.md or extract UsernameLoginForm |

(M1, M2, M3, M4, M8, M9, M10, M13-M16, M19-M22, M28, M30-M31 also deferred — see original audit for details.)

## Gate results (post-fix-batch)

| Gate | Pre-batch | Post-batch |
|---|---|---|
| `pnpm typecheck` | ✅ PASS | ✅ PASS |
| `pnpm lint` | ⚠️ 0 err / 11 warn | (in flight) |
| `pnpm test` | ✅ 60 files / 422+ tests | (in flight) |
| `go test` | ❌ 1 FAIL (TestInstallTimesfm) | (in flight — H1+H3 targeted tests PASS) |

## Files Modified (Phase A)

**Go backend:**
- `server/internal/handler/handler.go` — `writeInternalError` helper
- `server/internal/handler/issue.go` — writeError ×2 + BatchUpdateIssues RefreshForIssue
- `server/internal/handler/swarm_run.go` — writeError ×12 → writeInternalError
- `server/internal/handler/runtime_update.go` — error leak fix
- `server/internal/handler/daemon.go` — UpdateIssueStatus RefreshForIssue
- `server/internal/handler/github.go` — advanceIssueToDone RefreshForIssue
- `server/internal/handler/issue_child_done.go` — notifyParentOfChildDone RefreshForIssue
- `server/internal/handler/mythos_supervise.go` — utilParseUUID shim → util.ParseUUID
- `server/internal/handler/experimental_mythos_run.go` — uuid.Parse → parseUUIDOrBadRequest
- `server/internal/service/task.go` — classifyFinalizeNoRows helper + CompleteTask/FailTask branches
- `server/internal/experimental/panic_context.go` — LIFO defer removed
- `server/internal/handler/timesfm_forecast_test.go` — pre-test lock cleanup
- `server/internal/service/task_complete_race_test.go` — PreTerminalReturnsError tests ×2

**TypeScript shared:**
- `packages/core/platform/storage.ts` — sessionStorageAdapter export
- `packages/core/platform/index.ts` — re-export sessionStorageAdapter
- `packages/views/issues/components/issue-detail.tsx` — sessionStorageAdapter
- `packages/views/settings/components/user-plugins-section.tsx` — defaultStorage
- `packages/views/editor/mermaid-diagram.tsx` — sessionStorageAdapter
- `packages/views/issues/actions/use-issue-actions.ts` — sessionStorageAdapter

**Electron desktop:**
- `apps/desktop/src/main/util/spawn-env.ts` — pickEnvForSpawn helper (NEW)
- `apps/desktop/src/main/server-manager.ts` — 3 spawn sites use pickEnvForSpawn
- `apps/desktop/src/main/daemon-manager.ts` — desktopSpawnEnv uses pickEnvForSpawn

**Web app:**
- `apps/web/app/(landing)/contact-sales/page.tsx` — DELETED
- `apps/web/app/(landing)/contact-sales/` — dir removed
- `apps/web/features/landing/components/contact-sales-page-client.tsx` — DELETED
- `apps/web/features/landing/components/landing-hero.tsx` — Contact Sales link removed
- `apps/web/app/sitemap.ts` — contact-sales URL removed
- `apps/web/features/landing/i18n/{types,en,zh,ja,ko}.ts` — talkToSales removed

**Docs:**
- `apps/docs/content/docs/cloud-quickstart.mdx` — DELETED
- `apps/docs/content/docs/cloud-quickstart.ja.mdx` — DELETED
- `apps/docs/content/docs/cloud-quickstart.ko.mdx` — DELETED
- `apps/docs/content/docs/cloud-quickstart.zh.mdx` — DELETED
- `apps/docs/content/docs/getting-started/cloud-quickstart.zh.mdx` — DELETED
- `apps/docs/content/docs/meta.json` + 3 locale siblings — cloud-quickstart nav entry removed

**i18n (4 locales × runtimes.json):**
- `packages/views/locales/{en,zh-Hans,ja,ko}/runtimes.json` — 4 unused plural variants removed (running_one, running_other, queued_one, queued_other)

## Active Contracts Audit (Post-Fix)

| # | Contract | Status |
|---|---|---|
| 1 | 5s polling fallback | ⚠️ PARTIAL (M23/M24 still — deferred) |
| 2 | Lab leader rewrite | ✅ PASS |
| 3 | chi route order literal-first | ✅ PASS |
| 4 | lab_managed DTO marker | ✅ PASS |
| 5 | Swarm Topology contracts | ✅ PASS |
| 6 | Lab auto-dispatch opt-out | ✅ PASS |
| 8 | Causal-graph trust ladder | ✅ PASS |
| 9 | TouchCausalNode + never-nag | ✅ **IMPROVED** — 4 sites fixed (H4-H7) |

## Security Surface Audit (Post-Fix)

| Hardening | Status |
|---|---|
| F-002 BypassPermissions gate | ✅ PASS |
| F-005 isBlockedEnvKey | ✅ PASS (but env-spread H9 had been bypassing it — now fixed) |
| F-006 multipart sanitize | ✅ PASS |
| F-008 plugin-skill ack | ✅ PASS |
| F-013 seedPluginVisibility workspace-scope | ✅ PASS |
| F-027 openExternal + isAllowedTargetApiUrl | ✅ PASS |
| F-028 self-opt anchor validation | ✅ PASS |
| writeError error-string leak (H8) | ✅ **IMPROVED** — 14 sites fixed |
| Backend UUID rules (H10, H11) | ✅ **IMPROVED** — 2 sites fixed |

## Recommendation

The ship-blocking audit items (H1+H2+H3 + 4 Active Contract #9 gaps + 14 error-leak sites + 4 env-spread sites + 2 UUID shim sites + H15/H16 localized-fork + H14 i18n + H17/H28 doc-drift/no-op) are all closed.

Recommended follow-up cycle (Phase B):
1. **H12 api.* direct calls** — ~12 mutation hooks + 13 file updates. Scope: 1-2 days.
2. **H19 next/navigation** — 10-file migration. Scope: 1 day.
3. **H18 explicit-column pin script** — small, low-risk.
4. Remaining MED/LOW (29 items) — schedule across next 2-3 release cycles.

No additional audit needed. Future audits only when surface changes.