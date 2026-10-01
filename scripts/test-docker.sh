#!/usr/bin/env sh
# Full Go test suite (integration included) + race in a Linux container —
# for hosts where an Application Control policy blocks freshly built local
# test binaries (DECISIONS.md D11). FFmpeg is installed in the container so
# integration tests run instead of skipping.
#
# Usage: scripts/test-docker.sh [image]     (default: golang:1.25-bookworm)
set -e
cd "$(dirname "$0")/.."

image=${1:-golang:1.25-bookworm}

# Git Bash mangles POSIX-looking paths in -v args; pwd -W yields a
# Windows-style path that docker accepts (plain pwd on real Linux).
MSYS_NO_PATHCONV=1 docker run --rm \
    -v "$(pwd -W 2>/dev/null || pwd):/src:ro" \
    -w /src \
    -e "GOCACHE=/go-cache" \
    -v xcut-go-cache:/go-cache \
    "$image" \
    sh -c 'apt-get update -qq && apt-get install -y -qq ffmpeg >/dev/null 2>&1; go test -race ./...'
