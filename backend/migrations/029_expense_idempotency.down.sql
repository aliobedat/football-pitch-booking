-- 029_expense_idempotency.down.sql — reverse 029 (drop index, then column).

DROP INDEX IF EXISTS idx_expenses_owner_idem;

ALTER TABLE expenses DROP COLUMN IF EXISTS idempotency_key;
