-- ================================================================
-- Migration 002 — Pitch coordinates + reviews table
-- Run once against the live database (Neon SQL editor or psql).
-- Safe to re-run: uses IF NOT EXISTS / ON CONFLICT DO NOTHING.
-- ================================================================

-- ── A1. Coordinates ───────────────────────────────────────────────────────────
-- Plain DOUBLE PRECISION columns.
-- FUTURE: migrate to PostGIS geography(Point,4326) + GIST index
-- for server-side ST_DWithin nearest-queries. These columns deliberately
-- avoid any PostGIS dependency so that migration is non-blocking.

ALTER TABLE pitches
    ADD COLUMN IF NOT EXISTS latitude  DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION NOT NULL DEFAULT 0;

-- Backfill realistic Amman / Ain Al-Basha area coordinates.
-- Spread ≈ 500 m around 32.052 N, 35.859 E.
-- Replace with real per-pitch coordinates via the owner dashboard later.
UPDATE pitches
SET
    latitude  = 32.052 + ((id % 7) - 3) * 0.008,
    longitude = 35.859 + ((id % 5) - 2) * 0.006
WHERE latitude = 0 AND longitude = 0;

-- ── A2. Reviews table ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS reviews (
    id         SERIAL       PRIMARY KEY,
    pitch_id   INT          NOT NULL REFERENCES pitches (id) ON DELETE CASCADE,
    user_id    INT          NOT NULL REFERENCES users   (id) ON DELETE CASCADE,
    rating     INT          NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment    TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- One review per user per pitch
    CONSTRAINT reviews_pitch_user_unique UNIQUE (pitch_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_reviews_pitch ON reviews (pitch_id);
CREATE INDEX IF NOT EXISTS idx_reviews_user  ON reviews (user_id);

-- Seed 3–5 sample reviews per pitch (one per user, cycling ratings 3-5).
-- ON CONFLICT DO NOTHING: idempotent — safe to re-run.
INSERT INTO reviews (pitch_id, user_id, rating, comment)
SELECT
    p.id AS pitch_id,
    u.id AS user_id,
    (ARRAY[5, 4, 5, 4, 3, 5, 4])[((p.id + u.id) % 7) + 1] AS rating,
    CASE ((p.id * u.id) % 3)
        WHEN 0 THEN 'ملعب رائع وأرضية ممتازة'
        WHEN 1 THEN 'خدمة جيدة والملعب نظيف جداً'
        ELSE NULL
    END AS comment
FROM       pitches p
CROSS JOIN (SELECT id FROM users ORDER BY id LIMIT 5) u
ON CONFLICT DO NOTHING;
