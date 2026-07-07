-- Migration 032 (DOWN) — reverse of 032_amount_paid.up.sql.
--
-- Drops the bounds CHECK first (it depends on the column), then the column. No
-- data preservation: amount_paid is a new tracking field with no downstream
-- table, and payment_status (the legacy synced field) is untouched by this
-- migration, so reverting leaves the pre-032 payment model intact.

ALTER TABLE bookings DROP CONSTRAINT chk_amount_paid_bounds;
ALTER TABLE bookings DROP COLUMN amount_paid;
