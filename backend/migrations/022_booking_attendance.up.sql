-- 022_booking_attendance.up.sql
-- Dashboard PR 4: per-booking attendance (single source of truth — NO log table).
-- Per-player no-show history is DERIVED from this column. Meaningful for
-- player/manual bookings; blocks stay 'pending' and are ignored by UI/CRM.
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS attendance VARCHAR(16) NOT NULL DEFAULT 'pending'
    CHECK (attendance IN ('pending', 'checked_in', 'no_show'));
