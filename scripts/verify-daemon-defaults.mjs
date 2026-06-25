import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

const root = process.cwd();
const packageJson = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
const scripts = packageJson.scripts ?? {};
const launcherText = readFileSync(path.join(root, "scripts", "start-packaged-daemon.mjs"), "utf8");
const sourceRunnerText = readFileSync(path.join(root, "scripts", "run-daemon.mjs"), "utf8");
const installRunText = readFileSync(path.join(root, "scripts", "install-run.mjs"), "utf8");
const statusDaemonText = readFileSync(path.join(root, "scripts", "status-daemon.mjs"), "utf8");
const stopDaemonText = readFileSync(path.join(root, "scripts", "stop-daemon.mjs"), "utf8");
const verifyDaemonPackageText = readFileSync(path.join(root, "scripts", "verify-daemon-package.mjs"), "utf8");
const verifyDaemonArchiveText = readFileSync(path.join(root, "scripts", "verify-daemon-archive.mjs"), "utf8");
const verifyDaemonReleaseText = readFileSync(path.join(root, "scripts", "verify-daemon-release.mjs"), "utf8");
const smokeDaemonText = readFileSync(path.join(root, "scripts", "smoke-daemon.mjs"), "utf8");
const smokeDaemonPackageText = readFileSync(path.join(root, "scripts", "smoke-daemon-package.mjs"), "utf8");
const smokeDaemonUiText = readFileSync(path.join(root, "scripts", "smoke-daemon-ui.mjs"), "utf8");
const smokeStartText = readFileSync(path.join(root, "scripts", "smoke-start.mjs"), "utf8");
const runtimeClientText = readFileSync(path.join(root, "src", "app", "dashboard", "lib", "runtime-client.ts"), "utf8");
const installShellText = readFileSync(path.join(root, "scripts", "install.sh"), "utf8");
const installPackageShellText = readFileSync(path.join(root, "scripts", "install-package.sh"), "utf8");
const daemonModsText = readFileSync(path.join(root, "daemon", "internal", "httpserver", "mods.go"), "utf8");
const daemonWorldsText = readFileSync(path.join(root, "daemon", "internal", "httpserver", "worlds.go"), "utf8");
const daemonPlayersText = readFileSync(path.join(root, "daemon", "internal", "httpserver", "players.go"), "utf8");
const daemonFilesText = readFileSync(path.join(root, "daemon", "internal", "httpserver", "files.go"), "utf8");
const daemonServerCreateText = readFileSync(path.join(root, "daemon", "internal", "httpserver", "server_create.go"), "utf8");
const legacyApiDir = path.join(root, "src", "app", "api");
const legacyLibDir = path.join(root, "src", "lib");

const expectedScripts = {
  build: "npm run daemon:package",
  start: "node scripts/start-packaged-daemon.mjs",
  "start:lan": "npm start",
  "serve:lan": "npm run install:run",
};
const forbiddenDefaultVerifyScripts = ["build:next", "verify:standalone-pruned", "smoke:runtime", "verify:minecraft-meta"];

const legacyOnlyScripts = new Set(["build:next", "start:next", "serve:next:lan", "verify:standalone-pruned"]);
const failures = [];

for (const [name, expected] of Object.entries(expectedScripts)) {
  if (scripts[name] !== expected) {
    failures.push(`script "${name}" must be "${expected}", got "${scripts[name] ?? ""}"`);
  }
}

for (const forbidden of forbiddenDefaultVerifyScripts) {
  if ((scripts.verify ?? "").includes(`npm run ${forbidden}`)) {
    failures.push(`default verify must not run legacy/online check "${forbidden}"`);
  }
}

for (const [name, command] of Object.entries(scripts)) {
  if (legacyOnlyScripts.has(name) || name.startsWith("dev")) continue;
  if (command.includes(".next/standalone") || command.includes("next start")) {
    failures.push(`script "${name}" points at the legacy Next runtime: ${command}`);
  }
}

if (launcherText.includes("go run") || launcherText.includes(".next/standalone") || launcherText.includes("next start")) {
  failures.push("start-packaged-daemon.mjs must launch the packaged cliff binary, not go run or Next standalone");
}

if (!launcherText.includes("dist") || !launcherText.includes("cliff") || !launcherText.includes("cliff")) {
  failures.push("start-packaged-daemon.mjs does not clearly target dist/cliff/cliff");
}

if (!launcherText.includes("--server-root")) {
  failures.push("start-packaged-daemon.mjs must pass the server root as a daemon flag");
}

if (!launcherText.includes("/api/health") || !launcherText.includes("writeReadyState") || !launcherText.includes("health,")) {
  failures.push("start-packaged-daemon.mjs must update cliff.json with the foreground daemon health payload");
}

if (!launcherText.includes("writeRunState(child.pid)") || !launcherText.includes("clearRunState(child.pid)") || !launcherText.includes("cliff.pid")) {
  failures.push("start-packaged-daemon.mjs must write and clear foreground daemon PID/state metadata");
}

if (!sourceRunnerText.includes("Local: http://localhost:${port}") || !sourceRunnerText.includes("Same network:")) {
  failures.push("run-daemon.mjs must print local and same-network dashboard URLs");
}

if (!sourceRunnerText.includes("--host") || !sourceRunnerText.includes("--port") || !sourceRunnerText.includes("--server-root")) {
  failures.push("run-daemon.mjs must support host, port, and server-root options like the packaged daemon runner");
}

if (!installRunText.includes("--server-root") || installRunText.includes("CLIFF_SERVER_ROOT: serverRoot")) {
  failures.push("install-run.mjs must pass the server root as a daemon flag, not only through the environment");
}

if (!installRunText.includes("health: health ?? null")) {
  failures.push("install-run.mjs must persist the daemon health payload in cliff.json");
}

if (!statusDaemonText.includes("Daemon heap:") || !statusDaemonText.includes("Daemon memory:") || !statusDaemonText.includes("Daemon goroutines:") || !statusDaemonText.includes("Logs:") || !statusDaemonText.includes("Errors:")) {
  failures.push("status-daemon.mjs must report daemon heap, memory, goroutines, and log paths");
}

for (const [name, text] of Object.entries({
  "run-daemon.mjs": sourceRunnerText,
  "start-packaged-daemon.mjs": launcherText,
  "install-run.mjs": installRunText,
  "status-daemon.mjs": statusDaemonText,
  "stop-daemon.mjs": stopDaemonText,
  "verify-daemon-package.mjs": verifyDaemonPackageText,
  "verify-daemon-archive.mjs": verifyDaemonArchiveText,
  "verify-daemon-release.mjs": verifyDaemonReleaseText,
})) {
  if (!text.includes("Missing value for ${name}") || !text.includes('value.startsWith("-")')) {
    failures.push(`${name} must reject missing option values before reading daemon state`);
  }
}

for (const [name, text] of Object.entries({
  "smoke-daemon.mjs": smokeDaemonText,
  "smoke-daemon-package.mjs": smokeDaemonPackageText,
  "smoke-daemon-ui.mjs": smokeDaemonUiText,
  "smoke-start.mjs": smokeStartText,
})) {
  if (text.includes("CLIFF_SERVER_ROOT: serverRoot")) {
    failures.push(`${name} must test --server-root directly without the env fallback masking regressions`);
  }
}

if (!smokeStartText.includes("/api/settings?storage=0") || !smokeStartText.includes("settings.json?.serverRoot")) {
  failures.push("smoke-start.mjs must verify npm start reports the requested --server-root through daemon settings");
}

if (!smokeStartText.includes("waitForForegroundState") || !smokeStartText.includes("foregroundState.health?.self?.pid") || !smokeStartText.includes("npm start foreground daemon should not write background daemon PID/state files")) {
  failures.push("smoke-start.mjs must verify foreground daemon state is ready while running and cleaned after exit");
}

if (runtimeClientText.includes("!daemonRuntimeEnabled()")) {
  failures.push("runtime-client.ts must not fall back to the legacy Next runtime");
}

if (!runtimeClientText.includes("new WebSocket")) {
  failures.push("runtime-client.ts must use the daemon WebSocket console transport");
}

if (runtimeClientText.includes("EventSource") || runtimeClientText.includes("/events")) {
  failures.push("runtime-client.ts must not use the legacy SSE runtime transport");
}

if (!runtimeClientText.includes('message.type === "error"') || !runtimeClientText.includes("handlers.onError")) {
  failures.push("runtime-client.ts must surface daemon WebSocket error frames");
}

if (!runtimeClientText.includes('url.searchParams.set("logs", "0")')) {
  failures.push("runtime-client.ts must disable console log streaming for non-console daemon subscriptions");
}

if (!smokeDaemonText.includes("waitForWebSocketError") || !smokeDaemonText.includes("server is not running")) {
  failures.push("smoke-daemon.mjs must verify daemon WebSocket error frames end-to-end");
}

if (daemonModsText.includes("http.DefaultClient.Do") || daemonWorldsText.includes("http.DefaultClient.Do") || daemonPlayersText.includes("http.DefaultClient.Do")) {
  failures.push("Daemon external API calls must use the bounded shared fetch helper, not http.DefaultClient");
}

if (daemonFilesText.includes("ParseMultipartForm") || !daemonFilesText.includes("MultipartReader()")) {
  failures.push("File manager uploads must stream multipart bodies instead of buffering them with ParseMultipartForm");
}

if (daemonModsText.includes("ParseMultipartForm") || !daemonModsText.includes("MultipartReader()")) {
  failures.push("Mod uploads must stream multipart bodies instead of buffering them with ParseMultipartForm");
}

if (daemonWorldsText.includes("ParseMultipartForm") || !daemonWorldsText.includes("MultipartReader()")) {
  failures.push("World uploads must stream multipart bodies instead of buffering them with ParseMultipartForm");
}

if (!daemonServerCreateText.includes("maxImportMultipartMemoryBytes int64 = 8 << 20") || !daemonServerCreateText.includes("ParseMultipartForm(maxImportMultipartMemoryBytes)")) {
  failures.push("Server import uploads must use a bounded multipart memory threshold before spilling to disk");
}

if (existsSync(legacyApiDir)) {
  failures.push("src/app/api must not exist in the daemon-only runtime; add endpoints to the Go daemon instead");
}

if (existsSync(legacyLibDir)) {
  failures.push("src/lib must not exist in the daemon-only runtime; shared frontend types belong under src/app/dashboard");
}

if (!installShellText.includes('case "$0" in') || !installShellText.includes('if [ -n "$LOCAL_INSTALLER" ]')) {
  failures.push("install.sh must avoid local helper lookup when executed through a piped shell");
}

if (!installPackageShellText.includes('127.*|""|*:*|*.*.*.*.*') || !installPackageShellText.includes("*.*.*.*)")) {
  failures.push("install-package.sh must only print IPv4 same-network dashboard URLs");
}

if (failures.length > 0) {
  console.error("Daemon production defaults are not aligned:");
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log("Daemon production defaults verified");
