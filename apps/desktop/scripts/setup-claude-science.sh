#!/usr/bin/env bash
# Builds the OpenScience single-binary from
# /Users/jiangjianyan/Downloads/openscience-main and stages it under
# apps/desktop/vendor/openscience-bin/openscience so bundle-cli picks it
# up on the next desktop package run.
#
# Why this is a standalone script:
# - The OpenScience repo is a Bun + Vite monorepo with a multi-stage
#   build (`bun install` → frontend/workspace build → bun --compile).
#   It is heavier than what bundle-cli.mjs should run automatically on
#   every package invocation.
# - The build needs `~/.config/openscience/` for first-run config; we
#   only stage the binary, the user still has to run `openscience login`
#   or `openscience keys add` once on their machine before the manager
#   can boot a working model picker.
# - This script intentionally does NOT install npm deps inside the
#   Multica repo — OpenScience has its own bun.lock and bunfig.toml.
#   Running `bun install` inside apps/desktop/vendor would pollute the
#   monorepo and break pnpm.
#
# Exit codes:
#   0 — binary staged (or already present and up-to-date)
#   1 — build failed or source repo missing
#   2 — staging failed (cp / chmod)
set -euo pipefail

REPO="${OPENSCIENCE_SRC:-/Users/jiangjianyan/Downloads/openscience-main}"
VENDOR_DIR="${MULTICA_REPO_ROOT:-/Users/jiangjianyan/jjy/multica-main}/apps/desktop/vendor/openscience-bin"
DEST="${VENDOR_DIR}/openscience"
BUILT="${REPO}/backend/cli/dist/@synsci/openscience-darwin-arm64/bin/openscience"

if [[ ! -d "${REPO}" ]]; then
  echo "[setup-claude-science] source repo not found: ${REPO}" >&2
  echo "Set OPENSCIENCE_SRC=/path/to/openscience-main and re-run." >&2
  exit 1
fi

# OpenScience's build script calls `git branch --show-current` for the
# channel name. The user's Downloads copy is not a git checkout, so we
# init a minimal one if missing — this is local to the downloads dir
# and does NOT touch Multica's repo.
if [[ ! -d "${REPO}/.git" ]]; then
  echo "[setup-claude-science] initialising local git in ${REPO} (build script reads branch)"
  (cd "${REPO}" && git init -q && git checkout -q -b main 2>/dev/null || true)
fi

mkdir -p "${VENDOR_DIR}"

# If the build is already fresh (< 24h), skip. The check is mtime on
# the built binary — OpenScience's own build script does not have a
# stable fingerprint we can compare against.
NEEDS_BUILD=1
if [[ -x "${BUILT}" ]]; then
  if [[ -z "$(find "${REPO}/backend/cli/src" -newer "${BUILT}" -type f -print -quit 2>/dev/null)" ]]; then
    NEEDS_BUILD=0
  fi
fi

if [[ "${NEEDS_BUILD}" == "1" ]]; then
  echo "[setup-claude-science] bun install (monorepo)"
  (cd "${REPO}" && bun install --silent)
  echo "[setup-claude-science] building single-binary (this takes ~30-90s on first run)"
  (
    cd "${REPO}/backend/cli"
    OPENSCIENCE_CHANNEL=local OPENSCIENCE_VERSION=0.0.0-local \
      bun run build -- --single 2>&1 | tail -20
  )
fi

if [[ ! -x "${BUILT}" ]]; then
  echo "[setup-claude-science] build did not produce ${BUILT}" >&2
  exit 1
fi

echo "[setup-claude-science] staging ${BUILT} -> ${DEST}"
cp "${BUILT}" "${DEST}"
chmod 755 "${DEST}"

# The downloaded binary carries an upstream Developer ID signature
# that conflicts with ad-hoc signing on copy. Strip the xattrs so
# Gatekeeper does not flag the bundled copy.
xattr -cr "${DEST}" 2>/dev/null || true

# Ad-hoc sign for our bundle. The upstream signature blocks this —
# we strip it first via codesign --remove-signature.
codesign --remove-signature "${DEST}" 2>/dev/null || true
codesign -s - --force "${DEST}" 2>&1 | tail -3 || true

echo "[setup-claude-science] done. Now run:"
echo "  cd ${MULTICA_REPO_ROOT:-/Users/jiangjianyan/jjy/multica-main}"
echo "  pnpm --filter @multica/desktop bundle-cli"
echo "  pnpm --filter @multica/desktop package"
echo "Then enable the 'claude_science_lab' Labs flag and click the sidebar"
echo "entry. First boot will need an external 'openscience login' or"
echo "'openscience keys add' to configure a model — that flow lives"
echo "outside Multica by design (model routing stays on OpenScience,"
echo "not Multica's provider chain)."