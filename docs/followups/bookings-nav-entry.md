# Follow-up: /bookings has no sidebar entry for any role

Origin: WO-STAFF-BOOKINGS-LOCKOUT (2026-07-19).

The الحجوزات sidebar item was previously visible ONLY to staff (owner/admin
never had a link). The lockout removed the staff item without adding one for
other roles, so `/bookings` now has NO sidebar entry at all and remains
URL-only for owner/admin (the page itself is untouched and fully functional
for them; the backend route is RequireRole("owner","admin")).

Open decision (post-launch, owner ruling required — do NOT implement without
a WO):
- Give owner/admin a proper sidebar link to الحجوزات, OR
- Consolidate/retire the page (e.g. fold its list + CSV export into another
  owner surface).
