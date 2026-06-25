import { existsSync, readFileSync, rmSync } from "node:fs";
import { execFileSync } from "node:child_process";
import http from "node:http";
import path from "node:path";

const root = process.cwd();
const dataDir = path.resolve(readArg("--data-dir") || process.env.CLIFF_DATA_DIR || path.join(root, ".cliff"));
const pidPath = path.join(dataDir, "cliff.pid");
const statePath = path.join(dataDir, "cliff.json");
const help = process.argv.includes("--help") || process.argv.includes("-h");

if (help) {
  console.log(`Usage: npm run daemon:status -- [options]

Reports the Cliff daemon recorded in cliff.pid/cliff.json.

Options:
  --data-dir <path>   Daemon data directory. Defaults to .cliff.
  --port <port>       Fallback port when no state file exists. Defaults to 8080.
  -h, --help          Show this help.
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

function isProcessAlive(pid) {
  if (!pid || !Number.isInteger(pid)) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function processCommandLine(pid) {
  try {
    if (process.platform === "win32") {
      return execFileSync(
        "powershell.exe",
        ["-NoProfile", "-Command", `(Get-CimInstance Win32_Process -Filter "ProcessId=${pid}").CommandLine`],
        { encoding: "utf8", windowsHide: true },
      ).trim();
    }
    return execFileSync("ps", ["-p", String(pid), "-o", "command="], { encoding: "utf8" }).trim();
  } catch {
    return "";
  }
}

function processWorkingSetBytes(pid) {
  try {
    if (process.platform === "win32") {
      const output = execFileSync(
        "powershell.exe",
        ["-NoProfile", "-Command", `(Get-Process -Id ${pid}).WorkingSet64`],
        { encoding: "utf8", windowsHide: true },
      ).trim();
      return Number(output);
    }
    const output = execFileSync("ps", ["-o", "rss=", "-p", String(pid)], { encoding: "utf8" }).trim();
    return Number(output) * 1024;
  } catch {
    return NaN;
  }
}

function processLooksLikeDaemon(pid) {
  const commandLine = processCommandLine(pid).toLowerCase();
  return commandLine.includes("cliff") || commandLine.includes("cliff") || commandLine.includes("cliff");
}

function readJSON(filePath) {
  try {
    return JSON.parse(readFileSync(filePath, "utf8"));
  } catch {
    return null;
  }
}

function readPid() {
  if (!existsSync(pidPath)) return 0;
  return Number(readFileSync(pidPath, "utf8").trim());
}

function fetchHealth(port) {
  return new Promise((resolve) => {
    const request = http.get(`http://127.0.0.1:${port}/api/health`, (response) => {
      let body = "";
      response.setEncoding("utf8");
      response.on("data", (chunk) => {
        body += chunk;
      });
      response.on("end", () => {
        try {
          resolve({ ok: response.statusCode === 200, status: response.statusCode, body: JSON.parse(body) });
        } catch {
          resolve({ ok: false, status: response.statusCode, body: null });
        }
      });
    });
    request.on("error", () => resolve({ ok: false, status: 0, body: null }));
    request.setTimeout(1500, () => {
      request.destroy();
      resolve({ ok: false, status: 0, body: null });
    });
  });
}

const state = readJSON(statePath) ?? {};
const pid = readPid() || Number(state.pid) || 0;

if (!pid) {
  console.log("Cliff daemon is not running.");
  process.exit(0);
}

if (!isProcessAlive(pid)) {
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  console.log(`Cliff daemon PID ${pid} is not running. Removed stale state.`);
  process.exit(0);
}

if (!processLooksLikeDaemon(pid)) {
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  console.log(`PID ${pid} does not look like a Cliff daemon. Removed stale state.`);
  process.exit(0);
}

const port = Number(readArg("--port") || state.port || process.env.CLIFF_PORT || process.env.PORT || 8080);
const health = await fetchHealth(port);
const healthPid = Number(health.body?.self?.pid);
const displayPid = health.ok && Number.isInteger(healthPid) && healthPid > 0 ? healthPid : pid;
const localUrl = health.body?.localUrl || state.localUrl || `http://localhost:${port}`;
const lanUrls = health.body?.lanUrls || state.lanUrls || [];

console.log(health.ok ? "Cliff daemon is running." : "Cliff daemon process is running, but HTTP is not responding.");
console.log(`PID: ${displayPid}`);
if (displayPid !== pid) console.log(`State PID: ${pid}`);
console.log(`Local: ${localUrl}`);
for (const url of lanUrls) console.log(`Same network: ${url}`);
if (lanUrls.length === 0) console.log("Same network: no LAN IPv4 address detected");
if (state.dataDir || dataDir) console.log(`Data: ${state.dataDir || dataDir}`);
if (state.daemonCommand) console.log(`Command: ${state.daemonCommand}`);
if (state.logPath) console.log(`Logs: ${state.logPath}`);
if (state.errorLogPath) console.log(`Errors: ${state.errorLogPath}`);
if (health.body?.platform) console.log(`Platform: ${health.body.platform}`);
if (health.body?.build?.version) {
  console.log(`Version: ${health.body.build.version} (${health.body.build.commit ?? "unknown"})`);
}
if (Number.isFinite(health.body?.uptimeSeconds)) {
  console.log(`Uptime: ${formatDuration(health.body.uptimeSeconds)}`);
}
if (health.body?.self) {
  const heapAlloc = formatBytes(health.body.self.heapAllocBytes);
  const heapSys = formatBytes(health.body.self.heapSysBytes);
  const goroutines = health.body.self.goroutines;
  const workingSet = processWorkingSetBytes(displayPid);
  console.log(`Daemon heap: ${heapAlloc} allocated / ${heapSys} reserved`);
  if (Number.isFinite(workingSet)) console.log(`Daemon memory: ${formatBytes(workingSet)} working set`);
  if (Number.isFinite(goroutines)) console.log(`Daemon goroutines: ${goroutines}`);
}

process.exit(health.ok ? 0 : 1);

function formatBytes(value) {
  if (!Number.isFinite(value)) return "unknown";
  const units = ["B", "KB", "MB", "GB"];
  let size = Math.max(0, value);
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  const decimals = unit === 0 || size >= 10 ? 0 : 1;
  return `${size.toFixed(decimals)} ${units[unit]}`;
}

function formatDuration(seconds) {
  const value = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(value / 3600);
  const minutes = Math.floor((value % 3600) / 60);
  const remainingSeconds = value % 60;
  if (hours > 0) return `${hours}h ${minutes}m ${remainingSeconds}s`;
  if (minutes > 0) return `${minutes}m ${remainingSeconds}s`;
  return `${remainingSeconds}s`;
}
