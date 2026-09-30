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
# litter imports/) -> player-spot surface (seed/read/refuse/clear/404) -> export tap (409 at the door beside a provably active
# render, 409 for a second tap beside a provably active child render, solo
# tap queues and lands a reel). Every request carries --max-time. Exit 0 =
# all rounds green. Setup additionally uploads the fixture content twice
# (201, same name — the second must land beside it, never overwrite).
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
# The export scenario needs a render window wide enough for the door and
# exclusivity probes to catch a job provably running — the 8s fixture renders
# in well under a second, and even 30s loses the race to the status query's
# own python cold start (~0.5-1s), which leaves the 409 arms mostly blind
# (measured: expDoor 0/5, expDup 1/4 at 30s). 120s at 320x240 ultrafast
# renders in ~6-9s: generated once here, uploaded per round.
ffmpeg -y -hide_banner -loglevel error \
    -f lavfi -i "color=c=green:size=320x240:rate=30:duration=120" \
    -f lavfi -i "sine=frequency=550:duration=120" -shortest \
    -c:v libx264 -preset ultrafast -c:a aac "$WS/exp-fixture.mp4" || exit 2
"$XCUT" import soak "$WS/fixture.mp4" >/dev/null 2>&1
"$XCUT" timeline soak --style generic_highlight >/dev/null 2>&1


"$XCUT" serve >"$WS/serve.log" 2>&1 &
SERVE_PID=$!
cleanup() {
    kill "$SERVE_PID" >/dev/null 2>&1
    wait "$SERVE_PID" 2>/dev/null
    # Keep the crime scene on failure: the workspace holds serve.log and the
    # jobs DB — the only evidence of WHY a job failed. Destroying it on a red
    # run makes every failure undissectable (the "keep logs first" lesson).
    # ${errors:-1}: an early exit before the counters exist is an unknown
    # state — keep the evidence rather than assume green.
    if [ "${errors:-1}" -gt 0 ]; then
        echo "soak: errors>0 — workspace kept for triage: $WS"
    else
        rm -rf "$WS"
    fi
    rm -rf "$LOCK"
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
expDoor=0; expDoorSkip=0; expDoorLate=0; expQueued=0; expDup=0; expDupSkip=0; expDupLate=0; expDone=0
spotOK=0
carryOK=0

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
job_status_by_id() { # job_status_by_id JOBID -> status ('' if gone)
    curl -s --max-time 5 "$BASE/jobs" | python -c "
import json,sys
d=json.load(sys.stdin)
js=[j for j in d.get('jobs',[]) if j.get('id')=='$1']
print(js[0]['status'] if js else '')" 2>/dev/null
}
latest_render_id() { # latest_render_id PROJECT -> id of the newest render row ('' if none)
    curl -s --max-time 5 "$BASE/jobs" | python -c "
import json,sys
d=json.load(sys.stdin)
js=[j for j in d.get('jobs',[]) if j.get('project_id')=='$1' and j.get('type')=='render']
js.sort(key=lambda j: (j.get('created_at',0), j.get('id','')))
print(js[-1]['id'] if js else '')" 2>/dev/null
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

    # 5b. player-spot surface: seed -> read back -> a bad rect is refused ->
    #     clear -> second clear 404. The person filter's API joins the
    #     standing tripwire with the rest of the write surfaces; a redraw
    #     must leave a bins-less spot (re-measure, never replay).
    spotA=$(curl -s --max-time 10 "$BASE/projects/$PROJ_ID" | python -c "import json,sys; d=json.load(sys.stdin); print(d['assets'][0]['id'])")
    spot_put=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X PUT         -H "Content-Type: application/json" -d '{"rect":[0.3,0.3,0.2,0.2],"at":7}'         "$BASE/projects/$PROJ_ID/assets/$spotA/player-spot")
    spot_get=$(curl -s --max-time 10 "$BASE/projects/$PROJ_ID/assets/$spotA/player-spot" | python -c "import json,sys; d=json.load(sys.stdin); s=d['spot']; print('ok' if s and s['rect'][0]==0.3 and s['at']==7 and not s.get('bins') else 'wrong')")
    spot_bad=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X PUT         -H "Content-Type: application/json" -d '{"rect":[0.9,0.9,0.5,0.5],"at":0}'         "$BASE/projects/$PROJ_ID/assets/$spotA/player-spot")
    spot_del=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X DELETE         "$BASE/projects/$PROJ_ID/assets/$spotA/player-spot")
    spot_del2=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X DELETE         "$BASE/projects/$PROJ_ID/assets/$spotA/player-spot")
    if [ "$spot_put" = "200" ] && [ "$spot_get" = "ok" ] && [ "$spot_bad" = "400" ]        && [ "$spot_del" = "200" ] && [ "$spot_del2" = "404" ]; then
        spotOK=$((spotOK+1))
    else
        err="$err spot-arm put=$spot_put get=$spot_get bad=$spot_bad del=$spot_del del2=$spot_del2"
    fi

    # 5c. motion-carry surface (session #27): the picker plans a drift (needs
    #     no region), a region-less roi plan is refused, a wild zoom is
    #     refused, and a pick saved onto the lead clip survives the next
    #     regeneration on the same source window (this style frames nothing,
    #     so the carry is the only way the plan comes back).
    mA=$(curl -s --max-time 10 "$BASE/projects/$PROJ_ID" | python -c "import json,sys; d=json.load(sys.stdin); print(d['assets'][0]['id'])")
    plan=$(curl -s --max-time 10 -X POST -H "Content-Type: application/json" \
        -d "{\"mode\":\"drift\",\"asset\":\"$mA\",\"ordinal\":0}" \
        "$BASE/projects/$PROJ_ID/motion/plan")
    plan_ok=$(printf '%s' "$plan" | python -c "import json,sys; d=json.load(sys.stdin); m=d.get('motion') or {}; print('ok' if m.get('zoom') and m.get('from') and m.get('to') else 'wrong')" 2>/dev/null)
    plan_roi=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 -X POST \
        -H "Content-Type: application/json" -d "{\"mode\":\"roi\",\"asset\":\"$mA\"}" \
        "$BASE/projects/$PROJ_ID/motion/plan")
    plan_zoom=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 -X POST \
        -H "Content-Type: application/json" -d "{\"mode\":\"drift\",\"asset\":\"$mA\",\"zoom\":5}" \
        "$BASE/projects/$PROJ_ID/motion/plan")
    pick_zoom=$(printf '%s' "$plan" | python -c "import json,sys; print(json.load(sys.stdin)['motion']['zoom'])" 2>/dev/null)
    curl -s --max-time 10 "$BASE/projects/$PROJ_ID/timeline" | MOTION_PLAN="$plan" WS_OUT="$WS/picked.json" python -c "
import json,os,sys
d=json.load(sys.stdin)['timeline']
p=json.loads(os.environ['MOTION_PLAN'])
c=d['tracks'][0]['clips'][0]
c['motion']=p['motion']
c.setdefault('metadata',{})['framing']=p['framing']
open(os.environ['WS_OUT'],'w').write(json.dumps(d))
print(c['source_start'], c['source_end'])" > "$WS/pickwin.txt" 2>/dev/null
    pick_start=$(cut -d' ' -f1 "$WS/pickwin.txt" 2>/dev/null)
    pick_end=$(cut -d' ' -f2 "$WS/pickwin.txt" 2>/dev/null)
    pick_put=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X PUT \
        -H "Content-Type: application/json" --data-binary @"$WS/picked.json" \
        "$BASE/projects/$PROJ_ID/timeline")
    mcode=$(post_code "/projects/$PROJ_ID/timeline" '{"style":"generic_highlight"}')
    mregen=bad
    if [ "$mcode" = "202" ] || [ "$mcode" = "200" ]; then
        mst=$(wait_job timeline)
        [ "$mst" = "succeeded" ] && mregen=ok
    fi
    carry_get=$(curl -s --max-time 10 "$BASE/projects/$PROJ_ID/timeline" \
        | PICK_START="$pick_start" PICK_END="$pick_end" PICK_ZOOM="$pick_zoom" python -c "
import json,os,sys
d=json.load(sys.stdin)['timeline']
start,end,zoom=float(os.environ['PICK_START']),float(os.environ['PICK_END']),float(os.environ['PICK_ZOOM'])
hit=[c for c in d['tracks'][0]['clips'] if abs(c['source_start']-start)<1e-6 and abs(c['source_end']-end)<1e-6]
ok=bool(hit) and bool(hit[0].get('motion')) and abs(hit[0]['motion']['zoom']-zoom)<1e-9 and hit[0].get('metadata',{}).get('framing')=='drift'
print('ok' if ok else 'wrong')" 2>/dev/null)
    if [ "$plan_ok" = "ok" ] && [ "$plan_roi" = "400" ] && [ "$plan_zoom" = "400" ] \
        && [ "$pick_put" = "200" ] && [ "$mregen" = "ok" ] && [ "$carry_get" = "ok" ]; then
        carryOK=$((carryOK+1))
    else
        err="$err carry-arm plan=$plan_ok roi=$plan_roi zoom=$plan_zoom put=$pick_put regen=$mregen get=$carry_get"
    fi

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

    # 9. export tap (the one-tap family, 6498b14): the door precheck refuses
    #    while a render is provably active, a second export refuses while the
    #    first's child render runs (exclusive set), and a solo export queues
    #    and lands a reel. Throwaway project per round, deleted when idle.
    #    Assert the invariant, not the timing — the tiny fixture can finish a
    #    render between the 202 and the probe, and both answers are then
    #    correct; a regression still shows in the counters (expDoor/expDup
    #    collapsing to 0 while expDoorSkip/expDupSkip grow means the gate
    #    stopped refusing).
    exp=$(post_code "/projects" "{\"name\":\"soak-exp-$round\"}")
    if [ "$exp" = "201" ]; then
        EXP_ID=$(curl -s --max-time 5 "$BASE/projects" | python -c "
import json,sys
d=json.load(sys.stdin)
print([p['id'] for p in d['projects'] if p['name']=='soak-exp-$round'][0])" 2>/dev/null)
        eup=$(upload_code_pid "$EXP_ID" "$WS/exp-fixture.mp4" "exp.mp4")
        eat=$(post_code "/projects/$EXP_ID/analyze" '{}')
        if [ "$eup" = "201" ] && [ "$eat" = "202" ]; then
            wait_job_pid "$EXP_ID" analyze >/dev/null
            # a render needs a timeline (it fails not_found without one —
            # measured, and a fast-failing render blinds the door probe);
            # build it explicitly, then the render has its full window
            etl=$(post_code "/projects/$EXP_ID/timeline" '{"style":"generic_highlight"}')
            case "$etl" in
                202|200)
                    tlst=$(wait_job_pid "$EXP_ID" timeline)
                    [ "$tlst" = "succeeded" ] || err="$err exp-timeline $tlst";;
                *) err="$err exp-timeline-code $etl";;
            esac
            er_resp=$(curl -s -w "|%{http_code}" --max-time 15 -X POST \
                -H "Content-Type: application/json" -d '{}' \
                "$BASE/projects/$EXP_ID/render")
            er="${er_resp##*|}"
            er_id=$(echo "${er_resp%|*}" | python -c "import json,sys; print(json.load(sys.stdin).get('job_id',''))" 2>/dev/null)
            case "$er" in
                202|200)
                    rst=$(job_status_pid "$EXP_ID" render)
                    if [ "$rst" = "running" ] || [ "$rst" = "queued" ]; then
                        ex1=$(post_code "/projects/$EXP_ID/export" '{}')
                        if [ "$ex1" = "409" ]; then
                            expDoor=$((expDoor+1))
                        elif [ "$ex1" = "202" ] || [ "$ex1" = "200" ]; then
                            # A 202 here is only wrong if THE MANUAL RENDER
                            # was live when the tap was processed. The
                            # probe's own ~1s python gap is where a render
                            # can legitimately finish (2/58 at the 120s
                            # fixture) — and worse for a naive re-probe: the
                            # accepted tap can queue its OWN child render
                            # inside that gap, so "latest render still
                            # running" can be the child, not the row the
                            # door checked (fired once in 100 rounds, round
                            # 65). Re-probe the manual row BY ID: still live
                            # = real precheck race; terminal = the gap
                            # answer, counted, no error.
                            rst_now=$(job_status_by_id "$er_id")
                            if [ "$rst_now" = "running" ] || [ "$rst_now" = "queued" ]; then
                                err="$err export-door-race 202 accepted, render $er_id still $rst_now"
                            else
                                expDoorLate=$((expDoorLate+1))
                            fi
                        else
                            err="$err export-door $ex1 (render $rst)"
                        fi
                    else
                        expDoorSkip=$((expDoorSkip+1)) # render terminal in the gap; nothing to refuse
                    fi
                    wait_job_pid "$EXP_ID" render >/dev/null;;
                *) err="$err export-render $er";;
            esac
            # solo export with no render in flight: must queue
            exs=$(post_code "/projects/$EXP_ID/export" '{}')
            case "$exs" in
                202|200)
                    expQueued=$((expQueued+1))
                    # The child render row is the tap's own; while it is
                    # provably active, a second export must refuse — same
                    # honest-202 shape as the door probe above. The row is
                    # named BY ID once it appears: the 2026-09-29 run's round
                    # 8 fired this arm while both probes answered "latest
                    # render" — a position, not a row (session #24's door-arm
                    # lesson applied here too; the accepted tap can queue its
                    # own child, and a fresh row changes which row "latest"
                    # names between the two probes).
                    cst="" ; cid=""
                    for n in 1 2 3 4 5 6 7 8 9 10; do
                        cid=$(latest_render_id "$EXP_ID")
                        cst=$(job_status_by_id "$cid")
                        case "$cst" in running|queued) break;; esac
                        sleep 0.3
                    done
                    if [ "$cst" = "running" ] || [ "$cst" = "queued" ]; then
                        ex2=$(post_code "/projects/$EXP_ID/export" '{}')
                        if [ "$ex2" = "409" ]; then
                            expDup=$((expDup+1))
                        elif [ "$ex2" = "202" ] || [ "$ex2" = "200" ]; then
                            cst_now=$(job_status_by_id "$cid")
                            if [ "$cst_now" = "running" ] || [ "$cst_now" = "queued" ]; then
                                err="$err export-dup-race 202 accepted, child render $cid still $cst_now (tap1=$exs)"
                            else
                                expDupLate=$((expDupLate+1))
                            fi
                        else
                            err="$err export-dup $ex2 (child render $cid=$cst)"
                        fi
                    else
                        expDupSkip=$((expDupSkip+1)) # child finished in the gap
                    fi
                    exst=$(wait_job_pid "$EXP_ID" export)
                    [ "$exst" = "succeeded" ] || err="$err export-parent $exst"
                    rst2=$(wait_job_pid "$EXP_ID" render)
                    [ "$rst2" = "succeeded" ] || err="$err export-child-render $rst2"
                    expDone=$((expDone+1));;
                *) err="$err export-solo $exs (want 202)";;
            esac
            exdel=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 -X DELETE "$BASE/projects/$EXP_ID")
            [ "$exdel" = "200" ] || err="$err exp-delete $exdel (want 200)"
        else
            err="$err exp-setup eup=$eup eat=$eat"
        fi
    else
        err="$err exp-project $exp"
    fi

    if [ -n "$err" ]; then
        errors=$((errors+1))
        echo "round $round ERROR:$err"
    fi
done

echo "soak: $ROUNDS rounds, errors=$errors dup409=$dup409 stale409=$stale409 goodPUT=$goodPUT renderQueued=$renderQueued subsOK=$subsOK upNoLitter=$upNoLitter dlOK=$dlOK rangeOK=$rangeOK busy409=$busy409 busySkip=$busySkip idleDelOK=$idleDelOK expDoor=$expDoor/$expDoorSkip/$expDoorLate expQueued=$expQueued expDup=$expDup/$expDupSkip/$expDupLate expDone=$expDone spotOK=$spotOK carryOK=$carryOK"
[ "$errors" -eq 0 ]
