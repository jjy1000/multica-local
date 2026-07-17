# Multica 0.3.15 — claude_science research lab

## TL;DR

0.3.15 ships the first **installable** Labs entry: `claude_science`. Toggling the flag in Settings → Workspace → Labs now provisions a fully populated research lab from an on-disk manifest — no extra API keys, no extra services to start. Renderer shows resource counts (skills / agents / squads / members / workspace) under the toggle via the new `LabsFlagSidePanel` component.

| What | Status |
| --- | --- |
| Lock table + 7 ListVisible queries | shipped |
| Manifest emitter (build-claude-science-manifest.mjs) | shipped |
| HTTP API (install / rollback / status) | shipped |
| Flag wire (PATCH routes to install path) | shipped |
| Full installer (workspace + 1 lab-runtime + 291 skills + 5 agents + 5 squads + 5 squad_members + 4 lock rows) | shipped |
| Renderer side panel with counts + status | shipped |
| Sidebar hide via flag toggle | shipped |
| 4 MCP servers (KEGG / Zinc / Metabolomics Workbench / USPTO) | deferred to 0.3.16-patch.1 |
| MCP lifecycle manager | deferred to 0.3.16-patch.1 |

## Migration

**148_claude_science_experimental_lock** — adds `experimental_resource_lock`
table (workspace_id UUID NULL, resource_type TEXT, resource_id UUID,
experimental_source TEXT, hidden BOOL, claimed_at TIMESTAMPTZ). Forward-only,
no destructive changes.

## What got fixed during ship

Three P1s surfaced when exercising the end-to-end install path against the
packaged DMG:

1. **`MULTICA_RESOURCES_DIR` not set**: `apps/desktop/src/main/server-manager.ts::startServer`
   now spreads the resolved resource path (`process.resourcesPath +
   app.asar.unpacked/resources/`) into the server env at spawn time. Without
   it, the install handler couldn't load `manifest.json`. No change needed to
   the per-profile `.env` file (this is app-bundle surface, not user data).

2. **Squad member staleness**: when the manifest lists agent names that the
   current build didn't port over (e.g. OpenScience source dropped a prompt
   file), the install path now **skips** the missing member (`stderr` log)
   instead of bubbling up `"agent name X not found in install set"`, and
   the squad `leader_id` falls back to the first successfully-ported agent
   when `leader_id` is NOT NULL with FK to `agent.id`. Coverage:
   `TestExperimentalResourcesInstall_SkipsUnknownSquadMembers`.

3. **`schema_migrations` row 148 missing**: PR 6 manually applied migration
   148 during install-handler development but didn't register the row in
   `schema_migrations`. The packaged bundle's `migrate up` re-applied it,
   `relation already exists` propagated up through 3-retry migration loop,
   `ensureServerUp` threw, server stayed down. Fix: `INSERT INTO
   schema_migrations(version) VALUES ('148_claude_science_experimental_lock')`.
   Prevention contract: any future manual application of a migration must
   be paired with the schema_migrations row insert.

## Verification (live DMG)

- `bash ~/.multica/scripts/pre-update-snapshot.sh` — exit 0 (720MB .app bak
  + PG logical backup + KB rsync)
- `pnpm --filter @multica/desktop bundle-cli` — Go binaries build, manifest
  copied to `apps/desktop/resources/claude-science/`
- `pnpm --filter @multica/desktop build` + `electron-builder --mac --arm64`
  — packaged 0.3.15 .app built (zip 244MB, dmg blocked by create-dmg 1.2.3
  compatibility issue — same workaround as 0.3.4: install directly from
  the .app)
- `cp -R dist/mac-arm64/Multica.app /Applications/` + `open
  /Applications/Multica.app` — cold start < 8s, `/health` returns
  `{"status":"ok"}`
- `POST /api/experimental-resources/claude_science/install` returns 200
  with `counts: [workspace=1, skill=291, agent=5, squad=5, member=5]`
- `POST .../rollback` returns 204, status flips `Installed=true, Hidden=true`
- `POST .../install` (second time) returns 200, `Installed=true, Hidden=false`
- Row parity: `workspace=2 / issue=162 / comment=855 / agent=85 / squad=16`
  (user workspace + 1 claude-science workspace; 5 lab agents; 5 lab squads;
  existing user data 0 loss)

## Test additions

- `TestExperimentalResourcesInstall_SkipsUnknownSquadMembers` — fixture
  manifest whose squads reference agents not in the `agents` list; install
  must skip and complete
- `TestExperimentalResourcesManifestUnavailable_503` — install returns 503
  when the manifest isn't staged (no `MULTICA_RESOURCES_DIR`)
- `TestExperimentalResourcesRoundTrip_InstalledThenHidden` — install →
  status reports locked resources → rollback hides → re-install restores

Full Go suite: 27/27 packages PASS, `pnpm typecheck` 0 errors.

## What you can do today

1. Open Labs (Settings → Workspace → Labs) — you'll see `claude_science`
   listed under flags (Chinese title: "Claude 科学工作台")
2. Enable the toggle. The renderer side panel will show "已装载 1
   workspace · 291 skills · 5 agents · 5 squads" with the workspace slug
   `claude-science`
3. The sidebar adds a "试验性功能" group with an entry to the workspace
4. Toggle off → resources are soft-hidden (rows stay in DB, sidebar entry
   vanishes on the next refetch)
5. Toggle on → counts restored 1:1

## What's NOT here (deferred to 0.3.16-patch.1)

- 4 MCP servers: `kegg`, `zinc`, `metabolomics_workbench`, `uspto`
- Lifecycle manager: spawn / ensure / stop + per-call count persistence

These are ~800 LOC of fresh Bun stdio JSON-RPC plumbing unrelated to the
lock + manifest infrastructure shipped here.
