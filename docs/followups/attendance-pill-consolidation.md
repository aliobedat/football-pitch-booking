# Follow-up: consolidate inline attendance pills onto AttendancePill

**Origin:** WO-REPORTS-R3, ruling C1 (2026-07-06).

`admin-dashboard/components/AttendancePill.tsx` was extracted (additive-only)
for the bookings report tab. Three pre-existing inline copies of the same
label/colour trio (حضر / لم يحضر / —) were deliberately left untouched to keep
that PR's diff within its allowed set:

- `app/(dashboard)/customers/[id]/page.tsx` — `ATTENDANCE_AR` (~line 40),
  read-only pills in the history table.
- `app/(dashboard)/schedule/page.tsx` — labels ~line 35 plus the interactive
  حضر / لم يحضر toggle buttons (~lines 148–155); the toggle needs the shared
  colour classes, not the read-only component.
- `app/(dashboard)/calendar/page.tsx` — inline ternary (~line 358) rendering
  plain text, not a pill.

**Task:** migrate the read-only usages to `AttendancePill` (and export the
label/colour maps for the schedule toggle), mirroring how PaymentStatusPill
serves both its read-only and interactive callers. Pure refactor; no behaviour
change; do it in a dedicated small PR.
