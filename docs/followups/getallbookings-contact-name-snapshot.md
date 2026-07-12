# Follow-up: GetAllBookings user_name ignores the contact_name snapshot

**Severity:** P3 (consistency nit; no endpoint disagreement today)
**Found:** 2026-07-12 full-project audit
**File:** backend/internal/repository/booking_repository.go (GetAllBookings SELECT)

## Finding

The owner/admin booking list resolves `user_phone` snapshot-first
(`COALESCE(b.contact_phone, u.phone, '')` — fixed in this audit slice to match
`GetBookingContact`), but `user_name` still uses the live `u.full_name` and
ignores the `b.contact_name` snapshot frozen at creation (migration 030,
delta B).

## Impact

After a player renames their profile, owner lists show the new name rather
than the booking-time name. No other endpoint returns a snapshot name, so
nothing disagrees — it is only a semantic inconsistency with the snapshot
design.

## Why deferred

Whether owner-facing lists should show the frozen booking-time name or the
current profile name is a product/display decision, not a confirmed bug.

## Recommended work order

If snapshot semantics are wanted: change `user_name` to
`COALESCE(b.contact_name, u.full_name, '')` and extend
`TestContactSnapshot_ListAgreesWithDetail` to assert the name column the same
way it asserts the phone.
