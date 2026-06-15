-- 021_staff_pitch_binding.up.sql
-- Dashboard PR 2 (Backend RBAC): introduce the owner-provisioned `staff` role and
-- the staff→pitch binding table.
--
-- IMPORTANT (manual apply): `ALTER TYPE ... ADD VALUE` cannot run in the same
-- transaction that subsequently *uses* the new label. The staff table below does
-- NOT reference the 'staff' literal in DDL (the value is only ever written as
-- users.role data), so applying this file as a single batch is safe. If your
-- client wraps everything in one transaction and complains, run the ALTER TYPE
-- on its own first, then the CREATE TABLE.

-- 1. Extend the user_role enum with 'staff' (idempotent).
ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'staff';

-- 2. staff binding: which pitch a staff user operates, and which owner provisioned
--    them. V1 = a single pitch per staff member, enforced by the UNIQUE(user_id).
CREATE TABLE IF NOT EXISTS staff (
    id          SERIAL PRIMARY KEY,
    user_id     INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    pitch_id    INTEGER NOT NULL REFERENCES pitches(id) ON DELETE CASCADE,
    -- The owner who provisioned this staff member. Invariant enforced in the
    -- write path: owner_id MUST own pitch_id (an owner may only bind staff to a
    -- pitch they actually own).
    owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Scope guard reads the binding by user_id on every staff request (UNIQUE already
-- indexes it). Owner-side "list my staff" filters by owner_id; bookings views by
-- pitch_id — index both.
CREATE INDEX IF NOT EXISTS idx_staff_owner_id ON staff (owner_id);
CREATE INDEX IF NOT EXISTS idx_staff_pitch_id ON staff (pitch_id);
