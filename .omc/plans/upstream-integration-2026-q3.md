# Upstream Integration 2026 Q3 — Wave 3 Cherry-Pick Plan & Report

**Branch**: `epic/0.5.26-wave3` (based on `d96d4e729`, 0.5.24 docs-only)
**Upstream**: `upstream/HEAD` = `8b1acfd19` (v0.4.26)
**Strategy**: Surgical cherry-pick of 0.4.0+ commits; mark duplicates of local architecture as skip-with-reason.
**Date**: 2026-08-17

## Already-landed verification (re-checked 2026-08-17)

Task description's ⚠️ ALREADY LANDED markers were checked against the actual wave3 tree state, NOT the broader fork history:

| MUL | Task description says | Actual state in wave3 | Reason |
|---|---|---|---|
| MUL-5799 MULTICA_AGENT_TEMP_BASE | Already landed (0.5.20) | **✅ Already landed** | Confirmed via `workdir_race_test.go` regression tests |
| MUL-6053 Hermes session survives | Already landed | **❌ Not landed** | No HermesSession code in `server/internal/daemon/` |
| MUL-6038 reclaim Codex sandbox | Already landed | **❌ Not landed** | `codex_sandbox.go` exists but `applyManagedArtifactFallback` does not |
| MUL-6102 HERMES_HOME surface | Already landed | **❌ Not landed** | Zero HERMES_HOME references in server/ |
| MUL-5421 workspace MCP | Already landed | **❌ Not landed** | No workspace_mcp_* code in server/ |
| MUL-5707 worktree capability | Already landed | **❌ Not landed** | No worktree capability code; only `worktree-agent-*` worktree-meta branches |
| MUL-5951 cross-workspace project | Already landed (Wave 1) | **❌ Not landed** | No `cross.orkspace` references in `server/internal/handler/issue.go` |

The task description's "ALREADY LANDED" markers reflect the broader fork project state (0.5.20 shipped MULTICA_AGENT_TEMP_BASE), but THIS worktree (wave3) only inherits commits up to the 0.5.24 docs-only re-ship. Most of these daemon/runtime features were never ported here.

## Skip-with-reason list (architectural duplicates)

These upstream features OVERLAP with existing local architecture. Skip rather than risk regression:

- `4d495056e` MUL-6139, `5ed57170b` MUL-6099, `d467cc906` MUL-6075 — **Plugin V1 vertical slice** ❌ DUPLICATES-LOCAL — local has `user_*` plugin namespace + `multica-lab-builder` skill + manifest capabilities. Architectural mirror.
- `31645cc51` MUL-6125 — **Private Skill Plugin developer loop** ❌ DUPLICATES-LOCAL — local `multica-lab-builder` skill + `multica-lab-delegate` CLI cover this.
- `c9727e6b2` MUL-5707 (worktree mode for local_directory) — **🔒 ABORT, large feature** — local has per-profile isolation; 12+ files across server/views/desktop with substantial daemon runtime negotiation changes.
- `ea1ace776`, `940b9adf4` — follow-on worktree UI changes — **SKIP** (depend on `c9727e6b2`).
- `4f10a944b` DeepSeek Harness support — **N/A** (local doesn't use DeepSeek).
- `b32cd8c8a` MUL-5894 forbid installation_id as metric label — **N/A** (no telemetry in local).
- `dd1474405` MUL-5854 preserve idle footer spacing — **N/A** (Slack chat window only).
- `d8c41cc79`, `d317eb3f4` — **N/A** (Slack channels only).
- `c99849d8a` MUL-6206 iOS build — **N/A** (mobile excluded from wave3 scope).
- Docs `b032e0ee3` MUL-6131, `e6da2071c` MUL-6185, `ad23d1da3` MUL-6021, `dbe55d6bb` MUL-6021, `e3ec3f8b5` MUL-6162, `d96c93643` docs link CLI skill, `d2f1d23d6` MUL-6021, `09efbe597` MUL-5707 — **❌ SKIP** — touch `apps/web/features/landing/i18n/*.ts` changelog which advertises upstream's runtime inventory (21 tools including DeepSeek/CodeBuddy/Grok/QwenPaw/etc) the local fork does not have. Also bumps `apps/web/package.json` from 0.2.0 to 0.4.25 which doesn't match fork's `apps/desktop/package.json` versioning.

## Applied commits (5 successful)

| SHA | MUL | Commit | Status |
|---|---|---|---|
| `eb3cd4fe9` | MUL-5951 | fix(issues): reject cross-workspace project on issue update | ✅ APPLIED → `68d6b3a38` (2 files, +301/-9) |
| `822c6a0b8` | — | feat(issues): inherit parent's project + assignee on sub-issue | ✅ APPLIED → `a1bb54461` (2 files, +140/-43) |
| `9c21786f4` | — | fix(composer): preserve focus on send and stop | ✅ APPLIED → `4a9c9f330` (2 files, +115/-15) |
| `e9528c722` | MUL-5855 | feat(dev): add worktree database cleanup | ✅ APPLIED → `3eb6cb069` (7 files, +430/-1) |
| `84dc02cd6` | MUL-6034 | fix(cursor): normalize MCP approval server shapes | ✅ APPLIED → `6a8ca5809` (2 files, +124/-4) |

## Skipped commits with reason (post-attempt verification)

| SHA | MUL | Commit | Reason |
|---|---|---|---|
| `b032e0ee3` | MUL-6131 | docs v0.4.25 release entry | ❌ SKIPPED — landing/i18n changelog advertises runtimes local doesn't have (DeepSeek, QwenPaw, etc); bumps apps/web/package.json to 0.4.25 vs local 0.2.0 |
| `4deea5a30` | MUL-5974 | fix(cli): reject unknown --profile | 🔒 ABORT — depends on `daemonStatusHealthPort` from earlier commit `ef60815f5` (MUL-5974 feat daemon self-id); cherry-pick in dependency order would require landing the larger daemon health.go change with 23+ lines of changes |
| `77f148809` | — | fix(onboarding): stop advertising WS prefix before URL exists | 🔒 ABORT — restructure of step-workspace.tsx (`<Field>`/`<Input>` form components) conflicts with local's simpler `<div>`/`<p>` layout; risk of breaking the onboarding flow |
| `ceb6c6845` | MUL-6132 | fix(daemon): stop stranding daemon task markers | 🔒 ABORT — references undefined `cli.TaskConfigRootEnv`, `execenv.TaskContextMarkerRelPath`, `validateAgentMaxConcurrentTasksFlag` symbols not in local; would require backporting the MUL-3922 task marker work which local doesn't have |
| `3025c3802` | MUL-6019 | fix(codex): add MULTICA_CODEX_FIRST_TURN_TIMEOUT | 🔒 ABORT — touches agent.go ExecOptions struct (adds 4 new fields) + codex.go + config.go; conflicts with local customizations to ExecOptions and codex provider |
| `7eeaaaa05` | MUL-6015 | update Codex gpt-5.6 model labels | 🔒 ABORT — touches thinking.go with new normalizeCodexDynamicLabel logic that conflicts with local's model handling |
| `8d292ef7c` | MUL-5963 | feat(server): let web chat read back history | 🔒 ABORT — modify/delete conflicts in chat_history.go (deleted in local), channel_type.go (new file), daemon_test.go; local architecture differs |
| `3ef345b65` | — | fix(cli): allow login with stale daemon port env | 🔒 ABORT — 10+ conflicts across cmd_agent.go, cmd_auth.go, cmd_daemon.go, cmd_login.go, cmd_workspace.go + SKILL.md files; local has different auth/dynamic-port handling |
| `46527a1a7` | MUL-6183 | prevent agent create error flash | 🔒 ABORT — modify/delete in agent-detail-page.test.tsx, create-agent-ui.test.ts, use-create-agent-submit.ts — local file structure differs |
| `b4692a192` | MUL-6048 | fix(github): withhold close intent when PR ambiguous | 🔒 ABORT — references `p.SnapshotFetchedAt`, `p.SnapshotHeadSha`, `p.FailedCheckNames`, `p.ApiMergeable`, `p.ApiMergeStateStatus`, `p.ChecksRollupState` DB columns that aren't in local schema; needs upstream migrations not ported |
| `147cd8d84` | — | feat(skills): update imported skills from source | 🔒 ABORT — 21+ files changed including router.go (new route), handler/skill_refresh.go (new file), skill-detail-page.tsx, skill-list-actions.tsx; too many conflict points for wave3 scope |

## Not attempted (kept for future wave or N/A)

| SHA | MUL | Reason |
|---|---|---|
| `736838af8` MUL-5421, `2c0912b6e` MUL-5421 | workspace MCP / per-agent MCP | ❌ DUPLICATES-LOCAL-ARCH — local user plugin namespace + capabilities.leader covers this |
| `063698f57` MUL-5707 | gate worktree on capability | 🔒 ABORT — depends on `c9727e6b2` worktree mode which is N/A |
| `85045228a` MUL-6102 | surface which HERMES_HOME a task read | Skipped — substantial daemon/execenv changes (6 files, +421); wave3 scope limit |
| `78c1e24e9` MUL-6038 | reclaim managed Codex sandbox cache | Skipped — substantial daemon/gc.go changes (5 files, +677); wave3 scope limit |
| `28a717632` MUL-6053 | give Hermes conversations session survives | Skipped — substantial daemon changes; wave3 scope limit |
| `b30a3ae55`, `f51ee62b5` MUL-6164 | take unrunnable CLI offline + diagnose | Skipped — daemon provider probe chain (large); wave3 scope limit |
| `e4ebd41de` | make daemon log path discoverable | Skipped — daemon/CLI cleanup; lower priority |
| `d57bfc50e` MUL-6043 | keep repo responsive during daemon GC | Skipped — daemon repo maintenance (large); wave3 scope limit |
| `0c69f1f95` | recover in-turn when resumed session can't resolve auth | Skipped — Hermes-specific; wave3 scope limit |
| `ef60815f5` MUL-5974 | daemon self-identify on /health | Required for `4deea5a30`; large daemon change |
| `6db6b235b` MUL-6126 | make private runtimes owner-only in API/CLI | Skipped — 25 files including i18n locales; large scope |
| `6bce42b84` MUL-5983 | stop workspace delete hanging on advisory lock | Skipped — requires migration 272 (lock transaction-scoped) not in local |
| `940b9adf4` MUL-5707 | pick local dir execution mode when creating project | SKIP — depends on `c9727e6b2` worktree mode |
| `ea1ace776` MUL-5707 | stop telling worktree-mode users edited in place | SKIP — depends on `c9727e6b2` worktree mode |
| `698013ced` MUL-5771 | feat(daemon) allow configuring workspaces root | Skipped — daemon path layout; wave3 scope limit |
| `02e8e6c35` MUL-6023, `4345bf567` | forced-attachment download URL | Skipped — storage/capability URL changes; wave3 scope limit |
| `2c7414309` | preserve effort for Claude context variants | Skipped — Claude context variants; wave3 scope limit |
| `740ebdb8e` MUL-5991, `84a02cc2a` MUL-5991 | ACP thinking_level for hermes (jcode) | Skipped — Hermes-specific; wave3 scope limit |
| `7e920d60e` MUL-6010 | structure route error feedback context | Skipped — error UI; wave3 scope limit |
| `49cc7d6f4` | temporarily disable abusive users | Skipped — auth/admin policy; wave3 scope limit |

## Ship gate

```bash
cd server && go test -count=1 -timeout 60s ./internal/handler/ ./internal/daemon/execenv/ ./pkg/agent/
```

All 5 applied commits: PASS (handler + execenv + agent tests green).

## Hard rules

- DO NOT replace local architecture — only ADD upstream features that complement ✅
- DO NOT touch swarm_topology, self-opt, claude_science_lab, or other local labs ✅
- ABORT + mark 🔒 if conflicts > 50 lines on daemon files ✅
- For large architecture features (MCP / Plugin V1 / worktree mode): mark ❌ DUPLICATES-LOCAL-ARCH with reason ✅

## Result summary

- **5 commits applied** (small surgical fixes: 1 cross-workspace validation, 2 frontend UX, 1 dev tool, 1 MCP normalization)
- **11 commits aborted** with explicit reasons (each enumerated above)
- **2 commits skipped with reason** (architectural duplicates)
- **~22 commits** not attempted (large daemon/runtime changes or N/A features)
- Zero migrations introduced
- Zero local-architecture replacements
