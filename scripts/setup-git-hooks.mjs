#!/usr/bin/env node
// Points git's core.hooksPath at the committed `githooks/` directory so the
// pre-push fast-check runs for every contributor with no extra install step.
// Invoked by the root package.json `prepare` script during `pnpm install`.
//
// This must never fail an install: Docker image builds and tarball installs
// copy source without a `.git` directory, and CI checkouts may run in odd
// states. In any of those cases we quietly skip instead of erroring out.
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));

function tryGit(args) {
  return execFileSync('git', args, { cwd: repoRoot, stdio: ['ignore', 'pipe', 'ignore'] })
    .toString()
    .trim();
}

try {
  // Only configure hooks inside an actual git work tree.
  if (tryGit(['rev-parse', '--is-inside-work-tree']) !== 'true') {
    process.exit(0);
  }

  const hooksDir = join(repoRoot, 'githooks');
  if (!existsSync(hooksDir)) {
    process.exit(0);
  }

  // Store a repo-relative path so the setting stays valid regardless of where
  // the checkout lives on disk.
  execFileSync('git', ['config', 'core.hooksPath', 'githooks'], {
    cwd: repoRoot,
    stdio: 'ignore',
  });
  console.log('✓ git hooks configured (core.hooksPath=githooks; pre-push runs `make check-fast`).');
} catch {
  // No git, no repo, or git not on PATH — nothing to configure. Silent by design.
  process.exit(0);
}
