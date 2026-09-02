-- Squad-as-subscriber / squad-as-inbox-recipient (0.3.61 fix).
--
-- Background. The fork observed three SQLSTATE 23514 failures from
-- squad-assigned issues running the 0.3.60 server:
--
--   * `issue_subscriber_user_type_check` rejected `user_type='squad'`
--     when EventIssueCreated fired with `assignee_type='squad'` —
--     subscriber_listeners.go:36,61 writes `*issue.AssigneeType` straight
--     into the subscriber row, and squad is a legal assignee but not a
--     legal subscriber. The CHECK was invented when only member/agent
--     could be subscribers.
--   * `inbox_item_recipient_type_check` rejected `recipient_type='squad'`
--     from the same path — notification_listeners.go:564,618,630,836,876
--     hands `*issue.AssigneeType` to `notifyDirect`. Members and agents
--     get notifications; squads were never expected to. Schema never
--     followed when squad assignees shipped (0.3.31).
--   * `agent_task_queue_accountable_matches_originator` rejected rows
--     where `originator_user_id` was set (member commented on a squad
--     leader task) but `accountable_user_id` was NULL. The original
--     CHECK forced `accountable = originator` whenever originator was
--     set, but no Go code path ever writes `accountable_user_id` — the
--     column is historical. Squad leader briefing therefore can't merge
--     a member-authored comment into its queued task.
--
-- All three were reproduced verbatim against
-- `/Users/jiangjianyan/Downloads/multica-main` (the upstream source the
-- fork tracks); this is upstream latent, not a fork regression.
--
-- Fix: extend both subscriber/recipient CHECK enums to include 'squad';
-- relax the accountable/originator CHECK to "if both are set, they must
-- match — but either is allowed to be NULL on its own".
--
-- The 'squad' enum widening is intentional — the Multica app treats
-- squads as a first-class subscriber/recipient since 0.3.31. The CHECK
-- was just left behind.

ALTER TABLE issue_subscriber DROP CONSTRAINT issue_subscriber_user_type_check;
ALTER TABLE issue_subscriber ADD CONSTRAINT issue_subscriber_user_type_check
    CHECK (user_type IN ('member', 'agent', 'squad'));

ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_recipient_type_check;
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_recipient_type_check
    CHECK (recipient_type IN ('member', 'agent', 'squad'));

-- 2026-09-02 fresh-install fix: this file's agent_task_queue block ran
-- BEFORE the columns it references exist (originator_user_id /
-- accountable_user_id are first created by 240), so on an empty database
-- the plain DROP raised 42704 and every fresh install / empty-restore
-- died here. Existing databases recorded this version long ago and skip
-- it (schema_migrations tracks version only, no checksum), so editing in
-- place is invisible to them. The relaxed CHECK itself now lands in 288,
-- which runs after 240 on fresh databases; the gated block below keeps
-- this file's statement of intent true for any lineage that somehow has
-- the columns at this point.
ALTER TABLE agent_task_queue DROP CONSTRAINT IF EXISTS agent_task_queue_accountable_matches_originator;
DO $$
BEGIN
    IF (SELECT count(*) FROM information_schema.columns
        WHERE table_name = 'agent_task_queue'
          AND column_name IN ('originator_user_id', 'accountable_user_id')) = 2 THEN
        ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_accountable_matches_originator
            CHECK (
                (originator_user_id IS NULL)
                OR (accountable_user_id IS NULL)
                OR (accountable_user_id = originator_user_id)
            );
    END IF;
END $$;