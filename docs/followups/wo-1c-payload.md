# WO-1c payload followups — deferred by ruling (Gate 1c-minimal, 2026-07-12)

Ruling: option (a) — the redirect hop is accepted for now; the listing stays
on GET /pitches and availability-search rows keep linking /pitches/{id}
(which now client-redirects to /venues/{slug}?pitch={id}). Everything below
is the additive backend payload work needed to close the gap later.

## 1. Listing switch → GET /venues (items blocked in 1c)
- **surface/format on single-pitch venue rows:** venue cards can't render the
  spec pills («خماسي» / «عشب صناعي») today's pitch cards show, and the 5×5/7×7
  filter chips filter on `format`. Minimal shape: include the lone pitch's
  `surface`/`format` on pitchCount==1 rows (or a small pitch summary array).
- **availabilityToday on GET /venues:** the pitch listing's availability badge
  has no venue-level equivalent.
- **Then:** switch app/pitches/page.tsx to GET /venues — one card per venue,
  link /venues/{slug}, «N ملاعب» chip on multi-pitch cards, «من {min} د.أ»
  only when prices differ.

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
