import type { NextConfig } from "next";

// API origin used by connect-src. Derived from the build-time NEXT_PUBLIC_API_URL
// so the CSP automatically tracks whatever backend the deploy points at (e.g. the
// Railway prod URL), falling back to the local dev backend.
const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080/api/v1';
let apiOrigin = 'http://localhost:8080';
try {
  apiOrigin = new URL(API_URL).origin;
} catch {
  // Malformed env — keep the localhost default rather than emit a broken header.
}

// Content-Security-Policy shipped in REPORT-ONLY mode first (see headers()): it
// observes violations without blocking, so a too-tight policy cannot break the
// RTL app on launch. The owner MUST review violation reports and add any missing
// origins (notably Google Maps: https://maps.googleapis.com / maps.gstatic.com,
// used by the pitch map) BEFORE promoting this to the enforcing
// `Content-Security-Policy` header.
const cspReportOnly = [
  "default-src 'self'",
  // api.cloudinary.com is the signed direct-upload endpoint (PitchImageDropzone
  // POSTs bytes there); res.cloudinary.com serves the delivered images.
  `connect-src 'self' ${apiOrigin} https://api.cloudinary.com`,
  // blob: covers the in-flight local object-URL preview shown while uploading.
  "img-src 'self' https://res.cloudinary.com data: blob:",
  "style-src 'self' 'unsafe-inline'",
  "script-src 'self'",
].join('; ');

const securityHeaders = [
  // 2 years; preload OMITTED — do not add until the owner commits to the HSTS
  // preload list (it is hard to reverse).
  { key: 'Strict-Transport-Security', value: 'max-age=63072000; includeSubDomains' },
  { key: 'X-Content-Type-Options', value: 'nosniff' },
  { key: 'X-Frame-Options', value: 'DENY' },
  { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
  // Minimal deny set. geolocation=(self) is allowed because the listing page uses
  // navigator.geolocation (useUserLocation) to sort pitches by distance.
  {
    key: 'Permissions-Policy',
    value: 'geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()',
  },
  { key: 'Content-Security-Policy-Report-Only', value: cspReportOnly },
];

const nextConfig: NextConfig = {
  // @malaab/shared ships TS source consumed directly from the workspace.
  transpilePackages: ['@malaab/shared'],
  // Permit cross-origin dev requests from a phone on the same LAN. Next.js
  // otherwise blocks the device's LAN-IP origin against the dev server.
  allowedDevOrigins: ['192.168.100.46:3000', '192.168.100.46'],
  // No standalone player login UI for launch: booking is JIT/OTP-free. Permanently
  // redirect the retired /login and /register pages to home so stale
  // bookmarks/links land on '/' instead of a 404. Both point straight at '/' to
  // avoid a /register -> /login -> / chain.
  async redirects() {
    return [
      { source: '/login',    destination: '/', permanent: true },
      { source: '/register', destination: '/', permanent: true },
    ];
  },
  async headers() {
    return [
      { source: '/:path*', headers: securityHeaders },
    ];
  },
  images: {
    remotePatterns: [
      // Local development API server
      { protocol: 'http',  hostname: 'localhost',          port: '8080' },
      // Production storage — swap in your actual CDN / bucket hostname below
      { protocol: 'https', hostname: 'res.cloudinary.com'               },
      { protocol: 'https', hostname: '*.supabase.co'                    },
      { protocol: 'https', hostname: 'images.unsplash.com'              },
    ],
  },
};

export default nextConfig;
