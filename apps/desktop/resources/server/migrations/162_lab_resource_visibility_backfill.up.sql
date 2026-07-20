-- 162_lab_resource_visibility_backfill (0.3.53)
-- ----------------------------------------------------------------------------
-- Closes a real bug: lab-owned agents / squads / skills / workspaces were
-- showing up in the user-facing AssigneePicker / agent picker because the
-- experimental_resource_lock rows attached to every lab source were in
-- state `hidden=false`.
--
-- Root cause: the `Hide()` call at the end of every lab install path
-- (install_claude_science.go:242, install_mythos.go:??) sets
-- `hidden=true` on every lock row attached to the source. But a sequence
-- of historical toggle events (off + on via the UI, Restore calls from
-- manual debugging, restoration via the safety-net burst breaker) left
-- every row in `hidden=false` without an immediately-following Hide() run.
-- The install path's Hide() only fires on a fresh install; existing
-- installs that toggled off+on afterwards did not always re-hide, and a
-- typo in an early version of the safety-net recovery path silently
-- flipped the lock rows back to visible without invoking Hide() again.
--
-- Effect: ListVisibleAgentsByWorkspace (server/pkg/db/queries/agent.sql)
-- filters with `WHERE NOT EXISTS (... hidden = true)`. With all rows
-- `hidden=false`, every lab agent (Claude Science 生物/物理/ml/research/write,
-- mythos_prelude / loop_coder / loop_researcher / loop_analyst / coda,
-- agent_self_optimization, constitution_agent) was visible in the picker.
-- Per CLAUDE.md "Lab ↔ Assignee Mutex" + 0.3.31 contract, lab agents must
-- NEVER appear in the user-facing AssigneePicker — only in their own
-- lab's /experimental/<suffix> panel.
--
-- Fix: this migration flips every `hidden=false` lab lock row to
-- `hidden=true`, restoring the design invariant. Forward-only: rows that
-- were deliberately left `hidden=false` for the lifecycle marker
-- (LockWorkspace + LifecycleMarker) are explicitly excluded — those are
-- the install-status markers, not user-pickable resources.
--
-- Idempotent: re-running is a no-op because the WHERE clause matches 0
-- rows on the second run.
-- ----------------------------------------------------------------------------
UPDATE experimental_resource_lock
SET hidden = true,
    hidden_at = COALESCE(hidden_at, now())
WHERE hidden = false
  AND resource_type IN ('agent', 'skill', 'squad', 'member', 'mcp_server')
  AND experimental_source IN (
    'claude_science',
    'claude_science_lab',
    'claude_science_runtime',
    'mythos_swarm',
    'agent_self_optimization',
    'constitution_agent'
  );