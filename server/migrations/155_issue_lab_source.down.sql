DROP INDEX IF EXISTS idx_issue_lab_source;
ALTER TABLE issue DROP COLUMN IF EXISTS lab_source;
