//! XCut Rust media analysis worker.
//!
//! Protocol v1 (see docs/ARCHITECTURE.md): one JSON request on stdin, one
//! JSON response on stdout. The worker is a pure function over its input —
//! no ambient state — so the Go core can treat it as an optional, crash-
//! isolated accelerator (DECISIONS D2).

pub mod error;
pub mod protocol;
pub mod audio;

pub use error::WorkerError;
