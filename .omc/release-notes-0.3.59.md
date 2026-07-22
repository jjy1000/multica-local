---
name: release-notes-0.3.59
created: 2026-07-22T11:15:00Z
updated: 2026-07-22T11:15:00Z
status: complete
---

# Multica 0.3.59 — cleanup + icon consistency (cherry-picked from upstream 0.4.0)

## Changes

### 1. `packages/views/issues/components/lab-badge.tsx` — icon consistency
- Line 149: comment `agent_self_optimization → Scale` → `agent_self_optimization → Wrench`
- The `labIcon` switch already returns `Wrench` for `agent_self_optimization`; the comment was stale.

### 2. `server/internal/experimental/experiments/` — delete 2 placeholder manifests
- Deleted `claude_science_runtime/manifest.json` (18 lines, placeholder from 0.3.19 blueprint)
- Deleted `llm_wiki_bridge/manifest.json` (14 lines, placeholder from 0.3.19 blueprint)
- Both are `server/internal/experimental/experiments/` dev-tree placeholders; the live manifests used by `catalog.go` are at `apps/desktop/resources/experiments/*/manifest.json` (the bundle path).

## What was NOT included

The audit-v2 report (`/Users/jiangjianyan/jjy/multica-main/.omc/cherry-pick-audit-v2.html`) listed 6 candidates:
- #1 lab-badge icon swap — **applied** (above)
- #2 delete 2 manifests — **applied** (above)
- #3 agent-creation-studio-view constitution comment cleanup — **skipped** (fork's existing 0.3.57 comments are intentional historical documentation, not dead code)
- #4 issue.go comment — **skipped** (fork's existing 0.3.57 comments are intentional)
- #5 builtin_skills.go docstring example — **skipped** (the example skill `multica-constitution-agent` was retired in 0.3.57; adding it as an example is misleading)
- #6 catalog.go comment whitespace — **skipped** (gofmt handles this naturally)

The remaining ~100 candidates from the upstream 0.4.0 diff are explicitly excluded:
- MUL-3963 permission model (+241/-13 in `packages/core/types/agent.ts`, +1010 in `server/internal/handler/agent.go`, etc.) — hard forbidden
- constitution v7 rollback — conflicts with fork 0.3.57 retirement
- 52 migrations renumbering (165..216 → 166..217) — violates forward-only policy
- 5 new runtime protocol families (deveco/qoder/traecli/qwen/grok) — fork does not use these
- tab-bar.tsx / desktop-layout.tsx / tab-content.tsx architecture reverse-regression — fork's 293 LoC motion/react + chrome flare + single-router session model is intentional
- apps/desktop/src/main/index.ts (-1016) / bundle-cli.mjs (-483) / updater.ts / packages/core/api/client.ts — explicitly forbidden

## Verified

- `pnpm --filter @multica/desktop build` → successful (1.59s)
- `pnpm exec electron-builder --mac --dir` → `Multica.app` 786M with `CFBundleShortVersionString=0.3.59`
- Cold start: `lsof -nP -iTCP:5432` + `lsof -nP -iTCP:8090` listeners within 10s; `curl /health` → `{"status":"ok"}`
- Row parity: workspace=1, agent=92 (identical to 0.3.58 baseline — UI/cleanup-only change, no DB impact)
