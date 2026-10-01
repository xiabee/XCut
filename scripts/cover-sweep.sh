#!/usr/bin/env sh
# Attribution-correct coverage sweep.
#
# Why this exists: `go test -coverpkg=... -coverprofile=x.out ./p1 ./p2` writes
# every test binary's blocks into one file, and a block one binary hit and a
# later binary did not is reported with the later count. The 0% list that
# produces is therefore full of functions that *are* covered — by another
# package's tests. Not theoretical: the sweep at `5a8a4ff` put
# `pipeline.AnalyzeProjectAsync` on its list and the ledger carried it as an open
# gap, while profiling its own consumer (`internal/api`, `TestAsyncJobFlow`
# posting `/analyze`) measures it at 100%.
#
# So: one profile per test binary (with -v, so skips are visible), merged by
# taking the maximum hit count per block. Nothing is averaged, nothing dropped.
#
# Usage: sh scripts/cover-sweep.sh
#        sh scripts/cover-sweep.sh ./internal/api ./internal/pipeline
#
# Exit 0 only when every package's tests passed and the merge parsed: a red
# package or an empty merge would otherwise read as "no gaps left".
set -e
cd "$(dirname "$0")/.."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "== coverage sweep environment"
echo "  ffmpeg:  $(command -v ffmpeg || echo MISSING)"
echo "  python:  $(command -v python3 || command -v python || echo MISSING)"
echo "  go:      $(go version)"
echo "  note:    a test that skips for a missing tool counts as NOT executed,"
echo "           so read the skip line before trusting any 0.0% below."

if [ "$#" -gt 0 ]; then
    pkgs="$*"
else
    pkgs=$(go list ./internal/... ./cmd/...)
fi

failed=""
for p in $pkgs; do
    slug=$(printf '%s' "$p" | tr '/.' '__')
    if ! go test -count=1 -v -coverpkg=./internal/...,./cmd/... \
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
# Each per-package profile starts with its own `mode:` line: skip all of them,
# not just the first file's, or the header lands in the data as a bogus block.
awk '/^mode:/ { next }
     NF >= 3 { key = $1 " " $2; c = $3 + 0
               if (!(key in best) || c > best[key]) best[key] = c }
     END { for (k in best) printf "%s %d\n", k, best[k] }' "$tmp"/*.out >> "$merged"

# An empty merge or a profile the parser rejects looks identical to "nothing left
# untested" from here, so both are hard failures.
blocks=$(grep -c ':' "$merged" || true)
if [ "$blocks" -lt 100 ]; then
    echo "  ABORT: merged profile holds only $blocks blocks — the runs or the merge failed"
    exit 2
fi
if ! go tool cover -func="$merged" > "$tmp/func.txt" 2>"$tmp/cover.err"; then
    echo "  ABORT: go tool cover rejected the merged profile"
    head -3 "$tmp/cover.err"
    exit 2
fi
awk '$3 == "0.0%"' "$tmp/func.txt" > "$tmp/zero.txt"
echo "  merged blocks: $blocks"
tail -1 "$tmp/func.txt"
echo "  functions at 0.0% after max-merge: $(wc -l < "$tmp/zero.txt" | tr -d ' ')"
sed 's|github.com/xiabee/XCut/||' "$tmp/zero.txt"
echo "== sweep done (every 0.0% above is unexecuted by all $(echo "$pkgs" | wc -w | tr -d ' ') test binaries)"
