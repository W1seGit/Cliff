import { execFileSync, spawn, spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-start-smoke-"));
const dataDir = path.join(tempRoot, "data");
const serverRoot = path.join(tempRoot, "servers");
const foregroundPidPath = path.join(dataDir, "cliff.pid");
const foregroundStatePath = path.join(dataDir, "cliff.json");
const distDir = path.join(root, "dist");
const launcherPath = path.join(root, "scripts", "start-packaged-daemon.mjs");
const daemonHeapSysBudgetBytes = 64 * 1024 * 1024;
const daemonWorkingSetBudgetBytes = 100 * 1024 * 1024;

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
}

function run(command, args) {
  const result = spawnSync(commandName(command), args, {
    cwd: root,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed with status ${result.status}`);
}

function listProcesses() {
  if (process.platform === "win32") {
    const output = execFileSync(
      "powershell.exe",
      ["-NoProfile", "-Command", "Get-CimInstance Win32_Process | Select-Object ProcessId,Name,CommandLine | ConvertTo-Json -Compress"],
      { encoding: "utf8", windowsHide: true },
    ).trim();
    if (!output) return [];
    const parsed = JSON.parse(output);
    return (Array.isArray(parsed) ? parsed : [parsed]).map((entry) => ({
      pid: Number(entry.ProcessId),
      name: String(entry.Name ?? ""),
      commandLine: String(entry.CommandLine ?? ""),
    }));
  }
  const output = execFileSync("ps", ["-eo", "pid=,comm=,args="], { encoding: "utf8" });
  return output
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [pidText, name, ...args] = line.split(/\s+/);
      return { pid: Number(pidText), name: name ?? "", commandLine: args.join(" ") };
    });
}

function getFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      server.close(() => resolve(port));
    });
  });
}

async function request(baseUrl, pathname, options = {}) {
  const response = await fetch(`${baseUrl}${pathname}`, {
    ...options,
    headers: {
      ...(options.body && !(options.body instanceof FormData) ? { "Content-Type": "application/json" } : {}),
      ...(options.headers ?? {}),
    },
  });
  const text = await response.text();
  let json = null;
  if (text) {
    try {
      json = JSON.parse(text);
    } catch {
      json = null;
    }
  }
  return { response, text, json };
}

async function expectStatus(baseUrl, pathname, status, options = {}) {
  const result = await request(baseUrl, pathname, options);
  if (result.response.status !== status) {
    throw new Error(`${pathname} returned ${result.response.status}, expected ${status}: ${result.text}`);
  }
  return result;
}

async function waitForHealth(baseUrl, child, logs) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`npm start exited early with code ${child.exitCode}\n${logs.join("")}`);
    try {
      const health = await request(baseUrl, "/api/health");
      if (health.response.ok && health.json?.ok === true && health.json?.daemon === "cliff") return health.json;
    } catch {
      // Keep polling until the foreground daemon listener is ready.
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`npm start daemon did not become healthy\n${logs.join("")}`);
}

async function waitForForegroundState(pid) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (existsSync(foregroundStatePath)) {
      const state = JSON.parse(readFileSync(foregroundStatePath, "utf8"));
      if (state.pid === pid && state.ready === true && state.health?.daemon === "cliff") return state;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error("npm start did not update foreground state metadata with daemon health");
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGINT");
  await new Promise((resolve) => {
    const timer = setTimeout(resolve, 3000);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
  });
  if (child.exitCode === null) child.kill("SIGKILL");
}

function stopPackagedDaemonForDataDir(dataPath) {
  stopPackagedDaemonsMatching((processInfo) => processInfo.commandLine.includes(dataPath));
}

function stopPackagedDaemonsMatching(matches) {
  for (const processInfo of listProcesses()) {
    if (!Number.isInteger(processInfo.pid) || processInfo.pid <= 0 || processInfo.pid === process.pid) continue;
    if (!processInfo.commandLine.includes("cliff") && !processInfo.name.toLowerCase().includes("cliff")) continue;
    if (!matches(processInfo)) continue;
    if (process.platform === "win32") {
      spawnSync("powershell.exe", ["-NoProfile", "-Command", `Stop-Process -Id ${processInfo.pid} -Force -ErrorAction SilentlyContinue`], {
        stdio: "ignore",
        windowsHide: true,
      });
    } else {
      try {
        process.kill(processInfo.pid, "SIGKILL");
      } catch {
        // The daemon may have already exited.
      }
    }
  }
}

function processWorkingSetBytes(pid) {
  if (!Number.isInteger(pid) || pid <= 0) throw new Error(`Invalid daemon PID for memory check: ${pid}`);
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
}

try {
  const launcherText = readFileSync(launcherPath, "utf8");
  if (launcherText.includes("go run") || launcherText.includes(".next/standalone")) {
    throw new Error("start-packaged-daemon launcher must not invoke go run or the standalone Next server");
  }

  await mkdir(dataDir, { recursive: true });
  await mkdir(serverRoot, { recursive: true });
  stopPackagedDaemonsMatching((processInfo) => processInfo.commandLine.includes(path.join(distDir, "cliff")));
  run("npm", ["run", "build"]);

  const port = await getFreePort();
  const baseUrl = `http://127.0.0.1:${port}`;
  const logs = [];
  const child = spawn(commandName("npm"), ["start", "--", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot], {
    cwd: root,
    shell: process.platform === "win32",
    stdio: ["ignore", "pipe", "pipe"],
  });
  child.stdout.on("data", (chunk) => logs.push(chunk.toString()));
  child.stderr.on("data", (chunk) => logs.push(chunk.toString()));

  try {
    const health = await waitForHealth(baseUrl, child, logs);
    const startupOutput = logs.join("");
    if (!startupOutput.includes(`Local: http://localhost:${port}`)) {
      throw new Error(`npm start did not print the local dashboard URL:\n${startupOutput}`);
    }
    if (startupOutput.includes("<host-lan-ip>")) {
      throw new Error(`npm start printed the placeholder LAN URL instead of resolved LAN output:\n${startupOutput}`);
    }
    if (!/Same network: (http:\/\/\d{1,3}(?:\.\d{1,3}){3}:\d+|no LAN IPv4 address detected)/.test(startupOutput)) {
      throw new Error(`npm start did not print same-network URL status:\n${startupOutput}`);
    }
    if (health.build?.version !== "0.1.0" || typeof health.build?.commit !== "string" || !health.platform) {
      throw new Error(`npm start daemon did not expose packaged build metadata: ${JSON.stringify(health)}`);
    }
    const heapSysBytes = Number(health.self?.heapSysBytes);
    if (!Number.isFinite(heapSysBytes) || heapSysBytes > daemonHeapSysBudgetBytes) {
      throw new Error(`npm start daemon Go heap reservation exceeded budget: ${JSON.stringify(health.self)}`);
    }

    const daemonPid = Number(health.self?.pid);
    if (!Number.isInteger(daemonPid) || daemonPid <= 0) {
      throw new Error(`npm start daemon health did not include a valid PID: ${JSON.stringify(health.self)}`);
    }
    if (!existsSync(foregroundPidPath) || !existsSync(foregroundStatePath)) {
      throw new Error("npm start did not write foreground daemon PID/state metadata while running");
    }
    const foregroundPid = Number(readFileSync(foregroundPidPath, "utf8").trim());
    if (foregroundPid !== daemonPid) {
      throw new Error(`npm start foreground PID metadata ${foregroundPid} did not match daemon PID ${daemonPid}`);
    }
    const foregroundState = await waitForForegroundState(daemonPid);
    if (foregroundState.daemonBinary?.includes("cliff") !== true || foregroundState.serverRoot !== serverRoot || foregroundState.health?.self?.pid !== daemonPid) {
      throw new Error(`npm start foreground state metadata is incomplete: ${JSON.stringify(foregroundState)}`);
    }
    const workingSetBytes = processWorkingSetBytes(daemonPid);
    if (!Number.isFinite(workingSetBytes) || workingSetBytes > daemonWorkingSetBudgetBytes) {
      throw new Error(`npm start daemon working set exceeded ${daemonWorkingSetBudgetBytes} bytes: ${workingSetBytes}`);
    }

    const setup = await expectStatus(baseUrl, "/api/auth/setup", 200, {
      method: "POST",
      body: JSON.stringify({ username: "start-smoke", password: "start-smoke-password" }),
    });
    const cookie = setup.response.headers.get("set-cookie")?.split(";")[0];
    if (!cookie) throw new Error("npm start setup did not return a session cookie");
    const settings = await expectStatus(baseUrl, "/api/settings?storage=0", 200, { headers: { cookie } });
    if (path.resolve(settings.json?.serverRoot ?? "") !== path.resolve(serverRoot)) {
      throw new Error(`npm start daemon did not report the requested server root: ${settings.text}`);
    }

    const page = await request(baseUrl, "/servers/srv_start_smoke/overview");
    if (!page.response.ok || !page.text.includes("Loading dashboard")) {
      throw new Error(`npm start SPA fallback failed: ${page.response.status} ${page.text.slice(0, 200)}`);
    }
  } finally {
    await stopProcess(child);
    stopPackagedDaemonForDataDir(dataDir);
  }

  if (existsSync(path.join(dataDir, "cliff.pid")) || existsSync(path.join(dataDir, "cliff.json"))) {
    throw new Error("npm start foreground daemon should not write background daemon PID/state files");
  }

  console.log("Start smoke test passed");
} finally {
  stopPackagedDaemonForDataDir(dataDir);
  await new Promise((resolve) => setTimeout(resolve, 250));
  await rm(tempRoot, { recursive: true, force: true });
  await rm(distDir, { recursive: true, force: true });
}
