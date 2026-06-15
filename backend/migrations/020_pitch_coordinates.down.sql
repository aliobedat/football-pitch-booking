-- Migration 020 (DOWN) — reverse pitch coordinates confirmation
-- ─────────────────────────────────────────────────────────────────────────────
-- NO-OP by design. latitude/longitude pre-existed migration 020 (see the UP note);
-- dropping them here would destroy pre-existing data this migration did not create.
-- The columns are left intact on purpose.
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;
-- intentionally empty
COMMIT;
