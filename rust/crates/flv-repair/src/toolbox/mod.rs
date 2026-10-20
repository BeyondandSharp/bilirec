// SPDX-License-Identifier: GPL-3.0-or-later
//
// Toolbox subcommands (docs/FEATURES-V2.md §5.3): FLV tag export to the
// BililiveRecorder `XmlFlvFile` XML schema, plus Bilibili danmaku XML utilities.
// Behavior derived from `refs/BililiveRecorder/BililiveRecorder.ToolBox/Tool/**`
// (GPL-3.0) and the in-tree Go danmaku XML writer (`internal/services/danmaku`).

pub mod danmaku;
pub mod export;
pub mod time;
pub mod xml;
pub mod zip;

use std::fmt;

/// Error type for toolbox commands. Maps onto the process contract exit codes:
/// 1 = I/O error, 2 = invalid arguments, 3 = unrecoverable/invalid input data.
#[derive(Debug)]
pub enum ToolboxError {
    Io(String),
    Args(String),
    InvalidInput(String),
}

impl ToolboxError {
    pub fn exit_code(&self) -> u8 {
        match self {
            ToolboxError::Io(_) => 1,
            ToolboxError::Args(_) => 2,
            ToolboxError::InvalidInput(_) => 3,
        }
    }

    pub fn kind(&self) -> &'static str {
        match self {
            ToolboxError::Io(_) => "io_error",
            ToolboxError::Args(_) => "invalid_arguments",
            ToolboxError::InvalidInput(_) => "invalid_input",
        }
    }

    pub fn message(&self) -> &str {
        match self {
            ToolboxError::Io(message)
            | ToolboxError::Args(message)
            | ToolboxError::InvalidInput(message) => message,
        }
    }
}

impl fmt::Display for ToolboxError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.message())
    }
}

impl std::error::Error for ToolboxError {}

pub fn io_context(context: &str, error: std::io::Error) -> ToolboxError {
    ToolboxError::Io(format!("{context}: {error}"))
}

/// Escape a string for a JSON string literal (reuses the log module helper).
pub fn json_string(value: &str) -> String {
    format!("\"{}\"", crate::log::escape(value))
}
