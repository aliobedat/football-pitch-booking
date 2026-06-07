-- Migration 013 (DOWN) — reverse the Native Verified Review System.
-- Drop in reverse dependency order: the reviews FK depends on uq_booking_triple,
-- so the table (and its indexes) must go before the bookings constraint.

BEGIN;

DROP INDEX IF EXISTS idx_bookings_player_pitch;
DROP INDEX IF EXISTS idx_reviews_pitch_id;
DROP INDEX IF EXISTS uq_review_player_pitch;

DROP TABLE IF EXISTS reviews;

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS uq_booking_triple;

COMMIT;
