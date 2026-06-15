-- Migration 018 (UP) — academy recurring bookings (series + child materialization)
-- ─────────────────────────────────────────────────────────────────────────────
-- An ACADEMY is a recurring reservation an owner/manager creates once; a
-- materialization engine generates one CHILD bookings row per occurrence so each
-- concrete slot participates in the GIST anti-double-booking EXCLUDE and the
-- source-agnostic occupancy readers exactly like any other booking.
--
-- KEY TYPE NOTE (correcting the PR-4 brief's assumption): bookings.id is INTEGER
-- (serial), NOT uuid. Child academy rows are ordinary bookings rows, so they keep
-- the existing integer PK. The UUID is the SERIES identifier only: booking_series
-- .series_id (client-supplied, the idempotency key) and a nullable bookings.series_id
-- FK pointing at it. We do NOT alter bookings.id.
--
-- Schema changes:
--   1. Relationship CHECK: replace 017's one-directional implication with the
--      BRANCHY BIDIRECTIONAL invariant (fails closed, total over the 4 sources):
--        (source IN ('block','manual')  AND player_id IS NULL)
--        OR (source IN ('player','academy') AND player_id IS NOT NULL)
--      i.e. held/offline rows never carry a player; player AND academy rows always
--      do (an academy child carries its manager as player_id; the My-Bookings reader
--      filters source='player' so academy rows don't flood the manager's list).
--      NOTE: 'academy' is ALREADY in the source value-domain CHECK (migration 016),
--      so the domain CHECK is left untouched — only the relationship CHECK changes.
--   2. booking_series — the recurrence definition (parent).
--   3. bookings.series_id (nullable UUID) + a COMPOSITE FK (series_id, pitch_id) →
--      booking_series(series_id, pitch_id) so a child can never reference a series
--      on a different pitch. Requires a UNIQUE(series_id, pitch_id) on the parent.
--
-- No backfill: existing rows are player(non-null)/block(null)/manual(null) — all
-- satisfy the branchy CHECK; none have a series_id. Transaction-safe + idempotent.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. Branchy bidirectional relationship CHECK (replaces 017's implication).
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_nonplayer_null_player_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_source_player_chk') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_source_player_chk
            CHECK (
                (source IN ('block', 'manual')   AND player_id IS NULL)
             OR (source IN ('player', 'academy') AND player_id IS NOT NULL)
            );
    END IF;
END $$;

-- 2. booking_series — parent recurrence definition.
--    series_id is the CLIENT-SUPPLIED idempotency key (PR-4 Option C). days_of_week
--    uses Postgres DOW (0=Sun … 6=Sat), matching operating_hours. start/end_time are
--    Asia/Amman WALL-CLOCK times (no tz) resolved to concrete instants at
--    materialization (cross-midnight when end<=start, like operating_hours). status
--    is the series lifecycle; deleted_at is the soft-delete (matching the schema
--    convention). Per-child audit lives in status_transitions; the series-level
--    action is recorded in pitch_audit_log.
CREATE TABLE IF NOT EXISTS booking_series (
    series_id     UUID         PRIMARY KEY,
    pitch_id      INTEGER      NOT NULL REFERENCES pitches(id) ON DELETE RESTRICT,
    player_id     INTEGER      NOT NULL REFERENCES users(id)   ON DELETE RESTRICT,
    days_of_week  SMALLINT[]   NOT NULL,
    start_time    TIME         NOT NULL,
    end_time      TIME         NOT NULL,
    range_start   DATE         NOT NULL,
    range_end     DATE         NOT NULL,
    status        TEXT         NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ  NULL,

    CONSTRAINT booking_series_status_chk    CHECK (status IN ('active', 'cancelled')),
    CONSTRAINT booking_series_range_chk     CHECK (range_end >= range_start),
    -- Every weekday in-domain (0-6) and at least one day selected.
    CONSTRAINT booking_series_dow_chk       CHECK (
        array_length(days_of_week, 1) BETWEEN 1 AND 7
        AND days_of_week <@ ARRAY[0,1,2,3,4,5,6]::smallint[]
    ),
    -- Needed to back the composite FK from bookings (series_id is already unique as
    -- the PK; this pairs it with pitch_id so the FK target is addressable).
    CONSTRAINT booking_series_id_pitch_uq   UNIQUE (series_id, pitch_id)
);

CREATE INDEX IF NOT EXISTS idx_booking_series_pitch
    ON booking_series (pitch_id) WHERE deleted_at IS NULL;

-- 3. bookings.series_id + composite FK enforcing same-pitch parentage.
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS series_id UUID NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_series_pitch_fkey') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_series_pitch_fkey
            FOREIGN KEY (series_id, pitch_id)
            REFERENCES booking_series (series_id, pitch_id)
            ON DELETE RESTRICT;
            -- MATCH SIMPLE (default): a row with series_id NULL is exempt, so every
            -- non-academy booking is unaffected.
    END IF;
END $$;

-- Hot path for materialization/cancel: "all children of a series".
CREATE INDEX IF NOT EXISTS idx_bookings_series
    ON bookings (series_id) WHERE series_id IS NOT NULL;

COMMIT;
