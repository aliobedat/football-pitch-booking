# Follow-up: refresh-rotation replay clears a sibling tab's fresh session

**Severity:** P3 (intermittent UX; no security impact — fails closed)
**Found:** WO-AUTH-GHOST-LOGIN Phase A (2026-07-12), item A3f. Ruled out of
that WO's scope — rotation semantics untouched; own recon later.

## Finding

Refresh tokens are one-time-use (`FindAndConsumeRefreshToken` rotates on every
`/auth/refresh`). The interceptor's single-flight guard prevents duplicate
refreshes only WITHIN one JS runtime — but the B2C app and the admin dashboard
(and multiple tabs of either) share the same `malaab_refresh` cookie at
`Domain=.malaebjo.com`. Two runtimes can race the same token:

1. Tab A refreshes → token consumed, new pair issued.
2. Tab B (holding the same, now-consumed cookie value in its next request)
   replays it → `ErrRefreshTokenInvalid` → the handler calls
   `clearSessionCookies` — **deleting Tab A's freshly-issued, valid cookies**.

Proven at the HTTP level during the Phase A reproduction (replay → 401 +
`Max-Age=0` on all five cookies).

## Impact

A user with both apps (or two tabs) open across an access-token expiry can be
hard-logged-out everywhere by the race, despite holding a valid session one
request earlier. Fails CLOSED — never a security hole, purely a rough edge.

## Fix shapes to evaluate (later recon)

- Grace window: keep a consumed token verifiable (not usable) for N seconds
  and make replay within the window a no-op 401 WITHOUT clearing cookies.
- Or: only clear cookies when the presented token is unknown, not when it is
  a recently-rotated ancestor of a live token (parent-pointer check).
- Or: broadcast refresh across tabs (BroadcastChannel) client-side.

## Related intent: "sign out everywhere"

Logout revokes only the PRESENTED cookie's token (per-device, ruled in
WO-AUTH-GHOST-LOGIN commit clearance — a phone logout must not sign the owner
out of the laptop dashboard). `RevokeAllUserRefreshTokens` is deliberately
RETAINED, currently uncalled, as the seam for a future explicit
"تسجيل الخروج من كل الأجهزة" action and for account suspension.

---
Status: open — recon required before choosing; rotation semantics are frozen
until then.
