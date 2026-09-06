//! Typed worker errors (library layer keeps errors typed per project policy).

use thiserror::Error;

#[derive(Debug, Error)]
pub enum WorkerError {
    #[error("invalid protocol: {0}")]
    Protocol(String),

    #[error("unknown op: {0}")]
    UnknownOp(String),

    #[error("bad params: {0}")]
    Params(String),

    #[error("io error: {0}")]
    Io(#[from] std::io::Error),

    #[error("json error: {0}")]
    Json(#[from] serde_json::Error),

    #[error("decode error: {0}")]
    Decode(String),

    #[error("no audio stream in input")]
    NoAudio,
}

impl WorkerError {
    /// Stable machine code mirrored by the Go side's error model.
    pub fn code(&self) -> &'static str {
        match self {
            WorkerError::Protocol(_) => "protocol",
            WorkerError::UnknownOp(_) => "unknown_op",
            WorkerError::Params(_) => "validation",
            WorkerError::Io(_) => "io",
            WorkerError::Json(_) => "json",
            WorkerError::Decode(_) => "decode",
            WorkerError::NoAudio => "no_audio",
        }
    }
}
