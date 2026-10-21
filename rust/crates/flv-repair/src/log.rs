// SPDX-License-Identifier: GPL-3.0-only
//
// Structured JSON-line logging to stderr for the `flvrepair` sidecar.

use std::io::Write;

/// Minimal logging sink. Implementations must never panic and never write to stdout.
pub trait LogSink {
    /// An anomaly of class R1..R8 was detected (only called for the first occurrence).
    fn class(&mut self, class: &'static str, count: u64);
    /// A generic event with an optional human readable message.
    fn event(&mut self, level: &str, event: &str, message: Option<&str>);
}

/// Logs one JSON object per line to stderr. Failures are ignored on purpose: a closed
/// stderr must never take the repair process down.
pub struct EventLog {
    stderr: std::io::Stderr,
}

impl EventLog {
    pub fn new() -> Self {
        EventLog {
            stderr: std::io::stderr(),
        }
    }

    fn line(&mut self, value: &str) {
        let mut lock = self.stderr.lock();
        // write! (not writeln!) adds the newline; ignore all I/O errors.
        let _ = writeln!(lock, "{value}");
    }

    /// Emit a pre-rendered JSON object as one line. The caller is responsible for the
    /// value being valid JSON.
    pub fn raw_line(&mut self, value: &str) {
        self.line(value);
    }
}

impl Default for EventLog {
    fn default() -> Self {
        Self::new()
    }
}

impl LogSink for EventLog {
    fn class(&mut self, class: &'static str, count: u64) {
        let event = class_event(class);
        let line = format!(
            "{{\"level\":\"warn\",\"event\":\"{}\",\"count\":{},\"class\":\"{}\"}}",
            escape(event),
            count,
            class
        );
        self.line(&line);
    }

    fn event(&mut self, level: &str, event: &str, message: Option<&str>) {
        let mut line = format!(
            "{{\"level\":\"{}\",\"event\":\"{}\"",
            escape(level),
            escape(event)
        );
        if let Some(message) = message {
            line.push_str(",\"message\":\"");
            line.push_str(&escape(message));
            line.push('"');
        }
        line.push('}');
        self.line(&line);
    }
}

/// Drops all log events. Used by `analyze` so stdout stays a single JSON document and
/// stderr stays quiet.
pub struct NullLog;

impl LogSink for NullLog {
    fn class(&mut self, _class: &'static str, _count: u64) {}
    fn event(&mut self, _level: &str, _event: &str, _message: Option<&str>) {}
}

pub fn class_event(class: &str) -> &'static str {
    match class {
        "R1" => "timestamp_jump",
        "R2" => "timestamp_rollback",
        "R3" => "duplicate_tags",
        "R4" => "filler_data",
        "R5" => "delayed_audio_header",
        "R6" => "metadata_header_issue",
        "R7" => "input_corruption",
        "R8" => "aac_hevc_anomaly",
        _ => "repair_anomaly",
    }
}

/// Escape a string for embedding in a JSON string literal.
pub fn escape(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    for ch in value.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn escapes_controls() {
        assert_eq!(escape("a\"b\\c\n"), "a\\\"b\\\\c\\n");
    }

    #[test]
    fn class_names_are_stable() {
        assert_eq!(class_event("R1"), "timestamp_jump");
        assert_eq!(class_event("R8"), "aac_hevc_anomaly");
    }
}
