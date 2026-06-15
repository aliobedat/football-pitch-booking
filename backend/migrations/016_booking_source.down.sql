-- Migration 016 (DOWN) — reverse the source discriminator + nullable player_id
-- ─────────────────────────────────────────────────────────────────────────────
-- WARNING: restoring player_id NOT NULL will FAIL if any block/academy rows
-- (player_id IS NULL) exist. Delete or convert them before running this down.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_player_source_chk;

-- Re-impose NOT NULL on player_id (errors if NULL rows remain — see warning).
ALTER TABLE bookings ALTER COLUMN player_id SET NOT NULL;

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_source_chk;
ALTER TABLE bookings DROP COLUMN IF EXISTS source;

COMMIT;
