-- Migration 010 — booking idempotency keys
--
-- Run once against your database (manual psql, per the project convention):
--
--   psql "$DATABASE_URL" -f migrations/010_booking_idempotency.up.sql
--
-- Idempotent: IF NOT EXISTS guards make it safe to re-run.
--
-- Rationale:
--   * A double-tap or a network retry of POST /bookings must not create a second
--     booking (and, once payments exist, a double charge). The client sends an
--     Idempotency-Key (UUID) header per booking ATTEMPT; this table records the
--     outcome so a replay returns the ORIGINAL response instead of booking again.
--   * Keys are USER-SCOPED: the unique key is (user_id, idem_key), so one user's
--     key can never collide with another's, and a leaked/guessed key cannot replay
--     across users.
--   * fingerprint is a hash of the canonical request (pitch + time range). A reused
--     key with a DIFFERENT body is a client bug → rejected (422), never silently
--     mapped onto the first booking.
--   * The row is written in the SAME transaction as the booking insert (see
--     repository.CreateBookingIdempotent), so a booking and its completed
--     idempotency record commit together or not at all.
--   * expires_at gives a ~24h TTL: an expired key is reclaimable for reuse, and
--     DeleteExpiredIdempotencyKeys prunes old rows for storage hygiene.

CREATE TABLE IF NOT EXISTS booking_idempotency_keys (
    id          BIGSERIAL    PRIMARY KEY,
    idem_key    TEXT         NOT NULL,
    user_id     INTEGER      NOT NULL,
    endpoint    TEXT         NOT NULL,
    fingerprint TEXT         NOT NULL,
    status      TEXT         NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'completed')),
    booking_id  BIGINT       NULL,
    response    JSONB        NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ  NOT NULL,
    CONSTRAINT booking_idempotency_user_key_uniq UNIQUE (user_id, idem_key)
);

-- Supports the TTL prune (DELETE ... WHERE expires_at <= now()).
CREATE INDEX IF NOT EXISTS idx_booking_idempotency_expires_at
    ON booking_idempotency_keys (expires_at);
