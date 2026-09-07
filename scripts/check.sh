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
if command -v ffmpeg >/dev/null 2>&1; then
    echo "== ffmpeg: $(ffmpeg -version 2>/dev/null | head -1)"
else
    echo "== ffmpeg: not on PATH (integration tests will skip)"
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

echo "== go test"
go test ./...

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
    fi

    if command -v govulncheck >/dev/null 2>&1; then
        echo "== govulncheck"
        govulncheck ./...
    else
        echo "== govulncheck: not installed (go install golang.org/x/vuln/cmd/govulncheck@latest), skipped" >&2
    fi
fi

echo "== gate ($mode): PASS"
