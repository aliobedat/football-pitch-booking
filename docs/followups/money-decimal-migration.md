# Money path: migrate float64 → decimal-safe representation

Every JOD amount is stored as `NUMERIC(10,3)` (3 dp / fils — `bookings.total_price`,
`expenses.amount`), but the entire read path is `float64`:

- SQL aggregates cast `::float8` on the final `SUM()`
  (`analytics_repository.go`, `reports_repository.go`).
- Go structs carry `float64` (`models/booking.go` `TotalPrice`,
  `repository.RevenueSummary`, `repository.FinancialReportSummary`, …).
- Handlers round with `round3(v) = math.Round(v*1000)/1000` (`financials.go:34`)
  and `gin` serialises JSON numbers.

This is the ratified house pattern for R1 (Reports, 2026-07-06): **sum in SQL over
NUMERIC, single `::float8` cast on the final aggregate, `round3` at the handler,
JSON numbers — never sum in Go or client-side.** At JOD magnitudes under
`NUMERIC(10,3)` (max ~9,999,999.999) the float64 representation of a 3-dp sum is
exact after `round3`, so the pattern is safe today.

Follow-up: when a payment gateway lands (or amounts/aggregation windows grow),
migrate the money path to a decimal-safe representation end to end:

- scan `NUMERIC` via `pgtype.Numeric` (or a decimal library) instead of `::float8`,
- serialise amounts as JSON **strings** (`"45.500"`) or integer fils,
- delete `round3` (rounding belongs in SQL / the decimal type, not float math).

That is a breaking API change for every consumer of `total_price` / revenue
figures (dashboard, reports, CSV export), so it must be one coordinated pass —
do NOT convert endpoints piecemeal, or the same amount will render differently
across surfaces.

---

Status: open — documentation only, no code change.
Created by the Reports R1 PR, whose ratified definitions explicitly voided the
"no float64" rule in favour of the existing house pattern.
