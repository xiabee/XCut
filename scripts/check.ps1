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
# Secret scanning: gitleaks over the full git history AND the working
# tree (untracked files included — the gate runs before a commit, so a
# secret sitting in a fresh edit must be caught here). Resolved from PATH
# or the repo-local .tools/bin copy (which ships to CI snapshots); when
# neither exists the step skips LOUDLY rather than silently passing.
$gitleaks = $null
$gkCmd = Get-Command gitleaks -ErrorAction SilentlyContinue
if ($gkCmd) { $gitleaks = $gkCmd.Source }
elseif (Test-Path ".tools/bin/gitleaks.exe") { $gitleaks = (Resolve-Path ".tools/bin/gitleaks.exe").Path }
# Steps that did not run are named in the verdict line: a PASS that quietly
# skipped the Rust suite, the vulnerability scan or the integration tests is the
# same failure shape as a secret scan that reported "clean" over nothing.
$NotRun = @()
if ($hasFfmpeg) {
    Write-Host "== ffmpeg: $((ffmpeg -version 2>$null | Select-Object -First 1))"
}
else {
    Write-Host "== ffmpeg: not on PATH (integration tests will skip)"
    $NotRun += "integration-tests(no-ffmpeg)"
}

function Invoke-Step([string]$Name, [scriptblock]$Body) {
    Write-Host "== $Name"
    & $Body
    if ($LASTEXITCODE -ne 0) { throw "$Name failed with exit code $LASTEXITCODE" }
}

if ($gitleaks) {
    # The .gitleaks.toml allowlist excuses .gotmp/ (scratch: browser profiles,
    # lavfi media). Without it the working-tree scan reads 304 MB instead of
    # 13 MB and reports 157 shape-matches — so the exemption is load-bearing.
    # It is only legitimate while nothing under .gotmp is *tracked*: gitleaks
    # applies the same config to history, so a force-added file there would be
    # excused from the commit scan too. Refuse that shape before scanning.
    if (Test-Path ".git") {
        $trackedScratch = @(& git ls-files -- '.gotmp/*' 2>$null)
        if ($LASTEXITCODE -eq 0 -and $trackedScratch.Count -gt 0) {
            throw (".gotmp/ is allowlisted for secret scanning, so it must never hold tracked files: " +
                ($trackedScratch -join ", "))
        }
    }
    # Scope is reported with a file count rather than a bare "clean": a scan
    # that silently covered nothing looks identical to one that found nothing,
    # and that is exactly the failure mode found in a sibling project's gate
    # (an empty file list still printed "secret scan clean (0 tracked files)").
    # The count is the tree handed to the scanner, not the bytes it reads:
    # gitleaks does NOT honour .gitignore (measured — without the allowlist it
    # read 304 MB of .gotmp), it skips binaries and the configured paths. So
    # this number is an upper bound, and its only job is to prove the scope is
    # not empty: a zero scope is refused rather than called clean.
    $scanPaths = (Get-ChildItem -Recurse -File -Force . -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -notmatch '\\\.git\\' -and $_.FullName -notmatch '\\\.tools\\' } |
        Measure-Object).Count
    if ($scanPaths -eq 0) {
        throw "gitleaks: scan scope is empty — refusing to call that clean"
    }
    if (Test-Path ".git") {
        Invoke-Step "gitleaks (git history)" { & $gitleaks git . --no-banner --redact }
    }
    # The working-tree scan always runs: a secret sitting in an
    # uncommitted edit is invisible to a git scan and is exactly what a
    # pre-commit gate must catch. CI snapshots carry no .git dir, so on
    # nodes this is the only scan — still a real gate.
    # The JSON report goes to stdout so a failing scan names the offending file in
# the gate log; a clean run prints nothing extra. Without it the gate says
# "leaks found: 1" and leaves everyone to hunt.
Invoke-Step "gitleaks (working tree, $scanPaths paths under .)" {
    & $gitleaks dir . --no-banner --redact --report-format json --report-path -
}
} else {
    # A security step that cannot run is a failed gate, not a warning: the run
    # would otherwise continue to "== gate: PASS" with nothing scanned.
    throw "gitleaks not found on PATH or .tools/bin — refusing to claim the tree is secret-clean. Install it: https://github.com/zricethezav/gitleaks"
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
#
# -json is for the skip accounting below. Several suites here are conditional
# (integration tests need ffmpeg, the cross-language sidecar tests need python,
# the Rust protocol tests need the worker binary), and a package-level "ok"
# cannot tell "ran and passed" from "never ran". A green gate therefore names
# every skipped test — on a host without python the sidecar contract has not
# been exercised, and that must be visible in the verdict. The skip event
# carries no reason (it streams in the preceding output events), so the name is
# the lookupable unit: worker/TestDescribe, not "7 skipped".
Write-Host "== go test"
$ErrorActionPreference = "Continue"
$testErrFile = Join-Path ([System.IO.Path]::GetTempPath()) "xcut-gate-test.err"
$testLines = @(go test -count=1 -json ./... 2>$testErrFile | ForEach-Object { "$_" })
$testExit = $LASTEXITCODE
$ErrorActionPreference = "Stop"
$TestPass = 0
$TestSkips = @()
foreach ($line in $testLines) {
    if (-not $line.StartsWith("{")) { continue }
    $ev = $null
    try { $ev = $line | ConvertFrom-Json } catch { continue }
    if (-not $ev -or -not $ev.Test) { continue }
    $pkg = ($ev.Package -split "/")[-1]
    if ($ev.Action -eq "pass") { $TestPass = $TestPass + 1 }
    if ($ev.Action -eq "skip") {
        $TestSkips = $TestSkips + "$($pkg)/$($ev.Test)"
    }
    if ($ev.Action -eq "fail" -and $ev.Output) { Write-Host $ev.Output.TrimEnd() }
}
if ($testExit -ne 0) {
    Write-Host "go test stderr tail:"
    Get-Content $testErrFile -Tail 20 -ErrorAction SilentlyContinue
    throw "go test failed with exit code $testExit"
}
Remove-Item $testErrFile -ErrorAction SilentlyContinue
Write-Host "== go test: $TestPass passed, $($TestSkips.Count) skipped"
foreach ($s in ($TestSkips | Select-Object -First 20)) { Write-Host "   skip $s" }
if ($TestSkips.Count -gt 20) { Write-Host "   ... and $($TestSkips.Count - 20) more" }

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
        $NotRun += "rust"
    }

    if (Get-Command govulncheck -ErrorAction SilentlyContinue) {
        Invoke-Step "govulncheck" { govulncheck ./... }
    }
    else {
        Write-Host "== govulncheck: not installed (go install golang.org/x/vuln/cmd/govulncheck@latest), skipped"
        $NotRun += "govulncheck"
    }

    # Static security analysis: HIGH severity + HIGH confidence findings fail
    # the gate. Suppressions live in the source as `#nosec GXXX -- reason`
    # (each with a written justification), never as blanket rule exclusions.
    if (Get-Command gosec -ErrorAction SilentlyContinue) {
        Invoke-Step "gosec" { gosec -severity high -confidence high -tests=false ./... }
    }
    else {
        Write-Host "== gosec: not installed (GOBIN=.tools/bin go install github.com/securego/gosec/v2/cmd/gosec@latest), skipped"
        $NotRun += "gosec"
    }
}

$skipNote = ""
if ($TestSkips.Count -gt 0) { $skipNote = "; tests skipped: $($TestSkips.Count) (see '== go test' above)" }
Write-Host "== gate ($Mode): PASS (steps not run: $(if ($NotRun) { $NotRun -join ", " } else { "none" })$skipNote)"
