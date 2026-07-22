---
name: release-notes-0.3.57
created: 2026-07-22T10:48:00Z
updated: 2026-07-22T10:48:00Z
status: complete
---

# Multica 0.3.57 — retire `constitution_agent` lab

## Problem

`constitution_agent` lab (charter-guardian agent + 3 autopilots:
CTR 三周评审 / CSIL 宪章自优化 / TAOL 任务-智能体优化) shipped
as an opt-in toggle in 0.3.20. The lab enforces a workspace-level
charter (charter v6, CSIL + CTR + TAOL tracks) and runs autonomous
review cycles. This is a product-level feature, not a Labs
experiment — the charter system belongs in workspace settings or
team admin, not behind a hidden flag in the experimental tabs.

Decision (2026-07-22): retire the entire plugin. Lab surface,
agent + 3 autopilots, builtin skill, install handler, sidebar
entry, and view are removed. Future charter work (if any) will
land in a dedicated workspace admin / settings surface, not as
a lab.

## What changed

### Resources (L1)

- Deleted `apps/desktop/resources/experiments/constitution_agent/`
  (manifest.json + skills/multica-constitution-agent/SKILL.md)
- Deleted `server/internal/service/builtin_skills/multica-constitution-agent/`
  (boot-loaded skill directory)
- Deleted `server/migrations/153_constitution_agent_visibility.{up,down}.sql`
  (visibility seed rows)
- Deleted the bundled copy at `apps/desktop/resources/server/migrations/153_*.sql`

### Server Go (L2)

- Deleted `server/internal/handler/install_constitution_agent.go`
- `server/internal/experimental/catalog.go` — removed
  `constitution_agent` Flag literal
- `server/internal/experimental/visibility.go` — removed
  `SourceConstitutionAgent` + `constitutionAgentIDs` helpers
- `server/internal/experimental/lock.go` — removed
  `SourceConstitutionAgent` constant
- `server/internal/handler/{autopilot,skill,agent,daemon,squad,issue,
  experimental_resources,labs_visibility_filter}.go` — removed
  constitution_agent branches in `filterLabsHiddenByDefault`,
  `shouldSkipDispatch`, `defaultLabLeaderForKey`, install handler
  registration, and visibility filter slices
- `server/internal/service/{autopilot,issue,builtin_skills,
  agent_self_optimization/source}.go` — removed related branches
- Tests in `visibility_test.go`, `builtin_skills_system_key_test.go`,
  `autopilot_tick_test.go` — dropped constitution_agent cases

### Desktop (L3)

- Deleted `apps/desktop/src/renderer/src/pages/constitution-agent-view.tsx`
- `apps/desktop/src/renderer/src/routes.tsx` — removed route entry
- `apps/desktop/src/main/experimental/manager-factory.ts` —
  removed from `staticFlagDescriptors`
- `apps/desktop/src/renderer/src/pages/agent-self-optimization-view.tsx` —
  `CrossLinkSection` body replaced with `null` (the section was a
  constitution-agent link)
- `apps/desktop/src/renderer/src/pages/agent-creation-studio-view.tsx` —
  removed `CompatToggle` / `compatConstitution` and the system_key
  toggle placeholder that pre-wired `constitution_agent_v1`

### Packages / shared (L4)

- `packages/views/locales/{en,ja,ko,zh-Hans}/layout.json` —
  removed `constitution_agent_enabled_badge` and
  `experimental_constitution_agent` keys (4 files)
- `packages/views/locales/zh-Hans/experimental.json` — emptied
  `compat_label` / `compat_hint` strings (keys kept for shape
  stability)
- `packages/views/layout/app-sidebar.tsx` — removed the
  `ScrollText` icon map entry for constitution_agent
- `packages/views/issues/components/issue-labs-section.tsx` —
  removed from `FLAG_ROUTE_SUFFIX`
- `packages/views/issues/components/lab-badge.tsx` — removed
  the `ScrollText` icon switch case + `LAB_BADGES` entry
- `packages/views/modals/create-issue.tsx` — removed
  `LAB_DISPLAY_LABELS` entry + `experimentalLabRouteFor` case
- `packages/core/types/agent.ts` — updated `system_key` doc
  comment to reflect that the 0.3.45 `constitution_agent_v1`
  placeholder is now retired

### New migration (forward-only drop)

- `server/migrations/165_retire_constitution_agent.{up,down}.sql`
  + bundled copy at `apps/desktop/resources/server/migrations/165_*.sql`

  The up migration:
  1. `DELETE FROM experimental_resource_visibility WHERE flag_key = 'constitution_agent';`
  2. `UPDATE agent SET archived_at = COALESCE(archived_at, now()) WHERE name = '宪法智能体';`
     (the 3 autopilots were already archived at 0.3.20 ship —
     status='archived' — so the migration only re-affirms that
     defensively for freshly-provisioned workspaces)
  3. `UPDATE autopilot SET status = 'archived' WHERE title IN ('宪章智能体 · 宪章三周评审（CTR）', '宪章智能体 · 宪章自优化循环（CSIL）', '宪法智能体 · 任务-智能体优化循环（TAOL）');`

  The down migration restores the visibility rows + clears
  `archived_at` + flips the autopilot status back to 'active'.

## Backward compatibility

The system-prompt binding stub in
`server/internal/handler/daemon.go::loadSystemPromptBinding` keeps
`case "constitution_agent_v1": return ("", false)` with a
retirement comment. Any user-saved agent carrying
`system_key="constitution_agent_v1"` no longer receives the
charter prompt; the binding now resolves to no prompt and the
agent runs without the charter. This matches the `system_key`
column contract: unknown keys are silently ignored, not crashed.

## Verified

- `go build ./...` → 0 errors
- `pnpm --filter @multica/desktop typecheck` → 0 errors
  (both `typecheck:node` and `typecheck:web`)
- `go run ./cmd/migrate up` → migration 165 applied cleanly
- Cold start: `lsof -nP -iTCP:5432` + `lsof -nP -iTCP:8090`
  listeners within 10s; `curl /health` → `{"status":"ok"}`
- Row parity: workspace=1, issue=220, comment=1346, agent=92
  (91 active + 1 newly-archived `宪法智能体` via mig 165),
  agent_archived=14 (+1 vs 0.3.56 baseline)
- `grep -rln 'constitution_agent\|ConstitutionAgent' server/ apps/ packages/`
  → 0 active code paths; all residual hits are documentation
  comments, the system-prompt binding stub, and forward-only
  migration files (153 + 165)

## What did NOT change

- Migrations 153 + 165 stay on disk (forward-only policy,
  CLAUDE.md §Data Safety & Version Upgrades).
- The 3 autopilots' archived_at=0.3.20 status is preserved
  (no destructive UPDATEs to user data).
- Postgres user / DB / config / KB vault — all untouched.
- apps/web (no references to constitution_agent — confirmed
  by grep).
