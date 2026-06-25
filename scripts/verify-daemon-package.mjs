import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";

const root = process.cwd();
const packageDir = path.resolve(readArg("--package-dir") || path.join(root, "dist", "cliff"));
const manifestPath = path.join(packageDir, "package-manifest.json");
const packageSizeBudgetBytes = readSizeBudget("CLIFF_PACKAGE_SIZE_BUDGET_BYTES", 100 * 1024 * 1024);
const webSizeBudgetBytes = readSizeBudget("CLIFF_WEB_SIZE_BUDGET_BYTES", 8 * 1024 * 1024);
const webJavaScriptBudgetBytes = readSizeBudget("CLIFF_WEB_JS_SIZE_BUDGET_BYTES", 4 * 1024 * 1024);
const requiredPublicAssets = [
  "app-sw.js",
  "assets/logos/fabric.png",
  "assets/logos/forge.svg",
  "assets/logos/localtonet.ico",
  "assets/logos/minecraft.svg",
  "assets/logos/minekube.png",
  "assets/logos/neoforge.png",
  "assets/logos/papermc.svg",
  "assets/logos/playit.png",
  "assets/logos/steve-head.svg",
];

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

async function fileSHA256(filePath) {
  const data = await readFile(filePath);
  return createHash("sha256").update(data).digest("hex");
}

async function directorySHA256(dirPath) {
  const hash = createHash("sha256");
  const files = await directoryFiles(dirPath);
  for (const filePath of files) {
    const relative = path.relative(dirPath, filePath).replaceAll(path.sep, "/");
    hash.update(relative);
    hash.update("\0");
    hash.update(await readFile(filePath));
    hash.update("\0");
  }
  return hash.digest("hex");
}

async function directoryFiles(dirPath) {
  const files = [];
  const entries = await readdir(dirPath, { withFileTypes: true }).catch(() => []);
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) files.push(...(await directoryFiles(entryPath)));
    else if (entry.isFile()) files.push(entryPath);
  }
  return files.sort((left, right) => left.localeCompare(right));
}

async function fileSize(filePath) {
  const info = await stat(filePath);
  return info.size;
}

async function directorySize(dirPath, options = {}) {
  let total = 0;
  const entries = await readdir(dirPath, { withFileTypes: true }).catch(() => []);
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    const relative = path.relative(options.root ?? dirPath, entryPath).replaceAll(path.sep, "/");
    if (options.exclude?.has(relative)) continue;
    if (entry.isDirectory()) total += await directorySize(entryPath, { ...options, root: options.root ?? dirPath });
    else if (entry.isFile()) total += await fileSize(entryPath);
  }
  return total;
}

async function directoryExtensionSize(dirPath, extension) {
  let total = 0;
  const entries = await readdir(dirPath, { withFileTypes: true }).catch(() => []);
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) total += await directoryExtensionSize(entryPath, extension);
    else if (entry.isFile() && entry.name.endsWith(extension)) total += await fileSize(entryPath);
  }
  return total;
}

function manifestSHA256(manifest) {
  return createHash("sha256")
    .update(
      JSON.stringify({
        version: manifest.version,
        commit: manifest.commit,
        builtAt: manifest.builtAt,
        binary: manifest.binary,
        binarySizeBytes: manifest.binarySizeBytes,
        binarySHA256: manifest.binarySHA256,
        webSizeBytes: manifest.webSizeBytes,
        webSHA256: manifest.webSHA256,
        dataSizeBytes: manifest.dataSizeBytes,
        serverSizeBytes: manifest.serverSizeBytes,
        packageSizeBytes: manifest.packageSizeBytes,
      }),
    )
    .digest("hex");
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

function fail(message) {
  console.error(message);
  process.exit(1);
}

function readSizeBudget(name, fallback) {
  const value = Number(process.env[name] || "");
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

if (!existsSync(manifestPath)) fail(`Package manifest not found: ${manifestPath}`);

const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
const binaryPath = path.join(packageDir, manifest.binary || (process.platform === "win32" ? "cliff.exe" : "cliff"));
const webDir = path.join(packageDir, "web");
const dataDir = path.join(packageDir, "data");
const serversDir = path.join(packageDir, "servers");
const runPowerShellPath = path.join(packageDir, "run.ps1");
const runShellPath = path.join(packageDir, "run.sh");
const statusPowerShellPath = path.join(packageDir, "status.ps1");
const statusShellPath = path.join(packageDir, "status.sh");
const stopPowerShellPath = path.join(packageDir, "stop.ps1");
const stopShellPath = path.join(packageDir, "stop.sh");
const packageReadmePath = path.join(packageDir, "README.txt");
const allowedRootEntries = new Set([
  manifest.binary || (process.platform === "win32" ? "cliff.exe" : "cliff"),
  "build.json",
  "data",
  "package-manifest.json",
  "README.txt",
  "run.ps1",
  "run.sh",
  "servers",
  "status.ps1",
  "status.sh",
  "stop.ps1",
  "stop.sh",
  "web",
]);
const packageRootEntries = await readdir(packageDir, { withFileTypes: true });
const unexpectedRootEntries = packageRootEntries.map((entry) => entry.name).filter((name) => !allowedRootEntries.has(name));
if (unexpectedRootEntries.length > 0) {
  fail(`Package root contains unexpected entries: ${unexpectedRootEntries.sort().join(", ")}`);
}

if (!existsSync(binaryPath)) fail(`Package binary not found: ${binaryPath}`);
if (!existsSync(webDir)) fail(`Package web directory not found: ${webDir}`);
if (!existsSync(runPowerShellPath)) fail(`Package Windows runner not found: ${runPowerShellPath}`);
if (!existsSync(runShellPath)) fail(`Package shell runner not found: ${runShellPath}`);
if (!existsSync(statusPowerShellPath)) fail(`Package Windows status script not found: ${statusPowerShellPath}`);
if (!existsSync(statusShellPath)) fail(`Package shell status script not found: ${statusShellPath}`);
if (!existsSync(stopPowerShellPath)) fail(`Package Windows stop script not found: ${stopPowerShellPath}`);
if (!existsSync(stopShellPath)) fail(`Package shell stop script not found: ${stopShellPath}`);
for (const asset of requiredPublicAssets) {
  if (!existsSync(path.join(webDir, asset))) fail(`Package web is missing public asset: ${asset}`);
}

const actualBinarySize = await fileSize(binaryPath);
const actualWebSize = await directorySize(webDir);
const actualDataSize = await directorySize(dataDir);
const actualServerSize = await directorySize(serversDir);
const actualPackageSize = await directorySize(packageDir, { exclude: new Set(["package-manifest.json"]) });
const actualWebJavaScriptSize = await directoryExtensionSize(webDir, ".js");
const actualBinaryHash = await fileSHA256(binaryPath);
const actualWebHash = await directorySHA256(webDir);
const actualManifestHash = manifestSHA256(manifest);
const webFiles = await directoryFiles(webDir);
const webBundleText = (
  await Promise.all(
    webFiles
      .filter((filePath) => filePath.endsWith(".js"))
      .map((filePath) => readFile(filePath, "utf8")),
  )
).join("\n");
const leakedSourceFiles = webFiles
  .map((filePath) => path.relative(webDir, filePath).replaceAll(path.sep, "/"))
  .filter((relative) => {
    const normalized = relative.toLowerCase();
    return (
      normalized.startsWith("src/") ||
      normalized.includes("/src/") ||
      normalized.startsWith("app/api/") ||
      normalized.includes("/app/api/") ||
      normalized.startsWith(".daemon-static-export-api/") ||
      normalized.includes("/.daemon-static-export-api/") ||
      normalized.endsWith("/route.ts") ||
      normalized.endsWith("/route.tsx") ||
      normalized.endsWith("/route.js")
    );
  });
if (leakedSourceFiles.length > 0) {
  fail(`Package web assets contain source/API route files: ${leakedSourceFiles.sort().join(", ")}`);
}
if (webBundleText.includes("EventSource") || webBundleText.includes("/events")) {
  fail("Package web assets contain legacy SSE console/runtime paths.");
}
if (!webBundleText.includes("Console connection is not ready") || !webBundleText.includes("onError")) {
  fail("Package web assets do not include console WebSocket error handling.");
}

const packageFiles = await directoryFiles(packageDir);
const leakedRuntimeFiles = packageFiles
  .map((filePath) => path.relative(packageDir, filePath).replaceAll(path.sep, "/"))
  .filter((relative) => {
    const segments = relative.toLowerCase().split("/");
    const basename = segments.at(-1) ?? "";
    return (
      segments.includes("node_modules") ||
      segments.includes(".next") ||
      segments.includes("src") ||
      segments.includes("scripts") ||
      segments.includes("daemon") ||
      basename === "next.config.ts" ||
      basename === "next.config.js" ||
      basename === "package-lock.json"
    );
  });
if (leakedRuntimeFiles.length > 0) {
  fail(`Package contains Node/Next/source runtime files: ${leakedRuntimeFiles.sort().join(", ")}`);
}

if (manifest.binarySizeBytes !== actualBinarySize) fail("Package binary size does not match package-manifest.json.");
if (manifest.webSizeBytes !== actualWebSize) fail("Package web size does not match package-manifest.json.");
if (manifest.webSizeBytes > webSizeBudgetBytes) {
  fail(`Package web assets exceed ${formatBytes(webSizeBudgetBytes)} budget: ${formatBytes(manifest.webSizeBytes)}.`);
}
if (actualWebJavaScriptSize > webJavaScriptBudgetBytes) {
  fail(`Package web JavaScript exceeds ${formatBytes(webJavaScriptBudgetBytes)} budget: ${formatBytes(actualWebJavaScriptSize)}.`);
}
if (manifest.dataSizeBytes !== actualDataSize) fail("Package data size does not match package-manifest.json.");
if (manifest.serverSizeBytes !== actualServerSize) fail("Package server size does not match package-manifest.json.");
if (manifest.packageSizeBytes !== actualPackageSize) fail("Package total size does not match package-manifest.json.");
if (manifest.packageSizeBytes > packageSizeBudgetBytes) {
  fail(`Package exceeds ${formatBytes(packageSizeBudgetBytes)} budget: ${formatBytes(manifest.packageSizeBytes)}.`);
}
if (manifest.binarySHA256 !== actualBinaryHash) fail("Package binary SHA-256 does not match package-manifest.json.");
if (manifest.webSHA256 !== actualWebHash) fail("Package web SHA-256 does not match package-manifest.json.");
if (manifest.manifestSHA256 !== actualManifestHash) fail("Package manifest SHA-256 does not match package-manifest.json.");

const packageReadme = readFileSync(packageReadmePath, "utf8");
if (!packageReadme.includes("--server-root servers")) {
  fail("Package README does not document the server root daemon flag.");
}
if (!packageReadme.includes("curl -fsSL getcliff.dev/install.sh | sh") || !packageReadme.includes("irm getcliff.dev/install.ps1 | iex")) {
  fail("Package README does not document one-line release install commands.");
}
if (!packageReadme.includes("does not require Node.js, Go, or a Next.js server")) {
  fail("Package README does not document that extracted package usage is self-contained.");
}

const runPowerShell = readFileSync(runPowerShellPath, "utf8");
if (!runPowerShell.includes("/api/health") || !runPowerShell.includes("cliff.json") || !runPowerShell.includes("health = $Health")) {
  fail("run.ps1 does not persist daemon health metadata before reporting startup state.");
}
if (!runPowerShell.includes("--log-file")) {
  fail("run.ps1 does not pass a daemon log file path.");
}
if (!runPowerShell.includes("--server-root")) {
  fail("run.ps1 does not pass the server root as a daemon flag.");
}
if (!runPowerShell.includes("daemonCommand")) {
  fail("run.ps1 does not persist daemon command metadata.");
}
if (runPowerShell.includes("Cliff is already running.") || !runPowerShell.includes("Removing stale daemon state")) {
  fail("run.ps1 does not verify daemon health before reusing an existing PID.");
}
if (!runPowerShell.includes("Test-cliffProcess")) {
  fail("run.ps1 does not validate PID ownership before reusing daemon state.");
}
const runShell = readFileSync(runShellPath, "utf8");
if (!runShell.includes("/api/health") || !runShell.includes("cliff.json") || !runShell.includes('"health": $health_json')) {
  fail("run.sh does not persist daemon health metadata before reporting startup state.");
}
if (!runShell.includes("--log-file")) {
  fail("run.sh does not pass a daemon log file path.");
}
if (!runShell.includes("--server-root")) {
  fail("run.sh does not pass the server root as a daemon flag.");
}
if (!runShell.includes('"daemonCommand"')) {
  fail("run.sh does not persist daemon command metadata.");
}
if (!runShell.includes("json_string()") || !runShell.includes("DAEMON_COMMAND_JSON")) {
  fail("run.sh does not escape JSON state string fields before writing cliff.json.");
}
if (!runShell.includes("collect_lan_urls_json") || !runShell.includes('"lanUrls": $LAN_URLS_JSON') || !runShell.includes("print_lan_urls")) {
  fail("run.sh does not persist and print same-network LAN URL metadata consistently.");
}
if (runShell.includes("Cliff is already running.") || !runShell.includes("Removing stale daemon state")) {
  fail("run.sh does not verify daemon health before reusing an existing PID.");
}
if (!runShell.includes("looks_like_daemon")) {
  fail("run.sh does not validate PID ownership before reusing daemon state.");
}
const statusPowerShell = readFileSync(statusPowerShellPath, "utf8");
if (!statusPowerShell.includes("/api/health") || !statusPowerShell.includes("cliff.json")) {
  fail("status.ps1 does not read daemon health and state metadata.");
}
if (!statusPowerShell.includes("Daemon heap:") || !statusPowerShell.includes("Daemon memory:") || !statusPowerShell.includes("Daemon goroutines:") || !statusPowerShell.includes("heapSysBytes") || !statusPowerShell.includes("WorkingSet64")) {
  fail("status.ps1 does not report daemon heap, memory, and goroutine metrics.");
}
if (!statusPowerShell.includes("Command:") || !statusPowerShell.includes("Logs:") || !statusPowerShell.includes("Errors:")) {
  fail("status.ps1 does not report daemon command and log paths.");
}
if (!statusPowerShell.includes("Test-cliffProcess") || !statusPowerShell.includes("Removed stale state")) {
  fail("status.ps1 does not validate PID ownership and stale state.");
}
const statusShell = readFileSync(statusShellPath, "utf8");
if (!statusShell.includes("/api/health") || !statusShell.includes("cliff.json")) {
  fail("status.sh does not read daemon health and state metadata.");
}
if (!statusShell.includes("Daemon heap:") || !statusShell.includes("Daemon memory:") || !statusShell.includes("Daemon goroutines:") || !statusShell.includes("heapSysBytes") || !statusShell.includes("ps -o rss=")) {
  fail("status.sh does not report daemon heap, memory, and goroutine metrics.");
}
if (!statusShell.includes("Command:") || !statusShell.includes("Logs:") || !statusShell.includes("Errors:")) {
  fail("status.sh does not report daemon command and log paths.");
}
if (!statusShell.includes("looks_like_daemon") || !statusShell.includes("Removed stale state")) {
  fail("status.sh does not validate PID ownership and stale state.");
}
const stopPowerShell = readFileSync(stopPowerShellPath, "utf8");
if (!stopPowerShell.includes("Test-cliffProcess") || !stopPowerShell.includes("cliff.json") || !stopPowerShell.includes("Removed stale state")) {
  fail("stop.ps1 does not validate PID ownership and clear stale state.");
}
const stopShell = readFileSync(stopShellPath, "utf8");
if (!stopShell.includes("looks_like_daemon") || !stopShell.includes("cliff.json") || !stopShell.includes("Removed stale state")) {
  fail("stop.sh does not validate PID ownership and clear stale state.");
}

console.log(`Package verified: ${path.relative(root, packageDir) || packageDir}`);
console.log(`Version: ${manifest.version} (${manifest.commit})`);
console.log(`Size: ${formatBytes(manifest.packageSizeBytes)}`);
