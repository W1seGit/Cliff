$ErrorActionPreference = "Stop"

if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
  throw "Node.js is required to check Cliff status. Install Node.js 22 or newer."
}

Push-Location (Resolve-Path (Join-Path $PSScriptRoot ".."))
try {
  node scripts/status-daemon.mjs @args
}
finally {
  Pop-Location
}
