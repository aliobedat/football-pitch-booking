-- Migration 009 — pitch image public_id (Cloudinary asset handle)
--
-- Run once against your database (manual psql, per the project convention):
--
--   psql "$DATABASE_URL" -f migrations/009_pitch_image_public_id.up.sql
--
-- Idempotent: IF NOT EXISTS makes it safe to re-run.
--
-- Rationale:
--   * Pitch images are uploaded directly to Cloudinary (browser → Cloudinary)
--     and only the resulting URL is persisted in `image_url` (added in 002).
--   * `image_public_id` stores Cloudinary's stable asset identifier alongside the
--     delivery URL. It is what the backend needs to DESTROY the previous asset
--     when an image is replaced (avoiding orphaned uploads) — the URL alone is not
--     a reliable destroy handle.
--   * NOT NULL DEFAULT '' mirrors image_url's shape so existing rows backfill to an
--     empty handle and Go can scan it as a plain string (no NULL handling).

ALTER TABLE pitches
    ADD COLUMN IF NOT EXISTS image_public_id TEXT NOT NULL DEFAULT '';
