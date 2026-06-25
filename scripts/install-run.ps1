$ErrorActionPreference = "Stop"

if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
  throw "Node.js is required before installing Cliff. Install Node.js 22 or newer, then run this command again."
}

$NodeVersionText = (& node --version).Trim().TrimStart([char]"v")
$NodeMajor = [int]($NodeVersionText.Split(".")[0])
if ($NodeMajor -lt 22) {
  throw "Node.js 22 or newer is required before installing Cliff. Found Node.js $NodeVersionText."
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  throw "Go is required before installing Cliff daemon. Install Go 1.22 or newer, then run this command again."
}

$GoVersionText = (& go version)
$GoVersionMatch = [regex]::Match($GoVersionText, "go([0-9]+)\.([0-9]+)")
if (-not $GoVersionMatch.Success) {
  throw "Could not determine Go version from: $GoVersionText"
}
$GoMajor = [int]$GoVersionMatch.Groups[1].Value
$GoMinor = [int]$GoVersionMatch.Groups[2].Value
if ($GoMajor -lt 1 -or ($GoMajor -eq 1 -and $GoMinor -lt 22)) {
  throw "Go 1.22 or newer is required before installing Cliff daemon. Found $($GoVersionMatch.Value)."
}

Push-Location (Resolve-Path (Join-Path $PSScriptRoot ".."))
try {
  node scripts/install-run.mjs @args
}
finally {
  Pop-Location
}
