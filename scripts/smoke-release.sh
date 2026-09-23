#!/usr/bin/env sh
# Run the release artifact the way a user meets it.
#
# Why this exists: the packaging job used to do it, on a runner that no longer takes
# the work (GitHub Actions quota), so `build-release` has been producing binaries
# nothing has ever executed. A mistyped -X path, a link that succeeds but dies on
# start, or a stamping flag that quietly stops applying would each have shipped
# unnoticed — and the version line is the one artifact property a person can check
# without a test bench.
#
# Usage: scripts/smoke-release.sh <binary> <version> <commit> [workspace-dir]
# Exit 0 = every check ran and every check held. Anything else fails the build.
set -eu

BIN="${1:-}"
WANT_VERSION="${2:-}"
WANT_COMMIT="${3:-}"

if [ -z "$BIN" ] || [ ! -f "$BIN" ]; then
    echo "smoke: no artifact at '$BIN'" >&2
    exit 2
fi

# A workspace of our own, thrown away after: the point is to run the real command,
# so it needs somewhere to write that is not anyone's data.
WS="${4:-}"
if [ -z "$WS" ]; then
    WS=$(mktemp -d)
    trap 'rm -rf "$WS"' EXIT
fi
fail() { echo "smoke FAIL: $1" >&2; exit 1; }
ok()   { echo "smoke ok:   $1"; }

# 1. It starts, and it knows who it is. The commit and version are the ldflags
#    contract; "unknown" here means the stamping line in build-release broke.
VOUT=$("$BIN" version)
echo "$VOUT" | grep -q "^xcut $WANT_VERSION " || fail "the artifact reports '$VOUT', not version $WANT_VERSION"
echo "$VOUT" | grep -q "$WANT_COMMIT" || fail "the artifact does not carry commit $WANT_COMMIT: $VOUT"
echo "$VOUT" | grep -q "go1\." || fail "no Go toolchain in the version line: $VOUT"
ok "stamped binary runs: $VOUT"

# 2. The usage page, through the shipped binary rather than a test build.
HOUT=$("$BIN" -h)
echo "$HOUT" | grep -q "Usage: xcut" || fail "-h printed no usage page"
ok "usage page renders"

# 3. A bad command refuses with a nonzero status (a double-clicked exe reads the
#    exit code, and the desktop client's first move is a lookup that can miss).
if "$BIN" definitely-not-a-command >/dev/null 2>&1; then
    fail "an unknown command exited 0"
fi
ok "unknown command refused"

# 4. init creates the workspace it was pointed at, and the file it promises.
"$BIN" --workspace "$WS" init >/dev/null || fail "init exited nonzero"
[ -f "$WS/config.json" ] || fail "init wrote no config.json into $WS"
[ -f "$WS/xcut.db" ] || fail "init created no database"
ok "init lays out a workspace"

# 5. The secret handling, in the artifact a support ticket gets pasted from. The
#    value must not appear; the field must, masked (the encoder escapes the marker,
#    so the mask itself is not grepped — see docs/PROJECT_STATE.md on that trap).
SECRET="smoketoken$(printf '%032d' 7)"
if XCUT_AUTH_TOKEN="$SECRET" "$BIN" --workspace "$WS" config show | grep -q "$SECRET"; then
    fail "config show printed the bearer token"
fi
XCUT_AUTH_TOKEN="$SECRET" "$BIN" --workspace "$WS" config show | grep -q 'auth_token' \
    || fail "config show dropped the auth_token field entirely"
ok "the token is masked in the shipped binary"

echo "smoke: $BIN passed 5 checks"
