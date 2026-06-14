-- Migration 017 (DOWN) — reverse manual / walk-in bookings
-- ─────────────────────────────────────────────────────────────────────────────
-- WARNING: this restores the PR 2 SYMMETRIC player CHECK and removes 'manual'
-- from the source set. It will FAIL if any 'manual' rows still exist (they would
-- violate both the re-imposed biconditional and the narrowed source set). Delete
-- or convert manual rows before running this down.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 3. Restore the PR 2 symmetric source⟺player_id biconditional.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_nonplayer_null_player_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_player_source_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_player_source_chk
            CHECK ((source = 'player') = (player_id IS NOT NULL));
    END IF;
END $$;

-- 2. Drop the guest constraint + columns.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_manual_guest_chk;
ALTER TABLE bookings DROP COLUMN IF EXISTS guest_phone;
ALTER TABLE bookings DROP COLUMN IF EXISTS guest_name;

-- 1. Restore the PR 2 source allowed-set (no 'manual').
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_source_chk;
ALTER TABLE bookings
    ADD CONSTRAINT bookings_source_chk
    CHECK (source IN ('player', 'academy', 'block'));

COMMIT;
