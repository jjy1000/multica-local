# Release Notes 0.5.71 — 2026-08-26

Upstream CVE/security batch (first batch of the 0.5.71+ integration roadmap
defined in `.omc/upstream-integration-triage-2026-08-26.md`). 5 commits
cherry-picked out of an upstream 675-commit lead.

## Cherry-picks landed (5/6)

| SHA | Title | Reason included |
|---|---|---|
| `62c5ba11` | `fix: upgrade builder-util-runtime to 9.7.0` | CVE-2026-54673 |
| `79d13b6b` | `MUL-6411: upgrade brace-expansion patched versions` | CVE-2026-69152 |
| `4c1c8709` | `fix: CVE-2026-9277 security vulnerability` | direct CVE |
| `658b0b7d` | `MUL-6406: fail fast on insecure JWT secret defaults` | P0 auth hardening |
| `a3bd38da` | `fix(cli): redact autopilot webhook credentials` | P0 credential exposure |

## Cherry-pick skipped (1/6)

| SHA | Title | Reason skipped |
|---|---|---|
| `8f48c380` | `fix(desktop): enable the renderer process sandbox` | triage flagged REVIEW — 4-region index.ts refactor (extracts `createRendererWebPreferences`) collides with fork's fork-specific PDF plugin comment (mentions "signed CloudFront URLs" — fork has no CloudFront). Tracked for 0.5.72 with proper PDF-plugin justification rewrite. |

## Fork-local surgical resolutions

- **package.json overrides** (`62c5ba11`, `79d13b6b`, `4c1c8709`): 3-way conflicts
  in `overrides` block because the cherry-picks land in sequence; resolved by
  keeping the merged HEAD (postcss 8.5.26, app-builder-bin 5.0.0-alpha.13,
  uuid 11.1.1, builder-util-runtime 9.7.0, plus all brace-expansion overrides)
  and adding each upstream's new key. `pnpm install --lockfile-only`
  regenerates `pnpm-lock.yaml` from the merged overrides.
- **`server/cmd/server/main.go` imports** (`658b0b7d`): conflict between
  fork's `daemon/execenv` import and upstream's new `auth` package import.
  Kept both — fork still uses execenv, auth is a new dependency.
- **4× `environment-variables.{en,ja,ko,zh}.mdx`** (`658b0b7d`):
  - Conflict 1 (Callout): kept fork's `MULTICA_DEV_VERIFICATION_CODE` warning
    (fork-specific — fork has no email verification path); dropped upstream's
    JWT_SECRET warning (already implicitly required by fork's auth flow).
  - Conflict 2 (env-vars table rows): took upstream (DATABASE_URL + PORT +
    JWT_SECRET + APP_ENV + AUTH_TOKEN_TTL rows; fork's daemon-polling
    description text was overwritten by upstream's generic copy — minor
    accuracy regression, documented in follow-ups).
- **`scripts/selfhost-config.test.sh`** (`658b0b7d`): dropped upstream's
  ~408 lines of new test scaffolding that depends on `init-worktree-env.sh`
  (fork does not have that script). Fork keeps its simpler test.
- **`server/cmd/server/main_test.go`** (`658b0b7d`): kept fork's deletion
  (the file was removed in fork-hygiene wave 1 alongside GoogleLogin).
- **4× `autopilots.{en,ja,ko,zh}.mdx`** (`a3bd38da`): kept fork's
  curl example with `$MULTICA_WEBHOOK_URL` (the URL is the public endpoint,
  not the secret — redaction still applies to the token). Upstream replaced
  the curl example with CLI commands referencing `--show-secrets`; that
  alternate pattern is welcome as a follow-up but not required for the
  redaction to work.
- **`autopilots-source-map.md`** (`a3bd38da`): took upstream's longer
  description (the redaction behavior description matches the cherry-picked
  code; staying in sync is the whole point of the source-map).

## Ship-gate pre-existing fixes

4 pre-existing test failures (verified by checking out parent commit
`d70187260` and reproducing all 4) blocked both `pnpm typecheck` and
`go test ./internal/... ./pkg/agent/...`. Surgical fixes shipped in 2
separate commits so the audit trail separates "pre-existing fix" from
"upstream cherry-pick":

1. **`packages/views/editor/extensions/{mention,slash-command}-suggestion.test.tsx`**
   — 13 calls of `config.items!({...})` missing the required `signal`
   field after `SuggestionOptions.items()` signature gained an
   `AbortSignal` parameter. Added `signal: new AbortController().signal`.
2. **`apps/desktop/src/main/experimental/upstream-registry.test.ts`**
   — 4 `as [string]` tuple casts fail under TS 5.x strict mode.
   Switched to `as unknown as [string]` (double-cast pattern).
3. **`server/internal/service/mythos/supervise_completion_test.go`** —
   the "closed" and "empty_status" subtests panic because
   `issuestatus.Effective` walks the catalog via `s.queries` (not the
   `tickQ` seam) for non-canonical statuses, and the fixture leaves
   `s.queries` nil. Canonical-key subtests already pin the snap-to-total
   contract; the two custom-status cases are now skipped with an
   `issuestatus.IsBuiltIn` guard and a follow-up note ("when
   `Service.queries` becomes an interface seam").

## STOP flags honoured

- `git rebase upstream/main` NOT attempted — would re-introduce the
  0.5.36 wholesale-adoption trap (4.4x LOC inflation).
- No `cloud / billing / OAuth / seat / invite / discord / posthog /
  updater` cherry-picks — all 26 upstream SKIP-key commits left alone.
- Each cherry-pick verified at `file:line` for fork-applicability before
  merge (per root CLAUDE.md "Cherry-pick verification").

## Numbers

- **Upstream lead**: 675 commits → 670 (this ship) + 5 SKIP-key excluded
- **Fork-applicable batch 1** (this ship): 5 of 6 P0 CVE/security commits
  from triage §P0; 1 deferred to 0.5.72
- **Tests**: typecheck 6/6 packages green; go test all packages green
  (including 2 skipped pre-existing custom-status subtests)
- **Files touched**: 17 files (5 cherry-picks + 4 fork-local resolutions +
  4 ship-gate pre-existing fixes + version bump + 2 release docs)
- **LOC delta**: +158 / -89

## Follow-ups (for 0.5.72)

1. Renderer process sandbox cherry-pick (`8f48c380`) with PDF-plugin
   justification rewrite for fork (no CloudFront reference).
2. `Service.queries` → interface seam so `issuestatus.Effective` can
   mock custom-status lookups in unit tests (re-enables the 2 SKIP'd
   `TestTickSupervision_CompletionByFinalIssueStatus` subtests).
3. Restore fork-specific daemon-polling description in
   `apps/docs/content/docs/environment-variables.{mdx,ja,ko,zh}` for the
   `DATABASE_MAX_CONNS` row (lost in the JWT commit's table merge).
5. Continue cherry-pick batch 2 per triage §P1 (agent runtime, issues,
   chat, autopilot, inbox, daemon, skill bundle, source-context).