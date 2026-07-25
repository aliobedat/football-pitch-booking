# Follow-up: operating-hours containment gate is FAIL-OPEN on an unconfigured pitch

**Recorded:** 2026-07-25, during WO-24H-CONTINUITY Gate 1A. Report-only — no
fix attempted under that mandate. Risk is currently **theoretical**: all three
production pitches have a configured schedule (pitches 1 and 2 are 24/7
`00:00→00:00` on all seven weekdays; pitch 3 is `16:00→01:00`), so no live
pitch takes the fail-open branch today.

## 1. The fail-open branch conflicts with the fail-closed invariant

Every operating-hours containment gate treats "this pitch has ZERO configured
windows" as **open 24/7** rather than **closed**, and skips the gate entirely:

- `backend/internal/repository/booking_repository.go` — player `CreateBooking`
  (`if len(windows) > 0 {` … gate … `}`), `CreateManualBooking`, and
  `CreateAcademyBookings` all guard the gate on `len(windows) > 0`.
- `backend/internal/data/operating_hours_resolve.go` — `ResolveOpenWindows` and
  `SlotWithinOpenHours` both return `hasSchedule=false` for a pitch with no
  rows; `SlotWithinOpenHours` additionally returns `contained=true` in that case.
- `backend/internal/repository/day_view_repository.go` — the grid marks every
  unoccupied cell `available` when `!hasSchedule`.

This was a deliberate decision when operating hours shipped (migration 015 /
PR 1: "fail-open on unconfigured"), so that pitches predating the feature stayed
bookable. It nevertheless sits in tension with the project's fail-closed
posture elsewhere (notably the `APP_ENV` gating rule in CLAUDE.md, where any
unrecognised value is treated as production). A pitch that loses its schedule
rows — by an incomplete provisioning flow, a bad bulk edit, or a partial
delete — silently becomes bookable 24/7 rather than refusing bookings.

**Not fixed here** because flipping it is a product decision with a migration-
shaped blast radius: every unconfigured pitch would instantly stop accepting
bookings, and the day view would render entirely `closed`. Any future WO should
decide between (a) flipping to fail-closed with a backfill that gives every
pitch an explicit schedule first, or (b) keeping fail-open but adding an
explicit provisioning check so a pitch cannot go live without one.

## 2. The extend sheet moved that decision from the handler into the data layer

WO-24H-CONTINUITY rewired the booking-extension endpoint
(`backend/internal/handlers/booking_sheet.go`) from `ResolveOpenWindows` +
`data.SlotContained` to the merged gate `SlotWithinOpenHours`. That relocated
where the unconfigured case is decided.

Before — the handler held the fail-open guard explicitly:

```go
intervals, hasSchedule, err := h.hours.ResolveOpenWindows(ctx, pitchID, ammanDate)
if hasSchedule && !data.SlotContained(target.End, newEnd, intervals) { reject }
```

After — the guard lives inside the data-layer call:

```go
contained, _, err := h.hours.SlotWithinOpenHours(ctx, pitchID, target.End, newEnd)
if !contained { reject }
```

**Confirmed behaviorally equivalent for the unconfigured case.** Previously
`hasSchedule == false` short-circuited the `&&` so no rejection could occur;
now `SlotWithinOpenHours` returns `contained = true, hasSchedule = false` on
`len(windows) == 0`, so `!contained` is false and no rejection occurs. Both
paths accept the extension. The handler still receives `hasSchedule` (discarded
as `_`) should a future change need to branch on it.

For the CONFIGURED case the two are intentionally NOT equivalent — that is the
entire point of the WO: containment is now evaluated against the coalesced
union of windows anchored across every day the interval touches, so extending
past midnight on a 24/7 pitch is accepted where it was previously rejected.

## 3. `ResolveOpenWindows` now has zero production callers

The extend sheet was its only consumer. After the rewire,
`(*data.PitchModel).ResolveOpenWindows` (`operating_hours_resolve.go:341`) is
referenced only by comments and by a doc reference in
`operating_hours_resolve_test.go:254`. It is exported, still correct, and
harmless — but it is now dead production code and a plausible future trap: it
is the UNMERGED single-day resolver, so a future caller reaching for the
obvious-looking name would silently reintroduce the 24/7 cross-midnight bug
this WO fixed.

**Not removed here** (out of Gate 1A scope). A future WO should either delete it
or rename it to make the single-day semantics unmissable at the call site.
