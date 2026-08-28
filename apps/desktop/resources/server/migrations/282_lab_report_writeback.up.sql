-- 282_lab_report_writeback.up.sql
-- 0.5.86 issue-delivery batch: lab run reports land IN the issue.
--
-- Problem (0.5.83 post-ship audit, user report 2026-08-28): Pythia and
-- TimesFM forecast runs persisted their rows (pythia_forecast_run /
-- timesfm_forecast_run) but the text report lived ONLY in the lab view
-- (/experimental/pythia, /experimental/timesfm-lab). The user had no
-- issue-first deliverable.
--
-- Fix: the run handlers now post the report as the lab leader agent's
-- comment (AuthorType='agent') on the bound issue and record the
-- comment id here. The column is the idempotency marker — a run whose
-- report_comment_id is already set is never commented again, so
-- retries / handler re-entry cannot duplicate reports.
--
-- Forward-only additive: two nullable uuid columns, no existing data
-- touched.

ALTER TABLE pythia_forecast_run
    ADD COLUMN IF NOT EXISTS report_comment_id UUID NULL;

ALTER TABLE timesfm_forecast_run
    ADD COLUMN IF NOT EXISTS report_comment_id UUID NULL;
