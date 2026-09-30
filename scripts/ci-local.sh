#!/usr/bin/env bash
# Local CI gate — the sh twin of scripts/ci-local.ps1, and the entry the
# compat CI nodes (kylin-pc) look for when they cannot run PowerShell.
# Delegates to this repo's own quality gate (scripts/check.sh), which sets
# up the local FFmpeg environment the render tests require. Exit code is
# authoritative.
set -e
cd "$(dirname "$0")/.."

bash scripts/check.sh "${1:-fast}"

echo "LOCAL CI PASS"
