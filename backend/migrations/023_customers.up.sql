-- 023_customers.up.sql  (Cockpit WO1 — The Regulars CRM)
-- SCHEMA ONLY. No data movement: the existing-booking association is performed by
-- a SEPARATE, idempotent, owner-scoped backfill job run after this migration (so
-- the schema change stays trivially reversible and the data step is reviewable).
--
-- A `customer` is an OWNER-SCOPED contact: EITHER linked to a platform player
-- (player_id) OR a standalone owner-entered walk-in (name + phone). De-duplication
-- is by normalised E.164 phone WITHIN one owner — the SAME phone under two owners
-- is two independent rows (NO cross-owner sharing, by design). Notes are private to
-- the owning tenant.

CREATE TABLE IF NOT EXISTS customers (
    id          BIGSERIAL   PRIMARY KEY,
    -- The tenant boundary. Every CRM read/write filters on this via the canonical
    -- owner-scope predicate; admin is unscoped per the Actor model.
    owner_id    INTEGER     NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Set when this customer is a registered platform player; NULL for a standalone
    -- owner-entered walk-in contact. ON DELETE SET NULL: deleting the user account
    -- must not erase the owner's CRM contact/history.
    player_id   INTEGER     NULL     REFERENCES users (id) ON DELETE SET NULL,
    -- Display name: the player's full_name snapshot or the owner-entered guest name.
    name        TEXT        NULL,
    -- Normalised E.164 (the internal/phone rule, +962 default). The dedup key.
    phone       TEXT        NOT NULL,
    -- Owner's private free-text notes about this customer.
    notes       TEXT        NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- E.164 shape, mirroring users_phone_e164_chk — the dedup key is always canonical.
    CONSTRAINT customers_phone_e164_chk CHECK (phone ~ '^\+[1-9][0-9]{1,14}$')
);

-- De-dup: one customer per phone per owner. This is the match-or-create target and
-- the structural guarantee against duplicate contacts within a tenant.
CREATE UNIQUE INDEX IF NOT EXISTS uq_customers_owner_phone
    ON customers (owner_id, phone);

-- Owner-scoped "list my customers" filters by owner_id; the player-link backfill
-- and go-forward association look up by (owner_id, player_id).
CREATE INDEX IF NOT EXISTS idx_customers_owner_id  ON customers (owner_id);
CREATE INDEX IF NOT EXISTS idx_customers_player_id ON customers (player_id);

-- Explicit booking→customer link (approved: explicit FK over derived). NULL until
-- associated (by the backfill for existing rows, or go-forward at create time).
-- ON DELETE SET NULL: removing a customer never deletes booking/occupancy history.
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS customer_id BIGINT NULL REFERENCES customers (id) ON DELETE SET NULL;

-- The CRM profile aggregates booking history per customer (count, last-booked,
-- no-show tally) — all keyed on this column.
CREATE INDEX IF NOT EXISTS idx_bookings_customer_id ON bookings (customer_id);
