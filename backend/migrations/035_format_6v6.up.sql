-- Migration 035 (UP) — WO-FORMAT-6V6: add six-a-side to the pitch format enum.
--
-- Standalone statement, deliberately NO transaction block: on PG17,
-- ALTER TYPE ... ADD VALUE is permitted inside a transaction (since PG12) but
-- the new value cannot be USED until that transaction commits — running it
-- bare avoids the foot-gun entirely and matches how this file is applied
-- (single psql statement).
--
-- Position BEFORE 'سباعي' keeps the enum listing in playing-size order
-- (nothing sorts by enum order; cosmetic only).

ALTER TYPE public.pitch_format ADD VALUE IF NOT EXISTS 'سداسي' BEFORE 'سباعي';
