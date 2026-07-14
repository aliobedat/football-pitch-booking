# WO-PLAYER-PUBLIC-DEAD-ENDS

**Branch:** `fix/player-public-dead-ends`
**Scope:** Remove confirmed dead ends from the public Marma player experience
without deleting or modifying production data. Follow-up data purge remains a
separate work order (`WO-PRELAUNCH-DATA-PURGE`).

## Confirmed defects (from the live Playwright Gate 0 audit)

1. Global "صاحب ملعب؟" link resolved to the old domain `https://admin.malaebjo.com`.
2. Public venue listing included venues with **zero** active/bookable pitches;
   those cards opened `/venues/:slug` which rendered "الملعب غير موجود".
3. Footer links to `/terms` and `/contact` returned 404.

## Root cause per defect

### A. Admin link — NO CODE DEFECT (config only)
`frontend/components/Navbar.tsx` already derives the link from the canonical
env mechanism:

```ts
const ADMIN_URL = process.env.NEXT_PUBLIC_ADMIN_URL || 'http://localhost:3001';
```

The code is correct and matches the durable domain-sweep ruling ("runtime public
domains MUST be derived from environment variables — never hardcoded"). The live
`admin.malaebjo.com` value comes from the **Vercel env var `NEXT_PUBLIC_ADMIN_URL`
being set to the old domain**, not from source. The localhost fallback is correct
for local dev.

**Action:** none in code. Fixing the live link is a Vercel environment change
(`NEXT_PUBLIC_ADMIN_URL=https://admin.marmajo.com`), which is **explicitly out of
scope** for this WO (Vercel configuration). Reported, not implemented — hardcoding
`marmajo.com` in source would violate the domain-sweep ruling and regress local
dev. See "Owner action required" below.

### B. Non-bookable venues in the public listing — backend query
`backend/internal/data/venues.go` → `VenueModel.PublicList` selected venues with
only `v.deleted_at IS NULL AND v.is_active = true`. It did **not** require the
venue to have any bookable pitch, so a venue with zero active/non-deleted pitches
(e.g. "سامر" / `v-613`, and the seed BK/CS test entries) still returned a card
that dead-ended on `/venues/:slug`.

**Fix:** add an `EXISTS` guard requiring at least one active, non-deleted pitch,
using the exact pitch predicate the card aggregates already use:

```sql
AND EXISTS (SELECT 1 FROM pitches p
             WHERE p.venue_id = v.id AND p.deleted_at IS NULL AND p.is_active = true)
```

Enforced in the **backend public listing query** so invalid venues cannot leak to
any client. No frontend-only filter. `PublicBySlug` already fails safely (404 for
unknown/deleted/inactive venues; empty pitch array otherwise) and is unchanged.

Pitch state fields (confirmed from schema + existing queries in this file):
- **active:** `pitches.is_active = true` (default true; migration reference in
  PROJECT_HANDOFF §4).
- **deleted:** `pitches.deleted_at IS NULL` (soft delete, migration 008).
- **publicly bookable:** `is_active = true AND deleted_at IS NULL` — the same
  predicate `venueCols` / `venueListExtraCols` already use for MinPrice/formats.

### C. Broken footer destinations — frontend links
The footer is inline-duplicated in four player pages
(`app/pitches/page.tsx`, `app/pitches/[id]/page.tsx`, `app/venues/[slug]/page.tsx`,
`app/bookings/page.tsx`). Each mapped `['الخصوصية','الشروط','تواصل معنا']` →
slugs `['privacy','terms','contact']`; `/terms` and `/contact` have no route → 404.

**Fix:** reduce the label/slug arrays to `['الخصوصية']` / `['privacy']` in all
four files. `/privacy` is unchanged and still visible. No redirects added, no
placeholder legal text invented.

## Files changed

- `backend/internal/data/venues.go` — `PublicList` EXISTS guard (+ comment).
- `backend/internal/handlers/venues_db_test.go` — new test
  `TestVenuesDB_PublicListRequiresBookablePitch`.
- `frontend/app/pitches/page.tsx` — footer links → privacy only.
- `frontend/app/pitches/[id]/page.tsx` — footer links → privacy only.
- `frontend/app/venues/[slug]/page.tsx` — footer links → privacy only.
- `frontend/app/bookings/page.tsx` — footer links → privacy only.
- `docs/wo/WO-PLAYER-PUBLIC-DEAD-ENDS.md` — this file.

## Backend filtering rule (exact)

A venue is returned by `GET /venues` (public B2C listing) iff:
`v.deleted_at IS NULL AND v.is_active = true AND EXISTS (active, non-deleted pitch)`.
Ordering (`ORDER BY v.id`) and all existing columns/aggregates are unchanged.
Owner/admin listing (`OwnerList`) and `PublicBySlug` are untouched.

## Owner action required (out of scope, tracked here)

- Set Vercel `NEXT_PUBLIC_ADMIN_URL=https://admin.marmajo.com` on the B2C project
  and redeploy. This is the real fix for defect A.
- Real `/terms` and `/contact` pages remain a separate, approval-gated task.
- Production seed/test data purge → `WO-PRELAUNCH-DATA-PURGE`.

## Explicitly NOT changed

Booking creation/availability/conflict logic, prices, schedules, OTP/auth,
cookies/CSRF, WhatsApp/SMS, JOD decimal formatting, phone validation, the custom
404 page, the admin dashboard, DB schema/migrations, production data, and any
Railway/Vercel/Neon/Cloudflare configuration.
