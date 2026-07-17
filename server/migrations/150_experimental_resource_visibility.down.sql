-- experimental_resource_visibility: forward-only table. The down
-- migration removes the table entirely; any active flag in production
-- that referenced it must be flipped off first, or the Labs UI will
-- fall back to the catalog default (false) and hide the resources
-- regardless.
DROP INDEX IF EXISTS idx_experimental_resource_visibility_lookup;
DROP TABLE IF EXISTS experimental_resource_visibility;
