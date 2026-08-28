-- 281_causal_graph_maintenance_state.down.sql
-- Reverses 281 up: drop the maintenance anchor singleton table.
DROP TABLE IF EXISTS causal_graph_maintenance_state;
