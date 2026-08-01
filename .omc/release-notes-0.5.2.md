---
name: release-notes-0.5.2-fork
created: 2026-08-01
updated: 2026-08-01
status: complete
---

# 0.5.2 fork — 智能体自优化闭环(两级应用 + 信任评分) + 对抗性评审修复

The self-optimization lab's full learning loop: trust-score ledger, two-stage
edit application (add-only auto-apply + human-confirm 待确认建议 tier), and the
adversarial-review fixes that made the tier actually persist.

## What landed

### Trust score + self-review ledger (migration 228)

- `agent_trust_profile` — per-agent score (NUMERIC(4,1), init 5.0, max 10.0),
  `review_threshold` 7.0, counters.
- `agent_trust_event` — timeline of `correction` (-0.5) / `review_requested` /
  `review_pass` (+0.2) / `review_fail` (-0.5) / `review_skipped`, with
  task/issue anchors + user note.
- `server/internal/service/agent_trust/` — `ApplyCorrection`, `ReviewTask`
  (LLM verdict via `CLIReviewer`), `ProcessTaskCompletion` gate
  (score < threshold → auto-review), atomic score upserts.
- HTTP: `GET /trust/profiles`, `GET /trust/events`,
  `POST /trust/{agentId}/correct`, `POST /trust/{agentId}/review` — all
  membership-gated.

### SkillOpt-style optimizer (migrations 229-231)

- `agent_self_opt_run` gains `deferred_reason` / `deferred_until` / `data_count`
  (data-sufficiency deferral: < 5 done issues AND < 3 trust events →
  `status='deferred'`, retry +24h).
- `agent_opt_edit` — the edit ledger. `application`
  `applied|suggested|rejected|ignored|reverted`, `validation_score`,
  `validation_reason`, `instructions_snapshot`, `applied_by`
  (`user|auto`, nullable), `corrected_task_id` (traceability anchor).
- `optimizer.go` — `OptimizeAgent` classifies each proposal by the design
  verdict: **delete/replace NEVER auto-apply** (by construction), add-only
  auto-apply requires score ≥ 90 + enrolled (`【self-opt:enroll】` marker) +
  trust ≥ 8 + not lab-managed/hard-blocked + correction-backed +
  rate-capped 1/run + snapshot/rollback. Score 60-89 → `suggested`.
  Score < 60 → `rejected`.
- **Post-hoc commit gate (d1)**: the NEXT run re-scores each applied edit
  against its snapshot via `RevalidateAppliedEdits`; a regressive edit
  (score < 60) is auto-reverted to its snapshot + recorded as `review_fail`
  trust event (-0.5).
- Runner: parallel per-agent optimization (sem=3), write-back only applied
  edits, `IncrementIssueCounter` before `CreateIssue` (fixes the number=0
  collision), `expireSuggestions` (21d → `ignored`, never `rejected`).

### Adversarial-review fixes (third round, 25-agent workflow)

| verdict | finding | fix |
|---|---|---|
| CONFIRMED (P0) | `applied_by NOT NULL CHECK` rejected the `''` the ledger wrote → **待确认建议 tier INSERT/UPDATE all failed** | `applied_by` nullable (`DEFAULT 'auto'` + `sqlc.narg` writes NULL); DB synced |
| CONFIRMED | c10: correction-backing was a window boolean, no code-level traceability | `corrected_task_id` column persisted + DTO-exposed; proposal prompt anchors the corrected task id |
| CONFIRMED | d6: a reverted edit could be re-proposed + re-auto-applied | `ListNegativeExperienceEdits` (rejected+reverted only) + `isRejected` hard-blocks `reverted` |
| CONFIRMED | d7: rejection-buffer prompt mislabeled ignored/applied as REJECTED | runner feeds the negative-experience query to the optimizer |
| CONFIRMED | d1: post-hoc revalidation missing | `RevalidateAppliedEdits` (above) |
| s2 residual | read/trigger surface lacked membership checks | `requireWorkspaceMember` on List/Get/Trigger/Cancel runs + List edits |
| d5 | `SuggestedOrderingCredit` dead const | wired as +5 ordering credit inside the suggested queue for correction-backed rows |
| 11 findings | REFUTED by adversarial verifiers (6 described pre-fix code) | no change |

## Verification

- `go build ./...` / `go vet` / gofmt clean; self-opt + trust + handler suites
  green (rejection-buffer test updated to the real `application` row shape).
- Full `go test ./...` — only pre-existing shared-DB/temp-dir flaky tests fail
  when run together (`TestQuickCreateIssueParentTrustBoundary`, llmwiki writer);
  both pass in isolation.
- `pnpm typecheck` 6/6 green.

## Version

`apps/desktop/package.json` 0.5.1 → 0.5.2 (canonical version source).
