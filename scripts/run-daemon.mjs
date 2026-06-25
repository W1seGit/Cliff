import { spawn } from "node:child_process";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const daemonDir = path.join(root, "daemon");
const dataDir = path.resolve(readArg("--data-dir") || process.env.CLIFF_DATA_DIR || path.join(root, ".cliff"));
const webDir = path.join(daemonDir, "web");
const serverRoot = path.resolve(readArg("--server-root") || process.env.CLIFF_SERVER_ROOT || path.join(root, "servers"));
const host = readArg("--host") || process.env.CLIFF_HOST || "0.0.0.0";
const port = readArg("--port") || process.env.CLIFF_PORT || process.env.PORT || "8080";
const help = process.argv.includes("--help") || process.argv.includes("-h");

if (help) {
  console.log(`Usage: npm run daemon:run -- [options]

Runs the Go daemon from source with the static dashboard in daemon/web.
Run npm run build:daemon-web first when dashboard assets are stale.

Options:
  --host <host>          Host interface. Defaults to 0.0.0.0.
  --port <port>          Dashboard port. Defaults to 8080.
  --data-dir <path>      Daemon data directory. Defaults to .cliff.
  --server-root <path>   Minecraft server storage root. Defaults to servers.
  -h, --help             Show this help.
`);
  process.exit(0);
}

function readArg(name) {
  const arg = process.argv.find((item) => item === name || item.startsWith(`${name}=`));
  if (!arg) return "";
  if (arg.includes("=")) return arg.split("=").slice(1).join("=");
  const index = process.argv.indexOf(arg);
  const value = process.argv[index + 1] ?? "";
  if (!value || value.startsWith("-")) {
    console.error(`Missing value for ${name}`);
    process.exit(1);
  }
  return value;
}

function lanAddresses() {
  return Object.values(os.networkInterfaces())
    .flat()
    .filter((address) => address && address.family === "IPv4" && !address.internal)
    .map((address) => address.address);
}

const args = [
  "run",
  "./cmd/cliff",
  "--host",
  host,
  "--port",
  String(port),
  "--data-dir",
  dataDir,
  "--server-root",
  serverRoot,
  "--web-dir",
  webDir,
];

console.log("Starting Cliff daemon from source");
console.log(`Local: http://localhost:${port}`);
const lanUrls = lanAddresses().map((address) => `http://${address}:${port}`);
for (const url of lanUrls) console.log(`Same network: ${url}`);
if (lanUrls.length === 0) console.log("Same network: no LAN IPv4 address detected");

const child = spawn("go", args, {
  cwd: daemonDir,
  stdio: "inherit",
  shell: process.platform === "win32",
  env: {
    ...process.env,
    CLIFF_DATA_DIR: dataDir,
    CLIFF_WEB_DIR: webDir,
    CLIFF_HOST: host,
    CLIFF_PORT: String(port),
  },
});

child.on("exit", (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  process.exit(code ?? 0);
});

child.on("error", (error) => {
  console.error(`Failed to start Go daemon: ${error.message}`);
  process.exit(1);
});
