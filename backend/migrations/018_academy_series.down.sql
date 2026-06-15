-- Migration 018 (DOWN) — reverse academy recurring bookings
-- ─────────────────────────────────────────────────────────────────────────────
-- WARNING: restoring 017's one-directional CHECK and dropping booking_series will
-- FAIL if any academy child rows (source='academy') or series rows still exist.
-- Delete or convert them first.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 3. Drop the child link (FK + index + column).
DROP INDEX IF EXISTS idx_bookings_series;
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_series_pitch_fkey;
ALTER TABLE bookings DROP COLUMN IF EXISTS series_id;

-- 2. Drop the parent table.
DROP TABLE IF EXISTS booking_series;

-- 1. Restore 017's one-directional relationship CHECK.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_source_player_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_nonplayer_null_player_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_nonplayer_null_player_chk
            CHECK (source NOT IN ('block', 'manual') OR player_id IS NULL);
    END IF;
END $$;

COMMIT;
