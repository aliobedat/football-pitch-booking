import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  // @malaab/shared ships TS source consumed directly from the workspace.
  transpilePackages: ['@malaab/shared'],
};

export default nextConfig;
