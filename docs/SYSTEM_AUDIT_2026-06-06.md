# Malaeb (ملاعب) — System Audit

_Read-only reconnaissance audit. Date: 2026-06-06. Auditor: senior-engineer pass._
_Scope: verify PROJECT_HANDOFF.md against actual code; classify every feature's wiring; reconcile contracts; sweep health & security. **No source file, migration, or config was modified.** Only this report and PROJECT_HANDOFF.md were written._

---

## 1. Executive Summary

The codebase is in **substantially better shape than a typical mid-build SaaS**, and notably **better than PROJECT_HANDOFF.md claims**. The handoff's roadmap (§9) lists the entire player booking flow and owner dashboard as "not started" — **this is stale**. In reality:

- **The full player flow is FULLY WIRED**: pitch list → pitch detail → slot-picker calendar → instant booking → "My Bookings" → player self-cancel all call real, registered, implemented Go endpoints.
- **The full owner/admin dashboard is FULLY WIRED**: admin bookings list, cancel, pitch CRUD, activate/deactivate toggle, and Cloudinary image upload all hit live endpoints.
- **Auth is FULLY WIRED**: phone-OTP login, httpOnly-cookie session, `/auth/me` rehydration, single-flight refresh, CSRF double-submit, logout.

**Static health is green**: `go build ./...` ✅, `go vet ./...` ✅, `tsc --noEmit` ✅.

**No live security holes or data-loss bugs were found.** Mutating routes are uniformly behind `RequireAuth` + `RequireCSRF` + (where relevant) `RequireRole`, with ownership scoping enforced in SQL via the `auth.Actor`. The known residual risks (no access-token denylist, no WhatsApp webhook signature check) are accurately documented in the handoff and are accepted/deferred, not regressions.

### Highest-severity findings (none critical)

| Sev | Finding | Evidence |
|-----|---------|----------|
| **MEDIUM** | **`description` is silently dropped end-to-end.** The dashboard create/edit form collects a pitch `description` and the pitch-detail page renders one, but the Go `CreatePitchRequest`/`UpdatePitchRequest` structs have **no `Description` field**, the INSERT never writes it, and no SELECT reads the `description` column (which has existed since migration 002). Descriptions entered by owners are lost; the "عن الملعب" section never renders. | `data/pitches.go:98-111` (no field), `:512-528` (INSERT omits it), `:33-74` (SELECT omits it); `dashboard/page.tsx:722-725` (sends it); `pitches/[id]/page.tsx:213-218` (renders it) |
| **LOW** | **`POST /notifications/opt-out` is an orphaned endpoint** — implemented and registered, but no frontend consumer exists. Consent withdrawal is unreachable from the UI. | `routes.go:84`; no match in `frontend/` |
| **LOW** | **Handoff schema (§4) and roadmap (§9) are materially stale** — missing the `surface`, `amenities`, `latitude`/`longitude`, `is_active`, `pitch_hue`, `rating`/`review_count`, `image_public_id`, `deleted_at` pitch columns, the `reviews` and `pitch_audit_log` tables, and migrations 008–009. Corrected in this pass (see §9). | `data/pitches.go:33-74`; `migrations/008/009` |

---

## 2. Wired / Static Matrix (centerpiece)

Every player-facing and admin-facing feature, classified with file:line evidence. **All features are FULLY WIRED** — there is no static/mock data feeding any primary flow (the only hardcoded arrays are client-side filter chips and surface-label maps, which is correct).

### Player-facing

| Feature | Component | API client call | Backend endpoint | Status | Evidence |
|---------|-----------|-----------------|------------------|--------|----------|
| Pitch listing | `pitches/page.tsx` | `api.get('/pitches')` | `GET /pitches` → `PitchHandler.ListPitches` | **FULLY WIRED** | `pitches/page.tsx:193`; `routes.go:48`; `pitches.go:27` |
| Pitch detail | `pitches/[id]/page.tsx` | `api.get('/pitches/${id}')` | `GET /pitches/:id` → `GetPitch` | **FULLY WIRED** | `[id]/page.tsx:64`; `routes.go:49` |
| Availability / slot-picker | `BookingForm.tsx` | `api.get('/pitches/${id}/availability?date=')` | `GET /pitches/:id/availability` → `GetPitchAvailability` | **FULLY WIRED** | `BookingForm.tsx:185,318`; `routes.go:90`; `bookings.go:146` |
| Create booking (instant confirm) | `BookingForm.tsx` | `api.post('/bookings', …)` | `POST /bookings` → `CreateBooking` → `booking.Service.Create` | **FULLY WIRED** | `BookingForm.tsx:332`; `routes.go:89`; `bookings.go:46`; `service.go:93` |
| My Bookings | `bookings/page.tsx` | `api.get('/bookings')` | `GET /bookings` → `GetUserBookings` | **FULLY WIRED** | `bookings/page.tsx:242`; `routes.go:88` |
| Player self-cancel | `bookings/page.tsx` (`BookingCard`) | `api.patch('/bookings/${id}/cancel')` | `PATCH /bookings/:id/cancel` → `CancelBooking` (player-scoped in SQL) | **FULLY WIRED** | `bookings/page.tsx:88`; `routes.go:136`; `booking_repository.go:469-474` |
| Server time (for slot validity) | `BookingForm.tsx` | `fetch('/api/server-time')` | Next.js route handler (not backend) | **FULLY WIRED** (internal) | `BookingForm.tsx:128`; `app/api/server-time/route.ts` |
| Nearest-pitch geolocation | `pitches/page.tsx` | client-only (`navigator.geolocation` + `haversineKm`) | n/a (no backend) | **FULLY WIRED** (client) | `pitches/page.tsx:206-212`; `lib/distance.ts` |

### Auth

| Feature | Component | API client call | Backend endpoint | Status | Evidence |
|---------|-----------|-----------------|------------------|--------|----------|
| Request OTP | `login/page.tsx` | `api.post('/auth/request-otp', {phone, opt_in})` | `POST /auth/request-otp` → `RequestOTP` | **FULLY WIRED** | `login/page.tsx:118`; `routes.go:60`; `phone_auth.go:93` |
| Verify OTP → session | `login/page.tsx` | `api.post('/auth/verify-otp', …)` | `POST /auth/verify-otp` → `VerifyOTP` (sets httpOnly cookies) | **FULLY WIRED** | `login/page.tsx:140`; `routes.go:61`; `phone_auth.go:141` |
| Session rehydration | `AuthContext.tsx` | `api.get('/auth/me', {_silent})` | `GET /auth/me` → `GetCurrentUser` | **FULLY WIRED** | `AuthContext.tsx:45`; `routes.go:81` |
| Silent refresh on 401 | `lib/api.ts` interceptor | `api.post('/auth/refresh')` | `POST /auth/refresh` (CSRF-guarded) → `AuthHandler.Refresh` | **FULLY WIRED** | `lib/api.ts:66-71`; `routes.go:65` |
| Logout | `AuthContext.tsx` / `Navbar` | `api.post('/auth/logout')` | `POST /auth/logout` → `Logout` (clears cookies) | **FULLY WIRED** | `AuthContext.tsx:67`; `routes.go:78` |
| Dashboard edge guard | `proxy.ts` | reads `malaab_role` cookie | n/a (Next 16 Proxy) | **FULLY WIRED** | `proxy.ts:6-21` (see §5 note) |

### Owner / Admin-facing

| Feature | Component | API client call | Backend endpoint | Status | Evidence |
|---------|-----------|-----------------|------------------|--------|----------|
| Admin/owner bookings list | `dashboard/page.tsx` | `api.get('/admin/bookings')` | `GET /admin/bookings` → `GetAllBookings` (owner-scoped in SQL) | **FULLY WIRED** | `dashboard/page.tsx:975`; `routes.go:129`; `booking_repository.go:310-319` |
| Admin/owner cancel booking | `dashboard/page.tsx` (`CancelModal`) | `api.patch('/bookings/${id}/cancel')` | `PATCH /bookings/:id/cancel` (owner/admin-scoped) | **FULLY WIRED** | `dashboard/page.tsx:956`; `routes.go:136` |
| Owner pitches list | `dashboard/page.tsx` | `api.get('/owner/pitches')` | `GET /owner/pitches` → `GetOwnerPitches` → `ListForActor` | **FULLY WIRED** | `dashboard/page.tsx:985`; `routes.go:123`; `pitches.go:340` |
| Create pitch | `AddPitchForm` | `api.post('/pitches', payload)` | `POST /pitches` → `CreatePitch` (owner/admin) | **FULLY WIRED** (but drops `description` — see §4) | `dashboard/page.tsx:725`; `routes.go:93` |
| Edit pitch | `AddPitchForm` | `api.patch('/pitches/${id}', payload)` | `PATCH /pitches/:id` → `UpdatePitch` (actor-scoped) | **FULLY WIRED** (drops `description`) | `dashboard/page.tsx:724`; `routes.go:109` |
| Delete pitch (soft) | `DeletePitchModal` | `api.delete('/pitches/${id}')` | `DELETE /pitches/:id` → `SoftDeletePitch` (future-booking guard) | **FULLY WIRED** | `dashboard/page.tsx:1024`; `routes.go:113`; `pitches.go:169` |
| Activate / deactivate toggle | `PitchCard` switch | `api.patch('/pitches/${id}/active', {is_active})` | `PATCH /pitches/:id/active` → `ToggleActive` | **FULLY WIRED** | `dashboard/page.tsx:1044`; `routes.go:119` |
| Pitch image upload (signed direct) | `PitchImageDropzone` | `api.post('/pitches/upload-signature')` → Cloudinary → `api.patch('/pitches/${id}/image')` | `POST /pitches/upload-signature` + `PATCH /pitches/:id/image` | **FULLY WIRED** | `PitchImageDropzone.tsx:77,91`; `dashboard/page.tsx:708`; `routes.go:99,105` |

### Not surfaced in UI

| Feature | Backend endpoint | Status |
|---------|------------------|--------|
| Notification consent withdrawal | `POST /notifications/opt-out` (`routes.go:84`) | **ORPHANED** — implemented, registered, no consumer |
| WhatsApp delivery webhooks | `GET/POST /webhooks/whatsapp` (`routes.go:54-55`) | External (Meta) — not a frontend feature; expected |
| Health check | `GET /ping` (`routes.go:47`) | Infra — expected |

---

## 3. Endpoint Coverage Matrix

| # | Method | Path | Middleware chain | Handler (file:line) | Frontend consumer | Status |
|---|--------|------|------------------|---------------------|-------------------|--------|
| 1 | GET | /ping | — | `HealthHandler.Ping` (routes.go:47) | — | infra |
| 2 | GET | /pitches | — | `ListPitches` (pitches.go:27) | `pitches/page.tsx:193` | ✅ consumed |
| 3 | GET | /pitches/:id | — | `GetPitch` (pitches.go:53) | `[id]/page.tsx:64` | ✅ consumed |
| 4 | GET | /webhooks/whatsapp | — | `webhook.Verify` | Meta (external) | ✅ external |
| 5 | POST | /webhooks/whatsapp | — | `webhook.Receive` | Meta (external) | ✅ external |
| 6 | POST | /auth/request-otp | — | `RequestOTP` (phone_auth.go:93) | `login/page.tsx:118` | ✅ consumed |
| 7 | POST | /auth/verify-otp | — | `VerifyOTP` (phone_auth.go:141) | `login/page.tsx:140` | ✅ consumed |
| 8 | POST | /auth/refresh | RequireCSRF | `AuthHandler.Refresh` | `lib/api.ts:70` | ✅ consumed |
| 9 | POST | /auth/logout | RequireAuth + RequireCSRF | `AuthHandler.Logout` | `AuthContext.tsx:67` | ✅ consumed |
| 10 | GET | /auth/me | RequireAuth + RequireCSRF | `GetCurrentUser` (phone_auth.go:200) | `AuthContext.tsx:45` | ✅ consumed |
| 11 | POST | /notifications/opt-out | RequireAuth + RequireCSRF | `OptOut` | **none** | ⚠️ ORPHANED |
| 12 | GET | /bookings | RequireAuth + CSRF | `GetUserBookings` (bookings.go:100) | `bookings/page.tsx:242` | ✅ consumed |
| 13 | POST | /bookings | RequireAuth + CSRF | `CreateBooking` (bookings.go:46) | `BookingForm.tsx:332` | ✅ consumed |
| 14 | GET | /pitches/:id/availability | RequireAuth + CSRF | `GetPitchAvailability` (bookings.go:146) | `BookingForm.tsx:185,318` | ✅ consumed |
| 15 | POST | /pitches | + RequireRole(owner,admin) | `CreatePitch` (pitches.go:87) | `dashboard/page.tsx:725` | ✅ consumed |
| 16 | POST | /pitches/upload-signature | + RequireRole(owner,admin) | `UploadSignature` (pitches.go:259) | `PitchImageDropzone.tsx:77` | ✅ consumed |
| 17 | PATCH | /pitches/:id/image | + RequireRole(owner,admin) | `SetPitchImage` (pitches.go:277) | `dashboard/page.tsx:708` | ✅ consumed |
| 18 | PATCH | /pitches/:id | + RequireRole(owner,admin) | `UpdatePitch` (pitches.go:131) | `dashboard/page.tsx:724` | ✅ consumed |
| 19 | DELETE | /pitches/:id | + RequireRole(owner,admin) | `DeletePitch` (pitches.go:169) | `dashboard/page.tsx:1024` | ✅ consumed |
| 20 | PATCH | /pitches/:id/active | + RequireRole(owner,admin) | `ToggleActive` (pitches.go:213) | `dashboard/page.tsx:1044` | ✅ consumed |
| 21 | GET | /owner/pitches | + RequireRole(owner,admin) | `GetOwnerPitches` (pitches.go:340) | `dashboard/page.tsx:985` | ✅ consumed |
| 22 | GET | /admin/bookings | + RequireRole(owner,admin) | `GetAllBookings` (bookings.go:123) | `dashboard/page.tsx:975` | ✅ consumed |
| 23 | PATCH | /bookings/:id/cancel | + RequireRole(player,owner,admin) | `CancelBooking` (bookings.go:202) | `bookings/page.tsx:88` + `dashboard/page.tsx:956` | ✅ consumed |

> Routes 12–23 all inherit `RequireAuth` + `RequireCSRF` from the `protected` group (`routes.go:71-76`).

**Orphaned endpoints:** #11 (`/notifications/opt-out`). **Phantom calls (frontend → nonexistent backend):** none.

---

## 4. Contract Mismatches

| Sev | Endpoint | Mismatch | Evidence |
|-----|----------|----------|----------|
| **MEDIUM** | `POST /pitches`, `PATCH /pitches/:id` | Frontend sends `description` in the payload; backend request structs have no `Description` field, the INSERT doesn't write it, and no SELECT returns it. The value is silently discarded (and `pitch-detail` "عن الملعب" never renders). | `dashboard/page.tsx:722`; `data/pitches.go:98-111,254-260,512-528` |
| LOW | `GET /owner/pitches` response | Dashboard `OwnerPitch` interface declares `rating: number` and `reviewCount: number`, but backend JSON emits `rating: number\|null` and `reviewsCount` (different key). `reviewCount` is therefore always `undefined`. Harmless — not rendered in the owner card. | `dashboard/page.tsx:48-49`; `data/pitches.go:86-87` |
| LOW | `POST /bookings` request | Frontend sends `total_price` (and the binding marks it `required`), but the repository **recomputes** the total server-side from `price_per_hour × duration` and ignores the client value. Correct (server-authoritative); the `required` binding is a minor redundancy. Worth a comment so no one trusts the client field later. | `BookingForm.tsx:336`; `models/booking.go:32`; `booking_repository.go:153-154` |
| INFO | Booking status vocab | Frontend `BookingStatus` unions include `pending`; the instant-booking model never emits `pending`. Cosmetic (status maps tolerate it). The dashboard labels `cancelled` as "مرفوض/ملغاة". | `dashboard/page.tsx:22,78-82` |

---

## 5. Security / Scope Findings (ranked)

**No critical or high findings. No live hole, no data-loss path.**

| Sev | Finding | Detail / Evidence |
|-----|---------|-------------------|
| MEDIUM | **No access-token denylist** (pre-existing, documented). A stolen access JWT stays valid up to 15 min after logout; logout only revokes refresh tokens + clears cookies. | Handoff §8; `cookies.go:98-106` |
| MEDIUM | **WhatsApp webhook signature (`X-Hub-Signature-256`) not validated.** `POST /webhooks/whatsapp` is public and only the GET verify-token handshake is secured, so a forged status callback could write `message_deliveries`. Low blast radius (status tracking only). | `routes.go:55`; handoff §8 |
| LOW | **`opt_out` cannot be exercised by users** (orphaned endpoint) — a consent/compliance gap more than a security hole. | §3 #11 |
| INFO (not a finding) | **Mutating-route coverage is complete and correct.** Every state-changing route sits behind `RequireAuth` + `RequireCSRF`; owner/admin routes add `RequireRole`; ownership is enforced in **SQL** via `auth.Actor` (owner predicates on `owner_id`/`player_id`, admin unscoped), so cross-tenant access returns 404 without leaking existence. Booking create re-checks pitch bookability under `FOR UPDATE` (no TOCTOU). | `routes.go:71-139`; `data/pitches.go:217-510`; `booking_repository.go:103-206,433-544` |
| INFO (not a finding) | **The `proxy.ts` edge guard is correctly wired** — not dead code. Next.js 16 renamed Middleware → Proxy; `proxy.ts` exporting `proxy` with a `matcher` config is the current convention (`node_modules/next/dist/docs/.../16-proxy.md`). It is a defense-in-depth UX redirect only; the backend JWT role check is authoritative. The dashboard comment referencing "middleware.ts" (`dashboard/page.tsx:903`) and the `cookies.go:10` comment are slightly stale wording. | `proxy.ts:1-21`; Next 16 proxy doc |
| INFO | CORS hardcodes a Vercel origin alongside `localhost:3000` and merges `CORS_ALLOWED_ORIGINS`; `AllowCredentials:true` with an exact-match allowlist (no wildcard) — correct. Dormant while local-only. | `cmd/api/main.go:170-205` |

---

## 6. Schema / Model Drift

**Live schema truth derived from migrations + `data/pitches.go` scan columns** (the handoff warns the base tables predate migration 002 and aren't reproduced).

| Area | Drift | Resolution |
|------|-------|-----------|
| `pitches` columns | Code reads/writes `surface`, `amenities`, `pitch_hue`, `latitude`, `longitude`, `rating`, `review_count`, `is_active`, `image_public_id`, `deleted_at` — **none of which appear in handoff §4**. A `reviews` table is LEFT-JOINed for `rating`/`reviews_count`. | Handoff §4 corrected in this pass. |
| `description` | Column exists (migration 002) but **no Go struct field, no INSERT, no SELECT** references it → effectively dead at the app layer despite being collected/rendered in the UI. | Flagged MEDIUM (§4); remediation in §9. |
| Tables missing from handoff | `pitch_audit_log` (migration 008), `reviews` (pre-002, referenced by `data/pitches.go:41,64`). | Added to handoff. |
| Migrations | Handoff says "current schema version: Migration 007". Migrations **008** (soft delete + `pitch_audit_log`) and **009** (`image_public_id`) exist and are wired. | Handoff updated to 009. |
| `booking_status` enum | DB enum carries `pending/rejected/completed/no_show`; Go `models` only define `pending/confirmed/cancelled` and only `confirmed`/`cancelled` are exercised. Unused enum values are dead weight (consistent with handoff §8). | No change (already documented). |
| `go.mod` `go 1.26.3` | Handoff flags this as "looks like a typo, verify". **Verified real:** `go version` → `go1.26.3 windows/amd64`. Not a typo. | Handoff open question resolved. |

---

## 7. Build / Type-check / Lint Status

| Check | Command | Result |
|-------|---------|--------|
| Go build | `go build ./...` (backend) | ✅ PASS (exit 0, no output) |
| Go vet | `go vet ./...` (backend) | ✅ PASS (no diagnostics) |
| Go toolchain | `go version` | go1.26.3 windows/amd64 (matches `go.mod`) |
| TS type-check | `npx tsc --noEmit` (frontend) | ✅ PASS (no errors) |
| Test inventory | (not executed — many tests touch pgx; not run against Neon per guardrails) | See §8 |

**Test inventory (by reading, not running):** broad unit coverage with in-memory fakes —
`middleware/csrf_test.go`, `booking/service_test.go`, `booking/reminder_worker_test.go`, `handlers/bookings_test.go`, `handlers/phone_auth_test.go`, `handlers/notifications_test.go`, `handlers/whatsapp_webhook_test.go`, `handlers/pitch_image_test.go`, `data/pitches_scoping_test.go`, `repository/booking_scoping_test.go`, `repository/booking_cancel_scoping_test.go`, `repository/booking_bookable_test.go`, `repository/reminder_repository_test.go`, plus the full `notification/`, `notification/outbox/`, `otp/`, `cloudinary/` suites. New since handoff: the pitch-scoping, booking-scoping, bookable, image, and `auth/actor.go` tests/files. The handoff's "every new module has a paired `_test.go`" claim holds.

---

## 8. Dead-code / TODO Inventory

- **No `TODO`/`FIXME`/`HACK`/`XXX` markers** in backend or frontend app/lib/components/context (the only grep hits are E.164 example strings and a phone placeholder — false positives).
- **No commented-out fetches or mock arrays** feeding any live view.
- **Effectively-dead at app layer:** the `description` pitch column (§4/§6); the `rejected`/`completed`/`no_show` booking-status enum values (§6); the `payment_status` column (intentional dormant seam per CLAUDE.md — keep).
- **Orphaned endpoint:** `POST /notifications/opt-out` (§3).
- **Stale comments (cosmetic):** `dashboard/page.tsx:903` and `cookies.go:10` reference "middleware.ts"; the file is `proxy.ts` under Next 16's renamed convention.
- Untracked `docs/PR_pitch_image_upload.md` exists alongside this report (pre-existing).

---

## 9. Prioritized Remediation Backlog (RECOMMENDATIONS — not implemented)

1. **(MEDIUM) Wire `description` through the pitch layer.** Add `Description` to `CreatePitchRequest`/`UpdatePitchRequest`, include it in the INSERT, the `CASE`-based partial UPDATE, and both `pitchSelectCols`/`pitchReturnCols`; add `Description` to the `Pitch` struct + JSON. Otherwise remove the field from the UI to stop silently discarding owner input. _Single-purpose PR._
2. **(LOW) Surface or remove `opt_out`.** Either add a consent-withdrawal control (settings/profile) calling `POST /notifications/opt-out`, or accept it as backend-only and document why.
3. **(LOW) Reconcile `OwnerPitch` types** with backend JSON (`reviewsCount` vs `reviewCount`, nullable `rating`) so the dashboard model matches the wire shape.
4. **(MEDIUM, pre-existing) Add `X-Hub-Signature-256` validation** to the WhatsApp webhook before any real Meta integration goes live.
5. **(MEDIUM, pre-existing) Add an access-token denylist** (Redis/DB) for immediate logout invalidation.
6. **(LOW) Decide the `booking_status` enum's fate** — wire `rejected`/`completed`/`no_show` transitions or drop them from the enum.
7. **(HOUSEKEEPING) Migrate pitches off `data.PitchModel` onto the `repository` pattern** for consistency with bookings (handoff §9).
8. **(HOUSEKEEPING) Fix the stale "middleware.ts" comments** to say `proxy.ts` (Next 16).

> No remediation was applied. All items above are recommendations only.

---

## Appendix A — PROJECT_HANDOFF.md changelog (this audit)

Exact edits made to `PROJECT_HANDOFF.md`, with rationale and evidence. **LOCKED architectural decisions were preserved verbatim** (httpOnly-cookie JWT, CSRF double-submit, phone-first OTP, Actor model, soft delete, signed Cloudinary upload). Only factual drift was corrected.

1. **§ "NEEDS CLARIFICATION" — `go.mod` version.** Removed the "looks like a typo, verify the toolchain" item. _Why:_ `go version` confirms `go1.26.3` is the genuinely installed toolchain matching `go.mod` (§6/§7 of this report).
2. **§9 Roadmap (Frontend).** Corrected PART 3 (booking flow / slot picker) and PART 4 (owner dashboard) from "not started" to "done / wired". _Why:_ the full player and owner flows are FULLY WIRED against live endpoints (§2 matrix).
3. **§4 Database Schema — `pitches` table.** Added the columns actually present in code (`surface`, `amenities`, `pitch_hue`, `latitude`, `longitude`, `rating`, `review_count`, `is_active`, `image_public_id`, `deleted_at`) and noted the `reviews` and `pitch_audit_log` tables and the silently-unused `description` column. _Why:_ §6 drift; `data/pitches.go:33-74`.
4. **§4 header — schema version.** Bumped "Migration 007" → "Migration 009" and added 008/009 to the run order. _Why:_ those migrations exist and are wired (`migrations/008,009`).
5. **§8 Known Constraints — added** the `description`-dropped bug and the orphaned `opt_out` endpoint as known gaps. _Why:_ §4/§3 of this report.
6. **Pointer to this audit** added near the top so future readers see the verified-state report.

_No other content was altered; intentional sections (deployment-paused note, locked decisions, cookie policy, open questions) were left intact._
