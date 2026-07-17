#!/usr/bin/env node
// builds the claude_science import manifest at build time by walking
// the OpenScience repo at $OPENSCIENCE_SOURCE_DIR (or
// /Users/jiangjianyan/Downloads/openscience-main as a default).
//
// Output: apps/desktop/resources/claude-science/manifest.json, plus the
// SKILL.md files and agent prompt fragments copied into the same
// directory tree (one layer deep, so bundling is just a recursive copy).
//
// Invocation:
//   node apps/desktop/scripts/build-claude-science-manifest.mjs [--from <path>]
//
// Why a script and not an embed.FS in Go:
//   - the OpenScience repo is a separately maintained project. Walking
//     the on-disk source lets us rebuild the manifest after each
//     OpenScience release without touching the Go server.
//   - the manifest file is human-readable; a developer can diff it
//     between OpenScience versions to catch skill churn.
//   - the renderer / installer reads this manifest at runtime via
//     `apps/desktop/src/main/claude-science-manager.ts::loadManifest`,
//     keeping the existing resource-loading pattern consistent with
//     pg/manifest.json.

import { existsSync, readdirSync, readFileSync, writeFileSync, mkdirSync, statSync, copyFileSync } from "node:fs";
import { join, relative, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, "..", "..", "..");
// Source-of-record lives under vendor/ so bundle-cli can copy from a
// stable path that is not co-located with the destDir (which bundle-cli
// wipes each run). Splitting source / destination prevents the
// "rm dest then can't find source" race that bit PR 5's first
// build attempt.
const sourceDir = join(repoRoot, "apps", "desktop", "vendor", "claude-science-manifest");
const outputDir = sourceDir;
const skillsOut = join(outputDir, "skills");
const agentsOut = join(outputDir, "agents");

const args = process.argv.slice(2);
const fromFlag = args.indexOf("--from");
const OPENSCIENCE_SOURCE = fromFlag !== -1
  ? args[fromFlag + 1]
  : "/Users/jiangjianyan/Downloads/openscience-main";

mkdirSync(outputDir, { recursive: true });
mkdirSync(skillsOut, { recursive: true });
mkdirSync(agentsOut, { recursive: true });

if (!existsSync(OPENSCIENCE_SOURCE)) {
  console.warn(
    `[build-claude-science-manifest] OpenScience source not found at ${OPENSCIENCE_SOURCE}. ` +
      "Skip and emit an empty manifest. Pass --from <path> to point at a different checkout.",
  );
  // Only emit into vendor/ when OpenScience is available — never write
  // a stale manifest into resources/, because bundle-cli's rm destDir
  // step can wipe source-of-record by accident if the two paths
  // coincide.
  mkdirSync(sourceDir, { recursive: true });
  writeFileSync(join(sourceDir, "manifest.json"), JSON.stringify({
    schema_version: 1,
    experimental_source: "claude_science_lab",
    workspace: { name: "Claude 科研实验室", slug: "claude-science" },
    skills: [],
    agents: [],
    squads: defaultSquads(),
    installed_at_build: false,
  }, null, 2));
  process.exit(0);
}

const skillsRoot = join(OPENSCIENCE_SOURCE, "backend", "cli", "skills");
const agentsRoot = join(OPENSCIENCE_SOURCE, "backend", "cli", "src", "agent", "prompt");

// Catalogue the skills. OpenScience groups them by top-level category
// (biology, research, ...) and then by skill name. We emit one manifest
// entry per skill; the supporting files (additional md / txt under the
// skill dir) ride along as `body_path` references.
const skills = [];
if (existsSync(skillsRoot)) {
  for (const category of readdirSync(skillsRoot)) {
    const catPath = join(skillsRoot, category);
    if (!statSync(catPath).isDirectory()) continue;
    for (const name of readdirSync(catPath)) {
      const skillPath = join(catPath, name);
      if (!statSync(skillPath).isDirectory()) continue;
      const skillMd = join(skillPath, "SKILL.md");
      if (!existsSync(skillMd)) continue;
      const relPath = relative(skillsRoot, skillMd);
      // Copy the skill directory into resources so the runtime has the
      // raw SKILL.md + supporting files on disk.
      const dest = join(skillsOut, relPath);
      copyDir(skillPath, dest);
      skills.push({
        category,
        name,
        body_path: `skills/${relPath}`,
      });
    }
  }
}

// Catalogue the agents. We surface only the ones the local fork ports
// over (research / biology / physics / ml / write / plan); the opencode
// subagents (task / explore / compaction / title) stay out — they are
// OpenScience's internal machinery, not the user-facing research team.
const PORTED_AGENTS = new Set(["research", "biology", "physics", "ml", "write", "plan"]);
const agents = [];
if (existsSync(agentsRoot)) {
  for (const name of readdirSync(agentsRoot)) {
    const promptPath = join(agentsRoot, name);
    if (!statSync(promptPath).isFile()) continue;
    const stem = name.replace(/\.txt$/, "");
    if (!PORTED_AGENTS.has(stem)) continue;
    copyFileSync(promptPath, join(agentsOut, `${stem}.txt`));
    agents.push({
      name: stem,
      prompt_path: `agents/${stem}.txt`,
      category: primaryOrSub(stem),
    });
  }
}

const manifest = {
  schema_version: 1,
  experimental_source: "claude_science_lab",
  workspace: {
    name: "Claude 科研实验室",
    slug: "claude-science",
    description: "科研工作台，Skills / Agents / Squads 全部由 claude_science_lab flag 装载。",
  },
  skills,
  agents,
  squads: defaultSquads(),
  installed_at_build: true,
  built_from: relative(repoRoot, OPENSCIENCE_SOURCE),
  counts: {
    skills: skills.length,
    agents: agents.length,
    squads: 5,
  },
};

mkdirSync(outputDir, { recursive: true });
writeFileSync(join(outputDir, "manifest.json"), JSON.stringify(manifest, null, 2));
console.log(`[build-claude-science-manifest] wrote ${skills.length} skills + ${agents.length} agents to ${outputDir}`);

// defaultSquads returns the 5 squads wired in PR 6: 4 domain + 1 joint.
// The manifest is the source of truth; the installer iterates this list
// and creates the corresponding squad rows + member rows.
function defaultSquads() {
  return [
    {
      name: "Claude Science 生物",
      description: "生物 / 化学领域的研究 squad,带头人 biology agent。",
      members: ["biology", "plan"],
      leader_agent: "biology",
    },
    {
      name: "Claude Science 物理",
      description: "物理 / 仿真 / 量子 / 数据分析领域的研究 squad,带头人 physics agent。",
      members: ["physics", "plan"],
      leader_agent: "physics",
    },
    {
      name: "Claude Science 机器学习",
      description: "机器学习 / 训练 / 推理领域的研究 squad,带头人 ml agent。",
      members: ["ml", "plan"],
      leader_agent: "ml",
    },
    {
      name: "Claude Science 科研",
      description: "通用科研 / 文献综述 / 假设生成 / 同行评审 squad,带头人 research agent。",
      members: ["research", "write"],
      leader_agent: "research",
    },
    {
      name: "Claude Science 联合体",
      description: "用户交互入口：用户在这里提出问题，由 research 委托到对应学科的 squad。",
      members: ["research", "write", "plan"],
      leader_agent: "research",
    },
  ];
}

function primaryOrSub(stem) {
  if (stem === "research" || stem === "plan") return "primary";
  if (stem === "write") return "subagent";
  return "domain";
}

function copyDir(src, dst) {
  mkdirSync(dst, { recursive: true });
  for (const entry of readdirSync(src)) {
    const s = join(src, entry);
    const d = join(dst, entry);
    if (statSync(s).isDirectory()) {
      copyDir(s, d);
    } else {
      copyFileSync(s, d);
    }
  }
}
