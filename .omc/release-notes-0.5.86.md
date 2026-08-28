---
name: release-notes-0.5.86
created: 2026-08-28T15:39:55Z
updated: 2026-08-28T15:39:55Z
---

# 0.5.86 — Interaction-model law + assignee lock + lab report writeback + swarm consolidation + causal-graph readability

**Branch:** `epic/0.5.72-followups` (post `fc01afd76` dev record, version bump `f1647903a`)
**Trigger:** 0.5.85 follow-ups — labs interaction model 规范化, swarm 双入口合并, causal-graph 视图可读性
**Wire contract:** migrations 282+283 forward-only; no flag key changes; `swarm_topology` frozen but key still resolves

## TL;DR

Five work lines land in one release:

1. **Interaction-model law** — every lab flag carries `InteractionModel`: `assignee` (独立工作型, works as a selectable assignee: claude_science_lab, pythia_oracle, mythos_swarm, swarm_topology, semantica, timesfm) vs `auxiliary` (辅助协作型: causal_graph, llm_wiki_bridge); stamped as `interaction_model` on `ExperimentalFlagResponse`; legacy `""` keeps pre-0.5.86 behavior.
2. **Assignee-lock hard gate** (`handler/issue.go::assigneeLabLockError`, create + update parity) — assignee-model labs lock the assignee slot to the lab's leader agent: empty assignee always allowed (0.3.46 leader-rewrite fills), non-leader → 400 naming the leader, mythos keeps the 0.3.33 strict no-manual-assignee mutex. Update path scoped by touched fields (assignee-touched → full post-state check; lab-only flip → allowed ONLY when the leader AGENT ROW exists — name-level resolution is not enough; `swarm_topology`'s `swarm_coordinator` is bootstrap-provisioned). `timesfm` gained its missing leader `timesfm_oracle` in BOTH leader tables.
3. **Lab report writeback** (new `handler/lab_report_writeback.go` + mig 282 `report_comment_id` on both forecast-run tables) — Pythia + TimesFM issue-scoped runs deliver a text report comment on the issue (AuthorType=`agent`, system-author fallback = all-zero UUID from mig 107, exactly-once via `COALESCE(report_comment_id, $2)`).
4. **Swarm consolidation** — ONE 蜂群 lab: `mythos_swarm` is the swarm entry; `swarm_topology` frozen (HideFromIssueLabPicker + sidebar entry emptied + amber consolidation banner); orphan `swarm\_%\_%` agents archived by mig 283; swarm GC sweep gains stalled-run reaping + role-agent archiving.
5. **Causal-graph readability (views)** — depth-banded ring layout (≤3 center seeds, per-ring radius from chord length, parent-angle ordering), node drag with per-view position overrides, wheel zoom/pan canvas, `pollPaused` while dragging, reduced-motion-gated animations; issue popup icon shows depth-1 with a "+N 节点 · M 边" summary row at depth 2.

Plus **issue lab progress card**: per-lab run state + click-through on issue detail; run-recency heuristics extracted to `lab-run-heuristics.ts` (shared with lab-output-panel).

## Files (commit set `70a20d84f..f1647903a`)

| Commit | Scope |
| --- | --- |
| `70a20d84f` | causal graph readability overhaul (views) |
| `8edc53089` | issue lab progress card (views) |
| `94f693f7e` | interaction model + assignee lock (server, views) |
| `46c27cf51` | lab report writeback (server) |
| `4bf578f89` | swarm consolidation (server) |
| `5e4d6a49a` | bundled migration copies 281-283 (desktop) |
| `602281f4f` | DB-backed pins for assignee-lock gate (server) |
| `a3a78bacb` | swarm_topology mutex preserved: lab-flip bypass requires leader agent row (server) |
| `fc01afd76` | 0.5.86 dev record (docs) |
| `f1647903a` | version bump 0.5.85 → 0.5.86 (release) |

## Key contracts honoured / established

- **Assignee-lock parity**: create + update paths share `assigneeLabLockError`; pinned by `TestAssigneeLabLockGate` (8 subtests) + `TestUpdateIssueLabSourceRewritesStaleAssignee`.
- **Mutex preservation**: `swarm_topology` keeps the Contract #5 no-manual-assignee mutex; the lab-flip bypass only fires when the leader agent ROW exists (a3a78bacb caught + fixed the regression in the full handler suite run).
- **Exactly-once report writeback**: `COALESCE(report_comment_id, $2)` idempotency on both forecast-run tables.
- **Swarm GC**: stalled-run reaping (`ListStalledSwarmRunsForGC`) + role-agent archiving (`ArchiveSwarmRoleAgentRows`).

## Verification

- `pnpm typecheck` full turbo: 6/6 (1 cached).
- `go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...` with `DATABASE_URL` exported from repo-root `.env`: all packages ok, 0 FAIL; DB-backed handler suite confirmed live (`TestAssigneeLabLockGate` 8/8 PASS, 0.04 s — not silent-skip).
- Migrations 282+283 applied via ship step 2/7 `migrate up` before bundling.
- Cold-start verify + row parity: see `.omc/0.5.86-ship-2026-08-28.md`.

## Follow-ups (0.5.87+)

- DB-backed handler integration tests for `ClaimTaskByRuntime` with the subgraph path active (15 unit tests cover pure logic; handler pin is a follow-up).
- Tier C Pythia hypothesis→evidence closure; historian/verifier automation; evolver window bound.
- Mythos async-engine unification (swarm orchestrator port) as fast-follow.
