-- ================================================================
-- Migration 030 (DOWN) — reverse the contact snapshot + user booking stats.
-- Drops are guarded so the down is safe to re-run. No data is preserved (the
-- snapshot/stat columns are additive and were never the source of truth for any
-- pre-030 behaviour).
-- ================================================================

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_contact_phone_e164_chk;
ALTER TABLE bookings DROP COLUMN IF EXISTS contact_phone;
ALTER TABLE bookings DROP COLUMN IF EXISTS contact_name;

ALTER TABLE users DROP COLUMN IF EXISTS phone_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS booking_count;
ALTER TABLE users DROP COLUMN IF EXISTS last_booking_at;
