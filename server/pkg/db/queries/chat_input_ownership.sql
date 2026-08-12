-- 145_chat_input_ownership_queries
-- ----------------------------------------------------------------------------
-- New sqlc queries for MUL-4351 (chat input batch ownership) + 0.3.4 feature
-- wiring. Each query lives in a separate file so sqlc regen does not touch
-- chat.sql and risk disturbing existing handler call sites.
--
-- Migration 142 already added agent_task_queue.chat_input_task_id (UUID,
-- nullable). These queries let the chat claim path consume that field.
-- ----------------------------------------------------------------------------

-- name: CreateChatTaskWithInputOwner :one
-- Creates a chat task bound to its own chat_input_task_id — the same value
-- as the task's primary id. A fresh direct-send task sets this to its own id
-- in the same transaction that creates the user message
-- (chat_message.task_id = task.id), so a later claim loads exactly that
-- batch instead of scanning "trailing user messages after the last assistant
-- row". NULL chat_input_task_id means "legacy row or channel (Slack/Lark)
-- task": those keep using the trailing-message selector, so a rolling deploy
-- never replays channel history.
INSERT INTO agent_task_queue (
    id, agent_id, runtime_id, issue_id, status, priority, chat_session_id,
    initiator_user_id, force_fresh_session, chat_input_task_id
)
VALUES (
    $1, $2, $3, NULL, 'queued', $4, $5,
    $6,
    COALESCE(sqlc.narg('force_fresh_session')::boolean, FALSE),
    $7
)
RETURNING *;

-- name: ListChatInputMessages :many
-- Loads the user-message batch a direct-chat task is supposed to consume.
-- Keyed on chat_input_task_id (which equals task.id for fresh direct sends),
-- filtered to role='user', ordered by created_at. The 144 partial index
-- idx_chat_message_input_owner makes this an index-only scan.
SELECT * FROM chat_message
WHERE task_id = $1 AND role = 'user'
ORDER BY created_at ASC;

-- name: MarkChatMessageNoResponse :exec
-- Marks a chat_message row as message_kind='no_response' — the visible
-- "agent completed a turn without any text reply" terminal outcome. The
-- turn boundary is owned by chat_input_task_id, not by the presence of an
-- assistant row, so we no longer fake a blank bubble. Idempotent.
UPDATE chat_message
SET message_kind = 'no_response'
WHERE id = $1 AND role = 'assistant';

-- name: TouchChatSessionLastRead :exec
-- Updates last_read_at to now(). Called from the frontend's "mark as read"
-- path so the IM-style unread count derives against the new cursor.
UPDATE chat_session SET last_read_at = now()
WHERE id = $1;

-- name: ListChatSessionsByCreatorWithUnreadCount :many
-- Same shape as ListChatSessionsByCreator but replaces the boolean has_unread
-- flag with an integer unread_count — IM-style count of assistant messages
-- strictly after last_read_at. The frontend can render a number instead of
-- a dot. If last_read_at IS NULL (cold start before any read), fall back to
-- the legacy unread_since boundary.
--
-- Sort contract (chat_pin_ui, migration 139+140): pinned rows first
-- (`pinned_at IS NOT NULL`), then by pinned_at DESC, then by updated_at
-- DESC. Mirrors ListChatSessionsByCreator in chat.sql so the mobile IM-style
-- count variant and the web/desktop live path agree on the sort.
SELECT cs.*,
       (
         SELECT COUNT(*)
         FROM chat_message m
         WHERE m.chat_session_id = cs.id
           AND m.role = 'assistant'
           AND m.created_at > COALESCE(cs.last_read_at, cs.unread_since, '1970-01-01 00:00:00+00'::timestamptz)
       )::int AS unread_count
FROM chat_session cs
WHERE cs.workspace_id = $1 AND cs.creator_id = $2 AND cs.status = 'active'
ORDER BY (cs.pinned_at IS NULL) ASC, cs.pinned_at DESC, cs.updated_at DESC;

-- name: PinChatSession :exec
-- Toggles chat_session.pinned_at to mark a session as user-pinned.
UPDATE chat_session SET pinned_at = now(), updated_at = now()
WHERE id = $1;

-- name: UnpinChatSession :exec
UPDATE chat_session SET pinned_at = NULL, updated_at = now()
WHERE id = $1;

-- name: SetChatSessionAgentIntro :exec
-- Sets chat_session.is_agent_intro = TRUE so the frontend renders the
-- self-introduction header. Used by the daemon at agent-create time.
UPDATE chat_session SET is_agent_intro = TRUE
WHERE id = $1;