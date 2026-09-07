#!/usr/bin/env python3
"""XCut AI sidecar — reference implementation of protocol v1.

Optional plugin: XCut's core never requires this file. It demonstrates the
worker contract every AI sidecar must honor (see docs/ARCHITECTURE.md):

    one JSON request on stdin  →  one JSON response on stdout
    {"protocol": 1, "op": "...", "input": "...", "params": {...}}
    →  {"protocol": 1, "ok": true,  "op": "...", "result": {...}}
    →  {"protocol": 1, "ok": false, "op": "...", "error": {"code": "...", "message": "..."}}

Ops: describe (required), capabilities, health, analyze.

This reference sidecar advertises no models — it is the honest empty
baseline ("available: nothing, degrades gracefully"). Replace `ANALYZERS`
with real implementations (Whisper, VLM, …) to turn capabilities on; never
let the core download models silently (DECISIONS D3).

Install: put this file on PATH as `xcut-ai` (chmod +x), or point
workers.ai_bin in the workspace config at it.
"""

import json
import os
import platform
import sys

PROTOCOL = 1

# Real sidecars register analyzers here; each entry becomes a capability.
ANALYZERS = {}

# Models this sidecar could use, with local availability probed lazily.
# `available` must be a real check, never aspirational.
MODELS = []


def op_describe(req):
    return ok(req, {
        "name": "xcut-ai-sidecar",
        "version": "0.1.0",
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
