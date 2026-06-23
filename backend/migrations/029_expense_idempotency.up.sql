-- 029_expense_idempotency.up.sql  (close MED finding: expense create double-submit)
--
-- Run once against your database (manual psql, per the project convention):
--
--   psql "$DATABASE_URL" -f migrations/029_expense_idempotency.up.sql
--
-- Additive, non-disruptive. A double-tap / network retry of POST /owner/expenses
-- must not create a second ledger row (silent Net-Profit understatement). The
-- client sends an Idempotency-Key (UUID) per expense ATTEMPT; the partial unique
-- index makes a replay collide so the repository returns the ORIGINAL row.
--
--   * idempotency_key is NULLABLE: existing keyless rows stay valid and legacy /
--     keyless writes remain allowed (the partial index permits unlimited NULLs).
--     No backfill required.
--   * Uniqueness scope is (owner_id, idempotency_key) — mirrors the booking
--     idempotency convention (booking_idempotency_keys UNIQUE (user_id, idem_key)):
--     per-owner, so a leaked/guessed key cannot replay across owners.
--   * The index is deliberately NOT filtered on deleted_at: an idempotency key
--     must stay unique even after its row is soft-deleted (a retry must not be able
--     to resurrect a duplicate). Do NOT align this with the other partial indexes
--     on expenses, which filter deleted_at IS NULL for the active-ledger read path.
--   * Plain CREATE INDEX (not CONCURRENTLY): the table is pre-launch / near-empty,
--     keep the migration transaction-safe.

ALTER TABLE expenses ADD COLUMN IF NOT EXISTS idempotency_key TEXT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_expenses_owner_idem
    ON expenses (owner_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
