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
# Repo-local go tools (govulncheck etc.) installed via GOBIN=.tools/bin.
if (Test-Path ".tools/bin") {
    $env:PATH = (Resolve-Path ".tools/bin").Path + ";" + $env:PATH
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
    # The race detector needs cgo + a C toolchain (absent on this Windows
    # setup). Skip loudly rather than silently; run scripts/race-docker.sh
    # (WSL/docker) or a CI dispatch for race coverage.
    $cgoOn = (go env CGO_ENABLED).Trim() -eq "1"
    $hasGcc = $null -ne (Get-Command gcc -ErrorAction SilentlyContinue)
    if ($cgoOn -and $hasGcc) {
        Invoke-Step "go test -race" { go test -race ./... }
    }
    else {
        Write-Host "== go test -race: SKIPPED (no cgo/C toolchain; use scripts/race-docker.sh or CI dispatch)"
    }

    Write-Host "== cross-compile checks (compile-verified only, not runtime-verified)"
    $env:GOOS = "linux"; $env:GOARCH = "amd64"
    go build -o NUL ./cmd/xcut
    if ($LASTEXITCODE -ne 0) { throw "cross-compile linux/amd64 failed" }
    $env:GOARCH = "arm64"
    go build -o NUL ./cmd/xcut
    if ($LASTEXITCODE -ne 0) { throw "cross-compile linux/arm64 failed" }
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

    if (Get-Command cargo -ErrorAction SilentlyContinue) {
        # Windows hosts without MSVC Build Tools cannot link under the default
        # msvc toolchain; fall back to an installed windows-gnu toolchain.
        Push-Location crates/xcut-worker-media
        try {
            $probe = cargo check -q 2>$null; if ($LASTEXITCODE -ne 0) { $probe = $null }
            if (-not $probe -and (rustup toolchain list | Select-String "windows-gnu")) {
                $env:RUSTUP_TOOLCHAIN = "stable-x86_64-pc-windows-gnu"
                Write-Host "== rust: msvc linker unavailable, using windows-gnu toolchain"
            }
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
