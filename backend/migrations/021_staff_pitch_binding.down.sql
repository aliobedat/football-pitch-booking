-- 021_staff_pitch_binding.down.sql
-- Reverse the staff binding table. NOTE: Postgres cannot DROP a single enum
-- value, so 'staff' remains on user_role after a down-migration. This is
-- harmless (no rows should carry it once the table and its data are gone). Demote
-- any leftover staff users to player first so the dangling label is unused.
UPDATE users SET role = 'player' WHERE role = 'staff';

DROP INDEX IF EXISTS idx_staff_pitch_id;
DROP INDEX IF EXISTS idx_staff_owner_id;
DROP TABLE IF EXISTS staff;
