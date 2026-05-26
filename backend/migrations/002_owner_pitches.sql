-- Run once against your database before deploying the updated backend.
--
--   psql $DATABASE_URL -f migrations/002_owner_pitches.sql
--
-- Idempotent: each statement uses IF NOT EXISTS / DEFAULT so it is safe
-- to run multiple times without side effects.

ALTER TABLE pitches
    ADD COLUMN IF NOT EXISTS owner_id     INTEGER REFERENCES users(id),
    ADD COLUMN IF NOT EXISTS description  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS image_url    TEXT NOT NULL DEFAULT '';
