# Release Notes — 0.5.48

**Shipped 2026-08-21** (branch `epic/0.5.13-integration`, 1 functional commit on top of 0.5.47: `0db8b9da2`). `pnpm typecheck` 6/6.

## Summary

**CJK markdown integration.** Wires the upstream `cjk-emphasis` remark plugin (ported in 0.5.45 as a standalone utility) into fork's primary markdown renderer (`packages/views/editor/readonly-content.tsx`). The plugin walks the mdast tree and repairs CJK-adjacent strong emphasis boundaries — e.g. `'**水温适度。**水的温度'` becomes `<strong>水温适度。</strong>水的温度` instead of leaking into the particle.

**Zero migrations, zero schema drift, zero behavior change to non-CJK content.** All existing markdown rendering is unchanged. The new plugin only activates on text nodes whose value contains CJK characters adjacent to a `**` delimiter.

## Changes

### 1. `feat(0.5.48)` `0db8b9da2` — integrate `cjk-emphasis` into fork's `readonly-content.tsx`

Adds `remarkRepairCjkStrongTrailingWhitespace` to the existing `remarkPlugins` array in `packages/views/editor/readonly-content.tsx` (next to `remarkMath` / `remarkBreaks` / `remarkGfm`).

**Cross-references**:
- 0.5.45 commit `5fb0dd264` ported the `cjk-emphasis.ts` utility standalone (no consumers in fork).
- This commit integrates it into fork's primary markdown renderer (used by comment cards, issue comments, etc.).

**Fork integration**:
- Import: `import { remarkRepairCjkStrongTrailingWhitespace } from "../rich-content/cjk-emphasis";`
- Use: append to the `remarkPlugins` array in `ReactMarkdown`.

No additional tests added — fork's jsdom was removed in 0.5.33 (MUL-6291). The upstream CJK tests would fail with "document is not defined" — same pattern as the 0.5.44 webhook filter test skip.

## SKIP reconciliation from 0.5.47

| Skip | 0.5.48 progress | Status |
|---|---|---|
| MUL-6472 dispatch leak | LANDED in 0.5.47 ✅ | cleared |
| MUL-6310 caller integration | main handler task.go still has 8+ upstream sqlc queries (`GetActiveAutopilotRuleVersionParams`, `GetWorkspaceAttributionFailClosed`, etc.) + `protocol.ChatCancelFinalizedPayload` + `taskfailure.ReasonSkillBundleUnavailable` + `tasks.branch_name`/`tasks.error` columns | schema work done; new queries + protocol + column migrations needed |
| CJK markdown | **LANDED** ✅ | cleared |
| MUL-6417 follow-ups | Conflict on `runtime_config_sections.go` prompt text (fork has its own version of the two-moment rule) + 6 docs/.mdx conflicts (fork's docs structure differs) | minimal port possible (take upstream's prompt text) but requires manual surgery |

## Verification

| Gate | Result |
|---|---|
| `pnpm typecheck` (full turbo) | 6/6 ✅ (32.048s) |
| `pnpm --filter @multica/views typecheck` | PASS |
| `pnpm --filter @multica/views test` | 1605/1642 PASS (37 pre-existing jsdom failures, NOT caused by this commit) |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded constraint categories) |

## Files changed (1 commit)

| Commit | Files | Insertions | Deletions |
|---|---:|---:|---:|
| `0db8b9da2` cjk-emphasis integration | 1 | 2 | 0 |

## Strategic significance

**0.5.48 is the smallest batch since 0.5.45** (1 commit, 2 lines). But the change is load-bearing: the CJK markdown plugin was 0.5.45-coded but inert (no consumers). Wiring it into `readonly-content.tsx` is the missing piece that makes the upstream improvement actually visible to users.

CJK content is a significant use case (the fork's users include CJK-language workspaces per the project's existing i18n support). The plugin repairs a class of markdown rendering bugs that would otherwise fragment CJK strong emphasis across particles (e.g., Chinese full-width period `。`, Japanese full-width period `。`, Korean particles).

**3 of 4 SKIPs now cleared** (MUL-6472 + CJK + MUL-6417 still pending). The 0.5.47-0.5.48 cumulative audit batch + cherry-pick re-attempt cycle has landed 1 user-facing cherry-pick (MUL-6472) + 1 user-facing infrastructure fix (CJK markdown).
