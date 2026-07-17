# Multica 0.3.9 — 2026-07-13

## Summary

P0 hotfix for the Labs tab empty-state bug that has been silently
present since 0.3.6. Two new flags (`claude_science`, `pythia_oracle`)
added in 0.3.8 made the bug visible: the catalog grew to 3 entries but
the GUI still rendered "暂无实验 / 当前实验室页面是空的". This release
fixes the wire-shape mismatch and ships nothing else.

## Root cause

The TS zod schema at `packages/core/api/schemas.ts:1131` was typed as
a bare array, but the server handler
(`server/internal/handler/experimental_flags.go:62-76`) returns the
catalog wrapped in `{"flags": [...]}`. Every parse silently failed and
`parseWithFallback` returned `[]`, so `useExperimentalFlags().data`
was always empty regardless of how many catalog entries existed.
The 0.3.6 ship never noticed because:

- The empty-state UI rendered identically to a 1-flag catalog
  (the only flag, `chat_pin_ui`, defaulted off and was visually
  indistinguishable from "no flags").
- No schema test pinned the wire shape.

## Fix

`packages/core/api/schemas.ts`:
```diff
-export const ExperimentalFlagsListSchema = z.array(ExperimentalFlagSchema).default([]);
+export const ExperimentalFlagsListSchema = z.object({
+  flags: z.array(ExperimentalFlagSchema).default([]),
+}).loose().default({ flags: [] });
```

`packages/core/api/client.ts` — unwrap `.flags`:
```diff
- return parseWithFallback(raw, ExperimentalFlagsListSchema, [], { ... });
+ const parsed = parseWithFallback(raw, ExperimentalFlagsListSchema, { flags: [] }, { ... });
+ return parsed.flags;
```

## Regression guards

`packages/core/api/schemas.test.ts` — 5 new test cases pinning the wire
shape:

1. Accepts canonical `{flags: [...]}` wrapper from server.
2. Accepts empty list inside the wrapper.
3. **REGRESSION GUARD**: rejects a bare array (the 0.3.6 bug).
4. **REGRESSION GUARD**: undefined body falls back to `{flags: []}`.
5. Accepts unknown flag entries via `.loose()` (catalog grows without
   schema churn).

All 640 core tests pass (was 635).

## Files touched

| File | Δ |
|---|---|
| `packages/core/api/schemas.ts` | +6 / -1 (ExperimentalFlagsListSchema reshape) |
| `packages/core/api/client.ts` | +2 / -1 (unwrap `.flags` in listExperimentalFlags) |
| `packages/core/api/schemas.test.ts` | +57 (5 new test cases) |
| `apps/desktop/package.json` | bump 0.3.8 → 0.3.9 |

## Verification

- `pnpm --filter @multica/core test` — **640 / 640 pass** (was 635).
- `pnpm typecheck` — 0 errors (6 typecheck tasks).
- Cold-start 3-check on 0.3.9 DMG:
  - 5432 LISTEN (postgres) within 8s.
  - 8090 LISTEN (server) within 8s.
  - GUI window appears.
- Row parity preserved: `workspace=1 / issue=162 / agent=80 /
  squad=11 / comment=855 / schema_migrations=184`. No SQL
  migrations touched.

## Known caveats

- DMG hand-built via `create-dmg` (memory 0.3.4 workaround).
- The 0.3.8 vendored-binary caveats (`apps/desktop/vendor/{openscience-
  bin,pythia-src}` still absent) remain — enabling `claude_science`
  or `pythia_oracle` shows "service not bundled" until vendor paths
  are populated. This is independent of the Labs visibility fix.

## Rollback

`git revert` the 4 file changes above + restore `package.json`
version 0.3.8. No data concerns — schema migration count unchanged.

## By the numbers

- 4 files touched, 1 bumped version.
- ~65 LOC added, 0 LOC removed (modulo comments).
- 0 new SQL migrations.
- 0 breaking changes to wire format (consumer side only).