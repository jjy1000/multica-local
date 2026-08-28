-- 276_issue_dependency_revive.up.sql
-- 0.5.83 WL3 (issue causal graph): revive the dormant issue_dependency
-- table. The base table ships in 001_init.up.sql (id / issue_id /
-- depends_on_issue_id / type CHECK ('blocks','blocked_by','related'))
-- but has had zero writers since. This migration is ADDITIVE ONLY per
-- the forward-only law: four new columns, no existing column is
-- altered or dropped.
--
--   evidence_comment_id — optional provenance link to the comment that
--     justified the dependency. ON DELETE SET NULL so deleting a
--     comment never orphans / cascades the dependency edge.
--   created_by          — who authored the edge ('user' | 'agent' |
--     'system'), DEFAULT 'user' because every historical row was
--     user-authored through the UI.
--   created_at / updated_at — standard audit columns. DEFAULT now()
--     backfills existing rows at migration time (PostgreSQL 11+
--     ADD COLUMN .. DEFAULT semantics).
--
-- The CHECK is added as a NAMED constraint so the down migration can
-- drop it deterministically (auto-generated names are stable for
-- column-inline CHECKs, but naming it makes the contract explicit and
-- pins the name in the migration-276..279 static test).

ALTER TABLE issue_dependency
    ADD COLUMN evidence_comment_id UUID NULL REFERENCES comment(id) ON DELETE SET NULL,
    ADD COLUMN created_by TEXT NULL DEFAULT 'user',
    ADD COLUMN created_at TIMESTAMPTZ NULL DEFAULT now(),
    ADD COLUMN updated_at TIMESTAMPTZ NULL DEFAULT now();

ALTER TABLE issue_dependency
    ADD CONSTRAINT issue_dependency_created_by_check
    CHECK (created_by IN ('user', 'agent', 'system'));
