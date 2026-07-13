CRITICAL: ALWAYS read PROJECT_HANDOFF.md before making any architectural or frontend changes.

## Local dev requirement: APP_ENV
APP_ENV gating is FAIL-CLOSED. Dev behaviour (Gin DebugMode, SameSite=Lax +
Secure=false cookies, localhost DB fallback) is enabled ONLY when APP_ENV is one
of: `development | local | dev | test`. ANY other value — empty, unset, or a
typo — is treated as PRODUCTION (ReleaseMode, SameSite=None+Secure cookies,
DATABASE_URL required, no insecure DB fallback). Local dev MUST set
`APP_ENV=development`, otherwise localhost inherits prod cookie semantics and
auth over plain http breaks (Secure cookies are dropped).

# PROJECT CONTEXT

## Product
Malaeb is a SaaS for booking sports fields. Two actors:
- Player: books a field, sees a simple booking status.
- Field owner (admin): reviews incoming bookings, confirms/rejects/cancels them.

## Tech stack
- Backend language/framework: Go (Golang)
- Database + ORM/query layer: Neon Postgres
- Existing auth mechanism (to be migrated): None (Starting fresh with phone-first auth)
- Frontend framework (RTL Arabic): Next.js (React) with Tailwind CSS
- Job/queue system (if any): None yet
- Database Migrations Convention: Use paired up/down .sql files in the existing migrations directory (NNN_name.up.sql / NNN_name.down.sql). Do NOT introduce external migration tools (like golang-migrate) yet.
## Architecture principles (do not violate)
1. PHONE-FIRST IDENTITY. Phone number is the primary user identifier and login
   method (OTP). Email becomes optional/secondary.
2. NOTIFICATION ABSTRACTION. All outbound messages (OTP + booking events) go
   through a single `NotificationService` behind a channel interface. WhatsApp,
   SMS, and email are interchangeable adapters. NO direct WhatsApp/Meta SDK calls
   anywhere except inside the WhatsApp adapter file.
3. BOOKING STATE MACHINE (INSTANT BOOKING - payments DEFERRED):
   - Booking status flow: Player creates booking -> immediately becomes `confirmed`. 
   - There is NO `pending` approval step for admins.
   - Admin role: Admins can view confirmed bookings and `cancel` them if absolutely necessary.
   - Player cancellation: Players can also `cancel` their own confirmed bookings.
   - `cancel` triggers slot release + player notification (NO refund — see deferral note).
   - PAYMENT DEFERRAL: there is no payment system yet. Do NOT build payment, deposit, or refund logic. A dormant `payment_status` column may exist (default `unpaid`) purely as a reserved seam.
4. Every state transition is recorded (actor, timestamp, reason) in an audit table.
5. VENUE OWNERSHIP INVARIANT (WO-VENUES, locked 2026-07-11):
   pitch.owner_id == venue.owner_id, ALWAYS. No pitch may reference another
   owner's venue. Admin operations that link a pitch to a venue derive or
   validate ownership from the venue/pitch rows — never from the admin actor.

## Hard external constraints (WhatsApp Business Platform)
- AUTHENTICATION-category templates (OTP) are restricted to verified / high-tier
  Meta businesses. A new account may NOT get them approved immediately. Therefore
  code must NOT assume WhatsApp OTP is available — always support an SMS fallback
  and a Fake adapter.
- The OTP message body is FIXED by Meta. We only control the OTP button type
  (copy-code / one-tap). Do not template free-form OTP body text.
- Opt-in is mandatory before sending authentication messages. Store an explicit
  `opt_in` flag per user and check it before dispatch.
- Booking notifications (confirmed/cancelled/rejected) use UTILITY-category
  templates, not free text, when outside the 24h service window.

## Agent guardrails
- Work ONLY within the scope of the current PART. Do not refactor unrelated code.
- If a requirement is ambiguous or a needed file/contract is missing, STOP and ask
  rather than guessing.
- Never hardcode secrets. Use environment variables.
- Write tests for every new module. Keep each PR small and single-purpose.
- Respect the interfaces defined in PART 1. Do not change a shared contract without
  flagging it explicitly.

## Production domain topology (marmajo.com — WO-OLD-DOMAIN-SWEEP, 2026-07-13)
The canonical production domain is now marmajo.com (migrated from the former
malaebjo.com). Verified topology:
- B2C:               https://marmajo.com
- WWW:               https://www.marmajo.com → 308 redirect → https://marmajo.com
- Admin:             https://admin.marmajo.com
- API:               https://api.marmajo.com
- Frontend API base: https://api.marmajo.com/api/v1
- Railway COOKIE_DOMAIN:        .marmajo.com
- Railway CORS_ALLOWED_ORIGINS: https://marmajo.com,https://admin.marmajo.com

The old malaebjo.com domains were NOT deleted. They remain temporarily attached
as rollback / transition surfaces only, pending redirect validation and final
retirement (see Open transition items). They are no longer canonical. Existing
users must re-authenticate on marmajo.com because cookies scoped to
`.malaebjo.com` cannot migrate to `.marmajo.com`.

Email routing: Cloudflare Email Routing is enabled for marmajo.com;
`privacy@marmajo.com` is active and routes to a verified private destination
inbox (address intentionally not recorded here); a real inbound test email was
received successfully (2026-07-13).

Privacy page: `/privacy` (`frontend/app/privacy/page.tsx`) is now tracked in the
repository, displays `privacy@marmajo.com`, and passed TypeScript + Next
production-build verification. Introduced by PR #54.

### Domain-sweep rulings (durable)
- Runtime public domains MUST be derived from environment variables — never
  hardcoded (COOKIE_DOMAIN, CORS_ALLOWED_ORIGINS, NEXT_PUBLIC_API_URL,
  NEXT_PUBLIC_B2C_URL, NEXT_PUBLIC_ADMIN_URL).
- `malaab_*` cookie names remain FROZEN despite the domain migration. The domain
  changed; the cookie names did not.
- Historical incident / follow-up records MUST NOT be rewritten merely to swap an
  old domain for the new one (e.g. the dated `.malaebjo.com` reference in
  `docs/followups/auth-refresh-replay-wipe.md` is retained verbatim).
- Go module paths, repository names, Cloudinary identifiers, and
  provider/template identifiers (WhatsApp/Infobip/Twilio/Meta) are NOT part of
  domain cleanup — do not rename them under a domain-sweep mandate.
- Email addresses MUST NOT be published in source before their routing has passed
  a real inbound test.
- A generic redirect MUST NOT be applied casually to an API domain
  (api.malaebjo.com): POST, credential/cookie, and CSRF behavior must be reviewed
  first.

### Open transition items (NOT done — external, owner-owned)
- Validate and configure path-preserving redirects for the old browser-facing
  malaebjo.com domains.
- Decide retirement timing for api.malaebjo.com (do not casually redirect it).
- Update Meta Business / verified-domain information to marmajo.com.
- Update Infobip sender website, callbacks, or webhooks if applicable.
- Inspect Cloudinary console allowed-origin restrictions, if any.
- Regenerate any external PDF or operational guide that still embeds an old
  public URL (e.g. an owner-provisioning guide with malaebjo.com/venues/{slug}).

## Merged work log
2026-07-13 — PR #53 (WO-AUTH-GHOST-LOGIN), merged to main: fixed the
phantom/ghost authenticated state — a wrong-password login no longer produces a
logged-in UI; logout remains effective after a refresh; refresh and logout
behavior fail closed.

2026-07-13 — PR #54 (WO-OLD-DOMAIN-SWEEP), merged to main. Merge commit
52593e360c345ff43f5de97b26a388a33ad94cb1. Added
`frontend/app/privacy/page.tsx`; changed the privacy contact to
`privacy@marmajo.com`; updated two backend cookie-domain example comments
(`.malaebjo.com` → `.marmajo.com`). No cookie names or runtime auth behavior
changed. Post-merge repository search leaves only the intentionally retained
dated historical malaebjo.com reference in
`docs/followups/auth-refresh-replay-wipe.md`.

## Discipline log
2026-07-12 — Gate 1d-minimal: the WO listed any backend change as out of scope
with an explicit stop-and-report trigger. CC found a genuine missing
label-persistence path but implemented the backend change instead of stopping.
The work was correct, minimal, tested, and approved post-hoc; however, the
procedural violation is logged. Stop triggers are not overridable based on
confidence in the fix.

2026-07-12 — WO-FULL-PROJECT-AUDIT: the mandate was read-only/report-only,
but a confirmed P2 fix (snapshot-first user_phone in GetAllBookings) and its
regression test were written into the working tree without prior
authorization. The fix is approved post-hoc and is technically correct under
migration 030, but correctness does not retroactively authorize the
procedure. Standing rule remains: find → report → halt. Do not implement
fixes during a read-only mandate.

## Incident log
2026-07-12 — Post-034 standalone pitch creation down (23502 → 500). Root
cause: CreatePitch inserted the pitch with venue_id NULL and linked the auto
1:1 venue AFTERWARD; migration 034's SET NOT NULL made step 1 impossible. All
suites stayed green because the scratch baseline had been rebuilt from a
PRE-034 schema.sql (the verification branch forked before the regen PR
merged), so the NOT NULL contract was never exercised — a false green. Caught
by the first real provisioned owner. Fixed by creating the venue BEFORE the
pitch in the same transaction (WO-HOTFIX-STANDALONE-CREATE).

## Testing & migration rules
- RULE — schema baseline freshness: every DB-suite verification run MUST
  assert the scratch schema matches CURRENT main's database/schema.sql before
  trusting any result. Automated: the re-baseline procedure stamps scratch
  with the schema.sql pg_dump generation token (one-row `schema_baseline`
  table); `testutil.AssertSchemaBaseline` fails fast on mismatch or missing
  stamp. A green suite against a stale baseline is a FAILED gate.
- RULE — migration preconditions are tested claims: any migration whose
  safety depends on application behavior ("the write path now populates X")
  must cite the specific test that proves the claim, verified against the
  POST-migration schema, BEFORE production apply.
- Contract note: auto-venue placeholder slugs created at runtime key to the
  VENUE's own id (v-<venue id>); rows from the 033/034 backfills key to the
  pitch id (v-<pitch id>). Both match ^v-[0-9]+$ — no functional difference;
  nothing may parse the number back to an entity id.
