-- 024_payment_paid_cash.up.sql  (Phase 2 / WO-F1 — Cash-Settlement Marker)
--
-- Activates the DORMANT payment_status seam reserved in migration 003. We are NOT
-- building a payment system: there is no gateway, deposit, or refund logic. This
-- adds a single manual settlement state — 'paid_cash' — that an owner/staff member
-- toggles on a booking when cash is collected (cash-native market). The default
-- stays 'unpaid'; receivables/debt tracking is explicitly out of scope.
--
-- ┌─ NON-TRANSACTIONAL REQUIREMENT (read before applying) ───────────────────────┐
-- │ `ALTER TYPE ... ADD VALUE` CANNOT run inside a transaction block. This file   │
-- │ therefore contains EXACTLY ONE statement and MUST be executed standalone      │
-- │ (auto-commit) — NOT wrapped in BEGIN/COMMIT and NOT bundled with other        │
-- │ statements in a single multi-statement send (Postgres groups those into one   │
-- │ implicit transaction, which would raise                                       │
-- │   "ALTER TYPE ... ADD VALUE cannot be executed inside a transaction block").   │
-- │ IF NOT EXISTS makes the apply idempotent / safe to re-run.                     │
-- └──────────────────────────────────────────────────────────────────────────────┘

ALTER TYPE payment_status ADD VALUE IF NOT EXISTS 'paid_cash';
