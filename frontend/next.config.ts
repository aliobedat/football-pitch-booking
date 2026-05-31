import type { NextConfig } from "next";

const nextConfig: NextConfig = {
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
