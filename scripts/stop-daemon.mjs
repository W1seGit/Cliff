import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { execFileSync } from "node:child_process";
import path from "node:path";

const root = process.cwd();
const dataDir = path.resolve(readArg("--data-dir") || process.env.CLIFF_DATA_DIR || path.join(root, ".cliff"));
const pidPath = path.join(dataDir, "cliff.pid");
const statePath = path.join(dataDir, "cliff.json");
const force = process.argv.includes("--force");

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

function readPid() {
  if (!existsSync(pidPath)) return 0;
  return Number(readFileSync(pidPath, "utf8").trim());
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

function processLooksLikeDaemon(pid) {
  const commandLine = processCommandLine(pid).toLowerCase();
  return commandLine.includes("cliff") || commandLine.includes("cliff") || commandLine.includes("cliff");
}

async function waitForExit(pid) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    if (!isProcessAlive(pid)) return true;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  return !isProcessAlive(pid);
}

mkdirSync(dataDir, { recursive: true });

const pid = readPid();
if (!pid) {
  console.log("Cliff daemon is not running.");
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  process.exit(0);
}

if (!isProcessAlive(pid)) {
  console.log(`Cliff daemon PID ${pid} is not running. Removed stale PID file.`);
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  process.exit(0);
}

if (!processLooksLikeDaemon(pid)) {
  console.log(`PID ${pid} does not look like a Cliff daemon. Removed stale PID file.`);
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  process.exit(0);
}

process.kill(pid, "SIGTERM");
if (await waitForExit(pid)) {
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  console.log(`Stopped Cliff daemon PID ${pid}.`);
  process.exit(0);
}

if (!force) {
  console.error(`Cliff daemon PID ${pid} did not stop within 10 seconds. Re-run with --force to kill it.`);
  process.exit(1);
}

process.kill(pid, "SIGKILL");
if (await waitForExit(pid)) {
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  console.log(`Force-stopped Cliff daemon PID ${pid}.`);
  process.exit(0);
}

console.error(`Cliff daemon PID ${pid} could not be stopped.`);
process.exit(1);
