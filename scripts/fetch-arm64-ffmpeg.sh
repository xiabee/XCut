#!/usr/bin/env bash
# Fetch the stock arm64 FFmpeg that the ARM64 verification run uses.
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
# How the digest was established, stated plainly: GitHub's release API publishes no
# upstream digest for this asset (`digests: null`, checked 2026-09-23), so this is
# not publisher-signed. It is the hash the tagged URL served to two independent
# fetches on 2026-09-23 — the aarch64 host it is meant for, and an x86_64 laptop —
# which agreed at the byte count recorded below. The tag is an autobuild tag, so its
# assets do not move; `master-latest`, which does move, is deliberately not used.
#
# Usage: scripts/fetch-arm64-ffmpeg.sh [destdir]   (default: <repo>/.tools)
# Prints the bin directory on the last line.
#
# For `go test ./...` that directory must go on PATH: the suite resolves ffmpeg and
# ffprobe by name (internal/testmedia, media.requireFFmpeg), so XCUT_FFMPEG and
# XCUT_FFPROBE — which are config overrides, read only by the product at startup —
# do not reach it. The overrides are the right lever for the shipped binary.
set -eu

TAG="autobuild-2026-09-20-13-11"
FILE="ffmpeg-n9.0.2-3-ga5923073bf-linuxarm64-gpl-9.0.tar.xz"
DIR="ffmpeg-n9.0.2-3-ga5923073bf-linuxarm64-gpl-9.0"
SHA256="031d4336d6a9fab8c0bc26c3bca4fab0c09fcfdd405a7ef6ce2aa4b535fddce9"
BYTES=126904212
URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/$TAG/$FILE"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# `$1` is the destination as given; a relative one resolves against the repo root.
# Joining the two unconditionally is what made an absolute argument land *inside* the
# checkout (`<repo>//abs/path/tools`), which then downloaded 121 MB into the snapshot.
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

[ "$(uname -m)" = "aarch64" ] || echo "note: host is $(uname -m), the archive is for aarch64 — the binaries below will not run here" >&2

mkdir -p "$OUT"
TMP="$OUT/arm64-ffmpeg.tar.xz.part"
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
