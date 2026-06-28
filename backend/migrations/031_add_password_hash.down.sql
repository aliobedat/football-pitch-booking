-- Migration 031 (DOWN) — Drop users.password_hash again.
--
-- Removes the column re-added by 031_add_password_hash.up.sql. Any provisioned
-- bcrypt hashes are destroyed with it; the phone+password login path then has no
-- backing column and every attempt fails closed (401). Idempotent (IF EXISTS).
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
