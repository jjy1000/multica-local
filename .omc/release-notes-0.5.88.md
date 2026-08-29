---
name: release-notes-0.5.88
created: 2026-08-29T14:12:06Z
updated: 2026-08-29T14:12:06Z
---

# 0.5.88 — mythos engine unification + labs delegation loop + frozen metadata + user-plugin taxonomy + UX polish

**Branch:** `epic/0.5.72-followups` (post `d0b89de82` dev-record header refresh, version bump `81fd1fc8f`)
**Trigger:** 0.5.87 + 0.5.88 dev cycles were committed on the branch but NOT packaged (0.5.86 was the last shipped release); this ship packages both.
**Wire contract:** no new schema migrations (only `mythos_run.sql` query additions + sqlc generated code); no flag-key changes; `swarm_topology` stays frozen (advisory `Frozen`/`SuccessorKey` metadata); `pythia_oracle`/`timesfm` stay `AutoDispatch=false`.

## TL;DR

Six work lines land in one release:

1. **0.5.87 — mythos async-engine unification** (swarm-orchestrator hardening patterns ported into the mythos engine): `ListStalledMythosRunsForGC` + `service/mythos/reaper.go` 6h reaper loop (boot-sweep-first, fails `running` rows older than 24h and `supervising` rows whose `supervision_state.last_check_at` heartbeat is >1h stale, merged abort preserving tick history, `abort_reason='stalled_reap'`); per-tick 2×30s timeout in the supervise loop; heartbeat stamped at t=0. Live verification confirmed the 4 zombie runs stuck since 08-24 were reaped on first boot.
2. **0.5.87 — built-in-lab audit fixes**: web 404 stubs for `semantica-explorer`/`timesfm-lab`, missing locale keys in all 4 locales, picker comment drift. 10-lab built-in-plugin audit: all healthy or intentionally frozen.
3. **0.5.88 — labs delegation loop closed end-to-end**: `multica lab delegate --parent [--stage]` (child carries `parent_issue_id`+`lab_source` in one create; result comment posted back on the parent best-effort, `[lab delegate]` header + 2000-rune cap); parent-agent wake is FREE via the existing `issue_child_done.go` channel; causal parent→child `depends_on` edge recorded on delegated creates (`service/causal_graph/delegate_edge.go`, provenance `issue_delegate`, best-effort — issue creation must NEVER fail on the causal write); daemon briefing `## Available Labs (delegation)` (`delegate_brief.go`, injected in `ClaimTaskByRuntime`) — enabled assignee-model labs with resolvable leaders ONLY (frozen + `AutoDispatch=false` labs skipped), 2000-byte cap / 200 ms budget / silent fallback.
4. **0.5.88 — frozen metadata**: catalog `Frozen`/`SuccessorKey` advisory fields (`swarm_topology`→`mythos_swarm` machine-readable; toggle behavior deliberately unchanged; `FlagByKey` helper resolves user-plugin layer first; settings amber banner + briefing skip).
5. **0.5.88 — user plugins join the 0.5.86 interaction-model taxonomy**: manifest `interaction_model` (absent → auxiliary, behavior-preserving) + `leader_agent` (required iff assignee, 400 at create/update); BOTH `resolveLabLeader` tables (handler + service) route user plugins through `UserPluginLeaderAgent` reading the DB ROW (soft-deleted plugin never locks; legacy `capabilities.leader` stays as read-side fallback); form gains model selector + conditional leader input; lock semantics identical to built-ins.
6. **0.5.88 — UX/animation polish** (motion/react + `useReducedMotion` idiom): LabProgressCard skeletons + status fade-through; section expand height animation; settings interaction-model badges (独立工作型/辅助协作型) + frozen banner; causal-graph loading overlay (canvas stays mounted) + ~180 ms rAF-eased zoom + optimistic suggestion confirm/reject with toasts; targeted assignee-lock toast (`matchAssigneeLabLockError`); dead `LeaderRewriteConfirmDialog` (0.3.45.8 relic) deleted with its locale keys.

Plus verification fixes: `f2467873e` determinized the quick-create daemon-version gate (`QuickCreateVersionGateOverride`, same test-only pattern as `RuntimeOnlineOverride`) killing the parallel-cleanup race that 422'd `TestQuickCreateIssueParentTrustBoundary` in full `./...` runs; `98fd95138` fixed a briefing/delegate contradiction (skip `AutoDispatch=false` labs in the briefing + fail fast in lab delegate) caught by live verification.

## Files (commit set `19d4c506f..81fd1fc8f`)

| Commit | Scope |
| --- | --- |
| `19d4c506f` | feat(server): 0.5.87 — mythos async-engine unification (reaper, per-tick timeout, t=0 heartbeat) |
| `8d0cf35f6` | fix(web)+i18n: 0.5.87 — built-in-lab audit fixes (web stubs, locale keys, picker comment drift) |
| `b28715c66` | feat(server): 0.5.88 — delegation loop closure (lab delegate --parent, result comment, causal edge, briefing), frozen flags, user-plugin taxonomy |
| `4250a41a1` | feat(client): 0.5.88 — labs UX/animation polish + user-plugin form interaction-model contract |
| `b3924668b` | docs: 0.5.88 dev record — container vision gap-closure |
| `f2467873e` | test(handler): 0.5.88 — determinize quick-create daemon-version gate |
| `98fd95138` | fix(server): 0.5.88 — briefing/delegate contradiction (skip AutoDispatch=false labs) |
| `eb48f6a24` | docs: 0.5.88 — full verification chapter (static gates, live API full-loop, Electron UI audit, bug ledger) |
| `d0b89de82` | docs: header refresh for 0.5.87 + 0.5.88 dev cycles; AGENTS.md digest re-synced |
| `81fd1fc8f` | chore(release): bump 0.5.86 → 0.5.88 |

## Key contracts honoured / established

- **Briefing/delegate never-disagree law (standing)**: the delegation briefing and the `lab delegate` CLI advertise/accept the SAME lab set — enabled assignee-model with resolvable leaders ONLY; frozen + `AutoDispatch=false` labs skipped. Live verification caught the first cut advertising labs the loop can never dispatch.
- **Interaction-model taxonomy (0.5.86) extended to user plugins**: manifest `interaction_model` + `leader_agent`; both `resolveLabLeader` double tables route user plugins via DB row; behavior-preserving for plugins without the fields.
- **Causal-graph laws hold**: delegated creates record `depends_on` via `Recorder.ensureIssueRootNode` both ends with the any-status never-nag probe; the causal write is strictly best-effort.
- **Frozen labs stay frozen**: `swarm_topology` keeps mutex + HideFromIssueLabPicker + amber banner; `pythia_oracle`/`timesfm` keep `AutoDispatch=false` (manual trigger only).

## Verification (0.5.88 dev record acceptance chapter)

- Static gates green in a quiet env (deterministic — flake determinized by `f2467873e`).
- Live API full loop: daemon-role register → claim → start → complete; briefing asserted on real claims; 4 delegated creates → 4 idempotent edges; child-done mention; user-plugin lock 400 / auto-fill / unregister all live.
- Electron UI audit via Playwright `_electron.launch`: all 4 checkpoints PASS with measured timings (expand ~200 ms ease-out, zoom lerp ~140 ms, skeleton caught live; 28 screenshots).

## Known remaining (next cycle 0.5.89+ ledger)

Daemonless install of `causal_graph`/`pythia` inserts NULL `runtime_id` against a NOT NULL column → guaranteed `install_error` until a daemon registers (fix = synthetic runtime stub à la `upsertClaudeScienceRuntime`); `CompleteTask`/`FailTask` on `dispatched` tasks silently 200 no-op; unbound issue's LabPicker trigger is an invisible ~8×0px chip; post-rewrite assignee chip shows "Unknown Agent" for offline leaders; plugin create ignores `title_en`; `TestInstallTimesfm` workspace scoping.

## Ship log

[`.omc/0.5.88-ship-2026-08-29.md`](0.5.88-ship-2026-08-29.md) · dev records: [`.omc/0.5.87-dev-2026-08-29.md`](0.5.87-dev-2026-08-29.md) + [`.omc/0.5.88-dev-2026-08-29.md`](0.5.88-dev-2026-08-29.md)
