# 0.5.14 Release Notes

> **Shipped: 2026-08-10.** Fork-local cleanup batch — no upstream cherry-picks this
> cycle. Zero migrations. Pure repo hygiene.

## What landed

### Repo hygiene (chore only — no user-facing change)

- **`chore(repo): gitignore security/vuln-scan scratch state dirs`** — `.threat-model-state/`,
  `.triage-state/`, `.vuln-scan-state/` are pipeline-internal working state from
  the threat-model + vuln-scan skills (JSON chunks, partial python extractors,
  staging files). The final deliverables (THREAT_MODEL.md, VULN-FINDINGS.md,
  VULN-FINDINGS.json) belong in `.omc/audit/` once reviewed; the scratch dirs
  themselves should never reach the repo.

- **`chore(docs): commit pre-0.5.14 upstream + tech-debt planning notes`** —
  three `.omc/` planning artifacts produced during the 0.5.13 era that drove
  the upstream-integration work, but were untracked until now:
  - `.omc/upstream-0.4.0-main-port-candidates-2026-08-06.md` — 0.5.11-era survey
    of v0.4.0..main cherry-pick candidates (~70 work items, 22+ clusters). Most
    "immediate S/M" candidates it listed landed in 0.5.12 (#6194, #5406, #5355,
    #6095, #6124) and 0.5.13 (#5674 revert+redo, #6515, #6546, custom_args
    writer, addSubscriber filter). The P0 Runtime Unbind (#6220) is the
    highest-value remaining item — deferred to 0.5.15+.
  - `.omc/cherry-pick-fork-runtime-catalog-2026-08-05.md` — server/pkg/agent/
    runtime census: fork has 16, upstream gained 6 more (qwenpaw, qwen,
    reasonix, traecli, deveco, grok) + 6 shared helpers (stream_json/scanner,
    openclaw_stdout, mcp_config, browser_mcp_config, acp_deliverable). The
    shared-helpers row is the lowest-cost follow-up cherry-pick; new runtimes
    require a separate decision.
  - `.omc/tech-debt-text-size-migration-plan-2026-08-01.md` — Phase-B type-scale
    migration residual: ~1060 raw text-xs/sm/base/lg/xl sites across 176
    files, target ~755 sites in 92 web/desktop files (mobile + docs excluded).
    Authoritative mapping table from Phase B's actual diff. Defer to a dedicated
    cleanup PR after 0.5.14.

- **`chore(release): bump 0.5.13 → 0.5.14`** — version-only bump in
  `apps/desktop/package.json` (the canonical version source per
  `CLAUDE.md → Version source`).

### Upstream cherry-pick status (this cycle)

**0 picks.** A systematic survey of `v0.4.13..upstream/main` (258 distinct
upstream PRs, 127 fix/feat/refactor/perf candidates) was filtered for
zero-conflict Class A candidates (≤4 files, all-changed-files-exist-in-fork,
no new-file feature dependencies, no fork deletions). **0 candidates
survived** — every small surgical PR is already integrated through the
0.5.8 → 0.5.13 batches. The remaining missing upstream PRs are full-feature
blocks (saved issue views, channel framework, font overhaul, ACP backends,
Mika onboarding, WeCom/DingTalk/Lark channels) which exceed "fork-local
cleanup" scope and require per-block planning.

## What was deliberately NOT shipped

- **Saved issue views V1** (`#6516` cluster, ~10 commits, 3000+ lines) — multi-
  session feature integration; would touch CLAUDE.md, require schema review
  of `core/issue-views/*` module shape, and the new-feature deps cascade
  (view-bar.tsx + ManageViewsDialog + useSingleRowFit). Deferred to 0.5.15+
  with a dedicated feature-block plan.
- **Runtime catalog gap** (6 new runtimes + 6 shared helpers, ~1500 lines) —
  requires deciding whether the fork-localized single-user product should
  expose the additional runtimes. Deferred — captured in
  `.omc/cherry-pick-fork-runtime-catalog-2026-08-05.md` for the next sprint.
- **Type-scale text-size migration residual** (~755 sites, 92 files) — bulk
  mechanical migration with one semantic-context branch per occurrence
  (heading → `text-title*` vs control → `text-title-sm`). Captured in
  `.omc/tech-debt-text-size-migration-plan-2026-08-01.md`; defer to a
  dedicated cleanup PR.
- **VULN-FINDINGS.md / THREAT_MODEL.md deliverables** — these are unverified
  third-party scan outputs (Step-3b confidence-pass skipped, self-reported
  confidence only; line references don't match current `claude.go:574`).
  Reference outputs that need human review before commit; the scratch state
  is now gitignored so they no longer pollute `git status`.

## Zero migrations

No SQL changes. Pre-existing migration `~234/235/238` mirrored server-side
in 0.5.13 (`1c5535aa4`) remains current.

## Ship chain (per `CLAUDE.md → Ship chain`)

```bash
bash ~/.multica/scripts/pre-update-snapshot.sh
cd server && go run ./cmd/migrate up && cd ..
pnpm --filter @multica/desktop bundle-cli
pnpm --filter @multica/desktop build
pnpm exec electron-builder --mac --dir
bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app
bash ~/.multica/scripts/verify-desktop-cold-start.sh
```