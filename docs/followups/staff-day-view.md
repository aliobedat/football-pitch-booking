# Staff access to the Day View timeline

PR-1 ships `GET /owner/day-view` for **owner/admin only** (`RequireRole("owner",
"admin")` + handler re-assertion). Staff are deliberately excluded, consistent
with the current V1 confinement of staff to جدول اليوم (`/schedule`) and الحجوزات
(`/bookings`) enforced in `app/(dashboard)/layout.tsx`.

Follow-up: decide whether guards/staff should see the Day View for their bound
pitch(es). If yes, this mirrors the `/schedule` scoping shape — staff scoped in
SQL to `boundPitchIDs` (see `scopePredicate` / `ResolveScope`), with the same
403-before-handler treatment for unprovisioned staff — rather than the
owner/admin `OwnerScopeFilter` used by PR-1. That is a separate authorized pass
touching the route role set, the repository scoping, and its tests.

---

Status: open — documentation only, no code change.
Out of scope for PR-1 (owner/admin Day View timeline endpoint).
