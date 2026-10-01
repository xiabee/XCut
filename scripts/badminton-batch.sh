#!/bin/bash
# Batch: one highlight reel per date directory, shortest footage first.
set -u
export XCUT_WORKSPACE=/d/Projects/XCut/.badminton-ws
XCUT=/d/Projects/XCut/xcut.exe
SRC=/e/TEMP/Badminton
OUT=/e/TEMP/Badminton/output
LOG=/d/Projects/XCut/.badminton-batch.log
mkdir -p "$OUT"

# date-dir : total footage seconds (computed once)
declare -a DATES=(
  "2024.08.18:760"
  "2024.06.27:1560"
  "2024.08.07:1880"
  "2024.08.09:1850"
  "2024.07.21:2320"
  "2024.06.26:3730"
  "2024.06.28:4220"
)

for entry in "${DATES[@]}"; do
  date="${entry%%:*}"
  proj="session-$(echo "$date" | tr '.' '-')"
  echo "[$(date +%H:%M:%S)] === $date (project $proj) ===" >> "$LOG"

  # Skip dates already delivered.
  if [ -f "$OUT/$date-highlights.mp4" ]; then
    echo "  already done, skip" >> "$LOG"
    continue
  fi

  # Build the file list (MP4/MOV >= 5s each).
  files=()
  for f in "$SRC/$date"/*.MP4 "$SRC/$date"/*.MOV; do
    [ -f "$f" ] || continue
    d=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$f" 2>/dev/null || echo 0)
    if awk "BEGIN{exit !($d >= 5)}"; then files+=("$f"); fi
  done
  if [ ${#files[@]} -eq 0 ]; then
    echo "  no usable files" >> "$LOG"
    continue
  fi

  if ! "$XCUT" project create "$proj" >> "$LOG" 2>&1; then
    echo "  project create failed (may exist)" >> "$LOG"
  fi
  if ! "$XCUT" import "$proj" "${files[@]}" >> "$LOG" 2>&1; then
    echo "  IMPORT FAILED" >> "$LOG"; continue
  fi
  if ! "$XCUT" analyze "$proj" >> "$LOG" 2>&1; then
    echo "  ANALYZE FAILED" >> "$LOG"; continue
  fi
  if ! "$XCUT" timeline "$proj" --style badminton_highlight >> "$LOG" 2>&1; then
    echo "  TIMELINE FAILED" >> "$LOG"; continue
  fi
  if "$XCUT" render "$proj" --out "$OUT/$date-highlights.mp4" >> "$LOG" 2>&1; then
    echo "  DONE -> $OUT/$date-highlights.mp4" >> "$LOG"
  else
    echo "  RENDER FAILED" >> "$LOG"
  fi
done
echo "[$(date +%H:%M:%S)] batch complete" >> "$LOG"
