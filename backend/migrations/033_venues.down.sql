-- Migration 033 (DOWN) — full reverse of the venues grouping layer.
-- Drops the pitch→venue link and label, the venues table (and with it the
-- slug CHECK + unique index + owner index), and restores the duplicate
-- owner index removed by the UP's pre-authorized cleanup. Round-trips
-- clean: down → up reproduces the UP state exactly.

BEGIN;

DROP INDEX IF EXISTS idx_pitches_venue;

ALTER TABLE pitches
    DROP COLUMN IF EXISTS venue_id,
    DROP COLUMN IF EXISTS label;

DROP TABLE IF EXISTS venues;

CREATE INDEX IF NOT EXISTS idx_pitches_owner_id ON pitches (owner_id);

COMMIT;
