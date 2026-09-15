#!/usr/bin/env bash
# XCut mixed-API soak — the formal replacement for the retired ad-hoc soak
# (session #5 postmortem). Guarantees that made the ad-hoc script fail are
# built in: atomic instance lock (mkdir), random listen port, throwaway
# workspace, bounded waits on every request (--max-time), bounded round
# counts.
#
#   scripts/soak.sh [ROUNDS]          # default 30
#
# Each round: analyze (duplicate -> 409) -> two render triggers (one 409)
# -> timeline regenerate -> stale-revision PUT (must 409) -> matching PUT
# (must 200) -> subtitles status -> junk upload (must not import, must not
# litter imports/). Every request carries --max-time. Exit 0 = all rounds
# green. Setup additionally uploads the fixture content twice (201, same
# name — the second must land beside it, never overwrite).
set -u

ROUNDS="${1:-30}"
case "$ROUNDS" in ''|*[!0-9]*) echo "usage: soak.sh [ROUNDS]"; exit 2;; esac

REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"

# --- instance lock: mkdir is atomic; a stale lock self-heals after 30 min ---
LOCK="${TEMP:-/tmp}/xcut-soak.lock"
if ! mkdir "$LOCK" 2>/dev/null; then
    if [ -d "$LOCK" ] && [ -n "$(find "$LOCK" -maxdepth 0 -mmin +30 2>/dev/null)" ]; then
        rm -rf "$LOCK"
    fi
    if ! mkdir "$LOCK" 2>/dev/null; then
        echo "another soak instance holds $LOCK" >&2
        exit 2
    fi
fi
trap 'rm -rf "$LOCK"' EXIT

# --- build (reuse an existing binary unless absent) ---
XCUT="$REPO/.tools/xcut-soak.exe"
if [ ! -x "$XCUT" ]; then
    go build -o "$XCUT" ./cmd/xcut || exit 2
fi
command -v ffmpeg >/dev/null || { PATH="$REPO/.tools/ffmpeg/bin:$PATH"; export PATH; }

# --- environment: random port, throwaway workspace ---
PORT=$((RANDOM % 20000 + 20000))
WS="${TEMP:-/tmp}/xcut-soak-$$"
rm -rf "$WS"
mkdir -p "$WS"
export XCUT_WORKSPACE="$WS"
export XCUT_LISTEN="127.0.0.1:$PORT"
export XCUT_LOG_LEVEL="warn"
BASE="http://127.0.0.1:$PORT/api/v1"

"$XCUT" init >/dev/null 2>&1
"$XCUT" project create soak >/dev/null 2>&1
ffmpeg -y -hide_banner -loglevel error \
    -f lavfi -i "color=c=blue:size=320x240:rate=30:duration=8" \
    -f lavfi -i "sine=frequency=440:duration=8" -shortest \
    -c:v libx264 -preset ultrafast -c:a aac "$WS/fixture.mp4" || exit 2
"$XCUT" import soak "$WS/fixture.mp4" >/dev/null 2>&1
"$XCUT" timeline soak --style generic_highlight >/dev/null 2>&1


"$XCUT" serve >"$WS/serve.log" 2>&1 &
SERVE_PID=$!
cleanup() {
    kill "$SERVE_PID" >/dev/null 2>&1
    wait "$SERVE_PID" 2>/dev/null
    rm -rf "$WS" "$LOCK"
}
trap cleanup EXIT

# bounded health wait (5s)
ready=0
for _ in $(seq 1 25); do
    if curl -s --max-time 2 "$BASE/health" | grep -q '"ok":true'; then ready=1; break; fi
    sleep 0.2
done
[ "$ready" = 1 ] || { echo "serve never became healthy" >&2; exit 2; }

PROJ_ID=$(curl -s --max-time 5 "$BASE/projects" | python -c "import json,sys; d=json.load(sys.stdin); print([p['id'] for p in d['projects'] if p['name']=='soak'][0])")
[ -n "$PROJ_ID" ] || { echo "project id missing" >&2; exit 2; }

# --- content-upload setup (session #9 surface) ---
# The fixture lands under imports/<pid>/; re-uploading the same name must
# land a NEW copy (soak-up-1.mp4), never overwrite. 2 assets, 2 files.
upload_code() { # upload_code FILE NAME
    curl -s -o /dev/null -w "%{http_code}" --max-time 30 -X POST         --data-binary @"$1" "$BASE/projects/$PROJ_ID/assets/upload?filename=$2"
}
up1=$(upload_code "$WS/fixture.mp4" "soak-up.mp4")
up2=$(upload_code "$WS/fixture.mp4" "soak-up.mp4")
if [ "$up1" != "201" ] || [ "$up2" != "201" ]; then
    echo "setup upload codes: $up1 $up2 (want 201 201)" >&2
    exit 2
fi
IMPORTS_DIR="$WS/imports/$PROJ_ID"
n_files=$(ls "$IMPORTS_DIR" 2>/dev/null | wc -l)
[ "$n_files" = "2" ] || { echo "imports/ has $n_files files after duplicate-name uploads (want 2)" >&2; exit 2; }

errors=0; dup409=0; stale409=0; goodPUT=0; renderQueued=0; subsOK=0; upNoLitter=0

wait_job() { # wait_job TYPE -> prints final status; bounded 90s
    local typ="$1" n=0 st=""
    while [ $n -lt 180 ]; do
        st=$(curl -s --max-time 5 "$BASE/jobs" | python -c "
import json,sys
d=json.load(sys.stdin)
js=[j for j in d.get('jobs',[]) if j.get('project_id')=='$PROJ_ID' and j.get('type')=='$typ']
js.sort(key=lambda j: (j.get('created_at',0), j.get('id','')))
print(js[-1]['status'] if js else '')" 2>/dev/null)
        case "$st" in succeeded|failed|cancelled) echo "$st"; return 0;; esac
        n=$((n+1)); sleep 0.5
    done
    echo "timeout"
}

# POST helper: reports the HTTP code on stderr-style failures via $1 var name
post_code() { # post_code PATH BODY
    curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X POST \
        -H "Content-Type: application/json" -d "$2" "$BASE$1"
}
get_code() {
    curl -s -o /dev/null -w "%{http_code}" --max-time 15 "$BASE$1"
}

for round in $(seq 1 "$ROUNDS"); do
    err=""
    # 1. analyze: duplicate must 409
    code=$(post_code "/projects/$PROJ_ID/analyze" '{}')
    case "$code" in
        202|200)
            st=$(wait_job analyze)
            [ "$st" = "succeeded" ] || err="analyze ended $st"
            ;;
        409) dup409=$((dup409+1));;
        *)   err="analyze code $code";;
    esac

    # 2. two render triggers: exactly one accepted, one 409 — the soak's
    #    headline exclusivity guarantee, now actually asserted.
    c1=$(post_code "/projects/$PROJ_ID/render" '{}')
    c2=$(post_code "/projects/$PROJ_ID/render" '{}')
    accepted=0; conflicts=0
    for c in $c1 $c2; do
        case "$c" in 202|200) accepted=$((accepted+1)); renderQueued=$((renderQueued+1));; 409) conflicts=$((conflicts+1)); dup409=$((dup409+1));; *) err="$err render-pair $c";; esac
    done
    if [ "$accepted" -gt 1 ] || [ "$conflicts" -lt 1 ]; then
        err="$err render-dedup broken (accepted=$accepted conflicts=$conflicts)"
    fi
    if [ "$accepted" -ge 1 ]; then
        st=$(wait_job render)
        [ "$st" = "succeeded" ] || err="$err render ended $st"
    fi

    # 3. regenerate timeline (exclusive type, waits its turn)
    code=$(post_code "/projects/$PROJ_ID/timeline" '{"style":"generic_highlight"}')
    case "$code" in
        202|200)
            st=$(wait_job timeline)
            [ "$st" = "succeeded" ] || err="$err timeline ended $st"
            ;;
        *) err="$err timeline code $code";;
    esac

    # 4. stale revision PUT must 409; matching PUT must 200
    doc=$(curl -s --max-time 10 "$BASE/projects/$PROJ_ID/timeline")
    echo "$doc" | python -c "
import json,sys
d=json.load(sys.stdin)
d['timeline']['revision'] -= 1
d['timeline']['tracks'][0]['clips'] = d['timeline']['tracks'][0]['clips']
print(json.dumps(d['timeline']))" > "$WS/stale.json"
    code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X PUT \
        -H "Content-Type: application/json" --data-binary @"$WS/stale.json" \
        "$BASE/projects/$PROJ_ID/timeline")
    if [ "$code" = "409" ]; then stale409=$((stale409+1)); else err="$err stale-PUT $code (want 409)"; fi

    cur_rev=$(echo "$doc" | python -c "import json,sys; print(json.load(sys.stdin)['timeline']['revision'])")
    echo "$doc" | python -c "
import json,sys
d=json.load(sys.stdin)
print(json.dumps(d['timeline']))" > "$WS/good.json"
    code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X PUT \
        -H "Content-Type: application/json" --data-binary @"$WS/good.json" \
        "$BASE/projects/$PROJ_ID/timeline")
    if [ "$code" = "200" ]; then goodPUT=$((goodPUT+1)); else err="$err good-PUT $code (want 200)"; fi

    # 5. subtitles status answers 200 either way
    code=$(get_code "/projects/$PROJ_ID/subtitles")
    if [ "$code" = "200" ]; then subsOK=$((subsOK+1)); else err="$err subs-status $code"; fi

    # 6. junk upload: the probe refuses the content and the copy is cleaned
    #    up — imports/ must still hold exactly the 2 setup files.
    code=$(post_code "/projects/$PROJ_ID/assets/upload?filename=junk.mp4" "definitely not video")
    if [ "$code" = "201" ]; then
        err="$err junk-upload imported (201)"
    fi
    n_files=$(ls "$IMPORTS_DIR" 2>/dev/null | wc -l)
    if [ "$n_files" = "2" ]; then upNoLitter=$((upNoLitter+1)); else err="$err upload-litter ($n_files files)"; fi

    if [ -n "$err" ]; then
        errors=$((errors+1))
        echo "round $round ERROR:$err"
    fi
done

echo "soak: $ROUNDS rounds, errors=$errors dup409=$dup409 stale409=$stale409 goodPUT=$goodPUT renderQueued=$renderQueued subsOK=$subsOK upNoLitter=$upNoLitter"
[ "$errors" -eq 0 ]
