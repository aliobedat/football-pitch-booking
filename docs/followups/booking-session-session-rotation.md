# Follow-up: precautionary rotation of privileged refresh sessions

**Logged by:** WO-ENSUREBOOKINGUSER Gate 0/1, 2026-07-07. Ruling deferred to
AFTER the escalation fix merges — flagged here, not folded into the fix.

## Why
Gate 0 found **no affirmative evidence** the booking-session role-escalation was
ever exercised (no bookings under any privileged `user_id`, no anomalous account
state). But the platform **cannot prove a negative**:

- There is **no mint-source audit** — `refresh_tokens` records `user_id` and
  expiry but not the mint path, so booking-session, password-login, and
  verify-otp are indistinguishable at rest.
- Refresh sessions are **role-blind** once stored.

At Gate 0 the live census showed **91 active admin + 2 active staff** refresh
sessions of unknown provenance.

## The option
When the fix ships, optionally **force-rotate (invalidate) all live refresh
sessions held by owner/admin/staff accounts**, forcing a fresh password-login.
This guarantees that any session that *might* have been minted through the old
escalation is dead, at the cost of logging every dashboard user out once.

## Decision
Pending — to be ruled on after the fix merges. If executed, do it as a separate,
audited operation (DELETE FROM refresh_tokens WHERE user_id IN privileged set),
not as part of the code fix. Re-run the Gate 0 census first to get the current
count. See [[booking-session-rate-limit]] for the related endpoint hardening.
