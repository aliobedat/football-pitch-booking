# pitchDisplayNameExpr: per-row correlated sibling count on multi-row surfaces

**Context (WO-VENUES Gate 1b, 2026-07-11):** the composite pitch display name
(`internal/repository/display_name.go`) resolves "is this pitch in a
multi-pitch venue?" via a correlated `(SELECT count(*) FROM pitches p2 WHERE
p2.venue_id = p.venue_id AND p2.deleted_at IS NULL) > 1` subquery. EXPLAIN
ANALYZE (pre-commit verification, scratch, 58 pitches) confirms the planner
executes it as a **per-row SubPlan → Aggregate**, not a join/window.

**Why it was accepted:** matches the house correlated-subquery pattern; the
day-view resolve is single-row so the subplan runs once; `idx_pitches_venue`
serves the lookup at realistic scale; measured 0.079 ms.

**The debt:** on multi-row surfaces embedding the same expression — reports
`by_pitch`, staff list/chips, `GetAllBookings` — the count re-executes per
OUTPUT ROW. Linear in rows × sibling-lookup cost.

**Revisit trigger:** any tenant exceeding ~15 pitches, or observed latency on
the reports endpoints. Fix shape: precompute sibling counts once per query via
a windowed join, e.g.
`LEFT JOIN (SELECT venue_id, count(*) FILTER (WHERE deleted_at IS NULL) AS n
FROM pitches GROUP BY venue_id) sib ON sib.venue_id = p.venue_id`
(or `count(*) OVER (PARTITION BY p.venue_id)` where the row set allows), and
swap the CASE to read `sib.n`. Keep it defined once, like the current expr.
