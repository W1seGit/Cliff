import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { chmod, cp, mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import AdmZip from "adm-zip";
import path from "node:path";

const root = process.cwd();
const distRoot = path.join(root, "dist");
const binDir = path.join(distRoot, "bin");
const webDir = path.join(root, "daemon", "web");
const packageJson = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));

const targets = [
  { os: "windows", arch: "amd64", binary: "cliff.exe", runnerScripts: ["run.ps1", "status.ps1", "stop.ps1"] },
  { os: "linux", arch: "amd64", binary: "cliff", runnerScripts: ["run.sh", "status.sh", "stop.sh"] },
  { os: "linux", arch: "arm64", binary: "cliff", runnerScripts: ["run.sh", "status.sh", "stop.sh"] },
  { os: "darwin", arch: "amd64", binary: "cliff", runnerScripts: ["run.sh", "status.sh", "stop.sh"] },
  { os: "darwin", arch: "arm64", binary: "cliff", runnerScripts: ["run.sh", "status.sh", "stop.sh"] },
];

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

async function fileSHA256(filePath) {
  const data = await readFile(filePath);
  return createHash("sha256").update(data).digest("hex");
}

// Generate runner scripts for a platform
async function generateRunnerScripts(target, packageDir) {
  const isWindows = target.os === "windows";
  const binaryName = isWindows ? "cliff.exe" : "cliff";
  const port = 8080;

  if (isWindows) {
    const runPs1 = `param(
  [int]$Port = ${port},
  [string]$DataDir = "data",
  [string]$ServerRoot = "servers"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Binary = Join-Path $Root "${binaryName}"
$DataPath = Join-Path $Root $DataDir
$ServerPath = Join-Path $Root $ServerRoot
$WebPath = Join-Path $Root "web"
$PidPath = Join-Path $DataPath "cliff.pid"
$StatePath = Join-Path $DataPath "cliff.json"
$LogPath = Join-Path $DataPath "cliff.log"
$ErrorLogPath = Join-Path $DataPath "cliff-error.log"

if (-not (Test-Path $Binary)) {
  throw "${binaryName} was not found next to run.ps1."
}

function Test-CliffProcess($ProcessId) {
  $Target = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
  if (-not $Target) { return $false }
  $Executable = [string]$Target.ExecutablePath
  $Command = [string]$Target.CommandLine
  return $Executable.EndsWith("${binaryName}", [System.StringComparison]::OrdinalIgnoreCase) -or $Command.ToLowerInvariant().Contains("cliff")
}

New-Item -ItemType Directory -Force -Path $DataPath | Out-Null
New-Item -ItemType Directory -Force -Path $ServerPath | Out-Null

if (Test-Path $PidPath) {
  $ExistingPid = [int](Get-Content $PidPath -Raw)
  $Existing = Get-Process -Id $ExistingPid -ErrorAction SilentlyContinue
  if ($Existing -and (Test-CliffProcess $ExistingPid)) {
    $ExistingHealth = $null
    try { $ExistingHealth = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 2 } catch { }
    if ($ExistingHealth -and $ExistingHealth.ok -and $ExistingHealth.daemon -eq "cliff") {
      Write-Host "Cliff is running."
      Write-Host "PID: $ExistingPid"
      Write-Host "Local: http://localhost:$Port"
      exit 0
    }
    Write-Host "PID $ExistingPid is alive, but Cliff did not answer on port $Port. Removing stale state."
  }
  Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
  Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
}

New-Item -ItemType File -Force -Path $LogPath | Out-Null
New-Item -ItemType File -Force -Path $ErrorLogPath | Out-Null
$Args = @("--host", "0.0.0.0", "--port", "$Port", "--data-dir", $DataPath, "--server-root", $ServerPath, "--web-dir", $WebPath, "--log-file", $LogPath)
$Process = Start-Process -FilePath $Binary -ArgumentList $Args -WorkingDirectory $Root -WindowStyle Hidden -PassThru
Set-Content -Path $PidPath -Value $Process.Id

$Health = $null
$Deadline = (Get-Date).AddSeconds(20)
do {
  try { $Health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 1 } catch { $Health = $null }
  if ($Health -and $Health.ok -and $Health.daemon -eq "cliff") { break }
  Start-Sleep -Milliseconds 500
} while ((Get-Date) -lt $Deadline -and -not $Process.HasExited)

if ((-not $Health) -and $Process.HasExited) {
  Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
  Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
  throw "Cliff failed to start. Check $ErrorLogPath for details."
}

Write-Host "Cliff is running."
Write-Host "PID: $($Process.Id)"
Write-Host "Local: http://localhost:$Port"
Write-Host "Logs: $LogPath"
Write-Host "Errors: $ErrorLogPath"
`;

    const stopPs1 = `param(
  [string]$DataDir = "data",
  [switch]$Force
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$DataPath = Join-Path $Root $DataDir
$PidPath = Join-Path $DataPath "cliff.pid"
$StatePath = Join-Path $DataPath "cliff.json"

function Test-CliffProcess($ProcessId) {
  $Target = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
  if (-not $Target) { return $false }
  $Executable = [string]$Target.ExecutablePath
  $Command = [string]$Target.CommandLine
  return $Executable.EndsWith("cliff.exe", [System.StringComparison]::OrdinalIgnoreCase) -or $Command.ToLowerInvariant().Contains("cliff")
}

if (-not (Test-Path $PidPath)) { Write-Host "Cliff is not running."; exit 0 }

$DaemonPid = [int](Get-Content $PidPath -Raw).Trim()
$Process = Get-Process -Id $DaemonPid -ErrorAction SilentlyContinue
if (-not $Process) { Remove-Item $PidPath, $StatePath -Force -ErrorAction SilentlyContinue; Write-Host "Cliff PID $DaemonPid is not running. Removed stale state."; exit 0 }
if (-not (Test-CliffProcess $DaemonPid)) { Remove-Item $PidPath, $StatePath -Force -ErrorAction SilentlyContinue; Write-Host "PID $DaemonPid is not Cliff. Removed stale state."; exit 0 }

if ($Force) { & taskkill.exe /PID $DaemonPid /T /F | Out-Null } else { & taskkill.exe /PID $DaemonPid /T | Out-Null }
$Deadline = (Get-Date).AddSeconds(10)
do { Start-Sleep -Milliseconds 250; $Process = Get-Process -Id $DaemonPid -ErrorAction SilentlyContinue } while ($Process -and (Get-Date) -lt $Deadline)
if ($Process) { throw "Cliff PID $DaemonPid did not stop. Re-run with -Force." }
Remove-Item $PidPath, $StatePath -Force -ErrorAction SilentlyContinue
Write-Host "Stopped Cliff PID $DaemonPid."
`;

    const statusPs1 = `param([int]$Port = ${port}, [string]$DataDir = "data")
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$DataPath = Join-Path $Root $DataDir
$PidPath = Join-Path $DataPath "cliff.pid"
if (-not (Test-Path $PidPath)) { Write-Host "Cliff is not running."; exit 0 }
$DaemonPid = [int](Get-Content $PidPath -Raw).Trim()
$Process = Get-Process -Id $DaemonPid -ErrorAction SilentlyContinue
if (-not $Process) { Remove-Item $PidPath, (Join-Path $DataPath "cliff.json") -Force -ErrorAction SilentlyContinue; Write-Host "Cliff PID $DaemonPid is not running."; exit 0 }
$Health = $null
try { $Health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 2 } catch { }
if ($Health -and $Health.ok) {
  Write-Host "Cliff is running."
  Write-Host "PID: $DaemonPid"
  Write-Host "Local: http://localhost:$Port"
  Write-Host "Version: $($Health.build.version) ($($Health.build.commit))"
  Write-Host "Uptime: $($Health.uptimeSeconds)s"
  exit 0
}
Write-Host "Cliff process is running but HTTP is not responding."
Write-Host "PID: $DaemonPid"
exit 1
`;

    await writeFile(path.join(packageDir, "run.ps1"), runPs1);
    await writeFile(path.join(packageDir, "stop.ps1"), stopPs1);
    await writeFile(path.join(packageDir, "status.ps1"), statusPs1);
  } else {
    const runSh = `#!/usr/bin/env sh
set -eu
PORT="\${PORT:-${port}}"
DATA_DIR="\${DATA_DIR:-data}"
SERVER_ROOT="\${SERVER_ROOT:-servers}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
BINARY="$ROOT/${binaryName}"
DATA_PATH="$ROOT/$DATA_DIR"
SERVER_PATH="$ROOT/$SERVER_ROOT"
WEB_PATH="$ROOT/web"
PID_PATH="$DATA_PATH/cliff.pid"
LOG_PATH="$DATA_PATH/cliff.log"
ERROR_LOG_PATH="$DATA_PATH/cliff-error.log"

if [ ! -x "$BINARY" ]; then echo "${binaryName} not found or not executable." >&2; exit 1; fi

looks_like_cliff() {
  case "$(ps -p "$1" -o command= 2>/dev/null || true)" in *cliff*) return 0 ;; *) return 1 ;; esac
}

mkdir -p "$DATA_PATH" "$SERVER_PATH"

if [ -f "$PID_PATH" ]; then
  existing_pid="$(cat "$PID_PATH" || true)"
  if [ -n "$existing_pid" ] && kill -0 "$existing_pid" 2>/dev/null && looks_like_cliff "$existing_pid"; then
    if command -v curl >/dev/null 2>&1 && curl -fsS --max-time 2 "http://127.0.0.1:$PORT/api/health" 2>/dev/null | grep -q '"daemon".*"cliff"'; then
      echo "Cliff is running."
      echo "PID: $existing_pid"
      echo "Local: http://localhost:$PORT"
      exit 0
    fi
  fi
  rm -f "$PID_PATH"
fi

nohup "$BINARY" --host 0.0.0.0 --port "$PORT" --data-dir "$DATA_PATH" --server-root "$SERVER_PATH" --web-dir "$WEB_PATH" --log-file "$LOG_PATH" >"$LOG_PATH" 2>"$ERROR_LOG_PATH" &
pid="$!"
echo "$pid" > "$PID_PATH"

ready=false
if command -v curl >/dev/null 2>&1; then
  attempts=0
  while [ "$attempts" -lt 40 ]; do
    if curl -fsS --max-time 1 "http://127.0.0.1:$PORT/api/health" 2>/dev/null | grep -q '"daemon".*"cliff"'; then ready=true; break; fi
    if ! kill -0 "$pid" 2>/dev/null; then break; fi
    attempts=$((attempts + 1)); sleep 0.5
  done
fi

if [ "$ready" != "true" ] && ! kill -0 "$pid" 2>/dev/null; then
  rm -f "$PID_PATH"
  echo "Cliff failed to start. Check $ERROR_LOG_PATH." >&2; exit 1
fi

echo "Cliff is running."
echo "PID: $pid"
echo "Local: http://localhost:$PORT"
echo "Logs: $LOG_PATH"
echo "Errors: $ERROR_LOG_PATH"
`;

    const stopSh = `#!/usr/bin/env sh
set -eu
DATA_DIR="\${DATA_DIR:-data}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PID_PATH="$ROOT/$DATA_DIR/cliff.pid"
if [ ! -f "$PID_PATH" ]; then echo "Cliff is not running."; exit 0; fi
pid="$(cat "$PID_PATH" || true)"
if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then rm -f "$PID_PATH"; echo "Cliff PID $pid is not running."; exit 0; fi
kill "$pid" 2>/dev/null || true
attempts=0; while kill -0 "$pid" 2>/dev/null && [ "$attempts" -lt 40 ]; do attempts=$((attempts+1)); sleep 0.25; done
if kill -0 "$pid" 2>/dev/null; then
  if [ "\${FORCE:-0}" = "1" ]; then kill -9 "$pid" 2>/dev/null || true; sleep 0.25
  else echo "Cliff PID $pid did not stop. Re-run with FORCE=1." >&2; exit 1; fi
fi
rm -f "$PID_PATH"
echo "Stopped Cliff PID $pid."
`;

    const statusSh = `#!/usr/bin/env sh
set -eu
PORT="\${PORT:-${port}}"
DATA_DIR="\${DATA_DIR:-data}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PID_PATH="$ROOT/$DATA_DIR/cliff.pid"
if [ ! -f "$PID_PATH" ]; then echo "Cliff is not running."; exit 0; fi
pid="$(cat "$PID_PATH" || true)"
if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then rm -f "$PID_PATH"; echo "Cliff PID $pid is not running."; exit 0; fi
if command -v curl >/dev/null 2>&1 && curl -fsS --max-time 2 "http://127.0.0.1:$PORT/api/health" 2>/dev/null | grep -q '"ok"'; then
  echo "Cliff is running."
  echo "PID: $pid"
  echo "Local: http://localhost:$PORT"
  exit 0
fi
echo "Cliff process is running but HTTP is not responding."
echo "PID: $pid"
exit 1
`;

    await writeFile(path.join(packageDir, "run.sh"), runSh);
    await writeFile(path.join(packageDir, "stop.sh"), stopSh);
    await writeFile(path.join(packageDir, "status.sh"), statusSh);
    await chmod(path.join(packageDir, "run.sh"), 0o755);
    await chmod(path.join(packageDir, "stop.sh"), 0o755);
    await chmod(path.join(packageDir, "status.sh"), 0o755);
  }
}

async function packageTarget(target) {
  const { os: goos, arch: goarch, binary } = target;
  const platformName = `${goos}-${goarch}`;
  const packageDir = path.join(distRoot, `cliff-${platformName}`);
  const sourceBinary = path.join(binDir, `${platformName}-${binary}`);

  if (!existsSync(sourceBinary)) {
    throw new Error(`Binary not found: ${sourceBinary}`);
  }

  await rm(packageDir, { recursive: true, force: true });
  await mkdir(packageDir, { recursive: true });

  // Copy binary
  await cp(sourceBinary, path.join(packageDir, binary));
  if (goos !== "windows") {
    await chmod(path.join(packageDir, binary), 0o755);
  }

  // Copy web assets
  await cp(webDir, path.join(packageDir, "web"), { recursive: true });

  // Create empty data and servers directories
  await mkdir(path.join(packageDir, "data"), { recursive: true });
  await mkdir(path.join(packageDir, "servers"), { recursive: true });

  // Generate runner scripts
  await generateRunnerScripts(target, packageDir);

  // Write build.json and package-manifest.json
  const metadata = {
    version: packageJson.version || "dev",
    commit: process.env.GITHUB_SHA?.slice(0, 12) || "unknown",
    builtAt: new Date().toISOString(),
  };
  await writeFile(path.join(packageDir, "build.json"), `${JSON.stringify(metadata, null, 2)}\n`);

  // Write package-manifest.json (expected by install scripts)
  const binaryPath = path.join(packageDir, binary);
  const packageManifest = {
    ...metadata,
    binary,
    binarySizeBytes: (await stat(binaryPath)).size,
    binarySHA256: await fileSHA256(binaryPath),
    generatedAt: new Date().toISOString(),
  };
  await writeFile(path.join(packageDir, "package-manifest.json"), `${JSON.stringify(packageManifest, null, 2)}\n`);

  // Write README.txt
  const isWindows = goos === "windows";
  const runCmd = isWindows
    ? "powershell -ExecutionPolicy Bypass -File .\\run.ps1"
    : "sh ./run.sh";
  const installCmd = isWindows
    ? "irm getcliff.dev/install.ps1 | iex"
    : "curl -fsSL getcliff.dev/install.sh | sh";

  await writeFile(
    path.join(packageDir, "README.txt"),
    [
      "Cliff — Minecraft server management dashboard",
      "",
      `Version: ${metadata.version}`,
      `Commit: ${metadata.commit}`,
      `Built: ${metadata.builtAt}`,
      "",
      "One-line install:",
      `  ${installCmd}`,
      "",
      "Run from this folder:",
      `  ${runCmd}`,
      "",
      "Then open: http://localhost:8080",
      "",
      "Same-network access: http://<host-lan-ip>:8080",
      "",
      "Controls:",
      isWindows
        ? "  run.ps1     Start Cliff (background)"
        : "  run.sh      Start Cliff (background)",
      isWindows
        ? "  status.ps1  Check if Cliff is running"
        : "  status.sh   Check if Cliff is running",
      isWindows
        ? "  stop.ps1    Stop Cliff"
        : "  stop.sh     Stop Cliff",
      "",
      "Folders:",
      "  data/     SQLite database, metadata cache, logs",
      "  servers/  Minecraft server folders",
      "  web/      static dashboard assets",
      "",
    ].join("\n"),
  );

  // Create ZIP
  const archiveName = `cliff-${metadata.version}-${platformName}.zip`;
  const archivePath = path.join(distRoot, archiveName);
  const zip = new AdmZip();
  const files = await directoryFiles(packageDir);
  for (const filePath of files) {
    const relative = path.relative(packageDir, filePath).replaceAll(path.sep, "/");
    zip.addFile(path.posix.join("cliff", relative), await readFile(filePath));
  }
  zip.writeZip(archivePath);

  const archiveSize = (await stat(archivePath)).size;
  const archiveSHA = await fileSHA256(archivePath);

  console.log(`Packaged ${platformName}: ${formatBytes(archiveSize)} (${archiveName})`);

  return { platformName, archiveName, archivePath, archiveSize, archiveSHA };
}

try {
  // Clean dist (keep bin/)
  for (const entry of await readdir(distRoot)) {
    if (entry === "bin") continue;
    await rm(path.join(distRoot, entry), { recursive: true, force: true });
  }

  const results = [];
  for (const target of targets) {
    results.push(await packageTarget(target));
  }

  // Write SHA256SUMS.txt
  const checksumLines = [];
  for (const result of results) {
    checksumLines.push(`${result.archiveSHA}  ${result.archiveName}`);
  }
  const checksumsPath = path.join(distRoot, "SHA256SUMS.txt");
  await writeFile(checksumsPath, checksumLines.join("\n") + "\n");
  console.log(`Wrote SHA256SUMS.txt`);

  // Write release manifest
  const releaseManifest = {
    schemaVersion: 1,
    name: "cliff",
    version: packageJson.version || "dev",
    commit: process.env.GITHUB_SHA?.slice(0, 12) || "unknown",
    builtAt: new Date().toISOString(),
    platforms: results.map((r) => ({
      platform: r.platformName,
      archive: r.archiveName,
      sizeBytes: r.archiveSize,
      sha256: r.archiveSHA,
    })),
    commands: {
      install: {
        windows: "irm getcliff.dev/install.ps1 | iex",
        unix: "curl -fsSL getcliff.dev/install.sh | sh",
      },
    },
  };
  await writeFile(path.join(distRoot, "cliff-release.json"), `${JSON.stringify(releaseManifest, null, 2)}\n`);
  console.log(`Wrote cliff-release.json`);

  // Copy install scripts to dist
  for (const fileName of ["install.ps1", "install.sh", "install-package.ps1", "install-package.sh"]) {
    const source = path.join(root, "scripts", fileName);
    if (existsSync(source)) {
      await cp(source, path.join(distRoot, fileName));
      if (fileName.endsWith(".sh")) {
        await chmod(path.join(distRoot, fileName), 0o755);
      }
    }
  }

  console.log("\n=== Build summary ===");
  for (const result of results) {
    console.log(`  ${result.platformName.padEnd(15)} ${formatBytes(result.archiveSize)}`);
  }
} catch (error) {
  console.error(error);
  process.exit(1);
}
