-- Migration 026 (DOWN) — restore academy to the non-null-player branch.
-- ─────────────────────────────────────────────────────────────────────────────
-- Reverses 026 UP: academy moves back next to 'player' (player_id NOT NULL) and the
-- named-guest CHECK shrinks back to manual-only. SAFETY: this fails if any
-- source='academy' rows exist with NULL player_id (i.e. rows created by the Academy
-- Generator) — that is intentional. Those rows are valid only under the UP schema;
-- delete or re-source them before rolling back.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_named_guest_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_manual_guest_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_manual_guest_chk
            CHECK (source <> 'manual' OR guest_name IS NOT NULL);
    END IF;
END $$;

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_source_player_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_source_player_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_source_player_chk
            CHECK (
                (source IN ('block', 'manual')   AND player_id IS NULL)
             OR (source IN ('player', 'academy') AND player_id IS NOT NULL)
            );
    END IF;
END $$;

COMMIT;
