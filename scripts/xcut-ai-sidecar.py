#!/usr/bin/env python3
"""XCut AI sidecar — reference implementation of protocol v1.

Optional plugin: XCut's core never requires this file. It demonstrates the
worker contract every AI sidecar must honor (see docs/ARCHITECTURE.md):

    one JSON request on stdin  →  one JSON response on stdout
    {"protocol": 1, "op": "...", "input": "...", "params": {...}}
    →  {"protocol": 1, "ok": true,  "op": "...", "result": {...}}
    →  {"protocol": 1, "ok": false, "op": "...", "error": {"code": "...", "message": "..."}}

Ops: describe (required), capabilities, health, analyze.

This reference sidecar ships one analyzer: `transcript` (speech-to-text
subtitles). It probes for a locally installed Whisper backend —
openai-whisper, faster-whisper or whisper-cli — and honestly reports
"unavailable" when none is present: install any of them and the capability
turns on with zero XCut changes. Never let the core download models
silently (DECISIONS D3).

Install: put this file on PATH as `xcut-ai` (chmod +x), or point
workers.ai_bin in the workspace config at it.
"""

import base64
import json
import mimetypes
import os
import platform
import re
import subprocess
import tempfile
import shutil
import sys
import urllib.error
import urllib.request
import uuid

PROTOCOL = 1

# OpenAI-compatible HTTP backends (env-configured, never auto-probed over
# the network): XCUT_SIDECAR_STT_URL points at a transcription server
# (POST {url}/v1/audio/transcriptions, e.g. whisper.cpp server or
# faster-whisper-server), XCUT_SIDECAR_VISION_URL at a chat/completions
# gateway for frame description (e.g. an OpenAI-compatible LLM gateway
# serving a vision model). XCUT_SIDECAR_INSECURE_TLS=1 accepts self-signed
# certificates; XCUT_SIDECAR_TIMEOUT caps each HTTP call (seconds).
STT_URL_ENV = "XCUT_SIDECAR_STT_URL"
VISION_URL_ENV = "XCUT_SIDECAR_VISION_URL"
VISION_MODEL_ENV = "XCUT_SIDECAR_VISION_MODEL"
INSECURE_TLS_ENV = "XCUT_SIDECAR_INSECURE_TLS"
HTTP_TIMEOUT_ENV = "XCUT_SIDECAR_TIMEOUT"


def _http_timeout():
    try:
        return float(os.environ.get(HTTP_TIMEOUT_ENV, "300"))
    except ValueError:
        return 300.0


def _opener():
    if os.environ.get(INSECURE_TLS_ENV, "") == "1":
        ctx = ssl_unverified()
        return urllib.request.build_opener(ctx)
    return urllib.request.build_opener()


def ssl_unverified():
    import ssl
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    return urllib.request.HTTPSHandler(context=ctx)


def _http_json(url, payload, timeout=None):
    """POST JSON and parse the JSON response. Raises on HTTP/network errors
    (the caller maps them into the envelope's structured error)."""
    req = urllib.request.Request(url, data=json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json"})
    resp = _opener().open(req, timeout=timeout or _http_timeout())
    return json.loads(resp.read().decode())


def _http_multipart(url, field, filename, content, extra=None):
    boundary = "xcut-" + uuid.uuid4().hex
    parts = []
    for k, v in (extra or {}).items():
        parts.append("--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n"
                     % (boundary, k, v))
    parts.append("--%s\r\nContent-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\n"
                 "Content-Type: application/octet-stream\r\n\r\n" % (boundary, field, filename))
    body = "".join(parts).encode() + content + ("\r\n--%s--\r\n" % boundary).encode()
    req = urllib.request.Request(url, data=body, headers={
        "Content-Type": "multipart/form-data; boundary=%s" % boundary})
    return json.loads(_opener().open(req, timeout=_http_timeout()).read().decode())


def _transcript_backend():
    """Probe for a locally usable speech recognizer. Returns
    (backend_name, detail) or None. Every check here is real — a miss is
    reported as unavailable, never aspirational (DECISIONS D3). The HTTP
    backend is "configured" (env set), not probed live: its reachability is
    analyzed at analyze time and surfaces as analyze_failed."""
    stt = os.environ.get(STT_URL_ENV, "")
    if stt:
        return ("http-stt", "OpenAI-compatible transcription server at %s" % stt)
    try:
        import whisper  # noqa: F401  (openai-whisper)
        return ("whisper", "openai-whisper (python module)")
    except ImportError:
        pass
    try:
        import faster_whisper  # noqa: F401
        return ("faster-whisper", "faster-whisper (python module)")
    except ImportError:
        pass
    if shutil.which("whisper-cli"):
        return ("whisper-cli", "whisper.cpp whisper-cli (PATH)")
    return None


def _model_entry():
    backend = _transcript_backend()
    if backend is None:
        return {"name": "transcript", "available": False, "loaded": False,
                "detail": "no speech backend installed (openai-whisper | "
                          "faster-whisper | whisper-cli) — install one and "
                          "the capability turns on, no XCut changes needed"}
    name, detail = backend
    return {"name": "transcript", "available": True, "loaded": False,
            "detail": detail + " (model files load on first analyze)"}


def op_transcript(req):
    backend = _transcript_backend()
    if backend is None:
        return err(req, "not_implemented",
                   "no speech backend installed (openai-whisper | "
                   "faster-whisper | whisper-cli)")
    path = req.get("input") or ""
    if not path or not os.path.isfile(path):
        return err(req, "bad_request",
                   "input media file %r does not exist" % path)
    params = req.get("params") or {}
    name, _ = backend
    try:
        result = _run_whisper(name, path, params.get("language"),
                              params.get("model"))
    except Exception as e:  # noqa: BLE001 — the envelope is the contract
        return err(req, "analyze_failed", "transcription failed: %s" % e)
    return ok(req, result)


def _run_whisper(backend, path, language, model_name):
    """Run one backend and return {'language': ..., 'segments': [...]} with
    word timings where the backend provides them (karaoke output needs
    them; plain SRT does not)."""
    if backend == "http-stt":
        url = os.environ[STT_URL_ENV].rstrip("/")
        with open(path, "rb") as f:
            content = f.read()
        resp = _http_multipart(url + "/v1/audio/transcriptions", "file",
                               os.path.basename(path), content,
                               {"model": model_name or "whisper-1",
                                "response_format": "verbose_json",
                                "timestamp_granularities[]": "word,segment"})
        segments = []
        for seg in resp.get("segments", []):
            words = [{"start": float(w.get("start", 0.0)),
                      "end": float(w.get("end", 0.0)),
                      "word": (w.get("word") or "").strip()}
                     for w in seg.get("words", [])
                     if (w.get("word") or "").strip()]
            segments.append({"start": float(seg.get("start", 0.0)),
                             "end": float(seg.get("end", 0.0)),
                             "text": (seg.get("text") or "").strip(),
                             "words": words})
        return {"language": resp.get("language"), "segments": segments}
    if backend == "whisper":
        import whisper
        model = whisper.load_model(model_name or "base")
        result = model.transcribe(path, language=language,
                                  word_timestamps=True)
        segments = []
        for seg in result.get("segments", []):
            words = [{"start": w.get("start", 0.0), "end": w.get("end", 0.0),
                      "word": (w.get("word") or "").strip()}
                     for w in seg.get("words", [])
                     if (w.get("word") or "").strip()]
            segments.append({"start": float(seg.get("start", 0.0)),
                             "end": float(seg.get("end", 0.0)),
                             "text": (seg.get("text") or "").strip(),
                             "words": words})
        return {"language": result.get("language"), "segments": segments}
    if backend == "faster-whisper":
        from faster_whisper import WhisperModel
        model = WhisperModel(model_name or "base", device="cpu",
                             compute_type="int8")
        it, info = model.transcribe(path, language=language,
                                    word_timestamps=True)
        segments = []
        for seg in it:
            words = [{"start": w.start, "end": w.end, "word": w.word.strip()}
                     for w in (seg.words or []) if w.word.strip()]
            segments.append({"start": float(seg.start),
                             "end": float(seg.end),
                             "text": (seg.text or "").strip(), "words": words})
        return {"language": info.language, "segments": segments}
    raise RuntimeError("backend %r has no in-process runner" % backend)


def _frame_describe_entry():
    url = os.environ.get(VISION_URL_ENV, "")
    if not url:
        return {"name": "frame_describe", "available": False, "loaded": False,
                "detail": "no vision endpoint configured — set %s (an "
                          "OpenAI-compatible /v1/chat/completions base URL "
                          "serving a vision model) and the capability turns "
                          "on" % VISION_URL_ENV}
    model = os.environ.get(VISION_MODEL_ENV, "vision")
    return {"name": "frame_describe", "available": True, "loaded": False,
            "detail": "chat gateway %s (model %s)" % (url, model)}


def op_frame_describe(req):
    """Semantic frame analysis through an OpenAI-compatible chat gateway:
    one image in, one text description out. This is the seam for
    match-phase awareness (e.g. reading a scoreboard overlay), content
    tagging, or any future semantic selection signal. Input: an image file
    path; params: optional 'prompt'."""
    url = os.environ.get(VISION_URL_ENV, "")
    if not url:
        return err(req, "not_implemented",
                   "no vision endpoint configured (set %s)" % VISION_URL_ENV)
    path = req.get("input") or ""
    if not path or not os.path.isfile(path):
        return err(req, "bad_request",
                   "input image file %r does not exist" % path)
    params = req.get("params") or {}
    prompt = params.get("prompt") or "Describe this video frame."
    model = os.environ.get(VISION_MODEL_ENV, "vision")
    mime = mimetypes.guess_type(path)[0] or "image/jpeg"
    with open(path, "rb") as f:
        b64 = base64.b64encode(f.read()).decode()
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": [
            {"type": "text", "text": prompt},
            {"type": "image_url",
             "image_url": {"url": "data:%s;base64,%s" % (mime, b64)}},
        ]}],
        "max_tokens": int(params.get("max_tokens", 200)),
    }
    try:
        resp = _http_json(url.rstrip("/") + "/v1/chat/completions", payload)
    except Exception as e:  # noqa: BLE001 — the envelope is the contract
        return err(req, "analyze_failed", "vision request failed: %s" % e)
    try:
        text = resp["choices"][0]["message"]["content"]
    except (KeyError, IndexError, TypeError):
        return err(req, "analyze_failed",
                   "unexpected gateway response shape: %s"
                   % json.dumps(resp)[:200])
    return ok(req, {"description": (text or "").strip(), "model": model})


# Real sidecars register analyzers here; each entry becomes a capability.
ANALYZERS = {"transcript": op_transcript, "frame_describe": op_frame_describe}

# Models this sidecar could use, with local availability probed lazily.
# `available` must be a real check, never aspirational.
MODELS = [_model_entry(), _frame_describe_entry()]


def op_score_changes(req):
    """Point boundaries from a burned-in scoreboard.

    Every score change is exactly one finished rally, and an overlay changes
    only when the score does, so a scene-difference detector aimed at the
    digits produces the boundaries — the recipe docs/EVAL.md documents, with
    its three traps built in: the comma inside gt() must be quoted (modern
    ffmpeg rejects `\\,`), `metadata=print:file=` must be a *relative* path
    (a Windows drive-letter colon is a filter-argument separator), and the
    threshold must be low because the overlay cross-fades.
    """
    ffmpeg = shutil.which("ffmpeg")
    if ffmpeg is None:
        return err(req, "not_implemented", "ffmpeg is not installed")
    path = req.get("input") or ""
    if not path or not os.path.isfile(path):
        return err(req, "bad_request",
                   "input media file %r does not exist" % path)
    params = req.get("params") or {}
    crop = params.get("crop")  # normalized x,y,w,h like the court ROI
    if not isinstance(crop, (list, tuple)) or len(crop) != 4:
        return err(req, "bad_request",
                   "params.crop must be [x, y, w, h] in 0..1 fractions")
    try:
        fx, fy, fw, fh = [float(v) for v in crop]
    except (TypeError, ValueError):
        return err(req, "bad_request", "params.crop must hold four numbers")
    if not all(0.0 <= v <= 1.0 for v in (fx, fy, fw, fh)) or fw <= 0 or fh <= 0:
        return err(req, "bad_request", "crop values must be fractions within 0..1")
    threshold = float(params.get("threshold", 0.03))
    rate = float(params.get("fps", 4))
    gap = float(params.get("cluster_gap", 5.0))

    dims = _media_size(ffmpeg, path)
    if dims is None:
        return err(req, "analyze_failed", "cannot read the video dimensions")
    w, h = dims
    x, y = int(fx * w), int(fy * h)
    cw, ch = max(2, int(fw * w)), max(2, int(fh * h))
    if x + cw > w or y + ch > h:
        return err(req, "bad_request", "crop extends past the frame")

    # The dump path stays relative and the cwd is the temp dir: an absolute
    # Windows path would be split at its drive colon by the filter parser.
    tmp = tempfile.mkdtemp(prefix="xcut-score-")
    try:
        graph = ("crop=%d:%d:%d:%d,fps=%g,select='gt(scene,%g)',"
                 "metadata=print:file=board.txt" % (cw, ch, x, y, rate, threshold))
        p = subprocess.run([ffmpeg, "-hide_banner", "-nostdin", "-i", path,
                            "-an", "-sn", "-vf", graph, "-f", "null", "-"],
                           cwd=tmp, capture_output=True, text=True, timeout=900)
        if p.returncode != 0:
            return err(req, "analyze_failed",
                       "score-change scan failed: %s" % (p.stderr or "")[-400:])
        times = _scene_times(os.path.join(tmp, "board.txt"))
    except subprocess.TimeoutExpired:
        return err(req, "analyze_failed", "score-change scan timed out")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    return ok(req, {"times": _cluster(times, gap), "raw": len(times),
                    "crop": [x, y, cw, ch], "threshold": threshold})


def _media_size(ffmpeg, path):
    """Frame width/height via ffmpeg (ffprobe is not always the tool present)."""
    try:
        p = subprocess.run([ffmpeg, "-hide_banner", "-i", path],
                           capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.TimeoutExpired):
        return None
    m = re.search(r", (\d{2,6})x(\d{2,6})(?:[ ,])", p.stderr or "")
    if not m:
        return None
    return int(m.group(1)), int(m.group(2))


def _scene_times(dump_path):
    if not os.path.isfile(dump_path):
        return []
    out = []
    with open(dump_path, "r", encoding="utf-8", errors="replace") as fh:
        for line in fh:
            m = re.search(r"pts_time:([0-9.]+)", line)
            if m:
                out.append(float(m.group(1)))
    return out


def _cluster(times, gap):
    """The overlay cross-fades, so one point spans several frames: keep the
    first time of each run separated by more than `gap` seconds."""
    out = []
    prev = None
    for t in sorted(times):
        if prev is None or t - prev > gap:
            out.append(round(t, 3))
        prev = t
    return out


def op_describe(req):
    return ok(req, {
        "name": "xcut-ai-sidecar",
        "version": "0.3.0",
        "protocol": PROTOCOL,
        "ops": ["describe", "capabilities", "health", "analyze", "score_changes"],
    })


def op_capabilities(req):
    return ok(req, {
        "ops": [
            {"op": "describe", "desc": "protocol handshake"},
            {"op": "capabilities", "desc": "advertised analyzers and models"},
            {"op": "health", "desc": "readiness probe"},
            {"op": "analyze", "desc": "run one advertised analyzer"},
            {"op": "score_changes", "desc": "point boundaries from a burned-in "
                                            "score overlay (params.crop=[x,y,w,h])"},
        ],
        "models": MODELS,
        "device": "cpu",
        "extra": {"python": platform.python_version()},
    })


def op_health(req):
    return ok(req, {
        "ready": True,
        "models_loaded": [],
        "detail": "reference sidecar: protocol only, no models installed",
    })


def op_analyze(req):
    name = (req.get("params") or {}).get("analyzer")
    fn = ANALYZERS.get(name)
    if fn is None:
        return err(req, "not_implemented",
                   "analyzer %r is not available in this sidecar build" % name)
    return fn(req)


OPS = {
    "describe": op_describe,
    "capabilities": op_capabilities,
    "health": op_health,
    "analyze": op_analyze,
    "score_changes": op_score_changes,
}


def ok(req, result):
    return {"protocol": PROTOCOL, "ok": True, "op": req.get("op"), "result": result}


def err(req, code, message):
    return {"protocol": PROTOCOL, "ok": False, "op": req.get("op"),
            "error": {"code": code, "message": message}}


def main():
    try:
        req = json.loads(sys.stdin.read() or "{}")
    except json.JSONDecodeError as e:
        resp = err({}, "bad_request", "request is not valid JSON: %s" % e)
    else:
        op = req.get("op")
        fn = OPS.get(op)
        if fn is None:
            resp = err(req, "unknown_op", "op %r is not supported" % op)
        else:
            resp = fn(req)
    sys.stdout.write(json.dumps(resp))
    sys.stdout.flush()


if __name__ == "__main__":
    main()
