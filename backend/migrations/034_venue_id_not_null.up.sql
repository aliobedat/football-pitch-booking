-- Migration 034 (UP) — WO-VENUES contract step: pitches.venue_id NOT NULL.
--
-- Applies ONLY after the Gate 1b write-path is deployed (every code path that
-- inserts a pitch now populates venue_id or auto-creates the 1:1 venue).
--
-- CATCH-UP BACKFILL first: any pitch created between migration 033 and the
-- Gate 1b deploy — via the old write path — has venue_id IS NULL. That
-- includes SOFT-DELETED pitches (one such row exists in production from the
-- Gate 1b dashboard smoke test): the backfill copies deleted_at onto the
-- generated venue, exactly like 033, so SET NOT NULL cannot fail on them.
-- Slug: 033 semantics — ASCII-slugifiable names slugified (deduped -2/-3 per
-- base within this batch; any collision with an EXISTING venue slug falls back
-- to v-<pitch id>), everything else v-<pitch id>, unique by construction.
--
-- One transaction: a failure anywhere leaves the schema untouched.

BEGIN;

ALTER TABLE venues ADD COLUMN source_pitch_id INTEGER;

WITH src AS (
    SELECT p.id AS pitch_id, p.owner_id, p.name, p.neighborhood,
           COALESCE(p.maps_url, '') AS maps_url,
           p.latitude, p.longitude,
           p.description, p.image_url, p.image_public_id,
           p.is_active, p.deleted_at, p.created_at,
           CASE
               WHEN p.name ~ '^[\x20-\x7E]+$'
                    AND btrim(regexp_replace(lower(p.name), '[^a-z0-9]+', '-', 'g'), '-') <> ''
               THEN btrim(regexp_replace(lower(p.name), '[^a-z0-9]+', '-', 'g'), '-')
               ELSE 'v-' || p.id
           END AS base_slug
    FROM pitches p
    WHERE p.venue_id IS NULL          -- catch-up rows only, soft-deleted INCLUDED
),
dedup AS (
    SELECT src.*,
           CASE WHEN ROW_NUMBER() OVER (PARTITION BY base_slug ORDER BY pitch_id) = 1
                THEN base_slug
                ELSE base_slug || '-' ||
                     ROW_NUMBER() OVER (PARTITION BY base_slug ORDER BY pitch_id)
           END AS candidate_slug
    FROM src
)
INSERT INTO venues (owner_id, name, slug, neighborhood, maps_url,
                    latitude, longitude, description,
                    cover_image_url, cover_image_public_id,
                    is_active, created_at, deleted_at, source_pitch_id)
SELECT owner_id, name,
       CASE
           -- unlike 033 (empty venues table), a generated slug can now collide
           -- with a venue created since; fall back to the always-unique form.
           WHEN EXISTS (SELECT 1 FROM venues v WHERE lower(v.slug) = lower(candidate_slug))
               THEN 'v-' || pitch_id
           ELSE candidate_slug
       END,
       neighborhood, maps_url, latitude, longitude,
       NULLIF(description, ''),
       NULLIF(image_url, ''), NULLIF(image_public_id, ''),
       is_active, created_at, deleted_at, pitch_id
FROM dedup;

UPDATE pitches p
SET venue_id = v.id
FROM venues v
WHERE v.source_pitch_id = p.id
  AND p.venue_id IS NULL;

ALTER TABLE venues DROP COLUMN source_pitch_id;

-- The contract: every pitch, live or soft-deleted, belongs to a venue.
ALTER TABLE pitches ALTER COLUMN venue_id SET NOT NULL;

COMMIT;
