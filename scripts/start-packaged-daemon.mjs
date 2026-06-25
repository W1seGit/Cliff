import { spawn } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const packageDir = path.join(root, "dist", "cliff");
const binaryPath = path.join(packageDir, process.platform === "win32" ? "cliff.exe" : "cliff");
const webDir = path.join(packageDir, "web");
const dataDir = path.resolve(readArg("--data-dir") || process.env.CLIFF_DATA_DIR || path.join(root, ".cliff"));
const pidPath = path.join(dataDir, "cliff.pid");
const statePath = path.join(dataDir, "cliff.json");
const serverRoot = path.resolve(readArg("--server-root") || process.env.CLIFF_SERVER_ROOT || path.join(root, "servers"));
const host = readArg("--host") || process.env.CLIFF_HOST || "0.0.0.0";
const port = readArg("--port") || process.env.CLIFF_PORT || process.env.PORT || "8080";
const help = process.argv.includes("--help") || process.argv.includes("-h");

if (help) {
  console.log(`Usage: npm start -- [options]

Starts the packaged Cliff daemon in the foreground.
Run npm run build first to create dist/cliff.

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

if (!existsSync(binaryPath) || !existsSync(webDir)) {
  console.error("Packaged Cliff daemon was not found.");
  console.error("Run npm run build first, then run npm start.");
  console.error(`Expected binary: ${binaryPath}`);
  console.error(`Expected web assets: ${webDir}`);
  process.exit(1);
}

console.log(`Starting Cliff daemon from ${binaryPath}`);
console.log(`Local: http://localhost:${port}`);
const lanUrls = lanAddresses().map((address) => `http://${address}:${port}`);
for (const url of lanUrls) console.log(`Same network: ${url}`);
if (lanUrls.length === 0) console.log("Same network: no LAN IPv4 address detected");

mkdirSync(dataDir, { recursive: true });

const daemonArgs = ["--host", host, "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot, "--web-dir", webDir];
const child = spawn(
  binaryPath,
  daemonArgs,
  {
    cwd: packageDir,
    stdio: "inherit",
    env: {
      ...process.env,
      CLIFF_DATA_DIR: dataDir,
      CLIFF_WEB_DIR: webDir,
      CLIFF_HOST: host,
      CLIFF_PORT: String(port),
    },
  },
);

writeRunState(child.pid);
updateRunStateWhenReady(child.pid).catch(() => undefined);

let forwardedSignal = false;

function forward(signal) {
  forwardedSignal = true;
  if (!child.killed) child.kill(signal);
}

process.on("SIGINT", () => forward("SIGINT"));
process.on("SIGTERM", () => forward("SIGTERM"));

child.on("exit", (code, signal) => {
  clearRunState(child.pid);
  if (signal && !forwardedSignal) process.kill(process.pid, signal);
  process.exit(code ?? 0);
});

child.on("error", (error) => {
  console.error(`Failed to start packaged daemon: ${error.message}`);
  clearRunState(child.pid);
  process.exit(1);
});

function writeRunState(pid) {
  if (!pid) return;
  writeFileSync(pidPath, String(pid));
  writeFileSync(
    statePath,
    `${JSON.stringify(
      {
        pid,
        ready: false,
        port: Number(port),
        host,
        localUrl: `http://localhost:${port}`,
        lanUrls,
        dataDir,
        serverRoot,
        packageDir,
        webDir,
        daemonBinary: binaryPath,
        daemonArgs,
        daemonCommand: [binaryPath, ...daemonArgs].join(" "),
        logPath: "",
        errorLogPath: "",
        health: null,
        updatedAt: new Date().toISOString(),
      },
      null,
      2,
    )}\n`,
  );
}

async function updateRunStateWhenReady(pid) {
  if (!pid) return;
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`http://127.0.0.1:${port}/api/health`, { signal: AbortSignal.timeout(1000) });
      const health = await response.json();
      if (response.ok && health?.ok === true && health?.daemon === "cliff") {
        writeReadyState(pid, health);
        return;
      }
    } catch {
      // Keep polling until the daemon starts answering or the child exits.
    }
    if (child.exitCode !== null) return;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

function writeReadyState(pid, health) {
  if (!pid) return;
  let currentPid = 0;
  try {
    currentPid = Number(readFileSync(pidPath, "utf8").trim());
  } catch {
    return;
  }
  if (currentPid !== pid) return;
  writeFileSync(
    statePath,
    `${JSON.stringify(
      {
        pid,
        ready: true,
        port: Number(port),
        host,
        localUrl: health.localUrl || `http://localhost:${port}`,
        lanUrls: health.lanUrls || lanUrls,
        dataDir,
        serverRoot,
        packageDir,
        webDir,
        daemonBinary: binaryPath,
        daemonArgs,
        daemonCommand: [binaryPath, ...daemonArgs].join(" "),
        logPath: "",
        errorLogPath: "",
        health,
        build: health.build ?? null,
        platform: health.platform ?? null,
        updatedAt: new Date().toISOString(),
      },
      null,
      2,
    )}\n`,
  );
}

function clearRunState(pid) {
  if (!pid) return;
  let currentPid = 0;
  try {
    currentPid = Number(readFileSync(pidPath, "utf8").trim());
  } catch {
    return;
  }
  if (currentPid !== pid) return;
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
}
