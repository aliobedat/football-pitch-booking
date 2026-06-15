-- Migration 017 (UP) — manual / walk-in bookings (offline occupancy)
-- ─────────────────────────────────────────────────────────────────────────────
-- Adds the 'manual' source: an owner-logged offline booking (a walk-in / phone
-- reservation) that has NO platform player (player_id IS NULL) but DOES name a
-- guest. Like a block it shares the one anti-double-booking EXCLUDE, so an online
-- player can never double-book a slot an owner already filled offline (and vice
-- versa) — the platform becomes the single source of truth.
--
-- Schema changes:
--   1. Extend the source allowed-set CHECK with 'manual'.
--   2. guest_name / guest_phone (both nullable columns). guest_name is REQUIRED
--      for manual rows via a partial implication CHECK; guest_phone stays optional.
--   3. Relax the PR 2 SYMMETRIC source⟺player_id CHECK to a UNIDIRECTIONAL
--      implication. PR 2 enforced `(source='player') = (player_id IS NOT NULL)`,
--      which forbade ANY non-player row from carrying a player AND forced every
--      player row to carry one. PR 4 (academy, Option C) needs to relax the
--      biconditional, so we move now to the weaker, forward-compatible invariant:
--          source IN ('block','manual')  ⟹  player_id IS NULL
--      i.e. held/offline rows never carry a platform player. Player rows continue
--      to carry a player in practice (the write-path always sets it); we simply no
--      longer assert it at the constraint level, leaving room for PR 4 sources.
--
-- Existing rows need no backfill: today's rows are 'player' (player_id NOT NULL,
-- guest_name NULL) and 'block' (player_id NULL), both of which satisfy every new
-- CHECK. Transaction-safe + idempotent (IF NOT EXISTS + DO-block guards).
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. Allowed-value CHECK: re-create including 'manual'.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_source_chk;
ALTER TABLE bookings
    ADD CONSTRAINT bookings_source_chk
    CHECK (source IN ('player', 'academy', 'block', 'manual'));

-- 2. Guest identity columns (nullable). guest_name carries the walk-in's name;
--    guest_phone is an optional contact. They are NULL for every non-manual row.
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS guest_name  text;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS guest_phone text;

-- 2b. A manual row MUST name its guest (enforced at the DB level). Non-manual
--     rows are unconstrained here (guest_name stays NULL for them).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_manual_guest_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_manual_guest_chk
            CHECK (source <> 'manual' OR guest_name IS NOT NULL);
    END IF;
END $$;

-- 3. Relax the symmetric player invariant to a unidirectional implication.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_player_source_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_nonplayer_null_player_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_nonplayer_null_player_chk
            CHECK (source NOT IN ('block', 'manual') OR player_id IS NULL);
    END IF;
END $$;

COMMIT;
