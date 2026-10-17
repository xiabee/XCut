#!/usr/bin/env sh
# The Linux sandbox evidence leg (D20): the media sandbox tests — the systemd
# scope half and the unshare network half — run against a real manager in a
# throwaway container, so the claims in the posture string are measured, not
# assumed. A Windows or macOS dev host cannot create user namespaces, which is
# exactly why this script exists next to test-docker.sh (D11's pattern).
#
# The container boots systemd as PID 1 (privileged: the manager needs its
# cgroup and mount toys) with python3 installed for the kernel-boundary
# probes. Tests that need cgroup v2 still skip by name on a v1 host — the
# network-half tests have no cgroup dependency and are the point here.
#
# Usage: scripts/sandbox-docker.sh [image]     (default: golang:1.25-bookworm)
set -e
cd "$(dirname "$0")/.."

image=${1:-golang:1.25-bookworm}
cid=xcut-sandbox-$$

cleanup() { docker rm -f "$cid" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

# Git Bash mangles POSIX-looking paths in -v args; pwd -W yields a
# Windows-style path that docker accepts (plain pwd on real Linux).
MSYS_NO_PATHCONV=1 docker run -d --name "$cid" \
    --privileged \
    --tmpfs /tmp:exec --tmpfs /run --tmpfs /run/lock \
    -v "$(pwd -W 2>/dev/null || pwd):/src:ro" \
    -w /src \
    -e "GOCACHE=/go-cache" -v xcut-go-cache:/go-cache \
    -e "GOMODCACHE=/go-mod" -v xcut-go-mod:/go-mod \
    "$image" \
    sh -c 'apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq systemd python3 ffmpeg >/dev/null 2>&1; exec /lib/systemd/systemd' \
    >/dev/null

# Wait for the manager to answer — the container installs systemd, python3 and
# ffmpeg before it boots, and a cold apt cache with the ffmpeg dependency set
# can outlast the 8 minutes the original systemd-only budget allowed (measured:
# the first ffmpeg-carrying run died at exactly the old budget with a silent
# apt still in flight); the loop gives that 20 minutes before declaring the leg
# dead.
i=0
until docker exec "$cid" systemctl is-system-running >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -gt 600 ]; then
        echo "sandbox-docker: the container manager never came up" >&2
        docker logs "$cid" 2>&1 | tail -20 >&2
        exit 2
    fi
    sleep 2
done

docker exec "$cid" systemctl is-system-running || true
docker exec -e XNIGHTOPS_CI=1 "$cid" sh -c 'cd /src && go test ./internal/media/ -count=1 -v -run "TestScope|TestCap|TestWrap|TestSandbox|TestEnforcement|TestNet|TestBoth|TestUnshare|TestNoCap|TestMissing|TestCancel"'
