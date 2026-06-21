# Malaeb — Daily Engineering Report

**Date:** June 16, 2026
**Chapter:** Owner-Dashboard Migration — Close-Out

---

## Headline

The owner dashboard is now a fully standalone application, decoupled from the player-facing B2C app at the auth, routing, and deployment-surface level. The legacy owner dashboard embedded in the B2C app — 1,507 lines — has been purged. The B2C app is now 100% player-facing. The full system was verified running locally across three independent services. The migration chapter is closed, pending the pre-launch carry-forward items below.

---

## Architecture: Before → After

**Before** — a single Next.js B2C app served both audiences. Owners authenticated through the B2C login and were redirected into an embedded `/dashboard`. Auth, navigation, and edge routing were entangled across the player and owner surfaces — the root cause of two separate 404 risks caught today.

**After** — three independent surfaces:

| Surface | Responsibility | Local |
|---|---|---|
| Go API backend | Single source of truth — auth, scoping, booking engine | — |
| B2C app | 100% player-facing: browse, availability, reviews, booking | `:3000` |
| Admin Dashboard | Owner/admin operations, its own auth origin + route guard | `:3001` |

---

## What Shipped Today

### Frontend integration & hardening (PR 7 → 8.5)

- **PR 7** — Booking-ops UI ported byte-identical (BlocksModal, manual/walk-in bookings), tenant-isolation boundary confirmed.
- **PR 8** — Cancellation UI wired to `PATCH /bookings/:id/cancel`: explicit confirm step, server-authoritative state, no optimistic mutation. Full parity audit produced as the purge gate — which caught the Operating-Hours gap before any deletion.
- **PR 8.1** — IDOR / cross-tenant integration suite restored to a runnable state; `PROJECT_HANDOFF.md §4` re-synced to the live `tstzrange` schema after doc and test fixtures were found to have drifted.
- **PR 8.5** — Operating-Hours editor ported (a live owner capability the purge would otherwise have stranded — and one that matters acutely in this market for Ramadan/seasonal hours); toggle rollback now surfaces a toast instead of failing silently; HTTP-level cross-tenant boundary added to the verification pass.

### The decoupling trilogy (PR A → B → 9)

- **Standalone Admin Auth (PR A)** — the Admin Dashboard received its own OTP login, an owner/admin role gate (player-only accounts rejected at the door), and its own route guard, on an isolated auth origin. Additive step: nothing legacy was removed.
- **B2C Scrub (PR B)** — every owner surface and `/dashboard` reference stripped from the B2C app (login redirect, nav, edge guards). Owners in B2C are now treated as players with a passive link to the admin app — no cross-app redirect coupling introduced. Legacy dashboard left orphaned and grep-clean.
- **Broad Purge (PR 9)** — the orphaned legacy dashboard and its dead routes deleted in a single isolated, revertable commit, gated on the grep-clean evidence from PR B.

---

## Engineering Decisions That Held the Line

- **Destructive decoupled from constructive** — isolated commits and a human gate before every irreversible step. This failsafe fired twice today and prevented two separate 404 disasters.
- **YAGNI on features, never on invariants** — tenant isolation, fail-closed config, and DB-level constraints were never re-litigated; only redundant re-verification was cut.
- **Owner-treated-as-player over hard redirect** — avoided re-introducing the exact cross-app coupling the migration set out to remove.
- **Origin-isolated auth as defense-in-depth** — a compromise on the public B2C surface can no longer hand over higher-privilege owner/admin sessions.

---

## Verification Status

- Repository-layer IDOR / cross-tenant scoping — green against the live schema.
- Cross-tenant cancellation — 404, zero side effects, zero audit rows.
- Full decoupled system — confirmed running locally across the three services above.

---

## Carry-Forward (to close before launch)

- Confirm the PR 8.1 doc + fixture changes and the PR 8.5 HTTP cross-tenant ping evidence are committed and captured.
- Provision the Admin Dashboard production deployment: a dedicated Vercel origin/subdomain, with that production admin origin added to backend CORS + CSRF allow-lists and the cookie config.
- Settle the production cookie-domain strategy — host-only recommended, to preserve the auth isolation achieved today.
- CSP: move from Report-Only to enforced (pending Google Maps origin validation).
- Standing roadmap: payments integration (architectural seam reserved), STOP-keyword webhook → `opt_out`, and legal-entity registration unlocking Meta Business verification (WhatsApp templates) and A2P SMS sender-ID registration.
