#!/usr/bin/env sh
# Vulnerability scan in a Linux container — same role as the local
# govulncheck step of scripts/check.sh, for hosts where an Application
# Control policy blocks freshly built local tools (DECISIONS.md D11).
#
# Usage: scripts/vuln-docker.sh [image]     (default: golang:1.25-bookworm)
set -e
cd "$(dirname "$0")/.."

image=${1:-golang:1.25-bookworm}

# Git Bash mangles POSIX-looking paths in -v args; pwd -W yields a
# Windows-style path that docker accepts (plain pwd on real Linux).
MSYS_NO_PATHCONV=1 docker run --rm \
    -v "$(pwd -W 2>/dev/null || pwd):/src:ro" \
    -w /src \
    -e "GOCACHE=/go-cache" \
    -e "GOBIN=/go-bin" \
    -v xcut-go-cache:/go-cache \
    "$image" \
    sh -c 'go run golang.org/x/vuln/cmd/govulncheck@latest ./...'
