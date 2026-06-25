import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import AdmZip from "adm-zip";

const root = process.cwd();
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-install-package-smoke-"));
const installDir = path.join(tempRoot, "installed");
const bootstrapInstallDir = path.join(tempRoot, "bootstrap-installed");
const invalidInstallDir = path.join(tempRoot, "invalid-installed");
const distDir = path.join(root, "dist");

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
}

function run(command, args, options = {}) {
  const result = spawnSync(commandName(command), args, {
    cwd: options.cwd ?? root,
    stdio: options.stdio ?? "inherit",
    encoding: options.encoding,
    shell: process.platform === "win32",
    env: options.env ?? process.env,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed with status ${result.status}`);
  return result;
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
  const port = await getFreePort();
  const baseUrl = `http://127.0.0.1:${port}`;
  run("npm", ["run", "daemon:package"]);

  let installOutput = "";
  if (process.platform === "win32") {
    const result = run("powershell.exe", ["-ExecutionPolicy", "Bypass", "-File", ".\\scripts\\install-package.ps1", "-Manifest", ".\\dist\\cliff-release.json", "-InstallDir", installDir, "-Port", String(port), "-Start", "-Force"], { stdio: "pipe", encoding: "utf8" });
    installOutput = `${result.stdout}\n${result.stderr}`;
  } else {
    const result = run("sh", ["./scripts/install-package.sh", "--manifest", "./dist/cliff-release.json", "--install-dir", installDir, "--port", String(port), "--start", "--force"], { stdio: "pipe", encoding: "utf8" });
    installOutput = `${result.stdout}\n${result.stderr}`;
  }
  if (!installOutput.includes("Cliff installed.") || !installOutput.includes(`Local: http://localhost:${port}`) || !installOutput.includes("Same network:")) {
    throw new Error(`install package output did not include install status plus local/LAN URLs:\n${installOutput}`);
  }
  if (!installOutput.includes("Cliff is running.")) {
    throw new Error(`install package --start output did not include daemon startup status:\n${installOutput}`);
  }

  const health = await request(baseUrl, "/api/health");
  if (!health.response.ok || health.json?.daemon !== "cliff") {
    throw new Error(`installed package health failed: ${health.response.status} ${health.text}`);
  }
  const statePath = path.join(installDir, "data", "cliff.json");
  if (!existsSync(statePath)) {
    throw new Error("installed package did not write daemon state");
  }
  const state = JSON.parse(readFileSync(statePath, "utf8"));
  const stateCommit = state.build?.commit ?? state.health?.build?.commit;
  const statePlatform = state.platform ?? state.health?.platform;
  const expectedServerRoot = path.join(installDir, "servers");
  if (state.ready !== true || stateCommit !== health.json?.build?.commit || statePlatform !== health.json?.platform || state.health?.daemon !== "cliff") {
    throw new Error(`installed package state did not persist health metadata: ${JSON.stringify({ state, health: health.json })}`);
  }
  if (state.serverRoot !== expectedServerRoot) {
    throw new Error(`installed package state did not persist the package-local server root: ${JSON.stringify(state)}`);
  }
  const logPath = path.join(installDir, "data", "cliff.log");
  if (!existsSync(logPath) || !readFileSync(logPath, "utf8").includes("cliff daemon listening")) {
    throw new Error("installed package daemon log did not include startup output");
  }

  if (!existsSync(path.join(installDir, process.platform === "win32" ? "cliff.exe" : "cliff"))) {
    throw new Error("installed package binary is missing");
  }

  let statusOutput = "";
  if (process.platform === "win32") {
    const result = run("powershell.exe", ["-ExecutionPolicy", "Bypass", "-File", path.join(installDir, "status.ps1"), "-Port", String(port)], { stdio: "pipe", encoding: "utf8" });
    statusOutput = result.stdout;
    run("powershell.exe", ["-ExecutionPolicy", "Bypass", "-File", path.join(installDir, "stop.ps1"), "-Force"]);
  } else {
    const result = run("sh", [path.join(installDir, "status.sh")], { env: { ...process.env, PORT: String(port) }, stdio: "pipe", encoding: "utf8" });
    statusOutput = result.stdout;
    run("sh", [path.join(installDir, "stop.sh")], { env: { ...process.env, FORCE: "1" } });
  }
  if (!statusOutput.includes("Cliff daemon is running.") || !statusOutput.includes("Daemon heap:")) {
    throw new Error(`installed package status output did not include runtime metadata:\n${statusOutput}`);
  }
  if (!installOutput.includes("Verified package SHA-256:")) {
    throw new Error(`install package output did not verify package integrity:\n${installOutput}`);
  }

  const releaseManifest = JSON.parse(readFileSync(path.join(distDir, "cliff-release.json"), "utf8"));
  const invalidArchivePath = path.join(tempRoot, "cliff-invalid.zip");
  const invalidZip = new AdmZip();
  invalidZip.addFile("cliff/README.txt", Buffer.from("missing daemon files\n"));
  invalidZip.writeZip(invalidArchivePath);
  await mkdir(invalidInstallDir, { recursive: true });
  writeFileSync(path.join(invalidInstallDir, "keep.txt"), "existing install should survive invalid package\n");
  const invalidResult = process.platform === "win32"
    ? spawnSync(commandName("powershell.exe"), ["-ExecutionPolicy", "Bypass", "-File", ".\\scripts\\install-package.ps1", "-Package", invalidArchivePath, "-InstallDir", invalidInstallDir, "-Force"], {
        cwd: root,
        stdio: "pipe",
        encoding: "utf8",
        shell: process.platform === "win32",
      })
    : spawnSync(commandName("sh"), ["./scripts/install-package.sh", "--package", invalidArchivePath, "--install-dir", invalidInstallDir, "--force", "--skip-checksum"], {
        cwd: root,
        stdio: "pipe",
        encoding: "utf8",
        shell: process.platform === "win32",
      });
  if (invalidResult.status === 0) {
    throw new Error("installer unexpectedly accepted an invalid package archive");
  }
  const invalidOutput = `${invalidResult.stdout}\n${invalidResult.stderr}`;
  if (!invalidOutput.includes("Package archive is missing required file")) {
    throw new Error(`installer rejected invalid package for the wrong reason:\n${invalidOutput}`);
  }
  if (!existsSync(path.join(invalidInstallDir, "keep.txt"))) {
    throw new Error("installer removed an existing install before validating the replacement archive");
  }

  await rm(path.join(distDir, releaseManifest.archive.checksumFile), { force: true });

  let bootstrapOutput = "";
  if (process.platform === "win32") {
    const result = run("powershell.exe", ["-ExecutionPolicy", "Bypass", "-File", ".\\scripts\\install.ps1", "-Manifest", ".\\dist\\cliff-release.json", "-InstallDir", bootstrapInstallDir, "-Port", String(port), "-Force"], { stdio: "pipe", encoding: "utf8" });
    bootstrapOutput = `${result.stdout}\n${result.stderr}`;
  } else {
    const result = run("sh", ["./scripts/install.sh", "--manifest", "./dist/cliff-release.json", "--install-dir", bootstrapInstallDir, "--port", String(port), "--force"], { stdio: "pipe", encoding: "utf8" });
    bootstrapOutput = `${result.stdout}\n${result.stderr}`;
  }
  if (!bootstrapOutput.includes("Cliff installed.") || !bootstrapOutput.includes("Cliff is running.") || !bootstrapOutput.includes(`Local: http://localhost:${port}`) || !bootstrapOutput.includes("Same network:")) {
    throw new Error(`bootstrap installer output did not include install/start status plus local/LAN URLs:\n${bootstrapOutput}`);
  }
  if (!bootstrapOutput.includes("Verified package SHA-256:")) {
    throw new Error(`bootstrap installer did not verify package integrity through the release manifest hash:\n${bootstrapOutput}`);
  }
  const bootstrapHealth = await request(baseUrl, "/api/health");
  if (!bootstrapHealth.response.ok || bootstrapHealth.json?.daemon !== "cliff") {
    throw new Error(`bootstrap installed package health failed: ${bootstrapHealth.response.status} ${bootstrapHealth.text}`);
  }
  const bootstrapStatePath = path.join(bootstrapInstallDir, "data", "cliff.json");
  if (!existsSync(bootstrapStatePath)) {
    throw new Error("bootstrap installed package did not write daemon state");
  }
  const bootstrapState = JSON.parse(readFileSync(bootstrapStatePath, "utf8"));
  const expectedBootstrapServerRoot = path.join(bootstrapInstallDir, "servers");
  if (bootstrapState.serverRoot !== expectedBootstrapServerRoot) {
    throw new Error(`bootstrap installer state did not persist the package-local server root: ${JSON.stringify(bootstrapState)}`);
  }
  if (process.platform === "win32") {
    run("powershell.exe", ["-ExecutionPolicy", "Bypass", "-File", path.join(bootstrapInstallDir, "stop.ps1"), "-Force"]);
  } else {
    run("sh", [path.join(bootstrapInstallDir, "stop.sh")], { env: { ...process.env, FORCE: "1" } });
  }

  console.log("Install-package smoke test passed");
} finally {
  try {
    if (process.platform === "win32" && existsSync(path.join(bootstrapInstallDir, "stop.ps1"))) {
      run("powershell.exe", ["-ExecutionPolicy", "Bypass", "-File", path.join(bootstrapInstallDir, "stop.ps1"), "-Force"]);
    } else if (existsSync(path.join(bootstrapInstallDir, "stop.sh"))) {
      run("sh", [path.join(bootstrapInstallDir, "stop.sh")], { env: { ...process.env, FORCE: "1" } });
    }
  } catch {
    // The daemon may already be stopped.
  }
  try {
    if (process.platform === "win32" && existsSync(path.join(installDir, "stop.ps1"))) {
      run("powershell.exe", ["-ExecutionPolicy", "Bypass", "-File", path.join(installDir, "stop.ps1"), "-Force"]);
    } else if (existsSync(path.join(installDir, "stop.sh"))) {
      run("sh", [path.join(installDir, "stop.sh")], { env: { ...process.env, FORCE: "1" } });
    }
  } catch {
    // The daemon may already be stopped.
  }
  await rm(tempRoot, { recursive: true, force: true });
  await rm(distDir, { recursive: true, force: true });
}
