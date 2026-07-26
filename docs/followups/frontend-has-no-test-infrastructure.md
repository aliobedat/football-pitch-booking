# Follow-up: the frontend has no test infrastructure

**Recorded:** 2026-07-26, closing WO-24H-CONTINUITY. Report-only — a test runner
was explicitly NOT built as part of that WO. Recommended as its own post-launch
work order.

## The gap

There is no test infrastructure under `frontend/` at all: no `*.test.*` /
`*.spec.*` files, and no runner (vitest, jest, or otherwise) in
`frontend/package.json`. The only automated checks a frontend change passes are
`tsc --noEmit` and `next build`, neither of which evaluates behaviour.

## Why this specifically mattered

WO-24H-CONTINUITY went through **two retracted rules** before the shipped one.
Both lived entirely in `durationWindows` / `availableStartsFor` in
`frontend/app/pitches/[id]/BookingForm.tsx`:

1. *Extend every window by a flat 120 minutes when the date is `continuous`.*
   Fabricated open hours on a split-shift schedule — the card offered
   11:30→12:30 straight across a real 12:00–20:00 closure.
2. *Extend the window whose end reaches midnight, by `min(120, next-day run)`.*
   Has no anchor on a date that both spills and abuts, where no served window
   ends at midnight.

Both were caught by human review, and **neither would have been caught by the
test suite** — including the good Go tests added in the same WO. Those tests
assert what the *server* accepts and rejects, which was never in question: the
server correctly rejected 11:30→12:30 the whole time. What went wrong was the
client offering it anyway.

Concrete mutations that pass every test in the repository today:

- Delete the `sessionWindows.length !== 1` guard and map every window to
  `end + continuation` — restores retracted rule 1 verbatim.
- Delete the `w.end !== 1440` guard — restores retracted rule 2.
- Replace the served `continuation` with a literal `120` — over-offers by an
  hour on a next day that opens `00:00→01:00`.
- Pass `sessionWindows` instead of `durationWindows` as the room check — the
  23:30 start silently disappears on every 24/7 pitch.
- Change the after-midnight group filter to `!== 'continuous'` — double-renders
  on day-bounded dates.

## Recommendation

A separate WO adding a component/unit runner. The highest-value targets are the
pure functions, which need no DOM and no network:

- `windowToSessionRange` — session-axis projection, including the `start < 0`
  previous-session exclusion.
- `availableStartsFor` — mark generation, the `m < w.end` bound, and the
  separate `roomWindows` check (the split that lets 23:30 exist without emitting
  marks ≥1440).
- `durationWindows` — the five-condition extension rule, one case per schedule
  shape, asserted against the same fixtures the Go tests use.
- `validDur` — containment, currently a closure over component state; extracting
  it is a prerequisite.

The bar those tests should encode is the one the WO settled on: **the client
must never offer a start+duration the server would reject; under-offering is
acceptable and documented.** Each of the mutations listed above should fail at
least one test.

## Related

- `docs/followups/hours-shape-contract-limits.md` — the shapes the client
  deliberately under-serves, and why.
- `docs/followups/operating-hours-gate-fail-open.md` — the unconfigured-pitch
  fail-open.
