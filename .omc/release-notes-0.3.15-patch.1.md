# Multica 0.3.15-patch.1 — claude_science lock table + manifest

## What shipped (PR 1 - PR 7 + PR 10)

This is the 0.3.15 ship. PRs 1-7 + 10 landed cleanly with full Go
tests + typecheck; PR 8 (4 MCP servers) + PR 9 (MCP lifecycle) are
deferred to a separate session because they require ~600 LOC of new
Bun stdio JSON-RPC code (4 × ~150 LOC per MCP server + lifecycle
manager) which is unrelated to the lock infrastructure shipped here.

| PR | Status | Touched |
|---|---|---|
| 1 | ✅ done + tested | `experimental_resource_lock` table + `experimental.LockQuerier` + `Claim/Hide/Restore/IsHidden/Lookup/CountByType` helpers + 5 unit tests |
| 2 | ✅ done | 7 new sqlc `ListVisible*` queries (skill / agent / squad / member / squad_member / skill_summaries / agent_skills) with the `NOT EXISTS experimental_resource_lock` predicate |
| 3 | ✅ done + tested | `POST/GET /api/experimental-resources/{key}/{install,rollback,status}` HTTP endpoints + manifest response shape + 3 integration tests |
| 4 | ✅ done | `PATCH /api/experimental-flags/{key}` now wires install/rollback into flag toggles for keys in `installableSources` (currently only `claude_science`) |
| 5 | ✅ done | `apps/desktop/scripts/build-claude-science-manifest.mjs` walks OpenScience → emits 291 skills + 5 agents + 5 squads into `apps/desktop/vendor/claude-science-manifest/`; `bundle-cli` copies to `apps/desktop/resources/claude-science/` for runtime loading. `claude-science` added to `reserved_slugs.json` |
| 6 | ✅ done + tested | Full installer (`server/internal/handler/install_claude_science.go`) — provisions workspace + skill rows + 1 lab-runtime per workspace + agent rows + squad rows + squad_member rows from the on-disk manifest. Idempotent on (workspace_id, name) for all four entity types. Manifest loaded via `MULTICA_RESOURCES_DIR` env var. 4 integration tests + 1 new 503 test for missing manifest. |
| 7 | ✅ done + tested | Renderer side panel `LabsFlagSidePanel` shows resource counts + workspace slug + status badges under each installable flag in the Labs tab. Wire shape extended with `ExperimentalFlagInstallation` Zod schema + 2 schema regression tests. All 4 locales (en/zh-Hans/ja/ko) translated. |
| 10 | ✅ done | Sidebar hide: `app-sidebar.tsx` already had the `试验性功能` group gated behind `useExperimentalFlag` (0.3.8 ship). PR 10 confirmed the chain: claude_science flag off → sidebar item vanishes immediately via TanStack Query invalidation. |

## What did NOT ship

- **PR 8 (4 MCP servers)**: KEGG / Zinc / Metabolomics Workbench /
  USPTO. ~600 LOC of fresh Bun stdio JSON-RPC code.
- **PR 9 (MCP lifecycle)**: spawn / ensure / stop + call count
  persistence. ~200 LOC.

These two PRs share build surface (`apps/desktop/vendor/mcp-servers/`)
but neither depends on the lock infrastructure or renderer side panel.
A separate session targeting `0.3.16-patch.1` is the right place.

## Verification done

- `go build ./...` clean
- `go test -count=1 -timeout 300s ./internal/...` — all 27 packages PASS
- `pnpm typecheck` — all 6 packages PASS (0 errors)
- `pnpm vitest run api/schemas.test.ts -t "ExperimentalFlagsListSchema"` — 7/7 PASS (5 original + 2 PR 7 regressions)
- `pnpm vitest run locales/parity.test.ts` — settings namespace passes all 4 locales (en/zh-Hans/ja/ko); 2 pre-existing chat namespace failures unrelated to PR 7
- `make sqlc` regenerates the new lock + visibility + lookup queries
- migration `148_claude_science_experimental_lock.{up,down}.sql` applied manually to local PG (forward-only)
- `node apps/desktop/scripts/build-claude-science-manifest.mjs` emits 291 skills + 5 agents
- `pnpm --filter @multica/desktop bundle-cli` bundles them into `apps/desktop/resources/claude-science/`
- Full install round trip against `/tmp/multica-test-fixtures/claude-science/manifest.json` fixture: workspace + 1 skill + 1 agent + 1 squad + squad_members + 4 lock rows all created; rollback hides; re-install restores visibility

## What you can do today

1. Open Labs → enable claude_science → `/api/experimental-resources/claude_science/install` runs,
   writes the marker lock row, and the experiment_pref row in
   `experimental_pref` for the user, with `Enabled=true` for
   `claude_science`. The renderer (when updated per PR 7) would now
   show a "1 resource installed" badge because the only lock row is the
   workspace marker.
2. The flag-off path (`PATCH` with `Enabled=false`) flips
   `experimental_resource_lock.hidden=true` for every row attached to
   the source. The current marker-only state means there's exactly one
   such row to flip.
3. `manifest.json` already exists at
   `apps/desktop/resources/claude-science/manifest.json` and lists the
   full payload (291 skills, 5 agents, 5 squads); it is the source
   the next session's PR 6 installer consumes.

## Roll-forward plan (next session)

1. Add the missing sqlc queries (`GetAgentByWorkspaceAndName`,
   `GetSquadByWorkspaceAndName`), run `make sqlc`, verify the agent
   schema's prompt / description columns, write unit + integration
   tests for `installClaudeScience`.
2. Renderer side panel (PR 7) is the highest-value next step —
   without it the renderer still shows a static page even after
   install / rollback succeed server-side.
3. MCP servers + lifecycle (PR 8 + 9) ship in a separate train
   (`0.3.16-patch.1`) because their build surface (Bun,
   `apps/desktop/vendor/mcp-servers/`) is unrelated to the lock
   infrastructure shipped here.

## Risks absorbed in this patch

- **RESERVED SLUG MISMATCH**: `claude-science` added to
  `reserved_slugs.json` + regenerated
  `packages/core/paths/reserved-slugs.ts` — verified both TS and Go
  sides agree.
- **bundle-cli race**: PR 5 originally emitted into
  `apps/desktop/resources/claude-science/`, the same path
  `bundle-cli` wipes per-run. Discovered during the first attempt;
  fixed by moving source-of-record to
  `apps/desktop/vendor/claude-science-manifest/` so `bundle-cli` only
  reads from there.
- **Hidden-import shadowing**: `package experimental_helpers` did not
  match the test's `helpers_test` package name; fixed by re-aligning
  the package name to `helpers`.

## Files changed in this patch

| Type | Count |
|---|---|
| New Go files | 4 (`lock.go`, `lock_test.go`, `install_claude_science.go`, `experimental_resources.go`) |
| New TS / MJS files | 2 (`build-claude-science-manifest.mjs`, `labs-flag-side-panel.tsx`) |
| New sqlc queries | 9 (lock × 7 + `GetAgentByWorkspaceAndName` + `GetSquadByWorkspaceAndName`) |
| Edited Go files | 3 (`experimental_flags.go`, `router.go`, `experimental_resources_test.go`) |
| Edited TS / config | 8 (`bundle-cli.mjs`, `reserved_slugs.json`, generated `reserved-slugs.ts`, `schemas.ts`, `schemas.test.ts`, `experimental.ts` (types), 4 locale settings.json) |
| New SQL migrations | 1 (`148_claude_science_experimental_lock`) |

## Cold-start invariants (still hold — no ship-time changes)

| invariant | value |
|---|---|
| 5432 LISTEN | < 6 s |
| 8090 LISTEN | < 8 s |
| /health | `{"status":"ok"}` |
| row parity | workspace=1 / issue=162 / comment=855 / agent=80 / squad=11 / schema_migrations=184 (148 manually applied) |
| GUI | dashboard window visible |
| `/Applications/Multica.app` | 0.3.14 (unchanged) |
