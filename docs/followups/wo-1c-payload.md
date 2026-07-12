# WO-1c payload followups (Gate 1c-minimal, 2026-07-12)

Ruling: option (a) — the redirect hop was accepted; the listing switch then
shipped separately as WO-1C-PAYLOAD Phase 1 (five additive fields on
GET /venues + one-card-per-venue listing). Remaining items below.

## 1. Listing switch → GET /venues — ✅ DONE (WO-1C-PAYLOAD Phase 1)
Shipped: image_url (cover → first-pitch fallback), format/surface
(uniform-or-null), formats (DISTINCT set, client any-match), price_varies;
listing renders one card per venue via the endpoint (no dedup/CSS hiding).
Note: the pre-existing `pitchCount` keeps its Gate 1b meaning (non-deleted,
inactive INCLUDED) — the «N ملاعب» chip therefore counts inactive pitches
while the venue page shows only active ones. Cosmetic edge; an additive
`active_pitch_count` closes it if it ever matters.

- **availabilityToday on GET /venues — DROPPED by ruling (2026-07-12), future
  product decision.** Cost note from the Phase 0 recon: a faithful venue-level
  "any pitch available today" drags the operating-hours resolver + booked-range
  math into the listing path as a correlated per-pitch computation (tens of
  indexed GIST probes at 25–100 venues — feasible but the listing's only
  expensive query), all to power a badge/chip the pitch listing itself never
  rendered (the field was never populated; the «متاح الآن» chip is a no-op).
  Decide the product semantics first; only then spend the query budget.

## 2. Search rows → direct venue links
- **venue_slug on GET /pitches/availability rows:** rows carry only
  id/name/area/image/available_until/minutes/distance — no venue_slug, so
  AvailabilitySearch can't build /venues/{slug}?pitch={id} and keeps the
  legacy link + redirect hop.

## 3. Venue-page micro-deltas (logged in the 1c report)
- **isFeatured missing from PublicVenue pitches:** the hero «ملعب مميّز» badge
  can't render on venue pages — micro-delta for featured pitches vs the old
  pitch page.
- **Runtime 1:1 venue coordinates are (0,0):** CreatePitch's auto-venue
  INSERT writes latitude/longitude 0,0 and ResolveCoordsAsync updates only
  the PITCH row. Venue pages for post-Gate-1b pitches therefore hide the map
  widget (the maps_url link still renders). Fix shape: propagate resolved
  coords to the pitch's venue (single-pitch venues at least), or read the
  selected pitch's coords into the venue payload.

## 4. Also parked
- Venue-aggregate review display on multi-pitch venue pages (currently
  per-selected-pitch reviews — deliberate 1c scope cut).

---
Status: open — all items additive backend payload changes + the follow-on
listing/search frontend switch. None block the shipped 1c experience (every
path reaches the venue page; search results pay one redirect hop).
