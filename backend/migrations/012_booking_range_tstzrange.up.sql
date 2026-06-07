-- ================================================================
-- Migration 012 (UP) — booking_range: tsrange → tstzrange
--
-- WHY: booking_range previously stored UTC wall-clock instants in a tsrange
-- (timestamp WITHOUT time zone). That works only as long as every reader and
-- writer remembers the "values are UTC" convention; a single comparison made in
-- a non-UTC session silently shifts the slot. Converting to tstzrange makes the
-- instant unambiguous at the type level — the column now stores true points in
-- time, so the anti-double-booking guard can never be defeated by a timezone
-- mismatch.
--
-- The column is governed by the EXCLUDE USING GIST constraint
-- `bookings_pitch_id_booking_range_excl` — the ONLY double-booking protection.
-- The GIST operator class is type-specific, so the constraint must be dropped
-- before the type change and recreated afterwards with an IDENTICAL predicate
-- and operators over tstzrange. btree_gist (needed for `pitch_id WITH =`) is
-- already installed by migration 003.
--
-- The stored values are UTC wall-clock instants (confirmed in the booking write
-- path: CreateBooking inserts tsrange($start::timestamp, $end::timestamp, '[)')
-- from time.Time values normalised with .UTC()), so the conversion reinterprets
-- each bound AT TIME ZONE 'UTC' to recover the correct absolute instant. The
-- existing bound inclusivity is [) and is preserved.
--
-- Transaction-safe (run as one unit). NOT re-entrant — run once forward, use the
-- paired .down.sql to reverse.
-- ================================================================

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_pitch_id_booking_range_excl;

ALTER TABLE bookings
    ALTER COLUMN booking_range TYPE tstzrange
    USING tstzrange(
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
