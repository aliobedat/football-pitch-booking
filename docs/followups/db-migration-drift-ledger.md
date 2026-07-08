# Ledger: schema-file ↔ live-DB drift citations

**Status:** open — no `schema_migrations` table exists; migrations are applied
to Neon manually, so the repo's SQL files and the live database can disagree.
Each confirmed drift gets cited here until a proper ledger/migration runner
lands. Verify against the live schema before trusting file presence.

## Citations

- **2026-07-08 (WO-BOOKING-SHEET / PR-B.2a scratch run):**
  `database/schema.sql` declares `pitches.capacity SMALLINT NOT NULL CHECK
  (capacity > 0)` with **no default**, but the live schema evidently has a
  default (or nullable) — every DB-suite `mkPitch` insert omits `capacity` and
  the suites pass against scratch DBs built from the live schema. A scratch DB
  built from `schema.sql` + all migrations failed with `null value in column
  "capacity"` until `ALTER TABLE pitches ALTER COLUMN capacity SET DEFAULT 10`
  was applied. No migration file touches `capacity`; the live default was set
  out-of-band. Fix candidate: a migration codifying the default, or correcting
  `schema.sql`.
