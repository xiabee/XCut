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
    echo "== go test -race"
    go test -race ./...

    echo "== cross-compile checks (compile-verified only, not runtime-verified)"
    GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/xcut
    GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/xcut

    if command -v cargo >/dev/null 2>&1; then
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
