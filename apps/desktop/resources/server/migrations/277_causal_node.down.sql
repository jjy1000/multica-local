-- 277_causal_node.down.sql
-- 0.5.83 WL3 down-migration (dev / fresh-test-DB rollback only).
-- Forward-only production rule: never run down.sql on a live DB.

DROP TABLE IF EXISTS causal_node;
