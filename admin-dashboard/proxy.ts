import { NextResponse, type NextRequest } from 'next/server';
import { decodeClaims, isDashboardRole, type Role } from '@malaab/shared/auth';
import { isFinanceRoute } from '@/lib/nav';

// ── UX-only edge guard (Next "proxy" middleware convention) ───────────────────
// This proxy shapes navigation; it is NOT the security boundary. It reads
// { sub, role, exp } from the httpOnly access cookie WITHOUT verifying the
// signature (the Go backend verifies and authorizes every API call). Its jobs:
//   1. admin/owner/staff/super_admin  → allowed into the dashboard
//   2. player                         → bounced to the B2C app
//   3. staff deep-linking finance/analytics → redirected cleanly (not 403'd page)
//
// scope is intentionally absent from the token — it is DB-resolved server-side.

const ACCESS_COOKIE = process.env.NEXT_PUBLIC_ACCESS_COOKIE || 'malaab_access';
const B2C_URL = process.env.NEXT_PUBLIC_B2C_URL || 'http://localhost:3000';

// Paths that never require a session.
const PUBLIC_PATHS = ['/login'];

export function proxy(req: NextRequest) {
  const { pathname } = req.nextUrl;

  if (PUBLIC_PATHS.some((p) => pathname === p || pathname.startsWith(`${p}/`))) {
    return NextResponse.next();
  }

  const token = req.cookies.get(ACCESS_COOKIE)?.value;
  const claims = decodeClaims(token);
  const role: Role | null = claims?.role ?? null;

  // No (readable) session → send to login. The httpOnly cookie may still exist
  // for a valid session if the cookie name differs across origins; the page's
  // /auth/me probe then rehydrates. Login is the safe default landing.
  if (!role) {
    const url = req.nextUrl.clone();
    url.pathname = '/login';
    return NextResponse.redirect(url);
  }

  // Players don't belong in the dashboard — bounce to the B2C app.
  if (!isDashboardRole(role)) {
    return NextResponse.redirect(new URL('/pitches', B2C_URL));
  }

  // Staff deep-linking the finance/analytics route: redirect cleanly to the
  // overview rather than render a page the backend will 403.
  if (isFinanceRoute(pathname) && role === 'staff') {
    const url = req.nextUrl.clone();
    url.pathname = '/';
    return NextResponse.redirect(url);
  }

  return NextResponse.next();
}

export const config = {
  // Run on everything except Next internals and static assets.
  matcher: ['/((?!_next/static|_next/image|favicon.ico|.*\\..*).*)'],
};
