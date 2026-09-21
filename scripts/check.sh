#!/usr/bin/env sh
# XCut local quality gate — CI-equivalent validation without GitHub Actions
# (DECISIONS.md D11). Usage:
#
#   scripts/check.sh fast   # fmt + vet + build + go test (no race)
#   scripts/check.sh full   # + race, cross-compile, Rust, govulncheck
#
# Exit code 0 = gate green. A local FFmpeg under .tools/ is used when present;
# integration tests skip automatically when FFmpeg is unavailable.
set -e
cd "$(dirname "$0")/.."

mode=${1:-full}
if [ "$mode" != "fast" ] && [ "$mode" != "full" ]; then
    echo "usage: $0 [fast|full]" >&2
    exit 2
fi

# Prefer a repo-local FFmpeg (.tools/, gitignored) when PATH has none.
if ! command -v ffmpeg >/dev/null 2>&1; then
    for d in .tools/ffmpeg/bin .tools/ffmpeg-*; do
        if [ -x "$d/ffmpeg.exe" ] || [ -x "$d/ffmpeg" ]; then
            PATH="$(pwd)/$d:$PATH"
            export PATH
            break
        fi
    done
fi
# Repo-local go tools (govulncheck etc.) installed via GOBIN=.tools/bin.
if [ -d .tools/bin ]; then
    PATH="$(pwd)/.tools/bin:$PATH"
    export PATH
fi
# Steps that did not run are named in the verdict line. A gate that prints PASS
# while three of its checks quietly no-oped is the same failure as a secret scan
# that reports "clean" over an empty file list — and the ffmpeg case has already
# fooled this project once (a real-footage test "passed" because it had skipped).
NOT_RUN=""
if command -v ffmpeg >/dev/null 2>&1; then
    echo "== ffmpeg: $(ffmpeg -version 2>/dev/null | head -1)"
else
    echo "== ffmpeg: not on PATH (integration tests will skip)"
    NOT_RUN="$NOT_RUN integration-tests(no-ffmpeg)"
fi

# Secret scanning. This leg used to say nothing about secrets while still
# printing "gate: PASS" — a reader could not tell that no scan had run.
# gitleaks is optional here (the control plane scans commits before dispatch),
# but its absence is now stated in the same breath as the verdict.
GK=""
if command -v gitleaks >/dev/null 2>&1; then
    GK=gitleaks
elif [ -x ".tools/bin/gitleaks" ]; then
    GK="$(pwd)/.tools/bin/gitleaks"
elif [ -x ".tools/bin/gitleaks.exe" ]; then
    GK="$(pwd)/.tools/bin/gitleaks.exe"
fi
SECRET_STATUS="NOT RUN (gitleaks absent — commits are scanned by the control plane, this script does not)"
if [ -n "$GK" ]; then
    # See check.ps1: the .gotmp/ allowlist is only legitimate while nothing
    # under it is tracked, because gitleaks excuses tracked history too.
    if [ -d .git ]; then
        tracked_scratch=$(git ls-files -- '.gotmp/*' 2>/dev/null || true)
        if [ -n "$tracked_scratch" ]; then
            echo ".gotmp/ is allowlisted for secret scanning, so it must never hold tracked files:" >&2
            echo "$tracked_scratch" >&2
            exit 1
        fi
    fi
    # Upper bound on the scan scope, not the read set: gitleaks skips binaries
    # and allowlisted paths but not .gitignore. Its job here is to prove the
    # scope is not empty — a zero would mean a scan that covered nothing.
    scan_paths=$(find . -type f -not -path './.git/*' -not -path './.tools/*' 2>/dev/null | wc -l | tr -d ' ')
    if [ "$scan_paths" -eq 0 ]; then
        echo "gitleaks: scan scope is empty — refusing to call that clean" >&2
        exit 1
    fi
    if [ -d .git ]; then
        echo "== gitleaks (git history)"
        "$GK" git . --no-banner --redact
    fi
    echo "== gitleaks (working tree, $scan_paths paths under .)"
    # JSON report on stdout: a failing scan must name the file in the gate log.
    "$GK" dir . --no-banner --redact --report-format json --report-path -
    SECRET_STATUS="ran over $scan_paths paths"
fi

echo "== gofmt"
unformatted=$(gofmt -l internal cmd)
if [ -n "$unformatted" ]; then
    echo "unformatted files: $unformatted" >&2
    exit 1
fi

echo "== go vet"
go vet ./...

echo "== go build"
go build ./...

# Same skip accounting as check.ps1: a package-level "ok" cannot tell "ran and
# passed" from "never ran", and three suites here are conditional (integration
# tests need ffmpeg, the cross-language sidecar tests need python, the Rust
# protocol tests need the worker binary). Names, not reasons — a skip event
# carries no message.
echo "== go test"
test_json=$(mktemp)
test_err=$(mktemp)
set +e
go test -count=1 -json ./... >"$test_json" 2>"$test_err"
test_exit=$?
set -e
if [ "$test_exit" -ne 0 ]; then
    echo "go test FAILED (exit $test_exit). Failing output:"
    grep '"Action":"fail"' "$test_json" | tail -60
    echo "--- stderr tail:"
    tail -20 "$test_err"
    rm -f "$test_json" "$test_err"
    exit 1
fi
counts=$(awk '/"Action":"pass"/ && /"Test":"/ { p++ }
/"Action":"skip"/ && /"Test":"/ { s++ }
END { printf "%d %d", p+0, s+0 }' "$test_json")
TestPass=${counts%% *}
TestSkip=${counts##* }
echo "== go test: $TestPass passed, $TestSkip skipped"
if [ "$TestSkip" -gt 0 ]; then
    awk '/"Action":"skip"/ && /"Test":"/ {
        t = ""; pk = ""
        if (match($0, /"Test":"[^"]*"/)) t = substr($0, RSTART+8, RLENGTH-9)
        if (match($0, /"Package":"[^"]*"/)) {
            pk = substr($0, RSTART+11, RLENGTH-12)
            sub(/.*\//, "", pk)
        }
        print "   skip " pk "/" t
    }' "$test_json" | head -20
    if [ "$TestSkip" -gt 20 ]; then
        echo "   ... and $((TestSkip - 20)) more"
    fi
fi
rm -f "$test_json" "$test_err"

if [ "$mode" = "full" ]; then
    # The race detector needs cgo + a C toolchain (absent on this Windows
    # setup). Skip loudly rather than silently; run the linux container race
    # gate (scripts/race-docker.sh) or a CI dispatch for race coverage.
    if go env CGO_ENABLED | grep -q '^1$' && command -v gcc >/dev/null 2>&1; then
        echo "== go test -race"
        go test -race ./...
    else
        echo "== go test -race: SKIPPED (no cgo/C toolchain; use scripts/race-docker.sh or CI dispatch)"
    fi

    echo "== cross-compile checks (compile-verified only, not runtime-verified)"
    GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/xcut
    GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/xcut

    if command -v cargo >/dev/null 2>&1; then
        # Windows hosts without MSVC Build Tools cannot link under the default
        # msvc toolchain; an installed windows-gnu toolchain links fine (and
        # keeps the gate native). Probe once, stick with what works.
        probe=$(cd crates/xcut-worker-media && cargo check -q >/dev/null 2>&1 && echo ok) || probe=""
        if [ -z "$probe" ] && rustup toolchain list 2>/dev/null | grep -q windows-gnu; then
            export RUSTUP_TOOLCHAIN=stable-x86_64-pc-windows-gnu
            echo "== rust: msvc linker unavailable, using windows-gnu toolchain"
        fi
        echo "== cargo fmt --check"
        (cd crates/xcut-worker-media && cargo fmt --check)
        echo "== cargo clippy"
        (cd crates/xcut-worker-media && cargo clippy --all-targets -- -D warnings)
        echo "== cargo test"
        (cd crates/xcut-worker-media && cargo test)
    else
        echo "== cargo: not on PATH, skipped" >&2
        NOT_RUN="$NOT_RUN rust"
    fi

    if command -v govulncheck >/dev/null 2>&1; then
        if ! govulncheck ./...; then
            rc=$?
            if [ "$rc" = 126 ]; then
                # Blocked by an Application Control policy: loud skip, use
                # scripts/vuln-docker.sh for the scan instead.
                echo "== govulncheck: SKIPPED (execution blocked by policy; use scripts/vuln-docker.sh)" >&2
                NOT_RUN="$NOT_RUN govulncheck(policy-blocked)"
            else
                exit "$rc"
            fi
        fi
    else
        echo "== govulncheck: not installed (go install golang.org/x/vuln/cmd/govulncheck@latest), skipped" >&2
        NOT_RUN="$NOT_RUN govulncheck"
    fi
fi

skipnote=""
if [ "${TestSkip:-0}" -gt 0 ]; then
    skipnote="; tests skipped: $TestSkip (see '== go test' above)"
fi
echo "== gate ($mode): PASS (secret scan: $SECRET_STATUS; not run:${NOT_RUN:- nothing}$skipnote)"
