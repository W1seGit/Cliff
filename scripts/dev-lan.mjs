import { spawn } from "node:child_process";
import { networkInterfaces } from "node:os";

function npmCommand() {
  return process.platform === "win32" ? "npx.cmd" : "npx";
}

function localIpv4Addresses() {
  return Object.values(networkInterfaces())
    .flatMap((entries) => entries ?? [])
    .filter((entry) => entry.family === "IPv4" && !entry.internal)
    .map((entry) => entry.address);
}

const detectedOrigins = localIpv4Addresses();
const configuredOrigins = process.env.DASHBOARD_ALLOWED_DEV_ORIGINS
  ? process.env.DASHBOARD_ALLOWED_DEV_ORIGINS.split(",").map((origin) => origin.trim()).filter(Boolean)
  : [];
const allowedDevOrigins = [...new Set([...configuredOrigins, ...detectedOrigins])];

if (allowedDevOrigins.length) {
  console.log(`Allowing LAN dev origins: ${allowedDevOrigins.join(", ")}`);
} else {
  console.log("No LAN IPv4 address detected. Starting local dev server only.");
}

const child = spawn(npmCommand(), ["next", "dev", "--hostname", "0.0.0.0", "--port", "3000"], {
  env: {
    ...process.env,
    DASHBOARD_ALLOWED_DEV_ORIGINS: allowedDevOrigins.join(","),
  },
  shell: process.platform === "win32",
  stdio: "inherit",
});

child.on("exit", (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  process.exit(code ?? 0);
});
