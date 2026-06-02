-- ================================================================
-- Migration 005 (DOWN) — reverse notification hardening (PART 6)
--
-- Reverses 005_notification_hardening.up.sql in the opposite order of
-- creation. message_deliveries is dropped before notification_jobs because it
-- FK-references it.
-- ================================================================


-- 3. message_deliveries (references notification_jobs) → drop first.
DROP TABLE IF EXISTS message_deliveries;

-- 2. notification_jobs (the index falls with its table).
DROP TABLE IF EXISTS notification_jobs;

-- 1. users.opt_out.
ALTER TABLE users DROP COLUMN IF EXISTS opt_out;
