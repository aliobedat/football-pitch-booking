-- Migration 008 (down) — reverse pitch soft delete + deletion audit trail.
--
--   psql "$DATABASE_URL" -f migrations/008_pitch_soft_delete_and_audit.down.sql
--
-- NOTE: dropping `deleted_at` resurrects any soft-deleted pitches, and dropping
-- pitch_audit_log discards the deletion audit trail. Run only intentionally.

DROP TABLE IF EXISTS pitch_audit_log;

DROP INDEX IF EXISTS idx_pitches_active;

ALTER TABLE pitches
    DROP COLUMN IF EXISTS deleted_at;
