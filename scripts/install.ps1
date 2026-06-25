param(
  [string]$Manifest = "",
  [string]$Package = "",
  [string]$InstallDir = "",
  [Alias("p")]
  [int]$Port = 8080,
  [switch]$NoStart,
  [switch]$Force,
  [switch]$SkipChecksum
)

$ErrorActionPreference = "Stop"
$DefaultManifest = "https://github.com/W1seGit/Cliff/releases/latest/download/cliff-release.json"
$InstallerSource = "https://github.com/W1seGit/Cliff/releases/latest/download/install-package.ps1"

if (-not $Manifest) {
  $Manifest = if ($env:CLIFF_RELEASE_MANIFEST) { $env:CLIFF_RELEASE_MANIFEST } else { $DefaultManifest }
}

if ($env:CLIFF_INSTALL_PACKAGE_PS1) {
  $InstallerSource = $env:CLIFF_INSTALL_PACKAGE_PS1
}

$LocalInstaller = ""
if ($PSScriptRoot) {
  $Candidate = Join-Path $PSScriptRoot "install-package.ps1"
  if (Test-Path $Candidate) {
    $LocalInstaller = $Candidate
  }
}

$TempInstaller = ""
try {
  if ($LocalInstaller) {
    $InstallerPath = $LocalInstaller
  } else {
    $TempInstaller = Join-Path ([System.IO.Path]::GetTempPath()) ("cliff-install-package-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")
    Invoke-WebRequest -Uri $InstallerSource -OutFile $TempInstaller
    $InstallerPath = $TempInstaller
  }

  $InstallerArgs = @("-ExecutionPolicy", "Bypass", "-File", $InstallerPath, "-Port", "$Port")
  if ($Package) {
    $InstallerArgs += @("-Package", $Package)
  } else {
    $InstallerArgs += @("-Manifest", $Manifest)
  }
  if ($InstallDir) { $InstallerArgs += @("-InstallDir", $InstallDir) }
  if (-not $NoStart) { $InstallerArgs += "-Start" }
  if ($Force) { $InstallerArgs += "-Force" }
  if ($SkipChecksum) { $InstallerArgs += "-SkipChecksum" }

  powershell @InstallerArgs
}
finally {
  if ($TempInstaller) {
    Remove-Item -LiteralPath $TempInstaller -Force -ErrorAction SilentlyContinue
  }
}
