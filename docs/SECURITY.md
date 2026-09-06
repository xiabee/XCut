# XCut Security

## Threat Model

XCut processes **user-supplied media files** on a personal machine. Threat
sources, in order of practical importance:

1. **Malformed media files** — crafted videos/audios attacking FFmpeg/ffprobe
   parsers (memory corruption, decompression bombs).
2. **Malicious paths/names** — path traversal, absolute-path escapes, symlinks,
   UNC/drive-letter tricks (Windows), shell metacharacters.
3. **Local network exposure** — the future HTTP API must not silently open the
   machine to LAN.
4. **Supply chain** — dependencies (Go modules, Rust crates) and the FFmpeg
   build itself.

Trusted: the XCut binary, its config files (user-controlled), the local OS user.
Untrusted: every media file, every filename, every uploaded byte (future API).

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

### Uploads / HTTP API (when it exists)
- Binds `127.0.0.1` by default; `listen_remote: true` is an explicit user act
  and later requires authentication (not built yet — remote mode is therefore
  rejected while auth is absent).
- Upload size enforced with `http.MaxBytesReader`/`io.LimitReader` streaming
  writes; `Content-Length` is not trusted alone.
- File type decided by real probing (ffprobe), never by extension or
  Content-Type.

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
- No HTTP API yet, so upload/network attack surface does not exist yet. When it
  lands, the controls above are prerequisites, not follow-ups.
