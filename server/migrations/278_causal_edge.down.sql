-- 278_causal_edge.down.sql
-- 0.5.83 WL3 down-migration (dev / fresh-test-DB rollback only).
-- Forward-only production rule: never run down.sql on a live DB.

DROP INDEX IF EXISTS uq_causal_edge_active_from_to_type;
DROP TABLE IF EXISTS causal_edge;
