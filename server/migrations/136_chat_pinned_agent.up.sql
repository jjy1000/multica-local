-- 136_chat_pinned_agent (MUL-4351)
-- ----------------------------------------------------------------------------
-- Per-user "quick agent" pins for the Chat list: a curated, ordered set of
-- agents the user keeps at the top of the conversation list for one-tap new
-- chats. Kept separate from the generic `pinned_item` table (issues/projects)
-- so it does not leak into the sidebar's pinned section.
--
-- Renumbered from upstream 152 to 136.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS chat_pinned_agent (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    user_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    position FLOAT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id, agent_id)
);

-- Application layer owns these references (repo's no-FK rule). Drop any
-- constraint the runtime might have added by accident during a prior apply.
ALTER TABLE chat_pinned_agent
    DROP CONSTRAINT IF EXISTS chat_pinned_agent_workspace_id_fkey,
    DROP CONSTRAINT IF EXISTS chat_pinned_agent_user_id_fkey,
    DROP CONSTRAINT IF EXISTS chat_pinned_agent_agent_id_fkey;