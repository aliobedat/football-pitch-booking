# Reports: soft-deleted pitches' history counts in revenue (analytics parity)

The Reports aggregates (`reports_repository.go`) JOIN `pitches` **without** a
`deleted_at IS NULL` filter — deliberately mirroring the analytics endpoints
(`analytics_repository.go` `OwnerRevenueSummary` / `OwnerKPIs` /
`OwnerTimeSeries`), none of which filter soft-deleted pitches. Consequence: the
historical bookings of a soft-deleted pitch still contribute to
`gross_revenue` / `collected` / `booking_count`, and the pitch still appears as a
`by_pitch` line on the unfiltered financial statement.

This is ratified behaviour (R1, 2026-07-06): **dashboard parity is a hard
requirement** — a printed statement must equal the analytics tiles for the same
window, so the two surfaces must treat soft-deleted pitches identically.
(Asymmetry to be aware of: an explicit `?pitch_id=` targeting a soft-deleted
pitch returns 404 via `ResolveReportPitch` — the day-view convention — even
though its rows are included in unfiltered totals. Integration test:
`TestReports_SoftDeletedPitchRevenueIncluded`.)

Follow-up: decide whether soft-deleted pitches should be *visually* marked on
report surfaces (e.g. a "محذوف" tag on the `by_pitch` line) or whether an owner
should be able to opt them out. If the answer is ever "exclude them", the change
MUST land in analytics and reports in the same pass, or the statement stops
matching the dashboard and both lose credibility.

---

Status: open — documentation only, no code change.
