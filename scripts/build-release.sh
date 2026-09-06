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

# Host Rust worker, when built.
worker="crates/xcut-worker-media/target/release/xcut-worker-media"
[ -f "$worker.exe" ] && worker="$worker.exe"
if [ -f "$worker" ]; then
    cp "$worker" "$OUTDIR/xcut-worker-media-$VERSION-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)"
    echo "copied rust worker"
else
    echo "rust worker not built (optional; cargo build --release -p xcut-worker-media)"
fi

ls -la "$OUTDIR"
