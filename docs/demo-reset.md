# Demo reset (WO-DEMO-02)

`backend/cmd/demo-reset` resets the dedicated Demo Neon database to a small,
deterministic dataset for showing Marma to prospective pitch owners. It is a
standalone tool — not wired into the API server — run manually.

## One-time setup (per Demo database)

Install the Demo-only marker once, by hand, against the Demo database:

```
psql "$DEMO_DATABASE_URL" -f database/demo/marma_demo_marker.sql
```

Never run this against Production. The marker is what `demo-reset` checks
before touching any data — without it, the tool refuses.

## Workflow

1. **Stop the Demo backend.** Reset runs against the same database the API
   uses; stopping it first avoids concurrent writes racing the reset
   transaction.
2. **Run the reset:**
   ```
   APP_ENV=demo DEMO_OWNER_PASSWORD='<demo owner password>' \
     DATABASE_URL='<demo database url>' \
     go run ./cmd/demo-reset --confirm-reset
   ```
   `DATABASE_URL` may instead come from `backend/.env` (loaded automatically
   if present, matching the rest of the backend's local tooling).
3. **Check the result.** On success the tool prints a row-count report and
   `DEMO RESET SUCCESSFUL`. On any guard failure it prints `DEMO RESET
   REFUSED` and exits non-zero without touching the database. On any
   mid-reset failure it prints `DEMO RESET FAILED: ...`, rolls back, and the
   database is left exactly as it was before the run.
4. **Start the Demo backend.**
5. **Run a quick booking smoke test** — log in as the seeded Demo owner, open
   the schedule for either pitch, and create/cancel one test booking to
   confirm the app is reading the freshly seeded data correctly.

## Safety guards (checked in order, before any DELETE or INSERT)

1. `APP_ENV` must equal `demo` exactly.
2. `--confirm-reset` must be passed.
3. `DEMO_OWNER_PASSWORD` must be set and non-empty.
4. The connected database must contain the exact marker row installed by
   `database/demo/marma_demo_marker.sql` (table `marma_demo_marker`, value
   `MARMA_DEMO_DATABASE_ONLY`).

Guards 1-3 require no database connection at all — they fail before any I/O.
Guard 4 is a read-only check that runs before the reset transaction opens.
Any guard failure prints `DEMO RESET REFUSED`, exits non-zero, and performs
zero database mutations.

## What gets deleted / preserved

Deletes ALL rows from an explicit, FK-safe (child-before-parent) table list —
because the Demo database is dedicated to Demo only, a full delete is safe
(no row-tagging or scoping needed): `message_deliveries`, `notification_jobs`,
`booking_idempotency_keys`, `reviews`, `status_transitions`, `bookings`,
`pitch_audit_log`, `staff`, `expenses`, `operating_hours`, `customers`,
`pitches`, `venues`, `refresh_tokens`, `users`.

Preserved (never in the delete list): `marma_demo_marker`, and every other
table not listed above — schema/enum definitions, `otp_codes`,
`otp_rate_events`, `waba_daily_sends`, and the database structure itself. No
`DROP`, no `TRUNCATE CASCADE`, no migration replay.

## Seeded dataset

- 1 owner (`role='owner'`, bcrypt password hash from `DEMO_OWNER_PASSWORD`)
- 1 venue, 2 pitches (operating hours 08:00–23:00 every day, distinct prices)
- 6 synthetic customers (fictional Arabic names, synthetic `+962790000xxx`
  phone numbers)
- 12 bookings (`source='manual'`), all dates computed relative to reset time
  in `Asia/Amman`:
  - 4 past, 3 today, 5 future
  - 2 cancelled, the rest confirmed
  - a mix of tracked (`amount_paid` = full `total_price`) and untracked
    (`amount_paid = NULL`) payment examples
  - both pitches used; every booking occupies a distinct pitch+day+hour slot,
    so the `bookings_pitch_id_booking_range_excl` GIST exclusion constraint is
    satisfied by construction — the reset never needs to touch or bypass it
- 3 expenses (`Electricity`, `Maintenance`, `Water`)
- 0 staff, 0 notification jobs (not needed for any existing page to render
  correctly)

Running the reset twice in a row produces the exact same row counts both
times — the dataset is fully deterministic given the schema.
