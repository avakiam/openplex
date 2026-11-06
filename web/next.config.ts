import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";

// Production: static export embedded into openplexd (same origin as the API).
// Development: `next dev` proxies /api to a locally running openplexd.
export default function config(phase: string): NextConfig {
  if (phase === PHASE_DEVELOPMENT_SERVER) {
    const target = process.env.OPENPLEX_API ?? "http://127.0.0.1:7080";
    return {
      async rewrites() {
        return [{ source: "/api/:path*", destination: `${target}/api/:path*` }];
      },
    };
  }
  return {
    output: "export",
    trailingSlash: true,
    images: { unoptimized: true },
    poweredByHeader: false,
  };
}
