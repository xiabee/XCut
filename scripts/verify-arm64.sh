#!/usr/bin/env bash
# Verify this checkout on ARM64: run the whole suite against the pinned stock FFmpeg,
# with the toolchain proven to have reached the tests before any verdict is printed.
#
# Why a script rather than a paragraph in OPERATIONS.md: the first ARM64 run of the
# session that pinned the build reported 48 failures, and the cause was not the product
# — it was the harness exporting XCUT_FFMPEG/XCUT_FFPROBE, which the suite never reads
# (internal/testmedia and media.requireFFmpeg resolve ffmpeg and ffprobe *by name*), and
# asserting only that the pinned binary existed somewhere on disk. Both mistakes are
# unreachable here: the injection goes through PATH, and TOOL_CHECK asks PATH which
# binary the tests are about to get.
#
# Usage: sh scripts/verify-arm64.sh [--skip-race] [--tools DIR]
#   --tools DIR   where the pinned FFmpeg lives / should be fetched to (default:
#                 <repo>/.tools, gitignored). Point it at a directory a previous run
#                 already filled to skip a 121 MB download.
# Prints counts on stdout; the verbose suite log path is printed too, and kept.
# Exit 0 = suite green against the pin. The race leg never changes the exit status:
# Kylin V10 SP1's kernel refuses ThreadSanitizer, which is reported, not hidden.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SKIP_RACE=0
TOOLS=".tools"
while [ $# -gt 0 ]; do
    case "$1" in
        --skip-race) SKIP_RACE=1 ;;
        --tools) shift; [ $# -gt 0 ] || { echo "verify-arm64: --tools needs a directory" >&2; exit 2; }
                 TOOLS="$1" ;;
        *) echo "verify-arm64: unknown argument '$1' (only --skip-race, --tools DIR)" >&2; exit 2 ;;
    esac
    shift
done

fail() { echo "verify-arm64: $1" >&2; exit 1; }

ARCH=$(uname -m)
[ "$ARCH" = "aarch64" ] || fail "host is $ARCH, not aarch64 — the pinned archive is arm64-only and would not run here"

cd "$ROOT" || fail "cannot enter $ROOT"
command -v go >/dev/null 2>&1 || fail "no go toolchain on PATH"

# The fetch script's own status, not the pipeline's: `| tail -1` would report tail's 0
# even when the download refused, which is how a pin stops being a pin.
FETCHED=$(sh scripts/fetch-arm64-ffmpeg.sh "$TOOLS") || fail "the pinned FFmpeg refused to fetch"
BIN=$(printf '%s\n' "$FETCHED" | tail -1)
[ -x "$BIN/ffmpeg" ] && [ -x "$BIN/ffprobe" ] || fail "no ffmpeg/ffprobe at $BIN"

# PATH, never replaced: the Go and Rust toolchains, and python3 for the sidecar tests,
# have to stay reachable for the suite to mean anything.
export PATH="$BIN:$PATH"

echo "== environment"
echo "  arch:    $ARCH"
echo "  go:      $(go version)"
echo "  pin dir: $BIN"
echo "  ffmpeg:  $(ffmpeg -hide_banner -version 2>/dev/null | head -1)"
echo "  xfade:   $(ffmpeg -hide_banner -filters 2>/dev/null | grep -cw xfade)"

echo "== step: TOOL_CHECK — ask PATH what the suite is about to resolve"
FFP=$(command -v ffprobe)
FFF=$(command -v ffmpeg)
echo "  ffmpeg:  $FFF"
echo "  ffprobe: $FFP"
case "$FFF:$FFP" in
    *$BIN*) ;;
    *) fail "PATH did not take: resolved binaries are not under $BIN" ;;
esac
# The version string, not the path alone: a vendor build shadowed at the wrong depth
# resolves to a file that is not the pin.
"$FFP" -version 2>/dev/null | head -1 | grep -q 'n9\.0\.2' \
    || fail "the resolved ffprobe is not the pinned 9.0.2 build"
echo "  TOOL_CHECK=OK (the vendor build is shadowed)"

LOG=$(mktemp -d)/suite.out
echo "== step: go test -v ./...   (log: $LOG)"
go test -count=1 -v ./... >"$LOG" 2>&1
SUITE_RC=$?
echo "suite_rc=$SUITE_RC ran=$(grep -ac '^--- PASS' "$LOG") skipped=$(grep -ac '^--- SKIP' "$LOG") failed=$(grep -ac '^--- FAIL' "$LOG") packages_ok=$(grep -acE '^ok[[:space:]]' "$LOG")"
# The vendor corruption message is the tell that the toolchain injection failed even if
# everything above held; naming it means a reader does not have to diff failure lists.
echo "vendor_corruption_lines=$(grep -ac 'writes decoder-plugin logs to stdout' "$LOG")"
if [ "$SUITE_RC" -ne 0 ]; then
    grep -a -- '--- FAIL' "$LOG" | head -20
    grep -a -B1 -- '--- FAIL' "$LOG" | grep -aE '_test\.go:[0-9]+:' | head -10
fi

RACE_RC=skipped
if [ "$SKIP_RACE" -eq 0 ]; then
    echo "== step: go test -race (a kernel that refuses ThreadSanitizer is a report, not a pass)"
    RLOG=$(mktemp -d)/race.out
    go test -count=1 -race ./internal/job/ ./internal/worker/ ./internal/media/ >"$RLOG" 2>&1
    RACE_RC=$?
    echo "race_rc=$RACE_RC data_race_lines=$(grep -ac 'DATA RACE' "$RLOG") tsan_refused_lines=$(grep -ac 'unsupported VMA range' "$RLOG")"
fi

echo "== done suite_rc=$SUITE_RC race_rc=$RACE_RC tool_check=OK log=$LOG"
exit "$SUITE_RC"
