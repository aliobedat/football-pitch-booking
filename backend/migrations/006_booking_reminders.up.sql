-- ================================================================
-- Migration 006 (UP) — Automated 24-hour booking reminders (PART 7)
--
-- Adds the bookkeeping the reminder cron worker needs:
--   * bookings.reminder_sent — a once-only guard so a confirmed booking's
--     24h reminder is enqueued EXACTLY once, even across worker restarts or
--     multiple horizontally-scaled worker instances.
--
-- The worker claims due bookings (status='confirmed', reminder_sent=false,
-- starting within the next 24h) with SELECT ... FOR UPDATE SKIP LOCKED, marks
-- them reminded, and enqueues a booking_reminder onto the notification_jobs
-- outbox — all in one transaction, so a reminder is queued exactly once or not
-- at all.
--
-- Paired with 006_booking_reminders.down.sql. Idempotent (IF NOT EXISTS);
-- run exactly once forward.
-- ================================================================


-- ────────────────────────────────────────────────────────────────
-- 1. bookings.reminder_sent — once-only reminder guard.
--    Default FALSE: existing confirmed bookings become immediately eligible
--    for a reminder if they fall inside the 24h window. The worker flips it
--    true in the SAME transaction that enqueues the outbox job.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS reminder_sent BOOLEAN NOT NULL DEFAULT FALSE;


-- ────────────────────────────────────────────────────────────────
-- 2. idx_bookings_reminder_due — keep the recurring claim cheap.
--    The reminder claim always filters (status='confirmed', reminder_sent=false)
--    and orders by the booking start. This partial expression index serves
--    exactly that predicate, so the worker's poll stays O(due rows) rather than
--    O(all bookings) as completed/old rows accumulate.
-- ────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_bookings_reminder_due
    ON bookings (lower(booking_range))
    WHERE status = 'confirmed' AND reminder_sent = FALSE;
