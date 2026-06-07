# Malaeb — Production Readiness Audit

**Auditor role:** Senior staff engineer (read-only pre-launch audit)
**Date:** 2026-06-07
**Scope:** Go REST API + Next.js App Router + Neon Postgres + WhatsApp/SMS notifications + Postgres outbox + cron workers
**Mandate:** Findings only — no code was changed. Severities: **BLOCKER** (must not ship) / **HIGH** / **MEDIUM** / **LOW**.

> ⚠️ **Runtime caveat:** This is a static audit. No production environment, live Neon database, or running deployment was reachable. Findings that require runtime verification (actual env values on Railway/Vercel, the live Neon endpoint string, whether migrations 002–010 are actually applied) are flagged as **VERIFY AT RUNTIME** and cannot be confirmed from the repo alone. Per PROJECT_HANDOFF.md §0, deployment is currently *paused* and the project is local-only; several "production" findings below are therefore **dormant risks** that bite the moment a real deploy happens.

---

## 🔺 PRIORITISED BLOCKER / HIGH SUMMARY

| # | Sev | Area | Finding | Location |
|---|-----|------|---------|----------|
| 1 | **HIGH** | Config | `cookieSecurity` and Gin mode **fail OPEN**, not closed: any `APP_ENV` value other than the exact string `production` yields `SameSite=Lax`+`Secure=false` and Gin debug mode. A typo/unset env in prod silently ships insecure cookies + debug error pages. | `handlers/cookies.go:51-56`, `cmd/api/main.go:38-40` |
| 2 | **HIGH** | DB | **VERIFY AT RUNTIME** — cannot confirm the live Neon DSN uses the **pooled** (`-pooler`) endpoint. With `MaxConns=20` per instance against a non-pooled endpoint, Neon's connection ceiling is exhausted under scale. The fallback DSN also hardcodes `sslmode=disable`. | `config/config.go:139-147,276-289`, `database/db.go:28` |
| 3 | **HIGH** | Auth | **No access-token denylist.** A stolen/leaked access JWT stays valid for its full 15 min after logout — logout only revokes refresh tokens. Acknowledged in handoff §8 but unmitigated. | `handlers/auth.go:97-115` |
| 4 | **MEDIUM→HIGH** | Webhooks | **WhatsApp webhook HMAC (`X-Hub-Signature-256`) is NOT validated.** Any unauthenticated party can POST forged delivery-status callbacks into `message_deliveries`. HIGH once WhatsApp is the live channel. | `handlers/whatsapp_webhook.go:77-119` |
| 5 | **MEDIUM** | DB | Missing indexes on `bookings.player_id` and `pitches.owner_id` — both are hot WHERE/JOIN predicates (user bookings list, admin/owner scoping). Sequential scans grow linearly with data. | migrations (none defined) |
| 6 | **MEDIUM** | DB | `bookings.booking_range` is **`tsrange` (no timezone)**, not `tstzrange`. Correctness depends entirely on an app-level "always UTC" convention; any non-UTC write corrupts the anti-overlap guarantee. | `migrations/003…up.sql`, `repository/booking_repository.go:201` |
| 7 | **MEDIUM** | Frontend | No production **security headers** (CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy) configured in `next.config.ts`. | `frontend/next.config.ts` |
| 8 | **MEDIUM** | Deps | `npm audit`: 2 moderate vulns (postcss `<8.5.10` XSS via the bundled Next.js toolchain). govulncheck: see §7. | `frontend/package.json` |

**No BLOCKERS found.** The single designated BLOCKER check (DB-level double-booking exclusion constraint) **PASSES** — see §3.

---

## Section 1 — Secrets & Config Hygiene

**Git history secret scan — PASS.**
- `.env` is **not** tracked; only `.env.example` is committed (`git ls-files` confirms). Both `backend/.gitignore` and the repo-root `.gitignore` exclude `.env`/`.env.*` with an `!.env.example` allowlist. ✅
- Full-history scan (`git log --all -p` grepped for `postgres://`, `sk_live`, `AC[0-9a-f]{32}`, `eyJ`, `BEGIN…PRIVATE`, `AUTH_TOKEN=`, `JWT_SECRET=`, `PEPPER=`) surfaced **only placeholders** (`change-me-…`, `dummy-…`, `ACxxxx…`, `postgres://...` in test-doc comments). No real credentials were ever committed. ✅
- `.env.example` values are all placeholders (`change-me-to-a-long-random-string…`, `dummy-meta-cloud-api-token`). ✅

**Fail-closed switches — FAIL (Finding #1, HIGH).**
- `cookieSecurity(cfg)` (`handlers/cookies.go:51-56`) returns secure cookies **only** when `cfg.AppEnv == "production"` exactly; every other value (including a typo like `Production`, `prod`, or an unset `APP_ENV` defaulting to `development` at `config.go:188`) → `SameSite=Lax` + `Secure=false`. This is **fail-open**: a misconfigured prod env silently downgrades cookie security.
- Gin mode (`cmd/api/main.go:38-40`) likewise only enters `ReleaseMode` when `AppEnv == "production"`; otherwise Gin runs in **debug mode**, which emits verbose stack/route diagnostics. Fail-open.
- **Recommendation (not applied):** invert the defaults — treat anything that isn't an explicit `development`/`local` as production-secure, or hard-fail boot if `APP_ENV` is unrecognised.

**Required-secret hardening — PASS (good).** `config.Load()` panics at boot on missing/short `JWT_SECRET` (<32), `OTP_HMAC_PEPPER` (<16), Cloudinary creds, and DB creds; partial Twilio config fails fast (`config.go:164-228,245-253`). OTP routing safety assertion fails boot if OTP can't resolve to a real sender (`cmd/api/main.go:144-146`). These are exemplary fail-closed checks.

**LOW:** `BCRYPT_COST` is still loaded and validated (`config.go:169-171`) though password auth was removed — dead config, harmless.

---

## Section 2 — CORS & Cookies

**CORS — PASS with a dormant caveat (LOW/MEDIUM).**
- Allow-list is exact-match via `AllowOriginFunc` with origin normalisation (trim + strip trailing `/`), `AllowCredentials: true`, and a bounded method/header set including `X-CSRF-Token` and `Idempotency-Key` (`cmd/api/main.go:238-276`). ✅ No wildcard `*` origin (which would be illegal with credentials anyway). ✅
- **Dormant caveat:** `http://localhost:3000` is **hardcoded into the prod allow-list** alongside the Vercel origin (`main.go:243-247`). In production this means a plain-HTTP localhost origin remains an accepted CORS origin. Low real-world exploitability (an attacker can't make a victim's browser originate from the victim's localhost trivially), but it violates "prod locked to real domain." The Vercel origin is also hardcoded; acceptable as a documented fallback but should ideally be env-only in prod.

**Cookies — PASS on mechanism, see Finding #1 for the fail-open default.**
- httpOnly access + refresh JWTs; readable role/expiry/csrf companions (`cookies.go:62-83`). ✅
- Refresh cookie path-scoped to `/api/v1/auth` (`cookies.go:35,71`). ✅
- `clearSessionCookies` matches set-paths on logout/refresh-reject (`cookies.go:98-106`). ✅
- Prod topology `SameSite=None`+`Secure` is correct for cross-site Vercel↔Railway, **conditional on `APP_ENV=production` being set exactly** (Finding #1).

---

## Section 3 — Database (Neon Postgres)

**🟢 BLOCKER CHECK — DB-level double-booking exclusion constraint: PASS.**
`migrations/003_phone_first_auth_and_booking_states.up.sql` defines:
```sql
ALTER TABLE bookings ADD CONSTRAINT bookings_pitch_id_booking_range_excl
  EXCLUDE USING GIST (pitch_id WITH =, booking_range WITH &&)
  WHERE (status <> 'cancelled');
```
Backed by `CREATE EXTENSION IF NOT EXISTS btree_gist`. The repository relies on it correctly: `insertConfirmedBookingTx` maps SQLSTATE `23P01` → `ErrDoubleBooking` (`booking_repository.go:40,222-226`), and cancel releases the slot because the constraint ignores `cancelled` rows. Double-booking cannot occur at the data layer even under concurrent inserts. ✅
**VERIFY AT RUNTIME:** that migration 003 is actually applied to the live Neon DB (handoff §"DB migration drift" notes there is no `schema_migrations` ledger and migrations are applied manually). A live `\d bookings` is required to be certain the constraint exists in prod.

**Pooled endpoint — VERIFY AT RUNTIME (Finding #2, HIGH).** Code accepts `DATABASE_URL` verbatim (`config.go:277-279`); whether it points at Neon's pooled (`-pooler`) host cannot be determined statically. With `MaxConns=20` (`config.go:150`, default) per instance, a direct (non-pooled) endpoint will exhaust Neon's backend limit under modest horizontal scaling. The individual-field fallback DSN hardcodes **`sslmode=disable`** (`config.go:144`) — unsafe for any networked Postgres; only safe because prod is expected to use `DATABASE_URL` (with `sslmode=require`). Confirm the prod connection string.

**Indexes — PARTIAL (Finding #5, MEDIUM).**
Present & appropriate: unique `idx_users_phone_unique`; `idx_status_transitions_booking`; composite `idx_otp_rate_events_bucket_time`; partial `idx_notification_jobs_due`; partial `idx_bookings_reminder_due`; `idx_pitches_active`; `idx_pitch_audit_log_pitch`; `idx_booking_idempotency_expires_at`; unique `booking_idempotency_user_key_uniq`. The GIST exclusion index also serves `pitch_id` lookups.
**Missing:**
- `bookings.player_id` — hot predicate in `GetUserBookings` (`booking_repository.go:437`) and player-scoped cancel; no index → seq scan.
- `pitches.owner_id` — used in `GetAllBookings` owner join (`booking_repository.go:475`) and owner-scoped cancel (`:625`); no index.
- (LOW) `status_transitions.actor_id` and `message_deliveries.job_id` FKs are unindexed; low-traffic, acceptable for launch.

**timestamptz usage — MOSTLY PASS, one gap (Finding #6, MEDIUM).** All `created_at`/`updated_at`/`expires_at`/`next_attempt_at` columns are `TIMESTAMPTZ`. ✅ **But** `bookings.booking_range` is **`tsrange`** (timestamp *without* tz), with UTC enforced only by convention in Go (`booking_repository.go:201,213` casts `::timestamp` and passes `.UTC()`). The overlap-correctness BLOCKER guarantee therefore silently depends on every writer using UTC; a future code path inserting local time would break the anti-overlap semantics without any DB-level protection. Consider `tstzrange`.

**Other DB notes (LOW):** `payment_status` enum has a single dormant `unpaid` value — intentional per Architecture Principle 3. ✅ `booking_status` enum carries `rejected`/`completed`/`no_show` with no code path (dead weight; handoff-acknowledged).

---

## Section 4 — Auth, OTP & Abuse

**JWT algorithm pinning — PASS (excellent).** `validateToken` rejects non-HMAC methods, pins `HS256` via `jwt.WithValidMethods`, requires expiration, and guards token-type confusion (access vs refresh) (`auth/jwt.go:116-157`). The `alg:none` attack is closed. ✅

**Refresh rotation — PASS.** Refresh tokens are 256-bit random, **SHA-256 hashed before storage** (`jwt.go:87-96`), and consumed one-shot via `FindAndConsumeRefreshToken` (`auth.go:63`); rejected refresh clears cookies (`auth.go:66-73`). Rotation on every refresh. ✅

**OTP rate limiting — PASS (strong, defense-in-depth).** `otp/service.go` layers: resend cooldown (checked first, doesn't burn quota), per-phone burst + hourly + daily, per-IP burst + per-minute + per-hour, and a **global daily/hourly circuit breaker** (`service.go:76-92,239-276`). All evaluated *before* code generation/dispatch, so abuse costs zero messages. Codes are HMAC-pepper hashed, uniform crypto-random (no modulo bias), one-time-use, with verify-attempt lockout (`service.go:282-342`). Verify failures collapse to avoid an oracle. ✅

**BOLA / IDOR — PASS (well done).** Mutations are ownership-scoped **in SQL**, not just in handlers:
- `CancelBooking` resolves+locks the row with an actor predicate (`admin`=any, `owner`=`pitches.owner_id`, `player`=`bookings.player_id`); a non-matching row → `ErrBookingNotFound`/404, never leaking existence (`booking_repository.go:601-650`). ✅
- `GetAllBookings` applies the owner predicate in the join (`:467-476`). ✅
- Pitch mutations gated by `RequireRole("owner","admin")` and (verify in `data/pitches.go`) actor scoping; admin bypass via `ownerID=0` is documented.
- Unknown/empty role is denied categorically (`:632-634`). ✅

**Webhook HMAC — FAIL (Finding #4).** `WhatsAppWebhookHandler.Receive` parses and persists status callbacks with **no `X-Hub-Signature-256` verification** (`whatsapp_webhook.go:77-119`); only the GET verify-token handshake is secured (`:54-66`). Anyone who learns the public webhook URL can inject forged delivery statuses into `message_deliveries`. Contained today (deployment paused, WhatsApp not the active channel), but a launch blocker for the WhatsApp go-live workstream. Handoff §8 acknowledges this is unbuilt.

**Access-token denylist — FAIL (Finding #3, HIGH).** No revocation list; a stolen access token survives logout up to 15 min (`auth.go:97-99` comment admits it). Mitigated only by the short TTL.

---

## Section 5 — Backend Resilience & Scale

**Idempotency-Key — PASS (excellent).** `CreateBookingIdempotent` claims `(user_id, idem_key)` via `INSERT … ON CONFLICT DO NOTHING` in the **same transaction** as the booking insert, with fingerprint mismatch → 422, in-progress → 409, completed → replay-without-side-effects (`booking_repository.go:259-348`). User-scoped keys prevent cross-user replay. 24h TTL with a background sweep (`main.go:211-228`). ✅

**Outbox load-safety — PASS.** `ClaimDue` uses `FOR UPDATE SKIP LOCKED` (`outbox/postgres.go:64`), and the reminder claim does too (`reminder_repository.go:123`), so horizontally-scaled workers never double-process. Exponential backoff with cap + dead-letter after max attempts + `blocked` terminal state for consent/validation failures (`outbox/worker.go:190-213`). ✅

**Idempotent cron — PASS.** Reminder worker flips `reminder_sent=true` **and** enqueues the outbox job in one transaction under `SKIP LOCKED` (`reminder_repository.go`), so a reminder is queued exactly once across restarts/instances. Idempotency sweep failure only logs (pure hygiene) (`main.go:215-220`). ✅

**Timeouts — PARTIAL (MEDIUM/LOW).** HTTP server sets `ReadTimeout=15s`, `WriteTimeout=15s`, `IdleTimeout=60s` (`main.go:296-298`). ✅ DB pool has connect timeout + lifetimes (`database/db.go:30-36`). ✅ **Gap:** no per-request/per-handler context deadline on DB calls in handlers (most pass `c.Request.Context()` which is bounded by the 15s write timeout, acceptable). Outbox `Send` (HTTP to Twilio/Meta) timeout depends on the adapter's `http.Client` — **VERIFY** the Twilio/WhatsApp clients set an explicit timeout (a missing one risks a hung worker goroutine).

**Graceful shutdown — PASS.** SIGINT/SIGTERM → `stopWorker()` (stops claiming) → `server.Shutdown(10s)` drains in-flight HTTP (`main.go:308-322`). Workers exit on `workerCtx` cancel. ✅

**Health / readiness — PARTIAL (MEDIUM/LOW).** `GET /api/v1/ping` pings the DB with a 3s timeout and reports pool stats, 503 on DB-down (`handlers/health.go`). ✅ as a combined liveness+readiness. **Gaps:** no separate liveness vs readiness split (a readiness probe that fails on DB-down is correct, but a liveness probe that also fails on DB-down can cause unnecessary pod restarts); the endpoint does not check the outbox worker's health/liveness. Acceptable for launch, note for ops.

---

## Section 6 — Frontend

**Env-based API URL — PASS.** `NEXT_PUBLIC_API_URL` drives the axios base, defaulting to localhost only as a dev fallback (`lib/api.ts:3`). Prod must set the env (deployment paused, so `.env.local` legitimately points at localhost). ✅

**No secrets in public vars — PASS.** Only `NEXT_PUBLIC_API_URL` is exposed; no tokens/keys in `NEXT_PUBLIC_*`. Session is httpOnly-cookie only; no token ever touches JS (`lib/api.ts:16-23`). The CSRF cookie read is intentional and non-secret. ✅

**Route guard — PASS (UI-layer, correctly caveated).** `proxy.ts` (renamed from `middleware.ts` per Next 16 convention) guards `/dashboard/*` by the readable `malaab_role` cookie and redirects non-owner/admin; it correctly documents that the **backend** enforces the real check (`proxy.ts`). The cookie is non-authoritative (the signed JWT is), so spoofing `malaab_role` only grants UI access, not API access (the JWT role still gates every protected route). ✅

**Security headers — FAIL (Finding #7, MEDIUM).** `next.config.ts` defines redirects + image remote patterns but **no `headers()`** block: no CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, or Permissions-Policy. Add these before public launch (Vercel can also set some at the platform layer — VERIFY). `images.remotePatterns` includes `http://localhost:8080` and broad hosts (`*.supabase.co`, unsplash) — tighten for prod (LOW).

---

## Section 7 — Dependencies & Deploy

**npm audit — 2 moderate (Finding #8).**
```
postcss <8.5.10 — XSS via unescaped </style> in CSS stringify (GHSA-qx2v-qp2m-jg93)
  └─ pulled transitively by next (9.3.4-canary.0 - 16.3.0-canary.5)
2 moderate severity vulnerabilities
```
Fix requires a Next.js bump (`npm audit fix --force` would downgrade to next@9 — do NOT; upgrade Next forward instead, out of audit scope). Moderate severity, build-time/SSR-stringify surface; acceptable to launch with a tracked follow-up, not a blocker.

**govulncheck — 2 callable stdlib vulns (MEDIUM).** (`govulncheck` was not pre-installed; installed `golang.org/x/vuln/cmd/govulncheck@latest` for this run.) Both are **Go standard-library** issues fixed in **go1.26.4** — the project builds with go1.26.3. No fix needed beyond a toolchain bump:
```
GO-2026-5039  net/textproto — unescaped arbitrary input in errors (fixed go1.26.4)
   trace: notification/twilio.go:166 → io.ReadAll → textproto.Reader.ReadMIMEHeader
GO-2026-5037  crypto/x509 — inefficient candidate hostname parsing (fixed go1.26.4)
   trace: notification/twilio.go:166 (TLS verify on Twilio HTTPS call)
          handlers/pitches.go:327 (x509.HostnameError.Error, Cloudinary path)
2 affected (code calls them); +2 imported and +5 in required modules NOT called.
```
Both reachable only via outbound HTTPS to Twilio/Cloudinary (server-side, not attacker-controlled request paths). **Remediation:** rebuild with go ≥1.26.4. Low real exploitability; track as a launch follow-up, not a blocker.

**Toolchain note:** `go.mod` declares `go 1.26.3`; confirmed real (`go version` → `go1.26.3 windows/amd64`), not a typo. ✅

**Deploy posture:** No `schema_migrations` ledger and no migration runner — migrations are applied manually via `psql -f` (handoff §9). This is an operational risk: there is no programmatic guarantee that prod is at migration 010. **VERIFY AT RUNTIME** and strongly consider a migration runner before launch.

---

## Section 8 — Rollback & Launch-Day Runbook

### First-hour key metrics to watch
| Metric | Source | Healthy | Alarm |
|--------|--------|---------|-------|
| OTP send success rate | Twilio dashboard + `[NOTIFY]` logs | >95% accepted | sustained failures → login outage |
| OTP global daily counter | `otp_rate_events` global buckets | well under `OTP_GLOBAL_DAILY_CAP` (50 trial) | hitting cap → legit users blocked (raise cap off-trial) |
| `/api/v1/ping` status + pool stats | health endpoint | 200, `acquired_conns` ≪ `MaxConns` | 503 or acquired≈max → DB saturation (Finding #2) |
| Auth 401/refresh rate | Gin logs | low, stable | spike → cookie/SameSite misconfig (Finding #1) |
| Double-booking errors (`23P01`→409) | app logs | rare/expected | absent under load may mean constraint missing — verify |
| Outbox depth & dead-letters | `notification_jobs` by status | `pending` drains, few `dead_letter` | growing `pending` → worker stalled; `dead_letter` spike → provider down |
| Booking create success/latency | Gin logs | p95 < ~500ms | latency spike → missing indexes (Finding #5) |
| 5xx rate | Gin logs / platform | ~0 | any sustained 5xx |
| CORS preflight 403s | `[CORS]`/`[404]` logs | ~0 | origin misconfig |

### Step-by-step rollback procedure
1. **Declare & freeze.** Announce incident; stop any in-progress deploy/migration.
2. **Confirm scope.** Check `/api/v1/ping`, 5xx rate, OTP success, outbox depth to localise (app vs DB vs provider).
3. **App rollback first (fast, safe).** Re-deploy the previous known-good image/commit on Railway (backend) and the prior Vercel deployment (frontend). Both are stateless → instant rollback, no data risk.
4. **Provider isolation (if notification-driven).** If OTP/notifications are the failure: set `NOTIFY_OTP_ROUTE=FAKE` only as a last resort for internal testing (NOT for real users — they'd never get codes); preferably fail over Twilio config or pause signups.
5. **Connection storm (Finding #2).** If pool exhaustion: switch `DATABASE_URL` to the Neon **pooled** endpoint and/or lower `DB_MAX_CONNS`, redeploy.
6. **DB rollback — last resort, with care.** Migrations have **paired `.down.sql`** files (003–010). A down-migration is destructive (drops columns/constraints/data); take a Neon branch/snapshot **before** any down-migration and prefer forward-fixes. Never run a `.down.sql` against prod without a verified backup.
7. **Verify recovery.** Re-run the first-hour metric checks; confirm a test OTP login + a test booking end-to-end.
8. **Post-incident.** Capture timeline; file follow-ups for any finding that contributed.

---

## Appendix — What PASSED (explicit)
- Secrets never committed; `.env` gitignored; required secrets fail-closed at boot.
- DB double-booking EXCLUDE constraint present (the designated BLOCKER) ✅.
- JWT `alg` pinning + type-confusion guard + expiration required.
- Refresh-token rotation, hashed-at-rest, one-shot consume.
- Multi-layer OTP rate limiting + global breaker; pepper-hashed one-time codes.
- BOLA/IDOR closed via SQL-level ownership scoping; no existence leak.
- Idempotent booking creation; outbox + reminder workers use `FOR UPDATE SKIP LOCKED`.
- Graceful shutdown with worker drain.
- httpOnly-cookie sessions; no token in JS; CSRF double-submit with constant-time compare.
- Frontend exposes no secrets; route guard correctly defers to backend authz.
</content>
</invoke>
