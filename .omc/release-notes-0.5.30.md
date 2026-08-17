---
name: release-notes-0.5.30
created: 2026-08-17T08:45:00Z
updated: 2026-08-17T08:45:00Z
---

# 0.5.30 Release Notes — Semantica × Multica Round 7 Closing + MUL-6254 + Ship-Hardening (2026-08-17)

Final round of the Semantica × Multica synthesizer (Round 7, all 4 verdicts shipped across 0.5.28–0.5.30), plus the upstream MUL-6254 auth recovery UI, the 0.5.27/0.5.28 PORT-leak fixes carried over, and a ship-chain integrity check that catches stale renderer builds before they ship.

## Highlights

### Semantica Round 7 (4 verdicts shipped across 3 releases)
- **0.5.28 P0-1** — cold-start timeout: `ready_timeout_ms` 120000 → 180000 + `READY_TIMEOUT_MS` env override (precedence over manifest, mirrors PATH_PY) + clamp [10s, 600s] + `isBlockedEnvKey` guard (F-005 belt-and-braces). Sub-fix `daemon.go` so a user-custom_env override cannot arm an adversarial timeout.
- **0.5.29 P0-2** — per-workspace subprocess key + auth-chain rewire: `invoke()` accepts `payload?: { workspaceId?: string | null }`; pythia's legacy channels ignore it; semantica / code_canvas / llm_wiki_bridge consume it.
- **0.5.29 P1-1** — X-API-Key INSIDE auth chain (out of scope for fork — no external API keys; verify only).
- **0.5.30 P1-3** — SemanticaGC (filesystem-only, mirrors `RuntimeGC` lifecycle): 24h tick + 90d cutoff. Sweeps per-workspace `semantica-graph*.provenance` (post-P0-2), legacy `~/.multica/semantica-graph*.provenance` (pre-P0-2 backup left by the P0-2 cp migration), and pre-P1-1 orphan `*.api-key` files (X-API-Key transport moved to in-memory IPC in P1-1).
- **0.5.30 P1-2** — `DecisionRecord` schema: `packages/core/api/schemas.ts::SemanticaDecisionRecordSchema` (zod) + `server/internal/service/builtin_skills/multica-semantica-decision-advisor/references/api-source-map.md` "DecisionRecord shape" section. Closes the `decision_sync.go:25-27` dead-reference comment that cited a non-existent `vendor/semantica/semantica/decisions.py`.

### MUL-6254 — auth recovery UI (full port, upstream PR #7058)
6 atomic commits (`08c0a2407` + `a390c9df7` + `20fe99050` + `a2d347c7b` + `68f682af1` + `565a049d8`):
- `feat(auth)` core — `AuthStatus` union, retry ladder [1s, 2s, 4s, 8s, 16s, 30s] (~61s total), `retryAuthentication()` / `refreshMe()` actions. `loginWithGoogle` dropped per fork's username-only contract. Full rewrite of `packages/core/platform/auth-initializer.tsx` + `packages/core/auth/store.ts`.
- `feat(i18n)` — `desktop.recovery.{title,description,retry,retrying}` × 4 locales.
- `feat(desktop)` — `auth-recovery.tsx` page + `auth-session-bridge.tsx` platform component (orthogonal to existing `daemon-reauth.ts` daemon-PAT path; both kept).
- `feat(web)` — `useWorkspaceList({ enabled })` hook refactor (centralizes `EMPTY_WORKSPACES` sentinel + `ready` boolean across web layouts).
- `fix(desktop)` — App.tsx `useWorkspaceList` integration, daemon auto-start preservation at `:148-177`.
- 7 upstream test files deferred (need fork-idiom adaptation for username-only login, no Google OAuth, no cloud features).

### Ship hardening — `fix(ship)` `2f9edaf08` (0.5.30 patch)
Added **4a/7 renderer integrity check** between `electron-vite build` and `electron-builder --dir`:
- File exists
- First byte is `<` (rejects Vite inlined-asset corruption that prepended highlight.js Zig module to `<!doctype>` in 0.5.29)
- Size in [200, 5000] bytes
- On failure: prints first 200 bytes of broken file + dies with "wipe apps/desktop/out/ and re-run step 4"

The 0.5.29 ship crashed silently in production because `grep rawRequest` (5a/7) only checked replacement, not well-formedness. The 4a/7 check closes that gap.

### Carry-overs from earlier session (still in 0.5.30)
- `e4a69d314` (0.5.27) — daemon spawn passes `--server-url <targetApiBaseUrl>` so daemon URL resolution doesn't fall back to `ws://localhost:8080/ws` (SearXNG-on-8080 bug when shell leaks PORT=8080).
- `0ff8c64a4` (0.5.27) — `initTargetApiUrl` export seeded from desktop.json at `index.ts:706` for the main-process auto-start path.
- `7b5c3322f` (0.5.28) — `buildServerEnv` pins `env.PORT = String(port)` after `parseEnvFile` (stale `.env` no longer leaks wrong port).

## Verification
- `pnpm typecheck --force` 6/6 (0 cached, 38s)
- `cd server && go build ./...` exit 0
- Ship chain: snapshot → migrate (no new) → bundle → **4a/7 integrity check PASS (714 bytes, starts with '<')** → electron-vite → electron-builder `--dir` (no deadlock) → install + re-sign (nested `multica` exit 0) → **cold-start 6s, Server PID 48824, PASS**
- /Applications/Multica.app = 0.5.30
- Daemon: `--server-url http://localhost:8090 --profile desktop-localhost-8090`, heartbeat OK, agent-task-snapshot polls OK

## Deferred
- **MUL-5991 pair** (ACP thinking effort for jcode) — user decision: skip (no jcode).
- **0c69f1f95** (Hermes resume-auth) — user decision: defer (symptom not observed).
- **stopDaemon env symmetry (3.2 P2)** — `daemon-manager.ts:998` currently has no `env:` at all; `MULTICA_LAUNCHED_BY` missing on stop path.
- **DATABASE_URL leak on first-launch (3.3 second half)** — `pgProbeUrl()` reads `process.env["DATABASE_URL"]` and persists into `.env`. P0 `runMigrate(backend==="external")` refusal already limits destructive blast radius.
- **`vendor/code-canvas/run.sh` hardening (3.4 P3)** — assign-not-default comment to prevent future regressions.
- **7 MUL-6254 tests** — auth-initializer / useWorkspaceList / queries / auth-session-bridge / web layout / redirect-if-authenticated / onboarding-flow-mode — need fork-idiom test fixtures (username-only, no cloud).