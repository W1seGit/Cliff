param(
  [string]$Package = "",
  [string]$Manifest = "",
  [string]$InstallDir = "",
  [Alias("p")]
  [int]$Port = 8080,
  [switch]$Start,
  [switch]$Force,
  [switch]$SkipChecksum
)

$ErrorActionPreference = "Stop"
$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$ExpectedArchiveSha256 = ""
if (-not $InstallDir) {
  $InstallDir = Join-Path $env:LOCALAPPDATA "cliff"
}

function Resolve-PackagePath {
  param([string]$RequestedPackage)

  if ($RequestedPackage) {
    if ($RequestedPackage -match "^https?://") {
      $TempPackage = Join-Path ([System.IO.Path]::GetTempPath()) ("cliff-" + [System.Guid]::NewGuid().ToString("N") + ".zip")
      Invoke-WebRequest -Uri $RequestedPackage -OutFile $TempPackage
      try {
        Invoke-WebRequest -Uri "$RequestedPackage.sha256" -OutFile "$TempPackage.sha256"
      } catch {
        Write-Warning "No package checksum sidecar found at $RequestedPackage.sha256"
      }
      return $TempPackage
    }
    return (Resolve-Path $RequestedPackage).Path
  }

  $ReleaseManifestPath = Join-Path $Root "dist\cliff-release.json"
  if (Test-Path $ReleaseManifestPath) {
    $ReleaseManifest = Get-Content $ReleaseManifestPath -Raw | ConvertFrom-Json
    $ArchivePath = Join-Path (Split-Path -Parent $ReleaseManifestPath) $ReleaseManifest.archive.file
    if (Test-Path $ArchivePath) {
      return (Resolve-Path $ArchivePath).Path
    }
  }

  $Archive = Get-ChildItem -Path (Join-Path $Root "dist") -Filter "cliff-*.zip" -ErrorAction SilentlyContinue |
    Sort-Object LastWriteTime -Descending |
    Select-Object -First 1
  if (-not $Archive) {
    throw "No Cliff package archive was found. Run npm run daemon:package or pass -Package <zip-or-url>."
  }
  return $Archive.FullName
}

function Resolve-PackageFromManifest {
  param([string]$RequestedManifest)

  if (-not $RequestedManifest) {
    return ""
  }

  $ManifestPath = $RequestedManifest
  $ManifestBase = ""
  if ($RequestedManifest -match "^https?://") {
    $ManifestPath = Join-Path ([System.IO.Path]::GetTempPath()) ("cliff-release-" + [System.Guid]::NewGuid().ToString("N") + ".json")
    Invoke-WebRequest -Uri $RequestedManifest -OutFile $ManifestPath
    $ManifestBase = $RequestedManifest.Substring(0, $RequestedManifest.LastIndexOf("/") + 1)
  } else {
    $ManifestPath = (Resolve-Path $RequestedManifest).Path
    $ManifestBase = (Split-Path -Parent $ManifestPath)
  }

  $ReleaseManifest = Get-Content $ManifestPath -Raw | ConvertFrom-Json
  if (-not $ReleaseManifest.archive.file) {
    throw "Release manifest does not include archive.file."
  }
  if ($ReleaseManifest.archive.sha256) {
    $script:ExpectedArchiveSha256 = ([string]$ReleaseManifest.archive.sha256).Trim().ToLowerInvariant()
  }

  if ($RequestedManifest -match "^https?://") {
    $ArchiveUrl = $ManifestBase + $ReleaseManifest.archive.file
    return Resolve-PackagePath $ArchiveUrl
  }

  $ArchivePath = Join-Path $ManifestBase $ReleaseManifest.archive.file
  return (Resolve-Path $ArchivePath).Path
}

function Test-PackageChecksum {
  param([string]$ArchivePath)

  if ($SkipChecksum) {
    Write-Warning "Skipping package checksum verification."
    return
  }

  $ChecksumPath = "$ArchivePath.sha256"
  $ExpectedHash = ""
  if (-not (Test-Path $ChecksumPath)) {
    if ($script:ExpectedArchiveSha256) {
      $ExpectedHash = $script:ExpectedArchiveSha256
    } else {
      Write-Warning "No checksum file found at $ChecksumPath; package integrity was not verified."
      return
    }
  } else {
    $ChecksumText = Get-Content $ChecksumPath -Raw
    $ExpectedHash = ($ChecksumText -split "\s+")[0].Trim().ToLowerInvariant()
    if ($script:ExpectedArchiveSha256 -and $ExpectedHash -ne $script:ExpectedArchiveSha256) {
      throw "Package checksum sidecar does not match release manifest archive hash."
    }
  }

  if ($ExpectedHash -notmatch "^[a-f0-9]{64}$") {
    throw "Package checksum is invalid."
  }

  $Stream = [System.IO.File]::OpenRead($ArchivePath)
  try {
    $Sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
      $ActualHash = ([System.BitConverter]::ToString($Sha256.ComputeHash($Stream)) -replace "-", "").ToLowerInvariant()
    }
    finally {
      $Sha256.Dispose()
    }
  }
  finally {
    $Stream.Dispose()
  }
  if ($ActualHash -ne $ExpectedHash) {
    throw "Package checksum mismatch. Expected $ExpectedHash but got $ActualHash."
  }

  Write-Host "Verified package SHA-256: $ActualHash"
}

function Get-LanUrls {
  param([int]$DashboardPort)

  return [System.Net.NetworkInformation.NetworkInterface]::GetAllNetworkInterfaces() |
    ForEach-Object { $_.GetIPProperties().UnicastAddresses } |
    Where-Object { $_.Address.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetwork -and -not [System.Net.IPAddress]::IsLoopback($_.Address) } |
    ForEach-Object { "http://$($_.Address):$DashboardPort" }
}

function Assert-ExtractedPackage {
  param([string]$PackagePath)

  $RequiredPaths = @(
    "cliff.exe",
    "web\index.html",
    "package-manifest.json",
    "run.ps1",
    "status.ps1",
    "stop.ps1"
  )

  foreach ($RelativePath in $RequiredPaths) {
    $FullPath = Join-Path $PackagePath $RelativePath
    if (-not (Test-Path $FullPath)) {
      throw "Package archive is missing required file: cliff\$RelativePath"
    }
  }
}

if ($Manifest) {
  $PackagePath = Resolve-PackageFromManifest $Manifest
} else {
  $PackagePath = Resolve-PackagePath $Package
}
$ExtractRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("cliff-install-" + [System.Guid]::NewGuid().ToString("N"))
$ExtractedPackage = Join-Path $ExtractRoot "cliff"

try {
  Test-PackageChecksum $PackagePath
  New-Item -ItemType Directory -Force -Path $ExtractRoot | Out-Null
  Expand-Archive -Path $PackagePath -DestinationPath $ExtractRoot -Force
  if (-not (Test-Path $ExtractedPackage)) {
    throw "Package archive did not contain a cliff folder."
  }
  Assert-ExtractedPackage $ExtractedPackage

  if (Test-Path (Join-Path $InstallDir "stop.ps1")) {
    powershell -ExecutionPolicy Bypass -File (Join-Path $InstallDir "stop.ps1") -Force | Out-Null
  }

  if ((Test-Path $InstallDir) -and -not $Force) {
    throw "Install directory already exists: $InstallDir. Re-run with -Force to replace it."
  }

  if (Test-Path $InstallDir) {
    Remove-Item -LiteralPath $InstallDir -Recurse -Force
  }

  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $InstallDir) | Out-Null
  Move-Item -LiteralPath $ExtractedPackage -Destination $InstallDir

  Write-Host "Cliff installed."
  Write-Host "Path: $InstallDir"
  Write-Host "Local: http://localhost:$Port"
  $LanUrls = @(Get-LanUrls $Port)
  foreach ($Url in $LanUrls) {
    Write-Host "Same network: $Url"
  }
  if (-not $LanUrls) {
    Write-Host "Same network: no LAN IPv4 address detected"
  }
  Write-Host "Run: powershell -ExecutionPolicy Bypass -File `"$InstallDir\run.ps1`" -Port $Port"
  Write-Host "Status: powershell -ExecutionPolicy Bypass -File `"$InstallDir\status.ps1`""
  Write-Host "Stop: powershell -ExecutionPolicy Bypass -File `"$InstallDir\stop.ps1`""

  if ($Start) {
    powershell -ExecutionPolicy Bypass -File (Join-Path $InstallDir "run.ps1") -Port $Port
  }
}
finally {
  Remove-Item -LiteralPath $ExtractRoot -Recurse -Force -ErrorAction SilentlyContinue
}
