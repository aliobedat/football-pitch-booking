-- Migration 032 (UP) — bookings.amount_paid: partial cash-payment tracking
-- (WO-BOOKING-SHEET / PR-A).
--
-- WHY: the dormant `payment_status` seam (unpaid | paid_cash, migration 024) is
-- a binary settled/not-settled marker — it cannot express a partial cash payment.
-- `amount_paid` becomes the source of truth for how much cash has actually been
-- collected on a booking; `payment_status` remains a SYNCED legacy field so the
-- frozen collected-cash consumers (analytics/net-profit/reports) keep working
-- unchanged (they key on payment_status='paid_cash'). The sync is written
-- atomically in the payment PATCH path — see the endpoint, not this migration.
--
-- NUMERIC(10,3) mirrors bookings.total_price EXACTLY (same precision/scale), so
-- the bridge's equality comparison `amount_paid = total_price` is exact — no
-- cross-scale rounding. The column is NULLABLE with NO default and NO backfill:
-- every existing row stays NULL, meaning "untracked" (distinct from 0 = "tracked,
-- nothing paid yet"). Payment display state is DERIVED in the handler, never
-- stored beyond the legacy sync.
--
-- chk_amount_paid_bounds is the schema backstop for the endpoint validation:
-- a tracked amount is always within [0, total_price]. NULL is always allowed
-- (untracked). Paired with 032_amount_paid.down.sql.

ALTER TABLE bookings
    ADD COLUMN amount_paid NUMERIC(10,3) NULL;

ALTER TABLE bookings
    ADD CONSTRAINT chk_amount_paid_bounds
    CHECK (
        amount_paid IS NULL
        OR (amount_paid >= 0 AND amount_paid <= total_price)
    );
