-- 025_expenses.down.sql  (reverse of 025 — additive table, cleanly reversible)
-- Dropping the table removes its indexes and constraints with it. No other object
-- references expenses, so this is non-disruptive.

DROP TABLE IF EXISTS expenses;
