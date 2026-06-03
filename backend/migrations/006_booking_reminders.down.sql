-- ================================================================
-- Migration 006 (DOWN) — reverse automated 24-hour reminders (PART 7)
--
-- Reverses 006_booking_reminders.up.sql in the opposite order of creation:
-- the partial index is dropped before its underlying column.
-- ================================================================


-- 2. idx_bookings_reminder_due.
DROP INDEX IF EXISTS idx_bookings_reminder_due;

-- 1. bookings.reminder_sent.
ALTER TABLE bookings DROP COLUMN IF EXISTS reminder_sent;
