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
# -count=1: a gate that can answer from the test cache is not a gate — an
# environment change (ffmpeg removed, fixture regression) would be masked
# by cached PASSes instead of re-running the (possibly now-skipping) tests.
Invoke-Step "go test" { go test -count=1 ./... }

if ($Mode -eq "full") {
    # The race detector needs cgo + a C toolchain (absent on this Windows
    # setup). Skip loudly rather than silently; run scripts/race-docker.sh
    # (WSL/docker) or a CI dispatch for race coverage.
    $cgoOn = (go env CGO_ENABLED).Trim() -eq "1"
    $hasGcc = $null -ne (Get-Command gcc -ErrorAction SilentlyContinue)
    if ($cgoOn -and $hasGcc) {
        Invoke-Step "go test -race" { go test -count=1 -race ./... }
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
            # A healthy MSVC setup links fine (cargo check succeeds); decide
            # on the exit code alone — capturing output conflates "linked
            # quietly" with "failed" and flipped the fallback ON for working
            # MSVC installs.
            cargo check -q 2>$null | Out-Null
            $msvcBroken = ($LASTEXITCODE -ne 0)
            if ($msvcBroken -and (rustup toolchain list | Select-String "windows-gnu")) {
                $env:RUSTUP_TOOLCHAIN = "stable-x86_64-pc-windows-gnu"
                Write-Host "== rust: msvc linker unavailable, using windows-gnu toolchain"
            }
            try {
                Invoke-Step "cargo fmt --check" { cargo fmt --check }
                Invoke-Step "cargo clippy" { cargo clippy --all-targets -- -D warnings }
                Invoke-Step "cargo test" { cargo test }
            }
            finally {
                # The override must not leak into the caller's session.
                Remove-Item Env:RUSTUP_TOOLCHAIN -ErrorAction SilentlyContinue
            }
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

    # Static security analysis: HIGH severity + HIGH confidence findings fail
    # the gate. Suppressions live in the source as `#nosec GXXX -- reason`
    # (each with a written justification), never as blanket rule exclusions.
    if (Get-Command gosec -ErrorAction SilentlyContinue) {
        Invoke-Step "gosec" { gosec -severity high -confidence high -tests=false ./... }
    }
    else {
        Write-Host "== gosec: not installed (GOBIN=.tools/bin go install github.com/securego/gosec/v2/cmd/gosec@latest), skipped"
    }
}

Write-Host "== gate ($Mode): PASS"
