-- 282_lab_report_writeback.down.sql
ALTER TABLE pythia_forecast_run
    DROP COLUMN IF EXISTS report_comment_id;

ALTER TABLE timesfm_forecast_run
    DROP COLUMN IF EXISTS report_comment_id;
