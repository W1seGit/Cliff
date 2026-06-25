import { spawn, spawnSync } from "node:child_process";
import { cp, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const daemonDir = path.join(root, "daemon");
const sourceWebDir = path.join(daemonDir, "web");
const outDir = path.join(root, "out");
const nextDir = path.join(root, ".next");
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-daemon-ui-smoke-"));
const webBackup = path.join(tempRoot, "web-backup");
const webDir = path.join(tempRoot, "web");
const dataDir = path.join(tempRoot, "data");
const serverRoot = path.join(tempRoot, "servers");
const importSourceDir = path.join(tempRoot, "import-source");
const chromeProfile = path.join(tempRoot, "chrome");
const binary = path.join(tempRoot, process.platform === "win32" ? "cliff-ui-smoke.exe" : "cliff-ui-smoke");

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
}

function chromePath() {
  const candidates = process.platform === "win32"
    ? [
        path.join(process.env.PROGRAMFILES ?? "", "Google/Chrome/Application/chrome.exe"),
        path.join(process.env["PROGRAMFILES(X86)"] ?? "", "Google/Chrome/Application/chrome.exe"),
        path.join(process.env.LOCALAPPDATA ?? "", "Google/Chrome/Application/chrome.exe"),
        path.join(process.env.PROGRAMFILES ?? "", "Microsoft/Edge/Application/msedge.exe"),
        path.join(process.env["PROGRAMFILES(X86)"] ?? "", "Microsoft/Edge/Application/msedge.exe"),
      ]
    : [
        "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
        "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
        "/usr/bin/google-chrome",
        "/usr/bin/chromium",
        "/usr/bin/chromium-browser",
        "/usr/bin/microsoft-edge",
      ];
  return candidates.find((candidate) => candidate && existsSync(candidate));
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

function run(command, args, cwd = root) {
  const result = spawnSync(commandName(command), args, {
    cwd,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed with status ${result.status}`);
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
    if (child.exitCode !== null) {
      throw new Error(`daemon exited early with code ${child.exitCode}\n${logs.join("")}`);
    }
    try {
      const { response, json } = await request(baseUrl, "/api/health");
      if (response.ok && json?.ok === true && json?.daemon === "cliff") return;
    } catch {
      // Keep polling until the daemon listener is ready.
    }
    await wait(150);
  }
  throw new Error(`daemon did not become healthy\n${logs.join("")}`);
}

function getJson(url) {
  return new Promise((resolve, reject) => {
    http.get(url, (response) => {
      let data = "";
      response.on("data", (chunk) => {
        data += chunk;
      });
      response.on("end", () => {
        try {
          resolve(JSON.parse(data));
        } catch (error) {
          reject(error);
        }
      });
    }).on("error", reject);
  });
}

async function waitForPageTarget(chromePort) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    try {
      const tabs = await getJson(`http://127.0.0.1:${chromePort}/json/list`);
      const page = tabs.find((tab) => tab.type === "page" && tab.webSocketDebuggerUrl);
      if (page) return page.webSocketDebuggerUrl;
    } catch {
      await wait(250);
    }
  }
  throw new Error("Chrome DevTools page target did not start");
}

async function stopProcessTree(child) {
  if (!child || child.exitCode !== null) return;
  if (process.platform === "win32") {
    spawn("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore" });
  } else {
    child.kill("SIGTERM");
  }
  await wait(1500);
}

async function browserSession(chromePort) {
  const ws = new WebSocket(await waitForPageTarget(chromePort));
  const pending = new Map();
  const runtimeErrors = [];
  let nextId = 1;
  ws.onmessage = (event) => {
    const message = JSON.parse(event.data);
    if (message.method === "Runtime.exceptionThrown") {
      const details = message.params?.exceptionDetails;
      runtimeErrors.push([
        details?.text ?? "Runtime exception",
        details?.exception?.description ?? details?.exception?.value ?? "",
        ...(details?.stackTrace?.callFrames ?? []).slice(0, 5).map((frame) => `    at ${frame.functionName || "<anonymous>"} (${frame.url}:${frame.lineNumber + 1}:${frame.columnNumber + 1})`),
      ].filter(Boolean).join("\n"));
      return;
    }
    if (message.method === "Log.entryAdded" && message.params?.entry?.level === "error") {
      runtimeErrors.push(message.params.entry.text);
      return;
    }
    if (!message.id || !pending.has(message.id)) return;
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) reject(new Error(JSON.stringify(message.error)));
    else resolve(message.result);
  };
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = reject;
  });

  function send(method, params = {}) {
    const id = nextId++;
    ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
  }

  async function text() {
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: "document.body.innerText",
    });
    return result.result.value ?? "";
  }

  async function waitForText(label, expected) {
    const deadline = Date.now() + 20_000;
    while (Date.now() < deadline) {
      const bodyText = await text();
      if (bodyText.includes(expected)) return bodyText;
      await wait(250);
    }
    throw new Error(`${label} did not show ${expected}. Current text: ${(await text()).slice(0, 500)}${runtimeErrors.length > 0 ? `\nRuntime errors:\n${runtimeErrors.join("\n")}` : ""}`);
  }

  async function waitForPath(label, expectedSuffix) {
    const deadline = Date.now() + 10_000;
    while (Date.now() < deadline) {
      const result = await send("Runtime.evaluate", {
        returnByValue: true,
        expression: "window.location.pathname",
      });
      const pathname = result.result.value ?? "";
      if (pathname.endsWith(expectedSuffix)) return pathname;
      await wait(200);
    }
    throw new Error(`${label} did not reach ${expectedSuffix}`);
  }

  async function navigate(url) {
    await send("Page.navigate", { url });
    await wait(500);
  }

  async function goBack() {
    await send("Page.navigateToHistoryEntry", { entryId: await historyEntry(-1) });
    await wait(500);
  }

  async function goForward() {
    await send("Page.navigateToHistoryEntry", { entryId: await historyEntry(1) });
    await wait(500);
  }

  async function historyEntry(delta) {
    const result = await send("Page.getNavigationHistory");
    const currentIndex = result.currentIndex;
    const entry = result.entries[currentIndex + delta];
    if (!entry) throw new Error(`No history entry at delta ${delta}`);
    return entry.id;
  }

  async function clickText(text) {
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const candidates = [...document.querySelectorAll("button,a")];
        const target = candidates.find((element) => {
          const label = (element.innerText || element.textContent || "").trim();
          const rect = element.getBoundingClientRect();
          return !element.disabled && label.includes(${JSON.stringify(text)}) && rect.width > 0 && rect.height > 0;
        });
        if (!target) return false;
        target.click();
        return true;
      })()`,
    });
    if (!result.result.value) throw new Error(`Could not click ${text}`);
    await wait(500);
  }

  async function clickTab(text) {
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const candidates = [...document.querySelectorAll(".tabs button")];
        const target = candidates.find((element) => {
          const label = (element.innerText || element.textContent || "").trim();
          const rect = element.getBoundingClientRect();
          return !element.disabled && label === ${JSON.stringify(text)} && rect.width > 0 && rect.height > 0;
        });
        if (!target) return false;
        target.click();
        return true;
      })()`,
    });
    if (!result.result.value) throw new Error(`Could not click tab ${text}`);
    await wait(500);
  }

  async function createAdmin() {
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const username = document.querySelector('input[name="username"]');
        const password = document.querySelector('input[name="password"]');
        const form = document.querySelector("form");
        if (!username || !password || !form) return false;
        username.value = "daemonui";
        username.dispatchEvent(new Event("input", { bubbles: true }));
        password.value = "daemon-ui-password";
        password.dispatchEvent(new Event("input", { bubbles: true }));
        form.requestSubmit();
        return true;
      })()`,
    });
    if (!result.result.value) throw new Error("Could not submit setup form");
  }

  async function apiJson(pathname, options = {}) {
    const result = await send("Runtime.evaluate", {
      awaitPromise: true,
      returnByValue: true,
      expression: `(async () => {
        const response = await fetch(${JSON.stringify(pathname)}, {
          credentials: "include",
          ...${JSON.stringify(options)},
          headers: {
            ...(${JSON.stringify(options.headers ?? {})}),
            ...(${options.body ? `{ "content-type": "application/json" }` : "{}"}),
          },
        });
        const text = await response.text();
        let json = null;
        try { json = text ? JSON.parse(text) : null; } catch {}
        return { ok: response.ok, status: response.status, text, json };
      })()`,
    });
    const value = result.result.value;
    if (!value?.ok) throw new Error(`${pathname} returned ${value?.status}: ${value?.text}`);
    return value.json;
  }

  await send("Page.enable");
  await send("Runtime.enable");
  await send("Log.enable");
  await send("Network.enable");

  return {
    close: () => ws.close(),
    createAdmin,
    apiJson,
    clickTab,
    clickText,
    goBack,
    goForward,
    navigate,
    text,
    waitForPath,
    waitForText,
    assertNoRuntimeErrors() {
      if (runtimeErrors.length > 0) throw new Error(`Browser runtime errors:\n${runtimeErrors.join("\n")}`);
    },
  };
}

async function writeMetadataCache() {
  const cacheDir = path.join(dataDir, "cache");
  await mkdir(cacheDir, { recursive: true });
  await writeFile(path.join(cacheDir, "minecraft-metadata.json"), JSON.stringify({
    fetchedAt: new Date().toISOString(),
    latest: { release: "1.8.9", snapshot: "1.8.9" },
    minecraftVersions: [{ id: "1.8.9", type: "release", url: "https://example.invalid/1.8.9.json", time: "2015-12-09T00:00:00Z", releaseTime: "2015-12-09T00:00:00Z" }],
    loaders: { vanilla: [], paper: [], fabric: [], forge: [], neoforge: [] },
    loaderCatalog: { vanilla: [], paper: [], fabric: [], forge: [], neoforge: [] },
  }, null, 2));
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

let daemon = null;
let chrome = null;
try {
  const browser = chromePath();
  if (!browser) throw new Error("Chrome or Edge was not found for daemon UI smoke test");

  await mkdir(tempRoot, { recursive: true });
  if (existsSync(sourceWebDir)) await cp(sourceWebDir, webBackup, { recursive: true });

  run("npm", ["run", "build:daemon-web"]);
  await cp(sourceWebDir, webDir, { recursive: true });

  await rm(sourceWebDir, { recursive: true, force: true });
  if (existsSync(webBackup)) {
    await mkdir(path.dirname(sourceWebDir), { recursive: true });
    await cp(webBackup, sourceWebDir, { recursive: true });
  }
  await rm(outDir, { recursive: true, force: true });
  await rm(nextDir, { recursive: true, force: true });

  run("go", ["build", "-o", binary, "./cmd/cliff"], daemonDir);

  await mkdir(dataDir, { recursive: true });
  await mkdir(serverRoot, { recursive: true });
  await writeMetadataCache();
  const launchTarget = await writeFakeServer();

  const port = await getFreePort();
  const chromePort = await getFreePort();
  const baseUrl = `http://127.0.0.1:${port}`;
  const logs = [];
  daemon = spawn(binary, ["--host", "127.0.0.1", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot, "--web-dir", webDir], {
    cwd: daemonDir,
    env: { ...process.env },
    stdio: ["ignore", "pipe", "pipe"],
  });
  daemon.stdout.on("data", (chunk) => logs.push(chunk.toString()));
  daemon.stderr.on("data", (chunk) => logs.push(chunk.toString()));

  await waitForHealth(baseUrl, daemon, logs);

  chrome = spawn(browser, [
    "--headless=new",
    `--remote-debugging-port=${chromePort}`,
    `--user-data-dir=${chromeProfile}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-gpu",
    baseUrl,
  ], { stdio: "ignore" });

  const page = await browserSession(chromePort);
  try {
    await page.waitForText("setup page", "Create local admin");
    await page.createAdmin();
    await page.waitForText("dashboard shell", "Cliff");
    const dashboardText = await page.waitForText("empty server state", "No servers yet");
    if (dashboardText.includes("Create your first server")) {
      throw new Error("Initial empty-state fallback regressed to Create your first server");
    }

    await page.navigate(`${baseUrl}/app-settings`);
    await page.waitForText("settings deep link", "App settings");
    await page.waitForText("settings content", "Global dashboard settings.");

    const created = await page.apiJson("/api/servers", {
      method: "POST",
      body: JSON.stringify({
        mode: "import",
        name: "UI Runtime Server",
        path: importSourceDir,
        type: "vanilla",
        minecraftVersion: "1.8.9",
        minMemoryMb: 512,
        maxMemoryMb: 512,
        port: 25565,
        javaPath: "java",
        launchJar: launchTarget,
      }),
    });
    const serverId = created?.server?.id;
    if (!serverId) throw new Error(`Server creation did not return an id: ${JSON.stringify(created)}`);

    await page.navigate(`${baseUrl}/servers/${serverId}/overview`);
    await page.waitForText("runtime server page", "UI Runtime Server");
    await page.waitForPath("overview route", `/servers/${serverId}/overview`);
    await page.clickTab("Console");
    await page.waitForPath("console route", `/servers/${serverId}/console`);
    await page.waitForText("console panel", "Console");
    await page.goBack();
    await page.waitForPath("history back overview route", `/servers/${serverId}/overview`);
    await page.waitForText("history back overview panel", "Live usage");
    await page.goForward();
    await page.waitForPath("history forward console route", `/servers/${serverId}/console`);
    await page.waitForText("history forward console panel", "Console");
    await page.clickTab("Overview");
    await page.waitForText("runtime initial stopped state", "Stopped");
    await page.clickText("Start");
    await page.waitForText("runtime started state", "Running");
    await page.clickText("Stop");
    await page.waitForText("runtime stopped state", "Stopped");
    page.assertNoRuntimeErrors();
  } finally {
    page.close();
  }

  const fallback = await request(baseUrl, "/servers/srv_smoke/overview");
  if (!fallback.response.ok || !fallback.text.includes("<!DOCTYPE html>")) {
    throw new Error(`SPA fallback returned ${fallback.response.status}`);
  }

  console.log("Daemon UI smoke test passed");
} finally {
  await stopProcessTree(chrome);
  if (daemon && daemon.exitCode === null) {
    daemon.kill("SIGINT");
    await new Promise((resolve) => {
      const timer = setTimeout(resolve, 3000);
      daemon.once("exit", () => {
        clearTimeout(timer);
        resolve();
      });
    });
    if (daemon.exitCode === null) daemon.kill("SIGKILL");
  }
  await rm(sourceWebDir, { recursive: true, force: true });
  if (existsSync(webBackup)) {
    await mkdir(path.dirname(sourceWebDir), { recursive: true });
    await cp(webBackup, sourceWebDir, { recursive: true });
  }
  await rm(outDir, { recursive: true, force: true });
  await rm(nextDir, { recursive: true, force: true });
  await rm(tempRoot, { recursive: true, force: true });
}
