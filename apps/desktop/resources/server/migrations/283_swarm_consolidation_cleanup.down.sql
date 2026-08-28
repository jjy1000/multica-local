-- 283_swarm_consolidation_cleanup.down.sql
-- Data migration — no reverse. The failed zombie runs and archived
-- orphan agents stay terminal (forward-only law; the rows they close
-- were already non-functional).
SELECT 1;
