import { spawn, spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { mkdir, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import AdmZip from "adm-zip";

const root = process.cwd();
const dataDir = path.join(root, `.auth-smoke-data-${Date.now()}`);
const serverRoot = path.join(root, `.auth-smoke-servers-${Date.now()}`);
const distDir = path.join(root, "dist");
const port = 3020;
const smokeMinecraftReleases = [
  "1.21.1",
  "1.20.6",
  "1.20.4",
  "1.20.2",
  "1.20.1",
  "1.19.4",
  "1.19.2",
  "1.18.2",
  "1.17.1",
  "1.16.5",
  "1.12.2",
  "1.8.9",
];
const smokeMinecraftSnapshots = ["24w21a", "24w20a", "24w19a"];
const smokeLoaders = {
  vanilla: [],
  paper: [],
  fabric: [{ version: "0.16.14", stable: true }],
  forge: [{ version: "52.1.0", stable: true }],
  neoforge: [{ version: "21.1.200", stable: true }],
};

function npmCommand() {
  return process.platform === "win32" ? "npm.cmd" : "npm";
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

async function wait(ms) {
  await new Promise((resolve) => setTimeout(resolve, ms));
}

async function stopProcessTree(child) {
  if (child.exitCode !== null) return;
  if (process.platform === "win32") {
    spawn("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore" });
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
  throw new Error("Auth smoke server did not start");
}

function parseCookie(headers) {
  const cookie = headers.get("set-cookie");
  return cookie ? cookie.split(";")[0] : "";
}

async function getText(pathname, cookie = "") {
  const response = await fetch(`http://127.0.0.1:${port}${pathname}`, {
    headers: cookie ? { cookie } : undefined,
  });
  return response.text();
}

async function postJson(pathname, body, cookie = "") {
  return fetch(`http://127.0.0.1:${port}${pathname}`, {
    method: "POST",
    redirect: "manual",
    headers: { "Content-Type": "application/json", ...(cookie ? { cookie } : {}) },
    body: JSON.stringify(body),
  });
}

async function detectUploadedFolder(name, files, cookie = "") {
  const form = new FormData();
  form.set("mode", "detect-folder");
  form.set("name", name);
  form.set("paths", JSON.stringify(files.map((file) => file.path)));
  for (const file of files) {
    form.append("files", new Blob([Buffer.from(file.contents)], { type: "application/octet-stream" }), path.basename(file.path));
  }
  return fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: cookie ? { cookie } : undefined,
    body: form,
  });
}

function smokeMinecraftOption(id, type, index) {
  const month = String((index % 9) + 1).padStart(2, "0");
  const day = String((index % 20) + 1).padStart(2, "0");
  return {
    id,
    type,
    url: `http://127.0.0.1:${port}/__smoke-minecraft/${encodeURIComponent(id)}.json`,
    time: `2024-${month}-${day}T00:00:00Z`,
    releaseTime: `2024-${month}-${day}T00:00:00Z`,
  };
}

function loaderCacheFileName(type, minecraftVersion) {
  const safeVersion = minecraftVersion.replace(/[^a-zA-Z0-9._-]/g, "-");
  return `${type}-${safeVersion}.json`;
}

async function writeSmokeMinecraftFixture() {
  const staticDir = path.join(distDir, "cliff", "web", "__smoke-minecraft");
  const cacheDir = path.join(dataDir, "cache");
  const loaderDir = path.join(cacheDir, "loaders");
  await mkdir(staticDir, { recursive: true });
  await mkdir(loaderDir, { recursive: true });

  const fakeJarName = "server.jar";
  await writeFile(path.join(staticDir, fakeJarName), Buffer.alloc(1024 * 1024 + 8, "s"));
  const versionDetails = {
    downloads: {
      server: {
        url: `http://127.0.0.1:${port}/__smoke-minecraft/${fakeJarName}`,
        sha1: "smoke",
        size: 1024 * 1024 + 8,
      },
    },
  };
  for (const version of [...smokeMinecraftReleases, ...smokeMinecraftSnapshots]) {
    await writeFile(path.join(staticDir, `${version}.json`), JSON.stringify(versionDetails, null, 2));
  }

  const minecraftVersions = [
    ...smokeMinecraftSnapshots.map((version, index) => smokeMinecraftOption(version, "snapshot", index)),
    ...smokeMinecraftReleases.map((version, index) => smokeMinecraftOption(version, "release", index + smokeMinecraftSnapshots.length)),
  ];
  const metadata = {
    fetchedAt: new Date().toISOString(),
    latest: { release: smokeMinecraftReleases[0], snapshot: smokeMinecraftSnapshots[0] },
    minecraftVersions,
    loaders: smokeLoaders,
    loaderCatalog: smokeLoaders,
  };
  await writeFile(path.join(cacheDir, "minecraft-metadata.json"), JSON.stringify(metadata, null, 2));
  for (const version of smokeMinecraftReleases) {
    for (const [type, loaders] of Object.entries(smokeLoaders)) {
      if (type === "vanilla") continue;
      await writeFile(path.join(loaderDir, loaderCacheFileName(type, version)), JSON.stringify(loaders, null, 2));
    }
  }
}

async function firstSupportedLoaderRelease({ type, versions, cookie }) {
  const recentReleases = versions.minecraftVersions.filter((version) => version.type === "release").slice(0, 12);
  for (const release of recentReleases) {
    const response = await fetch(
      `http://127.0.0.1:${port}/api/minecraft/versions?type=${type}&minecraftVersion=${encodeURIComponent(release.id)}`,
      { headers: { cookie } },
    );
    if (!response.ok) throw new Error(`${type} loader dropdown API failed for ${release.id}: ${await response.text()}`);
    const payload = await response.json();
    if (Array.isArray(payload.loaders) && payload.loaders[0]?.version) {
      return { minecraftVersion: release.id, loaders: payload.loaders };
    }
  }
  throw new Error(`${type} loader dropdown API returned no compatible loaders for the latest ${recentReleases.length} releases`);
}

runNpmScript("build");
await writeSmokeMinecraftFixture();

const child = spawn(npmCommand(), ["run", "start", "--", "--host", "127.0.0.1", "--port", String(port), "--data-dir", dataDir, "--server-root", serverRoot], {
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

try {
  await waitForServer(child);

  const firstPage = await getText("/");
  if (!firstPage.includes("Loading dashboard")) throw new Error("First page did not render the static auth shell");
  if (!firstPage.includes("<title>Minecraft Cliff</title>")) {
    throw new Error("First page did not include branded browser title");
  }
  if (!firstPage.includes('rel="manifest"') || !firstPage.includes("/manifest.webmanifest")) {
    throw new Error("First page did not include app manifest link");
  }
  const iconResponse = await fetch(`http://127.0.0.1:${port}/icon.svg`);
  if (!iconResponse.ok || !iconResponse.headers.get("content-type")?.includes("image/svg+xml")) {
    throw new Error(`Icon route failed: ${iconResponse.status} ${iconResponse.headers.get("content-type")}`);
  }
  const manifestResponse = await fetch(`http://127.0.0.1:${port}/manifest.webmanifest`);
  if (!manifestResponse.ok) throw new Error(`Manifest route failed: ${await manifestResponse.text()}`);
  const manifest = await manifestResponse.json();
  if (manifest.name !== "Minecraft Cliff" || manifest.short_name !== "Cliff" || manifest.theme_color !== "#111315") {
    throw new Error(`Manifest did not expose branded app identity: ${JSON.stringify(manifest).slice(0, 300)}`);
  }
  const serviceWorkerResponse = await fetch(`http://127.0.0.1:${port}/app-sw.js`);
  if (!serviceWorkerResponse.ok) throw new Error(`App service worker route failed: ${await serviceWorkerResponse.text()}`);
  if (!serviceWorkerResponse.headers.get("content-type")?.includes("javascript")) {
    throw new Error(`App service worker returned unexpected content type: ${serviceWorkerResponse.headers.get("content-type")}`);
  }

  const setupState = await fetch(`http://127.0.0.1:${port}/api/auth/me`).then((response) => response.json());
  if (!setupState.needsSetup || setupState.user) {
    throw new Error(`Fresh auth state did not require setup: ${JSON.stringify(setupState)}`);
  }

  const setupResponse = await postJson("/api/auth/setup", { username: "Leo", password: "temporary-pass-123" });
  const sessionCookie = parseCookie(setupResponse.headers);
  if (!sessionCookie) throw new Error("Setup did not set a session cookie");

  const meAfterSetup = await fetch(`http://127.0.0.1:${port}/api/auth/me`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!meAfterSetup.user || meAfterSetup.needsSetup) {
    throw new Error(`Session cookie did not authenticate: ${JSON.stringify(meAfterSetup)}`);
  }

  const dashboardPage = await getText("/", sessionCookie);
  if (!dashboardPage.includes("Loading dashboard")) {
    throw new Error(`Authenticated page did not render static dashboard shell: ${dashboardPage.slice(0, 500)}`);
  }

  const versions = await fetch(`http://127.0.0.1:${port}/api/minecraft/versions`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!versions.latest?.release || !Array.isArray(versions.minecraftVersions) || versions.minecraftVersions.length < 10) {
    throw new Error(`Minecraft versions API did not return valid dropdown data: ${JSON.stringify(versions).slice(0, 300)}`);
  }
  if (versions.latest.snapshot !== versions.latest.release && !versions.minecraftVersions.some((version) => version.id === versions.latest.snapshot && version.type === "snapshot")) {
    throw new Error(`Minecraft versions API did not include latest snapshot in dropdown data: ${JSON.stringify(versions).slice(0, 300)}`);
  }
  const snapshotOptions = versions.minecraftVersions.filter((version) => version.type === "snapshot");
  if (snapshotOptions.length < 2 || snapshotOptions.length > 25) {
    throw new Error(`Minecraft versions API did not include bounded recent snapshot dropdown data: ${JSON.stringify(snapshotOptions).slice(0, 300)}`);
  }
  if (!versions.minecraftVersions.some((version) => version.id === "1.12.2" && version.type === "release")) {
    throw new Error(`Minecraft versions API did not include historic Java releases for import/edit dropdowns: ${JSON.stringify(versions).slice(0, 300)}`);
  }
  const latestReleaseOption = versions.minecraftVersions.find((version) => version.id === versions.latest.release);
  if (!latestReleaseOption?.releaseTime || Number.isNaN(Date.parse(latestReleaseOption.releaseTime))) {
    throw new Error(`Minecraft versions API did not include a valid release timestamp for dropdown details: ${JSON.stringify(latestReleaseOption).slice(0, 300)}`);
  }
  if (!Array.isArray(versions.loaders?.fabric) || !versions.loaders.fabric[0]?.version) {
    throw new Error(`Minecraft metadata did not preload Fabric loaders for the latest release: ${JSON.stringify(versions).slice(0, 300)}`);
  }
  const refreshedVersions = await fetch(`http://127.0.0.1:${port}/api/minecraft/versions?refresh=1`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (refreshedVersions.latest?.release !== versions.latest.release || !refreshedVersions.fetchedAt) {
    throw new Error(`Minecraft metadata refresh did not return valid current data: ${JSON.stringify(refreshedVersions).slice(0, 300)}`);
  }
  const fabricLoaderResponse = await fetch(
    `http://127.0.0.1:${port}/api/minecraft/versions?type=fabric&minecraftVersion=${encodeURIComponent(versions.latest.release)}`,
    { headers: { cookie: sessionCookie } },
  );
  if (!fabricLoaderResponse.ok) throw new Error(`Fabric loader dropdown API failed: ${await fabricLoaderResponse.text()}`);
  const fabricLoaderPayload = await fabricLoaderResponse.json();
  if (!Array.isArray(fabricLoaderPayload.loaders) || !fabricLoaderPayload.loaders[0]?.version) {
    throw new Error(`Fabric loader dropdown API returned invalid data: ${JSON.stringify(fabricLoaderPayload).slice(0, 300)}`);
  }
  const refreshedFabricLoaderResponse = await fetch(
    `http://127.0.0.1:${port}/api/minecraft/versions?type=fabric&minecraftVersion=${encodeURIComponent(versions.latest.release)}&refresh=1`,
    { headers: { cookie: sessionCookie } },
  );
  if (!refreshedFabricLoaderResponse.ok) throw new Error(`Fabric loader refresh API failed: ${await refreshedFabricLoaderResponse.text()}`);
  const refreshedFabricLoaderPayload = await refreshedFabricLoaderResponse.json();
  if (!refreshedFabricLoaderPayload.loaders?.some((loader) => loader.version === fabricLoaderPayload.loaders[0].version)) {
    throw new Error(`Fabric loader refresh did not return compatible loader data: ${JSON.stringify(refreshedFabricLoaderPayload).slice(0, 300)}`);
  }
  const alternateRelease = versions.minecraftVersions.find(
    (version) => version.type === "release" && version.id !== versions.latest.release,
  );
  if (!alternateRelease) throw new Error("Minecraft versions API did not provide a second release for loader dropdown testing");
  const alternateFabricLoaderResponse = await fetch(
    `http://127.0.0.1:${port}/api/minecraft/versions?type=fabric&minecraftVersion=${encodeURIComponent(alternateRelease.id)}`,
    { headers: { cookie: sessionCookie } },
  );
  if (!alternateFabricLoaderResponse.ok) {
    throw new Error(`Fabric loader dropdown API failed for ${alternateRelease.id}: ${await alternateFabricLoaderResponse.text()}`);
  }
  const alternateFabricLoaderPayload = await alternateFabricLoaderResponse.json();
  if (!Array.isArray(alternateFabricLoaderPayload.loaders) || !alternateFabricLoaderPayload.loaders[0]?.version) {
    throw new Error(`Fabric loader dropdown API returned invalid data for ${alternateRelease.id}: ${JSON.stringify(alternateFabricLoaderPayload).slice(0, 300)}`);
  }
  const forgeLoaderPayload = await firstSupportedLoaderRelease({ type: "forge", versions, cookie: sessionCookie });
  if (!forgeLoaderPayload.loaders[0]?.version.includes(".")) {
    throw new Error(`Forge loader dropdown API returned invalid loader data: ${JSON.stringify(forgeLoaderPayload).slice(0, 300)}`);
  }
  const neoforgeLoaderPayload = await firstSupportedLoaderRelease({ type: "neoforge", versions, cookie: sessionCookie });
  if (!neoforgeLoaderPayload.loaders[0]?.version.includes(".")) {
    throw new Error(`NeoForge loader dropdown API returned invalid loader data: ${JSON.stringify(neoforgeLoaderPayload).slice(0, 300)}`);
  }
  const invalidLoaderTypeResponse = await fetch(
    `http://127.0.0.1:${port}/api/minecraft/versions?type=bad&minecraftVersion=${encodeURIComponent(versions.latest.release)}`,
    { headers: { cookie: sessionCookie } },
  );
  if (invalidLoaderTypeResponse.status !== 400) {
    throw new Error(`Invalid loader type was not rejected: ${invalidLoaderTypeResponse.status} ${await invalidLoaderTypeResponse.text()}`);
  }
  const settingsPayload = await fetch(`http://127.0.0.1:${port}/api/settings`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!settingsPayload.storage || typeof settingsPayload.storage.serverRootSizeBytes !== "number" || typeof settingsPayload.storage.backupCount !== "number") {
    throw new Error(`Settings API did not return storage usage: ${JSON.stringify(settingsPayload).slice(0, 300)}`);
  }
  if (
    !settingsPayload.access ||
    !Array.isArray(settingsPayload.access.lanAddresses) ||
    !Array.isArray(settingsPayload.access.devUrls) ||
    !Array.isArray(settingsPayload.access.productionUrls)
  ) {
    throw new Error(`Settings API did not return LAN access info: ${JSON.stringify(settingsPayload).slice(0, 300)}`);
  }
  const accountRenameResponse = await fetch(`http://127.0.0.1:${port}/api/auth/account`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ username: "Leo Admin", currentPassword: "", newPassword: "" }),
  });
  if (!accountRenameResponse.ok) throw new Error(`Account rename failed: ${await accountRenameResponse.text()}`);
  const renamedAccount = await accountRenameResponse.json();
  if (renamedAccount.user?.username !== "Leo Admin" || renamedAccount.user?.id !== meAfterSetup.user.id) {
    throw new Error(`Account rename returned invalid user payload: ${JSON.stringify(renamedAccount).slice(0, 300)}`);
  }
  const badPasswordChangeResponse = await fetch(`http://127.0.0.1:${port}/api/auth/account`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ username: "Leo Admin", currentPassword: "wrong-password", newPassword: "permanent-pass-456" }),
  });
  if (badPasswordChangeResponse.ok) throw new Error("Password change unexpectedly succeeded with the wrong current password");
  const passwordChangeResponse = await fetch(`http://127.0.0.1:${port}/api/auth/account`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ username: "Leo Admin", currentPassword: "temporary-pass-123", newPassword: "permanent-pass-456" }),
  });
  if (!passwordChangeResponse.ok) throw new Error(`Password change failed: ${await passwordChangeResponse.text()}`);
  const passwordChangedAccount = await passwordChangeResponse.json();
  if (passwordChangedAccount.user?.username !== "Leo Admin" || passwordChangedAccount.user?.id !== meAfterSetup.user.id) {
    throw new Error(`Password change returned invalid user payload: ${JSON.stringify(passwordChangedAccount).slice(0, 300)}`);
  }
  const invalidSettingsResponse = await fetch(`http://127.0.0.1:${port}/api/settings`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ serverRoot: "   ", snapshotsEnabled: true, curseForgeApiKey: "" }),
  });
  if (invalidSettingsResponse.ok) throw new Error("Blank server root settings update unexpectedly succeeded");
  const createResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "create",
      name: "Smoke Vanilla",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      minMemoryMb: 1024,
      maxMemoryMb: 2048,
      port: 25569,
      javaPath: " java ",
      extraArgs: "  --nogui  ",
    }),
  });
  if (!createResponse.ok) throw new Error(`Server create failed: ${await createResponse.text()}`);
  const created = await createResponse.json();
  if (created.server?.port !== 25569) {
    throw new Error(`Server create did not preserve custom port: ${JSON.stringify(created).slice(0, 300)}`);
  }
  if (created.server?.javaPath !== "java" || created.server?.extraArgs !== "--nogui") {
    throw new Error(`Server create did not normalize launch fields: ${JSON.stringify(created).slice(0, 300)}`);
  }
  const serverJar = path.join(serverRoot, "Smoke-Vanilla", "server.jar");
  const jarStats = await stat(serverJar);
  if (jarStats.size < 1024 * 1024) throw new Error("Vanilla server jar was not downloaded");
  const createdProperties = readFileSync(path.join(created.server.path, "server.properties"), "utf8");
  if (!createdProperties.includes("server-port=25569")) throw new Error("Created server.properties did not use the requested port");
  const healthResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/health`, {
    headers: { cookie: sessionCookie },
  });
  if (!healthResponse.ok) throw new Error(`Server health failed: ${await healthResponse.text()}`);
  const healthPayload = await healthResponse.json();
  if (!Array.isArray(healthPayload.health?.checks) || !healthPayload.health.checks.some((check) => check.id === "launch" && check.state === "ok")) {
    throw new Error(`Server health did not report a valid launch target: ${JSON.stringify(healthPayload).slice(0, 300)}`);
  }
  const createdPropertiesPath = path.join(created.server.path, "server.properties");
  await writeFile(createdPropertiesPath, "server-port=25568\nlevel-name=world\n", "utf8");
  const portMismatchHealth = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/health`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!portMismatchHealth.health?.checks?.some((check) => check.id === "port" && check.state === "warn")) {
    throw new Error(`Server health did not warn about profile/server.properties port drift: ${JSON.stringify(portMismatchHealth).slice(0, 300)}`);
  }
  await writeFile(createdPropertiesPath, "server-port=25569\nlevel-name=world\n", "utf8");
  const badJavaPath = path.join(serverRoot, "missing-java", "bin", process.platform === "win32" ? "java.exe" : "java");
  const badJavaProfileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ javaPath: badJavaPath }),
  });
  if (!badJavaProfileResponse.ok) throw new Error(`Bad Java profile update failed: ${await badJavaProfileResponse.text()}`);
  const badJavaHealth = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/health`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!badJavaHealth.health?.checks?.some((check) => check.id === "java" && check.state === "error")) {
    throw new Error(`Server health did not block a missing custom Java runtime: ${JSON.stringify(badJavaHealth).slice(0, 300)}`);
  }
  const restoreJavaProfileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ javaPath: "java" }),
  });
  if (!restoreJavaProfileResponse.ok) throw new Error(`Java profile restore failed: ${await restoreJavaProfileResponse.text()}`);
  const duplicateCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "create",
      name: "Smoke Vanilla",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      minMemoryMb: 1024,
      maxMemoryMb: 2048,
    }),
  });
  if (!duplicateCreateResponse.ok) throw new Error(`Duplicate managed server create failed: ${await duplicateCreateResponse.text()}`);
  const duplicateCreated = await duplicateCreateResponse.json();
  if (!duplicateCreated.server?.id || duplicateCreated.server.id === created.server.id || duplicateCreated.server.path === created.server.path) {
    throw new Error(`Duplicate managed server create did not allocate a distinct profile/path: ${JSON.stringify(duplicateCreated).slice(0, 300)}`);
  }
  const invalidMemoryCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "create",
      name: "Bad Memory Smoke",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      minMemoryMb: 4096,
      maxMemoryMb: 1024,
    }),
  });
  if (invalidMemoryCreateResponse.ok) throw new Error("Invalid memory server create unexpectedly succeeded");
  const invalidPortCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "create",
      name: "Bad Port Smoke",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      minMemoryMb: 1024,
      maxMemoryMb: 2048,
      port: 70000,
    }),
  });
  if (invalidPortCreateResponse.ok) throw new Error("Invalid port server create unexpectedly succeeded");
  const invalidVersionCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "create",
      name: "Bad Version Smoke",
      type: "vanilla",
      minecraftVersion: "0.0-smoke",
      minMemoryMb: 1024,
      maxMemoryMb: 2048,
    }),
  });
  if (invalidVersionCreateResponse.ok) throw new Error("Invalid Minecraft version create unexpectedly succeeded");
  const invalidLoaderCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "create",
      name: "Bad Loader Smoke",
      type: "fabric",
      minecraftVersion: versions.latest.release,
      loaderVersion: "bad-loader-smoke",
      minMemoryMb: 1024,
      maxMemoryMb: 2048,
    }),
  });
  if (invalidLoaderCreateResponse.ok) throw new Error("Invalid loader create unexpectedly succeeded");

  const importPath = path.join(serverRoot, "Imported-Smoke");
  await mkdir(importPath, { recursive: true });
  await writeFile(path.join(importPath, "server.properties"), "server-port=25570\nlevel-name=world\n", "utf8");
  await writeFile(path.join(importPath, "server.jar"), "placeholder", "utf8");
  const detectResponse = await detectUploadedFolder("Imported Smoke", [
    { path: "Imported-Smoke/server.properties", contents: "server-port=25570\nlevel-name=world\n" },
    { path: "Imported-Smoke/server.jar", contents: "placeholder" },
  ], sessionCookie);
  if (!detectResponse.ok) throw new Error(`Server folder detect failed: ${await detectResponse.text()}`);
  const detected = await detectResponse.json();
  if (
    detected.detection?.type !== "vanilla" ||
    detected.detection?.launchJar !== "server.jar" ||
    detected.detection?.minecraftVersion !== versions.latest.release ||
    detected.detection?.loaderVersion !== "" ||
    detected.detection?.port !== 25570 ||
    detected.detection?.activeWorld !== "world" ||
    detected.detection?.alreadyRegistered
  ) {
    throw new Error(`Server folder detect returned unexpected data: ${JSON.stringify(detected).slice(0, 300)}`);
  }
  const forgeDetectPath = path.join(serverRoot, "Forge-Detect-Smoke");
  await mkdir(forgeDetectPath, { recursive: true });
  await writeFile(path.join(forgeDetectPath, "server.properties"), "server-port=25576\nlevel-name=forge-world\n", "utf8");
  await writeFile(path.join(forgeDetectPath, "forge-1.20.1-47.2.0.jar"), "placeholder", "utf8");
  const forgeDetectResponse = await detectUploadedFolder("Forge Detect Smoke", [
    { path: "Forge-Detect-Smoke/server.properties", contents: "server-port=25576\nlevel-name=forge-world\n" },
    { path: "Forge-Detect-Smoke/forge-1.20.1-47.2.0.jar", contents: "placeholder" },
  ], sessionCookie);
  if (!forgeDetectResponse.ok) throw new Error(`Forge folder detect failed: ${await forgeDetectResponse.text()}`);
  const forgeDetected = await forgeDetectResponse.json();
  if (
    forgeDetected.detection?.type !== "forge" ||
    forgeDetected.detection?.minecraftVersion !== "1.20.1" ||
    forgeDetected.detection?.loaderVersion !== "47.2.0" ||
    forgeDetected.detection?.launchJar !== "forge-1.20.1-47.2.0.jar"
  ) {
    throw new Error(`Forge folder detect returned unexpected profile data: ${JSON.stringify(forgeDetected).slice(0, 300)}`);
  }
  const importResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: importPath,
      name: "Imported Smoke",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      launchJar: "server.jar",
      port: 25577,
      javaPath: "custom-java",
      extraArgs: "-Dsmoke.import=true",
    }),
  });
  if (!importResponse.ok) throw new Error(`Server import failed: ${await importResponse.text()}`);
  const imported = await importResponse.json();
  if (imported.server?.port !== 25577) {
    throw new Error(`Server import did not preserve custom port override: ${JSON.stringify(imported).slice(0, 300)}`);
  }
  if (imported.server?.javaPath !== "custom-java" || imported.server?.extraArgs !== "-Dsmoke.import=true") {
    throw new Error(`Server import did not preserve launch fields: ${JSON.stringify(imported).slice(0, 300)}`);
  }
  const zipArchive = new AdmZip();
  zipArchive.addFile("Zip-Smoke/server.properties", Buffer.from("server-port=25578\nlevel-name=world\n"));
  zipArchive.addFile("Zip-Smoke/server.jar", Buffer.from("zip launch jar"));
  zipArchive.addFile("Zip-Smoke/mods/zip-mod.jar", Buffer.from("zip mod jar"));
  zipArchive.addFile("Zip-Smoke/world/level.dat", Buffer.from("zip level data"));
  zipArchive.addFile("Zip-Smoke/world/playerdata/player.dat", Buffer.from("zip player data"));
  zipArchive.addFile("Zip-Smoke/world/datapacks/zip-pack.zip", Buffer.from("zip datapack"));
  const zipImportForm = new FormData();
  zipImportForm.set("mode", "import-zip");
  zipImportForm.set("name", "Zip Imported Smoke");
  zipImportForm.set("type", "vanilla");
  zipImportForm.set("minecraftVersion", versions.latest.release);
  zipImportForm.set("launchJar", "server.jar");
  zipImportForm.set("port", "25579");
  zipImportForm.set("javaPath", "zip-java");
  zipImportForm.set("extraArgs", "-Dsmoke.zip=true");
  zipImportForm.set("minMemoryMb", "1024");
  zipImportForm.set("maxMemoryMb", "2048");
  zipImportForm.set("file", new Blob([zipArchive.toBuffer()], { type: "application/zip" }), "Zip-Smoke.zip");
  const zipImportResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: zipImportForm,
  });
  if (!zipImportResponse.ok) throw new Error(`Server ZIP import failed: ${await zipImportResponse.text()}`);
  const zipImported = await zipImportResponse.json();
  if (
    zipImported.server?.name !== "Zip Imported Smoke" ||
    zipImported.server?.port !== 25579 ||
    zipImported.server?.javaPath !== "zip-java" ||
    zipImported.server?.extraArgs !== "-Dsmoke.zip=true" ||
    zipImported.server?.launchJar !== "server.jar"
  ) {
    throw new Error(`Server ZIP import did not preserve profile fields: ${JSON.stringify(zipImported).slice(0, 300)}`);
  }
  for (const relativePath of [
    "server.properties",
    "server.jar",
    "mods/zip-mod.jar",
    "world/level.dat",
    "world/playerdata/player.dat",
    "world/datapacks/zip-pack.zip",
  ]) {
    if (!existsSync(path.join(zipImported.server.path, relativePath))) {
      throw new Error(`Server ZIP import did not extract ${relativePath}`);
    }
  }
  if (path.resolve(imported.server.path) === path.resolve(importPath)) {
    throw new Error(`Folder import reused the source folder instead of copying into managed storage: ${JSON.stringify(imported).slice(0, 300)}`);
  }
  if (!existsSync(path.join(importPath, "server.jar")) || !existsSync(path.join(imported.server.path, "server.jar"))) {
    throw new Error("Folder import did not keep both source and managed copy available");
  }
  const fileImportResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: path.join(importPath, "server.jar"),
      name: "File Import Smoke",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      launchJar: "server.jar",
    }),
  });
  if (fileImportResponse.ok) throw new Error("File path server import unexpectedly succeeded");
  const invalidImportVersionPath = path.join(serverRoot, "Invalid-Import-Version-Smoke");
  await mkdir(invalidImportVersionPath, { recursive: true });
  await writeFile(path.join(invalidImportVersionPath, "server.properties"), "server-port=25574\nlevel-name=world\n", "utf8");
  await writeFile(path.join(invalidImportVersionPath, "server.jar"), "placeholder", "utf8");
  const invalidImportVersionResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: invalidImportVersionPath,
      name: "Invalid Import Version Smoke",
      type: "vanilla",
      minecraftVersion: "0.0-smoke",
      launchJar: "server.jar",
    }),
  });
  if (invalidImportVersionResponse.ok) throw new Error("Invalid Minecraft version import unexpectedly succeeded");
  const invalidImportLoaderPath = path.join(serverRoot, "Invalid-Import-Loader-Smoke");
  await mkdir(invalidImportLoaderPath, { recursive: true });
  await writeFile(path.join(invalidImportLoaderPath, "server.properties"), "server-port=25575\nlevel-name=world\n", "utf8");
  await writeFile(path.join(invalidImportLoaderPath, "fabric-server-launch.jar"), "placeholder", "utf8");
  const invalidImportLoaderResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: invalidImportLoaderPath,
      name: "Invalid Import Loader Smoke",
      type: "fabric",
      minecraftVersion: versions.latest.release,
      loaderVersion: "bad-loader-smoke",
      launchJar: "fabric-server-launch.jar",
    }),
  });
  if (invalidImportLoaderResponse.ok) throw new Error("Invalid loader import unexpectedly succeeded");
  const missingLoaderImportPath = path.join(serverRoot, "Missing-Loader-Smoke");
  await mkdir(missingLoaderImportPath, { recursive: true });
  await writeFile(path.join(missingLoaderImportPath, "server.properties"), "server-port=25572\nlevel-name=world\n", "utf8");
  await writeFile(path.join(missingLoaderImportPath, "fabric-server-launch.jar"), "placeholder", "utf8");
  const missingLoaderImportResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: missingLoaderImportPath,
      name: "Missing Loader Smoke",
      type: "fabric",
      minecraftVersion: versions.latest.release,
      launchJar: "fabric-server-launch.jar",
    }),
  });
  if (missingLoaderImportResponse.ok) throw new Error("Fabric import without loader unexpectedly succeeded");
  const fabricLoader = versions.loaders?.fabric?.[0]?.version;
  if (!fabricLoader) throw new Error("Minecraft metadata did not include a Fabric loader for import testing");
  const fabricImportPath = path.join(serverRoot, "Imported-Fabric-Smoke");
  await mkdir(fabricImportPath, { recursive: true });
  await writeFile(path.join(fabricImportPath, "server.properties"), "server-port=25571\nlevel-name=world\n", "utf8");
  await writeFile(path.join(fabricImportPath, "fabric-server-launch.jar"), "placeholder", "utf8");
  const fabricImportResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: fabricImportPath,
      name: "Imported Fabric Smoke",
      type: "fabric",
      minecraftVersion: versions.latest.release,
      loaderVersion: fabricLoader,
      launchJar: "fabric-server-launch.jar",
    }),
  });
  if (!fabricImportResponse.ok) throw new Error(`Fabric server import failed: ${await fabricImportResponse.text()}`);
  const fabricImported = await fabricImportResponse.json();
  if (fabricImported.server?.loaderVersion !== fabricLoader || fabricImported.server?.type !== "fabric") {
    throw new Error(`Fabric import did not preserve loader metadata: ${JSON.stringify(fabricImported).slice(0, 300)}`);
  }
  const trimmedImportPath = path.join(serverRoot, "Trimmed-Import-Smoke");
  await mkdir(trimmedImportPath, { recursive: true });
  await writeFile(path.join(trimmedImportPath, "server.properties"), "server-port=25573\nlevel-name=world\n", "utf8");
  await writeFile(path.join(trimmedImportPath, "server.jar"), "placeholder", "utf8");
  const trimmedImportResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: trimmedImportPath,
      name: "   Trimmed Import   ",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      launchJar: "server.jar",
    }),
  });
  if (!trimmedImportResponse.ok) throw new Error(`Trimmed import failed: ${await trimmedImportResponse.text()}`);
  const trimmedImported = await trimmedImportResponse.json();
  if (trimmedImported.server?.name !== "Trimmed Import") {
    throw new Error(`Import name was not trimmed: ${JSON.stringify(trimmedImported).slice(0, 300)}`);
  }
  const trimmedUnregisterResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${trimmedImported.server.id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ deleteFiles: false }),
  });
  if (!trimmedUnregisterResponse.ok) throw new Error(`Trimmed server unregister failed: ${await trimmedUnregisterResponse.text()}`);
  const symbolNameCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "create",
      name: "!!!",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      minMemoryMb: 1024,
      maxMemoryMb: 1024,
    }),
  });
  if (!symbolNameCreateResponse.ok) throw new Error(`Symbol-name create failed: ${await symbolNameCreateResponse.text()}`);
  const symbolNameCreated = await symbolNameCreateResponse.json();
  if (!symbolNameCreated.server?.path.endsWith(`${path.sep}new-server`)) {
    throw new Error(`Symbol-name create did not use safe fallback slug: ${JSON.stringify(symbolNameCreated).slice(0, 300)}`);
  }
  const symbolDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${symbolNameCreated.server.id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ deleteFiles: true }),
  });
  if (!symbolDeleteResponse.ok) throw new Error(`Symbol-name server delete failed: ${await symbolDeleteResponse.text()}`);

  const duplicateImport = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      mode: "import",
      path: importPath,
      name: "Duplicate Smoke",
      type: "vanilla",
      minecraftVersion: versions.latest.release,
      launchJar: "server.jar",
    }),
  });
  if (!duplicateImport.ok) throw new Error(`Second copied server import failed: ${await duplicateImport.text()}`);
  const duplicateImported = await duplicateImport.json();
  if (!duplicateImported.server?.path || duplicateImported.server.path === importPath || duplicateImported.server.path === imported.server.path) {
    throw new Error(`Second copied server import did not create a distinct managed copy: ${JSON.stringify(duplicateImported).slice(0, 300)}`);
  }
  const unregisterResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${imported.server.id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ deleteFiles: false }),
  });
  if (!unregisterResponse.ok) throw new Error(`Server unregister failed: ${await unregisterResponse.text()}`);
  if (!existsSync(importPath)) throw new Error("Unregister deleted the imported server folder");

  const files = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!files.entries?.some((entry) => entry.name === "server.properties")) {
    throw new Error("File API did not list server.properties");
  }
  const props = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files?path=server.properties`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!props.file?.editable) throw new Error("server.properties was not editable");
  const mkdirResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "mkdir", path: "config/smoke-folder" }),
  });
  if (!mkdirResponse.ok) throw new Error(`Folder create failed: ${await mkdirResponse.text()}`);
  if (!existsSync(path.join(created.server.path, "config", "smoke-folder"))) {
    throw new Error("Folder create did not create the expected directory");
  }
  const newFileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "create-file", path: "config/smoke-folder/new-smoke.yml" }),
  });
  if (!newFileResponse.ok) throw new Error(`New file create failed: ${await newFileResponse.text()}`);
  if (!existsSync(path.join(created.server.path, "config", "smoke-folder", "new-smoke.yml"))) {
    throw new Error("New file create did not create the expected file");
  }
  const newFile = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files?path=config/smoke-folder/new-smoke.yml`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!newFile.file?.editable || newFile.file.content !== "") throw new Error("New file was not opened as an empty editable file");
  const duplicateNewFileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "create-file", path: "config/smoke-folder/new-smoke.yml" }),
  });
  if (duplicateNewFileResponse.ok) throw new Error("Duplicate new file create unexpectedly succeeded");
  const fileCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "write", path: "config/smoke-folder/smoke.txt", content: "temporary file" }),
  });
  if (!fileCreateResponse.ok) throw new Error(`File create failed: ${await fileCreateResponse.text()}`);
  const uploadForm = new FormData();
  uploadForm.set("action", "upload");
  uploadForm.set("path", "config/smoke-folder");
  uploadForm.set("file", new File(["uploaded config"], "uploaded-smoke.yml", { type: "text/yaml" }));
  const fileUploadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: uploadForm,
  });
  if (!fileUploadResponse.ok) throw new Error(`File upload failed: ${await fileUploadResponse.text()}`);
  if (!existsSync(path.join(created.server.path, "config", "smoke-folder", "uploaded-smoke.yml"))) {
    throw new Error("File upload did not create the expected file");
  }
  const fileDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete", path: "config/smoke-folder/smoke.txt" }),
  });
  if (!fileDeleteResponse.ok) throw new Error(`File delete failed: ${await fileDeleteResponse.text()}`);
  if (existsSync(path.join(created.server.path, "config", "smoke-folder", "smoke.txt"))) {
    throw new Error("File delete did not remove the expected file");
  }
  await mkdir(path.join(created.server.path, "config", "smoke-folder", "bulk-folder"), { recursive: true });
  await writeFile(path.join(created.server.path, "config", "smoke-folder", "bulk-folder", "nested.txt"), "bulk nested", "utf8");
  const bulkFileCreateResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "write", path: "config/smoke-folder/bulk-delete.yml", content: "bulk: true" }),
  });
  if (!bulkFileCreateResponse.ok) throw new Error(`Bulk delete setup file create failed: ${await bulkFileCreateResponse.text()}`);
  const selectedFileDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete-selected", paths: ["config/smoke-folder/bulk-delete.yml", "config/smoke-folder/bulk-folder"] }),
  });
  if (!selectedFileDeleteResponse.ok) throw new Error(`Selected file delete failed: ${await selectedFileDeleteResponse.text()}`);
  if (
    existsSync(path.join(created.server.path, "config", "smoke-folder", "bulk-delete.yml")) ||
    existsSync(path.join(created.server.path, "config", "smoke-folder", "bulk-folder"))
  ) {
    throw new Error("Selected file delete did not remove all expected entries");
  }
  const folderDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete", path: "config/smoke-folder" }),
  });
  if (!folderDeleteResponse.ok) throw new Error(`Folder delete failed: ${await folderDeleteResponse.text()}`);
  if (existsSync(path.join(created.server.path, "config", "smoke-folder"))) {
    throw new Error("Folder delete did not remove the expected directory");
  }
  const rootDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete", path: "" }),
  });
  if (rootDeleteResponse.ok) throw new Error("Server root delete unexpectedly succeeded");

  const blankCommandResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/command`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ command: "   " }),
  });
  if (blankCommandResponse.ok) throw new Error("Blank console command unexpectedly succeeded");
  const initialPresetResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/command`, {
    headers: { cookie: sessionCookie },
  });
  if (!initialPresetResponse.ok) throw new Error(`Initial command presets failed: ${await initialPresetResponse.text()}`);
  const initialPresets = await initialPresetResponse.json();
  if (!Array.isArray(initialPresets.presets) || initialPresets.presets.length !== 0) {
    throw new Error(`Initial command presets were unexpected: ${JSON.stringify(initialPresets).slice(0, 300)}`);
  }
  const savePresetResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/command`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "save-preset", command: " whitelist reload " }),
  });
  if (!savePresetResponse.ok) throw new Error(`Command preset save failed: ${await savePresetResponse.text()}`);
  const savedPresets = await savePresetResponse.json();
  const savedPreset = savedPresets.presets?.find((preset) => preset.command === "whitelist reload");
  if (!savedPreset?.id) throw new Error(`Command preset was not listed after save: ${JSON.stringify(savedPresets).slice(0, 300)}`);
  const duplicatePresetResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/command`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "save-preset", command: "WHITELIST RELOAD" }),
  });
  if (!duplicatePresetResponse.ok) throw new Error(`Command preset duplicate save failed: ${await duplicatePresetResponse.text()}`);
  const duplicatePresets = await duplicatePresetResponse.json();
  if (duplicatePresets.presets?.filter((preset) => preset.command.toLowerCase() === "whitelist reload").length !== 1) {
    throw new Error(`Command preset duplicate was not de-duplicated: ${JSON.stringify(duplicatePresets).slice(0, 300)}`);
  }
  const deletePresetResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/command`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete-preset", presetId: savedPreset.id }),
  });
  if (!deletePresetResponse.ok) throw new Error(`Command preset delete failed: ${await deletePresetResponse.text()}`);
  const deletedPresets = await deletePresetResponse.json();
  if (deletedPresets.presets?.some((preset) => preset.id === savedPreset.id)) {
    throw new Error(`Command preset remained after delete: ${JSON.stringify(deletedPresets).slice(0, 300)}`);
  }
  const missingLaunchProfileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ launchJar: "missing-smoke.jar" }),
  });
  if (!missingLaunchProfileResponse.ok) throw new Error(`Missing launch profile update failed: ${await missingLaunchProfileResponse.text()}`);
  const missingLaunchStartResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/start`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
  });
  if (missingLaunchStartResponse.ok) throw new Error("Missing launch target start unexpectedly succeeded");
  const missingLaunchHealth = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/health`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (missingLaunchHealth.health?.status !== "blocked") {
    throw new Error(`Missing launch target did not block health: ${JSON.stringify(missingLaunchHealth).slice(0, 300)}`);
  }

  const profileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ name: "Smoke Vanilla Edited", minMemoryMb: 1536, maxMemoryMb: 2560, launchJar: "server.jar" }),
  });
  if (!profileResponse.ok) throw new Error(`Profile update failed: ${await profileResponse.text()}`);
  const profile = await profileResponse.json();
  if (profile.server?.name !== "Smoke Vanilla Edited" || profile.server?.maxMemoryMb !== 2560) {
    throw new Error(`Profile update did not persist: ${JSON.stringify(profile).slice(0, 300)}`);
  }
  const profileVersionResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ minecraftVersion: versions.latest.release, type: "vanilla", loaderVersion: "" }),
  });
  if (!profileVersionResponse.ok) throw new Error(`Profile version update failed: ${await profileVersionResponse.text()}`);
  const profileVersion = await profileVersionResponse.json();
  if (profileVersion.server?.minecraftVersion !== versions.latest.release || profileVersion.server?.loaderVersion !== "") {
    throw new Error(`Profile version update did not persist: ${JSON.stringify(profileVersion).slice(0, 300)}`);
  }
  const historicProfileVersionResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ minecraftVersion: "1.12.2", type: "vanilla", loaderVersion: "" }),
  });
  if (!historicProfileVersionResponse.ok) throw new Error(`Historic profile version update failed: ${await historicProfileVersionResponse.text()}`);
  const historicProfileVersion = await historicProfileVersionResponse.json();
  if (historicProfileVersion.server?.minecraftVersion !== "1.12.2" || historicProfileVersion.server?.loaderVersion !== "") {
    throw new Error(`Historic profile version update did not persist: ${JSON.stringify(historicProfileVersion).slice(0, 300)}`);
  }
  const restoreLatestProfileVersionResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ minecraftVersion: versions.latest.release, type: "vanilla", loaderVersion: "" }),
  });
  if (!restoreLatestProfileVersionResponse.ok) throw new Error(`Profile version restore failed: ${await restoreLatestProfileVersionResponse.text()}`);
  const invalidLoaderProfileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ type: "fabric", minecraftVersion: versions.latest.release, loaderVersion: "" }),
  });
  if (invalidLoaderProfileResponse.ok) throw new Error("Fabric profile update without loader unexpectedly succeeded");
  const invalidProfileVersionResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ type: "vanilla", minecraftVersion: "0.0-smoke", loaderVersion: "" }),
  });
  if (invalidProfileVersionResponse.ok) throw new Error("Invalid Minecraft version profile update unexpectedly succeeded");
  const invalidProfileLoaderResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ type: "fabric", minecraftVersion: versions.latest.release, loaderVersion: "bad-loader-smoke" }),
  });
  if (invalidProfileLoaderResponse.ok) throw new Error("Invalid loader profile update unexpectedly succeeded");

  const writeResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/files`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "write", path: "server.properties", content: `${props.file.content}\nmotd=Smoke Test\n` }),
  });
  if (!writeResponse.ok) throw new Error(`File write failed: ${await writeResponse.text()}`);

  const properties = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/properties`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (properties.editable?.motd !== "Smoke Test") {
    throw new Error(`Properties API did not parse motd: ${JSON.stringify(properties).slice(0, 300)}`);
  }
  const propertiesDownloadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/properties?download=1`, {
    headers: { cookie: sessionCookie },
  });
  if (!propertiesDownloadResponse.ok) throw new Error(`server.properties download failed: ${await propertiesDownloadResponse.text()}`);
  if (!propertiesDownloadResponse.headers.get("content-type")?.includes("text/plain")) {
    throw new Error(`server.properties download did not return text: ${propertiesDownloadResponse.headers.get("content-type")}`);
  }
  if (!(await propertiesDownloadResponse.text()).includes("motd=Smoke Test")) {
    throw new Error("server.properties download did not include the expected MOTD");
  }

  const updatedPropertiesResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/properties`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ editable: { ...properties.editable, maxPlayers: 12, serverPort: 25566 } }),
  });
  if (!updatedPropertiesResponse.ok) {
    throw new Error(`Properties update failed: ${await updatedPropertiesResponse.text()}`);
  }
  const updatedProperties = await updatedPropertiesResponse.json();
  if (updatedProperties.editable?.maxPlayers !== 12 || updatedProperties.editable?.serverPort !== 25566) {
    throw new Error(`Properties update did not persist: ${JSON.stringify(updatedProperties).slice(0, 300)}`);
  }
  const serversAfterPortUpdate = await fetch(`http://127.0.0.1:${port}/api/servers?runtime=1`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  const createdAfterPortUpdate = serversAfterPortUpdate.servers?.find((server) => server.id === created.server.id);
  if (createdAfterPortUpdate?.port !== 25566) {
    throw new Error(`Server record port did not sync after server.properties save: ${JSON.stringify({ createdAfterPortUpdate, serversAfterPortUpdate }).slice(0, 300)}`);
  }
  const invalidPropertiesResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/properties`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ editable: { ...updatedProperties.editable, serverPort: 70000 } }),
  });
  if (invalidPropertiesResponse.ok) throw new Error("Invalid server.properties update unexpectedly succeeded");
  if (updatedProperties.eulaAccepted) throw new Error("EULA should not be accepted until explicitly enabled");

  const eulaResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/properties`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ editable: updatedProperties.editable, eulaAccepted: true }),
  });
  if (!eulaResponse.ok) throw new Error(`EULA update failed: ${await eulaResponse.text()}`);
  const eulaProperties = await eulaResponse.json();
  if (!eulaProperties.eulaAccepted) throw new Error("EULA update did not persist");
  const cloneResponse = await fetch(`http://127.0.0.1:${port}/api/servers`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ mode: "clone", sourceServerId: created.server.id, name: "Smoke Clone", port: 25577 }),
  });
  if (!cloneResponse.ok) throw new Error(`Server clone failed: ${await cloneResponse.text()}`);
  const cloned = await cloneResponse.json();
  if (!cloned.server?.id || cloned.server.name !== "Smoke Clone" || cloned.server.port !== 25577) {
    throw new Error(`Server clone returned invalid metadata: ${JSON.stringify(cloned).slice(0, 300)}`);
  }
  if (!existsSync(path.join(cloned.server.path, "server.properties"))) {
    throw new Error("Server clone did not copy server.properties");
  }
  if (!readFileSync(path.join(cloned.server.path, "server.properties"), "utf8").includes("server-port=25577")) {
    throw new Error("Server clone did not rewrite server.properties to the requested port");
  }
  const cloneDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${cloned.server.id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ deleteFiles: true }),
  });
  if (!cloneDeleteResponse.ok) throw new Error(`Cloned server delete failed: ${await cloneDeleteResponse.text()}`);
  if (existsSync(cloned.server.path)) throw new Error("Cloned server folder was not deleted");

  const smokeWorldPath = path.join(created.server.path, "world");
  await mkdir(smokeWorldPath, { recursive: true });
  await writeFile(path.join(smokeWorldPath, "level.dat"), "placeholder", "utf8");

  const worlds = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (worlds.activeWorld !== "world" || !Array.isArray(worlds.worlds) || !worlds.worlds.some((world) => world.name === "world")) {
    throw new Error(`Worlds API returned invalid data: ${JSON.stringify(worlds).slice(0, 300)}`);
  }

  const pathWorldZip = new AdmZip();
  pathWorldZip.addFile("Path World/level.dat", Buffer.from("path world"));
  pathWorldZip.addFile("Path World/playerdata/00000000-0000-0000-0000-000000000001.dat", Buffer.from("player"));
  pathWorldZip.addFile("Path World/datapacks/path-pack.zip", Buffer.from("pack"));
  const pathWorldForm = new FormData();
  pathWorldForm.set("action", "import-world-zip");
  pathWorldForm.set("worldName", "path-world");
  pathWorldForm.set("file", new Blob([pathWorldZip.toBuffer()], { type: "application/zip" }), "path-world.zip");
  const pathWorldResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: pathWorldForm,
  });
  if (!pathWorldResponse.ok) throw new Error(`World ZIP import failed: ${await pathWorldResponse.text()}`);
  const worldsAfterPathImport = await pathWorldResponse.json();
  const pathWorld = worldsAfterPathImport.worlds?.find((world) => world.name === "path-world");
  if (!pathWorld || pathWorld.playerFiles !== 1 || !pathWorld.datapacks?.some((pack) => pack.name === "path-pack.zip")) {
    throw new Error(`World path import did not preserve data: ${JSON.stringify(worldsAfterPathImport).slice(0, 300)}`);
  }
  const worldDownloadResponse = await fetch(
    `http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds?download=${encodeURIComponent("path-world")}`,
    { headers: { cookie: sessionCookie } },
  );
  if (!worldDownloadResponse.ok) throw new Error(`World download failed: ${await worldDownloadResponse.text()}`);
  if (!worldDownloadResponse.headers.get("content-type")?.includes("application/zip")) {
    throw new Error(`World download did not return a zip: ${worldDownloadResponse.headers.get("content-type")}`);
  }
  if ((await worldDownloadResponse.arrayBuffer()).byteLength < 100) {
    throw new Error("World download was unexpectedly small");
  }
  const renameWorldResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "rename-world", worldName: "path-world", nextWorldName: "renamed-path-world" }),
  });
  if (!renameWorldResponse.ok) throw new Error(`World rename failed: ${await renameWorldResponse.text()}`);
  const worldsAfterRename = await renameWorldResponse.json();
  const renamedWorld = worldsAfterRename.worlds?.find((world) => world.name === "renamed-path-world");
  if (!renamedWorld || renamedWorld.playerFiles !== 1 || !renamedWorld.datapacks?.some((pack) => pack.name === "path-pack.zip")) {
    throw new Error(`World rename did not preserve data: ${JSON.stringify(worldsAfterRename).slice(0, 300)}`);
  }
  if (existsSync(path.join(created.server.path, "path-world")) || !existsSync(path.join(created.server.path, "renamed-path-world"))) {
    throw new Error("World rename did not move the folder on disk");
  }

  const worldZip = new AdmZip();
  worldZip.addFile("Zip World/level.dat", Buffer.from("zip world"));
  worldZip.addFile("Zip World/playerdata/00000000-0000-0000-0000-000000000002.dat", Buffer.from("player"));
  worldZip.addFile("Zip World/datapacks/zip-pack.zip", Buffer.from("pack"));
  const worldZipForm = new FormData();
  worldZipForm.set("action", "import-world-zip");
  worldZipForm.set("worldName", "zip-world");
  worldZipForm.set("file", new Blob([worldZip.toBuffer()], { type: "application/zip" }), "zip-world.zip");
  const worldZipResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: worldZipForm,
  });
  if (!worldZipResponse.ok) throw new Error(`World zip import failed: ${await worldZipResponse.text()}`);
  const worldsAfterZipImport = await worldZipResponse.json();
  const zipWorld = worldsAfterZipImport.worlds?.find((world) => world.name === "zip-world");
  if (!zipWorld || zipWorld.playerFiles !== 1 || !zipWorld.datapacks?.some((pack) => pack.name === "zip-pack.zip")) {
    throw new Error(`World zip import did not preserve data: ${JSON.stringify(worldsAfterZipImport).slice(0, 300)}`);
  }
  const activeWorldDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete-world", worldName: "world" }),
  });
  if (activeWorldDeleteResponse.ok) throw new Error("Active world delete unexpectedly succeeded");
  const worldDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete-world", worldName: "zip-world" }),
  });
  if (!worldDeleteResponse.ok) throw new Error(`World delete failed: ${await worldDeleteResponse.text()}`);
  const worldsAfterWorldDelete = await worldDeleteResponse.json();
  if (worldsAfterWorldDelete.worlds?.some((world) => world.name === "zip-world")) {
    throw new Error("World delete did not remove zip-world from the listing");
  }
  if (existsSync(path.join(created.server.path, "zip-world"))) {
    throw new Error("World delete did not remove the folder from disk");
  }

  const activeWorldResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "set-active", worldName: "world" }),
  });
  if (!activeWorldResponse.ok) throw new Error(`Active world update failed: ${await activeWorldResponse.text()}`);
  const datapackUpload = new FormData();
  datapackUpload.set("action", "upload-datapack");
  datapackUpload.set("worldName", "world");
  datapackUpload.set("file", new Blob([Buffer.from("fake datapack zip")], { type: "application/zip" }), "smoke-datapack.zip");
  const datapackUploadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: datapackUpload,
  });
  if (!datapackUploadResponse.ok) throw new Error(`Datapack upload failed: ${await datapackUploadResponse.text()}`);
  const worldsWithDatapack = await datapackUploadResponse.json();
  if (!worldsWithDatapack.worlds?.some((world) => world.name === "world" && world.datapacks?.some((pack) => pack.name === "smoke-datapack.zip"))) {
    throw new Error(`Uploaded datapack was not listed: ${JSON.stringify(worldsWithDatapack).slice(0, 300)}`);
  }
  const secondDatapackUpload = new FormData();
  secondDatapackUpload.set("action", "upload-datapack");
  secondDatapackUpload.set("worldName", "world");
  secondDatapackUpload.set("file", new Blob([Buffer.from("second fake datapack zip")], { type: "application/zip" }), "smoke-datapack-two.zip");
  const secondDatapackUploadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: secondDatapackUpload,
  });
  if (!secondDatapackUploadResponse.ok) throw new Error(`Second datapack upload failed: ${await secondDatapackUploadResponse.text()}`);
  const datapackDisableResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "toggle-datapack", worldName: "world", fileName: "smoke-datapack.zip", enabled: false }),
  });
  if (!datapackDisableResponse.ok) throw new Error(`Datapack disable failed: ${await datapackDisableResponse.text()}`);
  const worldsWithDisabledDatapack = await datapackDisableResponse.json();
  if (!worldsWithDisabledDatapack.worlds?.some((world) => world.name === "world" && world.datapacks?.some((pack) => pack.name === "smoke-datapack.zip.disabled" && !pack.enabled))) {
    throw new Error(`Datapack disable did not update listing: ${JSON.stringify(worldsWithDisabledDatapack).slice(0, 300)}`);
  }
  const datapackEnableResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "toggle-datapack", worldName: "world", fileName: "smoke-datapack.zip.disabled", enabled: true }),
  });
  if (!datapackEnableResponse.ok) throw new Error(`Datapack enable failed: ${await datapackEnableResponse.text()}`);
  const worldsWithEnabledDatapack = await datapackEnableResponse.json();
  if (!worldsWithEnabledDatapack.worlds?.some((world) => world.name === "world" && world.datapacks?.some((pack) => pack.name === "smoke-datapack.zip" && pack.enabled))) {
    throw new Error(`Datapack enable did not update listing: ${JSON.stringify(worldsWithEnabledDatapack).slice(0, 300)}`);
  }
  const datapackDownloadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds?world=world&datapack=smoke-datapack.zip`, {
    headers: { cookie: sessionCookie },
  });
  if (!datapackDownloadResponse.ok) throw new Error(`Datapack download failed: ${await datapackDownloadResponse.text()}`);
  if (!datapackDownloadResponse.headers.get("content-type")?.includes("application/zip")) {
    throw new Error(`Datapack download did not return a zip: ${datapackDownloadResponse.headers.get("content-type")}`);
  }
  if (Buffer.from(await datapackDownloadResponse.arrayBuffer()).toString("utf8") !== "fake datapack zip") {
    throw new Error("Datapack download did not return the expected bytes");
  }
  const selectedDatapackDisableResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "disable-selected-datapacks", worldName: "world", fileNames: ["smoke-datapack.zip", "smoke-datapack-two.zip"] }),
  });
  if (!selectedDatapackDisableResponse.ok) throw new Error(`Selected datapack disable failed: ${await selectedDatapackDisableResponse.text()}`);
  const worldsWithSelectedDisabledDatapacks = await selectedDatapackDisableResponse.json();
  if (!["smoke-datapack.zip.disabled", "smoke-datapack-two.zip.disabled"].every((fileName) => worldsWithSelectedDisabledDatapacks.worlds?.some((world) => world.name === "world" && world.datapacks?.some((pack) => pack.name === fileName && !pack.enabled)))) {
    throw new Error(`Selected datapack disable did not update listing: ${JSON.stringify(worldsWithSelectedDisabledDatapacks).slice(0, 300)}`);
  }
  const selectedDatapackEnableResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "enable-selected-datapacks", worldName: "world", fileNames: ["smoke-datapack.zip.disabled"] }),
  });
  if (!selectedDatapackEnableResponse.ok) throw new Error(`Selected datapack enable failed: ${await selectedDatapackEnableResponse.text()}`);
  const selectedDatapackDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete-selected-datapacks", worldName: "world", fileNames: ["smoke-datapack.zip", "smoke-datapack-two.zip.disabled"] }),
  });
  if (!selectedDatapackDeleteResponse.ok) throw new Error(`Selected datapack delete failed: ${await selectedDatapackDeleteResponse.text()}`);
  const worldsAfterSelectedDatapackDelete = await selectedDatapackDeleteResponse.json();
  if (["smoke-datapack.zip", "smoke-datapack-two.zip.disabled"].some((fileName) => worldsAfterSelectedDatapackDelete.worlds?.some((world) => world.name === "world" && world.datapacks?.some((pack) => pack.name === fileName)))) {
    throw new Error(`Selected datapack delete did not remove selected packs: ${JSON.stringify(worldsAfterSelectedDatapackDelete).slice(0, 300)}`);
  }
  const restoreDatapackUpload = new FormData();
  restoreDatapackUpload.set("action", "upload-datapack");
  restoreDatapackUpload.set("worldName", "world");
  restoreDatapackUpload.set("file", new Blob([Buffer.from("fake datapack zip")], { type: "application/zip" }), "smoke-datapack.zip");
  const restoreDatapackUploadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: restoreDatapackUpload,
  });
  if (!restoreDatapackUploadResponse.ok) throw new Error(`Restore datapack upload failed: ${await restoreDatapackUploadResponse.text()}`);
  const datapackDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/worlds`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete-datapack", worldName: "world", fileName: "smoke-datapack.zip" }),
  });
  if (!datapackDeleteResponse.ok) throw new Error(`Datapack delete failed: ${await datapackDeleteResponse.text()}`);
  const worldsAfterDatapackDelete = await datapackDeleteResponse.json();
  if (worldsAfterDatapackDelete.worlds?.some((world) => world.name === "world" && world.datapacks?.some((pack) => pack.name === "smoke-datapack.zip"))) {
    throw new Error("Datapack delete did not remove the uploaded datapack");
  }

  const opResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ kind: "ops", action: "add", entry: { name: "SmokePlayer", uuid: "00000000-0000-0000-0000-000000000000", level: 3 } }),
  });
  if (!opResponse.ok) throw new Error(`OP add failed: ${await opResponse.text()}`);
  const blankPlayerResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ kind: "ops", action: "add", entry: { name: "   " } }),
  });
  if (blankPlayerResponse.ok) throw new Error("Blank player access update unexpectedly succeeded");
  const invalidPlayerLookupResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players?lookup=ab`, {
    headers: { cookie: sessionCookie },
  });
  if (invalidPlayerLookupResponse.ok) throw new Error("Invalid player lookup unexpectedly succeeded");
  const whitelistResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ kind: "whitelist", action: "add", entry: { name: "SmokePlayer", uuid: "00000000-0000-0000-0000-000000000000" } }),
  });
  if (!whitelistResponse.ok) throw new Error(`Whitelist add failed: ${await whitelistResponse.text()}`);
  const banResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ kind: "bannedIps", action: "add", entry: { ip: "203.0.113.10", reason: "smoke" } }),
  });
  if (!banResponse.ok) throw new Error(`IP ban add failed: ${await banResponse.text()}`);
  const accessLists = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  const access = accessLists.access ?? accessLists;
  if (!access.ops?.some((entry) => entry.name === "SmokePlayer" && entry.level === 3)) {
    throw new Error(`OP list did not persist: ${JSON.stringify(accessLists).slice(0, 300)}`);
  }
  if (!access.whitelist?.some((entry) => entry.name === "SmokePlayer")) {
    throw new Error(`Whitelist did not persist: ${JSON.stringify(accessLists).slice(0, 300)}`);
  }
  if (!access.bannedIps?.some((entry) => entry.ip === "203.0.113.10")) {
    throw new Error(`IP ban did not persist: ${JSON.stringify(accessLists).slice(0, 300)}`);
  }
  const bulkAccessEntries = [
    { kind: "ops", entry: { name: "BulkOpSmoke", uuid: "00000000-0000-0000-0000-000000000001", level: 2 } },
    { kind: "whitelist", entry: { name: "BulkWhiteSmoke", uuid: "00000000-0000-0000-0000-000000000002" } },
    { kind: "bannedPlayers", entry: { name: "BulkBanSmoke", uuid: "00000000-0000-0000-0000-000000000003", reason: "bulk smoke" } },
    { kind: "bannedIps", entry: { ip: "203.0.113.11", reason: "bulk smoke" } },
  ];
  for (const item of bulkAccessEntries) {
    const response = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
      method: "POST",
      headers: { "Content-Type": "application/json", cookie: sessionCookie },
      body: JSON.stringify({ kind: item.kind, action: "add", entry: item.entry }),
    });
    if (!response.ok) throw new Error(`Bulk setup access add failed for ${item.kind}: ${await response.text()}`);
  }
  for (const item of bulkAccessEntries) {
    const response = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
      method: "POST",
      headers: { "Content-Type": "application/json", cookie: sessionCookie },
      body: JSON.stringify({ kind: item.kind, action: "remove-selected", entries: [item.entry] }),
    });
    if (!response.ok) throw new Error(`Selected access remove failed for ${item.kind}: ${await response.text()}`);
  }
  const afterBulkAccessRemove = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (
    afterBulkAccessRemove.ops?.some((entry) => entry.name === "BulkOpSmoke") ||
    afterBulkAccessRemove.whitelist?.some((entry) => entry.name === "BulkWhiteSmoke") ||
    afterBulkAccessRemove.bannedPlayers?.some((entry) => entry.name === "BulkBanSmoke") ||
    afterBulkAccessRemove.bannedIps?.some((entry) => entry.ip === "203.0.113.11")
  ) {
    throw new Error(`Selected access remove did not remove all temporary entries: ${JSON.stringify(afterBulkAccessRemove).slice(0, 300)}`);
  }
  const removeOpResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/players`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ kind: "ops", action: "remove", entry: { name: "SmokePlayer" } }),
  });
  if (!removeOpResponse.ok) throw new Error(`OP remove failed: ${await removeOpResponse.text()}`);

  const modUpload = new FormData();
  modUpload.set("action", "upload");
  modUpload.set("file", new Blob([Buffer.from("fake jar content")], { type: "application/java-archive" }), "smoke-mod.jar");
  const modUploadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: modUpload,
  });
  if (!modUploadResponse.ok) throw new Error(`Mod upload failed: ${await modUploadResponse.text()}`);
  const duplicateModUpload = new FormData();
  duplicateModUpload.set("action", "upload");
  duplicateModUpload.set("file", new Blob([Buffer.from("duplicate fake jar content")], { type: "application/java-archive" }), "smoke-mod.jar");
  const duplicateModUploadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: duplicateModUpload,
  });
  if (!duplicateModUploadResponse.ok) throw new Error(`Duplicate mod upload failed: ${await duplicateModUploadResponse.text()}`);
  const duplicateModUploadPayload = await duplicateModUploadResponse.json();
  if (!duplicateModUploadPayload.files?.includes("smoke-mod-2.jar")) {
    throw new Error(`Duplicate mod upload did not use a unique filename: ${JSON.stringify(duplicateModUploadPayload).slice(0, 300)}`);
  }
  const secondModUpload = new FormData();
  secondModUpload.set("action", "upload");
  secondModUpload.set("file", new Blob([Buffer.from("second fake jar content")], { type: "application/java-archive" }), "smoke-mod-two.jar");
  const secondModUploadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { cookie: sessionCookie },
    body: secondModUpload,
  });
  if (!secondModUploadResponse.ok) throw new Error(`Second mod upload failed: ${await secondModUploadResponse.text()}`);
  const uploadedMods = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!["smoke-mod.jar", "smoke-mod-2.jar", "smoke-mod-two.jar"].every((fileName) => uploadedMods.mods?.some((mod) => mod.fileName === fileName && mod.enabled))) {
    throw new Error(`Uploaded mod was not listed: ${JSON.stringify(uploadedMods).slice(0, 300)}`);
  }
  const modDownloadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods?download=smoke-mod.jar&enabled=1`, {
    headers: { cookie: sessionCookie },
  });
  if (!modDownloadResponse.ok) throw new Error(`Mod download failed: ${await modDownloadResponse.text()}`);
  if (!modDownloadResponse.headers.get("content-type")?.includes("application/java-archive")) {
    throw new Error(`Mod download did not return a jar content type: ${modDownloadResponse.headers.get("content-type")}`);
  }
  if (Buffer.from(await modDownloadResponse.arrayBuffer()).toString("utf8") !== "fake jar content") {
    throw new Error("Mod download did not return the expected jar bytes");
  }
  const disableAllModsResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "disable-all" }),
  });
  if (!disableAllModsResponse.ok) throw new Error(`Bulk mod disable failed: ${await disableAllModsResponse.text()}`);
  const disabledMods = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!["smoke-mod.jar", "smoke-mod-2.jar", "smoke-mod-two.jar"].every((fileName) => disabledMods.mods?.some((mod) => mod.fileName === fileName && !mod.enabled))) {
    throw new Error(`Bulk mod disable did not update listings: ${JSON.stringify(disabledMods).slice(0, 300)}`);
  }
  const enableAllModsResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "enable-all" }),
  });
  if (!enableAllModsResponse.ok) throw new Error(`Bulk mod enable failed: ${await enableAllModsResponse.text()}`);
  const enabledMods = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!["smoke-mod.jar", "smoke-mod-2.jar", "smoke-mod-two.jar"].every((fileName) => enabledMods.mods?.some((mod) => mod.fileName === fileName && mod.enabled))) {
    throw new Error(`Bulk mod enable did not update listings: ${JSON.stringify(enabledMods).slice(0, 300)}`);
  }
  const selectedDisableResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      action: "disable-selected",
      mods: [
        { fileName: "smoke-mod-2.jar", enabled: true },
        { fileName: "smoke-mod-two.jar", enabled: true },
      ],
    }),
  });
  if (!selectedDisableResponse.ok) throw new Error(`Selected mod disable failed: ${await selectedDisableResponse.text()}`);
  const selectedDisabledMods = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!["smoke-mod-2.jar", "smoke-mod-two.jar"].every((fileName) => selectedDisabledMods.mods?.some((mod) => mod.fileName === fileName && !mod.enabled))) {
    throw new Error(`Selected mod disable did not update listings: ${JSON.stringify(selectedDisabledMods).slice(0, 300)}`);
  }
  const selectedEnableResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "enable-selected", mods: [{ fileName: "smoke-mod-2.jar", enabled: false }] }),
  });
  if (!selectedEnableResponse.ok) throw new Error(`Selected mod enable failed: ${await selectedEnableResponse.text()}`);
  const selectedDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({
      action: "delete-selected",
      mods: [
        { fileName: "smoke-mod-2.jar", enabled: true },
        { fileName: "smoke-mod-two.jar", enabled: false },
      ],
    }),
  });
  if (!selectedDeleteResponse.ok) throw new Error(`Selected mod delete failed: ${await selectedDeleteResponse.text()}`);
  const afterSelectedDeleteMods = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (["smoke-mod-2.jar", "smoke-mod-two.jar"].some((fileName) => afterSelectedDeleteMods.mods?.some((mod) => mod.fileName === fileName))) {
    throw new Error(`Selected mod delete did not remove selected jars: ${JSON.stringify(afterSelectedDeleteMods).slice(0, 300)}`);
  }
  const deleteModResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}/mods`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete", fileName: "smoke-mod.jar", enabled: true }),
  });
  if (!deleteModResponse.ok) throw new Error(`Mod delete failed: ${await deleteModResponse.text()}`);
  const fabricUnregisterResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${fabricImported.server.id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ deleteFiles: false }),
  });
  if (!fabricUnregisterResponse.ok) throw new Error(`Fabric server unregister failed: ${await fabricUnregisterResponse.text()}`);
  if (!existsSync(fabricImportPath)) throw new Error("Unregister deleted the imported Fabric server folder");

  const snapshotResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ reason: "smoke delete check" }),
  });
  if (!snapshotResponse.ok) throw new Error(`Manual snapshot failed: ${await snapshotResponse.text()}`);
  const snapshots = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  const manualSnapshot = snapshots.backups?.find((backup) => backup.reason === "smoke delete check");
  if (!manualSnapshot || typeof manualSnapshot.sizeBytes !== "number") {
    throw new Error(`Snapshot metadata missing: ${JSON.stringify(snapshots).slice(0, 300)}`);
  }
  const renameSnapshotResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "rename", backupId: manualSnapshot.id, reason: "renamed smoke snapshot" }),
  });
  if (!renameSnapshotResponse.ok) throw new Error(`Snapshot rename failed: ${await renameSnapshotResponse.text()}`);
  const renamedSnapshots = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (!renamedSnapshots.backups?.some((backup) => backup.id === manualSnapshot.id && backup.reason === "renamed smoke snapshot")) {
    throw new Error(`Snapshot rename did not persist: ${JSON.stringify(renamedSnapshots).slice(0, 300)}`);
  }
  const downloadSnapshotResponse = await fetch(
    `http://127.0.0.1:${port}/api/servers/${created.server.id}/backups?download=${encodeURIComponent(manualSnapshot.id)}`,
    { headers: { cookie: sessionCookie } },
  );
  if (!downloadSnapshotResponse.ok) throw new Error(`Snapshot download failed: ${await downloadSnapshotResponse.text()}`);
  if (!downloadSnapshotResponse.headers.get("content-type")?.includes("application/zip")) {
    throw new Error(`Snapshot download did not return a zip: ${downloadSnapshotResponse.headers.get("content-type")}`);
  }
  if ((await downloadSnapshotResponse.arrayBuffer()).byteLength < 100) {
    throw new Error("Snapshot download was unexpectedly small");
  }
  const currentExportResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups?current=1`, {
    headers: { cookie: sessionCookie },
  });
  if (!currentExportResponse.ok) throw new Error(`Current server export failed: ${await currentExportResponse.text()}`);
  if (!currentExportResponse.headers.get("content-type")?.includes("application/zip")) {
    throw new Error(`Current server export did not return a zip: ${currentExportResponse.headers.get("content-type")}`);
  }
  const currentExport = new AdmZip(Buffer.from(await currentExportResponse.arrayBuffer()));
  if (!currentExport.getEntries().some((entry) => entry.entryName.endsWith("server.properties"))) {
    throw new Error("Current server export did not include server.properties");
  }
  const beforePruneSnapshots = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  const keepSnapshotCount = Math.max(1, Math.min(beforePruneSnapshots.backups?.length ?? 1, 5));
  const pruneSnapshotResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "prune", keepCount: keepSnapshotCount }),
  });
  if (!pruneSnapshotResponse.ok) throw new Error(`Snapshot prune failed: ${await pruneSnapshotResponse.text()}`);
  const prunedSnapshots = await pruneSnapshotResponse.json();
  if ((prunedSnapshots.backups?.length ?? 0) > keepSnapshotCount) {
    throw new Error(`Snapshot prune kept too many entries: ${JSON.stringify(prunedSnapshots).slice(0, 300)}`);
  }
  if (!prunedSnapshots.backups?.some((backup) => backup.id === manualSnapshot.id)) {
    throw new Error(`Snapshot prune removed the latest manual snapshot unexpectedly: ${JSON.stringify(prunedSnapshots).slice(0, 300)}`);
  }
  const bulkSnapshotIds = [];
  for (const reason of ["bulk snapshot smoke one", "bulk snapshot smoke two"]) {
    const response = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
      method: "POST",
      headers: { "Content-Type": "application/json", cookie: sessionCookie },
      body: JSON.stringify({ reason }),
    });
    if (!response.ok) throw new Error(`Bulk snapshot setup failed: ${await response.text()}`);
    const payload = await response.json();
    if (!payload.backupId) throw new Error(`Bulk snapshot setup did not return an id: ${JSON.stringify(payload).slice(0, 300)}`);
    bulkSnapshotIds.push(payload.backupId);
  }
  const bulkSnapshotDeleteResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete-selected", backupIds: bulkSnapshotIds }),
  });
  if (!bulkSnapshotDeleteResponse.ok) throw new Error(`Selected snapshot delete failed: ${await bulkSnapshotDeleteResponse.text()}`);
  const bulkSnapshotDeletePayload = await bulkSnapshotDeleteResponse.json();
  if (bulkSnapshotDeletePayload.deleted?.length !== bulkSnapshotIds.length) {
    throw new Error(`Selected snapshot delete returned unexpected ids: ${JSON.stringify(bulkSnapshotDeletePayload).slice(0, 300)}`);
  }
  const afterBulkSnapshotDelete = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    headers: { cookie: sessionCookie },
  }).then((response) => response.json());
  if (bulkSnapshotIds.some((backupId) => afterBulkSnapshotDelete.backups?.some((backup) => backup.id === backupId))) {
    throw new Error(`Selected snapshot delete did not remove selected snapshots: ${JSON.stringify(afterBulkSnapshotDelete).slice(0, 300)}`);
  }
  const runScript = process.platform === "win32" ? "run-smoke.bat" : "run-smoke.sh";
  await writeFile(
    path.join(created.server.path, runScript),
    process.platform === "win32" ? "@echo off\r\nping -n 30 127.0.0.1 > nul\r\n" : "#!/bin/sh\nsleep 30\n",
    "utf8",
  );
  const scriptProfileResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ launchJar: runScript }),
  });
  if (!scriptProfileResponse.ok) throw new Error(`Smoke run script profile update failed: ${await scriptProfileResponse.text()}`);
  const startSmokeResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/start`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
  });
  if (!startSmokeResponse.ok) throw new Error(`Smoke server start failed: ${await startSmokeResponse.text()}`);
  for (let attempt = 0; attempt < 20; attempt += 1) {
    const running = await fetch(`http://127.0.0.1:${port}/api/servers?runtime=1`, { headers: { cookie: sessionCookie } }).then((response) => response.json());
    if (running.runtime?.runningServerId === created.server.id) break;
    await wait(150);
    if (attempt === 19) throw new Error("Smoke server did not enter running state");
  }
  const restartSmokeResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/restart`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ force: true }),
  });
  if (!restartSmokeResponse.ok) throw new Error(`Smoke server restart failed: ${await restartSmokeResponse.text()}`);
  for (let attempt = 0; attempt < 20; attempt += 1) {
    const running = await fetch(`http://127.0.0.1:${port}/api/servers?runtime=1`, { headers: { cookie: sessionCookie } }).then((response) => response.json());
    if (running.runtime?.runningServerId === created.server.id) break;
    await wait(150);
    if (attempt === 19) throw new Error("Smoke server did not re-enter running state after restart");
  }
  const runningRestoreResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "restore", backupId: manualSnapshot.id }),
  });
  if (runningRestoreResponse.ok) throw new Error("Snapshot restore while running unexpectedly succeeded");
  const forceStopSmokeResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/stop`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ force: true }),
  });
  if (!forceStopSmokeResponse.ok) throw new Error(`Smoke server force stop failed: ${await forceStopSmokeResponse.text()}`);
  const logDownloadResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/logs?download=1`, {
    headers: { cookie: sessionCookie },
  });
  if (!logDownloadResponse.ok) throw new Error(`Console log download failed: ${await logDownloadResponse.text()}`);
  if (!logDownloadResponse.headers.get("content-type")?.includes("text/plain")) {
    throw new Error(`Console log download did not return text: ${logDownloadResponse.headers.get("content-type")}`);
  }
  const downloadedLogs = await logDownloadResponse.text();
  if (!downloadedLogs.includes("Starting Smoke Vanilla") || !downloadedLogs.includes("Force stop requested")) {
    throw new Error(`Console log download did not include retained runtime logs: ${downloadedLogs.slice(0, 300)}`);
  }
  const invalidRestoreResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "restore", backupId: "missing-snapshot" }),
  });
  if (invalidRestoreResponse.ok) throw new Error("Invalid snapshot restore unexpectedly succeeded");
  const deleteSnapshotResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}/backups`, {
    method: "POST",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ action: "delete", backupId: manualSnapshot.id }),
  });
  if (!deleteSnapshotResponse.ok) throw new Error(`Snapshot delete failed: ${await deleteSnapshotResponse.text()}`);

  const deleteServerResponse = await fetch(`http://127.0.0.1:${port}/api/servers/${created.server.id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json", cookie: sessionCookie },
    body: JSON.stringify({ deleteFiles: true }),
  });
  if (!deleteServerResponse.ok) throw new Error(`Server delete failed: ${await deleteServerResponse.text()}`);
  if (existsSync(path.join(serverRoot, "Smoke-Vanilla"))) throw new Error("Delete files did not remove the created server folder");

  const logoutResponse = await fetch(`http://127.0.0.1:${port}/api/auth/logout`, {
    method: "POST",
    headers: { cookie: sessionCookie },
  });
  const clearedCookie = parseCookie(logoutResponse.headers);
  const loginPage = await getText("/", clearedCookie);
  if (!loginPage.includes("Loading dashboard")) throw new Error("Logged-out page did not render the static auth shell");
  const loggedOutState = await fetch(`http://127.0.0.1:${port}/api/auth/me`, {
    headers: clearedCookie ? { cookie: clearedCookie } : undefined,
  }).then((response) => response.json());
  if (loggedOutState.user || loggedOutState.needsSetup) {
    throw new Error(`Logged-out auth state was incorrect: ${JSON.stringify(loggedOutState)}`);
  }
  const oldLoginResponse = await fetch(`http://127.0.0.1:${port}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "Leo", password: "temporary-pass-123" }),
  });
  if (oldLoginResponse.ok) throw new Error("Old account credentials still worked after account update");
  const newLoginResponse = await fetch(`http://127.0.0.1:${port}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "Leo Admin", password: "permanent-pass-456" }),
  });
  if (!newLoginResponse.ok) throw new Error(`Updated account credentials did not work: ${await newLoginResponse.text()}`);
  const newLoginCookie = parseCookie(newLoginResponse.headers);
  if (!newLoginCookie) throw new Error("Updated account login did not set a session cookie");
  const meAfterAccountUpdate = await fetch(`http://127.0.0.1:${port}/api/auth/me`, {
    headers: { cookie: newLoginCookie },
  }).then((response) => response.json());
  if (meAfterAccountUpdate.user?.username !== "Leo Admin" || meAfterAccountUpdate.needsSetup) {
    throw new Error(`Updated account session did not authenticate: ${JSON.stringify(meAfterAccountUpdate)}`);
  }

  console.log("Auth smoke test passed");
} finally {
  await stopProcessTree(child);
  if (existsSync(dataDir)) {
    await rm(dataDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 250 }).catch(() => undefined);
  }
  if (existsSync(serverRoot)) {
    await rm(serverRoot, { recursive: true, force: true, maxRetries: 5, retryDelay: 250 }).catch(() => undefined);
  }
  if (existsSync(distDir)) {
    await rm(distDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 250 }).catch(() => undefined);
  }
}
