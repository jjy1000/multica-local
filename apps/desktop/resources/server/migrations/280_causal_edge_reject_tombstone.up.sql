-- 280_causal_edge_reject_tombstone.up.sql
-- 0.5.83 WL3 S2: rejection becomes a TOMBSTONE, not a hard delete.
--
-- The nightly evolver + curator scan re-propose tier-D edges from the
-- graph's current state; a hard-deleted rejection leaves no memory, so
-- a rejected proposal would resurface on the next pass (violates the
-- ICP-5 never-nag contract). With a 'rejected' status the row stays as
-- an audit trail AND a dedup anchor: proposers probe for an ANY-status
-- edge between the pair (FindCausalEdgeBetween) and stay silent.
--
-- Same drop + re-add widening shape as migs 149 / 154 / 160 / 163 /
-- 242 / 275 / 279: the constraint only validates inserts/updates, so
-- existing rows are unaffected. The partial unique index
-- (from, to, type) WHERE status='active' (mig 278) is untouched — a
-- rejected tombstone never blocks a fresh user-authored active edge.
--
-- Forward-only per CLAUDE.md: no tables/columns dropped, values only
-- widen.

ALTER TABLE causal_edge DROP CONSTRAINT causal_edge_status_check;
ALTER TABLE causal_edge ADD CONSTRAINT causal_edge_status_check
    CHECK (status IN ('active', 'suggested', 'rejected'));
