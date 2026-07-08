# Followup: schedule PATCH responses emit zero-valued money fields

**Filed:** 2026-07-08 (WO-BOOKING-SHEET / PR-B.2a)
**Status:** open — cosmetic wire inconsistency, no consumer affected

## What

PR-B.2a added five additive fields to `ScheduleRow` (`total_price`,
`amount_paid`, `payment_display`, `remaining`, `price_per_hour`), populated
and derived only in `DailySchedule` (GET /schedule). The other two methods
that serialize a `ScheduleRow` — `SetAttendance` and `SetPayment`
(`PATCH /bookings/:id/attendance`, legacy dbadmin probe) — scan a RETURNING
clause that does not include the new columns, so their JSON responses now
carry the keys as Go zero values: `total_price: 0`, `amount_paid: null`,
`payment_display: ""`, `remaining: null`, `price_per_hour: 0`.

## Why it's deferred, not fixed

- No consumer reads these PATCH response bodies for money state: the schedule
  page is optimistic-with-rollback and ignores the body; the sheet flow uses
  `ApplyPayment`, which returns a fully-derived `BookingSheet`.
- Fixing it means extending two RETURNING clauses + derivation in methods the
  PR-B.2a scope froze ("no other change than the DailySchedule SELECT").

## Fix when touched next

Extend the `SetAttendance` RETURNING (and `SetPayment`, if it survives) with
`b.total_price::float8, b.amount_paid::float8, p.price_per_hour` and apply the
same `round3` + `derivePayment` post-scan block `DailySchedule` uses — or
retire the legacy paths entirely per
[legacy-setpayment-callers-migration](legacy-setpayment-callers-migration.md),
which migrates the remaining legacy `payment_status` callers (calendar +
bookings pages) onto the sheet/new-form flow and would make `SetPayment`
dead code.
