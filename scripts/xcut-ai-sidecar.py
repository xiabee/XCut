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


def op_describe(req):
    return ok(req, {
        "name": "xcut-ai-sidecar",
        "version": "0.3.0",
        "protocol": PROTOCOL,
        "ops": ["describe", "capabilities", "health", "analyze"],
    })


def op_capabilities(req):
    return ok(req, {
        "ops": [
            {"op": "describe", "desc": "protocol handshake"},
            {"op": "capabilities", "desc": "advertised analyzers and models"},
            {"op": "health", "desc": "readiness probe"},
            {"op": "analyze", "desc": "run one advertised analyzer"},
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
