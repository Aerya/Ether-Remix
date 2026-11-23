# Build EtherSetup-<version>.exe on a Windows host/runner.
#   Local (unsigned):
#     pwsh packaging/windows/build-installer.ps1 -Version 1.2.0
#   CI (split so Azure Artifact Signing signs binaries before NSIS packages them):
#     pwsh packaging/windows/build-installer.ps1 -Version 1.2.0 -Phase Stage
#     # sign dist\windows\staging\ether.exe and ether-tray.exe
#     pwsh packaging/windows/build-installer.ps1 -Version 1.2.0 -Phase Package
#     # sign dist\EtherSetup-1.2.0.exe
# Requires: Go, Node 22+ with pnpm, NSIS (makensis on PATH).
param(
  [Parameter(Mandatory = $true)][string]$Version,
  [string]$Arch = "amd64",
  [ValidateSet("All", "Stage", "Package")][string]$Phase = "All"
)
$ErrorActionPreference = "Stop"
$Root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$Staging = Join-Path $Root "dist\windows\staging"

if ($Phase -in @("All", "Stage")) {
  if (Test-Path -LiteralPath $Staging) {
    Remove-Item -LiteralPath $Staging -Recurse -Force
  }
  New-Item -ItemType Directory -Force $Staging | Out-Null

  $ldflags = "-s -w" +
    " -X github.com/braedonsaunders/ether/internal/updater.Version=$Version" +
    " -X github.com/braedonsaunders/ether/internal/updater.Commit=$env:COMMIT" +
    " -X github.com/braedonsaunders/ether/internal/updater.PublicKeyB64=$env:ETHER_UPDATE_PUBLIC_KEY" +
    " -X github.com/braedonsaunders/ether/internal/tmdb.BundledKey=$env:ETHER_TMDB_BUNDLED_KEY"

  Write-Host "==> building web console (embedded by go:embed)"
  if (-not $env:NODE_OPTIONS) { $env:NODE_OPTIONS = "--max-old-space-size=4096" }
  Push-Location (Join-Path $Root "web")
  try {
    pnpm install --frozen-lockfile
    if ($LASTEXITCODE -ne 0) { throw "Web dependency install failed" }
    pnpm build
    if ($LASTEXITCODE -ne 0) { throw "Web console build failed" }
  } finally {
    Pop-Location
  }
  if (-not (Test-Path (Join-Path $Root "internal\admin\dist\index.html"))) {
    throw "Web console build did not produce internal\admin\dist\index.html"
  }

  Write-Host "==> building ether.exe + ether-tray.exe (windows/$Arch, CGO_ENABLED=0)"
  $env:CGO_ENABLED = "0"; $env:GOEXPERIMENT = "simd"; $env:GOOS = "windows"; $env:GOARCH = $Arch
  Push-Location $Root
  try {
    go build -trimpath -ldflags $ldflags -o "$Staging\ether.exe" ./cmd/ether
    if ($LASTEXITCODE -ne 0) { throw "Server build failed" }
    go build -trimpath -ldflags "$ldflags -H=windowsgui" -o "$Staging\ether-tray.exe" ./cmd/ether-tray
    if ($LASTEXITCODE -ne 0) { throw "Tray build failed" }

    Write-Host "==> staging ffmpeg"
    & bash "scripts/fetch-ffmpeg.sh" "windows-$Arch"
    if ($LASTEXITCODE -ne 0) { throw "FFmpeg staging failed" }
    Copy-Item "dist\ffmpeg\windows-$Arch\ffmpeg.exe" $Staging
    Copy-Item "dist\ffmpeg\windows-$Arch\ffprobe.exe" $Staging
    Copy-Item LICENSE, THIRD_PARTY_NOTICES.md $Staging
  } finally {
    Pop-Location
  }

  if ($Phase -eq "Stage") {
    Write-Host "==> staged: $Staging"
    return
  }
}

if ($Phase -eq "All") {
  Write-Host "==> local/dev build: leaving staged binaries and installer unsigned (CI signs with Azure Artifact Signing)"
}

foreach ($Required in @("ether.exe", "ether-tray.exe", "ffmpeg.exe", "ffprobe.exe", "LICENSE", "THIRD_PARTY_NOTICES.md")) {
  $Item = Join-Path $Staging $Required
  if (-not (Test-Path -LiteralPath $Item)) {
    throw "Missing staged file required for installer: $Item"
  }
}

New-Item -ItemType Directory -Force (Join-Path $Root "dist") | Out-Null
Write-Host "==> makensis"
$Out = Join-Path $Root "dist\EtherSetup-$Version.exe"
makensis "/DVERSION=$Version" "/DSTAGING=$Staging" "/DOUTFILE=$Out" `
  (Join-Path $PSScriptRoot "ether.nsi")
if ($LASTEXITCODE -ne 0) { throw "Installer build failed" }
Write-Host "==> done: $Out"
