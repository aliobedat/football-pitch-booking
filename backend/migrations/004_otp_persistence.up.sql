-- ================================================================
-- Migration 004 (UP) — OTP persistence + phone-first user shape
--
-- PART 3B: gives the OTP service a durable home and lets a user be born
-- from a phone number alone (no password, no name, no email).
--
--   * users: password_hash / full_name become OPTIONAL. A phone-first user
--     created by the OTP verify flow has neither yet — they are filled in
--     later by profile completion. email was already made optional in 003.
--   * otp_codes: at most one active one-time code per phone. Only the keyed
--     HMAC digest is stored, never the plaintext (see internal/otp/hasher.go).
--   * otp_rate_events: append-only event log backing the sliding-window
--     per-phone / per-IP request rate limiter.
--
-- Paired with 004_otp_persistence.down.sql. Idempotent where Postgres allows
-- (IF NOT EXISTS); run exactly once forward.
-- ================================================================


-- ────────────────────────────────────────────────────────────────
-- 1. users — relax the email-era NOT NULL constraints so a phone-only
--    identity is representable. The application reads these columns via
--    COALESCE(..., '') so a NULL surfaces as an empty string in the model.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE users ALTER COLUMN full_name     DROP NOT NULL;


-- ────────────────────────────────────────────────────────────────
-- 2. otp_codes — one active code per phone (phone is the primary key, so a
--    resend UPSERTs in place, replacing the previous code and resetting its
--    attempt count). attempts counts FAILED verifications and drives lockout.
--    No FK to users: a code may be issued for a phone whose user row is still
--    being shaped, and the code's lifecycle is independent of identity.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS otp_codes (
    phone      TEXT         PRIMARY KEY,
    code_hash  TEXT         NOT NULL,
    expires_at TIMESTAMPTZ  NOT NULL,
    attempts   INT          NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);


-- ────────────────────────────────────────────────────────────────
-- 3. otp_rate_events — one row per accepted OTP request, keyed by the limiter
--    bucket ("phone:<e164>" or "ip:<addr>"). The limiter counts rows newer
--    than the window cutoff and admits only while under quota. Rows older than
--    the window are pruned on each check, so the table stays small.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS otp_rate_events (
    id          BIGSERIAL    PRIMARY KEY,
    bucket_key  TEXT         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- The limiter always filters by (bucket_key, created_at); this composite index
-- serves both the count-in-window read and the prune delete.
CREATE INDEX IF NOT EXISTS idx_otp_rate_events_bucket_time
    ON otp_rate_events (bucket_key, created_at);
