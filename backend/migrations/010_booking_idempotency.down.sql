-- Migration 010 (DOWN) — drop booking idempotency keys.
--
--   psql "$DATABASE_URL" -f migrations/010_booking_idempotency.down.sql

DROP INDEX IF EXISTS idx_booking_idempotency_expires_at;
DROP TABLE IF EXISTS booking_idempotency_keys;
