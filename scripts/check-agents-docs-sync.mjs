#!/usr/bin/env node
// Single-source-of-truth gate for the two root agent guides.
//
// CLAUDE.md is the authoritative rules file; AGENTS.md is a derived digest of
// it. This check fails when the two files drift on any of the three shared
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
//
// Runs in CI (.github/workflows/ci.yml `docs-sync` job) and in the local
// githooks/pre-push hook. No dependencies; works on any Node >= 18.

import { readFileSync } from "node:fs";
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
  "✓ AGENTS.md and CLAUDE.md agree on toolchain versions, package boundaries, and verification commands.",
);
