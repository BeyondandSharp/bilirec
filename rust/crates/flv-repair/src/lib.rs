// SPDX-License-Identifier: GPL-3.0-or-later
//
// `flv-repair` library: FLV stream repair/analysis sidecar.
//
// This crate is a Rust port of `BililiveRecorder.Flv` (GPL-3.0-or-later), see
// `docs/PORTING.md` and `NOTICE`. Behavior contract: `docs/INTERFACES.md` §1.

pub mod amf;
pub mod analyze;
pub mod cli;
pub mod codec;
pub mod group;
pub mod log;
pub mod parser;
pub mod pipeline;
pub mod tag;
pub mod toolbox;
pub mod writer;

/// Crate version, also reported by `flvrepair --version`.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");
