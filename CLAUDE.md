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
3. BOOKING STATE MACHINE (payments DEFERRED — no payment system yet):
   - Booking status: pending -> confirmed | rejected; confirmed -> completed | cancelled | no_show
   - `reject` acts on a PENDING booking. `cancel` acts on a CONFIRMED booking and
     triggers slot release + player notification (NO refund — see deferral note).
   - PAYMENT DEFERRAL: there is no payment system yet. Do NOT build payment, deposit,
     or refund logic. A dormant `payment_status` column may exist (default `unpaid`)
     purely as a reserved seam, but nothing reads or acts on it. Payment is an
     orthogonal dimension to be added later without touching booking logic.
4. Every state transition is recorded (actor, timestamp, reason) in an audit table.

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
