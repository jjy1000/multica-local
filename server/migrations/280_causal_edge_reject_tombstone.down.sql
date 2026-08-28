-- 280_causal_edge_reject_tombstone.down.sql
-- Reverse of 280 up: purge tombstone rows (they are only valid under
-- the widened CHECK), then restore the two-state constraint.

DELETE FROM causal_edge WHERE status = 'rejected';

ALTER TABLE causal_edge DROP CONSTRAINT causal_edge_status_check;
ALTER TABLE causal_edge ADD CONSTRAINT causal_edge_status_check
    CHECK (status IN ('active', 'suggested'));
