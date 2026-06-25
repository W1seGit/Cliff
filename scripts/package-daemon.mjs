import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { chmod, cp, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import AdmZip from "adm-zip";
import os from "node:os";
import path from "node:path";

const root = process.cwd();
const daemonDir = path.join(root, "daemon");
const webDir = path.join(daemonDir, "web");
const distRoot = path.join(root, "dist");
const packageDir = path.join(distRoot, "cliff");
const binaryName = process.platform === "win32" ? "cliff.exe" : "cliff";
const binaryPath = path.join(packageDir, binaryName);
const releaseInstallerFiles = ["install.ps1", "install.sh", "install-package.ps1", "install-package.sh"];
const tempRoot = await mkdtemp(path.join(os.tmpdir(), "cliff-daemon-package-"));
const webBackup = path.join(tempRoot, "web-backup");
const packageJson = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
const buildInfoImport = "github.com/W1seGit/Cliff/daemon/internal/buildinfo";

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
}

function run(command, args, cwd = root) {
  const executable = commandName(command);
  const useShell = process.platform === "win32" && (command === "npm" || command === "npx");
  const result = spawnSync(executable, args, {
    cwd,
    stdio: "inherit",
    shell: useShell,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed with status ${result.status}`);
}

function commandOutput(command, args) {
  const executable = commandName(command);
  const useShell = process.platform === "win32" && (command === "npm" || command === "npx");
  const result = spawnSync(executable, args, {
    cwd: root,
    encoding: "utf8",
    shell: useShell,
  });
  if (result.status !== 0 || result.error) return "unknown";
  return result.stdout.trim() || "unknown";
}

function buildMetadata() {
  return {
    version: packageJson.version || "dev",
    commit: commandOutput("git", ["rev-parse", "--short=12", "HEAD"]),
    builtAt: new Date().toISOString(),
  };
}

function ldflags(metadata) {
  return [
    "-s",
    "-w",
    `-X ${buildInfoImport}.Version=${metadata.version}`,
    `-X ${buildInfoImport}.Commit=${metadata.commit}`,
    `-X ${buildInfoImport}.BuiltAt=${metadata.builtAt}`,
  ].join(" ");
}

function runCommandText() {
  const base = process.platform === "win32" ? ".\\cliff.exe" : "./cliff";
  return `${base} --host 0.0.0.0 --port 8080 --data-dir data --server-root servers --web-dir web`;
}

async function writePackagedRunnerScripts() {
  const runPowerShell = String.raw`param(
  [int]$Port = 8080,
  [string]$DataDir = "data",
  [string]$ServerRoot = "servers"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Binary = Join-Path $Root "cliff.exe"
$DataPath = Join-Path $Root $DataDir
$ServerPath = Join-Path $Root $ServerRoot
$WebPath = Join-Path $Root "web"
$PidPath = Join-Path $DataPath "cliff.pid"
$StatePath = Join-Path $DataPath "cliff.json"
$LogPath = Join-Path $DataPath "cliff.log"
$ErrorLogPath = Join-Path $DataPath "cliff-error.log"

if (-not (Test-Path $Binary)) {
  throw "cliff.exe was not found next to run.ps1."
}

function Test-cliffProcess($ProcessId) {
  $Target = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
  if (-not $Target) {
    return $false
  }
  $Executable = [string]$Target.ExecutablePath
  $Command = [string]$Target.CommandLine
  return $Executable.EndsWith("cliff.exe", [System.StringComparison]::OrdinalIgnoreCase) -or $Command.ToLowerInvariant().Contains("cliff")
}

New-Item -ItemType Directory -Force -Path $DataPath | Out-Null
New-Item -ItemType Directory -Force -Path $ServerPath | Out-Null

if (Test-Path $PidPath) {
  $ExistingPid = [int](Get-Content $PidPath -Raw)
  $Existing = Get-Process -Id $ExistingPid -ErrorAction SilentlyContinue
  if ($Existing -and (Test-cliffProcess $ExistingPid)) {
    $ExistingHealth = $null
    try {
      $ExistingHealth = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 2
    } catch {
      $ExistingHealth = $null
    }
    if ($ExistingHealth -and $ExistingHealth.ok -and $ExistingHealth.daemon -eq "cliff") {
      $ExistingLanUrls = @()
      if ($ExistingHealth.lanUrls) {
        $ExistingLanUrls = @($ExistingHealth.lanUrls)
      }
      $ExistingState = @{
        pid = $ExistingPid
        ready = $true
        port = $Port
        host = "0.0.0.0"
        localUrl = $(if ($ExistingHealth.localUrl) { $ExistingHealth.localUrl } else { "http://localhost:$Port" })
        lanUrls = @($ExistingLanUrls)
        dataDir = $DataPath
        serverRoot = $ServerPath
        packageDir = $Root
        webDir = $WebPath
        daemonBinary = $Binary
        daemonArgs = @("--host", "0.0.0.0", "--port", "$Port", "--data-dir", $DataPath, "--server-root", $ServerPath, "--web-dir", $WebPath, "--log-file", $LogPath)
        daemonCommand = "$Binary --host 0.0.0.0 --port $Port --data-dir $DataPath --server-root $ServerPath --web-dir $WebPath --log-file $LogPath"
        logPath = $LogPath
        errorLogPath = $ErrorLogPath
        health = $ExistingHealth
        build = $ExistingHealth.build
        platform = $ExistingHealth.platform
        updatedAt = (Get-Date).ToUniversalTime().ToString("o")
      }
      $ExistingState | ConvertTo-Json -Depth 4 | Set-Content -Path $StatePath
      Write-Host "Cliff is running."
      Write-Host "PID: $ExistingPid"
      Write-Host "Local: $($ExistingState.localUrl)"
      foreach ($Url in $ExistingLanUrls) {
        Write-Host "Same network: $Url"
      }
      if (-not $ExistingLanUrls) {
        Write-Host "Same network: no LAN IPv4 address detected"
      }
      Write-Host "Logs: $LogPath"
      Write-Host "Errors: $ErrorLogPath"
      exit 0
    }
    Write-Host "PID $ExistingPid is alive, but Cliff did not answer on port $Port. Removing stale daemon state."
  } elseif ($Existing) {
    Write-Host "PID $ExistingPid does not look like a Cliff daemon. Removing stale daemon state."
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
  try {
    $Health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 1
  } catch {
    $Health = $null
  }
  if ($Health -and $Health.ok -and $Health.daemon -eq "cliff") {
    break
  }
  Start-Sleep -Milliseconds 500
} while ((Get-Date) -lt $Deadline -and -not $Process.HasExited)

if ((-not $Health) -and $Process.HasExited) {
  Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
  Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
  throw "Cliff failed to start. Check $ErrorLogPath for details."
}

$LanUrls = [System.Net.NetworkInformation.NetworkInterface]::GetAllNetworkInterfaces() |
  ForEach-Object { $_.GetIPProperties().UnicastAddresses } |
  Where-Object { $_.Address.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetwork -and -not [System.Net.IPAddress]::IsLoopback($_.Address) } |
  ForEach-Object { "http://$($_.Address):$Port" }

if ($Health -and $Health.lanUrls) {
  $LanUrls = @($Health.lanUrls)
}

$State = @{
  pid = $Process.Id
  ready = [bool]$Health
  port = $Port
  host = "0.0.0.0"
  localUrl = $(if ($Health -and $Health.localUrl) { $Health.localUrl } else { "http://localhost:$Port" })
  lanUrls = @($LanUrls)
  dataDir = $DataPath
  serverRoot = $ServerPath
  packageDir = $Root
  webDir = $WebPath
  daemonBinary = $Binary
  daemonArgs = @($Args)
  daemonCommand = "$Binary $($Args -join " ")"
  logPath = $LogPath
  errorLogPath = $ErrorLogPath
  health = $Health
  build = $(if ($Health) { $Health.build } else { $null })
  platform = $(if ($Health) { $Health.platform } else { $null })
  updatedAt = (Get-Date).ToUniversalTime().ToString("o")
}
$State | ConvertTo-Json -Depth 4 | Set-Content -Path $StatePath

if ($Health) {
  Write-Host "Cliff is running."
} else {
  Write-Host "Cliff was started, but did not respond yet."
}
Write-Host "PID: $($Process.Id)"
Write-Host "Local: $($State.localUrl)"
foreach ($Url in $LanUrls) {
  Write-Host "Same network: $Url"
}
if (-not $LanUrls) {
  Write-Host "Same network: no LAN IPv4 address detected"
}
Write-Host "Logs: $LogPath"
Write-Host "Errors: $ErrorLogPath"
`;

  const stopPowerShell = String.raw`param(
  [string]$DataDir = "data",
  [switch]$Force
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$DataPath = Join-Path $Root $DataDir
$PidPath = Join-Path $DataPath "cliff.pid"
$StatePath = Join-Path $DataPath "cliff.json"

function Test-cliffProcess($ProcessId) {
  $Target = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
  if (-not $Target) {
    return $false
  }
  $Executable = [string]$Target.ExecutablePath
  $Command = [string]$Target.CommandLine
  return $Executable.EndsWith("cliff.exe", [System.StringComparison]::OrdinalIgnoreCase) -or $Command.ToLowerInvariant().Contains("cliff")
}

if (-not (Test-Path $PidPath)) {
  Write-Host "Cliff daemon is not running."
  exit 0
}

$DaemonPidText = Get-Content $PidPath -Raw
$DaemonPid = [int]$DaemonPidText.Trim()
$Process = Get-Process -Id $DaemonPid -ErrorAction SilentlyContinue
if (-not $Process) {
  Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
  Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
  Write-Host "Cliff daemon PID $DaemonPid is not running. Removed stale state."
  exit 0
}

if (-not (Test-cliffProcess $DaemonPid)) {
  Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
  Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
  Write-Host "PID $DaemonPid does not look like a Cliff daemon. Removed stale state."
  exit 0
}

if ($Force) {
  & taskkill.exe /PID $DaemonPid /T /F | Out-Null
} else {
  & taskkill.exe /PID $DaemonPid /T | Out-Null
}

$Deadline = (Get-Date).AddSeconds(10)
do {
  Start-Sleep -Milliseconds 250
  $Process = Get-Process -Id $DaemonPid -ErrorAction SilentlyContinue
} while ($Process -and (Get-Date) -lt $Deadline)

if ($Process) {
  throw "Cliff daemon PID $DaemonPid did not stop. Re-run with -Force."
}

Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
Write-Host "Stopped Cliff daemon PID $DaemonPid."
`;

  const statusPowerShell = String.raw`param(
  [int]$Port = 8080,
  [string]$DataDir = "data"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$DataPath = Join-Path $Root $DataDir
$PidPath = Join-Path $DataPath "cliff.pid"
$StatePath = Join-Path $DataPath "cliff.json"

function Test-cliffProcess($ProcessId) {
  $Target = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
  if (-not $Target) {
    return $false
  }
  $Executable = [string]$Target.ExecutablePath
  $Command = [string]$Target.CommandLine
  return $Executable.EndsWith("cliff.exe", [System.StringComparison]::OrdinalIgnoreCase) -or $Command.ToLowerInvariant().Contains("cliff")
}

if (-not (Test-Path $PidPath)) {
  Write-Host "Cliff daemon is not running."
  exit 0
}

$DaemonPid = [int](Get-Content $PidPath -Raw).Trim()
$Process = Get-Process -Id $DaemonPid -ErrorAction SilentlyContinue
if (-not $Process) {
  Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
  Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
  Write-Host "Cliff daemon PID $DaemonPid is not running. Removed stale state."
  exit 0
}

if (-not (Test-cliffProcess $DaemonPid)) {
  Remove-Item $PidPath -Force -ErrorAction SilentlyContinue
  Remove-Item $StatePath -Force -ErrorAction SilentlyContinue
  Write-Host "PID $DaemonPid does not look like a Cliff daemon. Removed stale state."
  exit 0
}

$State = $null
if (Test-Path $StatePath) {
  $State = Get-Content $StatePath -Raw | ConvertFrom-Json
  if ($State.port) {
    $Port = [int]$State.port
  }
}

$Health = $null
try {
  $Health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 2
} catch {
  $Health = $null
}

if ($Health -and $Health.ok) {
  Write-Host "Cliff daemon is running."
  Write-Host "PID: $DaemonPid"
  Write-Host "Local: $($Health.localUrl)"
  foreach ($Url in $Health.lanUrls) {
    Write-Host "Same network: $Url"
  }
  Write-Host "Version: $($Health.build.version) ($($Health.build.commit))"
  Write-Host "Uptime: $($Health.uptimeSeconds)s"
  Write-Host "Daemon heap: $($Health.self.heapAllocBytes) bytes allocated / $($Health.self.heapSysBytes) bytes reserved"
  $WorkingSet = (Get-Process -Id $DaemonPid -ErrorAction SilentlyContinue).WorkingSet64
  if ($WorkingSet) {
    Write-Host "Daemon memory: $WorkingSet bytes working set"
  }
  Write-Host "Daemon goroutines: $($Health.self.goroutines)"
  if ($State -and $State.daemonCommand) {
    Write-Host "Command: $($State.daemonCommand)"
  }
  if ($State -and $State.logPath) {
    Write-Host "Logs: $($State.logPath)"
  }
  if ($State -and $State.errorLogPath) {
    Write-Host "Errors: $($State.errorLogPath)"
  }
  exit 0
}

Write-Host "Cliff daemon process is running, but HTTP is not responding."
Write-Host "PID: $DaemonPid"
Write-Host "Local: http://localhost:$Port"
if ($State -and $State.daemonCommand) {
  Write-Host "Command: $($State.daemonCommand)"
}
if ($State -and $State.logPath) {
  Write-Host "Logs: $($State.logPath)"
}
if ($State -and $State.errorLogPath) {
  Write-Host "Errors: $($State.errorLogPath)"
}
exit 1
`;

  const runShell = `#!/usr/bin/env sh
set -eu

PORT="\${PORT:-8080}"
DATA_DIR="\${DATA_DIR:-data}"
SERVER_ROOT="\${SERVER_ROOT:-servers}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
BINARY="$ROOT/cliff"
DATA_PATH="$ROOT/$DATA_DIR"
SERVER_PATH="$ROOT/$SERVER_ROOT"
WEB_PATH="$ROOT/web"
PID_PATH="$DATA_PATH/cliff.pid"
STATE_PATH="$DATA_PATH/cliff.json"
LOG_PATH="$DATA_PATH/cliff.log"
ERROR_LOG_PATH="$DATA_PATH/cliff-error.log"

if [ ! -x "$BINARY" ]; then
  echo "cliff was not found next to run.sh, or it is not executable." >&2
  exit 1
fi

looks_like_daemon() {
  target_pid="$1"
  command_line="$(ps -p "$target_pid" -o command= 2>/dev/null || true)"
  case "$command_line" in
    *cliff*|*cliff*|*cliff*) return 0 ;;
    *) return 1 ;;
  esac
}

collect_lan_urls_json() {
  sep=""
  printf '['
  if command -v hostname >/dev/null 2>&1; then
    for address in $(hostname -I 2>/dev/null || true); do
      case "$address" in
        127.*|""|*:*|*.*.*.*.*) ;;
        *.*.*.*)
          printf '%s"http://%s:%s"' "$sep" "$address" "$PORT"
          sep=","
          ;;
      esac
    done
  fi
  printf ']'
}

json_string() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

print_lan_urls() {
  printed=0
  if command -v hostname >/dev/null 2>&1; then
    for address in $(hostname -I 2>/dev/null || true); do
      case "$address" in
        127.*|""|*:*|*.*.*.*.*) ;;
        *.*.*.*)
          echo "Same network: http://$address:$PORT"
          printed=1
          ;;
      esac
    done
  fi
  if [ "$printed" = "0" ]; then
    echo "Same network: no LAN IPv4 address detected"
  fi
}

mkdir -p "$DATA_PATH" "$SERVER_PATH"
LAN_URLS_JSON="$(collect_lan_urls_json)"
DATA_PATH_JSON="$(json_string "$DATA_PATH")"
SERVER_PATH_JSON="$(json_string "$SERVER_PATH")"
ROOT_JSON="$(json_string "$ROOT")"
WEB_PATH_JSON="$(json_string "$WEB_PATH")"
BINARY_JSON="$(json_string "$BINARY")"
LOG_PATH_JSON="$(json_string "$LOG_PATH")"
ERROR_LOG_PATH_JSON="$(json_string "$ERROR_LOG_PATH")"
DAEMON_COMMAND_JSON="$(json_string "$BINARY --host 0.0.0.0 --port $PORT --data-dir $DATA_PATH --server-root $SERVER_PATH --web-dir $WEB_PATH --log-file $LOG_PATH")"

if [ -f "$PID_PATH" ]; then
  existing_pid="$(cat "$PID_PATH" || true)"
  if [ -n "$existing_pid" ] && kill -0 "$existing_pid" 2>/dev/null && looks_like_daemon "$existing_pid"; then
    existing_health=""
    if command -v curl >/dev/null 2>&1; then
      existing_health="$(curl -fsS --max-time 2 "http://127.0.0.1:$PORT/api/health" 2>/dev/null || true)"
    fi
    if printf '%s' "$existing_health" | grep -q '"daemon"[[:space:]]*:[[:space:]]*"cliff"'; then
      cat > "$STATE_PATH" <<JSON
{
  "pid": $existing_pid,
  "ready": true,
  "port": $PORT,
  "host": "0.0.0.0",
  "localUrl": "http://localhost:$PORT",
  "lanUrls": $LAN_URLS_JSON,
  "dataDir": "$DATA_PATH_JSON",
  "serverRoot": "$SERVER_PATH_JSON",
  "packageDir": "$ROOT_JSON",
  "webDir": "$WEB_PATH_JSON",
  "daemonBinary": "$BINARY_JSON",
  "daemonCommand": "$DAEMON_COMMAND_JSON",
  "logPath": "$LOG_PATH_JSON",
  "errorLogPath": "$ERROR_LOG_PATH_JSON",
  "health": $existing_health,
  "updatedAt": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
}
JSON
      echo "Cliff is running."
      echo "PID: $existing_pid"
      echo "Local: http://localhost:$PORT"
      print_lan_urls
      echo "Logs: $LOG_PATH"
      echo "Errors: $ERROR_LOG_PATH"
      exit 0
    fi
    echo "PID $existing_pid is alive, but Cliff did not answer on port $PORT. Removing stale daemon state."
  elif [ -n "$existing_pid" ] && kill -0 "$existing_pid" 2>/dev/null; then
    echo "PID $existing_pid does not look like a Cliff daemon. Removing stale daemon state."
  fi
  rm -f "$PID_PATH" "$STATE_PATH"
fi

nohup "$BINARY" --host 0.0.0.0 --port "$PORT" --data-dir "$DATA_PATH" --server-root "$SERVER_PATH" --web-dir "$WEB_PATH" --log-file "$LOG_PATH" >"$LOG_PATH" 2>"$ERROR_LOG_PATH" &
pid="$!"
echo "$pid" > "$PID_PATH"

ready=false
health_json=null
if command -v curl >/dev/null 2>&1; then
  attempts=0
  while [ "$attempts" -lt 40 ]; do
    health_response="$(curl -fsS --max-time 1 "http://127.0.0.1:$PORT/api/health" 2>/dev/null || true)"
    if printf '%s' "$health_response" | grep -q '"daemon"[[:space:]]*:[[:space:]]*"cliff"'; then
      ready=true
      health_json="$health_response"
      break
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      break
    fi
    attempts="$((attempts + 1))"
    sleep 0.5
  done
fi

if [ "$ready" != "true" ] && ! kill -0 "$pid" 2>/dev/null; then
  rm -f "$PID_PATH" "$STATE_PATH"
  echo "Cliff failed to start. Check $ERROR_LOG_PATH for details." >&2
  exit 1
fi

cat > "$STATE_PATH" <<JSON
{
  "pid": $pid,
  "ready": $ready,
  "port": $PORT,
  "host": "0.0.0.0",
  "localUrl": "http://localhost:$PORT",
  "lanUrls": $LAN_URLS_JSON,
  "dataDir": "$DATA_PATH_JSON",
  "serverRoot": "$SERVER_PATH_JSON",
  "packageDir": "$ROOT_JSON",
  "webDir": "$WEB_PATH_JSON",
  "daemonBinary": "$BINARY_JSON",
  "daemonCommand": "$DAEMON_COMMAND_JSON",
  "logPath": "$LOG_PATH_JSON",
  "errorLogPath": "$ERROR_LOG_PATH_JSON",
  "health": $health_json,
  "updatedAt": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
}
JSON

if [ "$ready" = "true" ]; then
  echo "Cliff is running."
else
  echo "Cliff was started, but did not respond yet."
fi
echo "PID: $pid"
echo "Local: http://localhost:$PORT"
print_lan_urls
echo "Logs: $LOG_PATH"
echo "Errors: $ERROR_LOG_PATH"
`;

  const stopShell = `#!/usr/bin/env sh
set -eu

DATA_DIR="\${DATA_DIR:-data}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
DATA_PATH="$ROOT/$DATA_DIR"
PID_PATH="$DATA_PATH/cliff.pid"
STATE_PATH="$DATA_PATH/cliff.json"

looks_like_daemon() {
  target_pid="$1"
  command_line="$(ps -p "$target_pid" -o command= 2>/dev/null || true)"
  case "$command_line" in
    *cliff*|*cliff*|*cliff*) return 0 ;;
    *) return 1 ;;
  esac
}

if [ ! -f "$PID_PATH" ]; then
  echo "Cliff daemon is not running."
  exit 0
fi

pid="$(cat "$PID_PATH" || true)"
if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
  rm -f "$PID_PATH" "$STATE_PATH"
  echo "Cliff daemon PID $pid is not running. Removed stale state."
  exit 0
fi

if ! looks_like_daemon "$pid"; then
  rm -f "$PID_PATH" "$STATE_PATH"
  echo "PID $pid does not look like a Cliff daemon. Removed stale state."
  exit 0
fi

kill "$pid" 2>/dev/null || true
attempts=0
while kill -0 "$pid" 2>/dev/null && [ "$attempts" -lt 40 ]; do
  attempts="$((attempts + 1))"
  sleep 0.25
done

if kill -0 "$pid" 2>/dev/null; then
  if [ "\${FORCE:-0}" = "1" ]; then
    kill -9 "$pid" 2>/dev/null || true
    sleep 0.25
  else
    echo "Cliff daemon PID $pid did not stop. Re-run with FORCE=1." >&2
    exit 1
  fi
fi

rm -f "$PID_PATH" "$STATE_PATH"
echo "Stopped Cliff daemon PID $pid."
`;

  const statusShell = `#!/usr/bin/env sh
set -eu

PORT="\${PORT:-8080}"
DATA_DIR="\${DATA_DIR:-data}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
DATA_PATH="$ROOT/$DATA_DIR"
PID_PATH="$DATA_PATH/cliff.pid"
STATE_PATH="$DATA_PATH/cliff.json"

looks_like_daemon() {
  target_pid="$1"
  command_line="$(ps -p "$target_pid" -o command= 2>/dev/null || true)"
  case "$command_line" in
    *cliff*|*cliff*|*cliff*) return 0 ;;
    *) return 1 ;;
  esac
}

if [ ! -f "$PID_PATH" ]; then
  echo "Cliff daemon is not running."
  exit 0
fi

pid="$(cat "$PID_PATH" || true)"
if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
  rm -f "$PID_PATH" "$STATE_PATH"
  echo "Cliff daemon PID $pid is not running. Removed stale state."
  exit 0
fi

if ! looks_like_daemon "$pid"; then
  rm -f "$PID_PATH" "$STATE_PATH"
  echo "PID $pid does not look like a Cliff daemon. Removed stale state."
  exit 0
fi

if [ -f "$STATE_PATH" ]; then
  state_port="$(sed -n 's/.*"port":[[:space:]]*\\([0-9][0-9]*\\).*/\\1/p' "$STATE_PATH" | head -n 1)"
  if [ -n "$state_port" ]; then
    PORT="$state_port"
  fi
fi

if command -v curl >/dev/null 2>&1; then
  health="$(curl -fsS --max-time 2 "http://127.0.0.1:$PORT/api/health" 2>/dev/null || true)"
else
  health=""
fi

if [ -n "$health" ]; then
  echo "Cliff daemon is running."
  echo "PID: $pid"
  echo "Local: http://localhost:$PORT"
  echo "$health" | sed -n 's/.*"uptimeSeconds":[[:space:]]*\\([0-9][0-9]*\\).*/Uptime: \\1s/p'
  heap_alloc="$(printf '%s' "$health" | sed -n 's/.*"heapAllocBytes":[[:space:]]*\\([0-9][0-9]*\\).*/\\1/p' | head -n 1)"
  heap_sys="$(printf '%s' "$health" | sed -n 's/.*"heapSysBytes":[[:space:]]*\\([0-9][0-9]*\\).*/\\1/p' | head -n 1)"
  goroutines="$(printf '%s' "$health" | sed -n 's/.*"goroutines":[[:space:]]*\\([0-9][0-9]*\\).*/\\1/p' | head -n 1)"
  if [ -n "$heap_alloc" ] && [ -n "$heap_sys" ]; then
    echo "Daemon heap: $heap_alloc bytes allocated / $heap_sys bytes reserved"
  fi
  working_set="$(ps -o rss= -p "$pid" 2>/dev/null | awk '{print $1 * 1024}' | head -n 1)"
  if [ -n "$working_set" ]; then
    echo "Daemon memory: $working_set bytes working set"
  fi
  if [ -n "$goroutines" ]; then
    echo "Daemon goroutines: $goroutines"
  fi
  if [ -f "$STATE_PATH" ]; then
    command_text="$(sed -n 's/.*"daemonCommand":[[:space:]]*"\\([^"]*\\)".*/\\1/p' "$STATE_PATH" | head -n 1)"
    log_path="$(sed -n 's/.*"logPath":[[:space:]]*"\\([^"]*\\)".*/\\1/p' "$STATE_PATH" | head -n 1)"
    error_log_path="$(sed -n 's/.*"errorLogPath":[[:space:]]*"\\([^"]*\\)".*/\\1/p' "$STATE_PATH" | head -n 1)"
    if [ -n "$command_text" ]; then
      echo "Command: $command_text"
    fi
    if [ -n "$log_path" ]; then
      echo "Logs: $log_path"
    fi
    if [ -n "$error_log_path" ]; then
      echo "Errors: $error_log_path"
    fi
  fi
  exit 0
fi

echo "Cliff daemon process is running, but HTTP is not responding."
echo "PID: $pid"
echo "Local: http://localhost:$PORT"
exit 1
`;

  await writeFile(path.join(packageDir, "run.ps1"), runPowerShell);
  await writeFile(path.join(packageDir, "status.ps1"), statusPowerShell);
  await writeFile(path.join(packageDir, "stop.ps1"), stopPowerShell);
  await writeFile(path.join(packageDir, "run.sh"), runShell);
  await writeFile(path.join(packageDir, "status.sh"), statusShell);
  await writeFile(path.join(packageDir, "stop.sh"), stopShell);
  await chmod(path.join(packageDir, "run.sh"), 0o755);
  await chmod(path.join(packageDir, "status.sh"), 0o755);
  await chmod(path.join(packageDir, "stop.sh"), 0o755);
}

function archiveBaseName(metadata) {
  const version = String(metadata.version || "dev").replace(/[^a-zA-Z0-9._-]/g, "-");
  return `cliff-${version}-${process.platform}-${process.arch}`;
}

async function fileSize(filePath) {
  const info = await stat(filePath);
  return info.size;
}

async function directorySize(dirPath, options = {}) {
  let total = 0;
  const entries = await readdir(dirPath, { withFileTypes: true }).catch(() => []);
  for (const entry of entries) {
    const entryPath = path.join(dirPath, entry.name);
    const relative = path.relative(options.root ?? dirPath, entryPath).replaceAll(path.sep, "/");
    if (options.exclude?.has(relative)) continue;
    if (entry.isDirectory()) total += await directorySize(entryPath, { ...options, root: options.root ?? dirPath });
    else if (entry.isFile()) total += await fileSize(entryPath);
  }
  return total;
}

async function fileSHA256(filePath) {
  const data = await readFile(filePath);
  return createHash("sha256").update(data).digest("hex");
}

async function directorySHA256(dirPath) {
  const hash = createHash("sha256");
  const files = await directoryFiles(dirPath);
  for (const filePath of files) {
    const relative = path.relative(dirPath, filePath).replaceAll(path.sep, "/");
    hash.update(relative);
    hash.update("\0");
    hash.update(await readFile(filePath));
    hash.update("\0");
  }
  return hash.digest("hex");
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

async function writeArchive(metadata, packageManifest) {
  const baseName = archiveBaseName(metadata);
  const archiveName = `${baseName}.zip`;
  const archivePath = path.join(distRoot, archiveName);
  const checksumPath = `${archivePath}.sha256`;
  const metadataPath = `${archivePath}.json`;
  await rm(archivePath, { force: true });
  await rm(checksumPath, { force: true });
  await rm(metadataPath, { force: true });

  const zip = new AdmZip();
  const files = await directoryFiles(packageDir);
  for (const filePath of files) {
    const relative = path.relative(packageDir, filePath).replaceAll(path.sep, "/");
    zip.addFile(path.posix.join("cliff", relative), await readFile(filePath));
  }
  zip.writeZip(archivePath);

  const archiveSizeBytes = await fileSize(archivePath);
  const archiveSHA256 = await fileSHA256(archivePath);
  const archiveMetadata = {
    ...metadata,
    archive: archiveName,
    archiveSizeBytes,
    archiveSHA256,
    packageSizeBytes: packageManifest.packageSizeBytes,
    packageManifestSHA256: packageManifest.manifestSHA256,
    generatedAt: new Date().toISOString(),
  };
  await writeFile(checksumPath, `${archiveSHA256}  ${archiveName}\n`);
  await writeFile(metadataPath, `${JSON.stringify(archiveMetadata, null, 2)}\n`);
  return { archiveName, archivePath, archiveSizeBytes, archiveSHA256, metadataPath, checksumPath };
}

async function writeReleaseManifest(metadata, packageManifest, archive, installers) {
  const releaseManifest = {
    schemaVersion: 1,
    name: "cliff",
    version: metadata.version,
    commit: metadata.commit,
    builtAt: metadata.builtAt,
    generatedAt: new Date().toISOString(),
    platform: {
      os: process.platform,
      arch: process.arch,
      binary: binaryName,
    },
    package: {
      directory: "cliff",
      manifest: "cliff/package-manifest.json",
      sizeBytes: packageManifest.packageSizeBytes,
      manifestSHA256: packageManifest.manifestSHA256,
    },
    archive: {
      file: archive.archiveName,
      checksumFile: `${archive.archiveName}.sha256`,
      metadataFile: `${archive.archiveName}.json`,
      sizeBytes: archive.archiveSizeBytes,
      sha256: archive.archiveSHA256,
    },
    installers: {
      bootstrap: {
        windows: installers["install.ps1"],
        unix: installers["install.sh"],
      },
      package: {
        windows: installers["install-package.ps1"],
        unix: installers["install-package.sh"],
      },
    },
    commands: {
      install: {
        windows: "irm getcliff.dev/install.ps1 | iex",
        unix: "curl -fsSL getcliff.dev/install.sh | sh",
      },
      run: {
        windows: "powershell -ExecutionPolicy Bypass -File .\\run.ps1",
        unix: "sh ./run.sh",
      },
      status: {
        windows: "powershell -ExecutionPolicy Bypass -File .\\status.ps1",
        unix: "sh ./status.sh",
      },
      stop: {
        windows: "powershell -ExecutionPolicy Bypass -File .\\stop.ps1",
        unix: "sh ./stop.sh",
      },
    },
  };
  await writeFile(path.join(distRoot, "cliff-release.json"), `${JSON.stringify(releaseManifest, null, 2)}\n`);
  return releaseManifest;
}

async function writeReleaseInstallerScripts() {
  const installers = {};
  for (const fileName of releaseInstallerFiles) {
    const source = path.join(root, "scripts", fileName);
    const target = path.join(distRoot, fileName);
    await cp(source, target);
    if (fileName.endsWith(".sh")) await chmod(target, 0o755);
    installers[fileName] = {
      file: fileName,
      sizeBytes: await fileSize(target),
      sha256: await fileSHA256(target),
    };
  }
  return installers;
}

function manifestSHA256(manifest) {
  return createHash("sha256")
    .update(
      JSON.stringify({
        version: manifest.version,
        commit: manifest.commit,
        builtAt: manifest.builtAt,
        binary: manifest.binary,
        binarySizeBytes: manifest.binarySizeBytes,
        binarySHA256: manifest.binarySHA256,
        webSizeBytes: manifest.webSizeBytes,
        webSHA256: manifest.webSHA256,
        dataSizeBytes: manifest.dataSizeBytes,
        serverSizeBytes: manifest.serverSizeBytes,
        packageSizeBytes: manifest.packageSizeBytes,
      }),
    )
    .digest("hex");
}

async function writePackageManifest(metadata) {
  const webSizeBytes = await directorySize(path.join(packageDir, "web"));
  const binarySizeBytes = await fileSize(binaryPath);
  const dataSizeBytes = await directorySize(path.join(packageDir, "data"));
  const serverSizeBytes = await directorySize(path.join(packageDir, "servers"));
  const packageSizeBytes = await directorySize(packageDir, { exclude: new Set(["package-manifest.json"]) });
  const manifest = {
    ...metadata,
    binary: binaryName,
    binarySizeBytes,
    binarySHA256: await fileSHA256(binaryPath),
    webSizeBytes,
    webSHA256: await directorySHA256(path.join(packageDir, "web")),
    dataSizeBytes,
    serverSizeBytes,
    packageSizeBytes,
    generatedAt: new Date().toISOString(),
  };
  manifest.manifestSHA256 = manifestSHA256(manifest);
  await writeFile(path.join(packageDir, "package-manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  return manifest;
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

try {
  const metadata = buildMetadata();
  if (existsSync(webDir)) await cp(webDir, webBackup, { recursive: true });

  await rm(packageDir, { recursive: true, force: true });
  await mkdir(packageDir, { recursive: true });

  run("npm", ["run", "build:daemon-web"]);
  run("go", ["build", "-ldflags", ldflags(metadata), "-o", binaryPath, "./cmd/cliff"], daemonDir);
  await cp(webDir, path.join(packageDir, "web"), { recursive: true });
  await mkdir(path.join(packageDir, "data"), { recursive: true });
  await mkdir(path.join(packageDir, "servers"), { recursive: true });
  await writePackagedRunnerScripts();
  await writeFile(path.join(packageDir, "build.json"), `${JSON.stringify(metadata, null, 2)}\n`);
  await writeFile(
    path.join(packageDir, "README.txt"),
    [
      "Cliff daemon package",
      "",
      `Version: ${metadata.version}`,
      `Commit: ${metadata.commit}`,
      `Built: ${metadata.builtAt}`,
      "",
      "One-line release install:",
      "  Windows PowerShell: irm getcliff.dev/install.ps1 | iex",
      "  macOS/Linux:        curl -fsSL getcliff.dev/install.sh | sh",
      "",
      "Extracted package usage does not require Node.js, Go, or a Next.js server.",
      "",
      "Run from this folder:",
      `  ${runCommandText()}`,
      "",
      "Background runner scripts:",
      "  Windows PowerShell: powershell -ExecutionPolicy Bypass -File .\\run.ps1",
      "  macOS/Linux:        sh ./run.sh",
      "  Custom port:        powershell -ExecutionPolicy Bypass -File .\\run.ps1 -Port 8081",
      "  Custom port:        PORT=8081 sh ./run.sh",
      "  Status:             powershell -ExecutionPolicy Bypass -File .\\status.ps1",
      "  Status:             sh ./status.sh",
      "  Stop:               powershell -ExecutionPolicy Bypass -File .\\stop.ps1",
      "  Stop:               sh ./stop.sh",
      "",
      "Then open:",
      "  http://localhost:8080",
      "",
      "Same-network users can open:",
      "  http://<host-lan-ip>:8080",
      "",
      "Diagnostics:",
      "  data/cliff.json       Last known daemon PID, URLs, build metadata, and health payload",
      "  data/cliff.log        Daemon startup/runtime log output",
      "  data/cliff-error.log  Process launch errors when startup fails",
      "  status scripts              Print PID, local/LAN URLs, uptime, heap, and goroutine counts",
      "",
      "Folders:",
      "  data/     SQLite database, metadata cache, managed Java runtimes",
      "  servers/  Minecraft server folders",
      "  web/      static dashboard assets",
      "  run.ps1/status.ps1/stop.ps1   Windows package controls",
      "  run.sh/status.sh/stop.sh       macOS/Linux package controls",
      "",
      "Package metadata:",
      "  build.json              Build version, commit, and timestamp",
      "  package-manifest.json   Binary, static web, and initial package sizes",
      "  SHA-256 hashes          Verify binary and static web asset integrity",
      "",
    ].join("\n"),
  );
  const packageManifest = await writePackageManifest(metadata);
  const archive = await writeArchive(metadata, packageManifest);
  const installers = await writeReleaseInstallerScripts();
  await writeReleaseManifest(metadata, packageManifest, archive, installers);

  console.log(`Packaged Cliff daemon in ${path.relative(root, packageDir)} (${formatBytes(packageManifest.packageSizeBytes)})`);
  console.log(`Wrote archive ${path.relative(root, archive.archivePath)} (${formatBytes(archive.archiveSizeBytes)})`);
  console.log(`Wrote release manifest ${path.relative(root, path.join(distRoot, "cliff-release.json"))}`);
} finally {
  await rm(webDir, { recursive: true, force: true });
  if (existsSync(webBackup)) {
    await mkdir(path.dirname(webDir), { recursive: true });
    await cp(webBackup, webDir, { recursive: true });
  }
  await rm(tempRoot, { recursive: true, force: true });
}
