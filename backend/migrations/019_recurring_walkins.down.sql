-- Migration 019 (DOWN) — reverse recurring walk-ins, restore 018's series schema.
-- ─────────────────────────────────────────────────────────────────────────────
-- WARNING: this recreates booking_series + bookings.series_id as they were after
-- migration 018. Any recurrence_group_id data is dropped (the grouping is lost).
-- The recreated composite FK requires booking_series to exist before the column's
-- FK is added, so the order here is the inverse of the UP: parent table first.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- 1. Drop the recurrence grouping.
DROP INDEX IF EXISTS idx_bookings_recurrence_group;
ALTER TABLE bookings DROP COLUMN IF EXISTS recurrence_group_id;

-- 2. Recreate the 018 parent table.
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
    CONSTRAINT booking_series_dow_chk       CHECK (
        array_length(days_of_week, 1) BETWEEN 1 AND 7
        AND days_of_week <@ ARRAY[0,1,2,3,4,5,6]::smallint[]
    ),
    CONSTRAINT booking_series_id_pitch_uq   UNIQUE (series_id, pitch_id)
);

CREATE INDEX IF NOT EXISTS idx_booking_series_pitch
    ON booking_series (pitch_id) WHERE deleted_at IS NULL;

-- 3. Recreate bookings.series_id + the composite FK.
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS series_id UUID NULL;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_series_pitch_fkey') THEN
        ALTER TABLE bookings
            ADD CONSTRAINT bookings_series_pitch_fkey
            FOREIGN KEY (series_id, pitch_id)
            REFERENCES booking_series (series_id, pitch_id)
            ON DELETE RESTRICT;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_bookings_series
    ON bookings (series_id) WHERE series_id IS NOT NULL;

COMMIT;
