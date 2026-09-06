//! CLI entry: read one request from stdin, write one response to stdout, exit.
//! Any panic is caught (catch-unwind) and mapped to a failure response so the
//! Go core never sees a silent hang or a partial write.

use std::io::{self, Read, Write};
use xcut_worker_media::audio;
use xcut_worker_media::error::WorkerError;
use xcut_worker_media::protocol::{
    AudioRmsParams, AudioRmsResult, Describe, Request, Response, PROTOCOL,
};

const NAME: &str = "xcut-worker-media";
const VERSION: &str = env!("CARGO_PKG_VERSION");
const OPS: &[&str] = &["describe", "audio_rms"];

fn main() {
    let out = io::stdout();
    let code = std::panic::catch_unwind(main_inner).unwrap_or_else(|_| {
        let resp = Response {
            protocol: PROTOCOL,
            ok: false,
            op: "unknown".into(),
            result: None,
            error: Some(xcut_worker_media::protocol::ErrorBody {
                code: "panic".into(),
                message: "worker panicked".into(),
            }),
        };
        write_response(out.lock(), &resp);
        1
    });
    std::process::exit(code);
}

fn main_inner() -> i32 {
    let mut input = Vec::new();
    if io::stdin().read_to_end(&mut input).is_err() {
        eprintln!("cannot read stdin");
        return 1;
    }

    let response = match serde_json::from_slice::<Request>(&input) {
        Ok(req) => handle(req),
        Err(e) => Response {
            protocol: PROTOCOL,
            ok: false,
            op: "unknown".into(),
            result: None,
            error: Some(xcut_worker_media::protocol::ErrorBody {
                code: "json".into(),
                message: format!("cannot parse request: {e}"),
            }),
        },
    };

    let ok = response.ok;
    {
        let stdout = io::stdout();
        let mut lock = stdout.lock();
        write_response(&mut lock, &response);
    }
    if ok {
        0
    } else {
        1
    }
}

fn write_response<W: Write>(mut w: W, resp: &Response) {
    let mut data = serde_json::to_vec(resp).expect("serialize response");
    data.push(b'\n');
    let _ = w.write_all(&data);
    let _ = w.flush();
}

fn handle(req: Request) -> Response {
    if req.protocol != PROTOCOL {
        return Response::failure(
            &req.op,
            &WorkerError::Protocol(format!("want protocol {PROTOCOL}, got {}", req.protocol)),
        );
    }
    match req.op.as_str() {
        "describe" => {
            let d = Describe {
                name: NAME,
                version: VERSION,
                protocol: PROTOCOL,
                ops: OPS,
            };
            Response::success("describe", serde_json::to_value(d).expect("describe"))
        }
        "audio_rms" => {
            let params: AudioRmsParams = if req.params.is_null() {
                Default::default()
            } else {
                match serde_json::from_value(req.params.clone()) {
                    Ok(p) => p,
                    Err(e) => {
                        return Response::failure("audio_rms", &WorkerError::Params(format!("{e}")))
                    }
                }
            };
            match audio::audio_rms(&req.input, &params) {
                Ok(r) => {
                    let payload: AudioRmsResult = r;
                    Response::success("audio_rms", serde_json::to_value(payload).expect("rms"))
                }
                Err(e) => Response::failure("audio_rms", &e),
            }
        }
        other => Response::failure(other, &WorkerError::UnknownOp(other.to_string())),
    }
}
