-- Migration 020 (UP) — pitch coordinates for nearest-first availability search
-- ─────────────────────────────────────────────────────────────────────────────
-- DISCREPANCY NOTE (introspected on Neon before writing this): the pitches table
-- ALREADY carries latitude / longitude (double precision) — latitude NULLable
-- default 0, longitude NOT NULL default 0 — populated for some pitches and left at
-- the (0,0) sentinel for the rest. So the PR-4 "ADD COLUMN latitude/longitude NULL"
-- is, in reality, a CONFIRMATION: the columns pre-exist. We do NOT change their
-- nullability here — the canonical Pitch scan reads them as non-pointer float64
-- (internal/data/pitches.go), so flipping longitude to NULLable would require
-- rippling *float64 through that model + the frontend. Out of scope for this MVP.
--
-- Consequence for the availability search: "has usable coordinates" is defined as
-- NOT NULL AND NOT the (0,0) sentinel. A pitch at (0,0) — or NULL — has no location
-- and falls to the default-order tail (never gated out). A 4.1 backfill can set
-- real coordinates later with no schema change.
--
-- This migration is therefore idempotent + additive: it only guarantees the columns
-- exist (for any environment provisioned without them). Transaction-safe.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

ALTER TABLE pitches ADD COLUMN IF NOT EXISTS latitude  double precision NULL;
ALTER TABLE pitches ADD COLUMN IF NOT EXISTS longitude double precision NULL;

COMMIT;
