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

- **2026-07-08 (WO-REPORTS-COLLECTED / PR-C scratch run):** the live `pitches`
  table has drifted substantially from `database/schema.sql` + all migrations —
  the file schema is missing columns the app's `data.PitchModel.CreatePitch`
  depends on, so any DB test that seeds via `CreatePitch` (e.g. the reports
  repository suite) cannot run against a file-built scratch DB. Missing on the
  file side: `neighborhood`, `surface` (enum), `format` (enum), `amenities`,
  `rating`, `pitch_hue`, `is_featured`, `review_count`. Conversely `schema.sql`
  still declares `surface_type` + `capacity` (NOT NULL), which the live schema
  no longer matches (surface/format enums replaced surface_type; capacity is
  defaulted out-of-band — see the prior citation). No migration file reconciles
  any of this; the live pitches schema was evolved out-of-band. Fix candidate:
  regenerate `schema.sql` from a live `pg_dump --schema-only`, or add migrations
  codifying the enum columns + drops. NOT fixed in PR-C (out of scope).
