---
name: release-notes-0.3.68
created: 2026-07-30T14:44:54Z
updated: 2026-07-30T14:44:54Z
status: complete
---

# 0.3.68 — Issue detail full-width layout

## User-visible

- **Issue detail page now uses a full-width content column.** Removed the
  `mx-auto max-w-4xl` centered max-width constraint on the issue detail body
  (both the loading skeleton and the live content container in
  `packages/views/issues/components/issue-detail.tsx`). Long titles, wide
  tables, and code blocks now use the available panel width instead of being
  clamped to a centered 4xl column.

## Repo infrastructure (does NOT ship in the .app)

These are documentation / build-infra changes only — none of them change the
packaged renderer or backend binaries:

- Sub-domain `CLAUDE.md` / auto-synced `AGENTS.md` mirrors added across
  `apps/{desktop,mobile,web}`, `packages`, `packages/views`, and `server`
  (the "Sub-domain Guides" navigation system from the root `CLAUDE.md`).
- `scripts/check-agents-docs-sync.mjs` (+115 lines) enforces CLAUDE.md ↔
  AGENTS.md parity and the shared critical-constraint tokens; wired into the
  CI `docs-sync` job and `githooks/pre-push`.
- `packages/eslint-config/boundaries.js` + per-package `eslint.config.mjs`
  updates (core / ui / views) for package-boundary lint rules.

## Ship path

Manual asar-repack fallback (renderer-only patch; no resource-path changes),
same recovery path as 0.3.67. electron-builder's electron download is still
blocked by proxy ECONNRESET, and the M2 `app-builder-bin` pin has not landed
in the lockfile yet.
