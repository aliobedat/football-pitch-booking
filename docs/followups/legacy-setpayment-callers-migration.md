# Follow-up: migrate the legacy SetPayment callers to the new payment body

**Logged by:** WO-BOOKING-SHEET / PR-B, 2026-07-08. File-only; do not implement here.

PR-A extended `PATCH /bookings/:id/payment` to accept a new body form
(`{ amount_paid, total_price? }`) alongside the legacy `{ payment_status }`
toggle. PR-B's Day View booking sheet is the FIRST consumer of the new form and
proves the pattern (partial payment, بridge sync, inline errors).

Three legacy callers still send the old `{ payment_status: 'paid_cash' | 'unpaid' }`
toggle and remain intentionally untouched by PR-B (out of scope):

- `admin-dashboard/app/(dashboard)/schedule/page.tsx` (staff/owner cash toggle)
- `admin-dashboard/app/(dashboard)/calendar/page.tsx`
- `admin-dashboard/app/(dashboard)/bookings/page.tsx` (operational list — nav-hidden for owner/admin, still staff-facing)

They keep working: the backend handler is backward-compatible (legacy body maps
to `amount_paid := total_price` / `NULL`). Once the sheet pattern is validated in
production, migrate these three to the new form so partial-payment tracking is
available everywhere cash is settled. Each is a self-contained page edit; no
backend change needed (the endpoint already supports both).
