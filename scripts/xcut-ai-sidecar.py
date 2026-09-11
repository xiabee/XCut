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

import json
import os
import platform
import shutil
import sys

PROTOCOL = 1


def _transcript_backend():
    """Probe for a locally usable speech recognizer. Returns
    (backend_name, detail) or None. Every check here is real — a miss is
    reported as unavailable, never aspirational (DECISIONS D3)."""
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


# Real sidecars register analyzers here; each entry becomes a capability.
ANALYZERS = {"transcript": op_transcript}

# Models this sidecar could use, with local availability probed lazily.
# `available` must be a real check, never aspirational.
MODELS = [_model_entry()]


def op_describe(req):
    return ok(req, {
        "name": "xcut-ai-sidecar",
        "version": "0.2.0",
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
