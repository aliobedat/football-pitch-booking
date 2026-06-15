-- Migration 016 (UP) — booking source discriminator + nullable player_id
-- ─────────────────────────────────────────────────────────────────────────────
-- Introduces `source` so a bookings row can be a player booking, an owner BLOCK,
-- or (PR 3) an ACADEMY session — all sharing the one anti-double-booking EXCLUDE
-- (which is status-only, so non-cancelled rows of EVERY source already conflict).
--
-- Modeling choice: text + CHECK, NOT a PG enum. Extending the allowed set in PR 3
-- is a one-line CHECK swap; an enum would force `ALTER TYPE … ADD VALUE`, which
-- cannot run inside a transaction. (status is a legacy enum; we deliberately do
-- not mirror that here.)
--
-- player_id becomes NULLABLE. Locked invariant (holds for academy too):
--   source = 'player'  ⟺  player_id IS NOT NULL
-- i.e. ONLY player rows carry a player; blocks and academy sessions never do.
-- The contract party for academy lives on academy_contracts (PR 3), not here.
--
-- Safe sequence on the populated table:
--   1. ADD source with DEFAULT 'player' → backfills every existing row (the only
--      rows today are player bookings) as a fast metadata-only operation.
--   2. CHECK source ∈ {player, academy, block}.
--   3. DROP the DEFAULT → going forward EVERY insert states source explicitly
--      (fail-closed: a future academy insert that forgets it ERRORS rather than
--      silently becoming a player booking).
--   4. DROP NOT NULL on player_id.
--   5. ADD the symmetric source⟺player_id CHECK — AFTER backfill, so the existing
--      player rows (source='player', player_id NOT NULL) validate.
--
-- Transaction-safe. Idempotent: IF NOT EXISTS + DO-block guards make re-runs safe.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. source column, backfilling existing rows to 'player'.
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'player';

-- 2. Allowed-value CHECK (extended with 'academy' in PR 3).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_source_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_source_chk
            CHECK (source IN ('player', 'academy', 'block'));
    END IF;
END $$;

-- 3. Drop the default — every future insert must specify source explicitly.
ALTER TABLE bookings ALTER COLUMN source DROP DEFAULT;

-- 4. player_id becomes nullable (blocks/academy have no player).
ALTER TABLE bookings ALTER COLUMN player_id DROP NOT NULL;

-- 5. Symmetric invariant: player rows ⟺ a player; non-player rows ⟺ NULL player.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_player_source_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_player_source_chk
            CHECK ((source = 'player') = (player_id IS NOT NULL));
    END IF;
END $$;

COMMIT;
