# Follow-up: rate-limit POST /auth/booking-session

**Logged by:** WO-ENSUREBOOKINGUSER Gate 1, 2026-07-07. Post-fix ruling — not
part of the escalation fix itself.

## Context
The role-escalation hole is closed: a privileged phone through
`POST /auth/booking-session` now gets a neutral **403
`booking_session_unavailable`** and no session is minted (repository fails
closed, handler asserts player before minting). But the endpoint remains
**unauthenticated and phone-only** by design (MVP no-OTP booking), which leaves
two residual concerns that rate-limiting — not the escalation fix — should
address.

## 1. Residual status-differential oracle
The refusal is deliberately neutral in code and message (does not confirm
privilege). However, a determined attacker can still distinguish outcomes by
**HTTP status**: a privileged phone returns 403, a player/new phone returns 200.
That is a coarse oracle: feed a list of phones, bucket by status, and the 403s
reveal which numbers belong to owner/admin/staff accounts — without ever
obtaining a session. Closing the oracle entirely (e.g. returning 200 with a
dummy session for privileged phones) is undesirable, so the practical mitigation
is **rate-limiting + abuse monitoring** to make phone-enumeration expensive.

## 2. Abuse volume
Unauthenticated user-row creation (new phones become players) and unbounded
session minting are both abusable at volume (row spam, refresh-token churn).

## Proposed
- Per-IP and/or per-phone rate limit on `/auth/booking-session` (align with
  whatever limiter guards `/auth/request-otp`, which has the same exposure
  profile — check if one already exists and extend it rather than add a new
  mechanism).
- Log + alert on elevated 403 rates from a single source (enumeration signal).

## Legitimate-owner UX note
An owner/admin/staff whose personal phone is **also** how they'd book a pitch as
a player now hits the 403 wall on the booking flow. Today that is correct
(fail-closed > convenience, and no owner-as-player flow is shipped). If a real
need appears, the answer is an **authenticated** role-switch / separate booking
identity — never relaxing this unauthenticated endpoint. Capture the decision
here if/when it comes up.
