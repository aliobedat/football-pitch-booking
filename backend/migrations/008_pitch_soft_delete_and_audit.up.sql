-- Migration 008 — pitch soft delete + deletion audit trail
--
-- Run once against your database (manual psql, per the project convention):
--
--   psql "$DATABASE_URL" -f migrations/008_pitch_soft_delete_and_audit.up.sql
--
-- Idempotent: IF NOT EXISTS guards make it safe to re-run.
--
-- Rationale:
--   * `deleted_at` converts pitch deletion to a SOFT delete so a pitch that is
--     referenced by bookings (FK bookings.pitch_id) is never physically removed
--     — this eliminates the 23503 foreign-key violation while preserving booking
--     history and audit rows. All listing/read queries filter `deleted_at IS NULL`.
--   * `pitch_audit_log` records who deleted (or otherwise acted on) a pitch and
--     when. status_transitions is booking-scoped and cannot represent a pitch
--     event, so this is the dedicated pitch audit mechanism.

ALTER TABLE pitches
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ NULL;

-- Partial index keeps the common "live pitches" scans fast as soft-deleted rows
-- accumulate.
CREATE INDEX IF NOT EXISTS idx_pitches_active
    ON pitches (id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS pitch_audit_log (
    id         BIGSERIAL    PRIMARY KEY,
    pitch_id   INTEGER      NOT NULL,
    actor_id   INTEGER      NULL REFERENCES users(id) ON DELETE SET NULL,
    actor_role VARCHAR(20)  NOT NULL,
    action     TEXT         NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pitch_audit_log_pitch
    ON pitch_audit_log (pitch_id);
