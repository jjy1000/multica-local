-- Project start_date / due_date — 0.3.44 integration of MUL-4513 calendar-day fields.
--
-- Forward-only: ADD COLUMN IF NOT EXISTS so re-applying is a no-op. No DROP.
-- Both columns are nullable DATE: NULL means "no deadline" (the current behavior
-- for projects that never had a date set). The lifecycle contract is:
--
--   - CREATE with start_date / due_date populated echoes them back.
--   - GET returns the persisted values; nullable means the client renders
--     "no deadline" / "no start" affordances.
--   - UPDATE with the key present but the value as "" clears the date
--     (handler sets pgtype.Date{Valid:false}); absent key leaves it untouched
--     (same semantics as the existing description / icon / lead_* fields).
--
-- The handler enforces the lifecycle; the DB just stores the values. We do NOT
-- add a CHECK that due_date >= start_date because the legitimate use case
-- "I planned a deadline before I knew the start" still wants to record it
-- (MUL-4513 follow-up kept this as a UI-layer warning rather than a hard rule).

ALTER TABLE project ADD COLUMN IF NOT EXISTS start_date DATE;
ALTER TABLE project ADD COLUMN IF NOT EXISTS due_date DATE;

-- Indexes for the "projects due this week / overdue" UI surfaces. Both are
-- partial: only rows with a date get indexed, keeping the index small and
-- the planner happy on workspaces with hundreds of unscheduled projects.
CREATE INDEX IF NOT EXISTS idx_project_workspace_due_date
    ON project (workspace_id, due_date)
    WHERE due_date IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_project_workspace_start_date
    ON project (workspace_id, start_date)
    WHERE start_date IS NOT NULL;