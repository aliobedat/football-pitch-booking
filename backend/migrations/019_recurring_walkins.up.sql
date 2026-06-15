-- Migration 019 (UP) — recurring walk-ins (lean academy MVP); supersedes 018's
-- series materialization engine.
-- ─────────────────────────────────────────────────────────────────────────────
-- PRODUCT PIVOT: instead of a parent/child materialization engine (booking_series
-- + composite-FK children, migration 018), recurrence is now a LIGHTWEIGHT GROUPING.
-- Every recurring slot is just a normal manual booking (source='manual',
-- player_id=NULL, guest_name set, priced) tagged with a shared recurrence_group_id
-- UUID. The group id is the idempotency key for the create loop AND the handle for
-- bulk-cancelling future occurrences. No new entity, no FK graph.
--
-- We KEEP the hard invariants from 018 — they fail closed and still protect the
-- schema with manual rows:
--   * bookings_source_player_chk (branchy bidirectional source⟺player_id)
--   * bookings_source_chk (source value-domain, incl. 'manual'/'academy')
--
-- DROP ORDER IS CRITICAL: drop bookings.series_id FIRST. That column carries the
-- composite FK (bookings_series_pitch_fkey) and is covered by idx_bookings_series,
-- both of which Postgres removes automatically with the column. ONLY THEN is
-- booking_series unreferenced and safe to DROP. Reversing the order would fail:
-- DROP TABLE booking_series while the FK still points at it errors (2BP01).
--
-- Roll-forward, not a rollback of 018: this is a forward migration that reshapes
-- the academy approach. Transaction-safe + idempotent.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. Remove the 018 series link FIRST (drops the composite FK + idx_bookings_series
--    with the column), THEN the now-unreferenced parent table.
ALTER TABLE bookings DROP COLUMN IF EXISTS series_id;
DROP TABLE IF EXISTS booking_series;

-- 2. Lightweight recurrence grouping. NULL for one-off (non-recurring) bookings;
--    a shared UUID across every occurrence of one recurring walk-in series.
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS recurrence_group_id UUID NULL;

-- 3. Hot paths: idempotency replay lookup + bulk-cancel, both keyed on the group.
CREATE INDEX IF NOT EXISTS idx_bookings_recurrence_group
    ON bookings (recurrence_group_id) WHERE recurrence_group_id IS NOT NULL;

COMMIT;
