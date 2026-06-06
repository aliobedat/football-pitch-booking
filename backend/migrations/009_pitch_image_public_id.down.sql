-- Migration 009 (down) — drop pitch image public_id
--
--   psql "$DATABASE_URL" -f migrations/009_pitch_image_public_id.down.sql

ALTER TABLE pitches
    DROP COLUMN IF EXISTS image_public_id;
