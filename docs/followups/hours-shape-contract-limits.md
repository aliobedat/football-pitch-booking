# Follow-up: schedule shapes under-served by the `hours_shape` contract

**Recorded:** 2026-07-26, closing WO-24H-CONTINUITY. Report-only; no fix
attempted. **No production pitch has any of these shapes** — the live schedules
are only two: pitches 1 and 2 (Super Goal, same venue) are 24/7 `00:00→00:00`
single-row, and pitch 3 is `16:00→01:00`. Verified read-only against production.

## The contract

`GET /pitches/:id/availability` serves `open_windows` (day-shaped) plus two
per-date signals: `hours_shape` (`continuous | spill | day_bounded`) and
`continuation_minutes` (how far past the next local midnight the coverage runs,
capped at the player ceiling; 0 unless continuous).

The booking card extends its duration-containment window **only** when every
one of these holds: the pitch is configured, the date is `continuous`, the
served `continuation_minutes` is positive, there is **exactly one** served
window, and that window ends exactly at the midnight seam. Any condition
failing means no extension at all.

## What is under-served, and why it is safe

The client refuses to extend rather than reason about which window earns the
continuation. On the shapes below it therefore **under-offers**: a booking the
server would accept cannot be selected in the UI. It never over-offers *on the
hours dimension*, so the cardinal invariant ("never offer a start+duration the
server would reject") holds for operating-hours containment in every case.

**Scope of that claim — read this before relying on it.** "Never over-offers"
covers containment against operating hours ONLY. It said nothing about
occupancy, and there the same continuation opened a real hole: `GetBookedSlots`
returned bookings overlapping the Amman civil day `[dayStart, dayEnd)`, so a
booking starting at or after the next midnight was invisible to the client. Once
the card could offer a 23:30 start running 120 minutes, it would offer straight
through an existing 00:30 booking and take a 409 from the EXCLUDE constraint —
deterministically, not as a race, on the two production 24/7 pitches. Fixed in
the same change: `GetBookedSlots` now looks ahead
`dayEnd + data.MaxPlayerBookingDuration`, exactly as far as a same-day start can
reach, so nothing collidable is hidden and nothing unreachable is added
(`TestContinuity24h_BookedSlotsLookahead`, `..._LookaheadDoesNotOverreach`).

The lesson worth keeping: an invariant stated over one dimension does not
transfer to another. Widening what the UI may offer requires re-checking every
constraint the server applies, not just the one being changed.

### 1. Split shift with a midnight-abutting window
e.g. Mon `09:00→12:00` + Mon `20:00→00:00`, Tue `00:00→08:00`.
Classified `continuous`. Two served windows, so no extension: a 23:30→00:30
booking is refused by the card although the server accepts it. The refusal is
deliberate — an earlier implementation extended *every* window and offered
11:30→12:30 straight across the real 12:00–20:00 closure, which the server
rejected. Losing the evening continuation is the safe trade.

### 2. Short next-day window
e.g. Mon `16:00→00:00`, Tue `00:00→01:00`.
Classified `continuous` with `continuation_minutes = 60`. This shape IS handled
correctly (one window, ends at the seam), and the client extends by the served
60 rather than the ceiling — so 23:30+60 is offered and 23:30+120 is not. Listed
here only because a client that hardcoded 120 would over-offer by an hour; the
served value is what prevents it.

### 3. Spill AND continuous on the same date
e.g. Mon `16:00→02:00`, Tue `00:00→00:30`.
`ResolveHoursShape` tests abutment before spill, so `continuous` wins and the
`spill` fact is discarded. Two consequences:

- No served window ends at 1440 (the only window ends at 1560), so the client's
  seam condition fails and no extension occurs. Harmless — that window already
  reaches past midnight on its own.
- **Lost inventory.** Because the date reports `continuous`, the card suppresses
  the بعد منتصف الليل group, discarding the marks ≥1440 it generated. Those
  instants are reachable from the next day's chip only if that day's own window
  covers them. In the example it does not (Tue's window is `00:00→00:30`, too
  short for the 60-minute minimum), so the 00:30–02:00 slots are unreachable
  from any chip while the server would accept them.

This is the one place the three invariants genuinely conflict. Priority is
never-over-offer > no-double-render > no-lost-inventory, so inventory is what we
lose, and this note is the required documentation of that loss.

## Root cause and what a real fix needs

`hours_shape` is a lossy three-value enum: a date can be *both* spilling and
abutting, and the precedence discards the fact the renderer needs. No value of
`continuation_minutes` repairs it — the missing information is which window owns
the after-midnight slots, not how long the continuation is.

A full fix is a **contract redesign**, not a patch. The candidates considered:

- Serve the continuation as an **absolute end instant** instead of an offset,
  removing the need for the client to locate any window.
- Serve per-window metadata (which window is seam-eligible) so the client never
  infers.
- Carry the spill fact alongside the shape rather than letting `continuous`
  mask it.

**Not done** because no production pitch has these shapes and the failure mode
is fail-safe. Revisit if a partner venue configures a split shift, a next-day
window shorter than the booking ceiling, or an evening window that spills past a
next-day opening — the first such schedule should trigger the redesign rather
than another incremental rule.

## Related

- `docs/followups/operating-hours-gate-fail-open.md` — the unconfigured-pitch
  fail-open, and why `ResolveOpenWindows` is kept but warned against.
- The client rule lives in `durationWindows` in
  `frontend/app/pitches/[id]/BookingForm.tsx`; the classifier is
  `ResolveHoursShape` / `ResolveContinuationMinutes` in
  `backend/internal/data/operating_hours_resolve.go`.
