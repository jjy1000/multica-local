---
name: release-notes-0.5.78
created: 2026-08-27T19:30:00+08:00
updated: 2026-08-27T19:30:00+08:00
---

# 0.5.78 — Labs Hardening Batch (labs plan Phase 0–2)

**Branch:** `epic/0.5.72-followups`
**Plan:** `.omc/plans/labs-plugin-upstream-hardening-0.5.78-plan.md` (draft → executed)
**Builds on:** 0.5.77 (Batch 5 REVIEW-tier cherry-picks)

## TL;DR

First ship of the labs/plugin hardening plan. One real fork defect fixed in the lab picker, one real fork defect fixed in user-plugin teardown (visibility row leak), the intermittent morning-run dashboard test flake root-caused and killed (DB session timezone trap), semantica packaging contract closed end-to-end (surface block + long-missing wheel builder), openscience standalone binary formally declared out of scope, mcp_server lock release contract documented in code.

## Upstream integration status (P0-1 / P0-2)

- **Re-triage:** upstream/main still at `09a2410e8` (triage head) — **0 new commits** since `.omc/upstream-integration-triage-2026-08-26.md`. Nothing to re-grade this cycle.
- **Batch 4 docs reconciliation result:**
  - ✅ `90932227c` — MUL-6711 landing hero preload ported, adapted to fork's jpg backdrop + local dimensions (`priority` → `preload` on ProductImage).
  - ✅ `0fd5fb3f8` — MUL-6189 contributor-doc half (CONTRIBUTING pins = fork toolchain). The package.json `engines>=22` half was already present.
  - ⏭️ **SKIP class — landing-page changelog entries v0.4.30…v0.4.35 (+ drop-reverted pair `c7d66071e`/`21b938bfd` & predecessors):** the entries live in `apps/web/features/landing/i18n/*.ts` and advertise fork-excluded surfaces verbatim ("Checkout pre-fills your account email", DingTalk/Lark/Slack/Telegram/WeCom channel sessions, "cloud runtimes, billing, seat management"). Shipping that copy would publish features this fork deliberately does not have. Fork keeps its own release history under `.omc/release-notes-*`.
  - ⏭️ SKIP `a1d160ee` (MUL-6468 dimcode.ai dead link): the dead link does not exist anywhere in the fork's docs tree.
  - ⏸️ DEFER `f78c8671` (tasks.mdx execution-log passage): fork's tasks docs were restructured; the target section does not exist. Needs a docs-vs-fork-UI audit before a hand-port — not a blind cherry-pick.

## Real defect fixes

### Lab ↔ assignee mutex restored in the LabPicker (P2-6b) — `057947b84`

The picker wiped the manual assignee for EVERY non-mythos lab pick, but the 0.3.33 mutex narrowing (realigned 2026-07-28) reserves the roster only for **mythos_swarm sole-mode** and **swarm_topology** (Active Contract #5). Picking Claude Science / Pythia / Semantica / Code Canvas / LLM Wiki silently discarded the user's engineer despite the "all other labs allow assignee + lab" contract. Fix: clear gated behind `ASSIGNEE_MUTEX_LABS`; mythos keeps its sole/enhancer nuance; swarm_topology joins the clear-before-update ordering guarantee. Tests rewritten to pin the narrowed semantics (old broad-clear pin WAS the defect). Labs-audit Still-open item P2-6b closed in code, not by doc edit.

### User-plugin teardown leaked visibility rows (P2-1a) — `61712fd65`

DeleteUserPlugin tombstoned the plugin but left its `experimental_resource_visibility` rows behind. `lab_managed` is stamped from row EXISTENCE alone (`ListLabManagedResourceIDs`, flag-agnostic), so after deleting a plugin the user's own agents/squads stayed greyed out of regular pickers indefinitely. Now: `seedPluginVisibility` purges the flag key's rows before seeding (covers manifest capability removals on update AND slug reuse after soft-delete) and DELETE purges them at teardown (best-effort there — tombstone is committed and recreate re-purges). New sqlc query `DeletePluginResourceVisibilityByFlagKey`; regression test pins purge count + flag key.

## Stability / environment hardening

### Dashboard window tests UTC-anchored — morning-run flake killed (P0-3) — `9280f0bf5`

Root cause of the TestDashboardPerAgentRollupsUseExactWindow lock held since 0.5.47: seeds used bare `CURRENT_DATE` ("yesterday noon"), which follows the **Postgres session TimeZone**, while both rollup windows cut on **UTC midnights**. This dev DB runs `Asia/Shanghai` — any suite run between midnight and ~08:00 local slid yesterday's seeded 900s run into the days=1 window and failed. Every recent ship verified in exactly that window (e.g. backup timestamp `pre-update-20260827-080351`). Seeds now derive the date via `(now() AT TIME ZONE 'UTC')::date`. Verified: rollups ×2 green, endpoints green, quick-create race ×3 under `-race`, full handler package green with DB attached.

### Semantica packaging contract closed (P1-2 / audit P3-6) — `c91a6a975`

- `semantica/manifest.json` now declares `surface.proxy_prefix` + `loopback_service` matching catalog.go's `/experimental/semantica` mount (pythia_oracle half was already aligned).
- `apps/desktop/scripts/build-semantica-wheel.sh` finally exists — run.sh's header AND bundle-cli's builds/-missing warning have referenced it since 0.5.53, but the script itself was never committed. Validated end-to-end: built `semantica-0.6.6-py3-none-any.whl` from the vendored subtree.
- Wheel parity check: `vendor/semantica-src/builds/` ↔ `resources/semantica/builds/` diff clean; pythia engine vendor↔resources also byte-identical; `pythia-smoke.sh` 9/9 passed.

### openscience standalone binary declared permanently optional (P1-3, option B) — `51f15fdc2`

claude_science_lab is inline RuntimeKind routed through the Multica runtime bridge; the drop-shipped `openscience` native binary only ever served the gateway-bypassing subprocess path this fork doesn't ship. bundle-cli now logs an informational note instead of warning on every build; CLAUDE.md Known-Stability entry records the decision. (If someone later wants the subprocess path, the existing cp block stages + ad-hoc codesigns automatically.)

### mcp_server lock release contract documented (audit residual) — `3babe091a`

Code-level contract at `lock.go::Claim` + lifecycle-map addendum: agent/squad/skill/member/workspace orphans get the periodic sweep (migration 274 backfill + 6h tick); swarm_run locks are runner-released; **mcp_server has no GC fallback by design** — the first future writer must own release and extend the sweep BEFORE landing.

### Signed artifact URL TTL pinned at the HTTP boundary (P2-1b)

Serve-path subtest proves an expired signature is 403 before any artifact lookup/file IO (unit verify() already covered expiry/tamper/cross-slug/non-hex/wrong-length).

## Decision defaults recorded (STOP-AND-ASK gates, conservative choices — all reversible)

| Gate | Decision | Rationale |
|---|---|---|
| B5-starter (upstream per-agent conversation starters, ~2074 LOC) | **Option B**: keep fork's 3-button local starter_prompts; SKIP `f8ec870f` + `09a2410e8` | Upstream architecture conflicts conceptually with shipped fork UX; porting = multi-session project for one empty-state feature |
| source-context sub-issue `8c563b49` (10K LOC) | **SKIP** | Feature fork never had;连带 SKIP MUL-6660/B5-locale |
| MUL-6350 plugin rebuild series | **SKIP-DIVERGENCE unchanged** | Fork plugin layer is custom + F-006/F-008/F-013 hardened; wholesale adoption violates surgical discipline |
| "Durable scheduled hooks" idea | Parked as future fork-local design proposal (user_plugin schedule trigger) — NOT scheduled | Wait for explicit product need |

## Verification (ship gate)

- `node scripts/check-agents-docs-sync.mjs`: ✓ agree (after CLAUDE.md edit)
- `pnpm typecheck` (turbo full): see ship log
- `cd server && go test ./internal/handler/ ./internal/experimental/ ...`: handler + experimental green (dashboard trio + plugin suites included)
- Desktop script tests: 22/22 (`scripts/package.test.mjs`)
- Pre-existing failures NOT addressed here (documented for next cycle): `issue-detail.test.tsx > highlightCommentId scroll-to-comment` ×2 — baseline-failing before this batch (verified by stash-baseline run), unrelated to labs; needs its own root-cause pass.

## Deferred to 0.5.79 (per plan)

- P2 remainder: manual E2E smoke checklist on packaged app (create→run→artifacts→delete), long-run subprocess observation appendix, UpdateIssue×swarm mutex pin confirmation.
- P3-A inbox archive foundation chain (~3–5 commits) + MUL-6632 main body.
- issue-detail highlightCommentId ×2 pre-existing failure investigation.
