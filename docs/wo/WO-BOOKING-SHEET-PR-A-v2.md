# WO-BOOKING-SHEET / PR-A — v2 (record + Gate 2 amendment)

The full ratified WO-BOOKING-SHEET / PR-A v2 (Booking Extension + Partial
Payment Tracking — Backend Foundation) was dispatched in-session. This file
records the Gate-2 scope amendment so the divergence from the WO's §4 scope line
is documented in-tree.

Summary of scope: migration 032 (`bookings.amount_paid`), a new
`PATCH /bookings/:id/extend` endpoint, a backward-compatible extension of the
existing `PATCH /bookings/:id/payment` handler, additive Day View payload fields,
and DB-backed tests. `amount_paid` is the source of truth; `payment_status`
remains a synced legacy field for the frozen collected-cash consumers.

---

### §4.2 AMENDMENT (Gate 2 ruling — supersedes the scope line for this endpoint only)

- Payment endpoint retains the staff-aware scope (scopePredicate + BoundPitchIDs).
  Route guard RequireRole("staff","owner","admin") unchanged.
- Fetch-within-scope, then branch:
  - not-in-scope → 404
  - source='block' → 409 not_a_booking
  - status='cancelled' → 409 booking_cancelled
  - else apply body form.
- Staff carve-out:
  - staff may use legacy payment_status toggle on bound pitches
  - staff may use new form with amount_paid only on bound pitches
  - staff may NOT send total_price
  - staff sending total_price → 403 price_change_forbidden
  - row unchanged
- Owner/admin may use legacy toggle, amount_paid, and total_price.
- Error-code migration on live endpoint signed off:
  - out-of-scope 403 → 404
  - cancelled → 409
  - success paths and request bodies unchanged.
- Extend endpoint (§4.1) unaffected:
  - owner/admin only
  - OwnerScopeFilter → 404
  - staff cannot extend bookings.

### Gate 3 additions

20. Staff on bound pitch:
- legacy toggle → 200
- new form amount_paid only → 200
- bridge sync correct

21. Staff on bound pitch sending total_price:
- 403 price_change_forbidden
- row unchanged

22. Staff on unbound pitch, any payment form:
- 404

23. Staff attempting PATCH /bookings/:id/extend:
- route-guard rejection
- booking unchanged
