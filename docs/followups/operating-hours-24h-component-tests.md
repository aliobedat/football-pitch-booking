# Follow-up: automated component tests for the 24-hour operating-hours toggle

**Origin:** WO-24-HOUR-OPERATING-HOURS, Gate 1 (2026-07-20). Waiver approved by
the owner for this WO because neither `frontend` nor `admin-dashboard` has any
test runner configured (no jest/vitest, no testing-library, no `test` script in
either `package.json`). Standing up a test framework was explicitly out of
scope for this WO and was NOT introduced.

`frontend/components/OperatingHoursModal.tsx` and
`admin-dashboard/components/OperatingHoursModal.tsx` (byte-identical, both
updated in lockstep) gained:

- `isFullDayWindow`, `isFullDay24`, `FULL_DAY_WINDOW`
- `toggleFullDay24`, `applyFullDay24ToAll`
- a per-day "مفتوح 24 ساعة" toggle (distinct from "مغلق")
- a "تطبيق 24 ساعة على كل الأيام" bulk action
- `validateGrid` / `crossesMidnight` updated to mirror the backend's
  `ValidateSchedule` / `CrossesMidnight` sole-midnight-window exception

These were verified via `tsc --noEmit` (type-correctness) and manual mirroring
of the exact backend algorithm (same branch conditions, cross-checked
line-by-line against `backend/internal/data/operating_hours.go`), but have
**no automated component-level test coverage**.

**Task:** once a test runner exists for these apps (or is introduced for this
purpose), add component tests covering:

- **Loading existing `00:00 → 00:00`** — seeding the modal from a server
  response containing a sole `{weekday, open_time:"00:00", close_time:"00:00"}`
  window must render that day with "مفتوح 24 ساعة" already enabled and the
  manual time inputs hidden (requirement: no flash of the disabled/empty state).
- **Per-day 24h toggle** — enabling it on a day with existing manual windows
  replaces them with the sole full-day window and hides the inputs; disabling
  it seeds a normal editable default window (never leaves the day empty/closed).
- **Bulk "apply to all days"** — sets all seven weekdays to the explicit
  sole-window 24h representation, not an empty schedule; must not silently
  overwrite via a stale closure over `grid` (verify against `setGrid(() => ...)`
  functional-update form actually used).
- **Normal / 24h / closed transitions** — cycling a single day through all
  three states in sequence (normal → 24h → closed → normal) must never produce
  an invalid intermediate payload, and `validateGrid` must accept every
  intermediate state.
- **Exact submitted payload** — asserting the literal PUT body shape sent to
  `/pitches/:id/operating-hours` for a mixed week (one 24h day, one normal day,
  one closed day) matches `{windows: [...]}` with exactly one
  `{weekday, open_time:"00:00", close_time:"00:00"}` entry for the 24h day and
  no entry at all for the closed day.

Both `frontend/components/OperatingHoursModal.tsx` and
`admin-dashboard/components/OperatingHoursModal.tsx` need the same test suite
(they are maintained as two separate, currently-identical copies, not a shared
package).
