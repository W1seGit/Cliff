import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-install-run-smoke-"));
const dataDir = path.join(tempRoot, "data");
const serverRoot = path.join(tempRoot, "servers");
const distDir = path.join(root, "dist");
const daemonHeapSysBudgetBytes = 64 * 1024 * 1024;
const daemonWorkingSetBudgetBytes = 100 * 1024 * 1024;

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
}

function run(command, args, options = {}) {
  const result = spawnSync(commandName(command), args, {
    cwd: root,
    stdio: options.stdio ?? "inherit",
    encoding: options.encoding,
    shell: process.platform === "win32",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed with status ${result.status}`);
  return result;
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

async function request(baseUrl, pathname) {
  const response = await fetch(`${baseUrl}${pathname}`);
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

try {
  await mkdir(dataDir, { recursive: true });
  await mkdir(serverRoot, { recursive: true });
  const port = await getFreePort();
  const baseUrl = `http://127.0.0.1:${port}`;

  const installResult = run("npm", ["run", "install:run", "--", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot], { stdio: "pipe", encoding: "utf8" });
  const installOutput = `${installResult.stdout}\n${installResult.stderr}`;
  if (!installOutput.includes("Cliff is running.") || !installOutput.includes(`Local: http://localhost:${port}`) || !installOutput.includes("Same network:")) {
    throw new Error(`install-run output did not include running status plus local/LAN URLs:\n${installOutput}`);
  }

  const statePath = path.join(dataDir, "cliff.json");
  if (!existsSync(statePath)) throw new Error("install-run did not write daemon state");
  const state = JSON.parse(readFileSync(statePath, "utf8"));
  if (state.port !== port || state.localUrl !== `http://localhost:${port}` || !state.pid) {
    throw new Error(`install-run wrote invalid daemon state: ${JSON.stringify(state)}`);
  }
  if (state.serverRoot !== serverRoot) {
    throw new Error(`install-run state did not persist the selected server root: ${JSON.stringify(state)}`);
  }
  if (!Array.isArray(state.daemonArgs) || !state.daemonArgs.includes("--server-root") || !state.daemonArgs.includes(serverRoot) || !String(state.daemonCommand ?? "").includes("cliff")) {
    throw new Error(`install-run state did not persist daemon launch command metadata: ${JSON.stringify(state)}`);
  }
  if (state.ready !== true || state.build?.version !== "0.1.0" || typeof state.build?.commit !== "string" || !state.platform) {
    throw new Error(`install-run did not persist healthy daemon metadata: ${JSON.stringify(state)}`);
  }
  if (state.health?.daemon !== "cliff" || state.health?.build?.commit !== state.build.commit || state.health?.platform !== state.platform) {
    throw new Error(`install-run did not persist daemon health payload in state: ${JSON.stringify(state)}`);
  }

  const health = await request(baseUrl, "/api/health");
  if (!health.response.ok || health.json?.daemon !== "cliff") {
    throw new Error(`install-run daemon health failed: ${health.response.status} ${health.text}`);
  }
  const logPath = path.join(dataDir, "cliff.log");
  const logText = readFileSync(logPath, "utf8");
  if (!logText.includes("cliff daemon listening")) {
    throw new Error(`install-run daemon log did not include startup output: ${logText}`);
  }
  if (health.json?.build?.commit !== state.build.commit || health.json?.platform !== state.platform) {
    throw new Error(`install-run state metadata did not match daemon health: ${JSON.stringify({ state, health: health.json })}`);
  }
  if (Number(health.json?.self?.pid) !== state.pid) {
    throw new Error(`install-run daemon health PID did not match persisted state: ${JSON.stringify({ statePid: state.pid, healthPid: health.json?.self?.pid })}`);
  }
  const heapSysBytes = Number(health.json?.self?.heapSysBytes);
  if (!Number.isFinite(heapSysBytes) || heapSysBytes > daemonHeapSysBudgetBytes) {
    throw new Error(`daemon Go heap reservation exceeded budget: ${JSON.stringify(health.json?.self)}`);
  }
  const workingSetBytes = processWorkingSetBytes(state.pid);
  if (!Number.isFinite(workingSetBytes) || workingSetBytes > daemonWorkingSetBudgetBytes) {
    throw new Error(`daemon working set exceeded ${daemonWorkingSetBudgetBytes} bytes: ${workingSetBytes}`);
  }

  const reusedResult = run("npm", ["run", "install:run", "--", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot, "--skip-install"], { stdio: "pipe", encoding: "utf8" });
  const reusedOutput = `${reusedResult.stdout}\n${reusedResult.stderr}`;
  if (!reusedOutput.includes("Cliff is running.") || !reusedOutput.includes(`Local: http://localhost:${port}`) || !reusedOutput.includes("Same network:")) {
    throw new Error(`install-run reuse output did not include running status plus local/LAN URLs:\n${reusedOutput}`);
  }
  const reusedState = JSON.parse(readFileSync(statePath, "utf8"));
  if (reusedState.pid !== state.pid || reusedState.ready !== true || reusedState.build?.commit !== state.build.commit) {
    throw new Error(`install-run did not reuse the healthy background daemon: ${JSON.stringify({ state, reusedState })}`);
  }
  if (reusedState.health?.daemon !== "cliff" || reusedState.health?.build?.commit !== state.build.commit) {
    throw new Error(`install-run reuse did not refresh daemon health payload: ${JSON.stringify({ state, reusedState })}`);
  }

  const statusResult = run("npm", ["run", "daemon:status", "--", "--data-dir", dataDir], { stdio: "pipe", encoding: "utf8" });
  const statusOutput = `${statusResult.stdout}\n${statusResult.stderr}`;
  for (const expected of ["Cliff daemon is running.", "Daemon heap:", "Daemon memory:", "Daemon goroutines:", "Command:", "Logs:", "Errors:"]) {
    if (!statusOutput.includes(expected)) {
      throw new Error(`daemon status output did not include ${expected}\n${statusOutput}`);
    }
  }

  const page = await request(baseUrl, "/servers/srv_install_smoke/overview");
  if (!page.response.ok || !page.text.includes("Loading dashboard")) {
    throw new Error(`install-run SPA fallback failed: ${page.response.status} ${page.text.slice(0, 200)}`);
  }

  run("npm", ["run", "daemon:stop", "--", "--data-dir", dataDir]);

  if (existsSync(path.join(dataDir, "cliff.pid"))) {
    throw new Error("install-run stop did not remove the PID file");
  }
  if (existsSync(statePath)) {
    throw new Error("install-run stop did not remove the state file");
  }

  writeFileSync(path.join(dataDir, "cliff.pid"), String(process.pid));
  const staleStatusResult = run("npm", ["run", "daemon:status", "--", "--data-dir", dataDir], { stdio: "pipe", encoding: "utf8" });
  const staleStatusOutput = `${staleStatusResult.stdout}\n${staleStatusResult.stderr}`;
  if (!staleStatusOutput.includes("does not look like a Cliff daemon") || !staleStatusOutput.includes("Removed stale state")) {
    throw new Error(`daemon status did not report stale non-daemon PID cleanup:\n${staleStatusOutput}`);
  }
  if (existsSync(path.join(dataDir, "cliff.pid")) || existsSync(statePath)) {
    throw new Error("daemon status did not remove a PID file owned by a non-daemon process");
  }

  console.log("Install-run smoke test passed");
} finally {
  try {
    run("npm", ["run", "daemon:stop", "--", "--data-dir", dataDir, "--force"]);
  } catch {
    // The daemon may already be stopped.
  }
  await rm(tempRoot, { recursive: true, force: true });
  await rm(distDir, { recursive: true, force: true });
}
