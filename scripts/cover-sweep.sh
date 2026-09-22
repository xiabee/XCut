#!/usr/bin/env sh
# Attribution-correct coverage sweep.
#
# Why this exists: `go test -coverpkg=... -coverprofile=x.out ./p1 ./p2` writes
# every test binary's blocks into the *same* file, and a block hit by one binary
# and untouched by a later one is reported with the later binary's count. The
# result is a "0.0%" list full of functions that are covered — by another
# package's tests — which is how this repository ended up chasing phantom gaps
# (`pipeline.AnalyzeProjectAsync` reads 0.0% merged and 100% when its own
# consumer, internal/api, is measured alone).
#
# So: one profile per test binary, merged by taking the maximum hit count per
# block. Nothing is averaged and nothing is dropped.
#
# Usage: sh scripts/cover-sweep.sh            # whole repo
#        sh scripts/cover-sweep.sh ./internal/api ./internal/pipeline
#
# Exit 0 only if every package's tests passed; a red package makes the coverage
# numbers meaningless, so it is reported rather than tolerated.
set -e
cd "$(dirname "$0")/.."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "== coverage sweep environment"
echo "  ffmpeg:  $(command -v ffmpeg || echo MISSING)"
echo "  python:  $(command -v python3 || command -v python || echo MISSING)"
echo "  go:      $(go version)"
echo "  note:    tests that skip for a missing tool count as NOT executed;"
echo "           read the skip line below before trusting any 0.0%."

if [ "$#" -gt 0 ]; then
    pkgs="$*"
else
    pkgs=$(go list ./internal/... ./cmd/...)
fi

failed=""
for p in $pkgs; do
    slug=$(printf '%s' "$p" | tr '/.' '__')
    if ! go test -count=1 -coverpkg=./internal/...,./cmd/... \
            -coverprofile="$tmp/$slug.out" "$p" > "$tmp/$slug.log" 2>&1; then
        failed="$failed $p"
    fi
done

skips=$(grep -ah -- "--- SKIP" "$tmp"/*.log 2>/dev/null | wc -l | tr -d ' ')
echo "== results"
echo "  packages:    $(echo "$pkgs" | wc -w | tr -d ' ')"
echo "  test skips:  $skips"
if [ -n "$failed" ]; then
    echo "  FAILED packages:$failed"
    for p in $failed; do
        slug=$(printf '%s' "$p" | tr '/.' '__')
        tail -5 "$tmp/$slug.log"
    done
    exit 1
fi

merged="$tmp/merged.out"
printf 'mode: set\n' > "$merged"
# max count per block: the first field is file:start.line.col,end.line.col,
# second is statement count, third is the hit count.
awk 'NR==1 { next }
     { key = $1 " " $2; c = $3 + 0; if (!(key in best) || c > best[key]) best[key] = c }
     END { for (k in best) printf "%s %d\n", k, best[k] }' "$tmp"/*.out >> "$merged"

go tool cover -func="$merged" | awk '$3 == "0.0%"' > "$tmp/zero.txt"
total=$(go tool cover -func="$merged" | tail -1)
echo "  $total"
echo "  functions at 0.0% after max-merge: $(wc -l < "$tmp/zero.txt" | tr -d ' ')"
sed 's|github.com/xiabee/XCut/||' "$tmp/zero.txt"
echo "== sweep done (any 0.0% above is unexecuted by every package's tests)"
