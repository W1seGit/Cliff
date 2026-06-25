import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const baseUrl = process.env.CLIFF_URL || "http://localhost:8080";
const username = process.env.CLIFF_USER || "TestingLeo";
const password = process.env.CLIFF_PASS || "TestingLeo";
const shotDir = path.join(root, "screenshots");
const tempRoot = path.join(os.tmpdir(), "cliff-screenshots-" + Date.now());
const chromeProfile = path.join(tempRoot, "chrome");

async function loginAndGetServers() {
  // Login via API to get session cookie
  const loginUrl = new URL("/api/auth/login", baseUrl);
  const loginBody = JSON.stringify({ username, password });
  const cookies = await new Promise((resolve, reject) => {
    const req = http.request(loginUrl, {
      method: "POST",
      headers: { "content-type": "application/json", "content-length": Buffer.byteLength(loginBody) },
    }, (response) => {
      const setCookie = response.headers["set-cookie"] || [];
      const cookie = setCookie.map((c) => c.split(";")[0]).join("; ");
      resolve(cookie);
    });
    req.on("error", reject);
    req.write(loginBody);
    req.end();
  });

  // Get servers with session cookie
  const serversUrl = new URL("/api/servers", baseUrl);
  const serversResult = await new Promise((resolve, reject) => {
    const req = http.request(serversUrl, {
      method: "GET",
      headers: { cookie: cookies },
    }, (response) => {
      let data = "";
      response.on("data", (chunk) => data += chunk);
      response.on("end", () => {
        try {
          const json = JSON.parse(data);
          resolve(Array.isArray(json) ? json : (json.servers || []));
        } catch (e) { reject(e); }
      });
    });
    req.on("error", reject);
    req.end();
  });

  return serversResult;
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

function wait(ms) { return new Promise((resolve) => setTimeout(resolve, ms)); }

function getFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.listen(0, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
    server.on("error", reject);
  });
}

function getJson(url) {
  return new Promise((resolve, reject) => {
    http.get(url, (response) => {
      let data = "";
      response.on("data", (chunk) => data += chunk);
      response.on("end", () => { try { resolve(JSON.parse(data)); } catch (e) { reject(e); } });
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
    } catch { await wait(250); }
  }
  throw new Error("Chrome DevTools page target did not start");
}

async function browserSession(chromePort) {
  const ws = new WebSocket(await waitForPageTarget(chromePort));
  const pending = new Map();
  let nextId = 1;

  ws.onmessage = (event) => {
    const message = JSON.parse(event.data);
    if (!message.id || !pending.has(message.id)) return;
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) reject(new Error(JSON.stringify(message.error)));
    else resolve(message.result);
  };

  await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });

  function send(method, params = {}) {
    const id = nextId++;
    ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
  }

  await send("Page.enable");
  await send("Runtime.enable");

  async function navigate(url) {
    await send("Page.navigate", { url });
    await wait(1500);
  }

  async function text() {
    const result = await send("Runtime.evaluate", { returnByValue: true, expression: "document.body.innerText" });
    return result.result.value ?? "";
  }

  async function waitForText(label, expected, timeout = 15_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      const bodyText = await text();
      if (bodyText.includes(expected)) return bodyText;
      await wait(300);
    }
    throw new Error(`${label} did not show "${expected}"`);
  }

  async function screenshot(name) {
    const result = await send("Page.captureScreenshot", { format: "png" });
    const buffer = Buffer.from(result.data, "base64");
    const filePath = path.join(shotDir, `${name}.png`);
    await writeFile(filePath, buffer);
    console.log(`  Captured: ${name}.png (${(buffer.length / 1024).toFixed(0)} KB)`);
  }

  async function setViewport(width, height) {
    await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 2, mobile: false });
    await wait(500);
  }

  async function login() {
    // Login via Node API to get the session cookie value
    const loginUrl = new URL("/api/auth/login", baseUrl);
    const loginBody = JSON.stringify({ username, password });
    const cookieInfo = await new Promise((resolve, reject) => {
      const req = http.request(loginUrl, {
        method: "POST",
        headers: { "content-type": "application/json", "content-length": Buffer.byteLength(loginBody) },
      }, (response) => {
        const setCookie = response.headers["set-cookie"] || [];
        const cookie = setCookie[0] || "";
        const cookieName = "mc_dash_session";
        const cookieMatch = cookie.match(new RegExp(`${cookieName}=([^;]+)`));
        const expiresMatch = cookie.match(/expires=([^;]+)/i);
        resolve({
          value: cookieMatch ? cookieMatch[1] : "",
          expires: expiresMatch ? new Date(expiresMatch[1]).getTime() / 1000 : undefined,
        });
      });
      req.on("error", reject);
      req.write(loginBody);
      req.end();
    });

    if (!cookieInfo.value) throw new Error("Login did not return session cookie");
    console.log(`  Login: got session cookie`);

    // Set the cookie in Chrome via DevTools Network.setCookie
    await send("Network.enable");
    await send("Network.setCookie", {
      name: "mc_dash_session",
      value: cookieInfo.value,
      domain: "localhost",
      path: "/",
      httpOnly: true,
      sameSite: "Lax",
      expires: cookieInfo.expires,
    });

    // Now navigate to the dashboard - the cookie will be sent with the request
    await send("Page.navigate", { url: baseUrl });
    await wait(3000);

    // Verify we're authenticated
    const pageText = await text();
    if (pageText.includes("Login")) {
      throw new Error("Still showing login page after setting cookie");
    }
    return true;
  }

  async function clickTab(tabName) {
    await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const candidates = [...document.querySelectorAll(".tabs button, .nav-tabs button, [role='tab']")];
        const target = candidates.find((element) => {
          const label = (element.innerText || element.textContent || "").trim();
          return label.includes(${JSON.stringify(tabName)});
        });
        if (!target) return false;
        target.click();
        return true;
      })()`,
    });
    await wait(1000);
  }

  async function clickSidebarItem(itemText) {
    await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const candidates = [...document.querySelectorAll(".sidebar-item, .server-list-item, button, a")];
        const target = candidates.find((element) => {
          const label = (element.innerText || element.textContent || "").trim();
          const rect = element.getBoundingClientRect();
          return label.includes(${JSON.stringify(itemText)}) && rect.width > 0 && rect.height > 0;
        });
        if (!target) return false;
        target.click();
        return true;
      })()`,
    });
    await wait(1500);
  }

  async function getServers() {
    const result = await send("Runtime.evaluate", {
      awaitPromise: true,
      returnByValue: true,
      expression: `(async () => {
        const res = await fetch("/api/servers", { credentials: "include" });
        const json = await res.json();
        return Array.isArray(json) ? json : (json.servers || []);
      })()`,
    });
    return result.result.value;
  }

  return { navigate, text, waitForText, screenshot, setViewport, login, clickTab, clickSidebarItem, getServers, send };
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

let chrome = null;
try {
  const browser = chromePath();
  if (!browser) throw new Error("Chrome or Edge not found");

  await mkdir(shotDir, { recursive: true });
  await mkdir(tempRoot, { recursive: true });

  const chromePort = await getFreePort();
  console.log("Launching headless Chrome...");
  chrome = spawn(browser, [
    "--headless=new",
    `--remote-debugging-port=${chromePort}`,
    `--user-data-dir=${chromeProfile}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--hide-scrollbars",
    baseUrl,
  ], { stdio: "ignore" });

  const page = await browserSession(chromePort);

  // Login
  console.log("Logging in...");
  await page.login();
  await wait(2000);

  // Set desktop viewport
  await page.setViewport(1440, 900);
  await wait(1000);

  // Screenshot 1: Dashboard / Server list
  console.log("\nCapturing screenshots (desktop 1440x900)...");
  await page.screenshot("dashboard");

  // Get servers from API (direct Node call, not through browser)
  const servers = await loginAndGetServers();
  console.log(`  Found ${servers.length} server(s)`);

  if (servers.length > 0) {
    const server = servers[0];
    const serverId = server.id;

    // Screenshot 2: Server Overview
    await page.navigate(`${baseUrl}/servers/${serverId}/overview`);
    await page.waitForText("overview", server.name, 10_000).catch(() => {});
    await wait(2000);
    await page.screenshot("server-overview");

    // Screenshot 3: Console
    await page.navigate(`${baseUrl}/servers/${serverId}/console`);
    await wait(2000);
    await page.screenshot("server-console");

    // Screenshot 4: Mods
    await page.navigate(`${baseUrl}/servers/${serverId}/mods`);
    await wait(2000);
    await page.screenshot("server-mods");

    // Screenshot 5: Worlds
    await page.navigate(`${baseUrl}/servers/${serverId}/worlds`);
    await wait(2000);
    await page.screenshot("server-worlds");

    // Screenshot 6: Players
    await page.navigate(`${baseUrl}/servers/${serverId}/players`);
    await wait(2000);
    await page.screenshot("server-players");

    // Screenshot 7: Backups
    await page.navigate(`${baseUrl}/servers/${serverId}/backups`);
    await wait(2000);
    await page.screenshot("server-backups");

    // Screenshot 8: Files
    await page.navigate(`${baseUrl}/servers/${serverId}/files`);
    await wait(2000);
    await page.screenshot("server-files");

    // Screenshot 9: Settings
    await page.navigate(`${baseUrl}/servers/${serverId}/settings`);
    await wait(2000);
    await page.screenshot("server-settings");
  }

  // Screenshot 10: App Settings
  await page.navigate(`${baseUrl}/app-settings`);
  await wait(2000);
  await page.screenshot("app-settings");

  // Screenshot 11: Create server
  await page.navigate(`${baseUrl}/create`);
  await wait(2000);
  await page.screenshot("create-server");

  // Mobile screenshot
  console.log("\nCapturing mobile screenshot (390x844)...");
  await page.setViewport(390, 844);
  await page.navigate(baseUrl);
  await wait(2000);
  await page.screenshot("mobile-dashboard");

  console.log("\nAll screenshots saved to screenshots/");
} catch (error) {
  console.error("Error:", error.message);
  process.exitCode = 1;
} finally {
  if (chrome) await stopProcessTree(chrome);
  await rm(tempRoot, { recursive: true, force: true }).catch(() => {});
}
