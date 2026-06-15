-- Migration 014 — pitch Google Maps URL
--
-- Run once against your database (manual psql, per the project convention):
--
--   psql "$DATABASE_URL" -f migrations/014_pitch_maps_url.up.sql
--
-- Idempotent: IF NOT EXISTS makes it safe to re-run.
--
-- Rationale:
--   * Owners may paste a Google Maps link for their pitch; players see it as a
--     clickable link on the detail page. There is NO geocoding and NO map picker
--     — this is a paste-and-save text field only.
--   * Nullable with no default: existing pitches legitimately have no URL. The Go
--     layer reads it via COALESCE(maps_url, '') so a NULL scans as an empty string.

ALTER TABLE pitches
    ADD COLUMN IF NOT EXISTS maps_url TEXT;
