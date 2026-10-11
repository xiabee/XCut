#!/usr/bin/env bash
# XCut close battery — soak + shuffle + the full gate as one command, with
# per-leg evidence. Born the night the 2026-10-10 session was cut with its
# full leg still "running": the legs' logs survived in .night-<date>/, but
# nothing stitched them into a verdict, and the leg's actual FAIL (the race
# step's default 10 m timeout, hit by cli and pipeline at 04:29 and fixed the
# same night) sat untriaged for a day. Each leg here writes its own log and
# prints a verdict line the moment it lands, so a cut session leaves evidence
# a reader can trust instead of a question mark.
#
#   scripts/battery.sh [SOAK_ROUNDS]     # default 30, capped at 200
#
# The legs are sequential on purpose: soak and shuffle numbers are only
# comparable when nothing else owns the machine, and the full gate's race
# step is the load-flake family's favorite victim. Exit 0 = every leg green;
# a failed leg names itself and its log and does not stop the others.
set -u
REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"

ROUNDS="${1:-30}"
case "$ROUNDS" in ''|*[!0-9]*) echo "usage: battery.sh [SOAK_ROUNDS]" >&2; exit 2;; esac
if [ "$ROUNDS" -gt 200 ]; then
    echo "battery: soak rounds capped at 200 (got $ROUNDS) — this is a close battery, not a campaign" >&2
    exit 2
fi

dir=".night-$(date +%Y-%m-%d)"
mkdir -p "$dir"
fails=0

# leg <name> <command...>: run one leg to its own log, print the verdict the
# moment it lands. The verdict lines are the point — they are what a cut
# session's reader greps for.
leg() {
    name="$1"; shift
    log="$dir/battery-$name.log"
    echo "== battery leg: $name (log: $log)"
    start=$(date +%s)
    if "$@" > "$log" 2>&1; then
        echo "LEG $name: PASS ($(( $(date +%s) - start ))s, $log)"
    else
        rc=$?
        echo "LEG $name: FAIL exit=$rc ($(( $(date +%s) - start ))s, $log) — the log is the evidence"
        fails=$((fails + 1))
    fi
}

leg soak sh scripts/soak.sh "$ROUNDS"

# Shuffle twice with recorded seeds: the seed announcement ("-test.shuffle
# <N>", stderr) only prints under -v — the first flight ran without it and
# both seeds were lost (the order evidence stood, the replay did not) — so
# the shuffle legs run verbose and pay the bigger log for the replay handle.
# Replay a failure with `go test -shuffle=<N>`.
for i in 1 2; do
    leg "shuffle$i" go test -shuffle=on -count=1 -v ./...
    seed=$(grep -m1 -o '\-test\.shuffle [0-9]*' "$dir/battery-shuffle$i.log" 2>/dev/null || true)
    echo "   shuffle$i ${seed:-seed not printed}"
done

# The full gate: race over the whole tree (with the subset's 30 m budget and
# isolate-retry since the 04:29 timeout), cross-compile, Rust, and the
# in-gate secret scanners — through whichever twin this platform speaks.
if command -v powershell >/dev/null 2>&1 || command -v powershell.exe >/dev/null 2>&1; then
    leg full powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check.ps1 full
else
    leg full sh scripts/check.sh full
fi

if [ "$fails" -ne 0 ]; then
    echo "BATTERY: $fails leg(s) FAILED — the logs above are the evidence"
    exit 1
fi
echo "BATTERY: all legs PASS ($dir)"
