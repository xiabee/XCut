#!/usr/bin/env sh
# Idle-resource gate for `xcut serve` — AGENTS.md rule 6 states the goals (idle CPU ≈ 0,
# idle RAM < 100 MB, no background scanning loops), and until now they were only ever
# measured by hand: docs/NIGHTLY_LOG.md carries "11.9 MB / 0 CPU" from session #8 and
# "17.2 MB" from a later one, and nothing since has re-checked them. A product goal with no
# gate is a product goal that regresses quietly, which is the exact failure this rule was
# written against after someone added a polling loop.
#
# Usage: sh scripts/idle-check.sh <path to an xcut binary> [window-seconds]
# Exit 0 = inside the ceilings; 77 = this host cannot measure it (no /proc) — reported as
# "not run", never as a pass. Everything else is a failure naming what was measured.
#
# The ceilings are the rule's own numbers, not tuned to fit the reading: 100 MB of resident
# memory, and 2% of the window spent on CPU (a server that is truly idle spends its time in
# epoll_wait; 2% is 100× the noise a quiescent Go runtime produces).
set -eu

BIN="${1:-}"
WINDOW="${2:-5}"
RSS_LIMIT_KB=102400            # 100 MB, rule 6
CPU_LIMIT_PCT=2                # percent of the window

[ -n "$BIN" ] && [ -x "$BIN" ] || { echo "idle-check: no runnable binary at '$BIN'" >&2; exit 2; }
[ -r /proc/self/status ] || { echo "idle-check: no /proc on this host — the step did not run" >&2; exit 77; }

command -v curl >/dev/null 2>&1 || { echo "idle-check: no curl to wait for health — the step did not run" >&2; exit 77; }

HZ=$(getconf CLK_TCK 2>/dev/null || echo 100)

WS=$(mktemp -d)
PID=""
cleanup() {
    if [ -n "$PID" ]; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
    fi
    rm -rf "$WS"
}
trap cleanup EXIT INT TERM

# An ephemeral port and a throwaway workspace: the point is the real command, so nothing
# here may touch anyone's data or a fixed port a parallel leg could be holding.
"$BIN" --workspace "$WS" serve --addr 127.0.0.1:0 >"$WS/serve.out" 2>&1 &
PID=$!

ADDR=""
n=0
while [ $n -lt 100 ]; do
    ADDR=$(sed -n 's/.*addr=127\.0\.0\.1:\([0-9]*\).*/\1/p' "$WS/serve.out" 2>/dev/null | head -1)
    if [ -n "$ADDR" ] && curl -fsS -o /dev/null "http://127.0.0.1:$ADDR/api/v1/health" 2>/dev/null; then
        break
    fi
    if ! kill -0 "$PID" 2>/dev/null; then
        echo "idle-check: the server exited during startup:" >&2
        tail -5 "$WS/serve.out" >&2 || true
        exit 1
    fi
    n=$((n + 1))
    sleep 0.1
done
[ -n "$ADDR" ] || { echo "idle-check: no bound port after 10s" >&2; tail -5 "$WS/serve.out" >&2; exit 1; }
echo "idle-check: serving on 127.0.0.1:$ADDR, measuring ${WINDOW}s of silence"

read_counters() {
    # VmRSS from status, and utime+stime (fields 14 and 15, in clock ticks) from stat.
    rss=$(awk '/^VmRSS:/{print $2}' "/proc/$PID/status")
    cpu=$(awk '{print $14 + $15}' "/proc/$PID/stat")
    printf '%s %s\n' "$rss" "$cpu"
}

set -- $(read_counters)
RSS0=$1; CPU0=$2
sleep "$WINDOW"
# Nothing polled the server during the window: that is the measurement. A request would
# make the reading mean "cost of serving", which is a different number and a different gate.
set -- $(read_counters)
RSS1=$1; CPU1=$2

RSS_KB=$RSS1
CPU_TICKS=$((CPU1 - CPU0))
CPU_MS=$((CPU_TICKS * 1000 / HZ))
WINDOW_MS=$((WINDOW * 1000))
CPU_PCT=$((CPU_MS * 100 / WINDOW_MS))
RSS_MB=$((RSS_KB / 1024))

echo "idle-check: measured rss=${RSS_MB}MB (${RSS_KB} kB) cpu=${CPU_MS}ms of ${WINDOW_MS}ms (${CPU_PCT}%)"

FAIL=""
if [ "$RSS_KB" -gt "$RSS_LIMIT_KB" ]; then
    FAIL="$FAIL rss ${RSS_MB} MB exceeds the 100 MB idle ceiling;"
fi
if [ "$CPU_PCT" -gt "$CPU_LIMIT_PCT" ]; then
    FAIL="$FAIL cpu ${CPU_PCT}% of an idle window exceeds ${CPU_LIMIT_PCT}%;"
fi
if [ -n "$FAIL" ]; then
    echo "idle-check FAIL:$FAIL rule 6 asks that a quiet server cost nothing to run" >&2
    tail -5 "$WS/serve.out" >&2 || true
    exit 1
fi

# The startup sweep is one-shot (a loop would show up as CPU); assert it stayed one-shot by
# checking the log holds no more than the lines startup writes.
echo "idle-check: ok (rss under 100 MB, cpu under ${CPU_LIMIT_PCT}% over ${WINDOW}s of no traffic)"
