-- 0.3.19 P9: code_canvas internal pilot marker.
-- The marker row is inserted by application code at boot via
-- server/internal/experimental/lock.go::LifecycleMarker("code_canvas").
-- The raw SHA-256 expression approach was invalid — PG's sha256() returns
-- bytea, but experimental_resource_lock.resource_id is uuid, and PG does not
-- implicitly coerce bytea→uuid. The migration is intentionally a no-op;
-- the marker lives in the catalog + application code, not in SQL.
--
-- Forward-only. Companion .down.sql is also a no-op.
SELECT 1;
