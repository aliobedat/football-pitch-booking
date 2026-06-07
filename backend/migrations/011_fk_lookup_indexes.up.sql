-- ================================================================
-- Migration 011 (UP) — FK lookup indexes
--
-- Adds the missing b-tree indexes behind two hot foreign-key lookups:
--   * bookings.player_id  — every "my bookings" query filters on it.
--   * pitches.owner_id     — owner-scoped pitch/booking listings filter on it.
--
-- Plain CREATE INDEX (NOT CONCURRENTLY): pre-launch data volume is tiny and
-- migrations are applied manually, file-by-file (there is no automated runner /
-- schema_migrations ledger), so a brief lock is acceptable and a transaction-
-- wrapped apply is supported. IF NOT EXISTS keeps it idempotent/re-runnable.
-- ================================================================

CREATE INDEX IF NOT EXISTS idx_bookings_player_id ON bookings (player_id);
CREATE INDEX IF NOT EXISTS idx_pitches_owner_id   ON pitches  (owner_id);
