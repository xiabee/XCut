# Operations

Runbook for running `xcut serve` — locally and, since v0.1.8-alpha, for
somebody else over a network. Every number below comes from `Defaults` in
`internal/config/config.go` or from a command that was actually run; recipes
marked **verified** were driven end to end on real machines.

## Data directory

`xcut init` creates, and every command expects, this layout under the
workspace root (default `~/.xcut`; override with the **global** flag
`--workspace <dir>` or `XCUT_WORKSPACE` — global flags go *before* the
subcommand: `xcut --workspace /data/xcut doctor`):

```
<root>/
  config.json        defaults written by `xcut init` (0644; the token is not written)
  xcut.db            SQLite state: projects, assets, jobs, analysis cache index
  projects/<id>/     timeline.json and whatever you render into the project dir
  imports/<id>/      media copied in through the web upload (originals untouched)
  cache/             analysis artifacts + proxy videos, budgeted by resource.max_cache_gb
  temp/              per-job scratch, disposable, budgeted by resource.max_temp_gb
  logs/serve.log     rotated at log.max_size_mb (50) x log.max_files (3)
```

Source files you imported by path are never moved or copied — the DB only
records where they were. Back up `config.json` + `xcut.db` + `projects/` +
`imports/` if you want a restorable edit; `cache/` and `temp/` are pure
derived state and can be deleted at any time (`xcut cleanup` does the
budgeted version of that).

## What the server binds, and why

Default posture: `127.0.0.1:8619`, loopback only, no token, nothing on the
network. That is the posture the desktop client and a double-clicked exe are
built for, and it stays that way until you change two independent settings.

Opening it to another machine requires **both**:

1. `server.listen_remote = true` in the workspace config, and
2. `server.auth_token` (or `XCUT_AUTH_TOKEN`) of at least 24 characters,
   with no whitespace inside.

`config.Resolve` refuses the pair apart, and `serveAddr` re-checks it one step
before `net.Listen`, so a socket cannot open on the network without the other.
The startup log line tells you which posture you got:

```
xcut serving http://172.30.240.1:8675 (remote bind, authentication required)
msg="server started" addr=172.30.240.1:8675 version=v0.1.8-alpha auth=true
```

`logs/serve.log` keeps recording for as long as the server is up, which is the
point of it: a request the API refused (`msg="request failed"` with the method,
path and cause), job warnings, and the drain lines are all in there. The same
lines go to stderr, so a serve started from a terminal shows them live; the
file is what is left when it does not.

There is no token generator in the CLI on purpose (a token belongs in a
config file or an environment variable, never in argv, which is why there is
no `--token` flag either). Make one with either of these (both run today):

```sh
openssl rand -hex 24                                   # 48 hex chars
```
```powershell
$b = New-Object byte[] 24                               # PowerShell 5.1+
(New-Object Security.Cryptography.RNGCryptoServiceProvider).GetBytes($b)
($b | ForEach-Object { $_.ToString('x2') }) -join ''
```

## Recipe A — SSH tunnel, no token **verified 2026-09-20**

The server keeps its loopback default; the tunnel carries the port.

```sh
# on the machine that hosts the media
xcut serve --addr 127.0.0.1:8619
# on the machine you actually work from
ssh -f -N -L 18619:127.0.0.1:8619 <host>
# then open http://127.0.0.1:18619/
```

Observed through the tunnel: `/api/v1/health` 200, UI index 200 (10241 bytes),
`/api/v1/projects` 200 — with no token sent. **Read that consequence:** a local
forward makes the server see its peer as `127.0.0.1`, and loopback peers are
trusted by design, so the tunnel hands authentication entirely to SSH. Anyone
who can log into that account can drive XCut, and XCut reads media paths from
that account's filesystem. A tunnel is therefore the right answer for "me,
from my other laptop" and the wrong answer for "my teammate, on my file
server". Note that a `--addr` of `0.0.0.0` does not have this property: peers
then present their real LAN address and must authenticate.

## Recipe B — trusted LAN with a token **verified 2026-09-20**

```jsonc
// <workspace>/config.json
{ "server": { "listen_remote": true, "auth_token": "<24+ chars>" } }
```

```sh
xcut serve --addr 0.0.0.0:8619
```

Observed against the shipped installer binary from a non-loopback address:
no `Authorization` header → 401; wrong token → 401 (identical wording, no hint
of how close the attempt was); correct token → 200; `POST /api/v1/session` →
201 with a 64-hex session id and `expires_in: 43200`. Rejections are budgeted
per peer: 20 in 5 minutes, then 429 for the rest of that window — and only
requests that *presented* a credential are charged. A request carrying no
token, session header or session cookie cannot be a guess (there is nothing to
compare), so an idle sign-in page or an unconfigured poller keeps its own 401s
without spending the budget; every wrong token, wrong scheme and stale session
id still counts.

`config show` prints the token as `<set>` and `xcut init` writes an empty one,
so an `XCUT_AUTH_TOKEN` from the environment cannot silently land in a 0644
file. Logs record method/path/peer/status, never a token.

Two things a token does **not** do:

- It does not encrypt anything. The transport is plain HTTP — put it on a
  network you already trust, or inside WireGuard/Tailscale.
- It does not lock out local users. Loopback is trusted so the desktop client
  works without configuration, which means anyone who can reach `127.0.0.1` on
  that host is in. **This is also why you must not front XCut with a
  same-machine reverse proxy to add TLS**: every remote peer would arrive as
  `127.0.0.1` and bypass the gate entirely. Terminate TLS on a different host.

## Recipe C — Tailscale / WireGuard **verified 2026-09-21**

Bind `listen_remote` + token as in Recipe B and reach the peer's tunnel address:
the private network supplies reachability and encryption, so the bearer token
never crosses a wire an attacker can read, while the app layer stays cleartext.

Driven for real against the published v0.1.8-alpha **arm64** binary on a Kylin
V10 SP1 machine reached at its `100.111.136.x` address: no token → 401, wrong
token → 401, correct token → 200 (`/api/v1/health`), `POST /api/v1/session` →
201 with a 64-hex id, reads on the cookie alone → 200, a **write** with the
cookie alone → 401 while the same write with the id echoed in `X-Cut-Session` →
201, and the UI shell loaded unauthenticated (10241 bytes) so the sign-in panel
could appear at all. That is D14's read/write asymmetry observed over a network
path rather than an in-process test.

What this does not change: a peer that can reach the port is inside your trust
boundary by definition, so tailnet ACLs are the access control here, and the
loopback exemption still applies on the serving host itself.

## Rotating and revoking **verified 2026-09-20**

Rotation today is: change `server.auth_token`, restart `xcut serve`. There is
no hot swap, deliberately — the auth gate owns process-level state (the
per-peer failure budget and the issued sessions), and rebuilding it per
request would make rate limiting and sessions silently ineffective while every
unit test stayed green.

The same property is what makes revocation work without any new machinery: the
session store is in memory, so a restart signs every browser out. Revoke a
leaked token by restarting with a new one — old tokens stop working at once
(the gate compares against the configured value), and old cookies stop working
because nothing holds them anymore. A session on its own also expires after 12
hours, and `DELETE /api/v1/session` drops the caller's own session (sign out).
Observed against the shipped binary on a non-loopback bind: a session issued
before the restart returned 200 for `/api/v1/projects`, and the same id
returned 401 after it.
See DECISIONS.md D12 (rotation) and D14 (sessions).

## Verifying on ARM64 **pinned 2026-09-23**

The distro FFmpeg on Kylin V10 SP1 cannot run this project's test suite (the two rows
in the table below), so an ARM64 verification is run against a stock build that the
repository pins: `sh scripts/fetch-arm64-ffmpeg.sh [destdir]` downloads the
FFmpeg 9.0.2 `linuxarm64-gpl` tarball from the immutable release tag
`autobuild-2026-09-20-13-11`, checks its size and SHA256, refuses a mismatch, checks
the `xfade` filter is present, and prints the `bin` directory.

```sh
sh scripts/verify-arm64.sh        # fetches the pin, injects it, proves it reached the tests
```

That script is the durable form of the recipe, because the first hand-run of the pin got
it wrong in two ways worth naming. The toolchain has to go on `PATH`, not into
`XCUT_FFMPEG`/`XCUT_FFPROBE`: the suite resolves both binaries by name
(`internal/testmedia`, `media.requireFFmpeg`) while the env overrides are read by
`config.Env` and reach only the shipped product — a run that sets the env vars and
leaves `PATH` alone reports the vendor build's own failures, 48 of them on the Kylin box,
and says nothing about the pin. And "the pinned binary exists somewhere on disk" is not
"the tests used it", so the script prints `TOOL_CHECK=OK` only after asking `command -v`
what `ffmpeg`/`ffprobe` resolve to *and* reading the version string back. Measured on
Kylin V10 SP1 aarch64 at `2544cc1`: `ran=553 failed=0 skipped=17`, 19 packages `ok`,
`vendor_corruption_lines=0`.

To use the stock build for something other than the suite — the shipped binary, your own
command line — fetch it on its own and point at it:

```sh
BIN=$(sh scripts/fetch-arm64-ffmpeg.sh .tools | tail -1)   # last line is the bin dir
export XCUT_FFMPEG="$BIN/ffmpeg" XCUT_FFPROBE="$BIN/ffprobe"
```

What that verifies, and what it does not: the whole suite passes on aarch64 against
the pinned build, which is more than the distro build ever allowed (15+ tests failed
on environment alone). `-race` does not run on that kernel at all — Go's ThreadSanitizer
reports `unsupported VMA range` before any test executes — so ARM64 is verified
without the race detector, and the race coverage for this project stays on the Linux
x86 full leg and on the `-race` subset the fast gate runs on a dev box that has a C
toolchain. The script reports that refusal as a line (`tsan_refused_lines`) rather than
as a green step; `--skip-race` omits the attempt. The digest in the script is not
publisher-signed (GitHub exposes no digest for the asset); it is what two independent
fetches of that tag agreed on, recorded where it can be read.

## Troubleshooting

| Symptom | What it means |
|---|---|
| `refusing a non-loopback bind: remote listening requires ...` | `listen_remote`/`auth_token` pair incomplete, or `--addr` names a non-loopback interface. Fix the config, or use `--addr 127.0.0.1:8619` and tunnel. |
| `401` from the API | Missing/wrong `Authorization: Bearer`. The web UI shows the sign-in panel instead — it cannot attach a header to `<video>`, thumbnail or download requests, so it exchanges the token for a session cookie once (D14). |
| `429` | That peer presented a bad credential 20 times in 5 minutes (a request with no credential at all is not charged). Wait out the window, or fix the client that is retrying with a bad token. |
| `403` | A remote peer appeared while the server had no token configured — the server is telling you it will not serve unauthenticated remote traffic. |
| UI works, `curl` 401 | Expected: the browser is riding a session cookie; `curl` must send the bearer token or the `X-Cut-Session` header. |
| Import fails on Kylin V10 with "cannot parse probe output" | The vendor FFmpeg writes decoder-plugin logs into ffprobe's stdout. Point `XCUT_FFPROBE`/`XCUT_FFMPEG` at a stock build. The product refuses rather than repairing, because a heuristic filter recovers a *parseable but wrong* document (D13). |
| Render fails with "this FFmpeg build does not include the 'xfade' filter" | A trimmed distro build, not a broken project: Kylin V10 SP1's FFmpeg 4.2.2 has no `xfade` (measured in session #15's ARM64 run). Use `generic_highlight` (no transitions) or install a full build. |
| `doctor` says FFmpeg missing but the app finds it | Different shells, different PATH. `xcut doctor` exits 1 and names the remedy; check the environment the app actually runs in. |

## Health and limits

`GET /api/v1/health` reports `version`, `commit`, `ffmpeg` and `ai_sidecar`
(`missing` until you configure one — the app never downloads a model). Idle
goals are enforced by design rather than by polling: no background scanning
loops, and the server binds loopback unless you say otherwise. Measured idle
cost is in `docs/PERFORMANCE.md` (session #14: 16.4 MB RSS, 0.00 s CPU over
45 s).
