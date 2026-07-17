# 0.3.22 ship log

**Ship date**: 2026-07-14
**Predecessor**: 0.3.21 (0.3.22.1 partial PG-binary-tree fix)
**Builder**: jjy1000 (auto-built)
**Cold start**: PASS (port 5432 / 8090 LISTEN, /health 200, row parity 1/164/862/80)

## Highlights

### Claude Research Lab (consolidation of 0.3.20 双 flag)
- New single Labs flag `claude_science_lab` replaces the 0.3.20 pair
  `claude_science` + `claude_science_runtime`. One sidebar entry,
  one view (`/experimental/claude-lab`), one runtime gate.
- New view `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx`
  with six capability tabs: Plan / Chat / Artifact / Forecast /
  Code / Knowledge. The Forecast tab reuses `<PythiaDashboard />`
  (forceSample) so the lab inherits free prediction visualisation.
- Runtime sandbox + Skill catalog + install handler stay under the
  new flag key; the HTTP route prefix
  `/api/experimental/claude-science-runtime/*` is unchanged for
  wire-compat with the 0.3.20 Skill adapter.
- Old flag keys `claude_science` / `claude_science_runtime` removed
  from `server/internal/experimental/catalog.go`; existing
  `experimental_resource_lock` rows under those sources continue to
  validate (forward-only migration 154 widened the CHECK).

### Migration 154 — forward-only schema additions
- `experimental_claude_runtime_session.lab_id` (nullable UUID)
- `experimental_runtime_artifact.lab_id` (nullable UUID)
- `experimental_resource_lock.experimental_source` CHECK widened to
  include `claude_science_lab` and `mythos_swarm`; old values still
  validate. Down migration narrows the CHECK; lab_id columns are
  retained per the forward-only contract.

### OpenMythos Boost Badge
- New `packages/views/issues/components/mythos-boost-badge.tsx` —
  small "OpenMythos" pill rendered next to the agent activity
  indicator on issue list rows and board cards when `mythos_swarm`
  flag is on. HoverCard tooltip explains the prelude / loop / coda
  topology. Breathing pulse animation `mythos-boost-pulse` in
  `packages/ui/styles/base.css`; respects `prefers-reduced-motion`.

### Bidirectional cross-link in agent-lab views
- `agent-self-optimization-view.tsx` and `constitution-agent-view.tsx`
  now render a `CrossLinkSection` that explains the propose-and-
  review contract (SkillOpt-Multica proposes, CSIL reviews). Both
  views still flag-gated; both ship Chinese copy.

## Verification

- `pnpm typecheck` (desktop): 0 errors
- `pnpm --filter @multica/desktop test`: 38 test files / 312 tests passed
- `pnpm --filter @multica/desktop lint`: 17 pre-existing errors
  (pythia-manager / server-manager / tab-content / tab-store); 0 new
- `go vet ./internal/experimental/... ./internal/handler/...
  ./internal/middleware/... ./cmd/server/...`: 0 warnings
- `go test -race -count=1 ./internal/experimental/...`: PASS
- `go build ./...`: 0 errors
- pre-update snapshot: 766 MB .app.bak + PG CSV + KB rsync
- bundle-cli: 3 binaries + 154 migrations + PG manifest + Pythia +
  OpenScience prompts + claude-science manifest bundled
- electron-vite build: 0 errors, 11.25 MB renderer bundle
- electron-builder package: DMG 242 MB + .app signed
- cold start: 5432 + 8090 LISTEN within 6 s, /health 200, row
  parity workspace=1 / issue=164 / comment=862 / agent=80

## Known follow-ups (deferred to 0.3.23+)

- 0.3.23 PR-A: ChatWindow `wsId` prop + ExperimentalChatPane +
  fully-wired ClaudeLabView Chat tab
- 0.3.23 PR-B: drop `upsertClaudeScienceWorkspace`, install handler
  writes to active workspace tagged via `metadata.lab_id`
- 0.3.23 PR-C: vendor cleanup (`resources/claude-science/` + 3
  vendor dirs removed, ~150 MB DMG savings)
- 0.3.24: real forecast SSE endpoint for `claude_science_lab`
- 0.3.25: low-code canvas in ClaudeLabView Code tab
- 0.3.26: 5 literature connectors
- 0.3.27: plan-only mode + Research Plan Timeline

## Files changed (33 total)

Server:
- server/internal/experimental/catalog.go
- server/internal/experimental/lock.go
- server/internal/handler/claude_science_runtime.go
- server/internal/handler/install_claude_science.go
- server/cmd/server/router.go
- server/cmd/multica/cmd_experimental.go
- server/internal/experimental/{smoke,registry,runtime_gc,panic_context,safety}_test.go
- server/internal/handler/{claude_science_runtime_smoke,experimental_resources}_test.go
- server/internal/middleware/experimental_burst_test.go
- server/migrations/154_claude_lab_consolidation.{up,down}.sql

Desktop:
- apps/desktop/resources/experiments/claude_science_lab/manifest.json (new)
- apps/desktop/src/renderer/src/routes.tsx
- apps/desktop/src/renderer/src/pages/claude-lab-view.tsx (new)
- apps/desktop/src/renderer/src/pages/agent-self-optimization-view.tsx
- apps/desktop/src/renderer/src/pages/constitution-agent-view.tsx
- apps/desktop/src/renderer/src/components/experimental-artifact-view.tsx
- apps/desktop/src/renderer/src/components/experimental-artifact-view.test.tsx
- apps/desktop/src/main/index.ts
- apps/desktop/src/main/experimental/upstream-registry.ts
- apps/desktop/src/main/experimental-safety.test.ts
- apps/desktop/src/main/experimental/manager-factory.ts
- apps/desktop/scripts/build-claude-science-manifest.mjs
- apps/desktop/scripts/bundle-cli.mjs
- apps/desktop/scripts/setup-claude-science.sh

Shared:
- packages/views/layout/app-sidebar.tsx
- packages/views/locales/{en,zh-Hans,ja,ko}/layout.json
- packages/views/issues/components/{mythos-boost-badge,list-row,board-card}.tsx
- packages/ui/styles/base.css

## Files deleted (5 total)

- apps/desktop/resources/experiments/claude_science/manifest.json
- apps/desktop/resources/experiments/claude_science_runtime/manifest.json
- apps/desktop/src/renderer/src/pages/claude-science-view.tsx
- apps/desktop/src/renderer/src/pages/claude-science-runtime-view.tsx
- apps/desktop/src/main/claude-science-manager.ts
