---
name: release-notes-0.3.67
created: 2026-07-30T00:40:32Z
updated: 2026-07-30T00:40:32Z
---

# release-notes-0.3.67 (tech-debt + stability audit fixes — SHIPPED 2026-07-30)

> Status: **shipping 2026-07-30** — 22 atomic commits since 0.3.66 land:
> tech-debt + stability audit fixes (C1/P2-1/P1-2/P1-3), H1 i18n
> extraction, M4 + M3 PR1 + M3 PR2 cleanup, plus H2 regression tests
> for the 0.3.63 composite-plugin skill injection contract. M2 pins
> app-builder-bin to 5.0.0-alpha.13 (override recorded; lockfile
> regen deferred to next network round). No new migration this round
> (last is 168, applied in 0.3.64) — all changes are Go + TypeScript +
> scripts + docs.

This ship closes the last forks cloud-billing surface
(JoinCloudWaitlist, the only web /download page funnel) and aligns
the codebase with CLAUDE.md "No cloud features" end-to-end.

## Runtime stability fixes (need the reinstall to take effect)

- **C1** — ChatWindow rules-of-hooks crash. The `if (!wsId) return`
  guard sat above ~20 hooks; a wsId flip in one mount crashed React.
  Guard moved below all hooks. Cleared 37 lint errors.
- **claude-lab rules-of-hooks** (same family, found during gate
  verification) — `LabAgentFromIssue` had its second useQuery after
  an early return. Both queries are enabled-gated; guard moved below
  both. This is in the Claude Lab surface the user has been actively
  testing.
- **P1-2** — root-level ErrorBoundary in App.tsx. The renderer had
  no root boundary; any render throw blanked the whole window. The
  2026-07-14 AppSidebar incident only got a local boundary. New
  fallback is dependency-free (no useT) — an i18n crash is exactly
  the class of bug it catches, so the fallback must not depend on
  i18n.
- **P2-1** — LabContext schema-validated. Was a raw
  `as LabContext` cast on a gated endpoint. `LabContextSchema` +
  `EMPTY_LAB_CONTEXT` + parseWithFallback. NOTE: the audit's P2-1
  list over-reported — the Squad endpoints already go through
  `parseWithFallback`, and `ws-client` already has a runtime
  boundary guard (MUL-3418). `getLabContext` was the one genuine
  raw cast.

## Developer-gate / tooling fixes

- **H1** — i18n hardcoded strings 57→0. All no-literal-string
  violations in packages/views extracted to the 4 locales
  (en/zh-Hans/ja/ko), 50 new keys across experimental / claude-lab /
  layout / modals namespaces, all arrow-expression selectors
  (block-body is how the 2026-07-14 incident crashed the desktop
  window).
- **P1-1 / P1-3** — enforced ship chain. `scripts/ship-mac.sh` + `make
  ship-mac` runs the canonical chain end-to-end: snapshot → migrate
  up → bundle-cli → build → electron-builder --dir → asar rawRequest
  guard → (confirm gate) → cp -R /Applications → re-sign nested
  binaries → cold-start verify. This is the first real caller of
  `desktop-sign-nested-binaries.sh` (previously zero callers).
- **M2** — pin app-builder-bin 5.0.0-alpha.13. Same-line increment
  from the alpha.12 that intermittently deadlocks electron-builder
  on macOS 27. Lowest-risk mitigation per the audit (option a).
  builder-util@26.8.1 exact-pins the version, so an override is
  required. package.json updated; pnpm-lock.yaml regen deferred to
  next network round (registry.npmmirror.com ECONNRESET in this
  turn).
- **M4** — drop experimental_proxy legacy else + 2 dead handlers.
  Net -36 lines; production has always had a non-nil
  ExperimentRegistry, so the else branch was dead since 0.3.22.
- **M3 PR1** — drop onboarding_shim.go (613 lines) +
  BootstrapOnboarding* routes + 5 tests. Renderer was already
  creating the Helper agent + starter issue via generic
  CreateAgent/CreateIssue, so the legacy "bootstrap endpoints" were
  unused. Net -1080 lines.
- **M3 PR2** — retire JoinCloudWaitlist end-to-end. Server
  handler + route + 5 tests + sqlc query + analytics event +
  prometheus counter + funnel path. TS store + component + web
  CloudSection + page.tsx. Closes the last fork cloud-billing
  surface (the only web /download page funnel) and aligns with
  CLAUDE.md "No cloud features". Net -630 lines. **Kept**:
  `user.cloud_waitlist_*` columns and `User.CloudWaitlist*` model
  fields (forward-only migration 052 hard constraint; 7 sqlc
  SELECTs reference them).

## Regression tests (H2)

- **tickSupervision completion** — 7 subtests + snap-to-total
  guardrail. Production now reads `final_issue_id` status via a
  `tickSupervisionQuerier` interface seam; the previous "never wrote
  SubTasksDone" bug is fixed AND pinned.
- **UsernameLogin upsert / idempotent** — 5 subtests incl. the
  `CreateUser==0` idempotent invariant that nails the 2026-06-27
  "username-only login loses workspaces" regression.
- **user_plugin CRUD** — 30 subtests + 3 hard nails (migration-168
  partial unique index, 409 dual-path, list tombstone filter). All
  validated with mutation tests.
- **LoadAgentSkillsForClaim skill-inject** — 4 subtests
  (enabled / disabled / missing / dedup). Pins the 0.3.63
  composite-plugin contract: enabled user_plugin's
  capabilities.skills injects into every agent's bundle, disabled
  contributes nothing, missing skill is skipped without erroring
  the claim.

## Verification

- pnpm typecheck (full monorepo 6/6) exit 0
- apps/web + apps/desktop typecheck exit 0
- packages/views lint exit 0 (1 pre-existing unused-disable warning)
- packages/core tsc + vitest 0
- packages/views vitest 1282 tests pass
- server: handler / experimental / metrics / analytics / mythos
  tests all 0
- go build ./... 0

## Known remaining debt (NOT fixed this round — deliberately)

- apps/desktop pre-existing lint pool (20 errors / 5 warnings in
  files untouched this round) — pre-existing style debt, several
  touch P0 main-process files. Deferred to a separate care-flagged
  pass.
- H2 task skill-inject sub-item — actually shipped in 0.3.67.
- H2 user-plugin CRUD (shipped).
- H2 UsernameLogin (shipped).
- H2 tickSupervision (shipped).
