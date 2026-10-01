#!/bin/bash
# v2 re-edit: regenerate timeline + render with the v2 algorithm for every
# session project, overwriting the previous reels. Times each date for the
# resource ledger.
set -u
export XCUT_WORKSPACE=/d/Projects/XCut/.badminton-ws
XCUT=/d/Projects/XCut/xcut-tuned.exe
OUT=/e/TEMP/Badminton/output
LOG=/d/Projects/XCut/.badminton-v2.log

declare -a JOBS=(
  "session-2024-08-18:2024.08.18"
  "session-2024-06-27:2024.06.27"
  "session-2024-08-07:2024.08.07"
  "session-2024-08-09:2024.08.09"
  "session-2024-07-21:2024.07.21"
  "session-2024-06-26:2024.06.26"
  "session-0504:2025.05.04"
)

for entry in "${JOBS[@]}"; do
  proj="${entry%%:*}"
  stamp="${entry#*:}"
  echo "[$(date +%H:%M:%S)] === $proj ===" >> "$LOG"

  t0=$(date +%s)
  if ! "$XCUT" timeline "$proj" --style badminton_highlight >> "$LOG" 2>&1; then
    echo "  TIMELINE FAILED" >> "$LOG"; continue
  fi
  t1=$(date +%s)
  if "$XCUT" render "$proj" --out "$OUT/$stamp-highlights.mp4" >> "$LOG" 2>&1; then
    t2=$(date +%s)
    echo "  DONE -> $OUT/$stamp-highlights.mp4 (timeline $((t1-t0))s + render $((t2-t1))s)" >> "$LOG"
  else
    echo "  RENDER FAILED" >> "$LOG"
  fi
done
echo "[$(date +%H:%M:%S)] v2 re-edit complete" >> "$LOG"
