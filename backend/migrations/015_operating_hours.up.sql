-- Migration 015 — per-pitch weekly operating hours + audit detail payload
--
-- Run once against your database (manual psql, per the project convention):
--
--   psql "$DATABASE_URL" -f migrations/015_operating_hours.up.sql
--
-- Idempotent: IF NOT EXISTS guards make it safe to re-run.
--
-- Rationale:
--   * `operating_hours` stores a per-pitch WEEKLY open-window schedule. One row =
--     one open window on one weekday. Windows are wholesale-REPLACED by the PUT
--     editor (DELETE + INSERT in one tx), so the rows are config, not a lifecycle
--     entity — there is deliberately NO soft-delete column here. The meaningful
--     audit unit is "the schedule changed", recorded in pitch_audit_log (below).
--   * weekday: 0 = Sunday … 6 = Saturday, matching Postgres EXTRACT(DOW) and Go
--     time.Weekday. Pinned exactly; do not deviate.
--   * open_time / close_time are Asia/Amman wall-clock TIMEs with NO timezone —
--     they recur weekly, so no absolute instant is stored. The instant is resolved
--     at query time (ResolveOpenWindows) via the embedded IANA tzdata.
--   * Cross-midnight is DERIVED, not stored: a window with close_time <= open_time
--     spills into the next weekday (e.g. Thu 16:00 → 02:00 covers Thu 16:00 → Fri
--     02:00). No flag column.
--   * NO price column — but rows are discrete windows so a price column can be
--     added later without reshaping.
--   * pitch_id is INTEGER to match pitches.id (SERIAL). ON DELETE CASCADE: a pitch
--     is only ever SOFT-deleted (deleted_at), so this cascade is a belt-and-braces
--     guard for a hypothetical hard delete, never exercised in normal operation.

CREATE TABLE IF NOT EXISTS operating_hours (
    id         BIGSERIAL   PRIMARY KEY,
    pitch_id   INTEGER     NOT NULL REFERENCES pitches(id) ON DELETE CASCADE,
    weekday    SMALLINT    NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    open_time  TIME        NOT NULL,
    close_time TIME        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hot read path is "all windows for a pitch" (resolution touches the target
-- weekday and the day-before for cross-midnight spill), ordered by weekday.
CREATE INDEX IF NOT EXISTS idx_operating_hours_pitch_weekday
    ON operating_hours (pitch_id, weekday);

-- Audit richness: an `operating_hours_updated` action with no payload tells you
-- THAT the hours changed but not TO WHAT. `detail` carries the post-change window
-- snapshot (JSON array). Additive + nullable so every existing pitch_audit_log row
-- (activate/deactivate/delete) stays valid with a NULL detail.
ALTER TABLE pitch_audit_log
    ADD COLUMN IF NOT EXISTS detail JSONB NULL;
