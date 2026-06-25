import { cp, mkdir, mkdtemp, readdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const webDir = path.join(root, "daemon", "web");
const outDir = path.join(root, "out");
const nextDir = path.join(root, ".next");
const lockFile = path.join(root, ".daemon-static-export.lock");
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-daemon-web-smoke-"));
const webBackup = path.join(tempRoot, "web-backup");
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

async function assertFile(filePath, label) {
  const info = await stat(filePath).catch(() => null);
  if (!info?.isFile()) throw new Error(`${label} was not generated at ${path.relative(root, filePath)}`);
}

async function assertDirectory(dirPath, label) {
  const info = await stat(dirPath).catch(() => null);
  if (!info?.isDirectory()) throw new Error(`${label} was not generated at ${path.relative(root, dirPath)}`);
}

async function assertMissing(dirPath, label) {
  const info = await stat(dirPath).catch(() => null);
  if (info) throw new Error(`${label} should not exist at ${path.relative(root, dirPath)}`);
}

async function readBundleText(dirPath) {
  const entries = await readdir(dirPath, { withFileTypes: true }).catch(() => []);
  const chunks = [];
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      chunks.push(await readBundleText(entryPath));
    } else if (entry.name.endsWith(".js")) {
      chunks.push(await readFile(entryPath, "utf8"));
    }
  }
  return chunks.join("\n");
}

async function directorySize(dirPath) {
  let total = 0;
  const entries = await readdir(dirPath, { withFileTypes: true }).catch(() => []);
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) total += await directorySize(entryPath);
    else if (entry.isFile()) total += (await stat(entryPath)).size;
  }
  return total;
}

async function directoryExtensionSize(dirPath, extension) {
  let total = 0;
  const entries = await readdir(dirPath, { withFileTypes: true }).catch(() => []);
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) total += await directoryExtensionSize(entryPath, extension);
    else if (entry.isFile() && entry.name.endsWith(extension)) total += (await stat(entryPath)).size;
  }
  return total;
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

function readSizeBudget(name, fallback) {
  const value = Number(process.env[name] || "");
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

try {
  await mkdir(tempRoot, { recursive: true });
  if (existsSync(webDir)) await cp(webDir, webBackup, { recursive: true });
  await writeFile(lockFile, `${JSON.stringify({ pid: 0, createdAt: "2000-01-01T00:00:00.000Z" })}\n`);

  run("npm", ["run", "build:daemon-web"]);

  await assertFile(path.join(webDir, "index.html"), "daemon web index");
  await assertFile(path.join(webDir, "STATIC_WEB_README.txt"), "daemon web marker");
  await assertDirectory(path.join(webDir, "_next"), "daemon web Next assets");
  for (const asset of requiredPublicAssets) {
    await assertFile(path.join(webDir, asset), `daemon web public asset ${asset}`);
  }
  await assertMissing(path.join(root, "src", "app", "api"), "daemon-only API route source");
  await assertDirectory(path.join(root, "src", "app", "servers"), "dynamic server route source after static export");
  const bundleText = await readBundleText(path.join(webDir, "_next"));
  if (!bundleText.includes("healthFor") || !bundleText.includes("usageFor")) {
    throw new Error("daemon static bundle does not include scoped overview health/usage query parameters");
  }
  if (bundleText.includes("EventSource") || bundleText.includes("/events")) {
    throw new Error("daemon static bundle includes legacy SSE console/runtime paths");
  }
  const webSizeBytes = await directorySize(webDir);
  if (webSizeBytes > webSizeBudgetBytes) {
    throw new Error(`daemon static web assets exceed ${formatBytes(webSizeBudgetBytes)} budget: ${formatBytes(webSizeBytes)}`);
  }
  const webJavaScriptBytes = await directoryExtensionSize(webDir, ".js");
  if (webJavaScriptBytes > webJavaScriptBudgetBytes) {
    throw new Error(`daemon static JavaScript exceeds ${formatBytes(webJavaScriptBudgetBytes)} budget: ${formatBytes(webJavaScriptBytes)}`);
  }

  console.log(`Daemon web smoke test passed (${formatBytes(webSizeBytes)}, ${formatBytes(webJavaScriptBytes)} JS)`);
} finally {
  await rm(webDir, { recursive: true, force: true });
  if (existsSync(webBackup)) {
    await mkdir(path.dirname(webDir), { recursive: true });
    await cp(webBackup, webDir, { recursive: true });
  }
  await rm(outDir, { recursive: true, force: true });
  await rm(nextDir, { recursive: true, force: true });
  await rm(lockFile, { force: true });
  await rm(tempRoot, { recursive: true, force: true });
}
