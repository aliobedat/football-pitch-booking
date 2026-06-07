-- Migration 013 (UP) — Native Verified Review System
-- ─────────────────────────────────────────────────────────────────────────────
-- A review is 1-to-1 per (player, pitch). Eligibility is DERIVED: it requires a
-- non-cancelled booking whose end (upper(booking_range)) is already in the past.
--
-- ⚠ DEVIATION FROM SPEC (see PR note): the task brief specified UUID keys with
-- gen_random_uuid(). This database has NO uuid extension and every existing PK
-- (users.id, pitches.id, bookings.id — see migrations 002/003/008) is INTEGER.
-- A `booking_id UUID` column therefore cannot reference the integer bookings.id.
-- All keys below are INTEGER to stay consistent and keep the composite FK valid.
-- The exact constraint ORDER and definitions from the brief are otherwise kept.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. Composite UNIQUE on bookings — makes the (id, player_id, pitch_id) triple a
--    valid FK target. Beyond uniqueness (id alone is already unique), this lets
--    the reviews FK below guarantee a review's booking genuinely belongs to the
--    same player AND pitch the review claims — integrity, not just a pointer.
ALTER TABLE bookings
    ADD CONSTRAINT uq_booking_triple UNIQUE (id, player_id, pitch_id);

-- 2. reviews
CREATE TABLE IF NOT EXISTS reviews (
    id          INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    pitch_id    INTEGER     NOT NULL,
    player_id   INTEGER     NOT NULL,
    booking_id  INTEGER     NOT NULL,
    rating      SMALLINT    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment     TEXT        CHECK (char_length(comment) <= 1000),
    is_flagged  BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT fk_reviews_booking_triple
        FOREIGN KEY (booking_id, player_id, pitch_id)
        REFERENCES bookings (id, player_id, pitch_id) ON DELETE RESTRICT
);

-- 3. One LIVE (non-deleted) review per (player, pitch). A soft-deleted review
--    frees the slot so a player can review again after admin moderation.
CREATE UNIQUE INDEX IF NOT EXISTS uq_review_player_pitch
    ON reviews (player_id, pitch_id) WHERE deleted_at IS NULL;

-- 4. Pitch-scoped listing + aggregates over live reviews.
CREATE INDEX IF NOT EXISTS idx_reviews_pitch_id
    ON reviews (pitch_id) WHERE deleted_at IS NULL;

-- 5. Eligibility lookup: qualifying-booking probe filters by (player, pitch).
CREATE INDEX IF NOT EXISTS idx_bookings_player_pitch
    ON bookings (player_id, pitch_id);

COMMIT;
