# Follow-up: known gaps in the B2C booking card (unfixed, accepted)

**Recorded:** 2026-07-27, closing WO-24H-CONTINUITY. Every item below was found
by adversarial review of that WO and **deliberately left unfixed**. All are
pre-existing or low-impact, and in every case the server rejects cleanly — the
player sees an Arabic error, no bad data is written, and the GIST EXCLUDE
constraint remains the sole conflict referee.

Component: `frontend/app/pitches/[id]/BookingForm.tsx` unless stated otherwise.

## 1. Lead-time floor is never re-evaluated (HIGH of this list)

`serverNow` is fetched **once on mount** from `/api/server-time` and never
re-fetched or ticked, so `minStartMs` is frozen for the page's lifetime and the
start grid never expires. A card left open for 20 minutes still offers starts
that are now inside the server's 15-minute lead window
(`bookingLeadTime`, `backend/internal/handlers/bookings.go`), and the POST is
rejected with `insufficient_lead_time`.

The OTP flow makes the gap routine rather than hypothetical: minutes can pass
between picking a slot and the POST that follows verification. The pre-POST
refresh added in WO-24H-CONTINUITY re-validates *containment* and *occupancy*
but not lead time, so it does not close this.

Fix shape: tick `serverNow` on an interval (or re-read it in the pre-POST
block), and add the lead check alongside the containment re-check.

## 2. Lookahead test pinned only a loose interval

`TestContinuity24h_BookedSlotsLookahead` seeds a 00:30 booking and
`TestContinuity24h_LookaheadCoversSpillOverhang` a 03:30 one, so together they
pin the horizon to "greater than 30 minutes" and "at least 3.5 hours" past
midnight. A mutation to some intermediate value would still pass. The bound is
now `dayEnd + 24h + MaxPlayerBookingDuration`, far outside the pinned range, so
the risk is that a future narrowing goes undetected until it crosses one of the
two seeded points.

Fix shape: assert the computed bound directly, or seed a booking just inside it.

## 3. Dead 23:30 mark when an early next-day booking exists

`availableStartsFor` checks occupancy for the first **30 minutes** of a mark
(`overlapsBooked(absStart, absStart + 30 * 60000, …)`) while the room check
requires **60**. On a 24/7 pitch with a next-day 00:00–01:00 booking, the 23:30
mark passes both (23:30→00:00 is genuinely free) but every duration then fails
`validDur`, so the user gets a selectable chip with all three durations showing
`غير متاح` and a permanently disabled submit button.

Introduced by the interaction of the WO's 23:30 room-check change with the new
booked-slots lookahead — the lookahead reveals exactly the booking that creates
the dead end. Cosmetic (nothing bad is offered), but confusing.

Fix shape: filter a mark when no duration is valid for it.

## 4. No request-sequence guard on the availability effect

The availability `useEffect` has no `AbortController` and no generation ref. A
slow response for pitch A can land after pitch B's and populate `booked`,
`openWindows`, `hoursShape`, and `continuation` for the wrong pitch — the four
move together, so they stay mutually consistent, but they describe a pitch the
user is no longer looking at.

This cannot produce a bad booking: the pre-POST refresh re-fetches with the
current `pitchId` and re-validates containment against that payload. It can
produce a bad *display*. Note the pre-POST refresh's `catch` proceeds on network
failure, so under a blip the mitigation is absent too.

Fix shape: a captured-`pitchId`/`selDayStr` ignore flag in the effect.

## 5. The five-condition extension rule is transcribed twice

The rule (hasSchedule + `hours_shape === 'continuous'` + `continuation_minutes`
> 0 + exactly one served window + that window ends at 1440) exists in
`durationWindows` and again, hand-copied, in the pre-POST containment re-check.
They were verified to match condition-by-condition at the time of writing, and
**neither is covered by a test** — there is no frontend test runner at all (see
`frontend-has-no-test-infrastructure.md`). A future edit touching one copy
produces a card whose displayed offer and pre-POST gate disagree.

Given three versions of this rule were retracted during WO-24H-CONTINUITY, this
is the most likely place the next regression lands.

Fix shape: extract a pure
`containmentWindows(hasSchedule, shape, continuation, windows)` and call it from
both sites — the same extraction the test-infrastructure followup nominates.

## 6. Smaller items

- **`count` semantics widened.** `GET /pitches/:id/availability` returns
  `count: len(booked_slots)`, which now means "bookings overlapping the day plus
  the lookahead", not "bookings on this day". No client reads it. The
  `GetBookedSlots` interface declaration in
  `backend/internal/repository/booking_repository.go` also carries no doc
  comment describing the widened contract — the explanation lives only on the
  implementation.
- **`ResolveDateHours` classifies twice.** It calls `ResolveHoursShape`, then
  calls `ResolveContinuationMinutes`, which calls `ResolveHoursShape` again. Its
  `shape != ShapeContinuous` early return is behaviorally dead, since the inner
  function already returns 0 for every non-continuous shape. Two in-memory scans
  per request; correctness unaffected.
- **Four hardcoded `120`s in the frontend** — the duration ladder
  (`[60, 90, 120]`) and its labels. `data.MaxPlayerBookingDuration`'s doc names
  "three places that must agree"; the frontend ladder is a fourth, unenumerated
  one. If the ceiling were lowered to 90, the card would over-offer immediately.
- **Non-Amman browsers lose the extension.** `buildAbs` constructs in
  browser-local time, so the session axis is browser-local. At UTC+4 a 24/7
  window projects to `[60,1500]`, failing the `end === 1440` condition; at UTC+2
  it projects to `[-180,1260]` and is dropped entirely, rendering an empty card.
  Under-offers in both directions, so the cardinal bar holds, but the second
  case is a broken-looking page for a traveller. Pre-existing.

## Related

- `docs/followups/hours-shape-contract-limits.md` — schedule shapes the client
  deliberately under-serves.
- `docs/followups/frontend-has-no-test-infrastructure.md` — why none of the
  above is caught automatically.
- `docs/followups/operating-hours-gate-fail-open.md` — the unconfigured-pitch
  fail-open.
