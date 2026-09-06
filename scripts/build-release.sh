#!/usr/bin/env bash
# Build release binaries of xcut (host Rust worker when present).
# Usage: scripts/build-release.sh [version] [outdir]
set -euo pipefail

VERSION="${1:-}"
OUTDIR="${2:-dist}"

if [ -z "$VERSION" ]; then
    VERSION="$(git describe --tags --always 2>/dev/null || echo "dev-$(date +%Y%m%d-%H%M)")"
fi

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X github.com/xiabee/XCut/internal/version.Version=$VERSION -X github.com/xiabee/XCut/internal/version.Commit=$COMMIT -X github.com/xiabee/XCut/internal/version.BuildDate=$DATE"

mkdir -p "$OUTDIR"

for plat in windows/amd64 linux/amd64 linux/arm64; do
    goos="${plat%/*}"
    goarch="${plat#*/}"
    ext=""
    [ "$goos" = "windows" ] && ext=".exe"
    out="$OUTDIR/xcut-$VERSION-$goos-$goarch$ext"
    echo "building $out"
    GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/xcut
done

# Rust workers: static linux (musl via bundled rust-lld) + host platform.
worker_dir="crates/xcut-worker-media"
if command -v cargo >/dev/null 2>&1; then
    if (cd "$worker_dir" && cargo build --release --target x86_64-unknown-linux-musl >/dev/null 2>&1); then
        cp "$worker_dir/target/x86_64-unknown-linux-musl/release/xcut-worker-media"            "$OUTDIR/xcut-worker-media-$VERSION-linux-amd64"
        echo "built rust worker (linux, static musl)"
    else
        echo "rust worker linux build skipped (target not installed)"
    fi
    host_worker="$worker_dir/target/release/xcut-worker-media"
    [ -f "$host_worker.exe" ] && host_worker="$host_worker.exe"
    if [ -f "$host_worker" ]; then
        cp "$host_worker" "$OUTDIR/xcut-worker-media-$VERSION-host$( [ "${host_worker##*.}" = "exe" ] && echo ".exe" )"
        echo "copied host rust worker"
    fi
else
    echo "cargo not found — rust worker binaries skipped (optional component)"
fi

ls -la "$OUTDIR"
