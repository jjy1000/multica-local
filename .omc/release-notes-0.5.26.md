---
name: release-notes-0.5.26
created: 2026-08-17T06:52:21Z
updated: 2026-08-17T06:52:21Z
---

# 0.5.26 Release Notes — Upstream Integration Batch (2026-08-17)

29 upstream commits integrated via parallel expert agents, ship gate green, shipped to /Applications/Multica.app.

## Highlights

### MUL-6107 — Runtime GC preserves task history (upstream #6894)
The `agent_runtime` stale-GC was a single bulk `DELETE` that destroyed `agent_task_queue` + `task_message`/`task_usage`/`task_token` rows via the ON DELETE CASCADE FK. Replaced with a bounded per-runtime GC:
- Lock row `FOR UPDATE`, re-check eligibility, **block deletion when any non-terminal task exists**
- **Detach terminal task history** (`runtime_id = NULL`) so the cascade can't destroy it
- 4 new `runtime_gc_*` metric collectors + `agent(runtime_id)` index (migration 248)
- Migrations **247** (`agent_task_queue.runtime_id DROP NOT NULL` + `NOT VALID` CHECK — upstream 251 minimal-scope half) + **248** (upstream 309 index)
- 473-line regression test suite (6/6 pass, incl. keeps-terminal-history + every-non-terminal-status-subtest)

### MUL-6126 — Private runtimes owner-only in API + CLI (upstream #6905)
`canUseRuntimeForAgent` dropped the workspace-admin override — private runtimes are now owner-only end-to-end (UI already was). New `canSetRuntimeVisibility` (owner-only) with PATCH-as-PUT no-op tolerance. Ownerless-runtime refusal (MUL-3292 token-minting rationale). 403 strings updated at all 3 call sites. Frontend: new `core/runtimes/access.ts` predicate + owner-only visibility toggle + 8 locale files.

### MUL-5979 — Hide empty agent detail kebab (upstream #6696)
Defensive `hasMoreActions` guard — the fork's kebab was already structurally immune (single hardcoded archive item, required `onArchive`), ported for parity + future-proofing.

### Wave 2 fully resolved
All remaining UI/UX candidates verified N/A or structurally aborted with file-level evidence (browser tab names / page titles / tab-bar cluster / sidebar triggers / prompt-compress / MUL-6164 wiring).

## Migrations
- **247** `agent_task_queue.runtime_id` nullable + `agent_task_queue_active_requires_runtime` CHECK (NOT VALID)
- **248** `idx_agent_runtime_id` on `agent(runtime_id)` (CONCURRENTLY)

## Verification
- `pnpm typecheck --force` 6/6 (0 cached, 34s)
- `go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...` — all packages ok, 0 fail
- `go build ./...` exit 0
- Ship chain: snapshot → migrate up → bundle-cli → electron-vite → electron-builder --dir → install → re-sign nested binaries (multica runs exit 0) → cold-start 4s, Server PID 13717, Info.plist 0.5.26

## Deferred (documented, not ported)
- MUL-6053/6102 (Hermes session/HERMES_HOME) — fork never touches HERMES_HOME, fixes a failure mode that doesn't exist locally
- MUL-5991 pair (ACP thinking effort) — user decision: skip (no jcode)
- 0c69f1f95 (Hermes resume-auth) — user decision: defer (symptom not observed)
