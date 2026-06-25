$ErrorActionPreference = "Stop"

if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
  throw "Node.js is required to stop Cliff with this script. Install Node.js 22 or newer, or stop the cliff process manually."
}

Push-Location (Resolve-Path (Join-Path $PSScriptRoot ".."))
try {
  node scripts/stop-daemon.mjs @args
}
finally {
  Pop-Location
}
