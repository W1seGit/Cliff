import { cp, mkdir, open, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { spawn } from "node:child_process";
import path from "node:path";

const root = process.cwd();
const nextDir = path.join(root, ".next");
const outDir = path.join(root, "out");
const webDir = path.join(root, "daemon", "web");
const lockFile = path.join(root, ".daemon-static-export.lock");

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function processIsAlive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function run(command, args, env) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd: root, env: { ...process.env, ...env }, shell: process.platform === "win32", stdio: "inherit" });
    child.on("exit", (code) => {
      if (code === 0) resolve();
      else reject(new Error(`${command} ${args.join(" ")} exited with code ${code}`));
    });
    child.on("error", reject);
  });
}

async function acquireBuildLock() {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    try {
      const handle = await open(lockFile, "wx");
      await handle.writeFile(`${JSON.stringify({ pid: process.pid, createdAt: new Date().toISOString() })}\n`);
      await handle.close();
      return true;
    } catch (error) {
      if (error?.code !== "EEXIST") throw error;
      if (await removeStaleBuildLock()) continue;
      await wait(250);
    }
  }
  throw new Error("Timed out waiting for another static daemon web build to finish.");
}

async function removeStaleBuildLock() {
  const text = await readFile(lockFile, "utf8").catch(() => "");
  let lock = null;
  try {
    lock = JSON.parse(text);
  } catch {
    lock = { pid: Number(text.trim()), createdAt: "" };
  }
  const createdAt = lock?.createdAt ? Date.parse(lock.createdAt) : 0;
  const staleByAge = !Number.isFinite(createdAt) || createdAt <= 0 || Date.now() - createdAt > 10 * 60 * 1000;
  const staleByPid = !processIsAlive(Number(lock?.pid));
  if (!staleByAge && !staleByPid) return false;
  await rm(lockFile, { force: true });
  return true;
}

async function releaseBuildLock(acquired) {
  if (acquired) await rm(lockFile, { force: true });
}

async function copyIfExists(source, target) {
  if (!existsSync(source)) return false;
  await mkdir(path.dirname(target), { recursive: true });
  await cp(source, target, { recursive: true });
  return true;
}

// Remove Next.js static export artifacts that the daemon's SPA fallback
// never uses: RSC .txt metadata files, the _not-found standalone page,
// the no-op service worker, and the duplicate icon.svg (already in
// _next/static/media).
async function pruneStaticExportArtifacts(dir) {
  let removed = 0;
  let bytes = 0;

  async function walk(dirPath) {
    const entries = await readdir(dirPath, { withFileTypes: true });
    for (const entry of entries) {
      const fullPath = path.join(dirPath, entry.name);
      if (entry.isDirectory()) {
        // Remove _not-found/ directory entirely
        if (entry.name === "_not-found") {
          const size = await directorySize(fullPath);
          await rm(fullPath, { recursive: true, force: true });
          removed++;
          bytes += size;
          continue;
        }
        await walk(fullPath);
      } else if (entry.isFile()) {
        // Remove .txt RSC metadata files
        if (entry.name.endsWith(".txt")) {
          const s = await stat(fullPath);
          await rm(fullPath, { force: true });
          removed++;
          bytes += s.size;
          continue;
        }
        // Remove no-op service worker
        if (entry.name === "app-sw.js") {
          const s = await stat(fullPath);
          await rm(fullPath, { force: true });
          removed++;
          bytes += s.size;
          continue;
        }
        // Remove duplicate icon.svg at web root (already in _next/static/media/)
        if (entry.name === "icon.svg" && dirPath === dir) {
          const s = await stat(fullPath);
          await rm(fullPath, { force: true });
          removed++;
          bytes += s.size;
          continue;
        }
      }
    }
  }

  await walk(dir);
  if (removed > 0) {
    console.log(`Pruned ${removed} unused static export artifacts (${(bytes / 1024).toFixed(0)} KB) from ${path.relative(root, dir)}`);
  }
}

async function directorySize(dirPath) {
  let total = 0;
  const entries = await readdir(dirPath, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dirPath, entry.name);
    if (entry.isDirectory()) {
      total += await directorySize(fullPath);
    } else if (entry.isFile()) {
      const s = await stat(fullPath);
      total += s.size;
    }
  }
  return total;
}

let lockAcquired = false;
try {
  lockAcquired = await acquireBuildLock();
  await rm(nextDir, { recursive: true, force: true });
  await rm(outDir, { recursive: true, force: true });
  await run("npx", ["next", "build"], {
    NEXT_DAEMON_STATIC_EXPORT: "1",
    NEXT_PUBLIC_DAEMON_STATIC: "1",
    NEXT_BUILD_WORKERS: process.env.NEXT_BUILD_WORKERS ?? "4",
  });
  if (!existsSync(path.join(outDir, "index.html"))) {
    throw new Error("Static export did not produce out/index.html.");
  }

  await rm(webDir, { recursive: true, force: true });
  await mkdir(webDir, { recursive: true });
  await cp(outDir, webDir, { recursive: true });

  await copyIfExists(path.join(root, "public"), webDir);

  await pruneStaticExportArtifacts(webDir);

  await writeFile(
    path.join(webDir, "STATIC_WEB_README.txt"),
    [
      "This directory contains the static dashboard shell served by the Go daemon.",
      "It is produced with Next output: export.",
      "The daemon serves index.html as an SPA fallback for /servers/:id/:tab deep links.",
      "Rebuild with: npm run build:daemon-web",
      "",
    ].join("\n"),
  );
  await rm(outDir, { recursive: true, force: true });
  await rm(nextDir, { recursive: true, force: true });
} finally {
  await releaseBuildLock(lockAcquired);
}

console.log(`Prepared exported daemon web assets in ${path.relative(root, webDir)}`);
