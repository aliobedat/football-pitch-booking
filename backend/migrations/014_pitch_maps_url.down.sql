-- Migration 014 (down) — drop pitch Google Maps URL
--
--   psql "$DATABASE_URL" -f migrations/014_pitch_maps_url.down.sql

ALTER TABLE pitches
    DROP COLUMN IF EXISTS maps_url;
