# Ledger: schema-file ↔ live-DB drift citations

**Status: RECONCILED 2026-07-09 (WO-SCHEMA-DRIFT-RECONCILIATION).**
`database/schema.sql` was regenerated from the live schema
(`pg_dump --schema-only --no-owner --no-privileges`) and is now the canonical
scratch/test baseline — every citation below is baked into it. All known
fixture breakage is repaired (`bsEnv.mkPitch` in PR #40;
`schedule_payload_db_test.go`'s `mkPitchRate` in this WO).

**Scratch DB recipe (current):** `CREATE DATABASE scratch_<name>` →
`ALTER DATABASE scratch_<name> SET search_path TO public` → load
`database/schema.sql` → connect via the **unpooled** Neon host (drop
`-pooler` from the hostname; the pooler resets session defaults and rejects
startup options). Do **NOT** replay `backend/migrations/002–032` on such a
scratch — they are already baked into the regenerated baseline; they remain in
the repo as history and as the manual-apply path for production.

**Maintenance rule:** no `schema_migrations` table exists; migrations are still
applied to Neon manually, so after each new production migration, regenerate
`database/schema.sql` with the command above. New drift citations go below.

## Citations (all reconciled by the 2026-07-09 regeneration)

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

- **2026-07-09 (WO-SERIES-CANCEL incident fix):** the same pitches drift bit
  test fixtures — `bsEnv.mkPitch` (`booking_sheet_db_test.go`) and the inline
  `mkPitch` in `schedule_payload_db_test.go` seed pitches via raw INSERTs that
  omit `neighborhood`/`surface`/`format` (NOT NULL live), so those suites die at
  fixture setup on a faithful live-schema scratch. `bsEnv.mkPitch` was repaired
  (defaults added, fixture-only) with the incident fix; the
  `schedule_payload_db_test.go` fixture is still unrepaired — fix alongside the
  schema reconciliation.
