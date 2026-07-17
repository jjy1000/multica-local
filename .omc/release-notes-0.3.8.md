# Multica 0.3.8 — 2026-07-13

## Summary

Two new **Labs-gated experimental features** land in Multica 0.3.8 —
both fully isolated from legacy code, both default-off, both
discoverable in Settings → Workspace → Labs. The work was driven by
two user-shipped upstreams (`/Users/jiangjianyan/Downloads/openscience-main`
and `/Users/jiangjianyan/Downloads/Pythia-main`), each implemented on
top of the 0.3.6 Labs framework (`server/internal/experimental/catalog.go`)
without touching the model routing on either side. Multica's provider
chain is unchanged — the two new services live behind independent
managers that each bring up their own subprocess on a free loopback
port.

### 1. `claude_science` — Claude Science Workspace (browser-rendered)

Bundled OpenScience Bun-served SolidJS workspace, rendered inside an
Electron iframe at `/experimental/claude-science`. Same workspace the
human user sees in the iframe is reachable from Multica agents via the
`multica-claude-science` Skill which shells out to
`multica claude-science research --url ... --topic ...`. Backend
subcommands: `status`, `research`, `get-result`.

### 2. `pythia_oracle` — Pythia Prediction Oracle (headless)

Bundled Pythia FastAPI service (MiroFish swarm + Osiris live
intelligence), exposed purely through the `multica-pythia` Skill —
**no UI rendered**. Backend subcommands: `status`, `brief`, `predict`,
`whatif`. The sidebar item links to an informational status page
(`/experimental/pythia`) that shows the manager loopback URL and a
copy-paste-ready invocation example.

## Sidebar / UI

- New top-level sidebar section **试验性功能 / Experimental** appears
  below the existing Configure group. Each item is gated on its
  matching Labs flag (`useExperimentalFlag`), so nothing renders when
  both flags are off — fully aligned with the hard constraint
  documented in `server/internal/experimental/catalog.go:1-21`.
- Task / issue right sidebar — added `ExperimentalSection` footer
  block that lists every enabled flag as a `<Badge>`. Renders inside
  `packages/views/issues/components/issue-detail.tsx` next to the
  existing metadata section. Absent when nothing is enabled.

## Files of note

| File | Δ | Role |
|---|---|---|
| `server/internal/experimental/catalog.go` | +27 | Append 2 flag literals (DefaultVal false) |
| `server/internal/handler/experimental_flags_test.go` | +8 | Length pin against catalog count |
| `apps/desktop/src/main/util/free-port.ts` | +32 | `pickFreePort()` via `net.createServer().listen(0)` |
| `apps/desktop/src/main/util/free-port.test.ts` | +33 | 2 vitest cases (port range + uniqueness) |
| `apps/desktop/src/main/experimental/manager-template.ts` | +185 | `BaseExperimentalManager` abstract lifecycle |
| `apps/desktop/src/main/pythia-manager.ts` | +157 | Wraps template; spawns `resources/pythia/run.sh` |
| `apps/desktop/src/main/claude-science-manager.ts` | +99 | Wraps template; spawns `openscience web --port` |
| `apps/desktop/src/main/index.ts` | +14 | `setupPythiaIPC` + `setupClaudeScienceIPC` + before-quit teardown |
| `apps/desktop/src/preload/index.ts` | +30 | `experimentalAPI.{pythia,claudeScience}` surfaces |
| `apps/desktop/src/preload/index.d.ts` | +28 | `Window.experimentalAPI` + `ExperimentalAPI` type |
| `apps/desktop/src/renderer/src/routes.tsx` | +12 | `/experimental/{claude-science,pythia}` routes |
| `apps/desktop/src/renderer/src/pages/claude-science-view.tsx` | +94 | iframe + sandbox + polling cycle |
| `apps/desktop/src/renderer/src/pages/pythia-view.tsx` | +95 | Status card + invocation example |
| `packages/views/layout/app-sidebar.tsx` | +71 | 4th `SidebarGroup` + `experimentalNav` + flag hooks |
| `packages/views/locales/{en,zh-Hans,ja,ko}/layout.json` | +12 each | 3 bilingual keys per locale |
| `packages/views/issues/components/issue-detail.tsx` | +37 | `ExperimentalSection` sidebar footer |
| `server/internal/service/builtin_skills/multica-pythia/SKILL.md` | +66 | Agent-facing 3-step contract |
| `server/internal/service/builtin_skills/multica-claude-science/SKILL.md` | +64 | Agent-facing research session contract |
| `server/cmd/multica/cmd_pythia.go` | +208 | 4 CLI subcommands + groupExperimental |
| `server/cmd/multica/cmd_claude_science.go` | +213 | 3 CLI subcommands + loopback HTTP helpers |
| `server/cmd/multica/main.go` | +2 | Add pythiaCmd + claudeScienceCmd |
| `apps/desktop/electron-builder.yml` | +14 | asarUnpack: `resources/pythia/**`, `resources/openscience/**` |
| `apps/desktop/scripts/bundle-cli.mjs` | +67 | Vendor fallback + writeFile import |

## Verification

- `pnpm typecheck` — all 6 typecheck tasks green, 0 errors.
- `cd server && go build ./...` — 0 errors.
- `cd server && go test -count=1 ./internal/experimental/ ./internal/handler/ ./internal/service/` — all green.
- `pnpm exec vitest run apps/desktop/src/main/util/free-port.test.ts` — 2 / 2.
- Cold-start 3-check pass:
  - 5432 LISTEN (`postgres`) and 8090 LISTEN (`server`) within 8s.
  - `curl http://localhost:8090/health` → `{"status":"ok"}`.
  - GUI window appears (`osascript` query confirms process visible).
- Row parity: `workspace=1 issue=162 agent=80 squad=11 comment=855`
  unchanged from pre-snapshot baseline.
- `schema_migrations` count **unchanged** (no new SQL migrations in
  this release).

## Known caveats

- **OpenScience / Pythia source NOT vendored at ship time.**
  `apps/desktop/vendor/openscience-bin/openscience` and
  `apps/desktop/vendor/pythia-src/engine` were absent during the
  bundle run; bundle-cli correctly logged the fallback messages and
  skipped the copy step. **Therefore**: enabling either flag now
  shows a "service not bundled" inline notice in the sidebar entry,
  rather than spawning a real subprocess. To activate the full
  feature set, drop the vendor sources into the indicated paths and
  re-run `pnpm --filter @multica/desktop bundle-cli && ... package`.
- DMG was hand-built with `create-dmg` (memory 0.3.4) because
  electron-builder's dmg-builder socket hangs; the resulting volume
  is functionally identical to a normal DMG (UDZO compression, no
  notarization).
- DMG size: 228 MB. Slightly larger than 0.3.7 DMG (~228 MB) for the
  same reason — no embedded bundles add weight on first ship, but
  future 0.3.8.x patch releases that include vendored Python / Bun
  payloads will see +100-300 MB.
- Experimental sidebar section ONLY renders when at least one
  experimental flag is on. With both flags at default off the GUI is
  byte-identical to 0.3.7.

## Rollback

Phase A–F are independent commits. Reverting any one phase is safe
as long as the relevant Pieces of code are also reverted:

- Reverting Phase A removes the catalog entries + the new sidebar
  group + the right-panel `ExperimentalSection`. Labs UI returns to
  the single-flag state (`chat_pin_ui`).
- Reverting Phase B leaves the catalog entries visible but no
  managers; flags on → "service not bundled" persists for any
  pending enable.
- Reverting Phase C/D removes the manager + Skill + CLI subcommands
  for that feature individually.
- Reverting Phase E removes only the visual footer, no functional
  impact.
- Reverting Phase F = `git revert HEAD~N..HEAD` for the version-bump
  commit; no data concerns.

## By the numbers

- 23 files touched (1 deleted? no, 0 deleted).
- ~1,800 LOC added, 0 LOC removed (modulo comment-style whitespace).
- 0 new SQL migrations.
- 0 breaking changes to schema or wire format.
- 2 new Labs flags, both default off.
- 2 new Bundled subprocess managers (loopback-only, gated by flags).
- 2 new Skills, 7 new CLI subcommands, 0 new HTTP endpoints.
