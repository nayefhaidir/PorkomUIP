import type { NextConfig } from "next";
const goApiUrl = (process.env.GO_API_URL ?? "http://localhost:8080").replace(/\/$/, "");

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  outputFileTracingRoot: process.cwd(),
  async rewrites() {
    return {
      beforeFiles: [{ source: "/api/:path*", destination: `${goApiUrl}/api/:path*` }],
    };
  },
};
export default nextConfig;
