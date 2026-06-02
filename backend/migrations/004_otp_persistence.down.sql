-- ================================================================
-- Migration 004 (DOWN) — reverse OTP persistence + phone-first user shape
--
-- Reverses 004_otp_persistence.up.sql in the opposite order of creation.
--
-- NOTE: restoring NOT NULL on users.password_hash / users.full_name will FAIL
-- if any phone-first row (created with NULLs by the OTP verify flow) still
-- exists. Backfill those columns before reversing if that occurs — the same
-- lossy-reversal caveat as migration 003's email column.
-- ================================================================


-- ────────────────────────────────────────────────────────────────
-- 3 & 2. Drop the OTP tables (the index falls with its table).
-- ────────────────────────────────────────────────────────────────
DROP TABLE IF EXISTS otp_rate_events;
DROP TABLE IF EXISTS otp_codes;


-- ────────────────────────────────────────────────────────────────
-- 1. users — restore the email-era NOT NULL constraints.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE users ALTER COLUMN full_name     SET NOT NULL;
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
