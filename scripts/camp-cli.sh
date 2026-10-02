#!/usr/bin/env bash
# Flake campaign — the cli one-shot family, hunted under load.
#
#   scripts/camp-cli.sh [ROUNDS]        # default 10
#
# The family (session #26 ledger): cli tests that pass in isolation but
# strike intermittently under full-package load, a different test each run.
# The design that caught its oldest member (session #28, run 8 of 10 — the
# probe WaitDelay mislabel): run the cli package repeatedly with -shuffle=on
# while the worker and pipeline packages loop in parallel as the synthetic
# load. cli's failing-test Fatalf ships full stdout/stderr, so a strike
# carries its own evidence; this script's job is to keep that evidence.
#
# Exit codes: 0 = all rounds green, 1 = at least one strike, 2 = setup
# failure. Strike and clean round logs alike are kept under an evidence
# directory printed in the summary — the log IS the product, never trim it.
#
# Load discipline: run this alone — concurrent gates or heavy scans corrupt
# both the campaign and the other job (the worker package's 60 s deadline
# tests die under contention, measured twice on 2026-09-28/29).
set -u

ROUNDS="${1:-10}"
case "$ROUNDS" in ''|*[!0-9]*) echo "usage: camp-cli.sh [ROUNDS]" >&2; exit 2;; esac

REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"
command -v go >/dev/null 2>&1 || { echo "camp-cli: go is not on PATH" >&2; exit 2; }

EV="${TEMP:-/tmp}/xcut-camp-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$EV" || exit 2
FLAG="$EV/load.flag"
LOAD_LOG="$EV/loader.log"

# --- synthetic load: worker + pipeline loop until the flag is gone.
# The loop re-checks between passes; after the flag is lifted the running
# pass finishes and the subshell exits — never killed mid-test, because an
# orphaned test binary holds the Windows image lock and breaks the next
# go test with "unlinkat: Access is denied".
(
    while [ -f "$FLAG" ]; do
        go test ./internal/worker/ ./internal/pipeline/ >>"$LOAD_LOG" 2>&1
    done
) &
LOADER=$!
touch "$FLAG"
cleanup() {
    status=$?
    rm -f "$FLAG"
    if [ -n "${LOADER:-}" ]; then
        if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then
            wait "$LOADER" 2>/dev/null   # drain the pass in flight
        else
            kill "$LOADER" 2>/dev/null   # aborted by signal: best effort
        fi
    fi
}
trap cleanup EXIT

strikes=0
i=0
while [ "$i" -lt "$ROUNDS" ]; do
    i=$((i+1))
    out="$EV/round$i.log"
    if go test -shuffle=on -count=1 ./internal/cli/ >"$out" 2>&1; then
        echo "round $i: ok"
    else
        strikes=$((strikes+1))
        echo "round $i: STRIKE"
        grep -E '^(--- FAIL|FAIL|panic)' "$out" | head -5
    fi
done

rm -f "$FLAG"
wait "$LOADER" 2>/dev/null
echo "campaign: $ROUNDS rounds, $strikes strike(s); evidence: $EV"
[ "$strikes" -eq 0 ]
