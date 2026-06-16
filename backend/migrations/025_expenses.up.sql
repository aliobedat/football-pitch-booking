-- 025_expenses.up.sql  (Phase 2 / WO-F2 — Expense Ledger & Net Profit)
--
-- Additive, non-disruptive: creates one new table, touches nothing existing.
-- Cash-basis: Net Profit = (WO-F1 paid_cash COLLECTED) − Σ(expenses), per Amman period.
--
-- MONEY TYPE: amount is NUMERIC(10,3) — IDENTICAL to bookings.total_price (verified
-- against the live schema). JOD is a 3-decimal currency (1000 fils); reusing the
-- exact type/scale keeps (collected − expenses) lossless and type-consistent. No
-- cents/2-decimal assumption, no new money representation.
--
-- owner_id references users(id): owners are users in this schema (same convention
-- as pitches.owner_id / customers.owner_id). ON DELETE RESTRICT — an owner with
-- ledger history cannot be silently removed.
--
-- occurred_at is TIMESTAMPTZ, bucketed by `AT TIME ZONE 'Asia/Amman'` exactly like
-- the F1 collected aggregation, so revenue and expenses share one period basis.

CREATE TABLE IF NOT EXISTS expenses (
    id          BIGSERIAL    PRIMARY KEY,
    owner_id    INTEGER      NOT NULL REFERENCES users (id)   ON DELETE RESTRICT,
    -- NULL = business-wide / overhead (e.g. a general facility guard) — NEVER
    -- misattributed to a pitch. ON DELETE SET NULL preserves the expense as
    -- overhead if a pitch is ever hard-deleted (pitches normally soft-delete).
    pitch_id    INTEGER      NULL     REFERENCES pitches (id) ON DELETE SET NULL,
    category    TEXT         NOT NULL,
    -- Mirror bookings.total_price exactly: NUMERIC(10,3) (JOD, 3 dp / fils).
    amount      NUMERIC(10,3) NOT NULL CHECK (amount >= 0),
    -- The instant the cost was incurred. Bucketed in Asia/Amman (revenue-consistent).
    occurred_at TIMESTAMPTZ  NOT NULL,
    note        TEXT         NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- Soft delete: ledger history is preserved; reads filter deleted_at IS NULL.
    deleted_at  TIMESTAMPTZ  NULL,

    -- Fixed preset categories (clean analytics). 'Other' may carry a free-text note.
    CONSTRAINT expenses_category_chk
        CHECK (category IN ('Electricity', 'Staff', 'Water', 'Maintenance', 'Marketing', 'Other'))
);

-- Period ledger reads filter by owner + occurred_at; the partial index skips
-- soft-deleted rows (the common "active ledger" path).
CREATE INDEX IF NOT EXISTS idx_expenses_owner_occurred
    ON expenses (owner_id, occurred_at) WHERE deleted_at IS NULL;

-- Supports the (future) per-pitch attribution + owner-scoped pitch filtering.
CREATE INDEX IF NOT EXISTS idx_expenses_owner_pitch
    ON expenses (owner_id, pitch_id) WHERE deleted_at IS NULL;
