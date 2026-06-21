-- Migration 026 (UP) — academy as an owner-created, null-player source
-- ─────────────────────────────────────────────────────────────────────────────
-- The Academy Booking Generator (WO-Academy) inserts DISCRETE booking rows
-- (source='academy') — one per session — exactly like manual walk-ins, so every
-- session is individually cancellable/payable and participates in the one
-- anti-double-booking EXCLUDE, the Visual Calendar, and the Money Engine with NO
-- changes to those subsystems.
--
-- BLOCKER this migration removes: migrations 018→019 left 'academy' in the
-- NON-null-player branch of bookings_source_player_chk (an academy row was modeled
-- as carrying its manager as player_id). An owner-generated academy session has NO
-- platform player; it names the academy via guest_name (academy_name), exactly like
-- a walk-in. So we move 'academy' into the null-player / named-guest world:
--
--   1. bookings_source_player_chk  — academy joins the (block, manual) null-player
--      branch:  source IN ('block','manual','academy') ⟹ player_id IS NULL
--                                    source = 'player'  ⟹ player_id IS NOT NULL
--   2. bookings_manual_guest_chk → renamed-in-spirit to cover academy too: a row of
--      EITHER named source must carry a guest_name:
--          source NOT IN ('manual','academy') OR guest_name IS NOT NULL
--
-- The source value-domain CHECK (bookings_source_chk) ALREADY allows 'academy'
-- (migration 016), so it is left untouched.
--
-- No backfill: zero academy rows exist today (nothing has ever created the source),
-- so every existing row already satisfies the tightened CHECKs. The 'player' rows
-- keep player_id; block/manual rows keep NULL player_id + (manual) guest_name.
-- Transaction-safe + idempotent (drop-if-exists + DO-block guards).
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. Branchy source⟺player_id invariant — academy joins the null-player branch.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_source_player_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_source_player_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_source_player_chk
            CHECK (
                (source IN ('block', 'manual', 'academy') AND player_id IS NULL)
             OR (source = 'player'                        AND player_id IS NOT NULL)
            );
    END IF;
END $$;

-- 2. A named-source row (manual OR academy) must carry guest_name. Replaces the
--    manual-only guard with one that also covers academy sessions (guest_name holds
--    the academy_name).
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_manual_guest_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_named_guest_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_named_guest_chk
            CHECK (source NOT IN ('manual', 'academy') OR guest_name IS NOT NULL);
    END IF;
END $$;

COMMIT;
