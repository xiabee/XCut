# Local CI gate — delegates to this repo's own quality gate
# (scripts/check.ps1), which sets up the local FFmpeg environment the
# render tests require. Exit code is authoritative.
#Requires -Version 5
$ErrorActionPreference = "Stop"
Set-Location -LiteralPath (Join-Path $PSScriptRoot "..")

powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "check.ps1") fast
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "LOCAL CI PASS"
exit 0
