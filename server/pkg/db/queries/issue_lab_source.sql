-- Issue lab_source read for claim-time skill scoping (0.5.89). A dedicated
-- narrow query (not one of the explicit-column-list issue queries) so the
-- claim hot path never drags the full issue row just to decide whether a
-- lab_scoped plugin's skills apply.

-- name: GetIssueLabSource :one
SELECT lab_source FROM issue WHERE id = $1;
