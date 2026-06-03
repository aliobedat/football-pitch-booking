-- ================================================================
-- Migration 007 (UP) — Drop the legacy password_hash column.
--
-- Phone-first OTP is now the SOLE authentication method (Step C): the
-- email/password login + register endpoints and all password hashing have been
-- removed from the application, so password_hash has no remaining reader or
-- writer and is dropped.
--
-- email is intentionally KEPT (nullable, optional/secondary identifier) per the
-- phone-first architecture principle in CLAUDE.md — it still backs user profiles
-- and the owner booking view (AdminBooking.user_email). Only the password column
-- is removed here.
--
-- Paired with 007_drop_legacy_password.down.sql. Idempotent (IF EXISTS).
-- ================================================================

ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
