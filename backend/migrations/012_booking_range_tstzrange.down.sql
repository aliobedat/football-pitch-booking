-- ================================================================
-- Migration 012 (DOWN) — booking_range: tstzrange → tsrange
--
-- Reverses 012_up. Drops the exclusion constraint, converts the column back to
-- tsrange (storing the UTC wall-clock instant as a naive timestamp, the original
-- convention), then recreates the identical EXCLUDE USING GIST guard.
-- ================================================================

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_pitch_id_booking_range_excl;

ALTER TABLE bookings
    ALTER COLUMN booking_range TYPE tsrange
    USING tsrange(
        lower(booking_range) AT TIME ZONE 'UTC',
        upper(booking_range) AT TIME ZONE 'UTC',
        '[)'
    );

ALTER TABLE bookings
    ADD CONSTRAINT bookings_pitch_id_booking_range_excl
    EXCLUDE USING GIST (
        pitch_id      WITH =,
        booking_range WITH &&
    ) WHERE (status <> 'cancelled');
