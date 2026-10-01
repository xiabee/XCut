#!/usr/bin/env bash
# Fetch a stock FFmpeg build the repository pins, for the host architecture.
#
# Why a pin exists at all: the Kylin V10 SP1 distro build cannot drive XCut — its
# Hisilicon OMX decoder writes logs to stdout while ffprobe is answering JSON there
# (DECISIONS D13), and that 4.2.2 ships without the xfade filter — so the documented
# answer has always been "point XCUT_FFPROBE / XCUT_FFMPEG at a stock build". Until
# now that build was whatever someone happened to download, which is why the ARM64
# suite could only be reported as NOT VERIFIED. This pins one: an immutable release
# tag, a fixed file, a size and a SHA256, and it refuses a mismatch rather than
# running something else.
#
# Usage: scripts/fetch-stock-ffmpeg.sh [arm64|x64] [destdir]
#   (default: this host's own architecture; destdir default <repo>/.tools)
# Prints the bin directory on the last line.
#
# How each digest was established, stated plainly: GitHub's release API publishes no
# upstream digest for these assets (`digests: null`, checked 2026-09-23), so this is
# not publisher-signed. arm64: the hash the tagged URL served to two independent
# fetches on 2026-09-23 — the aarch64 host it is meant for, and an x86_64 laptop —
# which agreed at the byte count recorded below. x64: the same procedure on
# 2026-10-02 — an x86_64 laptop and the x86_64 compat node, independent fetches of
# the same tag. The tag is an autobuild tag, so its assets do not move;
# `master-latest`, which does move, is deliberately not used.
#
# For `go test ./...` that directory must go on PATH: the suite resolves ffmpeg and
# ffprobe by name (internal/testmedia, media.requireFFmpeg), so XCUT_FFMPEG and
# XCUT_FFPROBE — which are config overrides, read only by the product at startup —
# do not reach it. The overrides are the right lever for the shipped binary.
set -eu

ARCH=${1:-}
case "$ARCH" in
    arm64|x64)
        shift
        ;;
    "")
        case "$(uname -m)" in
            aarch64) ARCH=arm64 ;;
            x86_64)  ARCH=x64 ;;
            *) echo "host is $(uname -m); name the arch explicitly: $0 arm64|x64 [destdir]" >&2; exit 2 ;;
        esac
        ;;
    *)
        echo "usage: $0 [arm64|x64] [destdir]" >&2
        exit 2
        ;;
esac

TAG="autobuild-2026-09-20-13-11"
case "$ARCH" in
    arm64)
        FILE="ffmpeg-n9.0.2-3-ga5923073bf-linuxarm64-gpl-9.0.tar.xz"
        SHA256="031d4336d6a9fab8c0bc26c3bca4fab0c09fcfdd405a7ef6ce2aa4b535fddce9"
        BYTES=126904212
        ;;
    x64)
        FILE="ffmpeg-n9.0.2-3-ga5923073bf-linux64-gpl-9.0.tar.xz"
        SHA256="7569c7c00a421d4fb4636925a126e96e051bb5cdd0b0e0a91576ddfadddd9bff"
        BYTES=150145424
        ;;
esac
DIR="${FILE%.tar.xz}"
URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/$TAG/$FILE"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# `$1` (now $1 after the arch shift) is the destination as given; a relative one
# resolves against the repo root. Joining the two unconditionally is what made an
# absolute argument land *inside* the checkout (`<repo>//abs/path/tools`), which then
# downloaded 121 MB into the snapshot.
if [ $# -ge 1 ] && [ "$1" != "" ]; then
    OUT="$1"
    case "$OUT" in [!/]*) OUT="$ROOT/$OUT" ;; esac
else
    OUT="$ROOT/.tools"
fi
CD="$OUT/$DIR"

say_bin() {
    # xfade is one of the two reasons this build exists, so check it rather than
    # trusting the version string: a build without it reproduces the distro failure.
    "$1/ffmpeg" -hide_banner -version | head -1
    if ! "$1/ffmpeg" -hide_banner -filters 2>/dev/null | grep -qw xfade; then
        echo "this build has no xfade filter — it is not usable for the render suite" >&2
        exit 1
    fi
    echo "$1"
}

if [ -x "$CD/bin/ffmpeg" ] && [ -x "$CD/bin/ffprobe" ]; then
    say_bin "$CD/bin"
    exit 0
fi

case "$ARCH" in
    arm64) WANT_UNAME=aarch64 ;;
    x64)   WANT_UNAME=x86_64 ;;
esac
[ "$(uname -m)" = "$WANT_UNAME" ] || echo "note: host is $(uname -m), the archive is for $WANT_UNAME — the binaries below will not run here" >&2

mkdir -p "$OUT"
TMP="$OUT/$ARCH-ffmpeg.tar.xz.part"
echo "fetching $URL" >&2
curl -fL --retry 3 -o "$TMP" "$URL"

# Verify before trusting: size first (cheap), then digest. A partial transfer or a
# moved asset stops here instead of surfacing as a mysterious test failure later.
ACTUAL=$(wc -c < "$TMP")
if [ "$ACTUAL" != "$BYTES" ]; then
    rm -f "$TMP"
    echo "size mismatch: got $ACTUAL, want $BYTES" >&2
    exit 1
fi
HASH=$(sha256sum "$TMP" | cut -d' ' -f1)
if [ "$HASH" != "$SHA256" ]; then
    rm -f "$TMP"
    echo "sha256 mismatch: got $HASH, want $SHA256" >&2
    exit 1
fi

rm -rf "$CD"
tar -xf "$TMP" -C "$OUT"
rm -f "$TMP"
[ -x "$CD/bin/ffmpeg" ] || { echo "extracted archive has no bin/ffmpeg at $CD" >&2; exit 1; }
say_bin "$CD/bin"
