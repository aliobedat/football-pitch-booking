# Follow-up: ClientIP() consumers hardened by the trusted-proxy fix

**Logged by:** PR-1 `fix/trusted-proxy-clientip`, 2026-07-07.

## What changed
`cmd/api/main.go` engine setup now configures gin so `c.ClientIP()` resolves the
real client from Railway's edge-set, un-spoofable `X-Real-IP` header and ignores
a client-forged `X-Forwarded-For` prefix (`TrustedPlatform = "X-Real-IP"` +
`SetTrustedProxies(nil)`). Before this, `gin.New()` trusted all proxies, so
`ClientIP()` returned the client-most XFF entry — attacker-controllable.

## Current `ClientIP()` consumers this hardens
- **OTP per-IP rate-limit buckets** — `internal/handlers/phone_auth.go:158`
  (`otp.WithIP(ctx, c.ClientIP())`), feeding the `ip:`, `ip:m:`, `ip:h:` sliding
  windows in `internal/otp/service.go`. These were previously bypassable by
  rotating a forged `X-Forwarded-For`; they now key on the true client IP. (The
  per-phone and global OTP caps were never IP-dependent and were unaffected.)

## NOT affected (verified)
- **password-login limiter** (`internal/handlers/password_auth.go`) is keyed on
  the **normalized phone**, not IP (`limiter.Allow(phone)` at :92) — it never
  consumed `ClientIP()`, so this fix neither helps nor changes it. Its separate
  in-memory / multi-instance limitation is tracked elsewhere (PR-2 followups).

## Forward note
Future IP-keyed defenses (e.g. the PR-2 booking-session per-IP limiter) may now
assume `ClientIP()` returns a trustworthy client address on Railway. This
assumption rests on Railway's documented guarantee that its edge sets `X-Real-IP`
and clients cannot override it; if the platform/edge changes, re-verify with a
`curl -H "X-Real-IP: 1.1.1.1"` probe against prod (server must observe the real
IP, not the forged one).
