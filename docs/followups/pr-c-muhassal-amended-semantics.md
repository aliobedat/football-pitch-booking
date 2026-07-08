# Follow-up: PR-C المحصّل — amended collected-cash semantics

**Logged by:** WO-BOOKING-SHEET / PR-B, 2026-07-08 (pre-ratified in PR-A §6).
File-only; PR-C territory.

With `amount_paid` now the source of truth for partial cash (PR-A) and the Day
View sheet surfacing it (PR-B), the reports «المحصّل» (collected) figure should be
amended in PR-C to reflect actual cash collected, per the ratified rule:

- **tracked** (`amount_paid IS NOT NULL`) → collected = `amount_paid`
- **untracked + `payment_status='paid_cash'`** → collected = `total_price`
  (legacy fully-settled rows created before tracking existed)
- **untracked + `payment_status='unpaid'`** → collected = `0`

This SUPERSEDES the earlier "NULL = collected-in-full" reading. Until PR-C ships,
the frozen collected-cash consumers (analytics/net-profit/reports) still key on
`payment_status='paid_cash'` only, so partial cash is invisible to them by design
(PR-A ruling 5) — that is the correct transitional behavior, not a bug.

Staff Day View + staff extension power remain deferred — see
[[staff-day-view]]; extension is owner/admin-only in PR-A/PR-B by design.
