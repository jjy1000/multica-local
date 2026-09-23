-- 290_pythia_forecast_continuity.down.sql
-- Forward-only law: the columns added in the paired .up.sql stay. This
-- down migration is a no-op placeholder (the migrate tool requires a
-- symmetric file pair), kept empty because reversing an additive
-- migration on live user data is exactly what the data-safety contract
-- forbids.
SELECT 1;
