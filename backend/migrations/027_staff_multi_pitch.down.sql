-- Migration 027 (DOWN) — restore staff 1:1 (UNIQUE(user_id)).
-- ─────────────────────────────────────────────────────────────────────────────
-- Reverses 027 UP. SAFETY: this FAILS if any user currently holds more than one
-- binding (the 1:N rows created under the upgraded schema) — re-adding
-- UNIQUE(user_id) would violate. Collapse such users to a single pitch before
-- rolling back.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

DROP INDEX IF EXISTS idx_staff_user_id;
ALTER TABLE staff DROP CONSTRAINT IF EXISTS staff_user_pitch_uq;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'staff_user_id_key') THEN
        ALTER TABLE staff ADD CONSTRAINT staff_user_id_key UNIQUE (user_id);
    END IF;
END $$;

COMMIT;
