import { NextResponse, type NextRequest } from 'next/server';

// B2C edge proxy — intentionally INERT (PR B scrub).
//
// The B2C app is player-facing only and browsing is ROLE-AGNOSTIC: there is no
// owner/admin routing and no protected page tier here. The single auth boundary
// is the booking MUTATION (POST /bookings), which the Go backend enforces on the
// API — not the edge. So this middleware does nothing but pass requests through.
//
// (The file is kept rather than removed so the change is a clean de-reference;
// owners are pointed to the separate admin app via a passive Navbar link.)
export function proxy(_request: NextRequest) {
  return NextResponse.next();
}

export const config = {
  // Match nothing real — the proxy is a no-op. Kept present, exercises no routes.
  matcher: ['/__b2c_proxy_disabled'],
};
