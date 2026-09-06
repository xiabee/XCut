# Build release binaries of xcut (and the host-platform Rust worker when present).
# Usage: powershell -File scripts/build-release.ps1 [[-Version] "0.1.0"] [-Platforms windows/amd64,linux/amd64]
param(
    [string]$Version = "",
    [string]$Platforms = "windows/amd64,linux/amd64,linux/arm64",
    [string]$OutDir = "dist"
)

$ErrorActionPreference = "Stop"

if (-not $Version) {
    $Version = (git describe --tags --always 2>$null)
    if (-not $Version) { $Version = "dev-$(Get-Date -Format 'yyyyMMdd-HHmm')" }
}

$commit = (git rev-parse --short HEAD 2>$null)
if (-not $commit) { $commit = "unknown" }
$date = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w -X github.com/xiabee/XCut/internal/version.Version=$Version -X github.com/xiabee/XCut/internal/version.Commit=$commit -X github.com/xiabee/XCut/internal/version.BuildDate=$date"

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

foreach ($plat in $Platforms.Split(",")) {
    $goos, $goarch = $plat.Split("/")
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    $ext = if ($goos -eq "windows") { ".exe" } else { "" }
    $out = Join-Path $OutDir "xcut-$Version-$goos-$goarch$ext"
    Write-Host "building $out"
    go build -trimpath -ldflags $ldflags -o $out ./cmd/xcut
    if ($LASTEXITCODE -ne 0) { throw "go build failed for $plat" }
}
Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

# Host-platform Rust worker, when the crate has been built.
$workerExe = "crates/xcut-worker-media/target/release/xcut-worker-media.exe"
if (-not (Test-Path $workerExe)) { $workerExe = "crates/xcut-worker-media/target/release/xcut-worker-media" }
if (Test-Path $workerExe) {
    Copy-Item $workerExe (Join-Path $OutDir "xcut-worker-media-$Version-$(if ($IsWindows -or $env:OS -like '*Windows*') { 'windows' } else { 'linux' })-amd64$(if ($IsWindows -or $env:OS -like '*Windows*') { '.exe' } else { '' })") -Force
    Write-Host "copied rust worker"
} else {
    Write-Host "rust worker not built (optional; cargo build --release -p xcut-worker-media)"
}

Get-ChildItem $OutDir | Format-Table Name, Length
