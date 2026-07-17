-- 155: Add lab_source column to issue table.
-- Allows associating an issue with an experimental lab (e.g. claude_science_lab,
-- mythos_swarm). NULL means the issue is not associated with any lab.
ALTER TABLE issue ADD COLUMN lab_source TEXT;

-- Partial index for fast lookups when filtering issues by lab.
CREATE INDEX idx_issue_lab_source ON issue (lab_source) WHERE lab_source IS NOT NULL;
