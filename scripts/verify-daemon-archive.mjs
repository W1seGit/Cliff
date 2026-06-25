import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import AdmZip from "adm-zip";

const root = process.cwd();
const archivePath = path.resolve(readArg("--archive") || (await defaultArchivePath()));
const checksumPath = `${archivePath}.sha256`;
const metadataPath = `${archivePath}.json`;
const archiveSizeBudgetBytes = readSizeBudget("CLIFF_ARCHIVE_SIZE_BUDGET_BYTES", 50 * 1024 * 1024);
const webSizeBudgetBytes = readSizeBudget("CLIFF_WEB_SIZE_BUDGET_BYTES", 8 * 1024 * 1024);
const webJavaScriptBudgetBytes = readSizeBudget("CLIFF_WEB_JS_SIZE_BUDGET_BYTES", 4 * 1024 * 1024);
const requiredPublicAssets = [
  "app-sw.js",
  "icon.svg",
  "apple-icon.svg",
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

async function defaultArchivePath() {
  const distDir = path.join(root, "dist");
  const entries = await readdir(distDir).catch(() => []);
  const archives = entries.filter((entry) => /^cliff-.+\.zip$/.test(entry)).sort();
  return archives.length > 0 ? path.join(distDir, archives.at(-1)) : path.join(distDir, "cliff.zip");
}

async function fileSHA256(filePath) {
  const data = await readFile(filePath);
  return createHash("sha256").update(data).digest("hex");
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

if (!existsSync(archivePath)) fail(`Daemon archive not found: ${archivePath}`);
if (!existsSync(checksumPath)) fail(`Daemon archive checksum not found: ${checksumPath}`);
if (!existsSync(metadataPath)) fail(`Daemon archive metadata not found: ${metadataPath}`);

const archiveName = path.basename(archivePath);
const checksumText = readFileSync(checksumPath, "utf8").trim();
const checksumParts = checksumText.split(/\s+/);
const expectedHash = checksumParts[0] || "";
const checksumName = checksumParts.at(-1) || "";
const actualHash = await fileSHA256(archivePath);
const archiveInfo = await stat(archivePath);
const metadata = JSON.parse(readFileSync(metadataPath, "utf8"));

if (!/^[a-f0-9]{64}$/.test(expectedHash)) fail(`Invalid archive checksum file: ${checksumText}`);
if (checksumName !== archiveName) fail(`Archive checksum references ${checksumName}, expected ${archiveName}.`);
if (expectedHash !== actualHash) fail("Archive SHA-256 does not match checksum file.");
if (metadata.archive !== archiveName) fail(`Archive metadata references ${metadata.archive}, expected ${archiveName}.`);
if (metadata.archiveSHA256 !== actualHash) fail("Archive SHA-256 does not match archive metadata.");
if (metadata.archiveSizeBytes !== archiveInfo.size) fail("Archive size does not match archive metadata.");
if (metadata.archiveSizeBytes > archiveSizeBudgetBytes) {
  fail(`Archive exceeds ${formatBytes(archiveSizeBudgetBytes)} budget: ${formatBytes(metadata.archiveSizeBytes)}.`);
}

const zip = new AdmZip(archivePath);
const zipEntries = zip.getEntries();
const entries = new Set(zipEntries.map((entry) => entry.entryName.replaceAll("\\", "/")));
const manifestEntry = zip.getEntry("cliff/package-manifest.json");
const readmeEntry = zip.getEntry("cliff/README.txt");
const runPowerShellEntry = zip.getEntry("cliff/run.ps1");
const runShellEntry = zip.getEntry("cliff/run.sh");
const statusPowerShellEntry = zip.getEntry("cliff/status.ps1");
const statusShellEntry = zip.getEntry("cliff/status.sh");
const stopPowerShellEntry = zip.getEntry("cliff/stop.ps1");
const stopShellEntry = zip.getEntry("cliff/stop.sh");
const requiredEntries = [
  "cliff/README.txt",
  "cliff/build.json",
  "cliff/package-manifest.json",
  "cliff/run.ps1",
  "cliff/run.sh",
  "cliff/status.ps1",
  "cliff/status.sh",
  "cliff/stop.ps1",
  "cliff/stop.sh",
  "cliff/web/index.html",
];
for (const entry of requiredEntries) {
  if (!entries.has(entry)) fail(`Archive is missing ${entry}.`);
}
for (const asset of requiredPublicAssets) {
  const archiveEntry = `cliff/web/${asset}`;
  if (!entries.has(archiveEntry)) fail(`Archive web is missing public asset: ${asset}.`);
}
if (!manifestEntry) fail("Archive is missing cliff/package-manifest.json.");
if (!readmeEntry) fail("Archive is missing cliff/README.txt.");
if (!runPowerShellEntry) fail("Archive is missing cliff/run.ps1.");
if (!runShellEntry) fail("Archive is missing cliff/run.sh.");
if (!statusPowerShellEntry) fail("Archive is missing cliff/status.ps1.");
if (!statusShellEntry) fail("Archive is missing cliff/status.sh.");
if (!stopPowerShellEntry) fail("Archive is missing cliff/stop.ps1.");
if (!stopShellEntry) fail("Archive is missing cliff/stop.sh.");

const packageManifest = JSON.parse(manifestEntry.getData().toString("utf8"));
if (packageManifest.webSizeBytes > webSizeBudgetBytes) {
  fail(`Archive web assets exceed ${formatBytes(webSizeBudgetBytes)} budget: ${formatBytes(packageManifest.webSizeBytes)}.`);
}
const binaryName = packageManifest.binary || (process.platform === "win32" ? "cliff.exe" : "cliff");
if (!entries.has(`cliff/${binaryName}`)) fail(`Archive is missing cliff/${binaryName}.`);
const allowedRootEntries = new Set([
  binaryName,
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
const unexpectedEntries = [];
for (const entry of entries) {
  if (!entry.startsWith("cliff/")) {
    unexpectedEntries.push(entry);
    continue;
  }
  const relative = entry.slice("cliff/".length);
  if (!relative) continue;
  const rootEntry = relative.split("/")[0];
  if (!allowedRootEntries.has(rootEntry)) {
    unexpectedEntries.push(entry);
  }
}
if (unexpectedEntries.length > 0) {
  fail(`Archive contains unexpected entries: ${unexpectedEntries.sort().join(", ")}`);
}
const leakedRuntimeEntries = [];
for (const entry of entries) {
  const relative = entry.startsWith("cliff/") ? entry.slice("cliff/".length) : entry;
  if (!relative) continue;
  const segments = relative.toLowerCase().split("/");
  const basename = segments.at(-1) ?? "";
  if (
    segments.includes("node_modules") ||
    segments.includes(".next") ||
    segments.includes("src") ||
    segments.includes("scripts") ||
    segments.includes("daemon") ||
    basename === "next.config.ts" ||
    basename === "next.config.js" ||
    basename === "package-lock.json"
  ) {
    leakedRuntimeEntries.push(entry);
  }
}
if (leakedRuntimeEntries.length > 0) {
  fail(`Archive contains Node/Next/source runtime files: ${leakedRuntimeEntries.sort().join(", ")}`);
}
const webBundleText = zipEntries
  .filter((entry) => {
    const name = entry.entryName.replaceAll("\\", "/");
    return !entry.isDirectory && name.startsWith("cliff/web/") && name.endsWith(".js");
  })
  .map((entry) => entry.getData().toString("utf8"))
  .join("\n");
if (webBundleText.includes("EventSource") || webBundleText.includes("/events")) {
  fail("Archive web assets contain legacy SSE console/runtime paths.");
}
if (!webBundleText.includes("Console connection is not ready") || !webBundleText.includes("onError")) {
  fail("Archive web assets do not include console WebSocket error handling.");
}

const packageReadme = readmeEntry.getData().toString("utf8");
if (!packageReadme.includes("--server-root servers")) {
  fail("Archive README does not document the server root daemon flag.");
}
if (!packageReadme.includes("curl -fsSL getcliff.dev/install.sh | sh") || !packageReadme.includes("irm getcliff.dev/install.ps1 | iex")) {
  fail("Archive README does not document one-line release install commands.");
}
if (!packageReadme.includes("does not require Node.js, Go, or a Next.js server")) {
  fail("Archive README does not document that extracted package usage is self-contained.");
}
const runPowerShell = runPowerShellEntry.getData().toString("utf8");
if (!runPowerShell.includes("/api/health") || !runPowerShell.includes("cliff.json") || !runPowerShell.includes("health = $Health")) {
  fail("Archive run.ps1 does not persist daemon health metadata before reporting startup state.");
}
if (!runPowerShell.includes("--log-file") || !runPowerShell.includes("--server-root")) {
  fail("Archive run.ps1 does not pass daemon log and server-root flags.");
}
if (!runPowerShell.includes("daemonCommand")) {
  fail("Archive run.ps1 does not persist daemon command metadata.");
}
const runShell = runShellEntry.getData().toString("utf8");
if (!runShell.includes("/api/health") || !runShell.includes("cliff.json") || !runShell.includes('"health": $health_json')) {
  fail("Archive run.sh does not persist daemon health metadata before reporting startup state.");
}
if (!runShell.includes("--log-file") || !runShell.includes("--server-root")) {
  fail("Archive run.sh does not pass daemon log and server-root flags.");
}
if (!runShell.includes('"daemonCommand"')) {
  fail("Archive run.sh does not persist daemon command metadata.");
}
if (!runShell.includes("json_string()") || !runShell.includes("DAEMON_COMMAND_JSON")) {
  fail("Archive run.sh does not escape JSON state string fields before writing cliff.json.");
}
const statusPowerShell = statusPowerShellEntry.getData().toString("utf8");
if (!statusPowerShell.includes("/api/health") || !statusPowerShell.includes("cliff.json")) {
  fail("Archive status.ps1 does not read daemon health and state metadata.");
}
if (!statusPowerShell.includes("Daemon heap:") || !statusPowerShell.includes("Daemon memory:") || !statusPowerShell.includes("Daemon goroutines:") || !statusPowerShell.includes("heapSysBytes") || !statusPowerShell.includes("WorkingSet64")) {
  fail("Archive status.ps1 does not report daemon heap, memory, and goroutine metrics.");
}
if (!statusPowerShell.includes("Command:") || !statusPowerShell.includes("Logs:") || !statusPowerShell.includes("Errors:")) {
  fail("Archive status.ps1 does not report daemon command and log paths.");
}
if (!statusPowerShell.includes("Test-cliffProcess") || !statusPowerShell.includes("Removed stale state")) {
  fail("Archive status.ps1 does not validate PID ownership and stale state.");
}
const statusShell = statusShellEntry.getData().toString("utf8");
if (!statusShell.includes("/api/health") || !statusShell.includes("cliff.json")) {
  fail("Archive status.sh does not read daemon health and state metadata.");
}
if (!statusShell.includes("Daemon heap:") || !statusShell.includes("Daemon memory:") || !statusShell.includes("Daemon goroutines:") || !statusShell.includes("heapSysBytes") || !statusShell.includes("ps -o rss=")) {
  fail("Archive status.sh does not report daemon heap, memory, and goroutine metrics.");
}
if (!statusShell.includes("Command:") || !statusShell.includes("Logs:") || !statusShell.includes("Errors:")) {
  fail("Archive status.sh does not report daemon command and log paths.");
}
if (!statusShell.includes("looks_like_daemon") || !statusShell.includes("Removed stale state")) {
  fail("Archive status.sh does not validate PID ownership and stale state.");
}
const stopPowerShell = stopPowerShellEntry.getData().toString("utf8");
if (!stopPowerShell.includes("Test-cliffProcess") || !stopPowerShell.includes("cliff.json") || !stopPowerShell.includes("Removed stale state")) {
  fail("Archive stop.ps1 does not validate PID ownership and clear stale state.");
}
const stopShell = stopShellEntry.getData().toString("utf8");
if (!stopShell.includes("looks_like_daemon") || !stopShell.includes("cliff.json") || !stopShell.includes("Removed stale state")) {
  fail("Archive stop.sh does not validate PID ownership and clear stale state.");
}

const webJavaScriptSize = zipEntries
  .filter((entry) => {
    const name = entry.entryName.replaceAll("\\", "/");
    return !entry.isDirectory && name.startsWith("cliff/web/") && name.endsWith(".js");
  })
  .reduce((total, entry) => total + entry.getData().length, 0);
if (webJavaScriptSize > webJavaScriptBudgetBytes) {
  fail(`Archive web JavaScript exceeds ${formatBytes(webJavaScriptBudgetBytes)} budget: ${formatBytes(webJavaScriptSize)}.`);
}
if (metadata.packageSizeBytes !== packageManifest.packageSizeBytes) fail("Archive package size does not match package manifest.");
if (metadata.packageManifestSHA256 !== packageManifest.manifestSHA256) fail("Archive package manifest hash does not match package manifest.");

console.log(`Archive verified: ${path.relative(root, archivePath) || archivePath}`);
console.log(`Version: ${metadata.version} (${metadata.commit})`);
console.log(`Archive size: ${formatBytes(metadata.archiveSizeBytes)}`);
