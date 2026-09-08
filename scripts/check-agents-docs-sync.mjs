#!/usr/bin/env node
// Single-source-of-truth gate for the two root agent guides.
//
// NOTE (2026-08-19): the root AGENTS.md is a hand-maintained Qoder
// (qoder.com) derivation — different banner, version-history section
// dropped — while CLAUDE.md stays the Claude Code source of truth. The
// checks below are token-based (not byte-based) on purpose so that
// derivation stays legal. Do NOT "repair" AGENTS.md back to a verbatim
// mirror; that would destroy the Qoder parallel.
//
// CLAUDE.md is the authoritative rules file; AGENTS.md is a derived digest of
// it. This check fails when the two files drift on any of the six shared
// constraint categories, or when either file drifts from the real pins:
//
//   1. Toolchain versions  — the `| Tool | Version | ... |` table must list
//      identical versions in both files, and each version must match its
//      ground-truth pin (package.json, server/go.mod, .nvmrc,
//      pnpm-workspace.yaml catalog, ci.yml Postgres image).
//   2. Package boundaries  — the canonical boundary tokens for
//      core / ui / views / catalog must appear in both files.
//   3. Verification commands — the canonical check commands must appear in
//      both files and exist as real Makefile targets / package.json scripts.
//   4. Critical constraints — the canonical tokens for the remaining digest
//      sections (localized fork prohibitions, state management, backend UUID
//      rules, Pythia source-of-truth, experimental network calls,
//      migration/config immutability, i18n selectors) must appear in both
//      files.
//   5. Sub-domain guides — each sub-domain directory must carry both a
//      CLAUDE.md (source of truth) and an auto-discoverable AGENTS.md mirror
//      whose body is byte-identical to CLAUDE.md (after the banner line), and
//      must be registered in both root routing tables.
//
// Runs in CI (.github/workflows/ci.yml `docs-sync` job) and in the local
// githooks/pre-push hook. No dependencies; works on any Node >= 18.

import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFileSync(path.join(root, rel), "utf8");

const claude = read("CLAUDE.md");
const agents = read("AGENTS.md");

const errors = [];
const fail = (msg) => errors.push(msg);

// ---------------------------------------------------------------------------
// 1. Toolchain versions
// ---------------------------------------------------------------------------

const TOOLS = ["Node", "pnpm", "Go", "TypeScript", "React", "PostgreSQL"];

/** Parse the markdown table that starts with a `| Tool | Version |` header. */
function parseToolchainTable(text, label) {
  const lines = text.split("\n");
  const headerIdx = lines.findIndex((l) => /^\|\s*Tool\s*\|\s*Version\s*\|/.test(l));
  const map = new Map();
  if (headerIdx === -1) {
    fail(`${label}: no \`| Tool | Version | ... |\` toolchain table found`);
    return map;
  }
  for (let i = headerIdx + 1; i < lines.length; i++) {
    const m = lines[i].match(/^\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|/);
    if (!m) break; // table ended
    const [, tool, version] = m;
    if (/^[-\s]+$/.test(tool)) continue; // separator row
    map.set(tool, version);
  }
  for (const tool of TOOLS) {
    if (!map.has(tool)) fail(`${label}: toolchain table is missing a row for "${tool}"`);
  }
  return map;
}

const claudeTools = parseToolchainTable(claude, "CLAUDE.md");
const agentsTools = parseToolchainTable(agents, "AGENTS.md");

for (const tool of TOOLS) {
  const a = claudeTools.get(tool);
  const b = agentsTools.get(tool);
  if (a && b && a !== b) {
    fail(`toolchain drift for "${tool}": CLAUDE.md says "${a}", AGENTS.md says "${b}"`);
  }
}

/** Assert both docs pin `tool` to `expected` (per the ground-truth source). */
function checkGroundTruth(tool, expected, source, { prefix = false } = {}) {
  for (const [label, map] of [["CLAUDE.md", claudeTools], ["AGENTS.md", agentsTools]]) {
    const doc = map.get(tool);
    if (doc === undefined) continue; // missing row already reported
    const ok = prefix ? doc.startsWith(expected) : doc === expected;
    if (!ok) fail(`${label}: "${tool}" is "${doc}" but ${source} pins "${expected}"`);
  }
}

const pkg = JSON.parse(read("package.json"));
const pnpmPin = (pkg.packageManager ?? "").replace(/^pnpm@/, "");
checkGroundTruth("pnpm", pnpmPin, "package.json packageManager");

const goPin = read("server/go.mod").match(/^go\s+(\S+)/m)?.[1] ?? "";
checkGroundTruth("Go", goPin, "server/go.mod");

const nodePin = read(".nvmrc").trim();
checkGroundTruth("Node", nodePin, ".nvmrc", { prefix: true });

const catalog = read("pnpm-workspace.yaml");
const tsPin = catalog.match(/^\s{2}typescript:\s*"([^"]+)"/m)?.[1] ?? "";
checkGroundTruth("TypeScript", tsPin, "pnpm-workspace.yaml catalog");
const reactPin = catalog.match(/^\s{2}react:\s*"([^"]+)"/m)?.[1] ?? "";
checkGroundTruth("React", reactPin, "pnpm-workspace.yaml catalog");

const ci = read(".github/workflows/ci.yml");
const pgPin = ci.match(/pgvector\/pgvector:pg(\d+)/)?.[1] ?? "";
checkGroundTruth("PostgreSQL", pgPin, "ci.yml pgvector service image", { prefix: true });

// ---------------------------------------------------------------------------
// 1b. Release version line (0.5.105, audit M5)
// ---------------------------------------------------------------------------

// The "> **Current release: X.Y.Z**" banner is the one deliberately
// hand-maintained line in each file, so it used to drift silently (both
// files said 0.5.102 while 0.5.104 was shipped). Both files must now
// carry the SAME version — the release checklist bumps them together.
function parseReleaseLine(text, label) {
  const m = text.match(/\*?Current release:\s*(\d+\.\d+\.\d+)/);
  if (!m) {
    fail(`${label}: no "Current release: X.Y.Z" line found`);
    return null;
  }
  return m[1];
}

const claudeRelease = parseReleaseLine(claude, "CLAUDE.md");
const agentsRelease = parseReleaseLine(agents, "AGENTS.md");
if (
  claudeRelease !== null &&
  agentsRelease !== null &&
  claudeRelease !== agentsRelease
) {
  fail(
    `release-version drift: CLAUDE.md says ${claudeRelease}, AGENTS.md says ${agentsRelease} — bump both in the same commit`,
  );
}

// ---------------------------------------------------------------------------
// 2. Package boundaries
// ---------------------------------------------------------------------------

// Canonical boundary tokens. Each must appear verbatim in BOTH files, so a
// boundary rule silently deleted or reworded out of one file fails the gate.
const BOUNDARY_TOKENS = [
  ["packages/core", ["react-dom", "localStorage", "StorageAdapter", "process.env"]],
  ["packages/ui", ["@multica/core"]],
  ["packages/views", ["next/*", "react-router-dom", "NavigationAdapter"]],
  ["shared deps", ["catalog:", "pnpm-workspace.yaml"]],
];

for (const [scope, tokens] of BOUNDARY_TOKENS) {
  for (const token of tokens) {
    for (const [label, text] of [["CLAUDE.md", claude], ["AGENTS.md", agents]]) {
      if (!text.includes(token)) {
        fail(`${label}: package-boundary token "${token}" (${scope}) is missing`);
      }
    }
  }
}

// ---------------------------------------------------------------------------
// 3. Verification commands
// ---------------------------------------------------------------------------

// Each command must appear in both files AND exist as a real target/script,
// so neither doc can advertise a command that no longer exists.
const COMMANDS = [
  ["pnpm typecheck", () => Boolean(pkg.scripts?.typecheck), "package.json scripts.typecheck"],
  ["pnpm test", () => Boolean(pkg.scripts?.test), "package.json scripts.test"],
  ["pnpm lint", () => Boolean(pkg.scripts?.lint), "package.json scripts.lint"],
  ["make test", () => /^test:/m.test(makefile), "Makefile `test` target"],
  ["make check", () => /^check:/m.test(makefile), "Makefile `check` target"],
  ["make check-fast", () => /^check-fast:/m.test(makefile), "Makefile `check-fast` target"],
];
const makefile = read("Makefile");

for (const [cmd, existsInRepo, sourceLabel] of COMMANDS) {
  for (const [label, text] of [["CLAUDE.md", claude], ["AGENTS.md", agents]]) {
    if (!text.includes(cmd)) fail(`${label}: verification command "${cmd}" is missing`);
  }
  if (!existsInRepo()) fail(`"${cmd}" is documented but ${sourceLabel} does not exist`);
}

// ---------------------------------------------------------------------------
// 4. Critical constraints
// ---------------------------------------------------------------------------

// Canonical tokens for the remaining AGENTS.md "Critical Constraints" digest
// sections. Each must appear verbatim in BOTH files, so a constraint silently
// deleted or reworded out of either file fails the gate. These are token
// checks only — they guard the load-bearing identifiers, not the prose.
const CONSTRAINT_TOKENS = [
  ["localized fork", [
    "single-user fork",
    "telemetry",
    "auto-update",
    "Google OAuth",
    "HelpLauncher",
    'POST /auth/login {"name":"..."}',
  ]],
  ["state management", ["TanStack Query", "Zustand"]],
  ["backend UUID rules", [
    "server/internal/handler/",
    "parseUUIDOrBadRequest",
    "parseUUID",
  ]],
  ["Pythia source-of-truth", [
    "apps/desktop/vendor/pythia-src/engine/",
    "resources/pythia/engine/",
  ]],
  ["experimental network calls", ["api.rawRequest", "fetch()"]],
  ["upgrade immutability", [
    "Migrations are forward-only",
    "Config fields are append-only",
  ]],
  ["i18n selectors", ["arrow expressions"]],
];

for (const [scope, tokens] of CONSTRAINT_TOKENS) {
  for (const token of tokens) {
    for (const [label, text] of [["CLAUDE.md", claude], ["AGENTS.md", agents]]) {
      if (!text.includes(token)) {
        fail(`${label}: critical-constraint token "${token}" (${scope}) is missing`);
      }
    }
  }
}

// ---------------------------------------------------------------------------
// 5. Sub-domain guides
// ---------------------------------------------------------------------------

// Each sub-domain keeps its rules in CLAUDE.md (source of truth) plus an
// AGENTS.md mirror so platforms that auto-discover AGENTS.md load the same
// guidance. The mirror is the banner line + blank line + verbatim CLAUDE.md.
const SUBDOMAIN_GUIDE_DIRS = [
  "server",
  "packages",
  "packages/views",
  "apps/desktop",
  "apps/mobile",
  "apps/web",
];

const MIRROR_BANNER =
  "<!-- AUTO-SYNCED MIRROR of ./CLAUDE.md (the source of truth for this directory). " +
  "Edit CLAUDE.md, then regenerate this file; parity is enforced by " +
  "scripts/check-agents-docs-sync.mjs. -->";

for (const dir of SUBDOMAIN_GUIDE_DIRS) {
  const claudePath = `${dir}/CLAUDE.md`;
  const agentsPath = `${dir}/AGENTS.md`;

  if (!existsSync(path.join(root, claudePath))) {
    fail(`${claudePath}: sub-domain guide is missing`);
    continue;
  }
  if (!existsSync(path.join(root, agentsPath))) {
    fail(`${agentsPath}: AGENTS.md mirror of ${claudePath} is missing`);
    continue;
  }

  const guideBody = read(claudePath);
  const mirror = read(agentsPath);
  const expected = `${MIRROR_BANNER}\n\n${guideBody}`;
  if (mirror !== expected) {
    fail(
      `${agentsPath}: drifted from ${claudePath} — regenerate it as the banner ` +
        `line + blank line + verbatim CLAUDE.md content`,
    );
  }

  // Both root routing tables must point readers at the sub-domain guide.
  for (const [label, text] of [["CLAUDE.md", claude], ["AGENTS.md", agents]]) {
    if (!text.includes(claudePath)) {
      fail(`${label}: sub-domain guide "${claudePath}" is not registered in the routing table`);
    }
  }
}

// ---------------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------------

if (errors.length > 0) {
  console.error("✗ AGENTS.md / CLAUDE.md consistency check failed:\n");
  for (const e of errors) console.error(`  - ${e}`);
  console.error(
    "\nCLAUDE.md is the source of truth; update AGENTS.md (and the real pins) to match.",
  );
  process.exit(1);
}

console.log(
  "✓ AGENTS.md and CLAUDE.md agree on toolchain versions, package boundaries, " +
    "verification commands, critical constraints (localized fork, state management, " +
    "UUID rules, Pythia source-of-truth, rawRequest, migration/config immutability, i18n), " +
    "and sub-domain guide mirrors.",
);
