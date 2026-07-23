ALTER TABLE issue_subscriber DROP CONSTRAINT issue_subscriber_user_type_check;
ALTER TABLE issue_subscriber ADD CONSTRAINT issue_subscriber_user_type_check
    CHECK (user_type IN ('member', 'agent'));

ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_recipient_type_check;
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_recipient_type_check
    CHECK (recipient_type IN ('member', 'agent'));

ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_accountable_matches_originator;
ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_accountable_matches_originator
    CHECK (((originator_user_id IS NULL) OR ((accountable_user_id IS NOT NULL) AND (accountable_user_id = originator_user_id))));