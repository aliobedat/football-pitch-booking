# Follow-up: outbox worker can double-send on a lost ack (at-least-once)

**Severity:** P3 (rare duplicate WhatsApp message; never a lost message, never a
security issue). Accepted residual risk of the at-least-once outbox.
**Found:** WO-WA-BOOKING-CONFIRMATION-AR, blocking recon Q2 (2026-07-13).

## Finding

The notification outbox is at-least-once by construction
(`backend/internal/notification/outbox/worker.go:157-214`):

1. `ClaimDue` flips the job to `processing` and increments `attempts`
   (`outbox/postgres.go:55-69`).
2. `sender.Send(...)` makes the Infobip HTTP call (10 s timeout —
   `notification/infobip.go` `defaultInfobipTimeout`).
3. `MarkSucceeded` + `recordSent` run ONLY on a returned success
   (`worker.go:170-177`); any error/timeout reschedules for a backoff retry
   (`worker.go:201-211`).

**Lost-ack scenario:** Infobip actually accepts and delivers the message, but the
HTTP response is lost (client 10 s timeout, or a transport error). `Send` returns
`DeliveryFailed`, the worker reschedules, and the next attempt **sends the same
job again → the player receives a duplicate WhatsApp confirmation.**

`message_deliveries.provider_message_id` (UNIQUE, `schema.sql:928`) cannot help:
it is written only *after* a successful send (`postgres.go:152/183`), so on a lost
ack there is no provider id to dedupe against, and each accepted Infobip send
returns a *new* messageId anyway. The unique constraint prevents recording the
same id twice — not sending twice.

Dropping `booking_notifications` (the enqueue-time claim) does NOT change this:
that guard is per-enqueue, not per-send. Once the single job exists, the worker
can still retry-and-double-send it. The owner accepted this by keeping the outbox
(ruling R2': at-least-once, retry/backoff/dead-letter retained).

## Impact

At most one duplicate confirmation per booking, only when a send succeeds but its
ack is lost within the retry window. No message is ever lost by this path; the
booking itself is unaffected. Owner priority is "never duplicate," so this is a
known, accepted gap rather than a silently tolerated one.

## Candidate fixes for later

- **Infobip client-supplied dedupe key — NOT available.** Checked the Infobip docs
  (send-WhatsApp-template API, integration best-practices, client guides,
  2026-07-13): Infobip exposes a client `messageId` for delivery-report
  *correlation/tracking* only, with **no documented idempotency/deduplication
  guarantee**, and no idempotency key/header anywhere in the REST API (their only
  retry guidance is exponential backoff for HTTP 429). So the cheap "pass a dedupe
  key on the request" fix that would have closed Q2 does not exist today. If
  Infobip later adds one, revisit — it would close this at the provider.
- **Confirm-before-retry:** before re-sending, query Infobip's outbound message
  logs by the client `messageId` we set, and skip the resend if it shows delivered.
  Adds an API round-trip and depends on log availability/latency.
- **Shorten the ambiguity window:** treat a timeout distinctly from a 5xx and
  reduce (not eliminate) resend likelihood. Does not fully close the gap.

## Status

Open — accepted residual risk. No change made under WO-WA-BOOKING-CONFIRMATION-AR
(ruling R2' keeps at-least-once). Revisit if Infobip ships an idempotency/dedupe
key or if duplicate-confirmation reports appear in practice.
