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
};

export default config;
