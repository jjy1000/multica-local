-- 0.3.55: drop the pythia_forecast_run table (dev / rollback only).
-- Forward-only production rule: never run down.sql on a live DB. This
-- file exists so `migrate down` works in a fresh test database.

DROP TABLE IF EXISTS pythia_forecast_run;
