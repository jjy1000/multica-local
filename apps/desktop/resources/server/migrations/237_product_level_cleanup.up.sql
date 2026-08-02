-- 0.5.6: clean up `experimental_resource_lock` and
-- `experimental_resource_visibility` rows that referenced the
-- product-level flags `agent_creation_studio` and
-- `agent_self_optimization`. The two flags are no longer in the
-- catalog (catalog.go removed the Flag literals in 0.5.6), so the
-- corresponding lock / visibility rows are now dead data:
-- `experimental.DefaultFor` would return false for either key,
-- the install handlers are deleted, and the boot wire
-- (`boot_provision_product_labs.go`) does not claim locks.
--
-- Forward-only cleanup; the down migration is a no-op because the
-- rows were idempotent inserts and the catalog literal that would
-- have referenced them is gone (down would re-insert dangling rows
-- pointing at a flag the server can no longer resolve).
--
-- Why the cleanup is safe: the leader agents (`agent_creation_expert`,
-- 智能体优化专家) and the 2 self-opt autopilots are product-level
-- rows in `agent` / `autopilot` — they survive this migration. The
-- `lock` table's only consumer is `experimental.Claim` /
-- `experimental.Release` (per the 0.3.22 reserved-workspace removal
-- contract), and those calls are no longer issued for the two
-- removed flags. The `visibility` table's only consumer is
-- `filterLabsHiddenByDefault` (per the 0.3.20 hideable-resource
-- contract), and that filter is no longer queried for the two
-- removed flags. 0.5.5.2's UI plumbing (`HIDDEN_LAB_KEYS` /
-- `PRODUCT_LEVEL_LAB_KEYS`) is the client-side mirror of this
-- server-side cleanup.

DELETE FROM experimental_resource_lock
WHERE experimental_source IN ('agent_creation_studio', 'agent_self_optimization');

DELETE FROM experimental_resource_visibility
WHERE flag_key IN ('agent_creation_studio', 'agent_self_optimization');
