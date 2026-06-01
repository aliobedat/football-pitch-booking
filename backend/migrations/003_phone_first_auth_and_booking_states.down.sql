-- ================================================================
-- Migration 003 (DOWN) — reverse phone-first identity + booking states
--
-- Reverses 003_..._up.sql in the opposite order of creation. The
-- booking_status enum is rebuilt back to the original three values; any
-- rows using the new lifecycle states are collapsed to the nearest original
-- value (rejected/no_show -> cancelled, completed -> confirmed). This is a
-- lossy reversal by necessity — Postgres cannot drop enum values.
-- ================================================================


-- ────────────────────────────────────────────────────────────────
-- 5. status_transitions — drop first; it references booking_status, which
--    is rebuilt in step 1 below.
-- ────────────────────────────────────────────────────────────────
DROP TABLE IF EXISTS status_transitions;


-- ────────────────────────────────────────────────────────────────
-- 4. bookings — remove the dormant payment seam column.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE bookings DROP COLUMN IF EXISTS payment_status;


-- ────────────────────────────────────────────────────────────────
-- 3. users — restore email-first shape.
--    NOTE: SET NOT NULL on email will fail if any row has a NULL email by
--    the time this runs (expected — that data only exists after phone-first
--    rows are created). Backfill email before reversing if that occurs.
-- ────────────────────────────────────────────────────────────────
DROP INDEX IF EXISTS idx_users_phone_unique;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_phone_e164_chk;
ALTER TABLE users DROP COLUMN IF EXISTS opt_in;
ALTER TABLE users DROP COLUMN IF EXISTS phone_verified;
ALTER TABLE users ALTER COLUMN email SET NOT NULL;


-- ────────────────────────────────────────────────────────────────
-- 2. payment_status — drop the reserved type.
-- ────────────────────────────────────────────────────────────────
DROP TYPE IF EXISTS payment_status;


-- ────────────────────────────────────────────────────────────────
-- 1. booking_status — rebuild back to pending/confirmed/cancelled.
--    Collapse the new states onto the original set BEFORE the type swap,
--    then drop/restore the default and EXCLUDE constraint as in the up step.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_pitch_id_booking_range_excl;
ALTER TABLE bookings ALTER COLUMN status DROP DEFAULT;

UPDATE bookings SET status = 'cancelled' WHERE status IN ('rejected', 'no_show');
UPDATE bookings SET status = 'confirmed' WHERE status = 'completed';

ALTER TYPE booking_status RENAME TO booking_status_ext;

CREATE TYPE booking_status AS ENUM ('pending', 'confirmed', 'cancelled');

ALTER TABLE bookings
    ALTER COLUMN status TYPE booking_status
    USING status::text::booking_status;

ALTER TABLE bookings ALTER COLUMN status SET DEFAULT 'pending';

DROP TYPE booking_status_ext;

ALTER TABLE bookings
    ADD CONSTRAINT bookings_pitch_id_booking_range_excl
    EXCLUDE USING GIST (
        pitch_id      WITH =,
        booking_range WITH &&
    ) WHERE (status <> 'cancelled');
