# Ruling to enforce: admin CreatePitch WITHOUT venue_id should refuse (422)

Current behavior (WO-VENUES Gate 1b, as shipped): an admin calling CreatePitch
without a `venue_id` auto-creates a venue owned by the ADMIN actor, producing
admin-owned inventory. This was reported and accepted as current behavior for
Gate 1b.

Future ruling: this path must be fail-closed — refuse with 422. An admin
creating a pitch must always supply a `venue_id`, from which the owner is
derived (per the venue ownership invariant, CLAUDE.md principle 5:
`pitch.owner_id == venue.owner_id`, always).

---

Status: open — documentation only, not in the Gate 1b commit.
Touch point: venue-aware CreatePitch in `backend/internal/data/pitches.go`
(the admin/no-venue_id branch) and its handler validation in
`backend/internal/handlers/pitches.go`.
