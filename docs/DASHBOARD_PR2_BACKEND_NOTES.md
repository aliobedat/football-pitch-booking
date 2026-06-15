# Dashboard PR 2 — Required backend changes (documented in PR 1, NOT yet applied)

PR 1 is frontend-only: it scaffolds the admin dashboard, the shared package, the
auth client, and the role-aware shell + middleware. **No backend code changed.**
For the admin origin to authenticate against the same backend, PR 2 must apply:

## 1. CORS allowlist — add the admin origin
Add the admin-dashboard origin(s) to the CORS allowed-origins list and keep
`Access-Control-Allow-Credentials: true` (cookies must ride cross-origin):
- dev: `http://localhost:3001`
- prod: `https://admin.<domain>`

## 2. Session cookie Domain — share across both origins
The httpOnly session cookies (`malaab_access`, `malaab_refresh`) and the readable
`malaab_csrf` cookie are currently scoped to the player origin. To be sent from
the admin origin, set their `Domain` to a registrable parent both share, e.g.
`Domain=.<domain>` (player = `app.<domain>`, admin = `admin.<domain>`).
- Local dev across `localhost:3000`/`:3001` already shares host `localhost`, so
  no Domain change is needed locally.
- Keep `SameSite=None; Secure` in production (cross-site requests over HTTPS).

## 3. CSRF allowlist
The CSRF double-submit check must accept requests originating from the admin
origin (the `X-CSRF-Token` header echo already works; ensure any Origin/Referer
allowlist in `RequireCSRF` includes the admin origin from #1).

## 4. JWT role issuance + DB-resolved scope (the RBAC core of PR 2)
- Issue `role` in the access-token claims (`{ sub, role, exp }` only — **no
  scope claim**). The frontend reads `role` for UX; scope stays server-side.
- Resolve scope from the DB on every request (staff↔pitch binding table).
- Enforce: staff are 403'd at the analytics/finance endpoints regardless of the
  UI. The middleware/route-guard added in PR 1 are UX only — the Go backend is
  the boundary.

## 5. Staff table + pitch binding
- New `staff` records and a staff→pitch binding (which pitches a staff member may
  operate). Drives the DB-resolved scope guard above.

## 6. Player-app cleanup
- Strip any now-dead admin-only code paths from the player (B2C) app once the
  dashboard owns them.
