# Build release binaries of xcut (and the host-platform Rust worker when present).
# Usage: powershell -File scripts/build-release.ps1 [[-Version] "0.1.0"] [-Platforms windows/amd64,linux/amd64]
param(
    [string]$Version = "",
    [string]$Platforms = "windows/amd64,linux/amd64,linux/arm64",
    [string]$OutDir = "dist"
)

$ErrorActionPreference = "Stop"

# Resolve relative output path up front: later steps change the CWD.
$OutDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutDir)

if (-not $Version) {
    $Version = (git describe --tags --always 2>$null)
    if (-not $Version) { $Version = "dev-$(Get-Date -Format 'yyyyMMdd-HHmm')" }
}

$commit = (git rev-parse --short HEAD 2>$null)
if (-not $commit) { $commit = "unknown" }
$date = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w -X github.com/xiabee/XCut/internal/version.Version=$Version -X github.com/xiabee/XCut/internal/version.Commit=$commit -X github.com/xiabee/XCut/internal/version.BuildDate=$date"

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$hostGOOS = (go env GOOS)
$hostGOARCH = (go env GOARCH)
$hostOut = ""
foreach ($plat in $Platforms.Split(",")) {
    $goos, $goarch = $plat.Split("/")
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    $ext = if ($goos -eq "windows") { ".exe" } else { "" }
    $out = Join-Path $OutDir "xcut-$Version-$goos-$goarch$ext"
    Write-Host "building $out"
    go build -trimpath -ldflags $ldflags -o $out ./cmd/xcut
    if ($LASTEXITCODE -ne 0) { throw "go build failed for $plat" }
    if ($goos -eq $hostGOOS -and $goarch -eq $hostGOARCH) {
        # The one artifact this machine can actually run, and therefore the one it
        # has to run before shipping. build-release.sh smokes the same file through
        # the same script, so the check list exists in exactly one place.
        $hostOut = $out
    }
}
Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

if (-not $hostOut) {
    throw "no artifact matches the build host ($hostGOOS/$hostGOARCH) — nothing would be smoke-tested"
}
# Git for Windows' bash is what runs the gate's sh twin on the nodes; if it is
# genuinely absent, refusing the release is the honest answer, because shipping a
# binary nothing executed is the failure this step guards.
. (Join-Path $PSScriptRoot "posh-shells.ps1")
# Ask, do not assume: on a node whose PATH carries the WSL launcher, `bash` exists and
# refuses to run (WSL_E_LOCAL_SYSTEM_NOT_SUPPORTED, exit 1). Trusting the name would make
# the smoke step look like the artifact failed; see posh-shells.ps1 for the probe.
$bash = Resolve-PortableShell
if (-not $bash) {
    throw "no working bash (install Git for Windows) — refusing to ship an artifact nothing executed"
}
& $bash scripts/smoke-release.sh $hostOut $Version $commit
if ($LASTEXITCODE -ne 0) { throw "release smoke failed (exit $LASTEXITCODE)" }

# Rust workers: static linux (musl via bundled rust-lld) + windows host.
$workerDir = "crates/xcut-worker-media"
if (Get-Command cargo -ErrorAction SilentlyContinue) {
    Push-Location $workerDir
    # PS 5.1 decorates native stderr as errors; route through cmd instead.
    cmd /c "cargo build --release --target x86_64-unknown-linux-musl 2>nul" | Out-Null
    if ($LASTEXITCODE -eq 0) {
        Copy-Item "target/x86_64-unknown-linux-musl/release/xcut-worker-media" `
            (Join-Path $OutDir "xcut-worker-media-$Version-linux-amd64") -Force
        Write-Host "built rust worker (linux, static musl)"
    } else {
        Write-Host "rust worker linux build skipped (musl target not installed)"
    }
    Pop-Location
    $hostWorker = "crates/xcut-worker-media/target/release/xcut-worker-media.exe"
    if (-not (Test-Path $hostWorker)) { $hostWorker = "crates/xcut-worker-media/target/release/xcut-worker-media" }
    if (Test-Path $hostWorker) {
        Copy-Item $hostWorker (Join-Path $OutDir "xcut-worker-media-$Version-windows-amd64.exe") -Force
        Write-Host "copied host rust worker"
    }
} else {
    Write-Host "cargo not found — rust worker binaries skipped (optional component)"
}

Get-ChildItem $OutDir | Format-Table Name, Length
