import { NextRequest, NextResponse } from 'next/server';

// Protect all routes under /dashboard — only 'owner' role may access them.
// The malaab_role cookie is set on login (AuthContext) and cleared on logout.
// This is a UI-layer guard; the backend enforces the real check via JWT role.
export function middleware(request: NextRequest) {
  const role = request.cookies.get('malaab_role')?.value;

  if (role !== 'owner') {
    const destination = role
      ? '/pitches'   // logged-in player → send to pitch list
      : '/login';    // unauthenticated → send to login
    return NextResponse.redirect(new URL(destination, request.url));
  }

  return NextResponse.next();
}

export const config = {
  matcher: ['/dashboard/:path*'],
};
