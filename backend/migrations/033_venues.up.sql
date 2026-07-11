-- Migration 033 (UP) — venues grouping layer above pitches (WO-VENUES / 1a).
--
-- WHY: a physical complex (المجمع) can host several pitches, but today
-- pitch == place. `venues` owns the place-level identity (name, slug,
-- neighborhood, maps_url, coordinates, description, cover image, active
-- flag); pitches keep everything play-specific (surface, format, price,
-- amenities, schedule) and gain a nullable `label` for disambiguating
-- sibling pitches inside a venue. Bookings stay keyed to pitch_id — the
-- GIST EXCLUDE anti-double-booking constraint is NOT touched.
--
-- BACKFILL: 1:1 — one venue per existing pitch (including soft-deleted
-- pitches, whose venues copy deleted_at, so venue_id can be NOT NULL
-- globally). Slug: ASCII-slugifiable names are slugified (deduped with
-- -2/-3 suffixes per the WO); anything else (all current Arabic names)
-- gets 'v-<pitch id>', unique by construction. label stays NULL — a
-- single-pitch venue has no label by definition.
--
-- Everything runs in ONE transaction: a failure anywhere leaves the
-- schema untouched.

BEGIN;

-- 1. The venues table. Column types for latitude/longitude mirror
--    pitches verbatim (double precision; longitude NOT NULL, latitude
--    nullable — inherited asymmetry, kept for exact type parity).
CREATE TABLE venues (
    id                    BIGSERIAL PRIMARY KEY,
    owner_id              INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name                  TEXT NOT NULL,
    slug                  TEXT NOT NULL
        CONSTRAINT venues_slug_format_check CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    neighborhood          TEXT NOT NULL,
    maps_url              TEXT NOT NULL,
    latitude              double precision DEFAULT 0,
    longitude             double precision DEFAULT 0 NOT NULL,
    description           TEXT,
    cover_image_url       TEXT,
    cover_image_public_id TEXT,
    is_active             BOOLEAN NOT NULL DEFAULT true,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at            TIMESTAMPTZ
);

-- Case-insensitive slug uniqueness (the CHECK already forces lowercase;
-- the lower() index makes the guarantee explicit and future-proof).
CREATE UNIQUE INDEX venues_slug_lower_unique ON venues (lower(slug));

-- Live-venue lookups by owner (mirrors idx_pitches_active's partial style).
CREATE INDEX idx_venues_owner ON venues (owner_id) WHERE deleted_at IS NULL;

-- 2. Pitches gain the venue link (nullable until the backfill lands)
--    and the sibling label.
ALTER TABLE pitches
    ADD COLUMN venue_id BIGINT REFERENCES venues(id) ON DELETE RESTRICT,
    ADD COLUMN label    TEXT;

-- 3. Backfill: one venue per pitch. source_pitch_id is a TEMPORARY
--    mapping column so the pitches UPDATE can join INSERT output to its
--    source row; it is dropped at the end of this transaction.
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
),
dedup AS (
    SELECT src.*,
           ROW_NUMBER() OVER (PARTITION BY base_slug ORDER BY pitch_id) AS rn
    FROM src
)
INSERT INTO venues (owner_id, name, slug, neighborhood, maps_url,
                    latitude, longitude, description,
                    cover_image_url, cover_image_public_id,
                    is_active, created_at, deleted_at, source_pitch_id)
SELECT owner_id, name,
       CASE WHEN rn = 1 THEN base_slug ELSE base_slug || '-' || rn END,
       neighborhood, maps_url, latitude, longitude,
       NULLIF(description, ''),
       NULLIF(image_url, ''), NULLIF(image_public_id, ''),
       is_active, created_at, deleted_at, pitch_id
FROM dedup;

UPDATE pitches p
SET venue_id = v.id
FROM venues v
WHERE v.source_pitch_id = p.id;

ALTER TABLE venues DROP COLUMN source_pitch_id;

-- 4. venue_id stays NULLABLE here (expand/contract, ratified option (a)):
--    the Gate 1b write-path must populate it before migration 034 applies
--    the catch-up backfill + SET NOT NULL. Applying NOT NULL now would 500
--    production POST /pitches, whose INSERT predates venue awareness.

CREATE INDEX idx_pitches_venue ON pitches (venue_id);

-- 5. Pre-authorized cleanup: idx_pitches_owner and idx_pitches_owner_id
--    are byte-identical (both btree(owner_id)); keep the former.
DROP INDEX IF EXISTS idx_pitches_owner_id;

COMMIT;
