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
. (Join-Path $PSScriptRoot "posh-shells.ps1")

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
# Twin of check.sh's "== sh -n". The release path is shell (build-release.sh,
# smoke-release.sh, fetch-stock-ffmpeg.sh, verify-arm64.sh, this gate's own twin), and
# gofmt/vet/build cannot see a typo in any of it. Deliberately at script scope, like the
# race block below: `$NotRun +=` inside an Invoke-Step scriptblock would only append to a
# local copy and the verdict line would keep saying "steps not run: none".
Write-Host "== sh -n"
$sh = Get-Command sh -ErrorAction SilentlyContinue
if ($sh) {
    $shellFiles = @(Get-ChildItem -Path "scripts" -Filter "*.sh" -File)
    if ($shellFiles.Count -lt 5) {
        throw "only $($shellFiles.Count) shell scripts found under scripts/ — this step is not reading what it claims"
    }
    $shellBad = @()
    # Parse each script with the interpreter its own shebang names — the twin of
    # check.sh's shebang rule. Half of scripts/*.sh declare bash; a host whose sh
    # is a real POSIX shell (dash) fails a bash script on syntax only bash owns,
    # and both compat nodes died in exactly that step (night 2026-10-01). Here sh
    # is usually Git Bash's (bash in POSIX mode), so the selection is about
    # symmetry with the sh gate and about a host that ever puts a real dash
    # first on PATH.
    $bash = Get-Command bash -ErrorAction SilentlyContinue
    # A real syntax error is reported on stderr, and Stop promotes native stderr to a
    # terminating error: downgrade for the probe so the step names the offender instead
    # of dying on the first line sh ever wrote.
    $ErrorActionPreference = "Continue"
    foreach ($f in $shellFiles) {
        $shebang = ""
        try { $shebang = (Get-Content $f.FullName -TotalCount 1 -ErrorAction Stop) } catch { $shebang = "" }
        $probe = $sh.Source
        if ($shebang -match "bash" -and $bash) {
            $probe = $bash.Source
        }
        elseif ($shebang -match "bash" -and -not $bash) {
            $NotRun += "sh-n(no-bash)"
            continue
        }
        & $probe -n $f.FullName 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) { $shellBad += $f.Name }
    }
    $ErrorActionPreference = "Stop"
    if ($shellBad.Count -gt 0) { throw "shell syntax errors in: $($shellBad -join ' ')" }
    Write-Host "   $($shellFiles.Count) scripts parse"
} else {
    Write-Host "   no sh on PATH — the step did not run"
    $NotRun += "sh-n"
}

# The twin of check.sh's idle step, and deliberately in the unconditional region: the two
# Windows legs run `fast`, so a full-only step here would never execute on them.
Write-Host "== idle check (rule 6: a quiet server costs nothing)"
$idleBash = Resolve-PortableShell
if ($idleBash) {
    $idleDir = Join-Path ([System.IO.Path]::GetTempPath()) ("xcut-idle-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
    New-Item -ItemType Directory -Force -Path $idleDir | Out-Null
    $idleBin = Join-Path $idleDir "xcut-idlecheck.exe"
    go build -o $idleBin ./cmd/xcut
    if ($LASTEXITCODE -ne 0) { throw "idle check: go build failed" }
    & $idleBash scripts/idle-check.sh $idleBin 5
    $idleRc = $LASTEXITCODE
    Remove-Item -Recurse -Force $idleDir -ErrorAction SilentlyContinue
    if ($idleRc -eq 77) {
        Write-Host "   the step did not run here (no /proc or no curl)"
        $NotRun += "idle-check(no-proc)"
    }
    elseif ($idleRc -ne 0) { throw "idle check failed with exit $idleRc" }
} else {
    Write-Host "   no shell that would run a command — the step did not run"
    $NotRun += "idle-check(no-bash)"
}
# PowerShell twin of the step above, for the files `sh -n` cannot read: the gate, this
# script's own callers, the packaging and installer paths. A parse error in one of them is
# invisible to every other step (the file is only ever run by `powershell -File`), and the
# step parses itself, so a broken check.ps1 names itself.
Write-Host "== ps parse"
$psFiles = @(Get-ChildItem -Path "scripts" -Filter "*.ps1" -File) + @(Get-ChildItem -Path "." -Filter "ci-local.ps1" -File -ErrorAction SilentlyContinue)
if ($psFiles.Count -lt 5) {
    throw "only $($psFiles.Count) PowerShell scripts found — this step is not reading what it claims"
}
$psBad = @()
foreach ($f in $psFiles) {
    $parseErrors = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$null, [ref]$parseErrors)
    if ($parseErrors.Count -gt 0) {
        $psBad += "$($f.Name):$($parseErrors[0].Extent.StartLineNumber) $($parseErrors[0].Message)"
    }
}
if ($psBad.Count -gt 0) { throw "PowerShell syntax errors:`n  $($psBad -join "`n  ")" }
Write-Host "   $($psFiles.Count) scripts parse"

# The web UI is embedded Go-free JavaScript that no Go toolchain parses: a missing
# brace in app.js compiles, vets, tests and ships, and the panel just stops updating.
# Ask node, and record the absence rather than passing silently — a host without node
# has not checked anything, and a step that cannot fail is not a guard.
Write-Host "== js parse"
$jsFiles = @(Get-ChildItem -Path "internal/api/static" -Filter "*.js" -File)
if ($jsFiles.Count -lt 2) {
    throw "only $($jsFiles.Count) UI scripts found — this step is not reading what it claims"
}
$node = $null
try { $node = (& node --version 2>$null) } catch { $node = $null }
if (-not $node) {
    Write-Warning "node is not runnable on this host — the js-parse step did not run"
    $NotRun += " js-parse(no-node)"
} else {
    $jsBad = @()
    foreach ($f in $jsFiles) {
        $check = & node --check $f.FullName 2>&1
        if ($LASTEXITCODE -ne 0) {
            $first = ($check | Select-Object -First 1)
            $jsBad += "$($f.Name): $first"
        }
    }
    if ($jsBad.Count -gt 0) { throw "JavaScript syntax errors:`n  $($jsBad -join "`n  ")" }
    Write-Host "   $($jsFiles.Count) UI scripts parse ($($node.Trim()))"
}

Invoke-Step "go vet" { go vet ./... }
Invoke-Step "go build" { go build ./... }
# The Rust toolchain decision, asked once and reused: a healthy MSVC setup links
# fine (`cargo check` exits 0), so decide on the exit code alone — capturing output
# conflates "linked quietly" with "failed" and flipped the fallback ON for working
# MSVC installs. Call it from inside the crate directory.
function Resolve-RustToolchain {
    cargo check -q 2>$null | Out-Null
    if (($LASTEXITCODE -ne 0) -and (rustup toolchain list | Select-String "windows-gnu")) {
        $env:RUSTUP_TOOLCHAIN = "stable-x86_64-pc-windows-gnu"
        Write-Host "== rust: msvc linker unavailable, using windows-gnu toolchain"
    }
}

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
# The Go side of the worker protocol skips when the binary is absent, and none of
# `cargo check`, `cargo clippy --all-targets` or `cargo test` leaves an executable at
# target/{debug,release}/xcut-worker-media — which is where internal/worker's tests
# look. So build it *before* the tests: otherwise the thorough leg is the leg that
# never spoke to the worker. check.sh has the same step; the two are read together.
if ($Mode -eq "full" -and (Get-Command cargo -ErrorAction SilentlyContinue)) {
    Write-Host "== rust worker build"
    Push-Location crates/xcut-worker-media
    try {
        Resolve-RustToolchain
        cargo build -q
        if ($LASTEXITCODE -ne 0) { throw "cargo build failed (exit $LASTEXITCODE)" }
    }
    finally { Pop-Location }
    if (Get-ChildItem "crates/xcut-worker-media/target/debug/xcut-worker-media*" -ErrorAction SilentlyContinue) {
        Write-Host "   worker binary present — internal/worker's protocol tests will run"
    }
    else {
        Write-Host "   worker binary still absent — internal/worker will skip"
    }
}

Write-Host "== go test"
$ErrorActionPreference = "Continue"
# Named for this process, because a fixed name in a shared directory is a collision
# waiting for the next gate on the box: at 03:20 another project's local gate opened
# `%TEMP%\xcut-gate-test.err` while this one was starting, PowerShell's redirect raised
# an IOException, `go test` never ran, and the step that must be the gate's whole point
# reported "0 passed, 0 skipped" under a PASS line.
$testErrFile = Join-Path ([System.IO.Path]::GetTempPath()) "xcut-gate-test-$PID.err"
$testLines = @(go test -count=1 -json ./... 2>$testErrFile | ForEach-Object { "$_" })
$testExit = $LASTEXITCODE
$ErrorActionPreference = "Stop"
$TestPass = 0
$TestSkips = @()
# A failing test's text arrives as `output` events; the `fail` event that closes
# it carries none. The lines are buffered per test and replayed only for the
# tests that failed — without this the gate said "go test failed with exit code
# 1" and printed no reason at all, which is the exact silence this step exists
# to remove (measured: an internal/api assertion failure reached the console as
# zero lines).
$Outputs = @{}
$FailedKeys = @()
# go test -json prints its own tool-level words (build-cache trouble, an image
# lock it cannot clean up, a killed toolchain) as NON-JSON stdout lines; the
# parser below would skip them silently, and a red whose only words nobody
# printed is the exact silence this step exists to remove. Observed live
# 2026-10-03 02:00: exit 1, zero fail events, zero build events, empty stderr —
# the reason rode a non-JSON line and was never shown.
$nonJsonLines = @()
foreach ($line in $testLines) {
    if (-not $line.StartsWith("{")) { $nonJsonLines = $nonJsonLines + $line; continue }
    $ev = $null
    try { $ev = $line | ConvertFrom-Json } catch { continue }
    if (-not $ev -or -not $ev.Action) { continue }
    $pkg = ($ev.Package -split "/")[-1]
    $key = "$($ev.Package)|$($ev.Test)"
    if ($ev.Action -eq "pass" -and $ev.Test) { $TestPass = $TestPass + 1 }
    if ($ev.Action -eq "skip" -and $ev.Test) {
        $TestSkips = $TestSkips + "$($pkg)/$($ev.Test)"
    }
    if ($ev.Action -eq "fail" -and $ev.Test) { $FailedKeys = $FailedKeys + $key }
    if ($ev.Action -eq "output" -and $ev.Test -and $ev.Output) {
        if (-not $Outputs[$key]) { $Outputs[$key] = @() }
        if ($Outputs[$key].Count -lt 30) { $Outputs[$key] = $Outputs[$key] + $ev.Output }
    }
    # Package-level build failures have no Test at all, so they would miss every
    # branch above; their text arrives as build-output events.
    if (($ev.Action -eq "build-fail" -or $ev.Action -eq "build-output") -and $ev.Output) {
        Write-Host "   build: $($ev.Output.TrimEnd())"
    }
}
if ($testExit -ne 0) {
    Write-Host "== go test: output of the failing tests"
    foreach ($k in $FailedKeys) {
        $parts = $k -split "\|"
        Write-Host "--- $((($parts[0]) -split '/')[-1])/$($parts[1])"
        foreach ($l in $Outputs[$k]) { Write-Host "    $($l.TrimEnd())" }
    }
    if ($nonJsonLines.Count -gt 0) {
        Write-Host "go test said (tool lines, non-JSON):"
        $nonJsonLines | Select-Object -First 20 | ForEach-Object { Write-Host "    $_" }
    }
    Write-Host "go test stderr tail:"
    Get-Content $testErrFile -Tail 20 -ErrorAction SilentlyContinue

    # One isolated retry of exactly the packages that failed. This host has a
    # documented load-flake family (PROJECT_STATE; three strikes in one night
    # 2026-09-28/29 across the worker and api packages): a timing-sensitive
    # test fails under full parallel package load — each time a different one —
    # and passes the same test in isolation, which is that family's documented
    # acceptance. The gate performs the isolation re-run itself instead of
    # leaving every strike to a human: the first failure stays printed above
    # as the evidence, the verdict line discloses the recovery, and a retry
    # that also fails is thrown as the real failure it looks like. One retry,
    # failed packages only — not a loop, and not a threshold relaxed.
    $failedPkgs = $FailedKeys | ForEach-Object { ($_ -split "\|")[0] } | Sort-Object -Unique
    if ($failedPkgs.Count -eq 0) {
        # No failing test and no build event to isolate: the tool itself failed.
        # Retrying here would run the current directory ("0 failed package(s)",
        # seen live 2026-10-03 02:00) and misreport the shape as a test failure.
        throw "go test exited $testExit with no failing test to isolate — a tool-level failure; its own words are printed above"
    }
    Write-Host "== go test: isolated retry of $($failedPkgs.Count) failed package(s) (known load-flake family)"
    $retryLines = @(go test -count=1 -json $failedPkgs 2>$testErrFile | ForEach-Object { "$_" })
    $retryExit = $LASTEXITCODE
    $retryFail = @()
    foreach ($line in $retryLines) {
        if (-not $line.StartsWith("{")) { continue }
        $ev = $null
        try { $ev = $line | ConvertFrom-Json } catch { continue }
        if ($ev.Action -eq "fail" -and $ev.Test) { $retryFail = $retryFail + "$($ev.Package)|$($ev.Test)" }
    }
    if ($retryExit -ne 0 -or $retryFail.Count -gt 0) {
        Write-Host "== isolated retry FAILED — not the load flake, a real failure"
        Get-Content $testErrFile -Tail 20 -ErrorAction SilentlyContinue
        throw "go test failed twice (full run: exit $testExit; isolated retry: exit $retryExit, $($retryFail.Count) failed test(s))"
    }
    $script:FlakeRecovered = ($failedPkgs | ForEach-Object { ($_ -split '/')[-1] }) -join ", "
    Write-Host "== go test: isolated retry PASSED — load-flake strike recorded, the first failure above stays the evidence"
}
# An empty run is not a green run. Every count above is zero when the step never
# reached a test — which is what the colliding temp file caused — and the verdict line
# still read PASS with "steps not run: none", because this step *did* run: it just ran
# nothing. The exit code cannot catch it either: a redirect that fails before the child
# starts leaves $LASTEXITCODE holding whatever the previous step set.
if ($TestPass + $TestSkips.Count + $FailedKeys.Count -eq 0) {
    Write-Host "== go test: produced no test events at all (stdout lines: $($testLines.Count))"
    Write-Host "stderr tail:"
    Get-Content $testErrFile -Tail 20 -ErrorAction SilentlyContinue
    throw "go test ran no tests — that is not a pass"
}
Remove-Item $testErrFile -ErrorAction SilentlyContinue
Write-Host "== go test: $TestPass passed, $($TestSkips.Count) skipped"
foreach ($s in ($TestSkips | Select-Object -First 20)) { Write-Host "   skip $s" }
if ($TestSkips.Count -gt 20) { Write-Host "   ... and $($TestSkips.Count - 20) more" }

if ($Mode -eq "fast") {
    # The race detector runs here too, over a subset: every race this project has
    # actually hit lived in internal/cli (the analyze fan-out writing one shared
    # stream at 680d707; a test helper clearing a channel field the test goroutine
    # read unlocked, 2026-09-23), and the queue and the subprocess client are the
    # other two owners of goroutines its tests can race. The full leg still runs
    # -race over ./... — this is the same defect class caught in about a minute and a
    # half on the machine making the change instead of twenty minutes later on a node.
    # check.sh carries the same step and the same $NotRun entry; a race that goes
    # unwatched because no host in reach has a C toolchain must say so in the verdict.
    $racePkgs = @("./internal/cli", "./internal/job", "./internal/worker", "./internal/pipeline")
    if ((go env CGO_ENABLED).Trim() -eq "1" -and (Get-Command gcc -ErrorAction SilentlyContinue)) {
        Write-Host "== go test -race (subset: cli job worker pipeline)"
        $raceStart = Get-Date
        # -timeout=30m is a work budget, not an assertion: go's default 10 m per
        # package sat on a 458 s quiet-machine baseline (76% spent), and the night
        # this machine hosts several projects' gates at once — five were running
        # 01:03-01:41 on 2026-10-03 (QX, AetherScope ×2, ashare-quant ×2, game-
        # scheduler beside this one) — both the subset and its isolated retry blew
        # the default twice on the same commit while every package passed in
        # isolation. Same class as the in-gate limit QX raised 20m→30m on this
        # shared host. The retry policy below is unchanged.
        $raceOut = @(go test -count=1 -race -timeout=30m @racePkgs | ForEach-Object { "$_" })
        $raceExit = $LASTEXITCODE
        $raceOut | ForEach-Object { Write-Host $_ }
        # The same documented load-flake family the plain go test step isolates
        # and retries strikes this step too — the race build is slower and the
        # subset runs four goroutine-heavy packages at once, so worker's reaper
        # test blew its 60 s call deadline under the gate's own load on
        # 2026-10-01 while passing the same test in isolation at 8.7 s. Same
        # policy, applied here: one isolated retry of exactly the failed
        # packages, the first failure printed above as the evidence, the
        # verdict line disclosing the recovery, a second failure still a real
        # failure. One retry — not a loop, and not a threshold relaxed.
        if ($raceExit -ne 0) {
            $raceFailed = @($raceOut | ForEach-Object { if ($_ -match "^FAIL\s+(\S+)") { $Matches[1] } })
            if ($raceFailed.Count -eq 0) {
                throw "go test -race (subset) failed with exit $raceExit and no FAIL line to isolate (build or panic output above)"
            }
            Write-Host "== go test -race: isolated retry of $($raceFailed.Count) failed package(s) (known load-flake family)"
            $raceRetry = @(go test -count=1 -race -timeout=30m @raceFailed | ForEach-Object { "$_" })
            $raceRetryExit = $LASTEXITCODE
            $raceRetry | ForEach-Object { Write-Host $_ }
            if ($raceRetryExit -ne 0) {
                throw "go test -race (subset) failed twice (full run: exit $raceExit; isolated retry: exit $raceRetryExit)"
            }
            $script:RaceFlakeRecovered = ($raceFailed | ForEach-Object { ($_ -split '/')[-1] }) -join ", "
            Write-Host "== go test -race: isolated retry PASSED — load-flake strike recorded, the first failure above stays the evidence"
        }
        Write-Host "   race subset wall: $([int]((Get-Date) - $raceStart).TotalSeconds)s"
    }
    else {
        Write-Host "== go test -race: SKIPPED (no cgo/C toolchain; the full leg covers it)"
        $NotRun += "race-subset"
    }
}

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
        $NotRun += "race"
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
        Push-Location crates/xcut-worker-media
        try {
            Resolve-RustToolchain
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
        Write-Host "== govulncheck: not installed (go install golang.org/x/vuln/cmd/govulncheck@v1.8.0), skipped"
        $NotRun += "govulncheck"
    }

    # Static security analysis: HIGH-severity findings fail the gate. The
    # severity+confidence pair used to be the filter, and on this codebase that
    # cell was empty (90 findings, none HIGH x HIGH), so the step could not fail
    # for want of a threshold. Suppressions stay in the source as
    # `#nosec GXXX -- reason` (each with a written justification), never as
    # blanket rule exclusions — and the two scanner flags below reject a
    # suppression that leaves out either half, so the convention is enforced
    # rather than trusted to review.
    if (Get-Command gosec -ErrorAction SilentlyContinue) {
        # Same attribution as check.sh: gosec reports "dev" for --version whatever
        # it was built from, so the log names the bytes instead.
        $gosecBin = (Get-Command gosec).Source
        $gosecFp = (Get-FileHash -Algorithm SHA256 $gosecBin).Hash.Substring(0, 12).ToLowerInvariant()
        Write-Host "== gosec scanner: $gosecFp at $gosecBin (pin: GOBIN=.tools/bin go install github.com/securego/gosec/v2/cmd/gosec@v2.29.0)"
        Invoke-Step "gosec" { gosec -severity high -tests=false -nosec-require-justification -nosec-require-rules ./... }
    }
    else {
        Write-Host "== gosec: not installed (GOBIN=.tools/bin go install github.com/securego/gosec/v2/cmd/gosec@v2.29.0), skipped"
        $NotRun += "gosec"
    }
}

$skipNote = ""
if ($TestSkips.Count -gt 0) { $skipNote = "; tests skipped: $($TestSkips.Count) (see '== go test' above)" }
$flakeNote = ""
if ($script:FlakeRecovered) { $flakeNote = "; flake-recovered: $script:FlakeRecovered (failed under full load, passed the isolated retry — first failure printed above)" }
if ($script:RaceFlakeRecovered) { $flakeNote = "$flakeNote; race flake-recovered: $script:RaceFlakeRecovered (same policy, the -race subset)" }
Write-Host "== gate ($Mode): PASS (steps not run: $(if ($NotRun) { $NotRun -join ", " } else { "none" })$skipNote$flakeNote)"
