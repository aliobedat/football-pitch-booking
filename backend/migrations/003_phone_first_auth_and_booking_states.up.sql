-- ================================================================
-- Migration 003 (UP) — Phone-first identity + full booking state machine
--
-- Establishes the foundational schema for PART 1:
--   * Phone becomes the primary identifier (E.164), email becomes optional.
--   * booking_status gains the full lifecycle (rejected/completed/no_show).
--   * A DORMANT payment_status seam is added (RESERVED — no logic reads it).
--   * status_transitions audits every booking state change.
--
-- Run order matters: the booking_status type is rebuilt BEFORE the
-- status_transitions table is created (that table references the enum).
--
-- This migration is transaction-safe: it rebuilds the enum via a type swap
-- rather than ALTER TYPE ... ADD VALUE (which cannot run inside a transaction
-- block). It is NOT re-entrant — run it exactly once forward, then use the
-- paired .down.sql to reverse.
-- ================================================================

CREATE EXTENSION IF NOT EXISTS btree_gist;


-- ────────────────────────────────────────────────────────────────
-- 1. booking_status — extend pending/confirmed/cancelled to the full
--    state machine: pending -> confirmed | rejected;
--                    confirmed -> completed | cancelled | no_show.
--
--    Postgres cannot add enum values transactionally and cannot drop them
--    at all, so we rebuild the type by swapping. The bookings.status default
--    and the anti-double-booking EXCLUDE constraint reference this column, so
--    both are dropped around the swap and restored afterwards.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_pitch_id_booking_range_excl;
ALTER TABLE bookings ALTER COLUMN status DROP DEFAULT;

ALTER TYPE booking_status RENAME TO booking_status_old;

CREATE TYPE booking_status AS ENUM (
    'pending',
    'confirmed',
    'rejected',
    'completed',
    'cancelled',
    'no_show'
);

ALTER TABLE bookings
    ALTER COLUMN status TYPE booking_status
    USING status::text::booking_status;

ALTER TABLE bookings ALTER COLUMN status SET DEFAULT 'pending';

DROP TYPE booking_status_old;

ALTER TABLE bookings
    ADD CONSTRAINT bookings_pitch_id_booking_range_excl
    EXCLUDE USING GIST (
        pitch_id      WITH =,
        booking_range WITH &&
    ) WHERE (status <> 'cancelled');


-- ────────────────────────────────────────────────────────────────
-- 2. payment_status — DORMANT RESERVED SEAM.
--    Single value 'unpaid'. There is no payment system yet: NO code
--    reads or acts on this column. deposit/paid/refunded values and any
--    payment behaviour are deferred until a payment system exists.
-- ────────────────────────────────────────────────────────────────
CREATE TYPE payment_status AS ENUM ('unpaid');


-- ────────────────────────────────────────────────────────────────
-- 3. users — phone-first identity.
--    * phone (E.164) becomes the primary login identifier.
--    * email becomes OPTIONAL: drop NOT NULL but KEEP UNIQUE
--      (Postgres allows multiple NULLs under a UNIQUE constraint).
--    * phone_verified / opt_in flags (opt_in is mandatory before any
--      AUTHENTICATION message is dispatched — checked at the app layer).
--
--    phone stays NULLABLE for now so this migration runs cleanly against
--    pre-existing rows that have no phone. A UNIQUE index (NULLs distinct)
--    and an E.164 CHECK (NULL-tolerant) enforce correctness on populated
--    rows. NOT NULL enforcement is DEFERRED until a backfill exists.
--    App-layer normalisation defaults a missing country code to +962 (Jordan)
--    before persisting; the DB only validates the resulting E.164 string.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS phone_verified BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS opt_in         BOOLEAN NOT NULL DEFAULT FALSE;

-- E.164: leading '+', a non-zero country-code digit, then up to 14 more digits
-- (15 digits total max). NULL is tolerated until phone is backfilled everywhere.
ALTER TABLE users
    ADD CONSTRAINT users_phone_e164_chk
    CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{1,14}$');

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_phone_unique ON users (phone);


-- ────────────────────────────────────────────────────────────────
-- 4. bookings — attach the dormant payment seam.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS payment_status payment_status NOT NULL DEFAULT 'unpaid';


-- ────────────────────────────────────────────────────────────────
-- 5. status_transitions — append-only audit of every booking state change.
--    from_status is NULL for the initial creation event; actor_id is NULL
--    when the actor is the system. actor_role records who acted
--    (player | owner | admin | system).
-- ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS status_transitions (
    id          SERIAL          PRIMARY KEY,
    booking_id  INT             NOT NULL REFERENCES bookings (id) ON DELETE CASCADE,
    from_status booking_status,
    to_status   booking_status  NOT NULL,
    actor_id    INT             REFERENCES users (id) ON DELETE SET NULL,
    actor_role  VARCHAR(20)     NOT NULL,
    reason      TEXT,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_status_transitions_booking ON status_transitions (booking_id);
