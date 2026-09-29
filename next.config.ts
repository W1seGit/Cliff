import type { NextConfig } from "next";
import { networkInterfaces } from "os";

/** Auto-detect LAN IPv4 addresses so the dev server accepts HMR/WebSocket
 *  connections from other machines on the same network without manual config.
 *  This is only used by the Next.js dev server — production serves the
 *  static export from the Go daemon on the same origin, so no CORS is needed. */
function detectLanOrigins(): string[] {
  try {
    const nets = networkInterfaces();
    const origins: string[] = [];
    for (const interfaces of Object.values(nets)) {
      if (!interfaces) continue;
      for (const net of interfaces) {
        if (net.family === "IPv4" && !net.internal) {
          origins.push(net.address);
        }
      }
    }
    return origins;
  } catch {
    return [];
  }
}

const allowedDevOrigins = [
  ...detectLanOrigins(),
  ...(process.env.DASHBOARD_ALLOWED_DEV_ORIGINS ?? "")
    .split(",")
    .map((origin) => origin.trim())
    .filter(Boolean),
];

const nextConfig: NextConfig = {
  allowedDevOrigins,
  cacheMaxMemorySize: 10 * 1024 * 1024,
  experimental: {
    cpus: Number(process.env.NEXT_BUILD_WORKERS ?? 4),
  },
  output: process.env.NEXT_DAEMON_STATIC_EXPORT === "1" ? "export" : "standalone",
  images: {
    unoptimized: process.env.NEXT_DAEMON_STATIC_EXPORT === "1",
  },
  outputFileTracingExcludes: {
    "/*": [
      ".git/**/*",
      ".cliff/**/*",
      ".auth-smoke-servers-*",
      ".auth-smoke-servers-*/**/*",
      "servers/**/*",
    ],
  },
};

if (process.env.NEXT_DAEMON_STATIC_EXPORT !== "1") {
  nextConfig.rewrites = async () => [
    { source: "/servers/:id/public-access/setup", destination: "/servers/__server__/public-access/setup" },
    { source: "/servers/:id/mods/:subview", destination: "/servers/__server__/mods/:subview" },
    { source: "/servers/:id/mods", destination: "/servers/__server__/mods" },
    { source: "/servers/:id/:tab", destination: "/servers/__server__/:tab" },
    { source: "/servers/:id", destination: "/servers/__server__" },
  ];
}

export default nextConfig;
