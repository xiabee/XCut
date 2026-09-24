# XCut Decisions (ADR log)

One entry per decision that shapes the architecture. Newest at the bottom.
Format: Context → Decision → Consequences.

## D1: Go as the core language

Context: personal local-first tool; author knows Go and Rust; needs fast CLI,
good process orchestration, single static binary.
Decision: Go owns application core (CLI, scheduler, storage, timeline, style,
FFmpeg orchestration, future HTTP API).
Consequences: fast builds, trivial cross-compilation (incl. Windows/ARM64),
rich standard library. CPU-hot algorithms move to Rust only with benchmark
evidence.

## D2: Rust as optional worker processes, no FFI in phase 1

Context: Go GC and safety are fine for control plane; heavy per-frame/per-sample
math may later need Rust/SIMD; FFI (cgo) breaks easy cross-compilation.
Decision: workers are separate processes (`xcut-worker-*`) speaking versioned
JSON on stdin/stdout (files for bulk). Introduced only when profiling proves
need. IPC overhead is accepted until measured as a bottleneck.
Consequences: crash isolation, independent benchmarking, simple cross-platform
builds; slightly more orchestration code; deferred (not avoided) FFI.

Status (2026-09-07): protocol validated end-to-end. `crates/xcut-worker-media`
(Rust, symphonia) implements `describe` + `audio_rms`; Go client
(`internal/worker`) with timeouts + structured errors; `workers.audio` config
(auto: worker-first with FFmpeg fallback, rust: strict, ffmpeg: builtin).
Benchmark: parity with ffmpeg astats on 60s mp3 (0.127s vs 0.143s) — kept as
optionality, no rewrites. Known gap, narrowed by measurement on 2026-09-23: it is not
ffmpeg-encoded AAC as such — that stream decodes in a bare `.m4a` — it is the AAC track
as the isomp4 reader hands it over from a file that also carries video, which symphonia
refuses as `unsupported feature: aac: predictor data`. A PCM `.wav` fails earlier and
for a different reason (the crate's feature list has no wav demuxer). auto mode's
fallback covers both, and a test provokes the first on purpose; fixing the decode
itself is upstream's.

## D3: No Python in the core; AI as optional sidecar

Context: AI ecosystems (Whisper/YOLO/SAM/Demucs) are Python; core must not
depend on PyTorch or large model downloads.
Decision: base install = XCut binary + FFmpeg and nothing else. AI runs in an
optional sidecar worker with capability detection, timeouts, resource limits,
and protocol versioning. Missing sidecar degrades quality, never availability.
Consequences: deterministic baseline is a first-class product; AI features are
progressive enhancements.

## D4: SQLite via modernc.org/sqlite (no CGO)

Context: local-first single-user storage; must cross-compile Windows/AMD64,
Linux/AMD64+ARM64 without a C toolchain.
Decision: SQLite with the pure-Go `modernc.org/sqlite` driver; WAL mode; busy
timeout; migrations in code. No PostgreSQL/Redis — unacceptable operational
weight for a desktop tool.
Consequences: zero CGO, single-binary deploys, adequate performance for
single-user workloads; driver is slower than mattn/go-sqlite3 (CGO) — accepted,
benchmarks later if it matters.

## D5: Standard library first (no web framework, no Cobra yet)

Context: small CLI surface; net/http is sufficient for a localhost API;
dependencies are audit surface.
Decision: `flag`-based subcommands, `net/http` for the future API, `encoding/json`
everywhere, `log/slog` for logging. Revisit Cobra only when flag parsing hurts.
Consequences: zero-cost dependency audit for now; slight manual wiring.

## D6: JSON (not YAML) for config and style presets in v1

Context: styles need schema validation; stdlib has no YAML parser.
Decision: JSON for config + style presets, validated by explicit validation
code on load. Revisit YAML if user-authored styles demand it.
Consequences: no new dependency; slightly less human-friendly than YAML.

## D7: Timeline as versioned JSON intermediate representation

Context: Analyzer→FFmpeg direct coupling would make styles, previews, and
testing impossible.
Decision: all editing decisions materialize as a validated, serializable,
deterministic Timeline (version field) before any FFmpeg process starts.
Consequences: testable without media files; styles become data; renderer is a
dumb, safe executor.

## D8: Localhost-only by default

Context: local-first privacy tool; network exposure is a risk, not a feature.
Decision: the API server binds 127.0.0.1 unless `listen_remote: true`; remote
listening without authentication is rejected outright in v1.
Consequences: safe default; LAN/multi-device use waits for auth design.
(That design landed as D12 — remote listening is still refused without it.)

## D9: Fast file fingerprint (size+mtime+partial hash), full hash deferred

Context: hashing 50 GB videos per analysis is unacceptable; stale caches must
still be impossible to serve for changed files in the common case.
Decision: cache key = SHA256(size, mtime, head+tail partial content hash,
analyzer, analyzer version, config hash). Full-content hash is an optional
future verify step.
Consequences: cheap cache hits; a same-size-mtime-touched file could in
principle fool the partial hash — documented, acceptable for v1, full-hash mode
planned.

## D10: FFmpeg render strategy v1: normalize → concat

Context: arbitrary codecs/resolutions/fps per source clip.
Decision: per-clip normalize pass to a common canvas (H.264 yuv420p, fixed
fps/size, AAC 48 kHz stereo) with `-threads` capped, then concat demuxer +
fast mux. `<tmp>/<job>/*.partial` output, ffprobe-verified, then atomic rename.
Consequences: robust to heterogeneous inputs, no fancy filter graphs yet;
slightly slower than a single-pass smart filter graph — optimize later with
benchmarks.

## D11: Local-first validation; CI becomes explicit, manual dispatch

Context: the GitHub Actions quota ran out mid-night 2026-09-07 (billing
blocker recorded in PROJECT_STATE). `ci.yml` triggered on every push to main
and every PR, running a 2-OS Go matrix + Rust + packaging job (4 jobs/push,
Windows billed at 2×). Push-triggered CI turns GitHub Actions into a remote
debugger and burns quota on docs-only commits.
Decision (2026-09-07, nightly #2): regular development validates locally via
the local quality gate (`scripts/check.ps1` / `scripts/check.sh`: gofmt, vet,
build, test, race, cross-compile, Rust fmt/clippy/test, optional govulncheck,
FFmpeg integration). `.github/workflows/ci.yml` now triggers only on
`workflow_dispatch` — run it explicitly before a release, after large
cross-platform changes, or when quota recovers. `release.yml` stays
tag-triggered (already explicit). Commits stay frequent; pushes stay batched
(0–2 per night) since they no longer trigger anything.
Consequences: no per-push quota spend; CI returns to its role as independent
cross-platform verification. Acceptance criteria say "Local Quality Gate
Green; CI not run (quota policy)" instead of "CI green". When quota recovers,
re-add push/PR triggers (comment in ci.yml shows how) and consider
`concurrency: cancel-in-progress` plus docs-only `paths-ignore`.

Amendment (session #14): the remote leg must run on the *other* platform, not
just on another machine. Twelve sessions of "a remote CI node re-runs the gate"
were re-running Windows on Windows, and the first `scripts/check.sh full` on a
Linux node since the Windows-only installer landed found eight red tests.
A node that shares the dev box's OS re-verifies the machine; it cannot see
platform drift. Procedure: `git archive HEAD | ssh <node> 'tar -x -C
~/ci/xcut-<sha>'` (a clean snapshot, not a working copy, so no local caches or
untracked files ride along), `GOFLAGS=-count=1` so no cached result is quoted
as evidence, and a repo-local `.tools/ffmpeg` for the integration tests.

Amendment (session #18): the "explicit" release workflow had **never** run to
completion, and checking turned out to be two separate findings.

1. `release.yml` invoked `cargo build -p xcut-worker-media` from the repository
   root, where there is no `Cargo.toml` — every tag from v0.1.1 to v0.1.8-alpha
   is red on Actions with the same `could not find Cargo.toml` (exit 101). The
   step now runs in `crates/xcut-worker-media`, declares the `musl` target
   `ci.yml`'s package job already declared, and its smoke step asserts both
   worker artifacts exist rather than listing `dist/`.
2. Why that stayed invisible for eight releases: the release page **does** have
   assets on every tag, because the local packaging flow attached them
   (`gh release create`). Nobody ever asked the workflow to prove itself. The
   other half of the answer is billing — the 2026-09-06 runs and the v0.1.1…
   v0.1.6 releases were not started at all ("recent account payments have
   failed or your spending limit needs to be increased"), so Actions
   availability is intermittent, which is the standing reason it is not the
   acceptance path (rule 8: the control plane's legs are).

Verified without spending quota: the exact step commands were run against a
clean `git archive` snapshot on the Linux node — root-level cargo reproduces
the failure, the crate-directory build succeeds in 25.7 s, `build-release.sh`
emits the three Go binaries plus both workers, and the smoke assertions hold.
The negative direction came free: before the musl target was installed, the
worker file simply did not exist, which is precisely what the new `test -f`
catches. The tag string is passed to the build **verbatim, `v` included** — that
is what `git describe --tags` yields, what every existing asset is named with,
and what `make-installer.ps1` / `make-setup.ps1` look for in `dist/`; "tidying"
the `v` away would have desynchronised three scripts to fix a cosmetic prefix.
Proof still outstanding: the Actions plumbing itself (checkout, toolchain
inputs, `softprops/action-gh-release`), which only a tag push can exercise —
and cutting a release is the operator's call.

## D13: Refuse loudly over repair ambiguously

Context: on Kylin V10 aarch64 the vendor OMX decoder plugin writes log lines to
the same fd ffprobe uses for its JSON, interleaving *mid-line*, so the buffer
cannot be parsed. A line filter does recover a parseable document — and places
the log text inside `codec_long_name`. That is the general shape of this class
of problem: input that is 95% separable by heuristic and 5% silently wrong.

Decision (2026-09-20, session #14): where a repair cannot be shown to preserve
every value, XCut refuses with a message that names the failing component and
the remedy, and the rejected approach gets a test pinning why it was rejected
(`TestFilteringWouldNotBeSafe`). Wrong metadata outranks no metadata in harm:
a wrong duration or codec silently poisons every downstream timeline, style
score and render, and nothing downstream can tell.

Consequences: some real machines need an explicit `XCUT_FFMPEG`/`XCUT_FFPROBE`
at a stock build — an operator action with a stated reason, not a degraded mode
nobody asked for. No CLI knob avoids the contamination (measured: `-loglevel
quiet` leaves the byte count identical, `-out_filename` is unsupported, the
`-show_entries` form still opens the decoder). Applies to media parsing and to
anything else that reads an external tool's stdout.

## D12: Bearer-token authentication is what unlocks a remote bind

Context: D8 pinned the API to loopback because there was nothing to
authenticate a remote peer with — the whole workspace (every imported path,
every render, the FFmpeg installer trigger) would have been readable and
writable by anyone on the network. AGENTS.md and SECURITY.md both named auth as
the *prerequisite* for remote listening, not a follow-up.

Decision (2026-09-20, session #14): a static bearer token gates non-local
peers. Loopback peers stay trusted, so the desktop client, `xcut client`, and
the double-clicked exe keep working with zero setup.
- The token comes from `server.auth_token` or `XCUT_AUTH_TOKEN` — never a CLI
  flag, which would publish it into process listings and shell history.
- `config.Resolve` refuses `listen_remote: true` without a token of at least
  `config.MinAuthTokenLen` characters, and `serveAddr` re-checks that at the
  last moment before binding: the invariant must not depend on one call site
  having run.
- Trust is decided from `r.RemoteAddr` only. `Host` and forwarding headers are
  attacker-chosen and must never widen the gate.
- Comparison is constant-time; rejections carry no detail (no hint of how close
  an attempt was) and are logged without the token or the supplied header.
- Failed attempts are rate limited per peer (20 per 5 min) with a bounded
  tracker, because the gate is a new growth axis and AGENTS.md rule 4 has no
  exceptions. (Refined 2026-09-21, session #16: a "failed attempt" is a
  request that *presented* a credential — a wrong token, a wrong scheme, a
  stale session id. A credentialless request cannot be a guess, so it is not
  charged; otherwise the sign-in page's own background health poll locked its
  peer out, and the correct token got 429 afterwards — measured live.)
- `/api/v1/*` is gated; the embedded UI shell is not, since it carries no user
  data and a remote operator must be able to load it in order to be asked.

Consequences: `listen_remote` is now a usable option rather than a refusal, and
serve says which posture it started in. What is deliberately *not* solved here:
a browser cannot attach a header to `<video src>`, thumbnails, or download
links, so the web UI over a remote bind needs a second credential — solved by
D14, and explicitly NOT by accepting tokens in query strings (they land in
logs, history, and Referer). Configured tokens are masked
in `xcut config show` and never written into the `xcut init` starter file.

**Rotation is "edit the config and restart", and that stayed a decision rather
than an oversight.** A hot-swap endpoint is the shape an operator expects, and
it was rejected: the gate is process-level on purpose (rebuilding it per
request silently disables the failure budget and the session store while every
test stays green), so a swap would need an atomic pointer plus a
revocation-list for old tokens — machinery guarding a credential that is
already guarded by "restart and the old value stops being read". Revocation
comes for free from the same property (D14 keeps sessions in memory): verified
on the shipped binary, a session issued before a restart returns 200 and the
same id returns 401 after it. The runbook in OPERATIONS.md states the
procedure; if remote use ever grows enough for a restart to be an actual
outage, that is the trigger to revisit this — not a hypothetical leaked token, which restart already answers.

## D14: Remote UI reads ride a cookie; writes need the id echoed

Context: D12 made the API reachable remotely, but the web UI still was not —
the browser fetches media by URL (`<video src>`, asset previews, thumbnail and
download requests) and cannot attach an `Authorization` header to any of them.
The two usual answers were both unacceptable: a token in the query string
leaks into logs, history and `Referer` (D12 rejected it), and letting the
cookie authorize everything would hand CSRF to a server whose whole security
story is that a workspace is one click away from being edited.

Decision (2026-09-20, session #14): a login (`POST /api/v1/session`, proven by
the bearer token) mints a random 256-bit session id, delivered as an
`HttpOnly; SameSite=Strict; Path=/` cookie and returned once in the body. The
gate then applies an asymmetry:

- safe methods (`GET`/`HEAD`) accept the cookie — that is the entire reason the
  cookie exists, and what makes media URLs work;
- every other method requires the id echoed in an `X-Cut-Session` header, which
  no cross-site page can produce from an HttpOnly cookie (and whose cookie
  `SameSite=Strict` keeps off the request anyway).

The browser keeps only the session id (`sessionStorage`, per-tab) — the access
token is never persisted, so a reload stays signed in without storing the thing
that mints credentials. Sessions are in-memory, TTL 12 h, capped at 256 with
refusal past the cap; `DELETE /api/v1/session` revokes one.

Consequences: the web UI works from another machine on a trusted network, with
no per-URL signing and no expiry plumbing in media elements. What it costs: a
restart signs remote UIs out (deliberate — persistence would turn a stolen
session into a durable foothold), the cookie cannot carry `Secure` because
serve has no TLS, and the session id is a bearer credential for reads, so a
same-origin XSS would still read data through it (the UI's textContent-only
rendering is what holds that line, not this).

## D15: point boundaries are imported, and stay optional

**Status**: accepted (2026-09-21)

**Context**: three sessions of measurement (docs/EVAL.md) established that
where a rally *ends* is not recoverable from the core's own signals: audio
onsets are not court-specific in a shared hall, court-ROI motion decay landed
+4.90 s off, and the detector's own segment end reached only F1 0.759 against
an 0.963 oracle. The oracle gap has a price tag: ending clips at true point ends
is worth ~11 points of precision, and nothing inside the signal set can pay it.
One signal outside the signal set can: a burned-in scoreboard changes exactly
once per finished point.

**Decision**: import the fact, do not infer it. The scoreboard is read by the
optional AI sidecar (`score_changes` op); the times arrive as
`style.AssetEvents.Boundaries`, are stored per asset (`assets.score_marks`, with
the crop that produced them), and are consumed by one rule in `trimSegment` that
either trims a clip's dead tail or shifts it to end at a reachable mark. No
in-core vision, no model, no new dependency — D3 keeps recognition outside the
core, and the core keeps its "media is untrusted input" posture because the scan
is one more argv-bounded sidecar call.

**Alternatives rejected**:
- *Teach the event detector to find rally ends* — measured twice on two
  candidate signals; both are worse than the segment end it already has.
- *Ship a model in the core* — breaks D1 (Go core, optional workers) and the
  release-size and license story for one field of one sport.
- *Put the marks in the analysis cache* — they would evict under
  `resource.analysis_cache_mb` and selection would degrade silently between two
  runs of the same project. They are the user's annotation of a camera, like
  `motion_roi`, so they live in the row.

**Consequences**: a project either has marks or it does not, and the two paths
are byte-identical to the pre-feature behaviour when absent (pinned by test).
A misaimed crop is a silent quality loss, so the tools refuse to hide it:
`xcut boundaries` prints a note when a region never changed, and `xcut eval`
records `score_marks` per case so "ran with the scoreboard" cannot mean "scanned
nothing". The column is capped (`storage.MaxScoreMarks`) because the threshold
that produces marks is user-chosen. What it does not buy: which rally is worth
cutting — that ranking problem is unchanged and still the sidecar's to solve.


## D16: the two resource defaults that were left as opt-in are now shipped on

**Context**: two controls were built, measured, and then parked behind a
conservative default. `resource.ffmpeg_max_memory_mb` shipped at 0 (uncapped)
while the thing it bounds was measured at 566 MB for an xfade render and ~54 MB
per 720p analysis child; `xcut doctor` had to say "memory uncapped" in its
sandbox row, and ROADMAP recorded the number any default would have to clear but
not the decision. `resource.proxy_enabled` shipped false, which made its own
2 GiB budget `max_proxy_gb` a ceiling over an empty directory in the shipped
posture — while repeated analysis had measured 10–20× cheaper with the proxy, and
the measured cost of turning it on was ~2.5 MiB per source-minute with the budget
observed to drain correctly under pressure. Both were recorded as owner
decisions, not engineering ones.

**Decision (owner, 2026-09-23)**: ship the bounds. `ffmpeg_max_memory_mb`
defaults to 1536 (roughly 2.7× the largest legitimate child measured here, so a
normal render is never the thing that trips it) and `proxy_enabled` defaults to
true. `0` remains the documented way to ask for uncapped, and a negative value —
a typo — is repaired to the shipped cap rather than to uncapped, because silently
restoring the posture the default replaced is the worse of the two mistakes.

**Consequences**: `proxy_enabled` had to stop being a `bool`. With the default on,
"a file said false" and "the file said nothing" had to be distinguishable or a
workspace config could opt proxies in but never out — the one-way merge was fine
while the default was off and became a usability trap the moment it was not. The
field is now `*bool`, carried by `MergeLayer` only when non-nil, normalised by
`Resolve`, and read through `Config.ProxyOn()`; `config.Load` loading sparse
(the precedence fix in the same session) is what makes that layering mean
anything, and `TestMergeLayerCarriesEveryLeafField` had to learn the pointer kind
before the completeness gate would keep its promise about new fields. The doctor
line flips to the capped posture on Windows out of the box. On non-Windows the
memory knob is still a no-op stub — it is a job-object feature, and the row says
so rather than implying a bound that is not there.

> **Amended 2026-09-23 by D18**: that last sentence stopped being true the same week.
> Linux grew a second rung (a transient systemd scope per child), so the knob is a
> no-op only on macOS and other platforms; the row now reports the mechanism and the
> refusal, not a blanket "Windows-only". The rest of this entry stands as written.

## D17: ARM64 verification runs against a pinned stock FFmpeg, without the race detector

**Context**: D13 and the Kylin rows in OPERATIONS.md established that the distro
FFmpeg on Kylin V10 SP1 cannot drive this project — its OMX decoder writes plugin logs
into ffprobe's stdout (D13) and 4.2.2 has no `xfade`. The standing answer was "point
`XCUT_FFPROBE`/`XCUT_FFMPEG` at a stock build", and for the *product* that works. For
verification it never could: the test suite resolves both binaries by name
(`testmedia.HasFFmpeg`, `media.requireFFmpeg`), so the result was a ledger entry reading
"the ARM64 test suite is NOT VERIFIED — 15+ tests fail for environmental reasons", with
no way to tell an environmental failure from a real one on that platform. The Windows
one-click path already pins a checksummed Gyan.dev build; nothing equivalent existed for
arm64, and dropping an unpinned binary onto the node was refused as a decision, not as
laziness.

**Decision (owner, 2026-09-23)**: pin one. `scripts/fetch-arm64-ffmpeg.sh` fetches
FFmpeg 9.0.2 `linuxarm64-gpl` from the immutable release tag `autobuild-2026-09-20-13-11`
and checks size (126,904,212 B) and SHA256 before extracting, then checks `xfade` is
actually in the build — refusing a mismatch rather than running something else.
`master-latest`, which is the same content under a moving tag, is deliberately not used.

**Consequences**: the honest one is that this is not publisher-signed. GitHub's release
API publishes no digest for the asset (`digests: null`, checked 2026-09-23), so the hash
in the script is what two independent fetches of that tag agreed on (the aarch64 host and
an x86_64 laptop), and the script says so where a reader will see it. Second: ARM64 is
verified *without* `-race` — that kernel refuses ThreadSanitizer (`unsupported VMA range`),
so race coverage for this project stays on the Linux x86 full leg and the fast gate's
`-race` subset, and any claim of "arm64 is covered" has to carry that clause. Third: the
pin is injected through `PATH`, and a run that reaches for `XCUT_FFMPEG` instead will fail
48 tests on the vendor build while looking like it tested the pin — which is how this was
first measured, and why the runner asserts `TOOL_CHECK` from `command -v` before it runs
anything. Fourth: nothing in a gate runs the script yet, so arm64 remains a documented
verification step rather than an automated leg.

## D18: the memory cap gets a Linux rung through systemd scopes, and says less than it could

**Context**: `resource.ffmpeg_max_memory_mb` was built for the Windows job object and
shipped armed at 1536 MB (D16), which left the documented posture on every other platform
as what `doctor` politely called "job-object sandbox is Windows-only; cleanup relies on
the context-kill path". That sentence was true and useless: the server runs on Linux, the
ARM64 target is Linux, and on those machines an FFmpeg child that decides to allocate
without limit can take the host with it while the config claims a ceiling exists. AGENTS.md
rule 4 says nothing may be unbounded, and a knob that only works on one of the three
platforms the project ships is a knob that lies on two of them.

**Decision (2026-09-23)**: cap the children on Linux too, through what is already there —
each FFmpeg child starts in its own transient systemd scope with `MemoryMax=<n>M` and
`MemorySwapMax=0`. No new dependency, no bundled binary, no shell: `systemd-run` is
argv-first and on the three hosts checked (Ubuntu 24.04 ×2 with systemd 255 on cgroup v2,
Kylin V10 SP1 with systemd 245) it is installed and `prlimit` beside it. `media.Run`,
`RunCombined`, `StreamStdout` and `Version` go through one `wrapChild`, so no exec path is
capped by accident rather than by construction.

**Consequences**: three of them, and the last is the reason the wording matters.
1. *stdout purity is a hard requirement, and it holds* — measured before writing the code:
   `systemd-run` writes its `Running as unit:` line to stderr and `-q` removes even that,
   and the child's exit status propagates verbatim (1 for a failure, 137 when the cgroup
   killer acts). A wrapper that wrote to stdout would have recreated D13 inside our own
   toolchain, so `TestWrappedChildKeepsPureStdoutAndItsOwnExitStatus` compares bytes rather
   than parsing them.
2. *An unprivileged caller cannot ask the system manager for a scope* (measured:
   "Interactive authentication required"), so the wrap goes through `--user` off root, and
   the whole mechanism is probed once: a host without a session bus, or an old systemd that
   rejects the property, resolves to "children run unwrapped" with the refusal in the
   posture string. A render that died with `Failed to start transient scope unit` would read
   as FFmpeg breaking.
3. *Armed is not enforced.* On Kylin V10 SP1 — hybrid cgroup: v1 controllers under a tmpfs
   plus `unified` at `/sys/fs/cgroup/unified` — `systemd-run --scope -p MemoryMax=100M`
   exits 0, and the child allocates 400 MB and lives. `systemctl show` on that unit answers
   why: `MemoryMax=infinity`, `ControlGroup=` empty. The manager never attached the scope, so
   the property went nowhere, and nothing in the exit code says so. That is why the posture
   this package reports says the scope *started* — and why the enforcement claim in this
   repository is attached to a cgroup-v2 measurement (`TestCapKillsAChildThatOverrunsIt`:
   512 MB under a 128 MB cap, killed with SIGKILL, and red under the mutation that stops
   wrapping) rather than to "we asked systemd politely". `xcut doctor` asks the manager
   itself now — a named probe scope, `systemctl show -p MemoryMax --value` read off the
   live unit — so an operator sees the WARN on the host where the limit goes nowhere;
   and because the deciding half of that is a pure function over the answer string, its
   eight table cases (plus a four-value negative arm) run on every leg instead of
   only where a session bus exists. The
   container/seccomp rung in ROADMAP Phase 4 stays open: this closes a resource boundary,
   not a filesystem one.

## D19: the FFmpeg installer fetches from the pin it gates, and asks the tool what it is

Context: a coverage sweep of the whole tree (`4e039e1`, 17 functions at 0.0% after
max-merge) left five of them in `internal/setup` — the package that puts a binary on the
user's PATH. Reading that code rather than the list found two things the tests could not
have caught because they never ran it: `fetchPinned` ignored `Pin.URL` and fetched the
package-level constant, while `Status.Source` had already told the UI the other URL, and
`verifyFFprobe` treated a zero exit status as proof of identity. D17 and the release
digest checks already commit this repository to naming where a binary came from; a path
with 0% coverage was the one place that claim was unchecked.
Decision: fetch from the URL of the pin actually in force (`runErr` binds it, and a nil
`Fetch` means the production fetcher rather than a missing one), and verify by running
`ffprobe -version` **and** requiring the answer to call itself ffprobe — extended the same
day to both tools the archive extracts (`verifyTools`), because `extractTools` installs
`ffmpeg.exe` beside `ffprobe.exe`, the renderer execs the first on every job, and a check
that only ever asked the second would publish an unverified binary as installed.
Consequences: an artifact gated against one identity can no longer be downloaded from
another source, which is what makes `Status.Source` a statement rather than a decoration;
a file that runs happily but is not the tool is refused where it used to be published as
ready. What this does not buy: a banner is a string anyone can print, so the identity
check is a defect-catcher (a mislabeled extract, a zero-byte stub, the wrong entry kept
by the zip reader) and not an authenticity check — provenance still rests on the
SHA-256 and byte-count gate that runs before it. Both halves are tested by running this
test binary as the tool under verification (an env var picks the answer it gives), which
keeps the checks honest without a shell, a compiler call, or a committed fixture.
