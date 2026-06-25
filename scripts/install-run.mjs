import { closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { execFileSync, spawn, spawnSync } from "node:child_process";

const root = process.cwd();
const dataDir = path.resolve(readArg("--data-dir") || process.env.CLIFF_DATA_DIR || path.join(root, ".cliff"));
const pidPath = path.join(dataDir, "cliff.pid");
const statePath = path.join(dataDir, "cliff.json");
const logPath = path.join(dataDir, "cliff.log");
const errorLogPath = path.join(dataDir, "cliff-error.log");
const port = Number(readArg("--port") ?? process.env.CLIFF_PORT ?? process.env.PORT ?? 8080);
const packageDir = path.join(root, "dist", "cliff");
const webDir = path.join(packageDir, "web");
const serverRoot = path.resolve(readArg("--server-root") || process.env.CLIFF_SERVER_ROOT || path.join(root, "servers"));
const daemonBinary = path.join(packageDir, process.platform === "win32" ? "cliff.exe" : "cliff");
const daemonArgs = ["--host", "0.0.0.0", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot, "--web-dir", webDir, "--log-file", logPath];
const help = process.argv.includes("--help") || process.argv.includes("-h");
const forceInstall = process.argv.includes("--force-install");
const skipInstall = process.argv.includes("--skip-install");

if (help) {
  console.log(`Usage: npm run install:run -- [options]

Packages the Go daemon with the static dashboard, starts it in the background,
and prints local plus same-network dashboard URLs.

Options:
  --port <port>          Dashboard port. Defaults to 8080.
  --data-dir <path>      Daemon data directory. Defaults to .cliff.
  --server-root <path>   Minecraft server storage root. Defaults to servers.
  --skip-install         Do not run npm install before packaging.
  --force-install        Run npm install even when node_modules already exists.
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

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
}

function run(command, args, options = {}) {
  const executable = commandName(command);
  const result = spawnSync(executable, args, { cwd: options.cwd ?? root, stdio: "inherit", shell: process.platform === "win32" });
  if (result.error) {
    console.error(`Failed to run ${executable}: ${result.error.message}`);
    process.exit(1);
  }
  if (result.status !== 0) process.exit(result.status ?? 1);
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

function processLooksLikeDaemon(pid) {
  const commandLine = processCommandLine(pid).toLowerCase();
  return commandLine.includes("cliff") || commandLine.includes("cliff") || commandLine.includes("cliff");
}

function lanAddresses() {
  return Object.values(os.networkInterfaces())
    .flat()
    .filter((address) => address && address.family === "IPv4" && !address.internal)
    .map((address) => address.address);
}

function waitForHealth() {
  const deadline = Date.now() + 20_000;
  return new Promise((resolve) => {
    const check = () => {
      const request = http.get(`http://127.0.0.1:${port}/api/health`, (response) => {
        let body = "";
        response.setEncoding("utf8");
        response.on("data", (chunk) => {
          body += chunk;
        });
        response.on("end", () => {
          try {
            const json = JSON.parse(body);
            if (response.statusCode === 200 && json?.ok === true && json?.daemon === "cliff") {
              resolve(json);
              return;
            }
          } catch {
            // Keep polling until the daemon API is ready or the deadline expires.
          }
          if (Date.now() >= deadline) resolve(null);
          else setTimeout(check, 500);
        });
      });
      request.on("error", () => {
        if (Date.now() >= deadline) resolve(null);
        else setTimeout(check, 500);
      });
      request.setTimeout(1000, () => request.destroy());
    };
    check();
  });
}

function assertPortAvailable() {
  return new Promise((resolve, reject) => {
    const probe = net.createServer();
    probe.once("error", (error) => {
      if (error.code === "EADDRINUSE") {
        reject(new Error(`Port ${port} is already in use. Run with --port <free-port> or stop the process using that port.`));
        return;
      }
      reject(error);
    });
    probe.listen(port, "0.0.0.0", () => {
      probe.close(() => resolve());
    });
  });
}

mkdirSync(dataDir, { recursive: true });

if (!Number.isInteger(port) || port < 1 || port > 65535) {
  console.error("Port must be an integer between 1 and 65535.");
  process.exit(1);
}

if (existsSync(pidPath)) {
  const existingPid = Number(readFileSync(pidPath, "utf8").trim());
  if (isProcessAlive(existingPid) && processLooksLikeDaemon(existingPid)) {
    const existingHealth = await waitForHealth();
    if (existingHealth) {
      writeState(existingPid, existingHealth);
      printUrls(existingPid, existingHealth);
      process.exit(0);
    }
    console.log(`PID ${existingPid} is alive, but Cliff did not answer on port ${port}. Removing stale daemon state.`);
  } else if (isProcessAlive(existingPid)) {
    console.log(`PID ${existingPid} does not look like a Cliff daemon. Removing stale daemon state.`);
  }
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
}

try {
  await assertPortAvailable();
} catch (error) {
  console.error(error instanceof Error ? error.message : "Port availability check failed.");
  process.exit(1);
}

if (skipInstall) {
  console.log("Skipping dependency install.");
} else if (!forceInstall && existsSync(path.join(root, "node_modules"))) {
  console.log("Dependencies already installed. Use --force-install to refresh them.");
} else {
  console.log("Installing dependencies...");
  run("npm", ["install"]);
}

console.log("Packaging Cliff daemon...");
run("npm", ["run", "daemon:package"]);
if (!existsSync(daemonBinary)) throw new Error("Packaged daemon binary was not created");
if (!existsSync(webDir)) throw new Error("Packaged static dashboard assets were not created");

console.log("Starting Cliff in the background...");
const out = openSync(logPath, "a");
const err = openSync(errorLogPath, "a");
let child;
try {
  child = spawn(daemonBinary, daemonArgs, {
    cwd: packageDir,
    detached: true,
    stdio: ["ignore", out, err],
    env: { ...process.env, CLIFF_PORT: String(port), CLIFF_DATA_DIR: dataDir, CLIFF_WEB_DIR: webDir },
    shell: false,
  });
} finally {
  closeSync(out);
  closeSync(err);
}
child.unref();
writeFileSync(pidPath, String(child.pid));

const health = await waitForHealth();
if (!health && !isProcessAlive(child.pid)) {
  rmSync(pidPath, { force: true });
  rmSync(statePath, { force: true });
  console.error("Cliff failed to start. Check the error log for details:");
  console.error(errorLogPath);
  process.exit(1);
}
writeState(child.pid, health);
printUrls(child.pid, health);

function printUrls(pid, health) {
  const ready = Boolean(health);
  const localUrl = health?.localUrl || `http://localhost:${port}`;
  const lanUrls = health?.lanUrls || lanAddresses().map((address) => `http://${address}:${port}`);
  console.log("");
  console.log(ready ? "Cliff is running." : "Cliff was started, but did not respond yet.");
  console.log(`PID: ${pid}`);
  console.log(`Local: ${localUrl}`);
  for (const url of lanUrls) console.log(`Same network: ${url}`);
  if (lanUrls.length === 0) console.log("Same network: no LAN IPv4 address detected");
  console.log(`Daemon: ${daemonBinary}`);
  console.log(`Logs: ${logPath}`);
  console.log(`Errors: ${errorLogPath}`);
}

function writeState(pid, health) {
  const ready = Boolean(health);
  const localUrl = health?.localUrl || `http://localhost:${port}`;
  const lanUrls = health?.lanUrls || lanAddresses().map((address) => `http://${address}:${port}`);
  writeFileSync(
    statePath,
    `${JSON.stringify(
      {
        pid,
        ready,
        port,
        host: "0.0.0.0",
        localUrl,
        lanUrls,
        dataDir,
        serverRoot,
        packageDir,
        webDir,
        daemonBinary,
        daemonArgs,
        daemonCommand: [daemonBinary, ...daemonArgs].join(" "),
        logPath,
        errorLogPath,
        build: health?.build ?? null,
        platform: health?.platform ?? null,
        health: health ?? null,
        updatedAt: new Date().toISOString(),
      },
      null,
      2,
    )}\n`,
  );
}
