#!/usr/bin/env sh
# Race-detector gate in a Linux container (Windows hosts have no cgo/C
# toolchain locally; DECISIONS.md D11 keeps race coverage available without
# spending Actions quota).
#
# Usage: scripts/race-docker.sh [image]     (default: golang:1.25-bookworm)
#
# The repo is mounted read-only; module and build caches live in a named
# volume so repeat runs are fast. FFmpeg is installed only when missing
# (integration tests skip without it — race coverage of pure-Go code is the
# point here).
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
    sh -c 'go test -race ./...'
