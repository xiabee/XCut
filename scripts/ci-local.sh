#!/usr/bin/env bash
# Local CI gate — the sh twin of scripts/ci-local.ps1, and the entry the
# compat CI nodes (kylin-pc) look for when they cannot run PowerShell.
# Delegates to this repo's own quality gate (scripts/check.sh), which sets
# up the local FFmpeg environment the render tests require. Exit code is
# authoritative.
set -e
cd "$(dirname "$0")/.."

# Die loudly, not silently: a node whose non-login PATH lacks go exits here with
# one line in its log instead of an unexplained 200 ms death (work-vm's shape,
# night 2026-09-30 — the gate never started and the log said nothing).
if ! command -v go >/dev/null 2>&1; then
    echo "ci-local: go is not on PATH — a non-login ssh PATH is the usual cause; export PATH or install the toolchain" >&2
    exit 127
fi

bash scripts/check.sh "${1:-fast}"

echo "LOCAL CI PASS"
