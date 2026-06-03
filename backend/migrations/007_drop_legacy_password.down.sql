-- ================================================================
-- Migration 007 (DOWN) — Restore the legacy password_hash column.
--
-- Re-adds the column as NULLABLE (its post-004 shape). Password data CANNOT be
-- recovered — this only restores the column structure so an older application
-- build that still references password_hash can run again.
-- ================================================================

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;
