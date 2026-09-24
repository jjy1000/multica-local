-- Forward-only companion (never applied by the app; migrate down support).
DROP INDEX IF EXISTS idx_causal_reads_issue_time;
DROP TABLE IF EXISTS causal_graph_read_receipt;
