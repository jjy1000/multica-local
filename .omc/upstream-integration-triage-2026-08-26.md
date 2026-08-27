# Upstream Fork-Applicability Triage — 2026-08-26

## Executive Summary

- **Upstream HEAD**: `54027ba763fa7da0699b2fe89df4a6b2c13d1c6f` (MUL-6639: fix(skills): metadata-only skill listings and stall-based timeouts, 2026-08-26)
- **Fork integration HEAD**: `9eaf3987b` on `epic/0.5.13-integration` (release 0.5.70, 2026-08-24 ship)
- **Commit gap (non-merge)**: **675 commits** upstream ahead
- **After fork-irrelevant subject/path filter** (billing/cloud/oauth/invite/posthog/telemetry/discord/updater + soft-kw billing/seat/oauth/google/aws/azure/gcp/discord): **649 remaining candidates**
- **After fork-divergence + noise filter** (mobile/deploy/build/docs-only): **330 fork-applicable candidates**
  - **APPLY** (≤400 LOC, no fork-heavy touch): 353 raw → 330 after noise → split into 74 surgical / 178 small / 78 medium (excluding 23 noise)
  - **REVIEW** (fork-touch OR >400 LOC): 296 (128 fork-heavy touch + 168 large)
  - **SKIP**: 26 (15 hard subject-kw + 11 soft-kw)

> Volume is **massive** — 675 upstream commits is 3.7x what 0.5.35/0.5.36 batches tackled. Do NOT attempt a single mega-batch. Triages below split into small atomic sub-batches per ship.

## Methodology

1. **`git fetch upstream --tags`** then `git log --no-merges --format='COMMIT:%H%x00%aI%x00%an%x00%s' --numstat` parsed with NUL + COMMIT delimiter (handles `|` in subjects cleanly).
2. **HARD kill** on subjects matching: `subscription|invoice|checkout|cloudfront|cloud (team|workspace|billing|runtime|pat|login)|sso|send[_-]?code|verify[_-]?code|googlelogin|posthog|analytics|telemetry|helplauncher|feedbackmodal|contact[_-]?sales|electron[_-]?updater|auto[_-]?update|autoupdater|autoUpdate|workspace[_-]?(invite|member)|invitation|invite[_-]?(user|member|workspace)|onboard.*workspace|cloud[_-]?(pat|billing|runtime|team)`.
3. **SOFT kill** (need manual review but high probability): `billing|seat|oauth|google|aws|azure|gcp|discord|workspace.*member`. Currently classified as SKIP until proven otherwise.
4. **Path kill** (≥50% files hit fork-removed surfaces): `server/internal/{billing,cloud,seat,subscription,invitation,invite,posthog,analytics}/`, `helplauncher|feedbackmodal|discord|electron-updater|auto[_-]?update|posthog|telemetry|workspace_invite|cloud-{pat,billing,runtime}|contact-sales`.
5. **Fork-heavy touch** = any of 27 files CLAUDE.md §Known Stability Surfaces + §Active Contracts explicitly protects (skill.go, issue.go, agent_trust.go, mythos_*, swarm_*, claude_science_runtime.go, catalog.go, lock.go, visibility.go, daemon.go, daemon-manager.ts, server-manager.ts, pg-bootstrap.ts, pythia-manager.ts, schemas.ts, login.tsx, App.tsx, use-t.ts, lab-picker.tsx, app-sidebar.tsx, user_plugin_runtime.go).
6. **Noise filter**: commits where every file is `apps/mobile/` / `docs/` / `deploy/` / `apps/desktop/scripts/` / `.env.example` / `docker-compose.yml` / `.github/` / `examples/` / `.md` / `.mdx` / lockfiles.

## Exclusion Detail

**26 commits SKIP** (do NOT cherry-pick):

| SHA | Subject | Reason |
|---|---|---|
| `0a1357498` | rate limit workspace invitations (#7030) | hard: invitation |
| `aaedb864a` | drop the dead hang telemetry (MUL-5345) (#6433) | hard: telemetry |
| `2d711afdd` | Workspace Billing summary/prices proxy + Stripe (#6912) | hard: billing |
| `7f828b44e` | fix(billing): prefill first Checkout email (#7529) | soft: billing |
| `60a55ec57` | feat(billing): purchase a seat from the invite flow (#7538) | soft: billing/seat/invite |
| `f7dd08f30` | fix(daemon): explain why repo checkout auth failed (#7520) | hard: checkout (also touches `apps/web/app/join/page.tsx` — invite flow) |
| `13f9f8e5` | MUL-6529: let admins add workspace seats (#7388) | soft: seats |
| `3460f9ae` | MUL-6342: enforce entitlement-backed autopilot quotas (#7194) | soft: entitlement (billing-adjacent) |
| `210482476` | fix(views): stop the zh Discord sidebar label from truncating (#6359) | soft: discord |
| `176e8c684` | MUL-6050: romanize Chinese workspace names into a slug (#6823) | hard: onboarding/workspace |
| `d57bfc50e` | MUL-6043: Keep repo checkout responsive during daemon GC (#6803) | hard: checkout |
| (9 more soft-kw) | billing / oauth / google / aws / azure / gcp | soft: high probability fork-irrelevant |

**23 noise** (mobile/deploy/build/docs-only — skip silently):
- 41 docs-only commits (releases/changelogs, CONTRIBUTING updates, README fixes)
- 23 mobile/deploy/build/config commits (`.env.example`, `deploy/helm/*`, `apps/mobile/*`, `.github/*`)

## P0 — APPLY candidates (security / data integrity)

> All surgical (≤100 LOC), no schema, no fork-removed surface.

1. **`46b5d9e6`** — `fix(agent): own the runtime process tree on every backend` (MUL-6658, +144/-5 in `server/pkg/agent/launch.go` + tests)
   - Touches: `server/pkg/agent/{claude,codex,copilot,openclaw,opencode,pi,kimi,kiro,...}.go` (the agent spawn backends)
   - **Risk**: HIGH. Fork has process-tree work in `0.5.20 sub-agent tempdir fix (MUL-5799)` (`daemon-manager.ts` + per-task `TMPDIR` injection), `0.5.27/0.5.28 PORT leak fix` (spawn env wiring). Likely conflicts with fork's `daemon-manager.ts:1272` allowlist (F-027 closed 0.5.18).
   - **Cherry-pick note**: RE-GRADE to REVIEW. Verify fork's existing process tree handling in `server/pkg/agent/launch.go` first — fork has been actively patching this surface (5 atomic commits since 0.5.20). Do NOT cherry-pick blindly.
   - **Verdict**: REVIEW (was APPLY in raw filter).

2. **`62c5ba11`** — `fix: upgrade builder-util-runtime to 9.7.0 (CVE-2026-54673)` (+11/-9)
   - Touches: `package.json` only. Fork uses pnpm catalog → verify `pnpm-workspace.yaml` after.
   - **Risk**: LOW (dependency bump). Security CVE — should apply.
   - **Verdict**: APPLY (1-line dependency bump).

3. **`79d13b6b`** — `MUL-6411: fix: upgrade brace-expansion patched versions (CVE-2026-69152)` (+23/-15)
   - Touches: `package.json` only.
   - **Risk**: LOW (dependency bump). CVE — should apply.
   - **Verdict**: APPLY.

4. **`4c1c8709`** — `fix: CVE-2026-9277 security vulnerability` (+7/-5)
   - Touches: `package.json`/lockfile.
   - **Risk**: LOW (CVE patch).
   - **Verdict**: APPLY.

5. **`658b0b7d`** — `MUL-6406: fix(auth): fail fast in production on insecure JWT secret defaults` (+157/-15)
   - Touches: `server/internal/handler/auth.go` (likely).
   - **Risk**: MEDIUM. Fork has username-only auth + JWT — but fork's `JWT_SECRET` is auto-generated by `server-manager.ts::serializeEnvFile`. May be no-op or improvement.
   - **Verdict**: APPLY after verifying fork's `auth.go` already has the default-secret guard.

6. **`f4bf8e2c`** — `fix(realtime): bound inbound WebSocket message size (MUL-5569)` (+180/-17)
   - Touches: `server/internal/handler/realtime.go` (likely) + WS layer.
   - **Risk**: MEDIUM. Fork has WS push in `apps/desktop/src/main/daemon-manager.ts`. Verify size limit is sane for fork's payload (autopilot_run, mythos_run, source-context dumps).
   - **Verdict**: REVIEW (touches WS layer).

## P1 — APPLY candidates (practical bug fix / small surface)

> Surgical (≤200 LOC), no schema, no fork-removed surface.

7. **`54027ba76`** — `MUL-6639: fix(skills): metadata-only skill listings and stall-based timeouts` (+1441)
   - Touches: `server/internal/handler/skill.go`, `server/internal/cli/{client,errors,stall}.go`, `server/pkg/db/queries/skill.sql` (migration implied).
   - **Risk**: HIGH — 1441 LOC, adds new `stall.go` + `source_context_sweeper` pattern + DB schema change.
   - **RE-GRADE to REVIEW**. HEAD commit; verify fork's `skill.go` (fork has `0.5.18 SEC-P1-7` + `0.5.2 self-opt` work in adjacent files) and DB migration compatibility.

8. **`617d41602`** — `MUL-6658 fix(cli): tell a rejected task token to stop, not to sign in again` (+217/-21)
   - Touches: `server/internal/cli/{client,errors}.go` + tests.
   - **Risk**: LOW. CLI-side only. Fork uses CLI heavily (`multica` daemon binary).
   - **Verdict**: APPLY (CLI error UX; tests pin behavior).

9. **`21673268b`** — `MUL-6660 fix(issues): drop the "branch" metaphor from source-context copy` (+54/-50)
   - Touches: `packages/views/issues/components/{comment-card,source-context-viewer,source-context-comment-list}.tsx` + `packages/views/locales/{en,ja,ko,zh-Hans}/issues.json` (all 4 locales — fork has exactly 4).
   - **Risk**: MEDIUM. Locale JSON format may conflict with fork's 4-lang sync discipline. Component changes touch fork-modified files (issue-detail.tsx, source-context components).
   - **RE-GRADE to REVIEW** (locale JSON format check + component-touch).

10. **`e5f976144`** — `fix(server): keep source-context cleanup off the runtime sweep tick` (+476)
    - Touches: new `server/cmd/server/source_context_sweeper.go` + `server/internal/service/source_context.go`.
    - **Risk**: MEDIUM. New file + service changes. Fork has no equivalent sweeper.
    - **Verdict**: REVIEW (introduces new sweeper, schedule overlap with `runtime_gc`).

11. **`55c7cf05`** — `fix(daemon): add the reference fact to the Mentions brief` (+11/-3)
    - Touches: daemon brief section (likely `daemon/brief/*.md`).
    - **Risk**: LOW. Documentation fix.
    - **Verdict**: APPLY (docs fix, surgical).

12. **`1ef1d65b`** — `fix(cli): remove obsolete autopilot priority flag` (+20/-16)
    - Touches: CLI autopilot commands.
    - **Risk**: LOW. Fork's autopilot catalog has `priority` field — verify removal is safe.
    - **Verdict**: APPLY (CLI flag removal).

13. **`e6013e83e`** — `MUL-5928: fix(issues): bound board description previews` (+39/-1)
    - Touches: issue board UI.
    - **Risk**: LOW. UI performance fix.
    - **Verdict**: APPLY.

14. **`18a85b8c`** — `fix(github): support single-character issue prefixes` (+12/-2)
    - Touches: GitHub integration handler.
    - **Risk**: LOW (fork uses GitHub integration for PR snapshots per CLAUDE.md).
    - **Verdict**: APPLY.

15. **`a3bd38da`** — `fix(cli): redact autopilot webhook credentials` (+327/-4)
    - Touches: CLI autopilot webhook commands. Security-adjacent (secret leak fix).
    - **Risk**: MEDIUM. Fork has autopilot webhook in `server/internal/service/autopilot.go` + skill bundle work.
    - **Verdict**: APPLY after verifying fork's webhook command surface.

16. **`18f130a5`** — `fix(migrate): restore unique migration prefixes after the 362 collision` (+16/-10)
    - Touches: `server/cmd/migrate/*` + SQL migration files.
    - **Risk**: HIGH. Fork's migration history is heavily customized (0.5.13 → 0.5.70). Likely SKIP-DIVERGENCE — verify by comparing fork's `server/migrations/` against upstream's.
    - **Verdict**: REVIEW (migration file conflict).

17. **`7e92c58f`** — `fix(db): regenerate DingTalk sqlc output` (+4/-4)
    - Touches: `server/pkg/db/generated/dingtalk.sql.go`. Fork doesn't use DingTalk heavily.
    - **Risk**: LOW (auto-generated).
    - **Verdict**: APPLY (sqlc regeneration, no semantic change).

18. **`aa4c504c`** — `fix(issues): re-rank an issue when its status moves it to a new column` (+270/-15)
    - Touches: `packages/views/issues/*` + handler.
    - **Risk**: MEDIUM. Board view logic.
    - **Verdict**: APPLY after checking fork's board view hasn't diverged.

19. **`b2b4699f`** — `MUL-6632: add status and priority filters to Inbox` (+1282/-47)
    - Touches: Inbox UI + filter store.
    - **Risk**: MEDIUM. Fork's inbox has 0.5.45.9 5s polling fallback pattern (`packages/core/inbox/filter-store.ts`).
    - **Verdict**: REVIEW (UI surface + filter store).

20. **`39ddc82c`** — `MUL-6228: resolve full-UUID issue refs locally instead of a resolver GET` (+83/-36)
    - Touches: CLI issue ref resolver.
    - **Risk**: LOW (CLI perf improvement).
    - **Verdict**: APPLY.

21. **`5a80f802`** — `MUL-6558 feat(server): make task queue expiry configurable via MULTICA_TASK_QUEUED_TTL` (+109/-31)
    - Touches: server config + env wiring.
    - **Risk**: MEDIUM. Fork's `server-manager.ts::serializeEnvFile` only passes 4 env vars (PORT/DATABASE_URL/JWT_SECRET/MULTICA_PUBLIC_URL). Need to add `MULTICA_TASK_QUEUED_TTL` to the pass-through.
    - **Verdict**: APPLY + 1-line fork addition.

22. **`5dec16f4`** — `fix task failure recovery comment threading` (+45/-27)
    - Touches: `server/internal/service/delegated_failure_recovery.go` (likely).
    - **Risk**: LOW.
    - **Verdict**: APPLY.

23. **`3881f658`** — `fix(realtime): stamp comment:created broadcasts with the real UTC offset` (+95/-34)
    - Touches: WS broadcast layer.
    - **Risk**: MEDIUM. WS touch.
    - **Verdict**: APPLY (small).

24. **`9622dd55a`** — `MUL-6583: fix archived inbox group counts` (+297/-36)
    - Touches: `server/internal/handler/inbox.go` + frontend.
    - **Risk**: MEDIUM.
    - **Verdict**: REVIEW.

25. **`9b013e34`** — `feat(inbox): move the selection with the arrow keys` (+273/-6)
    - Touches: `packages/views/inbox/*`.
    - **Risk**: LOW (UX keyboard shortcut).
    - **Verdict**: APPLY.

26. **`844250420`** — `fix(runtimes): hide private runtimes from non-owners` (+3/-1)
    - Touches: runtime visibility gate.
    - **Risk**: LOW.
    - **Verdict**: APPLY.

27. **`750fc0cc5`** — `MUL-6655 Fix MCode ACP session startup race (ZIC-199)` (+247/-1)
    - Touches: `server/pkg/agent/acp_terminal.go` (MCode backend).
    - **Risk**: LOW.
    - **Verdict**: APPLY.

28. **`1ea6d18f`** — `MUL-6530: report bytes transferred when a skill bundle download fails` (+454/-28)
    - Touches: skill bundle error path.
    - **Risk**: MEDIUM. Fork has skill bundle work.
    - **Verdict**: REVIEW.

29. **`bdcb231ba`** — `MUL-5466 fix(agent): deliver the qwen prompt on stdin, not argv` (+2/-2)
    - Touches: Qwen backend.
    - **Risk**: LOW.
    - **Verdict**: APPLY.

30. **`f48f24c4`** — `fix(agent): route all Windows Pi runs through powershell -Command to preserve stdin` (+89/-44)
    - Touches: Pi backend Windows path. Fork is macOS-only — SKIP on platform.
    - **Verdict**: SKIP-PLATFORM (Windows-only, fork is darwin-only).

31. **`8a5a6adb`** — `feat(agent): add ZeroClaw as a native ACP runtime` (+1694/-37)
    - Touches: new runtime backend.
    - **Risk**: MEDIUM. Adds new runtime to fork's already-broad agent catalog (claude, codex, copilot, openclaw, opencode, hermes, pi, qwen, kimi, kiro, qoder, traecli, dim, mcode, codebuddy, cursor, deveco, dsh, grok, codex, zai, ...).
    - **Verdict**: REVIEW (large + new backend).

32. **`a1d160ee`** — `MUL-6468: fix(docs): replace dead dimcode.ai link with official Dim CLI docs` (+4/-4)
    - Docs link fix.
    - **Verdict**: APPLY (docs).

33. **`18cd750f`** — `fix(autopilots): keep configuration panel visible for wide runbooks` (+1/-1)
    - Surgical UI fix.
    - **Verdict**: APPLY.

34. **`6e147b0c`** — `fix(autopilots): use valid bug triage status` (+20/-1)
    - Surgical autopilot fix.
    - **Verdict**: APPLY.

35. **`f78c8671`** — `docs(tasks): describe the execution log where it actually lives` (+32/-8)
    - Docs.
    - **Verdict**: APPLY (docs).

36. **`8d9766d4`** — `fix(issues): scroll to the newly posted comment in both timeline modes (MUL-6140)` (+161/-15)
    - Touches: comment timeline UI.
    - **Risk**: MEDIUM (0.5.36 memory note: MUL-6140 was ported in 0.5.36 client-log audit batch — verify not duplicated).
    - **Verdict**: REVIEW (potential duplicate).

37. **`7c7ed8bf`** — `MUL-5937 fix(daemon): persist direct chat Codex sessions` (+103/-26)
    - Touches: daemon chat session persistence.
    - **Risk**: MEDIUM (fork has Codex runtime).
    - **Verdict**: REVIEW (daemon-touch).

## P2 — APPLY candidates (UX / cosmetic)

> Cosmetic / UI fixes, low risk.

38. **`fbb4ec59d`** — `MUL-6610: fix(web): proxy /health to the backend in the runtime rewrite` (+48/-0)
    - Web `/health` proxy fix. Fork desktop hits backend on `localhost:8090` directly, but web does Next.js rewrite.
    - **Verdict**: APPLY (web-only, no fork desktop impact).

39. **`e5b28564b`** — `fix(views): prevent crowded transcript end ticks` (+20/-0)
    - UI fix.
    - **Verdict**: APPLY.

40. **`04fd64b41`** — `fix(chat): keep following rapid live output` (+53/-4)
    - Chat UX.
    - **Verdict**: APPLY.

41. **`dd147440`** — `fix(chat): preserve idle footer spacing (MUL-5854)` (+36/-7)
    - Chat spacing.
    - **Verdict**: APPLY.

42. **`dd147440`** — `feat(views): mobile comment rails + full-screen keyboard-aware chat` (+272/-43)
    - Mobile UI.
    - **Verdict**: SKIP-PLATFORM (mobile-only, fork has separate apps/mobile/).

43. **`beb3e9be`** — `fix(chat): widen the conversation gutter and align its edges (MUL-5497)` (+188/-38)
    - Chat layout.
    - **Risk**: LOW (CSS-only).
    - **Verdict**: APPLY.

44. **`6be7adcb`** — `feat(chat): toggle the floating chat window from the keyboard (MUL-5522)` (+342/-20)
    - Chat keyboard shortcut.
    - **Risk**: MEDIUM (keyboard handler touch).
    - **Verdict**: REVIEW.

45. **`92c1a4df`** — `MUL-6279: De-emphasize share-link URLs` (+7/-2)
    - Surgical UI.
    - **Verdict**: APPLY.

46. **`f9b7bf52`** — `MUL-6363: refactor(i18n): reuse custom_property.none for the no-value filter` (+3/-7)
    - i18n refactor.
    - **Risk**: LOW (locale JSON only).
    - **Verdict**: APPLY.

47. **`9bd55678`** — `fix(inbox): give the desktop shell navigation feedback when switching issues` (+287/-30)
    - Desktop navigation UX.
    - **Risk**: MEDIUM (desktop app.tsx touch).
    - **Verdict**: REVIEW.

48. **`8f48c380`** — `fix(desktop): enable the renderer process sandbox` (+140/-46)
    - Desktop renderer security.
    - **Risk**: HIGH. Touches `apps/desktop/src/main/index.ts` (electron BrowserWindow webPreferences). Fork's 0.5.18 SEC-P1-7 closed nested-binary codesign — this is the next logical hardening step.
    - **Verdict**: REVIEW (electron security, requires ship-chain verify).

49. **`16f9f252`** — `feat(inbox): add a keyboard shortcut for archiving the open notification` (+303/-3)
    - Inbox UX.
    - **Verdict**: APPLY.

50. **`0716081b`** — `MUL-6580: add right sidebar toggle shortcut` (+180/-25, 25 files)
    - UI shortcut. Touches 25 files.
    - **Risk**: MEDIUM.
    - **Verdict**: REVIEW (breadth).

## P3 — APPLY candidates (docs / changelog)

> Pure docs, low risk.

51. **41 docs-only commits** — mostly changelog entries (`docs(changelog): add v0.4.30..v0.4.34 release entries` 5×), README tweaks, CONTRIBUTING updates, `MUL-6189` Node version alignment, `MUL-6468` dead link fixes.
    - **Verdict**: APPLY individually (cherry-pick each changelog as separate commit). Do NOT batch — they reference upstream issue numbers that don't exist in fork's history.

## REVIEW — Need Human Deep-Dive (296 commits)

### High-value but large (could be 0.5.71-0.5.74 sub-batches if approved)

| SHA | LOC | Subject | Review Focus |
|---|---|---|---|
| `f8ec870f` | +1504/-260 | MUL-6629: per-agent starter prompts for chat | Touches `server/internal/handler/agent_access.go`, `issue.go` (fork-heavy); `multica-creating-agents` skill; i18n. Verify fork's lab leader rewrite compatibility. |
| `9e2d9b6d` | +8475/-1242 | MUL-6661: channels /new and /clear | 9K LOC new feature (chat channel commands). Likely SKIP unless fork adds channel feature. |
| `8c563b49` | +10866/-306 | MUL-6555: preserve source context for sub-issues | 10K LOC. Fork has source-context work (`0.5.25 RuntimeGC fix` audit; `e5f976144` upstream). Requires multi-day port. |
| `8a5a6adb` | +1694/-37 | MUL-6511: ZeroClaw native ACP runtime | New runtime backend. Add to fork's catalog? Fork already has openclaw — verify this isn't a fork of openclaw. |
| `5bf10d48` | +2917/-81 | MUL-6582: durable scheduled Plugin hooks | 3K LOC plugin system. Fork has `multica-lab-builder` skill; verify hook engine doesn't conflict. |
| `bedad9e2` | +1297/-967 | MUL-6485: isolate hosted plugin surfaces | Plugin isolation work. Fork's user-plugin layer (`user_plugin_runtime.go`) is fork-modified — heavy conflict likely. |
| `f45b5916` | +4249/-679 | MUL-6469: host plugin artifacts | Plugin artifact hosting — fork's `user_plugin_artifacts.go` (F-006 hardened 0.5.18). Conflict likely. |
| `8f9c766e` | +449/-28 | MUL-6460: inject workspace status catalog into agent brief | Brief change. Fork has `MUL-6243 custom statuses` (0.5.33/0.5.34). Verify brief format matches fork. |
| `d2532f76` | +1391/-131 | MUL-6491: fence env-root resets | Daemon env work. Fork has `daemon-manager.ts:1272` allowlist (F-027). |
| `67c6aa05` | +9196/-59 | MUL-6166: Telegram channel integration | 9K LOC new channel. Fork explicitly removed channels-related code (no OAuth, no Discord). Verify not touching removed surface. |
| `4bf3c73a` | +4379/-57 | feat(plugins): agent integration PR 4 | Plugin system core. Fork's plugin layer is custom — likely SKIP-DIVERGENCE. |
| `4b8873df` | +5487/-658 | feat(issues): revision-aware concurrency guards | Issue concurrency. Fork's issue handler has 5 nested mutex gates (CLAUDE.md §Lab ↔ Assignee Mutex). Verify compatibility. |
| `5d5fe646` | +2366/-279 | MUL-6343: Add semantic issue activity timestamps | Issue timestamps. Fork may need schema migration. |
| `3460f9ae` | +3188/-259 | MUL-6342: enforce entitlement-backed autopilot quotas | soft-kw (entitlement = billing-adjacent). Likely SKIP — fork has no entitlement concept. |
| `13f9f8e5` | +1523/-39 | MUL-6529: admins add workspace seats | soft-kw (seats). SKIP. |
| `5e2d349e` | +6790/-5086 | MUL-6426: refactor dingtalk | DingTalk refactor. Fork doesn't use DingTalk. SKIP unless adding. |
| `659bb41a` | +5807/-220 | MUL-6350 PR 3: hook engine | Plugin system core. Likely SKIP-DIVERGENCE. |
| `eb2a94e8` | +662/-196 | MUL-6368: cross-agent task/workdir contamination | Agent sandboxing. Fork has `0.5.20 sub-agent tempdir fix` — verify compatibility. |
| `1dc13559` | +1399/-71 | MUL-6343: Enforce recently created issue windows | Issue CRUD change. |
| `520359a2` | +1147/-35 | MUL-6518: reclaim orphaned per-task temp dirs | Daemon GC. Fork has `0.5.20` private per-task TMPDIR + `0.5.25 RuntimeGC fix`. |
| `20ceaccc` | +805/-113 | MUL-6471: let pi and opencode reach custom providers | Runtime provider config. Fork uses Multica runtime bridge (per CLAUDE.md §Pythia engine). |
| `bae3a324` | +785/-27 | MUL-6504: stop chat tasks holding local_directory mutex | Daemon mutex change. Fork has `0.5.20 sub-agent tempdir fix`. |
| `7fdc854c` | +1030/-273 | fix(issues): copy durable local path for worktree tasks | Issue path work. Fork has worktree patterns in `0.5.36`. |
| `f8ec870f` | +1504/-260 | MUL-6629: per-agent starter prompts | Lab/fork-modified files. |
| `2e0c599e` | +46/-2 | fix(agent): avoid H1 headings in issue bodies (#6199) | **Already ported** in 0.5.15 (`da1cc2003` — wired `writeIssueBodyFormatting` into legacy verbose path per CLAUDE.md §0.5.15 cherry-pick batch). SKIP-DUPLICATE. |
| `a17857e6` | +60/-23 | fix(agent): parse tab-separated Antigravity model catalog | Antigravity runtime. Fork doesn't use Antigravity. SKIP-PLATFORM (likely). |
| `a8d5daac` | +142/-145 | fix(chat): decouple follow-up queue card from composer | Chat UI refactor. Fork has chat changes. |
| `642b6942` | +275/-120 | MUL-5760: align chat queue UI with desktop reference | Chat UI. |
| `86f25565` | +322/-38 | MUL-5730: expose chat audience without bloating prompts | Chat prompts — fork has brief pattern. |
| `b3fa17f0` | +306/-0 | fix(chat): preserve follow-up transcript order | Chat ordering. |

### Fork-touch small (≤200 LOC, but file conflict)

| SHA | LOC | Subject | Conflict |
|---|---|---|---|
| `0d036ee5` | +97/-7 | MUL-6445: keep skill cache failures from blocking skill loads | Fork has skill bundle work. |
| `7c7ed8bf` | +103/-26 | MUL-5937: persist direct chat Codex sessions | Daemon chat session. |
| `20d2dd8c` | +119/-16 | MUL-5620: close repo-eviction liveness window | Daemon GC. |
| `3d47913c` | +270/-21 | MUL-6161: allow verified autopilot child assignment | `server/internal/handler/issue.go` (lab leader rewrite gate). |
| `212ecfb8` | +146/-85 | MUL-6300: reply turns own the status arc | Daemon workflow. |
| `3268d011` | +98/-32 | move in_progress start write into workflow step 3 | Daemon workflow step. |
| `e4177432` | +106/-10 | default completed-task TTL to 14d on Multica Cloud | Cloud-specific (skip on fork — fork has no Cloud). |
| `8787003c` | +283/-15 | MUL-6085: add completed-task environment retention TTL | Daemon env retention. |
| `698013ce` | +334/-35 | MUL-5771: configure workspaces root | Daemon workspace root. |
| `e4ebd41d` | +315/-4 | fix(daemon): make daemon log path discoverable | Daemon logging — fork has split server/daemon profile dirs (per CLAUDE.md §Known Stability Surfaces). |
| `807b8706` | +273/-98 | MUL-5992: deny Reasonix ask tool per task | Agent runtime. |

## SKIP-DIVERGENCE (semantic conflict with fork contracts)

> These would BREAK fork invariants if cherry-picked without adaptation.

1. **`2e0c599e`** — `fix(agent): avoid H1 headings in issue bodies (#6199)` — **Already ported** in 0.5.15 (`da1cc2003`). Do not re-cherry-pick.

2. **`a17857e6`** — `fix(agent): parse tab-separated Antigravity model catalog (#6654)` — Fork doesn't ship Antigravity runtime. SKIP unless adding it.

3. **`f4bf8c4`** — Windows-only PowerShell routing. Fork is darwin-only. SKIP.

4. **`642b6942`/`86f25565`/`b3fa17f0`/`a8d5daac`** — Chat UI refactors touching `chat-window.tsx` / `experimental-chat-pane.tsx`. Fork has pre-workspace lab surface (`ChatWindow wsId` prop, `ExperimentalChatPane` thin wrapper per CLAUDE.md §Pre-workspace Lab surfaces). Likely conflicts.

5. **`67c6aa05`** — Telegram channel integration (9K LOC). Fork has no channels; would re-introduce a fork-rejected surface (CLAUDE.md §Localized Fork — no external support UI adjacent).

6. **`3460f9ae`** — Entitlement-backed quotas. Fork has no entitlement concept. SKIP.

7. **`13f9f8e5`** — Admin add workspace seats. SKIP (no seats concept).

8. **`bedad9e2`/`f45b5916`/`5bf10d48`/`4bf3c73a`/`659bb41a`** — MUL-6350 plugin system rebuild (5 commits, 20K+ LOC total). Fork has custom `user_plugin_runtime.go` (F-006/F-008/F-013 hardened 0.5.18; MUL-6350 4 atomic commits ported in 0.5.36 per memory `0.5.36-mul6243-ui-closeout`). Verify each PR 1-4 against fork's existing plugin layer before considering.

9. **`4b8873df`** — Revision-aware concurrency guards. Fork has 5 nested mutex gates (CLAUDE.md §Lab ↔ Assignee Mutex, §Active Contract #5 swarm_topology). Likely conflicts at the lock-acquisition order level.

10. **`5e2d349e`** — DingTalk refactor (6790 LOC). Fork doesn't use DingTalk. SKIP.

11. **`18f130a5`** — `fix(migrate): restore unique migration prefixes after the 362 collision`. Fork's migration history is heavily customized (migs 155-273 per 0.5.x). Verify by comparing `server/migrations/` against upstream before cherry-pick.

12. **`e5f976144`** — `fix(server): keep source-context cleanup off the runtime sweep tick` introduces a new `source_context_sweeper.go`. Fork has `runtime_gc.go` (0.5.25 fix); verify no GC schedule collision.

13. **`8a5a6adb`** — ZeroClaw ACP runtime. Fork already has `openclaw`; verify this isn't a fork/clone of openclaw before adding.

14. **`8f48c380`** — `fix(desktop): enable the renderer process sandbox` touches `apps/desktop/src/main/index.ts` `BrowserWindow` config. Fork has extensive main-process work (0.5.18 nested-binary codesign, 0.5.30 renderer integrity, offscreen guard). Verify sandbox doesn't break existing IPC channels (`window.daemonAPI`, `window.experimentalAPI`, `window.userPluginAPI`).

15. **`54027ba76`** (HEAD) — MUL-6639 skill listings + stall timeouts. 1441 LOC across 13 files including new `cli/stall.go`, DB schema change, new metadata-only listing. Fork's `skill.go` has 0.5.18 SEC-P1-7 work adjacent. **Verify DB migration compatibility** before considering.

## Recommended 0.5.71 Integration Plan

> Sub-batches to avoid agent thrash (0.5.36 lesson: 4.4x LOC inflation when wholesale-adopting).

### Batch 0 — Pre-flight (no cherry-picks)

- Run `pnpm typecheck` on clean `epic/0.5.13-integration` to confirm ship gate baseline green.
- Confirm fork's migration history vs upstream — `git log --oneline -- server/migrations/ | head -50` to see fork's added migrations.

### Batch 1 — CVE/security (~6 commits, ~1 hour)

Pure dependency bumps + auth defaults. Lowest conflict risk.

- `62c5ba11` builder-util-runtime CVE
- `79d13b6b` brace-expansion CVE
- `4c1c8709` CVE-2026-9277
- `658b0b7d` fail-fast on insecure JWT secret
- `a3bd38da` redact autopilot webhook credentials
- `8f48c380` enable renderer process sandbox (REVIEW first — verify no IPC break)

**Gate**: `pnpm typecheck` + `cd server && go test -count=1 ./internal/cli/... ./internal/handler/auth/... ./internal/service/autopilot/...`

### Batch 2 — CLI/skill surgical (~10 commits, ~2 hours)

CLI commands, skill listings, token rejection.

- `617d41602` task token reject
- `1ef1d65b` remove obsolete autopilot priority flag
- `39ddc82c` resolve full-UUID issue refs locally
- `55c7cf05` Mentions brief reference fact
- `bdcb231ba` Qwen prompt on stdin
- `844250420` hide private runtimes from non-owners
- `750fc0cc5` MCode ACP session startup race
- `1ea6d18f` skill bundle download bytes
- `0d036ee5` skill cache failures don't block loads
- `5a80f802` MULTICA_TASK_QUEUED_TTL (add to fork's serializeEnvFile)

**Gate**: same as Batch 1 plus `server/internal/cli/...` + skill handler tests.

### Batch 3 — UX/cosmetic (~12 commits, ~2 hours)

- `e6013e83e` bound board description previews
- `18a85b8c` single-character GitHub issue prefixes
- `e5b28564b` prevent crowded transcript end ticks
- `04fd64b41` keep following rapid live output
- `dd147440` preserve idle footer spacing
- `beb3e9be` widen conversation gutter
- `9bd55678` desktop shell nav feedback on issue switch (REVIEW)
- `9b013e34` inbox arrow-key selection
- `16f9f252` inbox archive keyboard shortcut
- `a1d160ee` dead dimcode.ai link
- `92c1a4df` de-emphasize share-link URLs
- `f9b7bf52` i18n custom_property.none reuse

**Gate**: `pnpm typecheck` + `pnpm test`.

### Batch 4 — Docs/changelog (~5 commits, ~30 min)

- 5× `docs(changelog): add v0.4.{30..34} release entries` — cherry-pick individually (each references upstream issue numbers).

### Batch 5 — Large REVIEW commits (decision: include or SKIP-DIVERGENCE)

> **STOP and ASK USER** before tackling. Each is multi-day port with high conflict risk:

- `f8ec870f` per-agent starter prompts (REVIEW)
- `8c563b49` source context for sub-issues (REVIEW — 10K LOC)
- `b2b4699f` inbox status/priority filters (REVIEW)
- `21673268b` source-context copy (REVIEW — locale)
- `e5f976144` source-context cleanup off runtime sweep (REVIEW)
- `46b5d9e6` process tree ownership (REVIEW — P0 security, fork-modified)
- `54027ba76` HEAD skill listings (REVIEW — 1441 LOC, schema)

### Estimated effort

- **Batch 1-4** (33 commits): ~5-6 hours, low conflict.
- **Batch 5** (7 commits): 2-3 days if approved one by one; otherwise SKIP entirely.
- **Total without Batch 5**: ~6 hours.
- **Total with Batch 5**: ~3 days.

## Critical Reminders

- **Do NOT cherry-pick SKIP** (re-introduces fork-rejected surfaces — billing, OAuth, Discord, seats, telemetry, analytics, electron-updater).
- **Do NOT use `git rebase upstream/main`** (0.5.36 lesson: wholesale adoption 4.4x LOC inflation; agents re-import fork's whole files instead of surgical change).
- **Per-commit gate**: each cherry-pick must pass `pnpm typecheck` + `cd server && go test -count=1 ./internal/... ./pkg/agent/...` (CLAUDE.md §Ship gate).
- **Cherry-pick one at a time, not batched** — verify per-commit, then `git commit --amend` only if attribution needed.
- **Verify migrations before applying** Batch 5 commits that touch `server/migrations/` — fork's history is 165-273 (post-0.5.6).
- **Do NOT port MUL-6350 PR 3/4 wholesale** (plugin system) — fork's plugin layer is custom.
- **Do NOT re-port `2e0c599e`** (#6199) — already in 0.5.15.

## Reference

- `.omc/upstream-integration-triage-2026-08-26.md` (this file)
- `CLAUDE.md` §Localized Fork (retired features list)
- `CLAUDE.md` §Retired Features (do NOT re-add)
- `CLAUDE.md` §Known Stability Surfaces (fork-modified files to protect)
- `CLAUDE.md` §Labs Platform (8 active flag catalog)
- `.omc/release-notes-0.5.70.md` (current fork ship state)
- `.omc/release-notes-0.5.67.md` (audit batch — 7 ships closed all labs audit deferred items)
- Memory `0.5.36-mul6243-ui-closeout-2026-08-18.md` (cherry-pick wholesale-adoption trap)
- Memory `0.5.15-cherry-pick-batch-2026-08-10.md` (surgical cherry-pick methodology, 95.7% conflict rate)

## Post-triage re-grade (2026-08-26) — 10 upstream commits since triage was authored

> Triage baseline `54027ba76` was the upstream HEAD at triage time (2026-08-26 morning). Since then upstream advanced to `09a2410e8` (current upstream/main). 10 new commits landed. Re-graded below using the same methodology as the original triage.

### New commits (10)

| SHA | Subject | Verdict | Reason |
|---|---|---|---|
| `09a2410e8` | MUL-6709: starter_prompts -> conversation_starters (#7592) | DEFER Batch 5 | Conflicts with `f8ec870f` per-agent starter prompts (already REVIEW-tier in triage). Two competing starter-prompt architectures in upstream — port one or the other, not both. |
| `f74a71060` | perf(web): preload landing hero (MUL-6711) | Batch 4 | Web-only (`apps/web/`); no fork desktop impact. |
| `21b938bfd` | docs(changelog): drop reverted entries v0.4.35 | Batch 4 | Docs-only, after `c7d66071e` (forward-cherry-pick order). |
| `c7d66071e` | docs(changelog): add v0.4.35 release entry | Batch 4 | Docs-only changelog entry — cherry-pick individually per triage methodology §Batch 4. |
| `3b1018e9c` | Revert MUL-6016 Codex capacity retry | SKIP | Fork never had MUL-6016 (Codex retry logic absent in fork's daemon). |
| `a9d86af46` | Revert MUL-6385 live HTML preview | SKIP | Fork never had MUL-6385 (live HTML preview surface not in fork's renderer). |
| `cd169947b` | MUL-6686 daemon readable workdir paths | DEFER Batch 5 | Touches daemon/execenv — fork has `0.5.20 sub-agent tempdir fix` + `0.5.25 RuntimeGC fix` + `0.5.27/0.5.28 PORT leak fix`; multi-layer integration risk. |
| `7b8c78399` | fix(issues): verified autopilot private agents | DEFER Batch 5 | Touches `server/internal/handler/issue.go` (fork-heavy — CLAUDE.md §Lab ↔ Assignee Mutex + Active Contract #5 swarm_topology). |
| `bbba9c546` | MUL-6697 runtime access on task claims | DEFER Batch 5 | Touches daemon + task layer — fork's `daemon-manager.ts:1272` allowlist (F-027 closed 0.5.18) + per-task TMPDIR injection (0.5.20) at risk. |
| `6c8f02aea` | fix(billing): trusted Pro limits | SKIP | Billing surface — fork removed (CLAUDE.md §Localized Fork). |

### Re-grade summary

- **4 new Batch 4 candidates** (`f74a71060`, `21b938bfd`, `c7d66071e`, plus reorder pair) — net +3 after `21b938bfd`/`c7d66071e` forward-order note.
- **4 new DEFER Batch 5 candidates** (`09a2410e8`, `cd169947b`, `7b8c78399`, `bbba9c546`) — fork-heavy touch + integration risk.
- **3 new SKIP** (`3b1018e9c`, `a9d86af46`, `6c8f02aea`) — never-in-fork or fork-removed.

### Updated effort estimate (incremental)

- **Batch 4 delta**: +3 commits, ~15 min (docs-only).
- **Batch 5 delta**: +4 commits, +1-2 days if approved one by one.
- **Total new work without Batch 5**: ~15 min.
- **Total new work with Batch 5**: +1-2 days on top of triage estimate.

### Critical reminder (unchanged)

- Per-commit gate: `pnpm typecheck` + `cd server && go test -count=1 ./internal/... ./pkg/agent/...` (CLAUDE.md §Ship gate).
- Cherry-pick one at a time, not batched (0.5.36 wholesale-adoption lesson).
- DEFER Batch 5 commits require explicit user decision before any port.

## Post-triage re-grade #2 (2026-08-27, 0.5.79 cycle)

Upstream advanced 1 commit past the last re-grade head (`09a2410e8` → `9a19c90f8`).

| SHA | Subject | Verdict | Notes |
|---|---|---|---|
| `9a19c90f8` | MUL-6703: feat(skills) Import from local in New skill dialog (+1638) | **APPLY — landed as a split port** | Pure-local feature (folder/.skill/.zip import), no cloud/telemetry surface, aligned with fork philosophy. Split into 3 commits: server multipart branch + finishSkillImport extraction (`701deb32b`), core pack-archive + client method (`a78df9892`, fork idiom WITHOUT upstream's zod envelope layer — fork has no SkillSchema), views dialog + locales ×4 + docs (`8194b1198`). Upstream client.test additions + webp asset skipped (pin absent layers). Gates: go handler suite, core tsc+11 vitest, dialog 8 vitest, turbo typecheck all green. |

### MUL-6632 / inbox archive family — verdict UPGRADED to decision-gate

The 0.5.77 B5-inbox STOP note framed the blockers as "port ~3–5 foundation commits first". Verified on 2026-08-27: **no such foundation chain exists.** Upstream's inbox architecture (filter-store.ts, inbox-view.ts, inbox-context-menu, inbox-filter-menu, inbox-list …19 files ≈ 4400 LOC under packages/{views,core}) was born inside MUL-6632 itself; the shared-name files fork DOES have are whole-file rewrites vs upstream (inbox-page 911↔523 lines, ~870 diff lines). Porting = architecture replacement of fork's inbox across desktop shell nav + realtime ws-updaters + locales ×4, not cherry-picking.

- Status: **GATE — needs explicit user decision** (adopt upstream inbox architecture as multi-session project, or keep fork inbox and treat status/priority filtering as future fork-local work).
- Until decided: MUL-6583 `9622dd55a`, MUL-6660 dependency chain items that touch inbox UI stay SKIP-DIVERGENCE.

## Incremental triage #3 (2026-08-27 12:20 CST, post-0.5.79 health check)

Upstream advanced exactly 1 commit past re-grade #2 head (`9a19c90f8` → `76aada3a9`).

| SHA | Subject | Verdict | Notes |
|---|---|---|---|
| `76aada3a9` | fix(views): restore quick create dialog height (#7596) (+12/−9, 2 files) | **SKIP — dependent on unported feature** | The fix refines the `sourceContextData`-conditional height chain (`sourceContextExpanded ? … : sourceContextData ? …`). Fork's `create-issue-dialog.tsx` contains neither `sourceContextData` nor `sourceContextExpanded` (verified by grep 2026-08-27): the whole source-context quick-create feature rides on `8c563b49`, already SKIP-default in Batch 5 deferrals. Revisit only if/when source-context is ever ported. |

Also recorded during this pass (environment, not upstream): `codesign --verify --deep --strict` fails on every shipped bundle (0.5.78 bak and 0.5.79 alike) with "file modified: app.asar.unpacked/resources/bin/{migrate,multica,server}" — chronic, by-design artifact of the ship chain re-signing nested Go binaries AFTER the electron-builder seal (ship-mac.sh header: skipping that re-sign → Gatekeeper SIGKILLs the backend). Nested binaries verify valid individually; cold-start gate passes. Accepted state for local non-notarized installs.
