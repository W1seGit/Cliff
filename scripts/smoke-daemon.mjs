import { spawn, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import crypto from "node:crypto";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const daemonDir = path.join(root, "daemon");
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-daemon-smoke-"));
const dataDir = path.join(tempRoot, "data");
const webDir = path.join(tempRoot, "web");
const serverRoot = path.join(tempRoot, "servers");
const importSourceDir = path.join(tempRoot, "import-source");
const binary = path.join(tempRoot, process.platform === "win32" ? "cliff-smoke.exe" : "cliff-smoke");

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
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

function buildDaemon() {
  const result = spawnSync(commandName("go"), ["build", "-o", binary, "./cmd/cliff"], {
    cwd: daemonDir,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`go build failed with status ${result.status}`);
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

async function waitForHealth(baseUrl, child, logs) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`daemon exited early with code ${child.exitCode}\n${logs.join("")}`);
    }
    try {
      const { response, json } = await request(baseUrl, "/api/health");
      if (response.ok && json?.ok === true && json?.daemon === "cliff") return;
    } catch {
      // Keep polling until the listener is ready.
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`daemon did not become healthy\n${logs.join("")}`);
}

async function expectStatus(baseUrl, pathname, status, options = {}) {
  const result = await request(baseUrl, pathname, options);
  if (result.response.status !== status) {
    throw new Error(`${pathname} returned ${result.response.status}, expected ${status}: ${result.text}`);
  }
  return result;
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function expectRuntime(baseUrl, cookie, serverId, lifecycle) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const result = await expectStatus(baseUrl, "/api/runtime", 200, { headers: { cookie } });
    const serverRuntime = result.json?.servers?.[serverId] ?? (result.json?.runningServerId === serverId ? result.json : null);
    if ((serverRuntime?.lifecycle ?? result.json?.lifecycle) === lifecycle) return result.json;
    await wait(150);
  }
  throw new Error(`runtime did not reach ${lifecycle}`);
}

function writeMetadataCache() {
  const cacheDir = path.join(dataDir, "cache");
  return mkdir(cacheDir, { recursive: true }).then(() => writeFile(path.join(cacheDir, "minecraft-metadata.json"), JSON.stringify({
    fetchedAt: new Date().toISOString(),
    latest: { release: "1.8.9", snapshot: "1.8.9" },
    minecraftVersions: [{ id: "1.8.9", type: "release", url: "https://example.invalid/1.8.9.json", time: "2015-12-09T00:00:00Z", releaseTime: "2015-12-09T00:00:00Z" }],
    loaders: { vanilla: [], paper: [], fabric: [], forge: [], neoforge: [] },
    loaderCatalog: { vanilla: [], paper: [], fabric: [], forge: [], neoforge: [] },
  }, null, 2)));
}

async function writeFakeServer() {
  await mkdir(importSourceDir, { recursive: true });
  const launchTarget = process.platform === "win32" ? "run.bat" : "run.sh";
  const script = process.platform === "win32"
    ? [
        "@echo off",
        "echo Done (0.1s)! For help, type \"help\"",
        ":loop",
        "set /p cmd=",
        "if \"%cmd%\"==\"stop\" echo stopped & exit /b 0",
        "echo command:%cmd%",
        "goto loop",
        "",
      ].join("\r\n")
    : [
        "#!/bin/sh",
        "echo 'Done (0.1s)! For help, type \"help\"'",
        "while IFS= read -r cmd; do",
        "  if [ \"$cmd\" = \"stop\" ]; then echo stopped; exit 0; fi",
        "  echo \"command:$cmd\"",
        "done",
        "",
      ].join("\n");
  await writeFile(path.join(importSourceDir, launchTarget), script, { mode: 0o755 });
  await writeFile(path.join(importSourceDir, "server.properties"), "server-port=25565\n");
  return launchTarget;
}

function createWebSocketClient(baseUrl, pathname, cookie) {
  const url = new URL(pathname, baseUrl);
  const key = crypto.randomBytes(16).toString("base64");
  const socket = net.createConnection({ host: url.hostname, port: Number(url.port) || 80 });
  let buffer = Buffer.alloc(0);

  function readUntilHandshake() {
    return new Promise((resolve, reject) => {
      const onData = (chunk) => {
        buffer = Buffer.concat([buffer, chunk]);
        const index = buffer.indexOf("\r\n\r\n");
        if (index === -1) return;
        socket.off("data", onData);
        socket.off("error", reject);
        const header = buffer.subarray(0, index).toString("utf8");
        buffer = buffer.subarray(index + 4);
        if (!header.startsWith("HTTP/1.1 101")) {
          reject(new Error(`WebSocket upgrade failed:\n${header}`));
          return;
        }
        resolve();
      };
      socket.on("data", onData);
      socket.on("error", reject);
    });
  }

  function readFrame() {
    return new Promise((resolve, reject) => {
      const tryParse = () => {
        if (buffer.length < 2) return false;
        const opcode = buffer[0] & 0x0f;
        let offset = 2;
        let length = buffer[1] & 0x7f;
        if (length === 126) {
          if (buffer.length < offset + 2) return false;
          length = buffer.readUInt16BE(offset);
          offset += 2;
        } else if (length === 127) {
          if (buffer.length < offset + 8) return false;
          const high = buffer.readUInt32BE(offset);
          const low = buffer.readUInt32BE(offset + 4);
          length = high * 2 ** 32 + low;
          offset += 8;
        }
        const masked = Boolean(buffer[1] & 0x80);
        const maskOffset = masked ? 4 : 0;
        if (buffer.length < offset + maskOffset + length) return false;
        let payload = buffer.subarray(offset + maskOffset, offset + maskOffset + length);
        if (masked) {
          const mask = buffer.subarray(offset, offset + 4);
          payload = Buffer.from(payload.map((byte, index) => byte ^ mask[index % 4]));
        }
        buffer = buffer.subarray(offset + maskOffset + length);
        if (opcode === 8) {
          reject(new Error("WebSocket closed"));
          return true;
        }
        if (opcode === 9) {
          sendFrame(payload, 0x0a);
          return tryParse();
        }
        resolve(JSON.parse(payload.toString("utf8")));
        return true;
      };
      if (tryParse()) return;
      const onData = (chunk) => {
        buffer = Buffer.concat([buffer, chunk]);
        if (!tryParse()) return;
        socket.off("data", onData);
        socket.off("error", reject);
      };
      socket.on("data", onData);
      socket.on("error", reject);
    });
  }

  function sendFrame(payload, opcode = 1) {
    const data = Buffer.isBuffer(payload) ? payload : Buffer.from(String(payload));
    const headerLength = data.length < 126 ? 2 : data.length <= 65535 ? 4 : 10;
    const header = Buffer.alloc(headerLength);
    header[0] = 0x80 | opcode;
    if (data.length < 126) {
      header[1] = 0x80 | data.length;
    } else if (data.length <= 65535) {
      header[1] = 0x80 | 126;
      header.writeUInt16BE(data.length, 2);
    } else {
      header[1] = 0x80 | 127;
      header.writeUInt32BE(0, 2);
      header.writeUInt32BE(data.length, 6);
    }
    const mask = crypto.randomBytes(4);
    const masked = Buffer.from(data.map((byte, index) => byte ^ mask[index % 4]));
    socket.write(Buffer.concat([header, mask, masked]));
  }

  return new Promise((resolve, reject) => {
    socket.once("connect", async () => {
      socket.write([
        `GET ${url.pathname}${url.search} HTTP/1.1`,
        `Host: ${url.host}`,
        "Upgrade: websocket",
        "Connection: Upgrade",
        `Sec-WebSocket-Key: ${key}`,
        "Sec-WebSocket-Version: 13",
        `Cookie: ${cookie}`,
        "\r\n",
      ].join("\r\n"));
      try {
        await readUntilHandshake();
        resolve({
          read: readFrame,
          sendJSON(value) {
            sendFrame(JSON.stringify(value));
          },
          close() {
            socket.end();
          },
        });
      } catch (error) {
        reject(error);
      }
    });
    socket.once("error", reject);
  });
}

async function waitForWebSocketLog(client, expected) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const message = await Promise.race([
      client.read(),
      wait(10_000).then(() => null),
    ]);
    if (!message) break;
    if (message.type === "snapshot" && message.logs?.some((line) => line.includes(expected))) return message;
    if (message.type === "event" && message.event?.type === "log" && message.event.line.includes(expected)) return message;
  }
  throw new Error(`WebSocket log did not include ${expected}`);
}

async function waitForWebSocketError(client, expected) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const message = await Promise.race([
      client.read(),
      wait(10_000).then(() => null),
    ]);
    if (!message) break;
    if (message.type === "error" && String(message.error ?? "").includes(expected)) return message;
  }
  throw new Error(`WebSocket error did not include ${expected}`);
}

async function assertWebSocketNoLogWithin(client, forbidden, timeoutMs = 500) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const message = await Promise.race([
      client.read(),
      wait(Math.max(1, deadline - Date.now())).then(() => null),
    ]);
    if (!message) return;
    const snapshotLogs = message.type === "snapshot" ? message.logs ?? [] : [];
    const eventLine = message.type === "event" && message.event?.type === "log" ? message.event.line ?? "" : "";
    if (snapshotLogs.some((line) => line.includes(forbidden)) || eventLine.includes(forbidden)) {
      throw new Error(`WebSocket unexpectedly included log ${forbidden}: ${JSON.stringify(message)}`);
    }
  }
}

let child = null;
try {
  await mkdir(dataDir, { recursive: true });
  await mkdir(webDir, { recursive: true });
  await mkdir(path.join(webDir, "_next", "static", "chunks"), { recursive: true });
  await mkdir(serverRoot, { recursive: true });
  await writeMetadataCache();
  await writeFile(path.join(webDir, "index.html"), "<!doctype html><title>Smoke SPA</title><main>daemon smoke dashboard</main>\n");
  await writeFile(path.join(webDir, "_next", "static", "chunks", "smoke.js"), "console.log('smoke');\n");
  await writeFile(path.join(tempRoot, "outside-web.txt"), "outside web root should never be served\n");
  const launchTarget = await writeFakeServer();

  buildDaemon();

  const port = await getFreePort();
  const baseUrl = `http://127.0.0.1:${port}`;
  const logs = [];
  child = spawn(binary, ["--host", "127.0.0.1", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot, "--web-dir", webDir], {
    cwd: daemonDir,
    env: { ...process.env },
    stdio: ["ignore", "pipe", "pipe"],
  });
  child.stdout.on("data", (chunk) => logs.push(chunk.toString()));
  child.stderr.on("data", (chunk) => logs.push(chunk.toString()));

  await waitForHealth(baseUrl, child, logs);

  const fallback = await expectStatus(baseUrl, "/servers/srv_smoke/overview", 200);
  if (!fallback.text.includes("daemon smoke dashboard")) {
    throw new Error("SPA fallback did not serve the daemon web index");
  }
  if (fallback.response.headers.get("cache-control") !== "no-cache") {
    throw new Error(`SPA fallback should not be cached aggressively: ${fallback.response.headers.get("cache-control")}`);
  }
  const staticChunk = await expectStatus(baseUrl, "/_next/static/chunks/smoke.js", 200);
  if (staticChunk.response.headers.get("cache-control") !== "public, max-age=31536000, immutable") {
    throw new Error(`Next static chunks should be immutable cached: ${staticChunk.response.headers.get("cache-control")}`);
  }
  const traversal = await expectStatus(baseUrl, "/%2e%2e/outside-web.txt", 200);
  if (traversal.text.includes("outside web root should never be served")) {
    throw new Error("SPA file server served a file outside the daemon web root");
  }

  const setupState = await expectStatus(baseUrl, "/api/auth/me", 200);
  if (setupState.json?.needsSetup !== true || setupState.json?.user !== null) {
    throw new Error(`unexpected initial setup state: ${setupState.text}`);
  }

  await expectStatus(baseUrl, "/api/servers", 401);

  const setup = await expectStatus(baseUrl, "/api/auth/setup", 200, {
    method: "POST",
    body: JSON.stringify({ username: "daemon-smoke", password: "daemon-smoke-password" }),
  });
  const cookie = setup.response.headers.get("set-cookie")?.split(";")[0];
  if (!cookie) throw new Error("setup did not return a session cookie");
  if (setup.json?.user?.username !== "daemon-smoke") throw new Error(`unexpected setup response: ${setup.text}`);

  const authenticated = await expectStatus(baseUrl, "/api/auth/me", 200, { headers: { cookie } });
  if (authenticated.json?.user?.username !== "daemon-smoke" || authenticated.json?.needsSetup !== false) {
    throw new Error(`session did not authenticate: ${authenticated.text}`);
  }

  const settings = await expectStatus(baseUrl, "/api/settings?storage=0", 200, { headers: { cookie } });
  if (path.resolve(settings.json?.serverRoot ?? "") !== path.resolve(serverRoot)) {
    throw new Error(`settings did not use isolated server root: ${settings.text}`);
  }
  const metadata = await expectStatus(baseUrl, "/api/minecraft/versions", 200, { headers: { cookie } });
  if (metadata.json?.latest?.release !== "1.8.9") {
    throw new Error(`metadata endpoint did not read smoke cache: ${metadata.text}`);
  }
  await writeFile(path.join(dataDir, "cache", "minecraft-metadata.json"), JSON.stringify({
    fetchedAt: new Date().toISOString(),
    latest: { release: "poisoned-cache", snapshot: "poisoned-cache" },
    minecraftVersions: [{ id: "poisoned-cache", type: "release", url: "https://example.invalid/poisoned.json", time: "2026-01-01T00:00:00Z", releaseTime: "2026-01-01T00:00:00Z" }],
    loaders: { vanilla: [], paper: [], fabric: [], forge: [], neoforge: [] },
    loaderCatalog: { vanilla: [], paper: [], fabric: [], forge: [], neoforge: [] },
  }, null, 2));
  const cachedMetadata = await expectStatus(baseUrl, "/api/minecraft/versions", 200, { headers: { cookie } });
  if (cachedMetadata.json?.latest?.release !== metadata.json?.latest?.release) {
    throw new Error(`metadata endpoint re-read disk cache instead of using daemon memory cache: ${cachedMetadata.text}`);
  }
  const settingsWithStorage = await expectStatus(baseUrl, "/api/settings", 200, { headers: { cookie } });
  if (settingsWithStorage.json?.storage?.rootExists !== true || typeof settingsWithStorage.json?.storage?.serverRootSizeBytes !== "number") {
    throw new Error(`settings did not include storage usage: ${settingsWithStorage.text}`);
  }
  await wait(1100);
  const cachedSettingsWithStorage = await expectStatus(baseUrl, "/api/settings", 200, { headers: { cookie } });
  if (cachedSettingsWithStorage.json?.storage?.updatedAt !== settingsWithStorage.json?.storage?.updatedAt) {
    throw new Error(`settings storage usage was not cached between rapid requests: ${JSON.stringify({
      first: settingsWithStorage.json?.storage,
      second: cachedSettingsWithStorage.json?.storage,
    })}`);
  }
  await writeFile(path.join(serverRoot, "storage-cache-poison.txt"), "this should not be counted until the directory size cache expires");
  await wait(10_100);
  const settingsAfterStorageCacheExpiry = await expectStatus(baseUrl, "/api/settings", 200, { headers: { cookie } });
  if (settingsAfterStorageCacheExpiry.json?.storage?.updatedAt === settingsWithStorage.json?.storage?.updatedAt) {
    throw new Error(`settings storage payload cache did not expire: ${settingsAfterStorageCacheExpiry.text}`);
  }
  if (settingsAfterStorageCacheExpiry.json?.storage?.serverRootSizeBytes !== settingsWithStorage.json?.storage?.serverRootSizeBytes) {
    throw new Error(`settings storage did not use cached directory sizes after payload refresh: ${JSON.stringify({
      first: settingsWithStorage.json?.storage,
      second: settingsAfterStorageCacheExpiry.json?.storage,
    })}`);
  }

  const servers = await expectStatus(baseUrl, "/api/servers", 200, { headers: { cookie } });
  if (!Array.isArray(servers.json) || servers.json.length !== 0) {
    throw new Error(`fresh daemon database should have no servers: ${servers.text}`);
  }

  const runtime = await expectStatus(baseUrl, "/api/runtime", 200, { headers: { cookie } });
  if (runtime.json?.lifecycle !== "stopped" || runtime.json?.pid !== 0) {
    throw new Error(`fresh daemon runtime should be stopped: ${runtime.text}`);
  }
  const emptyDashboard = await expectStatus(baseUrl, "/api/servers?runtime=1", 200, { headers: { cookie } });
  if (!Array.isArray(emptyDashboard.json?.servers) || emptyDashboard.json.servers.length !== 0 || emptyDashboard.json?.runtime?.lifecycle !== "stopped" || "health" in emptyDashboard.json) {
    throw new Error(`empty dashboard runtime bundle was not lean: ${emptyDashboard.text}`);
  }

  const imported = await expectStatus(baseUrl, "/api/servers", 200, {
    method: "POST",
    headers: { cookie },
    body: JSON.stringify({
      mode: "import",
      path: importSourceDir,
      name: "Smoke Server",
      type: "vanilla",
      minecraftVersion: "1.8.9",
      minMemoryMb: 512,
      maxMemoryMb: 512,
      port: 25565,
      launchJar: launchTarget,
    }),
  });
  const server = imported.json?.server;
  if (!server?.id || server.launchJar !== launchTarget) {
    throw new Error(`server import did not return expected launch target: ${imported.text}`);
  }
  if (path.resolve(server.path) === path.resolve(importSourceDir)) {
    throw new Error("server import reused the source folder instead of copying it");
  }
  const dashboard = await expectStatus(baseUrl, "/api/servers?runtime=1", 200, { headers: { cookie } });
  if (!Array.isArray(dashboard.json?.servers) || dashboard.json.servers.length !== 1 || dashboard.json?.runtime?.lifecycle !== "stopped" || "health" in dashboard.json) {
    throw new Error(`server dashboard runtime bundle was not lean: ${dashboard.text}`);
  }
  const fleetHealth = await expectStatus(baseUrl, "/api/servers?health=1", 200, { headers: { cookie } });
  if (!Array.isArray(fleetHealth.json?.servers) || fleetHealth.json.servers.length !== 1 || fleetHealth.json?.runtime?.lifecycle !== "stopped") {
    throw new Error(`server dashboard bundle did not include servers and runtime: ${fleetHealth.text}`);
  }
  const bundledHealth = fleetHealth.json?.health?.[server.id];
  if (typeof bundledHealth?.status !== "string" || !Array.isArray(bundledHealth?.checks) || !bundledHealth.checks.some((check) => check.id === "launch")) {
    throw new Error(`server dashboard bundle did not include full health checks: ${fleetHealth.text}`);
  }
  const secondImported = await expectStatus(baseUrl, "/api/servers", 200, {
    method: "POST",
    headers: { cookie },
    body: JSON.stringify({
      mode: "import",
      path: importSourceDir,
      name: "Second Smoke Server",
      type: "vanilla",
      minecraftVersion: "1.8.9",
      minMemoryMb: 512,
      maxMemoryMb: 512,
      port: 25566,
      launchJar: launchTarget,
    }),
  });
  const secondServer = secondImported.json?.server;
  if (!secondServer?.id) {
    throw new Error(`second server import failed: ${secondImported.text}`);
  }
  const selectedHealth = await expectStatus(baseUrl, `/api/servers?health=1&healthFor=${server.id}&usageFor=${server.id}`, 200, { headers: { cookie } });
  if (selectedHealth.json?.servers?.length !== 2 || selectedHealth.json?.health?.[server.id]?.status === undefined || selectedHealth.json?.health?.[secondServer.id] !== undefined) {
    throw new Error(`selected dashboard health should include all servers but only selected health: ${selectedHealth.text}`);
  }

  const stoppedConsoleClient = await createWebSocketClient(baseUrl, `/api/servers/${server.id}/console`, cookie);
  try {
    stoppedConsoleClient.sendJSON({ type: "command", command: "say stopped-smoke" });
    await waitForWebSocketError(stoppedConsoleClient, "server is not running");
  } finally {
    stoppedConsoleClient.close();
  }

  const createdBackup = await expectStatus(baseUrl, `/api/servers/${server.id}/backups`, 200, {
    method: "POST",
    headers: { cookie },
    body: JSON.stringify({ action: "create", reason: "cache smoke" }),
  });
  if (!createdBackup.json?.backupId) {
    throw new Error(`backup creation did not return an id: ${createdBackup.text}`);
  }
  const backupList = await expectStatus(baseUrl, `/api/servers/${server.id}/backups`, 200, { headers: { cookie } });
  const backup = backupList.json?.backups?.find((item) => item.id === createdBackup.json.backupId);
  if (!backup?.snapshotPath || !Number.isFinite(backup.sizeBytes)) {
    throw new Error(`backup listing did not include size and path: ${backupList.text}`);
  }
  await writeFile(path.join(backup.snapshotPath, "cache-poison.txt"), "this should not be observed until the short size cache expires");
  const cachedBackupList = await expectStatus(baseUrl, `/api/servers/${server.id}/backups`, 200, { headers: { cookie } });
  const cachedBackup = cachedBackupList.json?.backups?.find((item) => item.id === backup.id);
  if (cachedBackup?.sizeBytes !== backup.sizeBytes) {
    throw new Error(`backup listing did not use cached snapshot size: ${JSON.stringify({ first: backup, second: cachedBackup })}`);
  }

  const start = await expectStatus(baseUrl, `/api/servers/${server.id}/start`, 200, { method: "POST", headers: { cookie } });
  if (start.json?.runningServerId !== server.id || start.json?.lifecycle !== "running") {
    throw new Error(`server start did not wait for running status: ${start.text}`);
  }
  if ("usage" in start.json) {
    throw new Error(`server start response should avoid usage sampling: ${start.text}`);
  }
  const serverDetail = await expectStatus(baseUrl, `/api/servers/${server.id}`, 200, { headers: { cookie } });
  if (serverDetail.json?.runtime?.runningServerId !== server.id || "usage" in (serverDetail.json?.runtime ?? {})) {
    throw new Error(`server detail response should avoid usage sampling: ${serverDetail.text}`);
  }
  await expectRuntime(baseUrl, cookie, server.id, "running");
  const lightRuntime = await expectStatus(baseUrl, "/api/runtime?light=1", 200, { headers: { cookie } });
  const lightRuntimeStatus = lightRuntime.json?.servers?.[server.id] ?? lightRuntime.json;
  if (lightRuntimeStatus?.runningServerId !== server.id || "usage" in lightRuntimeStatus) {
    throw new Error(`light runtime should avoid usage sampling: ${lightRuntime.text}`);
  }
  const fullRuntime = await expectStatus(baseUrl, "/api/runtime", 200, { headers: { cookie } });
  const fullRuntimeStatus = fullRuntime.json?.servers?.[server.id] ?? fullRuntime.json;
  if (!fullRuntimeStatus?.usage) {
    throw new Error(`full runtime should include usage sampling: ${fullRuntime.text}`);
  }
  const runningDashboard = await expectStatus(baseUrl, "/api/servers?runtime=1", 200, { headers: { cookie } });
  const runningDashboardStatus = runningDashboard.json?.runtime?.servers?.[server.id] ?? runningDashboard.json?.runtime;
  if (runningDashboardStatus?.runningServerId !== server.id || "usage" in runningDashboardStatus) {
    throw new Error(`running dashboard runtime bundle should avoid usage sampling: ${runningDashboard.text}`);
  }

  const consoleClient = await createWebSocketClient(baseUrl, `/api/servers/${server.id}/console`, cookie);
  try {
    const consoleSnapshot = await waitForWebSocketLog(consoleClient, "Done (0.1s)!");
    if ("usage" in (consoleSnapshot.status ?? {})) {
      throw new Error(`default console websocket snapshot should avoid usage sampling: ${JSON.stringify(consoleSnapshot)}`);
    }
    consoleClient.sendJSON({ type: "command", command: "say daemon-smoke" });
    await waitForWebSocketLog(consoleClient, "> say daemon-smoke");
    await waitForWebSocketLog(consoleClient, "command:say daemon-smoke");
  } finally {
    consoleClient.close();
  }
  const command = await expectStatus(baseUrl, `/api/servers/${server.id}/command`, 200, {
    method: "POST",
    headers: { cookie },
    body: JSON.stringify({ command: "say daemon-http-command" }),
  });
  if (command.json?.runningServerId !== server.id || "usage" in command.json) {
    throw new Error(`server command response should avoid usage sampling: ${command.text}`);
  }

  const usageClient = await createWebSocketClient(baseUrl, `/api/servers/${server.id}/console?usage=1`, cookie);
  try {
    const usageSnapshot = await waitForWebSocketLog(usageClient, "Done (0.1s)!");
    if (!usageSnapshot.status?.usage) {
      throw new Error(`usage console websocket snapshot should include usage sampling: ${JSON.stringify(usageSnapshot)}`);
    }
  } finally {
    usageClient.close();
  }

  const lightNoLogClient = await createWebSocketClient(baseUrl, `/api/servers/${server.id}/console?logs=0`, cookie);
  try {
    const noLogSnapshot = await lightNoLogClient.read();
    if (noLogSnapshot.type !== "snapshot" || Array.isArray(noLogSnapshot.logs)) {
      throw new Error(`logs=0 websocket snapshot should omit retained logs: ${JSON.stringify(noLogSnapshot)}`);
    }
    lightNoLogClient.sendJSON({ type: "command", command: "say no-log-smoke" });
    await assertWebSocketNoLogWithin(lightNoLogClient, "no-log-smoke");
  } finally {
    lightNoLogClient.close();
  }

  const stop = await expectStatus(baseUrl, `/api/servers/${server.id}/stop`, 200, { method: "POST", headers: { cookie } });
  if (stop.json?.lifecycle !== "stopping" && stop.json?.lifecycle !== "stopped") {
    throw new Error(`server stop did not return stopping/stopped status: ${stop.text}`);
  }
  if ("usage" in stop.json) {
    throw new Error(`server stop response should avoid usage sampling: ${stop.text}`);
  }
  await expectRuntime(baseUrl, cookie, server.id, "stopped");
  const retainedLogs = await expectStatus(baseUrl, `/api/servers/${server.id}/logs`, 200, { headers: { cookie } });
  const logText = retainedLogs.json?.logs?.join("\n") ?? "";
  if (!logText.includes("command:say daemon-smoke") || !logText.includes("stopped")) {
    throw new Error(`daemon did not retain runtime logs after stop: ${retainedLogs.text}`);
  }

  await expectStatus(baseUrl, "/api/auth/logout", 200, { method: "POST", headers: { cookie } });
  await expectStatus(baseUrl, "/api/servers", 401);

  console.log("Daemon smoke test passed");
} finally {
  if (child && child.exitCode === null) {
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
  await rm(tempRoot, { recursive: true, force: true });
}
