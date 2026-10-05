/**
 * Run `build` or `dev` with `SKIP_ENV_VALIDATION` to skip env validation. This is especially useful
 * for Docker builds.
 */
import "./src/env.js";

/** @type {import("next").NextConfig} */
const config = {
  transpilePackages: ["@vectra/contracts", "@vectra/db"],
  serverExternalPackages: ["web-push"],
  // Lint is run separately (`pnpm lint`) so build doesn't have to gate on
  // legacy `unsafe-any` warnings in monoliths that the rewrite is distilling.
  eslint: { ignoreDuringBuilds: true },
  // No image optimizer: the panel has no use for /_next/image, and an
  // optimizer that fetches and resizes on request is attack surface.
  images: { unoptimized: true },
  // Caddy compresses every response (`encode zstd gzip` in both site blocks
  // of the Caddyfile), so Node does not spend the event loop on gzip as well.
  // Clients that send no Accept-Encoding still get identity, as before; Go's
  // http client asks for gzip on its own and gets it from Caddy.
  compress: false,
};

export default config;
