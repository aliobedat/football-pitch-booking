-- ================================================================
-- Migration 030 (UP) — immutable booking contact snapshot + user booking stats
--
-- WHY (delta B): a player booking's recipient contact was resolved at notify
-- time via a live JOIN to users (GetBookingContact), so a later profile phone
-- edit silently re-pointed every historical booking's contact. We freeze the
-- player's name + phone onto the booking row at creation, mirroring how walk-in
-- (manual) rows already snapshot guest_name/guest_phone. The columns are
-- NULLABLE and NOT backfilled: pre-existing rows fall back to the users join in
-- GetBookingContact (COALESCE), so this stays trivially reversible.
--
-- WHY (delta C): per-user booking stats (last_booking_at, booking_count) and the
-- verification timestamp (phone_verified_at) are added as reserved invariants.
-- booking_count/last_booking_at are maintained on the player create path;
-- phone_verified_at is stamped wherever phone_verified flips TRUE.
--
-- Transaction-safe + idempotent (IF NOT EXISTS / guarded constraint). Paired with
-- 030_booking_contact_snapshot.down.sql. EXCLUDE, email, and phone uniqueness are
-- deliberately untouched.
-- ================================================================

-- ── bookings: immutable contact snapshot (player path) ───────────────────────
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS contact_name  text,
    ADD COLUMN IF NOT EXISTS contact_phone text;

-- E.164 shape, mirroring users_phone_e164_chk. NULL-tolerant: old rows carry NULL
-- and non-player rows never populate it. An empty string would FAIL this CHECK, so
-- the write path stores NULL (not '') when a contact value is absent.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_contact_phone_e164_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_contact_phone_e164_chk
            CHECK (contact_phone IS NULL OR contact_phone ~ '^\+[1-9][0-9]{1,14}$');
    END IF;
END $$;

-- ── users: booking stats + verification timestamp ────────────────────────────
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS last_booking_at   timestamptz NULL,
    ADD COLUMN IF NOT EXISTS booking_count     integer     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS phone_verified_at timestamptz NULL;
