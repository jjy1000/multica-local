#!/usr/bin/env node
// Migration mirror parity gate.
//
// The desktop app bundles its SQL migrations at
// `apps/desktop/resources/server/migrations/`, a COMMITTED mirror of the
// source of truth at `server/migrations/` (bundle-cli.mjs copies source →
// resources on every `bundle-cli` run). Because bundle-cli is a heavy step
// (it builds the Go binaries) it does NOT run in CI, so a developer who
// edits `server/migrations/` and commits without re-running bundle-cli
// leaves the committed mirror stale — and the packaged app would ship the
// wrong schema while the running dev server uses the new one.
//
// This gate closes that gap: it asserts the two directories are identical
// (same `.sql` file set, same SHA-256 per file). It is stdlib-only and runs
// in CI (.github/workflows/ci.yml `docs-sync` job) and in the local
// githooks/pre-push hook, alongside check-agents-docs-sync.mjs.
//
// Why a separate gate from bundle-cli's own post-copy self-check: bundle-cli
// can only ever see a fresh copy (it just wrote it), so it cannot catch a
// *committed* stale mirror. This gate reads the committed trees directly.

import { readFileSync, readdirSync } from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const SRC = path.join(root, "server", "migrations");
const MIRROR = path.join(root, "apps", "desktop", "resources", "server", "migrations");

const sqlFiles = (dir) =>
  readdirSync(dir)
    .filter((f) => f.endsWith(".sql"))
    .sort();

const hashFile = (file) =>
  createHash("sha256").update(readFileSync(file)).digest("hex");

let errors = 0;
const fail = (msg) => {
  console.error(`  - ${msg}`);
  errors += 1;
};

let srcList;
let mirrorList;
try {
  srcList = sqlFiles(SRC);
} catch (e) {
  console.error(`✗ migration parity: cannot read source ${SRC}: ${e.message}`);
  process.exit(1);
}
try {
  mirrorList = sqlFiles(MIRROR);
} catch (e) {
  console.error(`✗ migration parity: cannot read mirror ${MIRROR}: ${e.message}`);
  console.error(
    "  The bundled mirror is missing. Run `pnpm --filter @multica/desktop bundle-cli` to regenerate it.",
  );
  process.exit(1);
}

// File-set parity (names only — catches added/removed migrations).
const srcSet = new Set(srcList);
const mirrorSet = new Set(mirrorList);
for (const f of srcList) if (!mirrorSet.has(f)) fail(`present in source but missing from mirror: ${f}`);
for (const f of mirrorList) if (!srcSet.has(f)) fail(`present in mirror but missing from source: ${f}`);

// Content parity (hash per common file — catches an edited-but-not-rebundled file).
for (const f of srcList) {
  if (!mirrorSet.has(f)) continue;
  const a = hashFile(path.join(SRC, f));
  const b = hashFile(path.join(MIRROR, f));
  if (a !== b) fail(`content drift on ${f} (source sha256 ${a.slice(0, 12)}… ≠ mirror ${b.slice(0, 12)}…)`);
}

if (errors > 0) {
  console.error(
    `\n✗ server/migrations ↔ apps/desktop/resources/server/migrations parity failed (${errors} difference(s)).\n` +
      "  The committed bundled mirror is stale. Re-run:\n" +
      "    pnpm --filter @multica/desktop bundle-cli\n" +
      "  and commit the regenerated apps/desktop/resources/server/migrations/.",
  );
  process.exit(1);
}

console.log(
  `✓ migration mirror in sync (${srcList.length} .sql files, source == bundled).`,
);
