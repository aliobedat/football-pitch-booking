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

## Production messaging posture & external constraints
- Current Production Messaging Posture:
  - Infobip WhatsApp is the active channel.
  - Twilio/SMS is inactive legacy code.
  - SMS fallback is disabled.
  - WhatsApp OTP is disabled until Meta Authentication-template approval.
  - Fake/log-only adapters remain available for local development and tests.
- AUTHENTICATION-category templates (OTP) are restricted to verified / high-tier
  Meta businesses. A new account may NOT get them approved immediately.
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
2026-07-27 — WO-OWNER-SLOTS SHIPPED to main (18093dd), owner phone-QA cleared.
Admin dashboard only; no backend, schema, or migration.

DEFECT B (fixed) — جدول الملعب iterates 30-minute grid cells and collapsed only
runs of `closed` cells, so a 90-minute booking rendered as three rows with the
same customer name. Consecutive cells sharing a booking id now merge into ONE
row labelled from the booking's own start/end. Booking identity was already in
the day-view payload, so this was pure rendering. The merge guard on a real
booking id is LOAD-BEARING: available/closed cells carry no booking, so
`next.booking?.id === slot.booking?.id` matches undefined to undefined and would
collapse the whole free grid, destroying tap-to-book. Merging on id alone is
safe only because the GIST EXCLUDE guarantees ≤1 occupancy row per instant.

Cross-midnight labels are clamped at BOTH edges. The payload returns any booking
OVERLAPPING the day and the labels print wall-clock with no date, so an
unclamped bound silently names another day. Markers: مستمر من ليلة أمس /
يمتد إلى الغد / مستمر طوال اليوم. The partial-cell edge bar was REMOVED (proven
dead: the backend sets `partial` only on an occupied cell, so every partial cell
now carries an exactly-labelled row). Filter chips count rendered rows via the
same function that builds the list; the booked chip is مشغول because it includes
blocks and would otherwise contradict the summary strip's حجوزات.

جدول اليوم gained prev/next day + اليوم reset, URL-synced. GET /schedule already
parsed `date`; the frontend never sent it. Sending it also fixed a latent bug —
the server's no-date default is time.Now() in server-local time while the day
bounds read the value's own location, so 00:00–02:59 Amman resolved to the
PREVIOUS day. Staff may navigate: read-only, SQL scoping is date-independent,
and ?date= was already accepted, so nothing new is reachable.

DEFECT A (NOT fixed, documented) — creating a cross-midnight booking from
جدول الملعب. It was NEVER the reported buildDateTime trap: all three admin forms
already do correct absolute-instant arithmetic. The real cause is a model
mismatch — the admin day is a CIVIL day while the player card became SESSION-day
aware in WO-24H-CONTINUITY. Day View is structurally incapable in two
independent ways (civil-day grid AND the manual sheet's duration clamped to that
grid, so 23:30 is not even offerable). **/calendar (التقويم) is the working path
for owner cross-midnight bookings today** — its window derives from unclipped
open_windows plus a buffer, and its duration list is unclamped. Option (i), a
session-day grid for Day View, is costed in
docs/followups/day-view-session-day-grid.md and deliberately not built.

2026-07-27 — WO-24H-CONTINUITY SHIPPED to main, owner phone-QA cleared.
Commits: 6557bfe (Gate 1A backend gate), 3a960d6 (resolver warning comment),
9ab63fe (Gates 1A-2/1B-2, frontend + lookahead). No schema change.

THE BUG: pitches open 24/7 store the explicit full-day row 00:00→00:00, which
anchors to a window ending exactly at the next local midnight. Monday's window
and Tuesday's abut at that instant, but ResolveWindowsForDate never put both in
one candidate set and SlotContained requires a SINGLE interval to contain a
slot — so 23:00→00:30 was rejected on a pitch that never closes. TWO of the
three production pitches (1 and 2, Super Goal, same venue) have that shape;
pitch 3 is 16:00→01:00. D1's "24-hour venue unrepresentable" limitation from
WO-CROSS-MIDNIGHT is therefore RESOLVED for booking purposes: 00:00→00:00 is
representable and is what these pitches use.

THE FIX, in three parts:
- Containment gate resolves EVERY day a slot touches (ResolveWindowsForRange —
  not a fixed D+1, because owner/manual/academy paths carry no max-duration
  cap) and coalesces abutting/overlapping windows (CoalesceIntervals) before
  testing containment (SlotWithinHours). Merging happens ONLY inside the gate.
- /availability serves two per-date signals beside the still-DAY-shaped
  open_windows: hours_shape (continuous|spill|day_bounded) and
  continuation_minutes (how far past midnight coverage actually runs, capped at
  the player ceiling, measured with the SAME coalescing the gate uses).
- The client extends its duration-containment window ONLY when ALL of:
  configured, continuous, continuation > 0, exactly ONE served window, and that
  window ends at exactly 1440. Anything else gets no extension.

READ PATHS UNCHANGED BY CONSTRUCTION (the load-bearing property): across both
backend commits operating_hours_resolve.go is ADDITIONS ONLY — anchorWindow,
ResolveWindowsForDate and SlotContained are byte-identical, so
SearchAvailability, GetOpenWindows, the owner day view and the calendar cannot
have moved. Do not delete from that file without re-establishing the proof.
day_view_repository.go's per-slot marking is intentionally left unmerged: grid
cells are 30-min slices strictly inside the civil day, so none straddles the
abutment seam and merging there is a provable no-op.

GetBookedSlots now looks ahead dayEnd + 24h + MaxPlayerBookingDuration. The
client attributes starts by the SESSION AXIS, not the civil day: a spill window
ends on the next calendar day, so an 18:00→04:00 pitch offers starts to 03:00
whose collidable bookings sat outside the old one-day horizon.

THREE RULES WERE RETRACTED before the shipped one, each for over-offering.
Recorded because the pattern repeated: (1) extend every window by a flat 120 —
fabricated a morning window on a split shift and offered 11:30→12:30 across a
real 12:00–20:00 closure; (2) "extend the window ending at midnight" — a date
that both spills and abuts has no such window; (3) the hours rule was then
correct but a NEW over-offer appeared in the OCCUPANCY dimension (the
booked-slots horizon above). Lesson worth keeping: an invariant proven over one
dimension does not transfer to another — widening what the UI may offer
requires re-checking EVERY constraint the server applies.

Locked invariant order for this surface: never over-offer > no double-render >
no lost inventory. Where they conflict, lose inventory and document it.
Deliberately under-served shapes, the absent frontend test runner, and the
booking card's known gaps (lead-time staleness chief among them) are recorded
in docs/followups/{hours-shape-contract-limits,
frontend-has-no-test-infrastructure,booking-card-known-gaps}.md.

2026-07-25 — WO-CROSS-MIDNIGHT SHIPPED to main, Gate 2 owner-cleared on a real
phone. Backend 4c73149 (Gate 1A): player-path hardening — 15-min lead-time gate
and 120-min max-duration gate added to CreateBooking; operating-window
containment (SlotContained) and the GIST EXCLUDE constraint were confirmed
already cross-midnight-correct with no change; owner/admin paths (CreateBlock,
CreateManualBooking) intentionally carry NEITHER gate (walk-in-now /
any-length-block authority, proven by an owner-accepted/player-rejected test
pair at the same magnitudes). Frontend 8642fa0 (Gate 1B): handoff redesign of
the booking card — continuous slot timeline grouped by part of day incl. the
after-midnight group ("فجر <weekday>"), unavailable slots hidden never
disabled, sticky summary with the cross-midnight note, D3-a live
previous-session chip ("مستمرة الآن") in the early hours, selectedStart as
absolute session-axis minutes (>1440 valid). IBM Plex Sans Arabic + hex tokens
scoped to the card; layout.tsx untouched. D1 dropped (existing close<=open
TIME convention stands; a 24-hour venue remains unrepresentable under it —
accepted limitation). D2 = calendar-start attribution accepted as-is, zero
stats/daily-view query changes.
STALE-BLOCKER CLEARED: the "migration 035 enum check" pre-merge blocker was
verified moot this session — سداسي is PRESENT in production pg_enum at
sortorder 1.5 (between خماسي and سباعي), exactly matching 035's ADD VALUE
BEFORE placement; 035 was already applied.

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
2026-07-27 — WO-OWNER-SLOTS: the adversarial review earned its cost a THIRD
time, and again on something live testing had already passed.

PASS 2 caught, BEFORE commit, that `SlotRow` returns early for `blocked` rows,
so the newly-added continuation markers — defined only in the `booked` branch —
never rendered on exactly the rows that can span days (owner blocks carry no
max-duration gate). The three-day block that MOTIVATED the change would have
shipped reading a bare "00:00 – 00:00" with no explanation: strictly worse than
the wrong-but-informative label it replaced. Browser QA had already passed,
because every seeded booking was ≤2h and none exercised the blocked branch.

Running tally of what review caught that testing could not:
  1. flat-120 extension fabricating hours across a real midday closure
     (WO-24H-CONTINUITY);
  2. the civil-day booked-slots horizon — a deterministic 409 on the 24/7
     pitches at the exact hour that WO unlocked;
  3. this one — a clamp applied to a branch whose explanation was not.
All three were invisible to live testing because the production-shaped data
never exercised the failing case. RULE, now three-for-three: run BOTH passes
before the commit, and treat "looks good" from a reviewer as a failed review.

Two more from the same review worth keeping: a ruling can be HALF a rule — the
owner's cross-midnight ruling clamped the start and mandated a wrong-day end,
and implementing it literally would have shipped a defect; and a `?? fallback`
can silently reintroduce the very staleness it was added to prevent (the
payload-date fallback), so prefer skipping an enrichment over computing it
against possibly-unrelated inputs.

PORT ZOMBIE, fourth and fifth occurrence (now on :3001, the admin dev port —
the pattern is not port-specific). Unchanged rule: after stopping a Next dev
shell on Windows, always netstat/taskkill before trusting the port is free.

2026-07-27 — WO-24H-CONTINUITY learnings:

- EXIT CODES LIED THREE TIMES IN ONE SESSION. Twice `go test ./... | tail -N`
  reported success because the pipeline's exit status is `tail`'s, not
  `go test`'s — the visible output said FAIL while the harness reported 0.
  Once a run "passed" in seconds having never executed at all: it was launched
  from the repo root and died on `go: cannot find main module`, which `tail`
  duly reported as a clean exit. RULE: never conclude a suite is green from an
  exit code attached to a pipeline. Redirect to a file and capture the real
  status — `go test … > out.txt 2>&1; echo "EXIT=$?" >> out.txt` — then READ
  the file. All three were caught only by reading output; the habit is what
  found them, and it is the same false-green family as the stale-baseline
  incident already logged under Testing & migration rules.
- THE ADVERSARIAL REVIEW SUBAGENT EARNED ITS COST. It caught two wrong
  ARCHITECTURAL rulings that live browser testing could not: the flat-120
  extension (fabricated hours across a real midday closure on a split shift)
  and the civil-day booked-slots horizon (deterministic 409 on the 24/7 pitches
  at the exact hour the WO unlocks). Neither was reachable by testing the
  production schedules, because on a single-window 24/7 pitch the wrong rule
  and the right rule are indistinguishable. Live QA validates the shapes you
  have; adversarial review finds the shapes you don't. Run BOTH passes before
  the commit, not after — and treat "looks good" from a reviewer as a failed
  review.
- MUTATION-TEST THE TESTS. Two new tests passed while the mutations they
  supposedly guarded against also passed: a "seam-only" test whose assertions
  held against a hardcoded return, and a probe-width test whose 48h constant
  was satisfied by the very mutation it targeted. Both were rewritten (72h;
  next-day 00:00→01:00 so the correct answer is 60, not the ceiling) and
  verified by actually applying each mutation and watching the suite go red.
  A test that cannot fail is documentation, not verification.
- PORT-3100 ZOMBIE, a third time — survived the shell stop again during final
  teardown. The existing rule holds and is now three-for-three: after stopping
  a Next dev shell on Windows, always netstat/taskkill before trusting the port
  is free.

2026-07-25 — WO-CROSS-MIDNIGHT learnings (gotchas re-encountered):
- PORT-ZOMBIE, again: a Next dev server survived its shell's termination and
  kept port 3100 with a STALE baked-in NEXT_PUBLIC_API_URL (localhost) — the
  page loads but every API call silently targets the wrong host. Same family
  as the PR #62 port-3000 zombie. It recurred TWICE in one session (including
  after the final TaskStop teardown). Rule: after stopping a Next dev shell on
  Windows, always `netstat -ano | findstr :<port>` and `taskkill /F` the
  survivor before starting a replacement — never trust the shell stop alone.
- buildDateTime SAME-DAY TRAP: removing the frontend endMs>1440 duration clamp
  alone was insufficient for cross-midnight — buildDateTime(dateStr,"HH:MM")
  always constructed the end on the START day, so 23:00+1.5h produced 00:30 on
  the WRONG date. Fix: model times as absolute minutes from the session day's
  midnight and construct via Date minute-overflow normalisation
  (new Date(y,m,d,0,1470) → next day 00:30), which also survives month/year
  boundaries. Any future clamp removal must trace every downstream consumer of
  the clamped value, not just delete the guard.

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

## Tiering — mandatory first line of every WO
TIER: T1 | T2 | T3
T1 — no booking_range / pricing / amount_paid / status / permission change.
     No recon. 2 tests. Stop before push only.
T2 — touches permissions, owner-scoped queries, or response payload shape.
     Targeted recon (max 3 questions). 5 tests. Gate 2 required.
T3 — touches booking_range, pricing, conflict resolution, or schema.
     Full recon. Full coverage + concurrency. Gate 2. Migration owner-executed only.

NEVER RELAXED AT ANY TIER:
GIST EXCLUDE sole conflict referee · zero pre-check SELECT on the conflict path ·
404 not 403 across tenants · single atomic UPDATE, never SELECT-then-UPDATE ·
minimum one cross-tenant test even at T1 · PR only · production writes by owner ·
a silently-skipped DB test is a failed test.

## Deletion rule (2026-08-16)
CC never deletes files — tracked or untracked, empty or not, including files CC
itself created. It reports the path and waits. Deletion is owner-executed.
Ledger: 2026-08-16 — CC self-deleted `120` and `backend/,+` without asking.

## Evidence standard (2026-08-16)
When raw bytes are requested, return raw bytes. A re-rendered string is not
evidence about a rendering artifact.
Ledger: 2026-08-16 — CC answered a hex-dump request with rendered text twice,
then drew a conclusion from it.

## Discipline lives in the repo, not in memory (2026-08-16)
Engineering rules go in CLAUDE.md, which is tracked, diffable, and reviewable in a
PR. Memory stores serve the session; the repo serves the project. Writing rules to
memory instead of CLAUDE.md is not a substitute.

## Booking identity model (settled)
Four parallel identity columns, not one:
- guest_name / guest_phone — manual/academy. EDITABLE via PATCH /bookings/:id/contact.
- contact_name / contact_phone — frozen snapshot from users at player-booking
  creation. IMMUTABLE — editing desyncs it from the users table.
- player_id — FK, player bookings only.
- customer_id — CRM FK, populated post-commit, fail-open.

## Series edits — scope by field (2026-08-16)
Whether an edit may apply to a whole recurrence_group_id series depends on the field:
- guest_name / guest_phone — SERIES ALLOWED via `apply_to_series`. These touch no
  range and no pitch, so GIST EXCLUDE cannot fire and a partial-failure state is
  impossible. Single atomic UPDATE across the group, no loop, no transaction.
- booking_range / pitch_id — SINGLE OCCURRENCE ONLY. Series rescheduling is deferred
  to a separate T3, because each row clears GIST EXCLUDE independently and a partial
  move would leave a series split across two times or pitches. The all-or-nothing
  vs best-effort decision is unmade.
This supersedes the original contact-edit G1 ("edits ONE booking row"), which
predates the field-level distinction.
