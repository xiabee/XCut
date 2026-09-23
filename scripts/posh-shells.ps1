# Shared PowerShell helper for the gate and the release scripts: find a shell that will
# actually run a command, rather than one whose name merely exists on PATH.
#
# Why this is not `Get-Command bash`: on the win-devops CI node, `bash` resolves to the WSL
# launcher, which answers `WSL_E_LOCAL_SYSTEM_NOT_SUPPORTED` and exits 1 (measured
# 2026-09-24). A gate that trusts the name reports that as its own step failing — the
# repository gets blamed for a host fact — while a script that *refuses the release* over
# it would be wrong in the other direction. So every candidate is asked to echo a token,
# and the answer is what counts.

function Resolve-PortableShell {
    $cands = @()
    # Git for Windows first: scripts/*.sh are written against its /proc and utilities.
    foreach ($p in "C:\Program Files\Git\bin\bash.exe", "C:\Program Files\Git\usr\bin\bash.exe") {
        if (Test-Path $p) { $cands += $p }
    }
    $cmd = Get-Command bash -ErrorAction SilentlyContinue
    if ($cmd) { $cands += $cmd.Source }
    $cmdSh = Get-Command sh -ErrorAction SilentlyContinue
    if ($cmdSh) { $cands += $cmdSh.Source }

    # A rejecting candidate writes its complaint to stderr, and the callers run under
    # $ErrorActionPreference = "Stop", which would promote it to a terminating error
    # before the probe could answer "no".
    $prev = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        foreach ($c in $cands) {
            $probe = & $c -c "echo xcut-shell-ok" 2>$null
            if ($LASTEXITCODE -eq 0 -and ("$probe").Trim() -eq "xcut-shell-ok") { return $c }
        }
    }
    finally { $ErrorActionPreference = $prev }
    return $null
}
