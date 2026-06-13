-- Migration 015 (DOWN) — reverse operating hours + audit detail payload
--
--   psql "$DATABASE_URL" -f migrations/015_operating_hours.down.sql
--
-- Drops the operating_hours table and the pitch_audit_log.detail column. Dropping
-- detail discards any captured schedule snapshots; the action rows themselves are
-- retained.

DROP INDEX IF EXISTS idx_operating_hours_pitch_weekday;
DROP TABLE IF EXISTS operating_hours;

ALTER TABLE pitch_audit_log
    DROP COLUMN IF EXISTS detail;
