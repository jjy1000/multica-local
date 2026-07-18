-- 0.3.45.1: drop the agent_self_opt_run table (dev / rollback only).
-- Forward-only production rule: never run down.sql on a live DB. This
-- file exists so `migrate down` works in a fresh test database.

DROP TABLE IF EXISTS agent_self_opt_run;