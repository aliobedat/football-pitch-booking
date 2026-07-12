-- Migration 035 (DOWN) — intentional no-op.
--
-- PostgreSQL has no ALTER TYPE ... DROP VALUE. Removing 'سداسي' would require
-- rebuilding the pitch_format type and rewriting the pitches.format column
-- (and would fail outright once any row uses the value). The addition is
-- ACCEPTED AS IRREVERSIBLE (WO-FORMAT-6V6 Phase 0 ruling); if a rollback is
-- ever truly needed, treat it as a new forward migration with a full
-- type-rebuild plan, not a down file.

SELECT 1; -- no-op
