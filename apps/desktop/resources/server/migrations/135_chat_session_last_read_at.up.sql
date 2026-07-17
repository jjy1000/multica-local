-- 135_chat_session_last_read_at (MUL-4351 prep)
-- ----------------------------------------------------------------------------
-- IM-style unread count: add a read cursor `last_read_at` next to the existing
-- `unread_since` flag. Unread is now the count of assistant messages strictly
-- after the cursor, so the Chat list shows a real number instead of a single
-- dot. The legacy `unread_since` flag is kept for rolling deploy / rollback
-- compatibility; new code reads `last_read_at` and treats `unread_since` as a
-- deprecated alias.
--
-- Renumbered from upstream 151 to 135 in the local fork per
-- `.omc/plan-0.3.3-upstream-integration.md`.
-- ----------------------------------------------------------------------------
ALTER TABLE chat_session ADD COLUMN IF NOT EXISTS last_read_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Preserve existing unread state during the switch: a session currently flagged
-- unread gets its cursor placed just before the first unread reply, so those
-- messages still count. `unread_since` is the arrival time of the first unread
-- assistant message.
UPDATE chat_session
   SET last_read_at = unread_since - interval '1 microsecond'
 WHERE unread_since IS NOT NULL;