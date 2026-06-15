// Canonical role + token contract shared by the player (B2C) and admin apps.
//
// IMPORTANT (token contract): the JWT payload carries ONLY { sub, role, exp }.
// There is NO `scope` claim in the token — fine-grained scope (which pitches a
// staff member is bound to, which financial sections an owner may see) is
// resolved server-side from the DB on every request. The frontend reads `role`
// for UX routing/visibility only; the Go backend remains the security boundary.

export type Role = 'player' | 'staff' | 'owner' | 'admin' | 'super_admin';

// Roles that belong inside the admin dashboard shell. Players are bounced to B2C.
export const DASHBOARD_ROLES: readonly Role[] = ['staff', 'owner', 'admin', 'super_admin'];

// Roles allowed to see Analytics & Financials (UX gate only — backend 403s staff
// at the finance/analytics endpoints regardless of what the UI renders).
export const FINANCE_ROLES: readonly Role[] = ['owner', 'admin', 'super_admin'];

export function isDashboardRole(role: Role | null | undefined): boolean {
  return !!role && DASHBOARD_ROLES.includes(role);
}

export function canViewFinance(role: Role | null | undefined): boolean {
  return !!role && FINANCE_ROLES.includes(role);
}

// The only claims the frontend ever reads off the access token.
export interface TokenClaims {
  sub: string;
  role: Role;
  exp: number;
}
