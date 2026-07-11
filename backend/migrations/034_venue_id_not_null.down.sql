-- Migration 034 (DOWN) — relax the venue link back to nullable.
--
-- Deliberately does NOT delete the venues created by the catch-up backfill:
-- they are indistinguishable from organically created venues by the time a
-- rollback could run, and pitches keep pointing at them harmlessly (the 033
-- state was exactly this — populated venue_id, nullable column).

BEGIN;

ALTER TABLE pitches ALTER COLUMN venue_id DROP NOT NULL;

COMMIT;
