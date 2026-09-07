# XCut local quality gate — CI-equivalent validation without GitHub Actions
# (DECISIONS.md D11). Usage:
#
#   powershell -File scripts/check.ps1 fast   # fmt + vet + build + go test
#   powershell -File scripts/check.ps1 full   # + race, cross-compile, Rust
#
# Exit code 0 = gate green. A local FFmpeg under .tools/ is used when present.
param(
    [ValidateSet("fast", "full")]
    [string]$Mode = "full"
)

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

# Prefer a repo-local FFmpeg (.tools/, gitignored) when PATH has none.
$hasFfmpeg = $null -ne (Get-Command ffmpeg -ErrorAction SilentlyContinue)
if (-not $hasFfmpeg) {
    foreach ($d in @(".tools/ffmpeg/bin")) {
        if (Test-Path (Join-Path $d "ffmpeg.exe")) {
            $env:PATH = (Resolve-Path $d).Path + ";" + $env:PATH
            $hasFfmpeg = $true
            break
        }
    }
}
if ($hasFfmpeg) {
    Write-Host "== ffmpeg: $((ffmpeg -version 2>$null | Select-Object -First 1))"
}
else {
    Write-Host "== ffmpeg: not on PATH (integration tests will skip)"
}

function Invoke-Step([string]$Name, [scriptblock]$Body) {
    Write-Host "== $Name"
    & $Body
    if ($LASTEXITCODE -ne 0) { throw "$Name failed with exit code $LASTEXITCODE" }
}

Invoke-Step "gofmt" {
    $unformatted = gofmt -l internal cmd
    if ($unformatted) { throw "unformatted files: $unformatted" }
}
Invoke-Step "go vet" { go vet ./... }
Invoke-Step "go build" { go build ./... }
Invoke-Step "go test" { go test ./... }

if ($Mode -eq "full") {
    Invoke-Step "go test -race" { go test -race ./... }

    Write-Host "== cross-compile checks (compile-verified only, not runtime-verified)"
    $env:GOOS = "linux"; $env:GOARCH = "amd64"
    go build -o NUL ./cmd/xcut
    if ($LASTEXITCODE -ne 0) { throw "cross-compile linux/amd64 failed" }
    $env:GOARCH = "arm64"
    go build -o NUL ./cmd/xcut
    if ($LASTEXITCODE -ne 0) { throw "cross-compile linux/arm64 failed" }
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

    if (Get-Command cargo -ErrorAction SilentlyContinue) {
        Push-Location crates/xcut-worker-media
        try {
            Invoke-Step "cargo fmt --check" { cargo fmt --check }
            Invoke-Step "cargo clippy" { cargo clippy --all-targets -- -D warnings }
            Invoke-Step "cargo test" { cargo test }
        }
        finally { Pop-Location }
    }
    else {
        Write-Host "== cargo: not on PATH, skipped"
    }

    if (Get-Command govulncheck -ErrorAction SilentlyContinue) {
        Invoke-Step "govulncheck" { govulncheck ./... }
    }
    else {
        Write-Host "== govulncheck: not installed (go install golang.org/x/vuln/cmd/govulncheck@latest), skipped"
    }
}

Write-Host "== gate ($Mode): PASS"
