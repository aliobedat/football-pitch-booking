package repository

// pitchDisplayNameExpr — THE composite pitch display name (WO-VENUES ruling #2),
// defined once and reused by every display/notification surface (day view,
// schedule, reports, staff chips, booking-notification PitchName).
//
//   - Pitch in a MULTI-pitch venue → "venue — label" (label falls back to the
//     pitch's own name when unset).
//   - Pitch in a single-pitch venue → bare venue name (collapse rule: a lone
//     pitch IS its venue; no redundant suffix).
//   - venue_id still NULL (pre-034 rows) → the pitch's own name, unchanged.
//
// Requires the pitches alias to be `p`. Correlated-subquery style matches the
// house pattern (see rating/reviews in data/pitches.go); the >1 sibling count
// considers only non-deleted pitches so a deleted sibling doesn't force the
// composite form.
const pitchDisplayNameExpr = `CASE
	WHEN p.venue_id IS NOT NULL
	     AND (SELECT count(*) FROM pitches p2 WHERE p2.venue_id = p.venue_id AND p2.deleted_at IS NULL) > 1
	THEN (SELECT v.name FROM venues v WHERE v.id = p.venue_id) || ' — ' || COALESCE(NULLIF(p.label, ''), p.name)
	WHEN p.venue_id IS NOT NULL
	THEN COALESCE((SELECT v.name FROM venues v WHERE v.id = p.venue_id), p.name)
	ELSE p.name
END`
