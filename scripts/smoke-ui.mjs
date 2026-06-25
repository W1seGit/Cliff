import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const port = 3030;
const chromePort = 9230;
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "mc-dashboard-ui-"));
const dataDir = path.join(tempRoot, "data");
const serverRoot = path.join(tempRoot, "servers");
const chromeProfile = path.join(tempRoot, "chrome");
const distDir = path.join(root, "dist");
const smokeMinecraftVersion = "1.21.1";
const smokeSnapshotVersion = "24w21a";
const smokeFabricLoader = "0.16.10";

function npmCommand() {
  return process.platform === "win32" ? "npm.cmd" : "npm";
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

function runNpmScript(script) {
  const result = spawnSync(npmCommand(), ["run", script], {
    cwd: root,
    shell: process.platform === "win32",
    stdio: "inherit",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`npm run ${script} failed with status ${result.status}`);
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function stopProcessTree(child) {
  if (!child || child.exitCode !== null) return;
  if (process.platform === "win32") {
    spawnSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore" });
  } else {
    child.kill("SIGTERM");
  }
  await wait(1500);
}

async function waitForServer(child) {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) break;
    try {
      const response = await fetch(`http://127.0.0.1:${port}/`);
      if (response.ok) return;
    } catch {
      await wait(250);
    }
  }
  throw new Error("UI smoke server did not start");
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

async function waitForPageTarget() {
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

async function writeSmokeMetadataCache() {
  const cacheDir = path.join(dataDir, "cache");
  const loaderDir = path.join(cacheDir, "loaders");
  await mkdir(loaderDir, { recursive: true });
  const loaders = {
    vanilla: [],
    paper: [],
    fabric: [{ version: smokeFabricLoader, stable: true }],
    forge: [{ version: "52.1.0", stable: true }],
    neoforge: [{ version: "21.1.200", stable: true }],
  };
  await writeFile(path.join(cacheDir, "minecraft-metadata.json"), JSON.stringify({
    fetchedAt: new Date().toISOString(),
    latest: { release: smokeMinecraftVersion, snapshot: smokeSnapshotVersion },
    minecraftVersions: [
      { id: smokeSnapshotVersion, type: "snapshot", url: "https://example.invalid/snapshot.json", time: "2024-05-22T00:00:00Z", releaseTime: "2024-05-22T00:00:00Z" },
      { id: smokeMinecraftVersion, type: "release", url: "https://example.invalid/release.json", time: "2024-06-13T00:00:00Z", releaseTime: "2024-06-13T00:00:00Z" },
      { id: "1.20.6", type: "release", url: "https://example.invalid/1.20.6.json", time: "2024-04-29T00:00:00Z", releaseTime: "2024-04-29T00:00:00Z" },
      { id: "1.12.2", type: "release", url: "https://example.invalid/1.12.2.json", time: "2017-09-18T00:00:00Z", releaseTime: "2017-09-18T00:00:00Z" },
    ],
    loaders,
    loaderCatalog: loaders,
  }, null, 2));
  await writeFile(path.join(loaderDir, `fabric-${smokeMinecraftVersion}.json`), JSON.stringify(loaders.fabric, null, 2));
}

async function setupData() {
  const setupResponse = await fetch(`http://127.0.0.1:${port}/api/auth/setup`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ username: "qatest", password: "qa-password-1234" }),
  });
  if (!setupResponse.ok) throw new Error(`Setup failed: ${await setupResponse.text()}`);
  const cookie = setupResponse.headers.get("set-cookie")?.split(";")[0];
  if (!cookie) throw new Error("Setup did not return a session cookie");

  const versions = await fetch(`http://127.0.0.1:${port}/api/minecraft/versions`, {
    headers: { cookie },
  }).then((response) => response.json());
  const loaderPayload = await fetch(
    `http://127.0.0.1:${port}/api/minecraft/versions?type=fabric&minecraftVersion=${encodeURIComponent(versions.latest.release)}`,
    { headers: { cookie } },
  ).then((response) => response.json());
  const loaderVersion = loaderPayload.loaders?.[0]?.version;
  if (!versions.latest.release || !versions.latest.snapshot || !loaderVersion) {
    throw new Error(`Version metadata was incomplete: ${JSON.stringify({ versions, loaderPayload }).slice(0, 300)}`);
  }

  const importPath = path.join(tempRoot, "existing-fabric");
  await mkdir(path.join(importPath, "world", "playerdata"), { recursive: true });
  await mkdir(path.join(importPath, "world", "datapacks"), { recursive: true });
  await mkdir(path.join(importPath, "mods"), { recursive: true });
  await writeFile(
    path.join(importPath, "server.properties"),
    [
      "motd=QA Fabric Server",
      "level-name=world",
      "server-port=25565",
      "max-players=20",
      "view-distance=10",
      "simulation-distance=10",
      "gamemode=survival",
      "difficulty=normal",
      "white-list=false",
      "pvp=true",
      "online-mode=true",
      "enable-command-block=false",
      "allow-flight=false",
    ].join("\n"),
  );
  await writeFile(path.join(importPath, "eula.txt"), "eula=true");
  await writeFile(path.join(importPath, "fabric-server-mc.1.21.1-loader.0.16.10-launcher.jar"), "fake jar");
  await writeFile(path.join(importPath, "mods", "example-mod.jar"), "fake mod");
  await writeFile(path.join(importPath, "world", "level.dat"), "fake level");
  await writeFile(path.join(importPath, "world", "playerdata", "00000000-0000-0000-0000-000000000001.dat"), "fake player");
  await writeFile(path.join(importPath, "world", "datapacks", "qa-pack.zip"), "fake datapack");

  const createResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "content-type": "application/json", cookie },
    body: JSON.stringify({
      mode: "import",
      name: "QA Fabric Server",
      path: importPath,
      type: "fabric",
      minecraftVersion: versions.latest.release,
      loaderVersion,
      minMemoryMb: 2048,
      maxMemoryMb: 4096,
      port: 25565,
      javaPath: "java",
      launchJar: "fabric-server-mc.1.21.1-loader.0.16.10-launcher.jar",
      extraArgs: "nogui",
    }),
  });
  if (!createResponse.ok) throw new Error(`Server import failed: ${await createResponse.text()}`);
  const created = await createResponse.json();
  await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "content-type": "application/json", cookie },
    body: JSON.stringify({ reason: "UI smoke baseline" }),
  });

  return {
    cookie,
    serverId: created.server.id,
    latestRelease: versions.latest.release,
    latestSnapshot: versions.latest.snapshot,
    loaderVersion,
  };
}

async function browserSession(cookie, initialPath = "/") {
  const ws = new WebSocket(await waitForPageTarget());
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

  async function settle() {
    await wait(1400);
  }

  async function clickText(text) {
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const buttons = [...document.querySelectorAll("button,a")];
        const isVisible = (el) => {
          const rect = el.getBoundingClientRect();
          return rect.width > 0 && rect.height > 0 && rect.bottom > 0 && rect.right > 0 && rect.left < innerWidth && rect.top < innerHeight;
        };
        const exact = buttons.filter((el) => (el.innerText || el.textContent || "").trim() === ${JSON.stringify(text)});
        const partial = buttons
          .filter((el) => (el.innerText || el.textContent || "").trim().includes(${JSON.stringify(text)}))
          .sort((a, b) => (a.innerText || a.textContent || "").trim().length - (b.innerText || b.textContent || "").trim().length);
        const target = [...exact, ...partial].find((el) => !el.disabled && el.offsetParent !== null && isVisible(el));
        if (!target) return false;
        target.click();
        return true;
      })()`,
    });
    if (!result.result.value) throw new Error(`Could not click ${text}`);
    await settle();
  }

  async function clickSelector(selector) {
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const target = document.querySelector(${JSON.stringify(selector)});
        if (!target || target.disabled || target.offsetParent === null) return false;
        const rect = target.getBoundingClientRect();
        if (rect.width <= 0 || rect.height <= 0 || rect.bottom <= 0 || rect.right <= 0 || rect.left >= innerWidth || rect.top >= innerHeight) return false;
        target.click();
        return true;
      })()`,
    });
    if (!result.result.value) throw new Error(`Could not click ${selector}`);
    await settle();
  }

  async function clickTab(text) {
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const buttons = [...document.querySelectorAll(".tabs button")];
        const target = buttons.find((el) => (el.innerText || el.textContent || "").trim() === ${JSON.stringify(text)});
        if (!target || target.disabled) return false;
        const tabs = target.closest(".tabs");
        if (tabs) tabs.scrollLeft = target.offsetLeft - (tabs.clientWidth / 2) + (target.clientWidth / 2);
        window.scrollTo(0, window.scrollY);
        target.click();
        return true;
      })()`,
    });
    if (!result.result.value) throw new Error(`Could not click tab ${text}`);
    await settle();
  }

  async function inspect(width, height) {
    await send("Emulation.setDeviceMetricsOverride", {
      width,
      height,
      deviceScaleFactor: 1,
      mobile: width < 700,
    });
    await settle();
    await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
    const result = await send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const shell = document.querySelector(".shell");
        const overflow = [...document.querySelectorAll("body *")].filter((el) => {
          if (shell?.classList.contains("sidebar-collapsed") && el.closest(".sidebar")) return false;
          let parent = el.parentElement;
          while (parent) {
            const style = getComputedStyle(parent);
            if (["auto", "scroll"].includes(style.overflowX) && parent.scrollWidth > parent.clientWidth) return false;
            parent = parent.parentElement;
          }
          const rect = el.getBoundingClientRect();
          return rect.width > 0 && rect.height > 0 && (rect.right > innerWidth + 2 || rect.left < -2);
        }).slice(0, 10).map((el) => ({
          tag: el.tagName,
          className: String(el.className || ""),
          text: (el.innerText || el.textContent || "").trim().slice(0, 80),
          left: Math.round(el.getBoundingClientRect().left),
          right: Math.round(el.getBoundingClientRect().right),
        }));
        const sidebar = document.querySelector(".sidebar");
        const sidebarRect = sidebar?.getBoundingClientRect();
        const tabs = document.querySelector(".tabs");
        const tabStyle = tabs ? getComputedStyle(tabs) : null;
        const mobileButton = document.querySelector(".mobile-sidebar-button");
        const mobileButtonStyle = mobileButton ? getComputedStyle(mobileButton) : null;
        return {
          text: document.body.innerText,
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
          overflow,
          shellCollapsed: shell?.classList.contains("sidebar-collapsed") ?? false,
          sidebarLeft: sidebarRect ? Math.round(sidebarRect.left) : null,
          sidebarRight: sidebarRect ? Math.round(sidebarRect.right) : null,
          sidebarWidth: sidebarRect ? Math.round(sidebarRect.width) : null,
          backdropVisible: Boolean(document.querySelector(".sidebar-backdrop")),
          mobileButtonDisplay: mobileButtonStyle?.display ?? "",
          tabsDisplay: tabStyle?.display ?? "",
          tabsFlexWrap: tabStyle?.flexWrap ?? "",
          tabsOverflowX: tabStyle?.overflowX ?? "",
          tabsClientWidth: tabs?.clientWidth ?? 0,
          tabsScrollWidth: tabs?.scrollWidth ?? 0,
        };
      })()`,
    });
    return result.result.value;
  }

  await send("Page.enable");
  await send("Runtime.enable");
  await send("Log.enable");
  await send("Network.enable");
  const [name, value] = cookie.split("=");
  await send("Network.setCookie", {
    name,
    value,
    domain: "127.0.0.1",
    path: "/",
    httpOnly: true,
    sameSite: "Lax",
  });
  await send("Page.navigate", { url: `http://127.0.0.1:${port}${initialPath}` });
  await settle();

  return {
    close: () => ws.close(),
    clickText,
    clickSelector,
    clickTab,
    inspect,
    async waitForText(expected) {
      const deadline = Date.now() + 10_000;
      let lastReport = null;
      while (Date.now() < deadline) {
        const report = await inspect(1440, 1000);
        lastReport = report;
        if (report.text.includes(expected)) return report;
        await wait(250);
      }
      throw new Error(`Timed out waiting for ${expected}. Current text: ${(lastReport?.text ?? "").slice(0, 700)}${runtimeErrors.length > 0 ? `\nRuntime errors:\n${runtimeErrors.join("\n")}` : ""}`);
    },
    assertNoRuntimeErrors() {
      if (runtimeErrors.length > 0) throw new Error(`Browser runtime errors:\n${runtimeErrors.join("\n")}`);
    },
  };
}

function assertNoOverflow(name, report) {
  if (report.scrollWidth !== report.clientWidth || report.overflow.length > 0) {
    throw new Error(`${name} has horizontal overflow: document ${report.scrollWidth}/${report.clientWidth}; ${JSON.stringify(report.overflow).slice(0, 500)}`);
  }
}

function assertIncludes(name, text, values) {
  for (const value of values) {
    if (!text.includes(value)) throw new Error(`${name} did not include ${value}`);
  }
}

function assertExcludes(name, text, values) {
  for (const value of values) {
    if (text.includes(value)) throw new Error(`${name} still included ${value}`);
  }
}

function assertMobileNavClosed(name, report) {
  if (!report.shellCollapsed) throw new Error(`${name} expected collapsed shell`);
  if (report.mobileButtonDisplay === "none") throw new Error(`${name} expected mobile menu button to be visible`);
  if (typeof report.sidebarRight === "number" && report.sidebarRight > 2) throw new Error(`${name} expected sidebar off canvas, got right ${report.sidebarRight}`);
  if (report.backdropVisible) throw new Error(`${name} should not show sidebar backdrop while closed`);
  if (report.tabsDisplay && (report.tabsDisplay !== "flex" || report.tabsFlexWrap !== "nowrap")) {
    throw new Error(`${name} expected horizontal nowrap tabs, got ${report.tabsDisplay}/${report.tabsFlexWrap}`);
  }
}

function assertMobileNavOpen(name, report) {
  if (report.shellCollapsed) throw new Error(`${name} expected expanded shell`);
  if (typeof report.sidebarLeft === "number" && report.sidebarLeft < -2) throw new Error(`${name} expected sidebar visible, got left ${report.sidebarLeft}`);
  if (!report.backdropVisible) throw new Error(`${name} expected sidebar backdrop`);
}

let server;
let chrome;
let browser;

try {
  await mkdir(dataDir, { recursive: true });
  await mkdir(serverRoot, { recursive: true });
  await mkdir(chromeProfile, { recursive: true });
  runNpmScript("build");
  await writeSmokeMetadataCache();

  server = spawn(npmCommand(), ["run", "start", "--", "--host", "127.0.0.1", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot], {
    cwd: root,
    env: {
      ...process.env,
      CLIFF_HOST: "127.0.0.1",
      PORT: String(port),
      CLIFF_DATA_DIR: dataDir,
    },
    shell: process.platform === "win32",
    stdio: "ignore",
  });
  await waitForServer(server);

  const seeded = await setupData();
  const chromeExecutable = chromePath();
  if (!chromeExecutable) throw new Error("Chrome or Edge is required for UI smoke testing");
  chrome = spawn(chromeExecutable, [
    "--headless=new",
    `--remote-debugging-port=${chromePort}`,
    `--user-data-dir=${chromeProfile}`,
    "--no-first-run",
    "--disable-gpu",
    "about:blank",
  ], { stdio: "ignore" });

  browser = await browserSession(seeded.cookie);
  const overviewDesktop = await browser.waitForText("Live usage");
  assertNoOverflow("desktop overview", overviewDesktop);
  assertIncludes("desktop overview", overviewDesktop.text, ["Live usage", "Join address", "Health checks"]);
  assertExcludes("desktop overview", overviewDesktop.text, ["Search servers", "All states", "All health", "Version intelligence", "Workflows"]);

  await browser.clickText("Add server");
  await browser.clickText("Create server");
  const createDesktop = await browser.inspect(1440, 1200);
  assertNoOverflow("desktop create", createDesktop);
  assertIncludes("desktop create", createDesktop.text, [
    "Create server profile",
    "Server type",
    "Name",
    "Continue",
  ]);

  await browser.clickText("Add server");
  await browser.clickText("Import server");
  const importDesktop = await browser.inspect(1440, 1200);
  assertNoOverflow("desktop import", importDesktop);
  assertIncludes("desktop import", importDesktop.text, [
    "Import existing server",
    "Choose source",
    "Choose ZIP",
    "Choose folder",
    "managed storage",
  ]);

  await browser.clickText("QA Fabric Server");
  await browser.clickText("Mods");
  const modsDesktop = await browser.inspect(1440, 1200);
  assertNoOverflow("desktop mods", modsDesktop);
  assertIncludes("desktop mods", modsDesktop.text, ["Installed content", "Add mods", "Filter installed content", "Type", "Sort", "datapack", "File", "Scope", "Status", "Size", "Actions"]);

  await browser.clickText("Add mods");
  const addModsSidebar = await browser.inspect(1440, 1200);
  assertIncludes("desktop add mods sidebar", addModsSidebar.text, ["Add mods", "Upload file", "Modrinth", "CurseForge", "Filters", "Content", "Mods", "Modpacks", "Datapacks", "Version", "Loader", "Category", "Close"]);
  assertExcludes("desktop add mods sidebar", addModsSidebar.text, ["Limit"]);

  await browser.clickText("Close");

  await browser.clickText("Settings");
  const settingsDesktop = await browser.inspect(1440, 1200);
  assertNoOverflow("desktop server settings", settingsDesktop);
  assertIncludes("desktop server settings", settingsDesktop.text, [
    "Profile",
    "Server identity and Minecraft runtime.",
    "Game",
    "World, access, and gameplay properties.",
    "Tools",
    "Profile utilities and server removal.",
    seeded.latestRelease,
    "Clone server",
    "Danger zone",
    "Raw properties",
  ]);
  assertExcludes("desktop server settings", settingsDesktop.text, ["Advanced launch"]);

  await browser.clickText("Overview");
  const overviewMobile = await browser.inspect(390, 1200);
  assertNoOverflow("mobile overview", overviewMobile);
  assertMobileNavClosed("mobile overview", overviewMobile);
  assertIncludes("mobile overview", overviewMobile.text, ["Overview", "Live usage", "Health checks"]);
  assertExcludes("mobile overview", overviewMobile.text, ["Version intelligence", "Workflows"]);

  await browser.clickSelector(".mobile-sidebar-button");
  const mobileDrawerOpen = await browser.inspect(390, 1200);
  assertMobileNavOpen("mobile drawer open", mobileDrawerOpen);
  assertIncludes("mobile drawer open", mobileDrawerOpen.text, ["Cliff", "QA Fabric Server", "Add server", "App settings"]);

  await browser.clickText("QA Fabric Server");
  const mobileDrawerClosedAgain = await browser.inspect(390, 1200);
  assertMobileNavClosed("mobile drawer after server select", mobileDrawerClosedAgain);

  await browser.clickSelector(".mobile-sidebar-button");
  await browser.clickText("Add server");
  await browser.clickText("Create server");
  const createMobile = await browser.inspect(390, 1200);
  assertNoOverflow("mobile create", createMobile);
  assertMobileNavClosed("mobile create", createMobile);
  assertIncludes("mobile create", createMobile.text, ["Create server profile", "Server type", "Continue"]);

  await browser.clickSelector(".mobile-sidebar-button");
  await browser.clickText("Add server");
  await browser.clickText("Import server");
  const importMobile = await browser.inspect(390, 1200);
  assertNoOverflow("mobile import", importMobile);
  assertMobileNavClosed("mobile import", importMobile);
  assertIncludes("mobile import", importMobile.text, ["Import existing server", "Choose source", "Choose ZIP", "Choose folder"]);

  await browser.clickSelector(".mobile-sidebar-button");
  await browser.clickText("QA Fabric Server");
  await browser.clickTab("Mods");
  const modsMobile = await browser.inspect(390, 1200);
  assertNoOverflow("mobile mods", modsMobile);
  assertMobileNavClosed("mobile mods", modsMobile);
  assertIncludes("mobile mods", modsMobile.text, ["Installed content", "Add mods", "Filter installed content"]);

  await browser.clickTab("Settings");
  const settingsMobile = await browser.inspect(390, 1200);
  assertNoOverflow("mobile settings", settingsMobile);
  assertMobileNavClosed("mobile settings", settingsMobile);
  assertIncludes("mobile settings", settingsMobile.text, ["Profile", "Game", "Tools", "Danger zone"]);

  console.log("UI smoke test passed");
} finally {
  browser?.close();
  await stopProcessTree(chrome);
  await stopProcessTree(server);
  await rm(tempRoot, { recursive: true, force: true });
  await rm(distDir, { recursive: true, force: true });
}
