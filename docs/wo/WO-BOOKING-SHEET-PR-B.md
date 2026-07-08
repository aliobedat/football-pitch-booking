# WORK ORDER — WO-BOOKING-SHEET / PR-B
## Day View Booking Bottom Sheet — Frontend

**Status:** AUTHORIZED FOR GATE 0 ONLY
**Scope:** admin-dashboard Day View page (الدفتر) — tap an occupied slot → booking details bottom sheet with payment tracking and extension actions.
**Out of scope:** Reports (PR-C), any other page, any backend change, the three legacy SetPayment callers (schedule/calendar/bookings pages stay on the legacy toggle untouched).

---

## 0. Backend contract (frozen, proven by 23-case DB suite — consume, never modify)

Per-slot booking ref (Day View payload): `id, source, status, attendance, payment_status (legacy), title, start_time, end_time, total_price, amount_paid (nullable), payment_display (untracked|unpaid|partial|paid), remaining (nullable)`. Envelope carries `price_per_hour`.

**Actions:**
- `PATCH /bookings/:id/extend` `{"minutes": 30|60}` → 200 full sheet object · 409 `slot_conflict` · 400 `booking_ended` · 400 `outside_operating_hours` · 409 `booking_cancelled` · 409 `not_a_booking` · 404
- `PATCH /bookings/:id/payment` `{"amount_paid": number|null, "total_price"?: number}` (new form only on this surface) → 200 · 422 `paid_exceeds_total` · 422 `invalid_*` · 409s · 404

## 1. Locked UX rulings

1. **Sheet opens only for `status: booked` slots whose booking `source ≠ 'block'`.** Blocked/closed/available slots keep their existing tap behavior exactly. The client never offers an action the server will 409.
2. **NULL renders neutral.** `payment_display: untracked` → no badge, no red, المدفوع input empty with placeholder «غير مسجّل». The board must not scream "unpaid" at bookings the owner never tracked. `0` → غير مدفوع (red is legitimate — the owner explicitly recorded zero).
3. **«دفع كامل» is the primary action** — one tap sends `{amount_paid: total_price}`. Free numeric entry (`inputMode="decimal"`, 3dp max) covers partials. This is the 90% path for the fifty-year-old owner.
4. **Extension chips are two-tap.** Tap `+30 دقيقة` / `+ساعة` → chip enters confirm state showing the projected new total (`price_per_hour × minutes / 60` added client-side for DISPLAY ONLY — the server computes the real delta) → second tap fires the PATCH. Tap elsewhere cancels. No accidental money mutations from a thumb graze.
5. **Extension chips hidden when `end_time < now`** (client mirror of `booking_ended`). Payment remains available on ended bookings — settling cash after the game is normal.
6. **Total price editable in the sheet** (owner discretion, discounts happen): small edit affordance on الإجمالي, sends `{amount_paid: <current>, total_price: <new>}` — note the server 422s if new total < current paid; surface that error, don't pre-block silently.
7. **Refetch, never optimistic.** On any successful PATCH: refetch the day timeline, then update the open sheet from the fresh payload. Money display must be authoritative, and a successful extend can change neighboring slot availability. Apply the stale-response guard pattern from the reports race fix if the fetch layer needs it.
8. **In-flight discipline:** all sheet actions disabled while a PATCH is pending; single spinner state.

## 2. Error copy — Levantine, mapped from the server error-code table

| Code | Copy |
|---|---|
| `slot_conflict` | لا يمكن التمديد — الوقت التالي محجوز |
| `booking_ended` | الحجز خلص — التمديد غير متاح |
| `outside_operating_hours` | التمديد يتجاوز ساعات دوام الملعب |
| `paid_exceeds_total` | المبلغ المدفوع أكبر من إجمالي الحجز |
| `booking_cancelled` | هذا الحجز ملغي |
| network / 500 / unknown | صار خطأ — جرّب مرة ثانية |

Errors render inside the sheet (inline, near the action that failed), never as a bare toast the owner misses.

## 3. Guardrails

- ❌ No changes outside the Day View page + the new sheet component (+ a shared util only if strictly required — declare it at the gate).
- ❌ `PaymentStatusPill` untouched. Reuse it if it fits; do not extend it — its other four consumers are frozen surfaces.
- ❌ No new nav items, routes, or pages. No changes to schedule/calendar/bookings pages.
- ❌ No client-side price mutation logic beyond the display projection in ruling 4. Every JOD value shown post-action comes from the server response/refetch.
- ❌ RTL logical properties throughout; Asia/Amman display conventions as the page already does.

## 4. Gate 0 — Read-only recon (report, STOP)

1. Day View page structure: where slot tap is handled today, existing state/fetch pattern, how `booking` ref is threaded to the slot component.
2. Existing bottom-sheet or modal primitives in the dashboard codebase (reuse before build).
3. `PaymentStatusPill` fit assessment for `payment_display` states (reuse vs sibling component — recommend, don't decide).
4. Confirm the deployed payload already carries the new fields (one authenticated GET — hand Ali the command, per the prod-access rule, or read from a dev session if one exists).
5. Where `price_per_hour` lands in the envelope and its type as received (integer JOD — confirm serialization).
6. Existing number-input patterns in the dashboard (3dp money entry precedent, if any).

## 5. Gate 1 — Implementation (STOP before commit)

Build per §1–§3. Deliverable report: component tree, files touched, screenshot set (untracked / partial / paid / zero / ended / confirm-state chip / each error state), `tsc` + build green.

## 6. Gate 2 — Manual QA checklist (executed, not asserted)

On a dev session against prod-equivalent data: open sheet on booked slot · blocked slot does NOT open sheet · دفع كامل one-tap → pill flips, refetch confirms · partial entry → المتبقي correct to 3dp · overpay → inline 422 copy · edit total below paid → inline 422 · extend happy path → new end + new total from server · extend into occupied → inline `slot_conflict` copy, nothing changed · ended booking → chips absent, payment works · NULL booking renders neutral · all actions disabled mid-flight.

**STOP before commit. STOP before push. Ship sequence: no migration this time — push → PR → merge → Vercel deploys. Verify against the live backend post-deploy.**

## 7. Followups touched by this WO (file, don't implement)

- Legacy-caller migration (schedule/calendar/bookings) to the new body form — unblocked once this sheet proves the pattern.
- Staff Day View + staff extension power — unchanged, deferred.
- PR-C `المحصّل` — next in queue after PR-B ships, semantics pre-ratified (amended version).
