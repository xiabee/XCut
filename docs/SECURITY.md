# XCut Security

## Threat Model

XCut processes **user-supplied media files** on a personal machine. Threat
sources, in order of practical importance:

1. **Malformed media files** — crafted videos/audios attacking FFmpeg/ffprobe
   parsers (memory corruption, decompression bombs).
2. **Malicious paths/names** — path traversal, absolute-path escapes, symlinks,
   UNC/drive-letter tricks (Windows), shell metacharacters.
3. **Local network exposure** — the HTTP API must not silently open the machine
   to the LAN (controls: D12).
4. **Supply chain** — dependencies (Go modules, Rust crates) and the FFmpeg
   build itself.

Trusted: the XCut binary, its config files (user-controlled), the local OS user
(and therefore every process running as it — which is why loopback peers are
trusted). Untrusted: every media file, every filename, every uploaded byte, and
every peer that reaches the API from off-box.

## Attack Surface & Controls

### FFmpeg / ffprobe invocation
- Always `exec.CommandContext(bin, args...)` — **never** a shell (`sh -c`,
  `cmd /c`) and never string-concatenated commands. User text can only ever be
  a single argument value.
- `context.Context` timeout on every external call; child process killed on
  cancel; process group cleanup where supported.
- Concurrency capped by config (`resource.max_ffmpeg_processes`), threads capped
  via `-threads`.
- FFmpeg is treated as an untrusted decoder: it runs as the same (user) process,
  crashes are caught and surfaced as `ffmpeg_failure`, never fatal to the core.

### Path security
- All project/asset paths resolve through a safe-join helper that rejects
  absolute inputs, `..` escapes, and (where detectable) symlink escapes outside
  the workspace root.
- Windows specifics handled: drive-letter absoluteness, UNC paths, reserved
  device names.
- Asset files may live anywhere on disk (user's videos) — import records them;
  project *outputs* never leave the workspace.

### HTTP API and uploads
- Binds `127.0.0.1` by default (`serve` refuses any other bind).
  `listen_remote: true` is an explicit user act and additionally requires a
  bearer token — both enforced in `config.Resolve` *and* again in `serveAddr`
  before the socket opens (D12).
- Non-local peers must send `Authorization: Bearer <token>`; the token is
  compared in constant time, failures are reported as a bare 401 (no hint how
  close an attempt was) and counted per peer — 20 rejections per 5 minutes,
  then 429 — so the gate cannot be brute-forced and cannot grow its tracking
  state without bound.
- Peer trust is decided from the socket's own `RemoteAddr`. `Host`,
  `X-Forwarded-For` and similar are attacker-chosen and never consulted.
- Loopback peers are trusted without a token, which is what keeps the desktop
  client and the double-clicked exe zero-config. Deliberate trade-off: any
  local process (and any user account on that machine) can reach the API. The
  threat model already treats the local OS user as trusted.
- The embedded UI shell is served unauthenticated; every `/api/v1` route
  (including the media preview, render/subtitle download and the installer
  trigger) is not. Browser-loaded media URLs cannot carry a header, so the web
  UI over a *remote* bind additionally needs signed capability URLs — until
  that lands, remote serving is for API clients, and the UI remains a local
  surface.
- Upload size enforced with `http.MaxBytesReader`/`io.LimitReader` streaming
  writes; `Content-Length` is not trusted alone.
- File type decided by real probing (ffprobe), never by extension or
  Content-Type.
- No CORS headers are sent: the UI is same-origin by construction, and a
  wildcard would hand the loopback-trusted API to any page the operator opens.

### Secrets
- No secrets in git-tracked config. Env vars (`XCUT_*`) or a git-ignored local
  secrets file. `.gitignore` covers `secrets*.json`, `.env*`.

### Telemetry
- **None.** No network calls by the core. No filenames, hashes, or usage data
  leave the machine, ever, unless a future explicit opt-in exists.

### Rust workers (future)
- Safe Rust by default; any `unsafe` requires a written safety justification,
  tests, and minimal scope. Workers parse only structured JSON input; workers
  are optional — absence degrades capability, never availability.

## Known Limitations (current)

- FFmpeg parses untrusted media in-process with full user rights; OS-level
  sandboxing (job objects, Docker, seccomp) is not yet applied. Mitigation:
  FFmpeg is a mature parser and runs with timeouts/concurrency caps. Future
  hardening will add platform sandbox options.
- No HTTP API auth beyond the static bearer token (D12): there is no
  per-client identity, no rotation or revocation surface (revoke = edit the
  config and restart), and no TLS — `serve` speaks cleartext HTTP. A remote
  bind therefore belongs on a trusted network or inside a tunnel (Tailscale,
  WireGuard, SSH), never on a raw untrusted LAN: the bearer token crosses the
  wire in plaintext and is the only thing between a passive attacker and the
  whole workspace.
- **Do not front `serve` with a same-machine reverse proxy.** Loopback peers
  are trusted by design, so a TLS terminator on 127.0.0.1 would present every
  remote client as local and silence the gate entirely. Terminating TLS in
  front of this API requires first making trust provable through the proxy
  (a signed header or a unix socket), which is not built.
- The failure tracker and its budget live in one process: a restart resets a
  peer's lockout. Bounded, in-memory, and deliberately not persisted.
