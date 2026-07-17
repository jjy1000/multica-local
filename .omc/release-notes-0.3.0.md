---
name: 0.3.0 release notes
created: 2026-07-02T17:24:00Z
updated: 2026-07-02T17:24:00Z
version: 0.3.0
baseline: 0.2.98
type: release-notes
---

# Multica 0.3.0 (2026-07-02) — Standalone Edition (脱离 Docker)

This is the **standalone** release of Multica Desktop. v0.3.0 ships its own PostgreSQL inside the .app — OrbStack / Docker Desktop / `docker compose` are **no longer required** to run Multica.

## TL;DR

- **No more Docker.** The desktop bundle installs + starts + manages its own PostgreSQL 17 (Postgres.app kernel, BSD-licensed). First launch downloads the binary (~119 MB, SHA-256 verified) and caches it in `~/Library/Application Support/Multica/pg/17.4/`. Subsequent launches reuse the cached binary.
- **One-time migration.** Users upgrading from v0.2.x (which used the `multica_pgdata` Docker volume) get a one-time "Migrate data" dialog. On confirm, the app runs `pg_dump | pg_restore` (with row-count parity check) and writes a sentinel to suppress re-runs. The Docker volume is preserved on disk for 30 days as a rollback anchor.

## Highlights

- **feat(desktop): native PostgreSQL bootstrap** — `pg-bootstrap.ts` handles binary download (with progress push to renderer), `hdiutil` extract + `codesign --force --sign -`, `initdb`, `pg_ctl start/stop`, and `pg_dump | pg_restore` migration. The previous `docker compose up postgres` path is removed entirely.
- **feat(desktop): migration flow with row-count parity check (Bug 2 mitigation)** — `runMigrationFlow` does `pg_dump -Fc` from Docker → host dump file → `pg_restore --single-transaction --exit-on-error` into native → per-table row count comparison. Mismatch aborts without writing the sentinel; user's Docker data is unchanged.
- **fix(server-manager): skip migrate on external backend (P0)** — historical migrations 029 / 046 / 103 contain `DROP TABLE` operations. Running the bundled `migrate` binary against a pre-existing Docker pgdata was deleting the user's accumulated data on launch. v0.3.0 now skips `migrate` entirely when the picker chose `external` (any multica PG already on 5432). Native always migrates (its pgdata is freshly initdb'd).
- **feat(renderer): migration dialog** — `migration-dialog.tsx` paints when `shouldOfferMigration()` reports `hasDocker=true` AND no sentinel exists. Three states: prompt (Migrate / Skip) → running (with phase + bar) → result (migrated-X-rows / skipped-reason).
- **feat(preload): `shouldOfferMigration` + `runMigration` IPC** — previously the dialog was wired but the preload bridge was missing, so clicking "Migrate" was a no-op. Both channels are now exposed end-to-end (`pg:get-migration-status` → main, `server:run-migration` → main).

## Files of note

| Path | Change |
|---|---|
| `apps/desktop/src/main/pg-bootstrap.ts` | NEW (~960 LOC): binary download, `initdb`, `pg_ctl` start/stop, `pg_dump\|pg_restore` migration, sentinel management |
| `apps/desktop/src/main/server-manager.ts` | Removed `dockerOnPath()`, `startDockerPostgres()` (~60 LOC); simplified `pickPgBackend` to 2-way (`native`/`external`); new `runMigrationFlowIfNeeded`; **new: skip migrate when backend === "external"** |
| `apps/desktop/src/main/index.ts` | Migration progress push (subscribe-and-forward `currentState`) |
| `apps/desktop/src/preload/index.ts` + `.d.ts` | Expose `serverAPI.shouldOfferMigration()` + `serverAPI.runMigration(confirmed)` |
| `apps/desktop/src/renderer/src/components/migration-dialog.tsx` | NEW: prompt / running / result states |
| `apps/desktop/src/renderer/src/components/pg-download-progress.tsx` | NEW: download progress modal |
| `apps/desktop/src/renderer/src/components/updates-settings-tab.tsx` | Open-source attribution section (Postgres.app credit, required by PostgreSQL BSD notice) |
| `apps/desktop/resources/pg/manifest.json` | NEW: `{ version, sha256, url, sizeBytes }` pinned to Postgres.app v2.9.5 |
| `apps/desktop/scripts/bundle-cli.mjs` | Replaced `docker-compose.yml` copy step with `resources/pg/manifest.json` |
| `apps/desktop/src/shared/runtime-config.ts` | `pgBackend` union gained `"external"` |
| `docker-compose.yml`, `apps/desktop/resources/docker-compose.yml` | DELETED |
| `apps/desktop/package.json` | `version: 0.2.98 → 0.3.0` |

## Data migration paths

| Source state | After v0.3.0 launch | Action |
|---|---|---|
| Fresh install (no prior `/Applications/Multica.app`) | Native (127 migrations, empty) | Auto-downloads PG, initdb, `ensureMulticaDb`, migrates, starts |
| v0.3.0 already in place + `~/.multica/.pg-migrated-v1` exists | Native (existing data) | No-op; starts existing pgdata |
| v0.2.x with `multica_pgdata` Docker volume still on disk + no sentinel | Native (empty) + migration dialog | User confirms → `pg_dump\|pg_restore`, sentinel written |
| v0.2.x with Docker pgdata but `multica-postgres-1` is `Exited` | Native (empty) + migration dialog | Same as above; migration flow calls `docker start` internally |

## Verification

- **TypeScript**: `pnpm --filter @multica/desktop typecheck` — 0 errors (both `:node` and `:web`).
- **Tests**: `pnpm --filter @multica/desktop test` — 29 files, 259 tests, all green.
- **Cold start**: Multica launches in ~700 ms with native PG already cached; first-time download + initdb + migrate + server start completes in ~35 s on 10 Mbps.
- **Migration parity (dry-run on this host's gold backup)**:
  - docker pgdata → `pg_dump -Fc` → 6.9 MB custom-format dump → `pg_restore --single-transaction --exit-on-error` into native pgdata.
  - Row counts before/after: `workspace=1/1`, `issue=84/84`, `agent=38/38`, `comment=477/477`. Parity confirmed.

## Risks, mitigations, and rollout notes

- **R-destruct (newly documented)**: History migration `029_drop_daemon_pairing.up.sql`, `046_drop_runtime_usage.up.sql`, `103_drop_legacy_daily_rollups.up.sql` are `DROP TABLE`. Mitigated by the P0 skip-on-external-backend fix; documented here for future audits. **Future contributors writing destructive migrations must add them to a runtime allowlist in `pg-bootstrap.runMigrate()` or add a `--destructive-only-skip` flag — DO NOT just remove the migration.**
- **R-license**: Postgres.app bundles PostgreSQL under the PostgreSQL License (BSD-style). Attribution added to Settings → Updates panel ("Open-source attribution"). Multica's `about` credit string is required to remain in this panel for any redistribution.
- **R-rollback**: `/Applications/Multica.app.0.2.98.pre-update-*.bak` (or older) remains a valid fallback. The Docker `multica_pgdata` volume is intentionally not auto-deleted; users keep it for 30 days post-upgrade. The migration's `dumpPath` is permanently written to `~/.multica/backups/multica-pgdata-<ts>.dump` (size ~7 MB for the typical 1-workspace install).
- **R-codesign**: Each freshly-downloaded PG binary is `codesign --force --sign -` (ad-hoc) + `xattr -dr com.apple.quarantine` to avoid Gatekeeper rejection.

## Known follow-ups (NOT in this release)

- Decide a sun-set date for the `external` backend (currently the picker still accepts any multica PG on 5432, including manual installs).
- Consider a "PG version upgrade" flow (`manifest.json` ships a version bump → app downloads new binary → runs `pg_upgrade` against existing pgdata).
- Decide whether to embed Postgres.app tarball directly in the DMG (offline install) — currently the first-launch download is mandatory.
