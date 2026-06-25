import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, readFileSync } from "node:fs";
import { mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import AdmZip from "adm-zip";

const root = process.cwd();
const distDir = path.join(root, "dist");
const packageDir = path.join(distDir, "cliff");
const binary = path.join(packageDir, process.platform === "win32" ? "cliff.exe" : "cliff");
const webDir = path.join(packageDir, "web");
const packageDataDir = path.join(packageDir, "data");
const packageServerDir = path.join(packageDir, "servers");
const buildJson = path.join(packageDir, "build.json");
const packageManifestJson = path.join(packageDir, "package-manifest.json");
const releaseManifestJson = path.join(distDir, "cliff-release.json");
const releaseInstallerFiles = ["install.ps1", "install.sh", "install-package.ps1", "install-package.sh"];
const runPowerShell = path.join(packageDir, "run.ps1");
const statusPowerShell = path.join(packageDir, "status.ps1");
const stopPowerShell = path.join(packageDir, "stop.ps1");
const runShell = path.join(packageDir, "run.sh");
const statusShell = path.join(packageDir, "status.sh");
const stopShell = path.join(packageDir, "stop.sh");
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-package-smoke-"));
const dataDir = path.join(tempRoot, "data");
const serverRoot = path.join(tempRoot, "servers");
const HEAP_ALLOC_BUDGET_BYTES = 32 * 1024 * 1024;
const HEAP_SYS_BUDGET_BYTES = 96 * 1024 * 1024;
const GOROUTINE_BUDGET = 64;

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

function runAllowFailure(command, args) {
  return spawnSync(commandName(command), args, {
    cwd: root,
    encoding: "utf8",
    shell: process.platform === "win32",
  });
}

function capture(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd ?? packageDir,
    encoding: "utf8",
    shell: options.shell ?? false,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed with status ${result.status}: ${result.stderr}`);
  return result.stdout.trim();
}

function capturePackageCommand(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: packageDir,
    encoding: "utf8",
    env: { ...process.env, ...(options.env ?? {}) },
    shell: options.shell ?? false,
    timeout: options.timeout ?? 45_000,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed with status ${result.status}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`);
  }
  return result.stdout.trim();
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
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

async function assertFile(filePath, label) {
  const info = await stat(filePath).catch(() => null);
  if (!info?.isFile()) throw new Error(`${label} missing at ${path.relative(root, filePath)}`);
}

async function assertDirectory(dirPath, label) {
  const info = await stat(dirPath).catch(() => null);
  if (!info?.isDirectory()) throw new Error(`${label} missing at ${path.relative(root, dirPath)}`);
}

async function fileSHA256(filePath) {
  const data = await readFile(filePath);
  return createHash("sha256").update(data).digest("hex");
}

async function readBundleText(dirPath) {
  const entries = await readdir(dirPath, { withFileTypes: true });
  const chunks = [];
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      chunks.push(await readBundleText(entryPath));
    } else if (entry.name.endsWith(".js")) {
      chunks.push(readFileSync(entryPath, "utf8"));
    }
  }
  return chunks.join("\n");
}

async function findArchive() {
  const entries = await readdir(distDir);
  const archives = entries.filter((entry) => /^cliff-.+\.zip$/.test(entry)).sort();
  if (archives.length !== 1) throw new Error(`expected one daemon archive, found ${archives.length}: ${archives.join(", ")}`);
  return path.join(distDir, archives[0]);
}

async function request(baseUrl, pathname, options = {}) {
  const response = await fetch(`${baseUrl}${pathname}`, {
    ...options,
    headers: {
      ...(options.body ? { "Content-Type": "application/json" } : {}),
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

async function waitForHealth(baseUrl, child, logs) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`packaged daemon exited early with code ${child.exitCode}\n${logs.join("")}`);
    try {
      const { response, json } = await request(baseUrl, "/api/health");
      if (response.ok && json?.ok === true && json?.daemon === "cliff") return json;
    } catch {
      // Keep polling until the packaged daemon listener is ready.
    }
    await wait(150);
  }
  throw new Error(`packaged daemon did not become healthy\n${logs.join("")}`);
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

async function smokeExtractedArchive(archivePath) {
  const extractRoot = path.join(tempRoot, "archive-extract");
  const extractedPackageDir = path.join(extractRoot, "cliff");
  const dataName = "data-archive-smoke";
  const serverName = "servers-archive-smoke";
  const extractedDataDir = path.join(extractedPackageDir, dataName);
  const extractedServerRoot = path.join(extractedPackageDir, serverName);
  const port = await getFreePort();
  new AdmZip(archivePath).extractAllTo(extractRoot, true);

  await assertDirectory(extractedPackageDir, "extracted daemon package");
  const extractedBinary = path.join(extractedPackageDir, process.platform === "win32" ? "cliff.exe" : "cliff");
  await assertFile(extractedBinary, "extracted daemon binary");
  await assertFile(path.join(extractedPackageDir, "web", "index.html"), "extracted static web index");
  await mkdir(extractedDataDir, { recursive: true });
  await mkdir(extractedServerRoot, { recursive: true });

  const logs = [];
  const extractedChild = spawn(extractedBinary, ["--host", "127.0.0.1", "--port", String(port), "--data-dir", extractedDataDir, "--server-root", extractedServerRoot, "--web-dir", path.join(extractedPackageDir, "web")], {
    cwd: extractedPackageDir,
    env: { ...process.env },
    stdio: ["ignore", "pipe", "pipe"],
  });
  extractedChild.stdout.on("data", (chunk) => logs.push(chunk.toString()));
  extractedChild.stderr.on("data", (chunk) => logs.push(chunk.toString()));
  try {
    await waitForExtractedHealth(port, extractedChild, logs);
  } finally {
    await stopProcess(extractedChild);
  }
}

async function smokePackagedRunnerScripts() {
  const port = await getFreePort();
  const dataName = "data-runner-smoke";
  const serverName = "servers-runner-smoke";
  const statePath = path.join(packageDir, dataName, "cliff.json");
  const pidPath = path.join(packageDir, dataName, "cliff.pid");
  const logPath = path.join(packageDir, dataName, "cliff.log");
  const baseUrl = `http://127.0.0.1:${port}`;

  try {
    let firstOutput = "";
    let secondOutput = "";
    let statusOutput = "";
    let firstState = null;
    if (process.platform === "win32") {
      const runArgs = ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", runPowerShell, "-Port", String(port), "-DataDir", dataName, "-ServerRoot", serverName];
      firstOutput = capturePackageCommand("powershell.exe", runArgs);
      firstState = readPackagedRunnerState(statePath, pidPath, port);
      secondOutput = capturePackageCommand("powershell.exe", runArgs);
      statusOutput = capturePackageCommand("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", statusPowerShell, "-DataDir", dataName]);
    } else {
      const env = { PORT: String(port), DATA_DIR: dataName, SERVER_ROOT: serverName };
      firstOutput = capturePackageCommand("sh", [runShell], { env });
      firstState = readPackagedRunnerState(statePath, pidPath, port);
      secondOutput = capturePackageCommand("sh", [runShell], { env });
      statusOutput = capturePackageCommand("sh", [statusShell], { env: { DATA_DIR: dataName } });
    }

    if (!firstOutput.includes("Cliff is running.") || !secondOutput.includes("Cliff is running.") || !statusOutput.includes("Cliff daemon is running.")) {
      throw new Error(`packaged run/status scripts did not report a healthy daemon:\nfirst:\n${firstOutput}\nsecond:\n${secondOutput}\nstatus:\n${statusOutput}`);
    }
    for (const expected of ["Daemon heap:", "Daemon memory:", "Daemon goroutines:", "Command:", "Logs:", "Errors:"]) {
      if (!statusOutput.includes(expected)) {
        throw new Error(`packaged status script did not report ${expected}\n${statusOutput}`);
      }
    }
    const health = await request(baseUrl, "/api/health");
    if (!health.response.ok || health.json?.daemon !== "cliff") {
      throw new Error(`packaged runner daemon health failed: ${health.response.status} ${health.text}`);
    }
    const logText = readFileSync(logPath, "utf8");
    if (!logText.includes("cliff daemon listening")) {
      throw new Error(`packaged runner daemon log did not include startup output: ${logText}`);
    }
    const secondState = readPackagedRunnerState(statePath, pidPath, port);
    if (secondState.pid !== firstState.pid || secondState.ready !== true) {
      throw new Error(`packaged runner did not reuse the healthy daemon: ${JSON.stringify({ firstState, secondState })}`);
    }
  } finally {
    if (process.platform === "win32") {
      try {
        capturePackageCommand("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", stopPowerShell, "-DataDir", dataName, "-Force"]);
      } catch {
        // The runner may have failed before a daemon was started.
      }
    } else {
      try {
        capturePackageCommand("sh", [stopShell], { env: { DATA_DIR: dataName, FORCE: "1" } });
      } catch {
        // The runner may have failed before a daemon was started.
      }
    }
  }
}

function readPackagedRunnerState(statePath, pidPath, port) {
  if (!existsSync(statePath) || !existsSync(pidPath)) {
    throw new Error("packaged runner did not write daemon state and PID files");
  }
  const state = JSON.parse(readFileSync(statePath, "utf8"));
  if (state.port !== port || state.ready !== true || !state.pid || state.localUrl !== `http://localhost:${port}`) {
    throw new Error(`packaged runner wrote invalid state: ${JSON.stringify(state)}`);
  }
  if (!Array.isArray(state.lanUrls)) {
    throw new Error(`packaged runner did not persist LAN URL metadata as an array: ${JSON.stringify(state)}`);
  }
  if (!String(state.daemonCommand ?? "").includes("cliff") || !String(state.daemonCommand ?? "").includes("--server-root")) {
    throw new Error(`packaged runner did not persist daemon command metadata: ${JSON.stringify(state)}`);
  }
  return state;
}

async function waitForExtractedHealth(port, extractedChild, logs) {
  const baseUrl = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (extractedChild.exitCode !== null) throw new Error(`extracted package daemon exited early with code ${extractedChild.exitCode}\n${logs.join("")}`);
    try {
      const { response, json } = await request(baseUrl, "/api/health");
      if (response.ok && json?.ok === true && json?.daemon === "cliff") return json;
    } catch {
      // Keep polling until the extracted package listener is ready.
    }
    await wait(150);
  }
  throw new Error(`extracted package daemon did not become healthy\n${logs.join("")}`);
}

let child = null;
try {
  run("npm", ["run", "daemon:package"]);

  await assertFile(binary, "packaged daemon binary");
  await assertDirectory(webDir, "packaged static web assets");
  await assertFile(path.join(webDir, "index.html"), "packaged static web index");
  await assertFile(path.join(packageDir, "README.txt"), "packaged README");
  await assertFile(runPowerShell, "packaged Windows runner");
  await assertFile(statusPowerShell, "packaged Windows status script");
  await assertFile(stopPowerShell, "packaged Windows stop script");
  await assertFile(runShell, "packaged shell runner");
  await assertFile(statusShell, "packaged shell status script");
  await assertFile(stopShell, "packaged shell stop script");
  await assertFile(buildJson, "packaged build metadata");
  await assertFile(packageManifestJson, "packaged size manifest");
  await assertDirectory(packageDataDir, "packaged data directory");
  await assertDirectory(packageServerDir, "packaged servers directory");
  const archivePath = await findArchive();
  await assertFile(archivePath, "packaged daemon archive");
  await assertFile(`${archivePath}.sha256`, "packaged daemon archive checksum");
  await assertFile(`${archivePath}.json`, "packaged daemon archive metadata");
  await assertFile(releaseManifestJson, "packaged release manifest");
  for (const installerFile of releaseInstallerFiles) {
    await assertFile(path.join(distDir, installerFile), `release installer ${installerFile}`);
  }
  const bundleText = await readBundleText(path.join(webDir, "_next"));
  if (!bundleText.includes("healthFor") || !bundleText.includes("usageFor")) {
    throw new Error("packaged static dashboard does not include scoped overview health/usage query parameters");
  }
  const packagedReadme = readFileSync(path.join(packageDir, "README.txt"), "utf8");
  for (const expected of ["data/cliff.json", "data/cliff.log", "data/cliff-error.log", "heap", "goroutine"]) {
    if (!packagedReadme.includes(expected)) {
      throw new Error(`packaged README is missing diagnostic detail: ${expected}`);
    }
  }
  for (const expected of ["curl -fsSL getcliff.dev/install.sh | sh", "irm getcliff.dev/install.ps1 | iex", "does not require Node.js, Go, or a Next.js server"]) {
    if (!packagedReadme.includes(expected)) {
      throw new Error(`packaged README is missing install detail: ${expected}`);
    }
  }

  const build = JSON.parse(readFileSync(buildJson, "utf8"));
  if (!build.version || !build.commit || !build.builtAt) {
    throw new Error(`packaged build metadata is incomplete: ${JSON.stringify(build)}`);
  }
  const packageManifest = JSON.parse(readFileSync(packageManifestJson, "utf8"));
  if (
    packageManifest.version !== build.version ||
    packageManifest.commit !== build.commit ||
    !Number.isFinite(packageManifest.binarySizeBytes) ||
    !Number.isFinite(packageManifest.webSizeBytes) ||
    !Number.isFinite(packageManifest.packageSizeBytes) ||
    !/^[a-f0-9]{64}$/.test(packageManifest.binarySHA256 ?? "") ||
    !/^[a-f0-9]{64}$/.test(packageManifest.webSHA256 ?? "") ||
    !/^[a-f0-9]{64}$/.test(packageManifest.manifestSHA256 ?? "") ||
    packageManifest.packageSizeBytes <= packageManifest.binarySizeBytes
  ) {
    throw new Error(`packaged size manifest is invalid: ${JSON.stringify(packageManifest)}`);
  }
  const corePayloadSize = packageManifest.binarySizeBytes + packageManifest.webSizeBytes + packageManifest.dataSizeBytes + packageManifest.serverSizeBytes;
  if (packageManifest.packageSizeBytes <= corePayloadSize) {
    throw new Error(`packaged size manifest does not include scripts and metadata: ${JSON.stringify(packageManifest)}`);
  }
  run("npm", ["run", "daemon:verify-package", "--", "--package-dir", packageDir]);
  const leakedSourceFile = path.join(webDir, "src", "app", "api", "route.ts");
  await mkdir(path.dirname(leakedSourceFile), { recursive: true });
  await writeFile(leakedSourceFile, "export const runtime = 'nodejs';\n");
  const invalidWebPackage = runAllowFailure("npm", ["run", "daemon:verify-package", "--", "--package-dir", packageDir]);
  if (invalidWebPackage.status === 0 || !`${invalidWebPackage.stdout}\n${invalidWebPackage.stderr}`.includes("source/API route files")) {
    throw new Error("daemon package verifier did not reject leaked source/API route files in web assets");
  }
  await rm(path.join(webDir, "src"), { recursive: true, force: true });
  const unexpectedRootDir = path.join(packageDir, "data-runner-smoke");
  await mkdir(unexpectedRootDir, { recursive: true });
  await writeFile(path.join(unexpectedRootDir, "runtime.txt"), "runtime data should not be packaged\n");
  const invalidPackage = runAllowFailure("npm", ["run", "daemon:verify-package", "--", "--package-dir", packageDir]);
  if (invalidPackage.status === 0 || !`${invalidPackage.stdout}\n${invalidPackage.stderr}`.includes("unexpected entries")) {
    throw new Error("daemon package verifier did not reject an unexpected runtime directory in the package root");
  }
  await rm(unexpectedRootDir, { recursive: true, force: true });
  run("npm", ["run", "daemon:verify-archive", "--", "--archive", archivePath]);
  run("npm", ["run", "daemon:verify-release", "--", "--manifest", releaseManifestJson]);
  const invalidArchive = path.join(tempRoot, "cliff-invalid-extra.zip");
  copyFileSync(archivePath, invalidArchive);
  copyFileSync(`${archivePath}.sha256`, `${invalidArchive}.sha256`);
  copyFileSync(`${archivePath}.json`, `${invalidArchive}.json`);
  const invalidZip = new AdmZip(invalidArchive);
  invalidZip.addFile("cliff/data-runner-smoke/runtime.txt", Buffer.from("runtime data should not be archived\n"));
  invalidZip.writeZip(invalidArchive);
  const invalidArchiveHash = await fileSHA256(invalidArchive);
  const invalidArchiveInfo = await stat(invalidArchive);
  const invalidMetadata = JSON.parse(readFileSync(`${invalidArchive}.json`, "utf8"));
  invalidMetadata.archive = path.basename(invalidArchive);
  invalidMetadata.archiveSHA256 = invalidArchiveHash;
  invalidMetadata.archiveSizeBytes = invalidArchiveInfo.size;
  await writeFile(`${invalidArchive}.sha256`, `${invalidArchiveHash}  ${path.basename(invalidArchive)}\n`);
  await writeFile(`${invalidArchive}.json`, `${JSON.stringify(invalidMetadata, null, 2)}\n`);
  const invalidArchiveResult = runAllowFailure("npm", ["run", "daemon:verify-archive", "--", "--archive", invalidArchive]);
  if (invalidArchiveResult.status === 0 || !`${invalidArchiveResult.stdout}\n${invalidArchiveResult.stderr}`.includes("unexpected entries")) {
    throw new Error("daemon archive verifier did not reject an unexpected runtime directory in the archive");
  }
  const invalidReleaseManifest = path.join(distDir, "cliff-release-invalid.json");
  const invalidRelease = JSON.parse(readFileSync(releaseManifestJson, "utf8"));
  invalidRelease.archive.sha256 = "0".repeat(64);
  await writeFile(invalidReleaseManifest, `${JSON.stringify(invalidRelease, null, 2)}\n`);
  const invalidReleaseResult = runAllowFailure("npm", ["run", "daemon:verify-release", "--", "--manifest", invalidReleaseManifest]);
  if (invalidReleaseResult.status === 0 || !`${invalidReleaseResult.stdout}\n${invalidReleaseResult.stderr}`.includes("archive hash")) {
    throw new Error("daemon release verifier did not reject a stale archive hash");
  }
  await rm(invalidReleaseManifest, { force: true });
  const archiveInfo = await stat(archivePath);
  const releaseManifest = JSON.parse(readFileSync(releaseManifestJson, "utf8"));
  if (
    releaseManifest.schemaVersion !== 1 ||
    releaseManifest.version !== build.version ||
    releaseManifest.commit !== build.commit ||
    releaseManifest.platform?.os !== process.platform ||
    releaseManifest.platform?.arch !== process.arch ||
    releaseManifest.platform?.binary !== packageManifest.binary ||
    releaseManifest.package?.directory !== "cliff" ||
    releaseManifest.package?.sizeBytes !== packageManifest.packageSizeBytes ||
    releaseManifest.package?.manifestSHA256 !== packageManifest.manifestSHA256 ||
    releaseManifest.archive?.file !== path.basename(archivePath) ||
    releaseManifest.archive?.sizeBytes !== archiveInfo.size ||
    !/^[a-f0-9]{64}$/.test(releaseManifest.archive?.sha256 ?? "") ||
    releaseManifest.installers?.bootstrap?.windows?.file !== "install.ps1" ||
    releaseManifest.installers?.bootstrap?.unix?.file !== "install.sh" ||
    releaseManifest.installers?.package?.windows?.file !== "install-package.ps1" ||
    releaseManifest.installers?.package?.unix?.file !== "install-package.sh" ||
    !/^[a-f0-9]{64}$/.test(releaseManifest.installers?.bootstrap?.windows?.sha256 ?? "") ||
    !/^[a-f0-9]{64}$/.test(releaseManifest.installers?.bootstrap?.unix?.sha256 ?? "") ||
    !/^[a-f0-9]{64}$/.test(releaseManifest.installers?.package?.windows?.sha256 ?? "") ||
    !/^[a-f0-9]{64}$/.test(releaseManifest.installers?.package?.unix?.sha256 ?? "") ||
    releaseManifest.commands?.install?.windows !== "irm getcliff.dev/install.ps1 | iex" ||
    releaseManifest.commands?.install?.unix !== "curl -fsSL getcliff.dev/install.sh | sh" ||
    !releaseManifest.commands?.run?.windows ||
    !releaseManifest.commands?.run?.unix ||
    !releaseManifest.commands?.status?.windows ||
    !releaseManifest.commands?.stop?.unix
  ) {
    throw new Error(`packaged release manifest is invalid: ${JSON.stringify(releaseManifest)}`);
  }
  await smokeExtractedArchive(archivePath);
  await smokePackagedRunnerScripts();
  const versionOutput = capture(binary, ["--version"]);
  if (!versionOutput.includes(build.version) || !versionOutput.includes(build.commit)) {
    throw new Error(`packaged binary version output did not include build metadata: ${versionOutput}`);
  }

  await mkdir(dataDir, { recursive: true });
  await mkdir(serverRoot, { recursive: true });

  const port = await getFreePort();
  const baseUrl = `http://127.0.0.1:${port}`;
  const logs = [];
  child = spawn(binary, ["--host", "127.0.0.1", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot, "--web-dir", webDir], {
    cwd: packageDir,
    env: { ...process.env },
    stdio: ["ignore", "pipe", "pipe"],
  });
  child.stdout.on("data", (chunk) => logs.push(chunk.toString()));
  child.stderr.on("data", (chunk) => logs.push(chunk.toString()));

  const health = await waitForHealth(baseUrl, child, logs);
  if (health.build?.version !== build.version || health.build?.commit !== build.commit) {
    throw new Error(`packaged daemon health did not include build metadata: ${JSON.stringify(health)}`);
  }
  if (!health.startedAt || !Number.isFinite(health.uptimeSeconds)) {
    throw new Error(`packaged daemon health did not include runtime metadata: ${JSON.stringify(health)}`);
  }
  if (
    !Number.isFinite(health.self?.pid) ||
    !Number.isFinite(health.self?.goroutines) ||
    !Number.isFinite(health.self?.heapAllocBytes) ||
    !Number.isFinite(health.self?.heapSysBytes) ||
    health.self.pid <= 0 ||
    health.self.heapAllocBytes <= 0
  ) {
    throw new Error(`packaged daemon health did not include self metrics: ${JSON.stringify(health)}`);
  }
  if (health.self.goroutines > GOROUTINE_BUDGET) {
    throw new Error(`packaged daemon exceeds ${GOROUTINE_BUDGET} goroutine budget: ${health.self.goroutines}`);
  }
  if (health.self.heapAllocBytes > HEAP_ALLOC_BUDGET_BYTES) {
    throw new Error(`packaged daemon exceeds ${formatBytes(HEAP_ALLOC_BUDGET_BYTES)} allocated heap budget: ${formatBytes(health.self.heapAllocBytes)}`);
  }
  if (health.self.heapSysBytes > HEAP_SYS_BUDGET_BYTES) {
    throw new Error(`packaged daemon exceeds ${formatBytes(HEAP_SYS_BUDGET_BYTES)} reserved heap budget: ${formatBytes(health.self.heapSysBytes)}`);
  }
  await wait(1100);
  const secondHealth = await request(baseUrl, "/api/health");
  if (secondHealth.json?.startedAt !== health.startedAt || secondHealth.json?.uptimeSeconds < health.uptimeSeconds) {
    throw new Error(`packaged daemon health runtime metadata was not stable: ${JSON.stringify({ first: health, second: secondHealth.json })}`);
  }

  const fallback = await request(baseUrl, "/servers/srv_packaged/overview");
  if (!fallback.response.ok || !fallback.text.includes("Loading dashboard")) {
    throw new Error(`packaged SPA fallback failed: ${fallback.response.status} ${fallback.text.slice(0, 200)}`);
  }

  const setupState = await request(baseUrl, "/api/auth/me");
  if (!setupState.response.ok || setupState.json?.needsSetup !== true) {
    throw new Error(`packaged daemon auth setup state failed: ${setupState.text}`);
  }

  console.log("Daemon package smoke test passed");
} finally {
  await stopProcess(child);
  killPathDaemons(tempRoot);
  killPathDaemons(packageDir);
  await rmWithRetry(distDir);
  await rmWithRetry(tempRoot);
}

function formatBytes(value) {
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

async function rmWithRetry(targetPath) {
  let lastError = null;
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      await rm(targetPath, { recursive: true, force: true });
      return;
    } catch (error) {
      lastError = error;
      await wait(500);
    }
  }
  throw lastError;
}

function killPathDaemons(targetPath) {
  if (process.platform !== "win32") return;
  const escaped = targetPath.replaceAll("'", "''").toLowerCase();
  spawnSync(
    "powershell.exe",
    [
      "-NoProfile",
      "-Command",
      `Get-CimInstance Win32_Process -Filter "Name = 'cliff.exe'" | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.ToLower().StartsWith('${escaped}') } | ForEach-Object { taskkill.exe /PID $_.ProcessId /T /F | Out-Null }`,
    ],
    { stdio: "ignore", windowsHide: true },
  );
}
