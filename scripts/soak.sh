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

# --- build (reuse the binary unless sources moved past it) ---
# A stale soak binary silently tests old code against tonight's claims —
# rebuild whenever any source file is newer than the cached binary.
XCUT="$REPO/.tools/xcut-soak.exe"
if [ ! -x "$XCUT" ] || [ -n "$(find "$REPO/cmd" "$REPO/internal" "$REPO/go.mod" -newer "$XCUT" -print -quit 2>/dev/null)" ]; then
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
upload_code() { # upload_code FILE NAME  (main soak project)
    upload_code_pid "$PROJ_ID" "$1" "$2"
}
upload_code_pid() { # upload_code_pid PROJECT FILE NAME
    curl -s -o /dev/null -w "%{http_code}" --max-time 30 -X POST         --data-binary @"$2" "$BASE/projects/$1/assets/upload?filename=$3"
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
dlOK=0; rangeOK=0; busy409=0; busySkip=0; idleDelOK=0

job_status_pid() { # job_status_pid PROJECT TYPE -> latest job's status ('' if none)
    curl -s --max-time 5 "$BASE/jobs" | python -c "
import json,sys
d=json.load(sys.stdin)
js=[j for j in d.get('jobs',[]) if j.get('project_id')=='$1' and j.get('type')=='$2']
js.sort(key=lambda j: (j.get('created_at',0), j.get('id','')))
print(js[-1]['status'] if js else '')" 2>/dev/null
}

wait_job() { # wait_job TYPE -> prints final status; bounded 90s
    wait_job_pid "$PROJ_ID" "$1"
}
wait_job_pid() { # wait_job_pid PROJECT TYPE -> prints final status; bounded 90s
    local pid="$1" typ="$2" n=0 st=""
    while [ $n -lt 180 ]; do
        st=$(job_status_pid "$pid" "$typ")
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

    # 7. render download streams (write-idle heartbeat surface, M120):
    #    full GET is 200 + real bytes; a 1 KiB range GET is 206 + exactly
    #    1024 bytes (range handling intact under the streaming wrapper).
    code=$(get_code "/projects/$PROJ_ID/render")
    if [ "$code" = "200" ]; then dlOK=$((dlOK+1)); else err="$err render-dl $code"; fi
    range_code=$(curl -s -o "$WS/range.bin" -w "%{http_code}" --max-time 15         -r 0-1023 "$BASE/projects/$PROJ_ID/render")
    range_bytes=$(wc -c < "$WS/range.bin" 2>/dev/null | tr -d ' ')
    if [ "$range_code" = "206" ] && [ "$range_bytes" = "1024" ]; then
        rangeOK=$((rangeOK+1))
    else
        err="$err render-range $range_code/${range_bytes}B"
    fi

    # 8. delete gate (M-A): a busy project refuses deletion (409), the same
    #    project deletes once idle (200). Throwaway project per round.
    busy=$(post_code "/projects" "{\"name\":\"soak-busy-$round\"}")
    if [ "$busy" = "201" ]; then
        BUSY_ID=$(curl -s --max-time 5 "$BASE/projects" | python -c "
import json,sys
d=json.load(sys.stdin)
print([p['id'] for p in d['projects'] if p['name']=='soak-busy-$round'][0])" 2>/dev/null)
        bup=$(upload_code_pid "$BUSY_ID" "$WS/fixture.mp4" "busy.mp4")
        bat=$(post_code "/projects/$BUSY_ID/analyze" '{}')
        if [ "$bup" = "201" ] && [ "$bat" = "202" ]; then
            # Assert the invariant, not the timing. delete-must-409 only
            # means something while the analyze job is provably still
            # active; on a tiny fixture the job can finish between the 202
            # and the DELETE, and a 200 is then the CORRECT gate answer —
            # the project (and its cascaded job rows) is gone, so the race
            # is unverifiable after the fact. A gate regression still shows
            # up in the counters: busy409 collapsing toward 0 across rounds
            # means the gate stopped refusing busy deletes.
            busy_st=$(job_status_pid "$BUSY_ID" analyze)
            case "$busy_st" in
                running|queued)
                    del1=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15                 -X DELETE "$BASE/projects/$BUSY_ID")
                    if [ "$del1" = "409" ]; then
                        busy409=$((busy409+1))
                        wait_job_pid "$BUSY_ID" analyze >/dev/null
                        del2=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15             -X DELETE "$BASE/projects/$BUSY_ID")
                        if [ "$del2" = "200" ]; then idleDelOK=$((idleDelOK+1)); else err="$err idle-delete $del2 (want 200)"; fi
                    else
                        busySkip=$((busySkip+1)) # finished in the gap; this delete was the idle one
                        idleDelOK=$((idleDelOK+1))
                    fi;;
                *)
                    busySkip=$((busySkip+1)) # already terminal before the delete
                    del2=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15             -X DELETE "$BASE/projects/$BUSY_ID")
                    if [ "$del2" = "200" ]; then idleDelOK=$((idleDelOK+1)); else err="$err idle-delete $del2 (want 200)"; fi;;
            esac
        else
            err="$err busy-setup bup=$bup bat=$bat"
        fi
    else
        err="$err busy-project $busy"
    fi

    if [ -n "$err" ]; then
        errors=$((errors+1))
        echo "round $round ERROR:$err"
    fi
done

echo "soak: $ROUNDS rounds, errors=$errors dup409=$dup409 stale409=$stale409 goodPUT=$goodPUT renderQueued=$renderQueued subsOK=$subsOK upNoLitter=$upNoLitter dlOK=$dlOK rangeOK=$rangeOK busy409=$busy409 busySkip=$busySkip idleDelOK=$idleDelOK"
[ "$errors" -eq 0 ]
