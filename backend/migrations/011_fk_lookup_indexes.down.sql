-- ================================================================
-- Migration 011 (DOWN) — drop the FK lookup indexes.
-- ================================================================

DROP INDEX IF EXISTS idx_pitches_owner_id;
DROP INDEX IF EXISTS idx_bookings_player_id;
