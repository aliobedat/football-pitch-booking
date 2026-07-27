# Follow-up: جدول الملعب is civil-day bounded (Defect A, deferred)

**Recorded:** 2026-07-27, closing WO-OWNER-SLOTS Gate 1. Report-only — Option
(i) below was explicitly NOT built. The owner ruled: ship the current behaviour
and document that `/calendar` is the working path for cross-midnight manual
bookings today.

## What the owner reported

"On the Sunday pitch-schedule view, picking 12:00 ص for a 00:00→01:00 booking is
rejected as *in the past*." The instinct was that this is the same
`buildDateTime` same-day trap fixed in the player card during WO-CROSS-MIDNIGHT.

**It is not.** Gate 0 recon found no such trap anywhere in the admin app — all
three creation forms do correct absolute-instant arithmetic:

- `components/DayViewManualSheet.tsx` — `new Date(new Date(start).getTime() + duration * 60_000)`,
  where `start` is the server grid's authoritative UTC instant.
- `app/(dashboard)/calendar/page.tsx` (`CreateManualModal`) — `new Date(startMs + duration * 60_000)`.
- `components/BlocksModal.tsx` — `ammanInstant(viewDate, hour)`, which builds a
  literal `YYYY-MM-DDTHH:00:00+03:00` and even accepts `hour = 24`.

The real cause is a **model mismatch**: the owner's day is a CIVIL day
everywhere in the admin surface, while the player's day became a SESSION day in
WO-24H-CONTINUITY. Sunday's grid genuinely stops at Mon 00:00, so "Sunday
night's midnight" has no home on Sunday's screen. Both the client past-guard
(`BlocksModal.tsx`, `h < nowHour` → `وقت منقضٍ`) and the server past-check
(`handlers/bookings.go`, "cannot log a booking entirely in the past") are
correct about the instant they were handed — the instant offered is simply the
wrong midnight.

## Day View is structurally incapable; Calendar already works

**جدول الملعب cannot express it, in two independent ways.** The grid is bounded
by `AmmanDayBoundsUTC` in `backend/internal/repository/day_view_repository.go`
(`buildGrid` loops `fromUTC → toUTC`), and the sheet's DURATION is clamped on
top of that: `contiguousMinutes()` counts contiguous *available cells*, gating
the start list (≥60 min), the duration chips, the stepper, and a shrink effect.
On Sunday, 23:30 has one cell left (30 min < the 60-minute floor) so it is not
offered at all, and 23:00 offers exactly 60 minutes — ending precisely at Mon
00:00, never past it. Neither starts nor duration headroom exist after midnight.

**`/calendar` already supports it.** Its timeline window comes from
`computeWindow(allWindows, BUFFER_MIN, …)` in `admin-dashboard/lib/calendar.ts`,
derived from the pitch's **unclipped** `open_windows` (a `ConcreteInterval` whose
`End` may fall on the next calendar day) plus a 60-minute buffer each side — not
from civil-day bounds. `DURATIONS` is a fixed `[60, 90, 120]` with no clamp. So
on a `16:00→01:00` pitch an owner can already tap 23:30, choose an hour, and
create Mon 00:30; the write path accepts it, because after WO-24H-CONTINUITY
`CreateManualBooking` runs the merged containment gate and carries no lead-time
or max-duration gate on owner paths.

**Operationally: use `/calendar` (التقويم) for cross-midnight manual bookings.**

## Option (i) — session-day grid for Day View (not built)

Extend the day-view payload past midnight using the `hours_shape` /
`continuation_minutes` machinery already shipped in WO-24H-CONTINUITY, so
جدول الملعب and the player card finally agree on what a "day" is.

Cost, and why it was deferred:

- **Backend `buildGrid` change.** The cell loop and `loadOccupancy` are bounded
  by `AmmanDayBoundsUTC`; both would need session-day bounds. Neither
  `hours_shape` nor `continuation_minutes` is consumed anywhere in the admin app
  today (grep returns zero hits in `day_view_repository.go`), so the contract
  would have to be threaded through first.
- **The additions-only proof must be re-established.** WO-24H-CONTINUITY's D-B
  ruling rests on `backend/internal/data/operating_hours_resolve.go` being
  additions-only, which is what guarantees `SearchAvailability`, `GetOpenWindows`,
  the day view and the calendar are unchanged *by construction*. Any edit that
  touches the shared resolver re-opens that proof and must re-close it.
- **Summary counters bucket by civil day.** `DayViewSummary.ConfirmedRevenue`
  mirrors analytics semantics — `SUM(total_price)` over bookings whose START
  falls in the Amman civil day — and `TotalBookings`/`BookedHours` are computed
  from the same civil-day occupancy set. A session-day grid would silently change
  what those numbers mean, and they are numbers the owner reads daily.
- **Blast radius.** `components/BlocksModal.tsx` carries its own independent
  24-hour civil grid (`HOURS = 0…23`) with the same assumption, so a coherent fix
  spans two tools, not one.

**Revisit when** an owner actually needs to create or review after-midnight
occupancy from جدول الملعب rather than التقويم. Until then the calendar covers
the need and the day view stays truthful about the day it is showing.

## Adjacent, unfixed: `fetchDay`'s catch is not `!silent`-guarded

Found during WO-OWNER-SLOTS adversarial review; **pre-existing, not introduced
there, and deliberately not fixed under that WO's scope.**

In `admin-dashboard/app/(dashboard)/day-view/page.tsx`, `fetchDay`'s `.catch`
sets the error state without checking `silent`, and its `setError(null)` is not
sequence-guarded either. A *silent* refetch — the kind a booking sheet fires
after a payment or an extension — therefore replaces the entire rendered
timeline with the error box if it fails, rather than leaving the grid on screen
as the `silent` flag intends.

The sibling `/schedule` screen now does guard both (its `load` checks
`!silent && seq === reqSeq.current` before touching `error` or `loading`), so
the two pages currently differ in a way that is not deliberate. Align
`fetchDay` with that pattern in a future WO.

## Related

- `docs/followups/hours-shape-contract-limits.md` — the shapes the player card
  deliberately under-serves, and the lossy `hours_shape` enum.
- `docs/followups/booking-card-known-gaps.md` — the B2C card's known gaps.
- `docs/followups/frontend-has-no-test-infrastructure.md` — why none of the
  admin-side rendering logic is covered by an automated test.
