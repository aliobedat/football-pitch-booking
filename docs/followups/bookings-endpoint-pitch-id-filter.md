# Backend: `pitch_id` unsupported on the bookings history endpoint

`GET /admin/bookings` (`bookings.go` → `parseBookingFilter`, `booking_repository.go`
→ `GetAllBookings` / `BookingFilter`) supports `status`, `from`, and `to` filters
but has **no `pitch_id` filter**. `BookingFilter` carries only `Status`, `From`,
`To`, and the repository scopes rows by owner/staff — never by a single pitch.

`BlocksModal.tsx` (commit `f4d97e2`) now issues a date-scoped fetch (`from =
viewDate − 1`, `to = viewDate`) and then filters to the one pitch **client-side**
after the response arrives. That captures most of the payload win via the date
window, but every non-viewed pitch's rows for those two Amman days still cross the
wire before being discarded.

Follow-up: at production data volume, add a real `pitch_id` query param on this
endpoint (parsed in `parseBookingFilter`, applied as a parameterised predicate in
`GetAllBookings`), with owner-scoping enforced the same way `owner_id` is today —
the pitch filter must only ever *narrow* the caller's already-owner-scoped rows,
never reach across tenants. Then the reduction happens before the row hits the
wire, not after, and `BlocksModal` can drop its client-side pitch filter.

---

Status: open — documentation only, no code change.
Out of scope for the frontend date-scoping pass (`BlocksModal.tsx`, commit `f4d97e2`),
which was explicitly frontend-only. Adding the server-side filter is a separate
authorized pass touching the handler + repository + a filter test.
