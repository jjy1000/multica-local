-- 0.5.116 issue-deletion sync: pre-clean queries run by DeleteIssue /
-- BatchDeleteIssues BEFORE the cascade DELETE. mythos_run.root_issue_id /
-- final_issue_id, mythos_members.result_issue_id and swarm_run.root_issue_id
-- are bare FKs (no ON DELETE action), so an unreferenced delete would fail
-- with 23503 and surface as a generic 500. Mirror of FailAutopilotRunsByIssue
-- + the 0.5.112 pythia termination closure.

-- name: DetachMythosRunsByIssue :many
-- NULL the root link of every run bound to the issue; in-flight
-- (running/supervising) runs are also flipped to aborted so the supervise
-- panel and the stalled-run reaper see a terminal row immediately. The
-- supervise goroutine itself winds down on its next terminal detection or
-- the 24h lifetime cap (best-effort, same posture as abortPythiaRunsForIssue).
-- SET expressions see the OLD row, so the CASE branches read the pre-update
-- status. Run rows survive as history with root_issue_id NULL.
UPDATE mythos_run
SET root_issue_id = NULL,
    status = CASE WHEN status IN ('running', 'supervising')
                  THEN 'aborted' ELSE status END,
    completed_at = CASE WHEN status IN ('running', 'supervising')
                        THEN now() ELSE completed_at END,
    supervision_state = CASE
        WHEN status IN ('running', 'supervising')
        THEN COALESCE(supervision_state, '{}'::jsonb) ||
             '{"phase":"aborted","abort_reason":"linked issue was deleted"}'::jsonb
        ELSE supervision_state
    END
WHERE root_issue_id = $1
RETURNING id;

-- name: DetachMythosRunFinalIssue :exec
-- The coda/result issue can be deleted independently of the root issue;
-- NULL the link so the delete is not blocked. The run row keeps its history.
UPDATE mythos_run SET final_issue_id = NULL WHERE final_issue_id = $1;

-- name: DetachMythosMemberResultIssue :exec
UPDATE mythos_members SET result_issue_id = NULL WHERE result_issue_id = $1;

-- name: DeleteSwarmRunsByRootIssue :exec
-- swarm_topology is retired (0.5.105): the remaining rows are terminal
-- tombstones and root_issue_id is NOT NULL (mig 241), so the pre-clean
-- deletes them instead of detaching — a tombstone view for a deleted
-- issue is moot. No writer path exists to recreate rows.
DELETE FROM swarm_run WHERE root_issue_id = $1;
