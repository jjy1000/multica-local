#!/usr/bin/env node
// Builds the Go binaries the desktop app needs (CLI, backend server,
// schema migrator) from server/cmd/* and copies them into
// apps/desktop/resources/bin/ so electron-vite (dev) and electron-builder
// (prod) pick them up. Running this on every dev/build/package
// invocation guarantees the bundled binaries always match the current
// Go source — no more stale binary surprises. Go's build cache makes
// the no-op case (nothing changed) effectively free.
//
// ldflags mirror `make build` so the binaries report a meaningful
// version / commit / date.
//
// Three binaries are produced, each matching a single Go entrypoint:
//
//   multica   — server/cmd/multica   — the agent runtime CLI
//                                     (auth, daemon, runtime, setup).
//                                     Desktop spawns this for the
//                                     local agent runtime.
//
//   server    — server/cmd/server    — the HTTP+WS backend. The
//                                     desktop main process spawns it
//                                     on app start so the rendered
//                                     web frontend has an API to
//                                     talk to without an external
//                                     `make start`.
//
//   migrate   — server/cmd/migrate   — one-shot forward/back schema
//                                     runner. server-manager calls
//                                     `migrate up` before spawning
//                                     `server` so the embedded PG
//                                     (or local Docker PG) has the
//                                     latest schema.
//
// Graceful: if `go` is not installed (e.g. frontend-only contributor),
// we skip the build and fall through to whatever is already in
// resources/bin/. A genuine Go compile error is fatal — you want that
// to block dev, not hide.

import { access, chmod, copyFile, cp, mkdir, readdir, rm, writeFile } from "node:fs/promises";
import { constants, readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { execFileSync, execSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..", "..", "..");
const serverDir = join(repoRoot, "server");

const PLATFORM_TO_GOOS = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const SUPPORTED_ARCHS = new Set(["x64", "arm64"]);

function runtimePlatformFromArgs(argv) {
  const flagIndex = argv.indexOf("--target-platform");
  if (flagIndex === -1) return process.platform;
  return argv[flagIndex + 1] ?? "";
}

function runtimeArchFromArgs(argv) {
  const flagIndex = argv.indexOf("--target-arch");
  if (flagIndex === -1) return process.arch;
  return argv[flagIndex + 1] ?? "";
}

function normalizeRuntimePlatform(platform) {
  if (platform in PLATFORM_TO_GOOS) return platform;
  throw new Error(
    `[bundle-cli] unsupported target platform: ${platform}. ` +
      "Use darwin, linux, or win32.",
  );
}

function normalizeRuntimeArch(arch) {
  if (SUPPORTED_ARCHS.has(arch)) return arch;
  throw new Error(
    `[bundle-cli] unsupported target architecture: ${arch}. ` +
      "Use x64 or arm64.",
  );
}

function binaryNameForPlatform(platform, base) {
  return platform === "win32" ? `${base}.exe` : base;
}

const targetPlatform = normalizeRuntimePlatform(
  runtimePlatformFromArgs(process.argv.slice(2)),
);
const targetArch = normalizeRuntimeArch(runtimeArchFromArgs(process.argv.slice(2)));
const goos = PLATFORM_TO_GOOS[targetPlatform];
const goarch = targetArch === "x64" ? "amd64" : targetArch;
const destDir = join(repoRoot, "apps", "desktop", "resources", "bin");

// Build three Go binaries. multica gets version+commit+date ldflags so
// `multica --version` matches the CLI release; the other two just need
// the version so log lines are greppable.
const BUILDS = [
  { cmd: "./cmd/multica", name: "multica", ldflags: true },
  { cmd: "./cmd/server", name: "server", ldflags: true },
  { cmd: "./cmd/migrate", name: "migrate", ldflags: false },
];

function sh(cmd) {
  try {
    return execSync(cmd, { encoding: "utf-8" }).trim();
  } catch {
    return "";
  }
}

function hasGo() {
  try {
    execSync("go version", { stdio: "pipe" });
    return true;
  } catch {
    return false;
  }
}

async function exists(p) {
  try {
    await access(p, constants.F_OK);
    return true;
  } catch {
    return false;
  }
}

// 0.3.29 fix: this fork uses sparse git tracking (only ~7 files in
// the index), so `git describe --tags` returns the pre-update marker
// tag we wrote in the snapshot step, NOT a release tag. That makes
// the bundle name render as "0.0.0-gpre-update-...-dirty" instead of
// the version declared in apps/desktop/package.json. Trust
// package.json (the canonical source per CLAUDE.md) and treat any
// pre-update marker tag as "no release tag present".
let version = sh("git describe --tags --always --dirty");
if (!version || version.startsWith("pre-update-") || version.startsWith("gpre-update-")) {
  try {
    version = JSON.parse(readFileSync(join(repoRoot, "apps", "desktop", "package.json"), "utf-8")).version || "dev";
  } catch {
    version = "dev";
  }
}
const commit = sh("git rev-parse --short HEAD") || "unknown";
const date = new Date().toISOString().replace(/\.\d+Z$/, "Z");
const fullLdflags = `-X main.version=${version} -X main.commit=${commit} -X main.date=${date}`;

if (hasGo()) {
  await mkdir(join(serverDir, "bin", `${goos}-${goarch}`), { recursive: true });
  for (const build of BUILDS) {
    const binName = binaryNameForPlatform(targetPlatform, build.name);
    const srcBinary = join(serverDir, "bin", `${goos}-${goarch}`, binName);
    const ldflags = build.ldflags ? fullLdflags : `-X main.version=${version}`;
    console.log(
      `[bundle-cli] go build → ${srcBinary} (${goos}/${goarch}, cmd=${build.cmd}, version=${version})`,
    );
    execFileSync(
      "go",
      [
        "build",
        "-ldflags",
        ldflags,
        "-o",
        srcBinary,
        build.cmd,
      ],
      {
        cwd: serverDir,
        stdio: "inherit",
        env: {
          ...process.env,
          CGO_ENABLED: "0",
          GOOS: goos,
          GOARCH: goarch,
        },
      },
    );
  }
} else {
  console.warn(
    "[bundle-cli] `go` not found in PATH — skipping Go build. " +
      "Desktop will use whatever is already in resources/bin/.",
  );
}

await rm(destDir, { recursive: true, force: true });
await mkdir(destDir, { recursive: true });

// PR 3 (Stage D-2): the bundled pg manifest ships with the DMG so
// pg-bootstrap.ts can read it on first launch and download Postgres.app
// from the recorded URL. The .dmg itself is NOT bundled (~120 MB
// would inflate the DMG considerably) — users pay the download cost
// once, and the result lands at app.getPath("userData") + "/pg/17.4/".
const manifestSrc = join(repoRoot, "apps/desktop/resources/pg/manifest.json");
const manifestDest = join(destDir, "..", "pg/manifest.json");
if (await exists(manifestSrc)) {
  await mkdir(dirname(manifestDest), { recursive: true });
  await copyFile(manifestSrc, manifestDest);
  console.log(`[bundle-cli] bundled ${manifestSrc} → ${manifestDest}`);
} else {
  console.warn(
    `[bundle-cli] ${manifestSrc} not present — first-launch native PG bootstrap will fail ` +
      `with a "pg manifest not found" error. Run pnpm --filter @multica/desktop bundle-cli ` +
      `after committing apps/desktop/resources/pg/manifest.json.`,
  );
}

// Ship the SQL migration files alongside the migrate binary so the
// bundled migrator can find them at runtime. The Go code's ResolveDir()
// walks up from the binary's directory and checks server/migrations/.
const migrationsSrc = join(serverDir, "migrations");
const migrationsDest = join(destDir, "..", "server", "migrations");
if (await exists(migrationsSrc)) {
  await rm(migrationsDest, { recursive: true, force: true });
  await cp(migrationsSrc, migrationsDest, { recursive: true });
  console.log(`[bundle-cli] bundled ${migrationsSrc} → ${migrationsDest}`);
  // Post-copy integrity check (0.3.66): confirm the mirror we just wrote is
  // byte-identical to the source so a silent partial copy can never ship a
  // half-migrated app. The committed-mirror-vs-source gate lives in
  // scripts/check-migrations-sync.mjs (CI + pre-push); this guards the copy
  // itself.
  const srcSql = (await readdir(migrationsSrc)).filter((f) => f.endsWith(".sql")).sort();
  const destSql = (await readdir(migrationsDest)).filter((f) => f.endsWith(".sql")).sort();
  const destSet = new Set(destSql);
  const sha = (p) => createHash("sha256").update(readFileSync(p)).digest("hex");
  const drift = [];
  for (const f of srcSql) {
    if (!destSet.has(f)) { drift.push(`missing after copy: ${f}`); continue; }
    if (sha(join(migrationsSrc, f)) !== sha(join(migrationsDest, f))) drift.push(`content mismatch: ${f}`);
  }
  if (drift.length) {
    console.error(`[bundle-cli] migration copy verification FAILED:\n  ${drift.join("\n  ")}`);
    process.exit(1);
  }
  console.log(`[bundle-cli] verified ${srcSql.length} migration files copied intact`);
} else {
  console.warn(
    `[bundle-cli] ${migrationsSrc} not present — migrate binary won't find ` +
      `SQL migrations at runtime.`,
  );
}

let bundledAny = false;
for (const build of BUILDS) {
  const binName = binaryNameForPlatform(targetPlatform, build.name);
  const srcBinary = join(serverDir, "bin", `${goos}-${goarch}`, binName);
  if (!(await exists(srcBinary))) {
    console.warn(
      `[bundle-cli] ${srcBinary} not present — skipping ${build.name} bundle.`,
    );
    continue;
  }
  const destBinary = join(destDir, binName);
  await copyFile(srcBinary, destBinary);
  await chmod(destBinary, 0o755);

  // macOS: ad-hoc sign so Gatekeeper doesn't complain when the parent app
  // (which itself may be unsigned in dev) spawns the child.
  if (process.platform === "darwin") {
    try {
      execSync(`codesign -s - --force ${JSON.stringify(destBinary)}`, {
        stdio: "pipe",
      });
    } catch {
      // Non-fatal. Unsigned binaries still run when the parent app is trusted.
    }
  }

  console.log(`[bundle-cli] bundled ${srcBinary} → ${destBinary}`);
  bundledAny = true;
}

if (!bundledAny) {
  console.warn(
    "[bundle-cli] no binaries were bundled — desktop will fall back to " +
      "auto-installing the latest release at runtime, OR the server may " +
      "be missing entirely (no `multica server` available).",
  );
}

// ---------------------------------------------------------------------------
// 0.3.8 experimental-services bundle: Pythia source + OpenScience binary.
//
// Both services are behind Labs flags. Vendoring here is OPTIONAL — when
// the vendor directories are absent the desktop still ships, the Labs flags
// still surface, but enabling them shows the user a "service not bundled"
// message instead of spawning anything. This mirrors the existing `hasGo()`
// fallback so a partial checkout can build the desktop.
//
// Pythia layout:
//   apps/desktop/vendor/pythia-src/engine/*.py
//   → apps/desktop/resources/pythia/engine/*.py
//   + resources/pythia/run.sh — bash wrapper invoked by pythia-manager.
//
// OpenScience layout:
//   apps/desktop/vendor/openscience-bin/openscience
//   → apps/desktop/resources/openscience/openscience
// ---------------------------------------------------------------------------

const pythiaSrc = join(repoRoot, "apps/desktop/vendor/pythia-src/engine");
const pythiaDest = join(destDir, "..", "pythia/engine");
const pythiaWrapperDest = join(destDir, "..", "pythia/run.sh");
const pythiaRequirementsDest = join(destDir, "..", "pythia/requirements.txt");
if (await exists(pythiaSrc)) {
  await rm(join(destDir, "..", "pythia"), { recursive: true, force: true });
  await mkdir(pythiaDest, { recursive: true });
  await cp(pythiaSrc, pythiaDest, { recursive: true });
  // Stage the Pythia requirements alongside the engine so the wrapper
  // can tell the user exactly what to pip install when deps are missing.
  // The runtime contract is "no Ollama / no MiroFish / no Osiris" — only
  // the libraries PYTHIA needs to expose its FastAPI surface.
  // 0.3.30.3: full reference engine now ships, so we add `mcp`
  // (server.py / engine.mcp.py uses it for the optional MCP gateway)
  // and `pydantic-settings` (engine.config.py reads from env via
  // pydantic-settings). Both are tiny but otherwise the engine
  // crashes on first import.
  const requirements = [
    "fastapi>=0.115",
    "uvicorn[standard]>=0.30",
    "httpx>=0.27",
    "python-dotenv>=1.0",
    "pydantic>=2.7",
    "pydantic-settings>=2.3",
    "mcp>=1.2",
  ].join("\n") + "\n";
  await writeFile(pythiaRequirementsDest, requirements);
  // 0.3.30: read the wrapper straight from the vendor source-of-record
  // (apps/desktop/vendor/pythia-src/run.sh) instead of embedding it
  // as a JS template literal. Editing the vendor file is the single
  // supported way to update run.sh; the bundle step copies it
  // byte-for-byte into resources/pythia/run.sh on every run. This
  // avoids JS template-escape pitfalls (`${...}`, backticks, etc.)
  // and keeps both copies in lockstep.
  const { readFile } = await import("node:fs/promises");
  const vendorWrapperPath = join(repoRoot, "apps", "desktop", "vendor", "pythia-src", "run.sh");
  const wrapper = await readFile(vendorWrapperPath, "utf-8");
  await writeFile(pythiaWrapperDest, wrapper);
  await chmod(pythiaWrapperDest, 0o755);
  console.log(`[bundle-cli] bundled Pythia source → ${pythiaDest} (+ run.sh + requirements.txt)`);
} else {
  console.warn(
    "[bundle-cli] Pythia source not vendored at " +
      "apps/desktop/vendor/pythia-src/engine — pythia_oracle flag will show " +
      "'service not bundled' when enabled. To bundle: copy " +
      "/Users/jiangjianyan/jjy/关于试验性功能开发参考/Pythia-main/engine/ to that path " +
      "before running bundle-cli.",
  );
}

const openscienceBin = join(
  repoRoot,
  "apps/desktop/vendor/openscience-bin/openscience",
);
const openscienceDest = join(destDir, "..", "openscience/openscience");

// 0.3.16+: openscience-src/agent-prompts/*.txt are the raw OpenScience
// agent prompt templates (research / biology / physics / ml / etc.).
// They drive the Multica-native 290-skills browser under
// /experimental/claude-lab — we display the same text the original
// OpenScience renderer showed, but render it as React components
// rather than shipping the SolidJS bundle. We deliberately do NOT
// bundle the openscience binary itself (0.3.14 retired it).
const opensciencePromptsSrc = join(
  repoRoot,
  "apps/desktop/vendor/openscience-src/agent-prompts",
);
const opensciencePromptsDest = join(
  destDir,
  "..",
  "openscience-prompts",
);
if (await exists(opensciencePromptsSrc)) {
  await rm(opensciencePromptsDest, { recursive: true, force: true });
  await mkdir(opensciencePromptsDest, { recursive: true });
  await cp(opensciencePromptsSrc, opensciencePromptsDest, { recursive: true });
  console.log(`[bundle-cli] bundled OpenScience prompts → ${opensciencePromptsDest}`);
} else {
  console.warn(
    "[bundle-cli] OpenScience prompts not vendored at " +
      "apps/desktop/vendor/openscience-src/agent-prompts — claude_science " +
      "skills-browser will fall back to in-app placeholder text. To bundle: " +
      "copy /Users/jiangjianyan/jjy/关于试验性功能开发参考/openscience-main/" +
      "backend/cli/src/agent/prompt/*.txt to that path before running " +
      "bundle-cli.",
  );
}
if (await exists(openscienceBin)) {
  await mkdir(dirname(openscienceDest), { recursive: true });
  await copyFile(openscienceBin, openscienceDest);
  await chmod(openscienceDest, 0o755);
  if (process.platform === "darwin") {
    try {
      execSync(`codesign -s - --force ${JSON.stringify(openscienceDest)}`, {
        stdio: "pipe",
      });
    } catch {
      // Non-fatal.
    }
  }
  console.log(`[bundle-cli] bundled OpenScience binary → ${openscienceDest}`);
} else {
  console.warn(
    "[bundle-cli] OpenScience binary not vendored at " +
      "apps/desktop/vendor/openscience-bin/openscience — claude_science flag " +
      "will show 'service not bundled' when enabled. To bundle: drop the " +
      "`openscience` native binary at that path before running bundle-cli.",
  );
}

// 0.3.15: copy the claude-science import manifest + assets into the
// DMG so the renderer can read skills/agents/squads/installed counts
// at runtime. The manifest is generated by
// apps/desktop/scripts/build-claude-science-manifest.mjs which walks
// the OpenScience source tree and emits into
// apps/desktop/vendor/claude-science-manifest/. We copy from vendor/
// (NOT apps/desktop/resources/claude-science/) because bundle-cli
// wipes resources/ on every run; keeping source-of-record in vendor/
// avoids the "rm dest then can't find source" race.
const claudeScienceSrc = join(
  repoRoot,
  "apps",
  "desktop",
  "vendor",
  "claude-science-manifest",
);
const claudeScienceDest = join(destDir, "..", "claude-science");
if (await exists(claudeScienceSrc)) {
  try {
    await rm(claudeScienceDest, { recursive: true, force: true });
  } catch {
    // dest missing — fine.
  }
  await cp(claudeScienceSrc, claudeScienceDest, { recursive: true });
  console.log(`[bundle-cli] bundled claude-science manifest + assets → ${claudeScienceDest}`);
} else {
  console.warn(
    "[bundle-cli] claude-science assets not generated at " +
      "apps/desktop/vendor/claude-science-manifest — claude_science flag will show " +
      "'resources not packaged' when enabled. Run " +
      "apps/desktop/scripts/build-claude-science-manifest.mjs before bundle-cli " +
      "if you want skills / agents / squads bundled in the DMG.",
  );
}

// ---------------------------------------------------------------------------
// 0.3.27 PR-5 / C2-mini: LLM Wiki Bridge stdio MCP stub.
//
// Source of record is apps/desktop/vendor/llm-wiki-bridge/run.sh (note
// the DASH). bundle-cli wipes resources/ on every run, so we re-stage
// from vendor/ into resources/experiments/llm_wiki_bridge/run.sh
// (UNDERSCORE) — LLMWikiBridgeManager's resolveBinary() points at
// that underscored path. The dash-vs-underscore mismatch is a known
// pre-existing naming wart; renaming would also require touching the
// manifest's runtime.binary field, which is out of scope for C2-mini.
//
// When the vendor copy is missing the manager surfaces a clear
// BINARY_NOT_BUNDLED error at flag-enable time instead of crashing.
// ---------------------------------------------------------------------------
const llmWikiBridgeSrc = join(
  repoRoot,
  "apps",
  "desktop",
  "vendor",
  "llm-wiki-bridge",
);
const llmWikiBridgeDest = join(destDir, "..", "experiments", "llm_wiki_bridge");
if (await exists(llmWikiBridgeSrc)) {
  try {
    await rm(llmWikiBridgeDest, { recursive: true, force: true });
  } catch {
    // dest missing — fine.
  }
  await mkdir(llmWikiBridgeDest, { recursive: true });
  await cp(llmWikiBridgeSrc, llmWikiBridgeDest, { recursive: true });
  console.log(`[bundle-cli] bundled llm-wiki-bridge stub → ${llmWikiBridgeDest}`);
} else {
  console.warn(
    "[bundle-cli] llm-wiki-bridge stub not vendored at " +
      "apps/desktop/vendor/llm-wiki-bridge — llm_wiki_bridge flag will show " +
      "'resources not packaged' when enabled. Drop run.sh into " +
      "apps/desktop/vendor/llm-wiki-bridge/ before bundle-cli if you want " +
      "the bundled fallback shipped in the DMG.",
  );
}

// ---------------------------------------------------------------------------
// 0.3.29.2: code_canvas stub binary (P9 internal pilot).
//
// code_canvas is the only subprocess-kind experiment left without its
// vendor copy in this fork — pythia / llm-wiki-bridge / claude-science all
// ship, but code_canvas's 30-line /health Python stub was never committed
// to apps/desktop/vendor/code-canvas/. catalog.go and the desktop
// manager-factory both reference `code-canvas/run.sh` under
// resources/; without this cp the enable-time spawn falls through to
// "BINARY_NOT_BUNDLED" (memory labs-flag-enable-breaks-2026-07-14).
//
// Mirror the llm-wiki-bridge pattern: source is apps/desktop/vendor/
// (NOT resources/, because bundle-cli wipes resources/ on each run).
// ---------------------------------------------------------------------------
const codeCanvasSrc = join(
  repoRoot,
  "apps",
  "desktop",
  "vendor",
  "code-canvas",
);
const codeCanvasDest = join(destDir, "..", "code-canvas");
if (await exists(codeCanvasSrc)) {
  try {
    await rm(codeCanvasDest, { recursive: true, force: true });
  } catch {
    // dest missing — fine.
  }
  await mkdir(codeCanvasDest, { recursive: true });
  await cp(codeCanvasSrc, codeCanvasDest, { recursive: true });
  console.log(`[bundle-cli] bundled code-canvas service → ${codeCanvasDest}`);
} else {
  console.warn(
    "[bundle-cli] code-canvas stub not vendored at " +
      "apps/desktop/vendor/code-canvas — code_canvas flag will show " +
      "'resources not packaged' when enabled. Drop run.sh into " +
      "apps/desktop/vendor/code-canvas/ before bundle-cli if you want " +
      "the bundled fallback shipped in the DMG.",
  );
}

// ---------------------------------------------------------------------------
// 0.5.43: semantica runtime (FastAPI / uvicorn / pydantic).
//
// semantica is the only subprocess-kind experiment that survives a vendor
// copy but no cp block in this fork — pythia / llm-wiki-bridge /
// claude-science / code-canvas all ship, but semantica's run.sh +
// requirements.txt never got a cp. catalog.go LoopbackService="semantica"
// + manager-factory resolve to apps/desktop/resources/semantica/run.sh
// at spawn time; without this cp the enable-time spawn falls through to
// "BINARY_NOT_BUNDLED" (memory labs-flag-enable-breaks-2026-07-14).
//
// Mirror the code-canvas pattern (block above); source is
// apps/desktop/vendor/ (NOT resources/, because bundle-cli wipes
// resources/ on each run).
// ---------------------------------------------------------------------------
const semanticaSrc = join(
  repoRoot,
  "apps",
  "desktop",
  "vendor",
  "semantica",
);
const semanticaDest = join(destDir, "..", "semantica");
if (await exists(semanticaSrc)) {
  try {
    await rm(semanticaDest, { recursive: true, force: true });
  } catch {
    // dest missing — fine.
  }
  await mkdir(semanticaDest, { recursive: true });
  await cp(semanticaSrc, semanticaDest, { recursive: true });
  // 0.5.53 P1: also mirror the prebuilt wheel(s) from
  // apps/desktop/vendor/semantica-src/builds/ — run.sh looks for the
  // wheel at ./builds/ (Stage 3 of the new run.sh). Without this the
  // packaged runtime refuses to start with "no prebuilt wheel found".
  const semanticaBuildsSrc = join(
    repoRoot,
    "apps",
    "desktop",
    "vendor",
    "semantica-src",
    "builds",
  );
  if (await exists(semanticaBuildsSrc)) {
    await cp(semanticaBuildsSrc, join(semanticaDest, "builds"), { recursive: true });
    console.log(
      `[bundle-cli] bundled semantica runtime → ${semanticaDest} (+ builds/)`,
    );
  } else {
    console.log(
      `[bundle-cli] bundled semantica runtime → ${semanticaDest} ` +
        "(builds/ missing — run bash scripts/build-semantica-wheel.sh before packaging)",
    );
  }
} else {
  console.warn(
    "[bundle-cli] semantica runtime not vendored at " +
      "apps/desktop/vendor/semantica — semantica flag will show " +
      "'resources not packaged' when enabled. Drop run.sh + requirements.txt into " +
      "apps/desktop/vendor/semantica/ before bundle-cli if you want " +
      "the bundled fallback shipped in the DMG.",
  );
}

