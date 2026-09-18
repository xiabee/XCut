# Build the true Windows installer (setup exe) with Inno Setup.
# Owner directive 2026-09-18: ship an installer artifact, not just a zip.
#
# Usage:
#   scripts/build-release.ps1 first, then:
#   powershell -File scripts/make-setup.ps1 [-Version v0.1.7-alpha]
#
# Prereqs:
#   - Inno Setup 6 (winget install JRSoftware.InnoSetup) — ISCC.exe found
#     on PATH or under %LOCALAPPDATA%\Programs\Inno Setup 6.
#   - dist/xcut-<version>-windows-amd64.exe from build-release.ps1.
# The brand .ico is generated from scripts/genicon (programmatic, no
# third-party binary resources).
#Requires -Version 5
param(
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
Set-Location -LiteralPath (Join-Path $PSScriptRoot "..")

if (-not $Version) {
    $Version = (git describe --tags --always 2>$null)
    if (-not $Version) { $Version = "dev-$(Get-Date -Format 'yyyyMMdd-HHmm')" }
}

$exe = Join-Path "dist" "xcut-$Version-windows-amd64.exe"
if (-not (Test-Path $exe)) { throw "missing $exe — run scripts/build-release.ps1 first" }

# Brand icon for the setup exe (generated, same source as the exe resource).
$ico = Join-Path "dist" "xcut.ico"
go run scripts/genicon/main.go -out $ico
if ($LASTEXITCODE -ne 0) { throw "genicon failed" }

$isccPath = ""
$isccCmd = Get-Command ISCC.exe -ErrorAction SilentlyContinue
if ($isccCmd) { $isccPath = $isccCmd.Source }
if (-not $isccPath) {
    $candidate = Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"
    if (Test-Path $candidate) { $isccPath = $candidate }
}
if (-not $isccPath) {
    throw "Inno Setup 6 not found — install with: winget install JRSoftware.InnoSetup"
}

& $isccPath "/DAppVersion=$Version" (Join-Path "installer" "xcut.iss")
if ($LASTEXITCODE -ne 0) { throw "ISCC failed with $LASTEXITCODE" }

$out = Join-Path "dist" "xcut-$Version-windows-setup.exe"
if (-not (Test-Path $out)) { throw "expected output $out not produced" }
Write-Host "== setup built: $out"
