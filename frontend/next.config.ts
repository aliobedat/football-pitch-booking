import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // The legacy email/password /register page was removed in the auth-hardening
  // pass — phone OTP via /login is now the sole auth entry point. Permanently
  // redirect any stale bookmarks/links so they land on login instead of a 404.
  async redirects() {
    return [
      { source: '/register', destination: '/login', permanent: true },
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
