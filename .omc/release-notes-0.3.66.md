---
name: release-notes-0.3.66
created: 2026-07-28T23:48:56Z
updated: 2026-07-28T23:48:56Z
---

# release-notes-0.3.66 (tech-debt + stability audit fixes)

> Status: **SHIPPED 2026-07-29** — `/Applications/Multica.app` 0.3.66,
> cold-start verified (three-check pass; workspace=1 / agent=92 invariants
> held, issue/comment drift expected). Ship log: `.omc/0.3.66-ship-2026-07-29.md`.
> No new migration this round (last is 168, applied in 0.3.64) — all changes
> are TypeScript / scripts / docs. electron-builder did NOT deadlock this run.

Follow-up to the two-agent tech-debt + stability audit. Fixes the audit's
high-severity findings plus one same-class crash discovered during gate
verification. None of these add schema; the runtime fixes only take effect
after rebuild + install, which is why 0.3.66 ships.

## Runtime stability fixes (need the reinstall to take effect)

- **C1 — ChatWindow rules-of-hooks latent crash.** `chat-window.tsx` had an
  `if (!wsId) return` guard sitting *above* ~20 hooks. A `wsId` that flipped
  falsy↔truthy within one mount changed the hook count and crashed React
  ("Rendered fewer hooks than expected"), unmounting the whole window. Moved
  the guard below every hook (queries fired with an undefined wsId are
  harmless — none of their results render in that state). Cleared all 37
  `react-hooks/rules-of-hooks` errors.
- **claude-lab-view rules-of-hooks (same class, found during verification).**
  `LabAgentFromIssue` had its second `useQuery` (agent-name lookup) *after* an
  `if (!selectedIssueId) return` guard. Same hook-count crash on issue
  flip. Moved the guard below both queries (both are `enabled`-gated, so
  behaviour is unchanged). This is in the Claude Lab surface the user reported
  errors in.
- **P1-2 — root-level ErrorBoundary.** The desktop renderer had NO root error
  boundary: any render-time throw (un-defended `undefined.map`, drifted field,
  missing i18n key) escaped React and blanked the entire window to white. The
  2026-07-14 AppSidebar incident only got a *local* boundary. Added a root
  `<ErrorBoundary>` in `App.tsx` (inside ThemeProvider, outside CoreProvider so
  a provider crash is also caught) with a dependency-free bilingual fallback
  (retry / reload) — deliberately no `useT`, since an i18n crash is exactly the
  class this catches. Emits `client_render_error`.
- **P2-1 — `getLabContext` schema.** Was a raw `(await r.json()) as LabContext`
  cast (violates the API-compat contract; could white-screen the Claude Lab
  workbench on a drifted gated response). Added `LabContextSchema` (lenient:
  wide-string enums, defaulted nullables, `.loose()`) + `EMPTY_LAB_CONTEXT`,
  routed through `parseWithFallback`. NOTE: the audit's P2-1 list over-reported
  — the Squad endpoints already go through `parseWithFallback`, and `ws-client`
  already has a runtime boundary guard (MUL-3418); `getLabContext` was the one
  genuine raw cast. 4 drift tests added (core `schemas.test.ts`, 57 pass).

## Developer-gate / tooling fixes

- **H1 — i18n hardcoded strings 57 → 0.** All `i18next/no-literal-string`
  violations in `packages/views` extracted to the 4 locales (en/zh-Hans/ja/ko),
  50 new keys across the `experimental` / `claude-lab` / `layout` / `modals`
  namespaces, all arrow-expression selectors (no block-body — the white-screen
  path). 52/58 were in the 0.3.60 user-plugin system that shipped un-localized.
  (The `chat-window` no-workspace string became `chat.window.no_workspace` as
  part of the C1 fix.)
- **P1-1 / P1-3 — enforced ship chain.** New `scripts/ship-mac.sh` (+ `make
  ship-mac`) runs the canonical chain in order and aborts on first failure:
  snapshot → migrate up → bundle-cli → build → `electron-builder --dir` →
  asar `rawRequest` check → (confirm gate) → `cp -R` → re-sign nested binaries
  → cold-start verify. This is the first real caller of
  `desktop-sign-nested-binaries.sh` (previously zero callers, so "runnable" ≠
  "run"). The install step prompts before the destructive `/Applications`
  overwrite (`--yes` to auto-confirm, `--build-only` to stop before install).

## Verification

- `packages/views` eslint: `no-literal-string` 0; package exit 0 (removed two
  dead `@next/next/no-img-element` disable comments — rule not loaded in views
  — and one unused-disable directive).
- typecheck: `@multica/core`, `@multica/views`, `@multica/desktop` (node+web),
  `@multica/web` all exit 0.
- tests: core 57, views 1282, desktop 321 — all pass.
- `make check-fast` still red **only** on `@multica/desktop#lint` — a separate
  PRE-EXISTING pool (20 errors in files untouched this session; see below).

## Known remaining debt (NOT fixed this round — deliberately)

- **`apps/desktop` pre-existing lint pool (20 errors).** Not part of the audit
  cards and none in files touched this session. Breakdown: 12 `require()`-style
  imports in main-process files (`server-manager.ts`, `pythia-manager.ts`,
  `manager-template.test.ts` — likely intentional Electron-main CJS), 3
  `shell.openExternal` (should use `openExternalSafely`), 2 `prefer-const`, 2
  `recharts` extraneous-dependency (`claude-lab-view`, `experimental-artifact-view`
  — recharts resolves via hoisting from `packages/views`, renderer-bundled so
  not a runtime/packaging blocker), + exhaustive-deps / unused-disable warnings.
  Several touch P0 main-process files → deferred to a separate, care-flagged
  pass rather than a blind rewrite.
- **M2 — `app-builder-bin` not pinned.** `builder-util@26.8.1` exact-pins
  `5.0.0-alpha.12` (the macOS-27 `electron-builder --dir` deadlock culprit);
  npm `latest`=4.2.0 (cross-major, IPC-contract risk), `next`=5.0.0-alpha.13.
  Not pinned blindly — can't validate without a real build run. Ship uses the
  documented manual asar-repack fallback if it deadlocks.
- **H2 — regression tests** for 4 incident-prone subsystems (auth login
  workspace binding, user-plugin CRUD, task skill-inject, mythos supervise
  completion) — pending.
- **M1/M3/M4/M5 cleanup** (search-store → core, onboarding_shim deletion
  [needs approval], experimental_proxy legacy else, stale comments/mocks) —
  pending; M3/M4 are deletion-class and need explicit sign-off.
