# PROJECT HANDOFF — Malaeb

> **CRITICAL: Read this before making any architectural or frontend changes.**

_Last updated: 2026-06-06 — read-only system audit reconciled this handoff against the actual code (see `docs/SYSTEM_AUDIT_2026-06-06.md`). Verified reality: the full player booking flow AND the owner/admin dashboard are FULLY WIRED to live endpoints; build/vet/tsc are green. Stale schema (§4) and roadmap (§9) corrected. Prior update 2026-06-04 — deployment paused; project reverted to local-only development (see §0). Auth-hardening pass (Steps C–E): httpOnly-cookie sessions, CSRF double-submit, and removal of legacy email/password auth. The two original critical conflicts are **resolved in code**._

---

## 0. ⏸️ DEPLOYMENT PAUSED — LOCAL-ONLY DEVELOPMENT

**As of 2026-06-04, the Vercel (frontend) and Railway (backend) deployment is paused.** The cross-site CORS/cookie configuration was causing too much friction. We are building all remaining features **purely locally** and will deploy cleanly to a proper domain once the project is finished.

**What this means right now:**

| Concern | Local-only setting |
|---------|--------------------|
| Frontend → backend URL | `frontend/.env.local` → `NEXT_PUBLIC_API_URL=http://localhost:8080/api/v1` |
| Backend CORS | Allows `http://localhost:3000` only (hardcoded + `CORS_ALLOWED_ORIGINS` in `backend/.env`) |
| `APP_ENV` | `development` → cookies are `SameSite=Lax` + `Secure=false` (works over plain-HTTP localhost) |
| Notification channel | `NOTIFICATION_CHANNEL=FAKE` → OTP code is **printed to the backend console**, no real WhatsApp/SMS needed |

**Local run (two terminals):**
```bash
# Terminal 1 — backend (from backend/)
go run ./cmd/api

# Terminal 2 — frontend (from frontend/)
npm install   # first time only
npm run dev
```

Frontend: <http://localhost:3000> · Backend API: <http://localhost:8080/api/v1>

**Login locally:** request an OTP from the login page, then read the code from the backend console line `[NOTIFY:FAKE] >>> OTP for +962... is 123456 <<<` and enter it.

> The production cookie policy (`SameSite=None`+`Secure`, Vercel↔Railway) described later in §2/§6 still documents how a future deploy must be configured — it is **dormant** while local-only. The hardcoded Vercel origin in `cmd/api/main.go` is harmless and left in place for that eventual redeploy.

---

## ✅ RESOLVED — original critical conflicts

The audit that produced the first version of this document flagged three conflicts between CLAUDE.md and the code. All three are now reconciled:

| # | Original conflict | Resolution |
|---|-------------------|-----------|
| 1 | JWTs stored in `localStorage` (CLAUDE.md required httpOnly cookies) | **Cookies.** Access + refresh JWTs are now delivered ONLY as httpOnly cookies (`malaab_access`, `malaab_refresh`). No token ever reaches JavaScript. localStorage token storage is gone. |
| 2 | No CSRF protection anywhere | **Implemented.** Double-submit-cookie CSRF (`malaab_csrf` readable cookie + `X-CSRF-Token` header), enforced by `middleware.RequireCSRF` on cookie-authenticated unsafe methods. (Was `SameSite=Strict` as a second layer; the decoupled Vercel/Railway deploy now requires `SameSite=None` in prod, so the double-submit token is the sole cross-site CSRF defence — see §2 cookie policy.) |
| 3 | CLAUDE.md product text said admin "confirms/rejects" bookings, but code auto-confirms | **Code is authoritative.** Instant booking (auto-`confirmed`, no pending step) is the locked decision per Architecture Principle 3. The CLAUDE.md product blurb is the stale text, not the code. |

**The legacy email/password auth path has also been removed** (Step C). Phone-first OTP is now the sole login method, matching Architecture Principle 1.

---

## ❓ NEEDS CLARIFICATION (still open)

- ~~`go.mod` declares `go 1.26.3` — verify the toolchain; looks like a typo~~ **Verified real** (audit 2026-06-06): `go version` → `go1.26.3 windows/amd64`. Not a typo.
- `booking_status` enum still includes `rejected`, `completed`, `no_show` — none of these transitions are wired in any handler or service. Wire them or drop them from the enum.
- **`description` pitch field is silently dropped** (audit 2026-06-06): the dashboard form collects it and the pitch-detail page renders it, but `data.CreatePitchRequest`/`UpdatePitchRequest` have no `Description` field and no SELECT reads the `description` column (present since migration 002). Wire it through or remove it from the UI. See audit §4.
- ~~`lib/api.ts` `isAuthEndpoint()` still lists `/auth/login`~~ **Removed** (Frontend PART 2 cleanup) — email login is gone.

---

## 1. Project Identity

**Malaeb** is an Arabic-first (RTL) SaaS platform for booking sports fields (football pitches) in Jordan. Players find and book pitches; field owners manage their pitches and may cancel bookings. Booking is instant-confirm — no pending approval step exists. Payments are deferred entirely.

| Layer | Technology | Version |
|-------|-----------|---------|
| Backend | Go + Gin | go.mod: `1.26.3` (verify) |
| Database | Neon Postgres (pgx/v5) | pgx v5.9.2 |
| Frontend | Next.js + React + Tailwind CSS (RTL Arabic) | see `frontend/package.json` |
| Notification | WhatsApp Cloud API (Meta) / SMS / Fake adapter | — |
| Auth | Phone OTP → JWT (access 15m + refresh 7d) delivered as **httpOnly cookies** | golang-jwt v5 |
| OTP hashing | HMAC-SHA256, pepper-keyed (`OTP_HMAC_PEPPER`) | — |

**Target users:** Players (book a pitch), Field Owners/Admins (manage pitches, view/cancel bookings).

> ⚠️ Frontend note: `frontend/AGENTS.md` warns this Next.js version has breaking changes vs. common knowledge — read `node_modules/next/dist/docs/` before writing frontend code.

---

## 2. Architecture Overview

```
┌─────────────────────────────────────┐
│  Next.js Frontend (port 3000)       │
│  AuthContext (in-memory user only)  │
│  lib/api.ts (axios, withCredentials)│
│  session = httpOnly cookies         │
└───────────────┬─────────────────────┘
                │ HTTP JSON + cookies (malaab_access/refresh/csrf)
                │ X-CSRF-Token header on unsafe methods
                ▼
┌─────────────────────────────────────┐
│  Gin HTTP Server (port 8080)        │
│  /api/v1/*                          │
│  middleware.RequireAuth  (cookie→JWT,│
│     Bearer fallback for API clients)│
│  middleware.RequireCSRF             │
│  middleware.RequireRole             │
└──┬──────────┬──────────┬────────────┘
   │          │          │
   ▼          ▼          ▼
handlers   handlers   handlers
(auth)   (bookings) (pitches/notify)
   │          │          │
   └──────────┴──────────┘
              │
       services / repositories
              │
   ┌──────────▼──────────┐
   │  Neon Postgres DB   │
   │  (pgx pool)         │
   └─────────────────────┘
              │ (async)
   ┌──────────▼──────────┐
   │  Outbox Worker      │  ← goroutine, SELECT FOR UPDATE SKIP LOCKED
   │  Reminder Worker    │  ← goroutine, SELECT FOR UPDATE SKIP LOCKED
   └──────────┬──────────┘
              │
   ┌──────────▼──────────┐
   │  NotificationService│
   │  (Fake|SMS|WhatsApp)│
   └─────────────────────┘
```

**Backend layering:** `handler → service → repository → pgx pool`
- Reads go directly handler → repository (no service needed).
- Writes (state transitions) go handler → `booking.Service` which persists audit + enqueues notification via the outbox.
- OTP dispatches are **synchronous** (time-sensitive) through `NotificationService` directly.
- Booking notifications are **async** through the Postgres outbox queue.

**Session model (post-hardening):**
- Sign-in (OTP verify) and refresh both call `issueTokenPair`, which mints access + refresh JWTs and a CSRF token and sets them via `issueSessionCookies` (`handlers/cookies.go`).
- `malaab_access` / `malaab_refresh` are **httpOnly** (never readable by JS). `malaab_role`, `malaab_expiry`, `malaab_csrf` are readable companions (non-secret) for UX, the Next.js edge guard, and the CSRF echo.
- `malaab_refresh` is path-scoped to `/api/v1/auth` so the long-lived secret isn't sent on every request.
- Cookie `SameSite`/`Secure` are environment-derived via `cookieSecurity(cfg)` in `handlers/cookies.go`: **production → `SameSite=None` + `Secure=true`** (required so the cookies ride cross-site from the Vercel frontend to the Railway backend; `None` mandates `Secure`, satisfied by Railway TLS); **dev/local → `SameSite=Lax` + `Secure=false`** (localhost:3000→8080 is same-site, and plain-HTTP dev would otherwise drop `Secure` cookies). Because production no longer gets the `SameSite=Strict` transport-layer backstop, **cross-site CSRF defence relies purely on the double-submit token** (`malaab_csrf` cookie ↔ `X-CSRF-Token` header, enforced by `middleware.RequireCSRF`). `X-CSRF-Token` is in the backend CORS `AllowHeaders` so the header survives the cross-origin preflight.
- `RequireAuth` reads the access token from the cookie first, then falls back to `Authorization: Bearer` for programmatic/test clients.

**Frontend → Backend:** `NEXT_PUBLIC_API_URL` (default `http://localhost:8080/api/v1`). Axios uses `withCredentials: true`; a request interceptor echoes `malaab_csrf` into `X-CSRF-Token` on POST/PUT/PATCH/DELETE. A single-flight response interceptor calls `/auth/refresh` once on 401 and retries. `AuthContext` holds only the in-memory user, rehydrated on load via `GET /auth/me`.

---

## 3. Completed Work Log

| Part | Goal | Key Files | Decisions |
|------|------|-----------|-----------|
| Backend PART 1 | Phone-first schema, booking state machine, audit trail | `003_*.up.sql`, `models/booking.go`, `models/user.go` | enum rebuilt via type swap (transactional); `payment_status` dormant seam added |
| Backend PART 2 | Pitch CRUD + owner management | `002_owner_pitches.sql`, `data/pitches.go`, `handlers/pitches.go` | `owner_id` added to pitches; admin bypasses ownership check with `ownerID=0` |
| Backend PART 3 (3A) | Email/password auth + JWT | (since removed — see Step C) | _superseded_ |
| Backend PART 3B | Phone OTP auth | `handlers/phone_auth.go`, `otp/`, `004_*.up.sql` | HMAC pepper-keyed OTP digests; rate limiting per phone+IP via sliding window in DB; E.164 normalisation defaults +962 (Jordan) |
| Backend PART 4 | WhatsApp adapter + SMS fallback | `notification/whatsapp.go`, `notification/sms.go`, `notification/fallback.go` | WhatsApp wraps SMS in `FallbackChannel`; Fake adapter for dev; routed by `NOTIFICATION_CHANNEL` env var |
| Backend PART 5 | Booking service + cancel + audit | `booking/service.go`, `handlers/bookings.go`, `repository/booking_repository.go` | All state transitions write `status_transitions`; `ErrDoubleBooking` from DB EXCLUDE constraint |
| Backend PART 6 | Notification outbox + webhooks + opt-out | `notification/outbox/`, `handlers/notifications.go`, `handlers/whatsapp_webhook.go`, `005_*.up.sql` | Pure Postgres outbox; exponential backoff; dead-letter after 5 attempts; `FailureMonitor` |
| Backend PART 7 | 24h booking reminder cron | `booking/reminder_worker.go`, `repository/reminder_repository.go`, `006_*.up.sql` | `SELECT FOR UPDATE SKIP LOCKED` + atomic `reminder_sent=true` + enqueue in one TX |
| **Auth hardening (Steps C–E)** | Resolve the two critical conflicts; remove legacy auth | `handlers/cookies.go`, `middleware/csrf.go` (+`_test.go`), `middleware/auth.go`, `handlers/auth.go`, `handlers/phone_auth.go`, `routes/routes.go`, `007_*.sql`, frontend `lib/api.ts` / `context/AuthContext.tsx` / `app/login` | **(C)** Email/password login + register removed; `auth/password.go`, `data/bookings.go`, `app/register/page.tsx` deleted. **(D)** httpOnly-cookie sessions + CSRF double-submit; data path migrated to `repository.BookingRepository`. **(D.5)** Live smoke test confirmed the booking INSERT/overlap SQL runs against the migrated schema (throwaway tool, since deleted). **(E)** This handoff. |

---

## 4. Database Schema

**Current schema version: Migration 009** (run in order: 002 → 003 → 004 → 005 → 006 → 007 → 008 → 009)

> Audit 2026-06-06 added migrations **008** (pitch soft-delete `deleted_at` + `pitch_audit_log` table) and **009** (`pitches.image_public_id`), which the earlier "version 007" line predated.

> Note: Migration 002 has no paired `.down.sql`. The base `pitches`/`bookings`/`users` tables predate migration 002 and are not reproduced in this repo's migration set — 002+ only alter them. Treat the live Neon schema (verified by the D.5 smoke test) as ground truth.

### `users`
| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| full_name | TEXT NULL | nullable for phone-only accounts |
| email | TEXT NULL UNIQUE | optional/secondary identifier; **kept** after migration 007 |
| phone | TEXT NULL UNIQUE | E.164, CHECK `^\+[1-9][0-9]{1,14}$` |
| ~~password_hash~~ | — | **DROPPED in migration 007** (legacy auth removed) |
| role | VARCHAR | `player` / `owner` / `admin` |
| phone_verified | BOOL DEFAULT false | |
| opt_in | BOOL DEFAULT false | must be true before OTP dispatch |
| opt_out | BOOL DEFAULT false | blocks ALL notifications when true |
| created_at / updated_at | TIMESTAMPTZ | |

### `pitches`
> Corrected by the 2026-06-06 audit — the prior version of this table omitted most columns the code actually reads/writes (see `data/pitches.go:33-74`). Truth derived from migrations + the Go scan columns.

| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| name | TEXT | |
| neighborhood | TEXT | |
| surface | `pitch_surface` ENUM | e.g. `artificial_grass`/`natural_grass`/`futsal_court` |
| format | `pitch_format` ENUM | e.g. خماسي / سباعي |
| price_per_hour | NUMERIC | |
| rating | NUMERIC | legacy column; live rating is computed by LEFT JOIN on `reviews` |
| review_count | INT | legacy column; live count from `reviews` join |
| is_featured | BOOL | |
| amenities | TEXT[] | defaults `'{}'` |
| pitch_hue | TEXT | UI accent colour, assigned at create |
| latitude / longitude | NUMERIC | map coords (default 0) |
| is_active | BOOL DEFAULT true | activate/deactivate toggle; players see only `is_active = true` |
| owner_id | INT FK users | added in migration 002 |
| description | TEXT DEFAULT '' | **present but NOT wired in Go** — never inserted or selected (audit §4) |
| image_url | TEXT DEFAULT '' | migration 002 |
| image_public_id | TEXT DEFAULT '' | migration 009 — Cloudinary destroy handle |
| deleted_at | TIMESTAMPTZ NULL | migration 008 — soft delete; all read queries filter `deleted_at IS NULL` |

> Related tables not previously listed: **`reviews`** (pre-002; LEFT-JOINed for rating/count) and **`pitch_audit_log`** (migration 008; records pitch activate/deactivate/delete with actor + role).

### `bookings`
| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| pitch_id | INT FK pitches | |
| **player_id** | INT FK users | the booking owner (code/SQL use `player_id`, **not** `user_id`) |
| booking_range | **tsrange** (timestamp; UTC by convention) | written via `tsrange($lo::timestamp,$hi::timestamp,'[)')`; GIST EXCLUDE: no overlap WHERE `status <> 'cancelled'` |
| status | booking_status ENUM | `pending/confirmed/rejected/completed/cancelled/no_show` — only `confirmed`/`cancelled` exercised |
| total_price | NUMERIC | computed from `price_per_hour × duration` |
| payment_status | payment_status ENUM | `unpaid` ONLY — dormant seam |
| reminder_sent | BOOL DEFAULT false | flipped true when 24h reminder enqueued |
| created_at | TIMESTAMPTZ | |

### `status_transitions` (audit, append-only)
| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL PK | |
| booking_id | INT FK bookings CASCADE | |
| from_status | booking_status NULL | NULL = initial creation |
| to_status | booking_status | |
| actor_id | INT FK users SET NULL | NULL = system |
| actor_role | VARCHAR(20) | `player/owner/admin/system` |
| reason | TEXT NULL | |
| created_at | TIMESTAMPTZ | |

### `otp_codes`
| Column | Type | Notes |
|--------|------|-------|
| phone | TEXT PK | one active code per phone; UPSERT on resend |
| code_hash | TEXT | HMAC-SHA256 digest, never plaintext |
| expires_at | TIMESTAMPTZ | |
| attempts | INT DEFAULT 0 | failed verify attempts; drives lockout |
| created_at | TIMESTAMPTZ | |

### `otp_rate_events`
| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL PK | |
| bucket_key | TEXT | `phone:<e164>` or `ip:<addr>` |
| created_at | TIMESTAMPTZ | rows older than window are pruned on each check |

### `notification_jobs` (outbox)
| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL PK | |
| recipient | TEXT | phone number |
| kind | TEXT | `booking_confirmed / booking_cancelled / booking_reminder` |
| envelope | JSONB | full `OutboundMessage` payload |
| status | TEXT | `pending/processing/succeeded/dead_letter/blocked` |
| attempts | INT | |
| max_attempts | INT DEFAULT 5 | |
| next_attempt_at | TIMESTAMPTZ | backoff target |
| last_error | TEXT NULL | |
| provider_message_id | TEXT NULL | filled after provider accepts |

### `message_deliveries` (webhook tracking)
| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL PK | |
| provider_message_id | TEXT UNIQUE | idempotent UPSERT from webhook |
| job_id | BIGINT FK NULL | |
| recipient | TEXT | |
| status | TEXT | `sent/delivered/read/failed` |
| error_code / error_title | INT / TEXT | Meta error details |
| raw | JSONB | full webhook payload |

---

## 5. API Contract

Base: `http://localhost:8080/api/v1`
Browser auth: httpOnly session cookies (set at OTP verify). Programmatic clients may still use `Authorization: Bearer <access_token>`.
CSRF: state-changing cookie-authenticated requests must send `X-CSRF-Token` equal to the `malaab_csrf` cookie. Bearer-authenticated and safe (GET/HEAD/OPTIONS/TRACE) requests are exempt.

### Auth
| Method | Path | Auth | Body | Response |
|--------|------|------|------|----------|
| POST | /auth/request-otp | No | `{phone, opt_in: bool}` | `{message}` |
| POST | /auth/verify-otp | No | `{phone, code}` | `{message, data: {expires_in_seconds, user}}` + sets session cookies |
| POST | /auth/refresh | Cookie + CSRF | — (refresh token rides in httpOnly cookie) | `{message, data: authResponse}` + rotates cookies |
| GET | /auth/me | Yes | — | `{data: SafeUser}` — session rehydration |
| POST | /auth/logout | Yes | — | `{message}` + clears cookies, revokes all refresh tokens |

> **Removed (Step C):** `POST /auth/register` and `POST /auth/login` (email/password) no longer exist. Response bodies for auth no longer contain `access_token` / `refresh_token` — tokens are cookie-only.

### Pitches
| Method | Path | Auth | Role | Notes |
|--------|------|------|------|-------|
| GET | /pitches | No | — | `?neighborhood=&format=&featured=true` |
| GET | /pitches/:id | No | — | |
| POST | /pitches | Yes | owner/admin | `{name, neighborhood, format, price_per_hour, is_featured, description, image_url}` |
| PATCH | /pitches/:id | Yes | owner/admin | Partial update; admin bypasses ownership |
| DELETE | /pitches/:id | Yes | owner/admin | |
| GET | /owner/pitches | Yes | owner/admin | Admin sees all pitches |

### Bookings
| Method | Path | Auth | Role | Notes |
|--------|------|------|------|-------|
| POST | /bookings | Yes | any | `{pitch_id, start_time, end_time, total_price}` → instant `confirmed` |
| GET | /bookings | Yes | any | Player's own bookings |
| GET | /admin/bookings | Yes | owner/admin | All bookings with user info |
| GET | /pitches/:id/availability | Yes | any | `?date=YYYY-MM-DD` → booked slots |
| PATCH | /bookings/:id/cancel | Yes | player/owner | `{reason?}` → `cancelled` |

### Notifications
| Method | Path | Auth | Notes |
|--------|------|------|-------|
| POST | /notifications/opt-out | Yes | Sets `opt_out=true`, blocks all future messages |
| GET | /webhooks/whatsapp | No | Meta webhook verify handshake |
| POST | /webhooks/whatsapp | No | Meta status callbacks → `message_deliveries` |

---

## 6. Critical Architectural Decisions

**Phone-first OTP is the SOLE login.** `EnsureVerifiedUser` upserts a user row keyed by phone on successful OTP verify. Email/password auth was removed in Step C; `email` remains as an optional secondary identifier only.

**httpOnly-cookie sessions (LOCKED — was the #1 conflict).** Access + refresh JWTs are delivered only as httpOnly cookies and never appear in a response body or in JavaScript. `handlers/cookies.go` owns all cookie plumbing. Do NOT reintroduce localStorage token storage. Cookie `SameSite`/`Secure` are environment-derived via `cookieSecurity(cfg)`: prod → `None`+`Secure` (cross-site Vercel↔Railway); dev → `Lax`+insecure. With `SameSite=None` in prod, the double-submit CSRF token is the sole cross-site CSRF defence.

**CSRF double-submit (LOCKED — was the #2 conflict).** `middleware.RequireCSRF` (constant-time compare of `malaab_csrf` cookie vs. `X-CSRF-Token` header). Exempt: safe methods and Bearer-authenticated requests. Applied to the whole protected group and explicitly to `/auth/refresh` (which lives outside that group). The frontend interceptor in `lib/api.ts` echoes the cookie automatically.

**Refresh token rotation.** Refresh tokens are hashed (SHA-256) before DB storage. `FindAndConsumeRefreshToken` is a one-shot atomic consume — the token is revoked on use, and a rejected refresh clears the session cookies.

**HMAC OTP digests.** `OTP_HMAC_PEPPER` keys all stored OTP hashes. Rotating it invalidates all in-flight OTPs.

**Postgres Outbox (no broker).** `notification_jobs` is the durable queue; `outbox.Worker` drains it with `SELECT … FOR UPDATE SKIP LOCKED`. Shares the HTTP server's `pgx.Pool`.

**WhatsApp/SMS FallbackChannel.** `NewFallbackChannel(wa, sms)` is registered as the WhatsApp channel; on WhatsApp delivery failure SMS is tried silently. Do NOT call the Meta SDK outside `notification/whatsapp.go`.

**Reminder atomicity.** `reminder_worker.go` wraps `UPDATE bookings SET reminder_sent=true` + `INSERT INTO notification_jobs` in one transaction with `FOR UPDATE SKIP LOCKED`. Never split these.

**Booking auto-confirm.** `booking.Service.Create` sets status directly to `confirmed` (no `pending` step). `CancelBooking` permits only `confirmed → cancelled`; cancelling releases the slot (the EXCLUDE constraint ignores cancelled rows).

**Opt-out beats opt-in.** `users.opt_out=true` blocks all messages regardless of `opt_in`, checked at the `NotificationService` gate.

---

## 7. Conventions & Patterns

**Naming:** snake_case DB columns; Go structs PascalCase; files snake_case. Repository sentinel errors prefixed `Err` (e.g. `ErrDoubleBooking`, `ErrUserNotFound`).

**Error handling:** Sentinel errors propagate repository → handler; handler maps to HTTP via `switch errors.Is(...)`. Never leak internal error text to clients. OTP verify failures collapse to a single 401 to avoid an oracle.

**Transactions:** All multi-step writes (`CreateBooking`, `CancelBooking`, reminder worker) use explicit `pgx` transactions with `defer tx.Rollback`.

**Testing:** Unit tests use in-memory fakes for stores/channels. Every new module has a paired `_test.go` (e.g. `middleware/csrf_test.go`). The full suite passes (`go test ./...`). The D.5 booking smoke test was a one-off live check against Neon, run inside a rolled-back transaction, and has been deleted.

**Arabic/RTL:** Backend returns some Arabic user-facing strings. Frontend uses Tailwind with `dir="rtl"`. No i18n library yet — strings are inline.

**Default country code:** Phone normalisation prepends `+962` (Jordan) for local-format numbers. Centralised in `handlers/phone_auth.go:defaultCountryCode`.

---

## 8. Known Constraints & Gotchas

- **WhatsApp AUTHENTICATION templates** may not be approved for a new Meta account. The Fake/SMS fallback exists for exactly this. Never assume OTP-over-WhatsApp works in a fresh deployment.
- **`booking_status` enum** has `rejected`, `completed`, `no_show` with no code path. Dead weight or future work.
- **`data/` vs `repository/` packages:** pitches still use the older `data.PitchModel` pattern; bookings now use `repository.BookingRepository` (the legacy `data/bookings.go` was deleted in Step C). Pitches are the remaining inconsistency to migrate.
- **`go.mod` version** (`1.26.3`) is suspicious — verify the installed toolchain.
- **Cookie `SameSite`/`Secure` are env-gated** (`cookieSecurity(cfg)`): prod → `SameSite=None`+`Secure=true`, dev → `SameSite=Lax`+`Secure=false`. Production MUST set `APP_ENV=production` — otherwise cookies fall through to `Lax`+insecure and will NOT be sent on cross-site requests from the Vercel frontend, silently breaking the session. TLS is terminated at the proxy (Railway); there is no in-app HTTPS.
- **Access-token denylist absent:** logout revokes refresh tokens and clears cookies, but a stolen access token stays valid up to 15 minutes. A Redis/DB denylist would close this.
- **WhatsApp webhook signature** (`X-Hub-Signature-256`) is not yet validated — only the verify-token handshake is secured.
- **`OTP_HMAC_PEPPER` in `.env.example`** is a placeholder; generate an independent secret per environment and never reuse `JWT_SECRET`.
- **`description` pitch field silently dropped** (audit 2026-06-06): collected in the dashboard form and rendered on pitch-detail, but not wired in the Go data layer — entered descriptions are lost. See audit §4.
- **`POST /notifications/opt-out` is orphaned** (audit 2026-06-06): implemented and registered, but no frontend consumer — consent withdrawal is unreachable from the UI.

---

## 9. Roadmap / Next Steps

### Frontend
| Part | Goal | Status |
|------|------|--------|
| PART 1 | App shell + auth + pitch list + booking flow | done (now cookie-based; localStorage removed) |
| PART 2 | OTP login/verify UI fully wired to cookie session + `/auth/me` rehydration | **done** (audit 2026-06-06 verified the full flow: request-otp → verify-otp → cookie session → `/auth/me` rehydration → single-flight refresh → logout) |
| PART 3 | Booking flow: pitch detail page with calendar/slot picker, booking confirmation | **done** (audit 2026-06-06: `pitches/[id]/BookingForm.tsx` slot-picker → `GET /pitches/:id/availability` + `POST /bookings`, server-time-anchored, with success/confirmation screen — FULLY WIRED) |
| PART 4 | Owner dashboard: booking list, cancel button, pitch management UI | **done** (audit 2026-06-06: `/admin/bookings`, cancel modal, pitch create/edit/delete, activate-toggle, Cloudinary image upload — all FULLY WIRED) |

### Backend (open items)
- Wire `rejected`, `completed`, `no_show` status transitions or remove them from the enum.
- Add a migration runner (or apply migrations to production; currently manual `psql -f`).
- Implement WhatsApp webhook signature validation (`X-Hub-Signature-256`).
- Access-token denylist (Redis or DB) for immediate logout invalidation.
- Migrate pitches off the legacy `data.PitchModel` onto the `repository` pattern for consistency.

### Open questions for the owner
1. ~~localStorage vs httpOnly cookies?~~ **Resolved → httpOnly cookies + CSRF.**
2. ~~Keep legacy email/password auth?~~ **Resolved → removed; phone OTP only.**
3. Is `+962` (Jordan) the permanent default country code, or will multi-country support be needed?
4. Should `email` collection be surfaced anywhere in the UI now that it's optional/secondary, or stay backend-only?
