# Release Notes — 0.5.13 (2026-08-09)

## Summary

Upstream integration ship (baseline `epic/0.5.12-cherry-pick` + 3 commits):

- **#5674 — daemon fail-fast** (re-introduced): `daemon start` now fails fast
  with actionable errors when you are not logged in, the stored token is
  rejected, or the server is unreachable — instead of a blind 45 s poll that
  printed a vague warning and exited 0. `daemon restart` probes `/api/me`
  BEFORE stopping the running daemon, so a revoked token or unreachable
  server never kills a working daemon. (Originally ported on 0.5.12, reverted
  there; re-landed on 0.5.13 as a fork-local fusion.)
- **#6515 — search demote cancelled**: cancelled issues and projects no
  longer outrank live work. A cancelled issue whose title matched exactly used
  to beat an in_progress issue that merely contained the phrase — and a
  workspace with many cancelled issues could push live work off the first
  search page entirely. Direct hits (exact title / number) stay on top; `done`
  is deliberately untouched. Applies to issue search, project search, the
  command palette (Cmd+K) and the mention picker partition.
- **#6546 — CLI `--compact`**: `multica issue comment list --compact
  --output json` drops reader-noise fields (issue_id, source_task_id,
  updated_at when equal to created_at, nulls, empty arrays) for leaner agent
  reads. Opt-in; default output unchanged.
- **Audit fixes**: the `agent_creation_expert` provisioner no longer writes
  `custom_args` as a JSON object (would re-seed per-read WARN noise that
  migration 238 repairs); `addSubscriber` filters non-subscribable entity
  types at the single chokepoint instead of hitting the DB CHECK per
  occurrence.

Also carried on the 0.5.12 baseline: #6194 usage rollup window fix, #5406
non-ErrNoRows DB error propagation, #6095 WCAG muted-foreground token,
#5355 daemon self-heal, #6124 open-in-new-tab, plus the 0.5.12 stability
ship (custom_args migration 238, log rotation).

## Backups (disk savings)

The pre-update snapshot now uses `pg_dump -Fc` (compressed custom format)
instead of per-table CSVs: **24 MB per snapshot vs ~1.5 GB** (~62x smaller),
full schema + data in one restorable file. Pre-update retention (3 dirs) and
KB vault strategy unchanged.

## Verification

- Go: `cmd/multica` + `internal/handler` + `internal/daemon` + `internal/cli`
  suites green, including 8 new fail-fast tests and 4 new search-demote tests.
- TS: `core` 680 tests green (12 new cancelled-rank cases); `views` green
  except 2 pre-existing `issue-detail` scroll-to-comment failures (verified
  failing on the clean baseline — unrelated).
- Cold start: three-check pass (5432/8090/health), row parity
  `1|303|2109|105` (workspace/agent unchanged; issue/comment only grow),
  asar 125 `rawRequest`, nested binaries signed.

## Notes

- The upstream chat follow-up queue cluster and chat V2 remain deliberately
  not ported (fork single-task chat model). Runtime Unbind (MUL-6220) and the
  CodeX rollout gate (MUL-5305) were evaluated and skipped: the former is a
  large irreversible migration with low single-user payoff, the latter's
  prerequisite (MUL-4424 per-task session isolation) does not match the
  fork's shared-Codex-home model — schema columns stay pre-seeded but inert.
- 2 pre-existing `views` test failures are tracked separately (issue-detail
  scroll-to-comment timing).
