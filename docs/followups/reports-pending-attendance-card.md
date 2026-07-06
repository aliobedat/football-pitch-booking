# Follow-up: pending-attendance card on the bookings report tab

**Origin:** WO-REPORTS-R3, ruling A1 (2026-07-06).

The الحجوزات tab of التقارير shows four summary cards: إجمالي الحجوزات /
الحضور / الغياب / الملغاة. The backend summary satisfies
`total = attended + no_show + pending`, but **pending attendance has no card**
by ruling — the numbers therefore don't visibly sum for windows containing
rows whose attendance hasn't been marked yet (common for future/recent
bookings). Pending rows are still visible in the table via the neutral —
pill (`components/AttendancePill.tsx`).

**Open question for a future WO:** add a fifth card (e.g. «غير مسجّل») or a
footnote clarifying the arithmetic, once real usage shows whether owners are
confused by total ≠ حضور + غياب + ملغاة.

No backend change needed — pending is derivable as
`total − attended − no_show` from the existing summary.
