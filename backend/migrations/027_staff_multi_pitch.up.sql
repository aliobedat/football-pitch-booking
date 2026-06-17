-- Migration 027 (UP) — staff 1:1 → 1:N (one staff member, multiple discrete pitches)
-- ─────────────────────────────────────────────────────────────────────────────
-- Business reality: an owner often runs a single complex with several pitches
-- (Pitch A + Pitch B side-by-side) staffed by ONE guard. Migration 021 modeled
-- staff as strictly 1:1 via UNIQUE(user_id) on the staff table; we relax that to
-- allow a user to hold MULTIPLE bindings, one per pitch.
--
-- Same linkage architecture — still the staff(user_id, pitch_id, owner_id) table.
-- Only the cardinality constraint changes:
--   1. DROP UNIQUE(user_id)            (staff_user_id_key) — the 1:1 cap.
--   2. ADD  UNIQUE(user_id, pitch_id)  — a user may be bound to a pitch at most
--      once, but to many distinct pitches (the 1:N invariant + insert idempotency
--      key for ON CONFLICT DO NOTHING).
--   3. ADD  a plain index on user_id — the per-request scope lookup
--      (StaffBindings(user_id)) was previously served by the dropped UNIQUE; keep
--      it fast.
--
-- No data change: existing rows (≤1 per user today) trivially satisfy the new
-- composite UNIQUE. Transaction-safe + idempotent (IF EXISTS / IF NOT EXISTS).
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. Drop the 1:1 cap.
ALTER TABLE staff DROP CONSTRAINT IF EXISTS staff_user_id_key;

-- 2. Composite uniqueness: one binding per (user, pitch); many pitches per user.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'staff_user_pitch_uq') THEN
        ALTER TABLE staff ADD CONSTRAINT staff_user_pitch_uq UNIQUE (user_id, pitch_id);
    END IF;
END $$;

-- 3. Replace the lookup index the dropped UNIQUE used to provide.
CREATE INDEX IF NOT EXISTS idx_staff_user_id ON staff (user_id);

COMMIT;
