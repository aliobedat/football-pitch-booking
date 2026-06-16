-- 023_customers.down.sql  (reverse of 023_customers.up.sql)
-- Drops the booking→customer link first (FK dependency), then the table and its
-- indexes. Booking rows are untouched apart from losing the nullable customer_id.

DROP INDEX IF EXISTS idx_bookings_customer_id;
ALTER TABLE bookings DROP COLUMN IF EXISTS customer_id;

DROP INDEX IF EXISTS idx_customers_player_id;
DROP INDEX IF EXISTS idx_customers_owner_id;
DROP INDEX IF EXISTS uq_customers_owner_phone;
DROP TABLE IF EXISTS customers;
