// SPDX-License-Identifier: GPL-3.0-or-later
//
// Bilibili danmaku XML utilities: start-time extraction and merging.
// Reference behavior: `refs/BililiveRecorder/BililiveRecorder.ToolBox/Tool/DanmakuStartTime`
// and `.../DanmakuMerger` (GPL-3.0). Output element layout follows the in-tree Go writer
// `internal/services/danmaku/format_xml.go`.

use crate::log::{EventLog, LogSink};
use crate::toolbox::time;
use crate::toolbox::xml::{self, XmlEvent, XmlReader};
use crate::toolbox::{io_context, json_string, ToolboxError};
use std::fs;
use std::path::{Path, PathBuf};

const DANMAKU_ELEMENT_NAMES: [&str; 4] = ["d", "gift", "sc", "guard"];
const MAX_ENTRIES: usize = 4_000_000;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DanmakuKind {
    D,
    Gift,
    Sc,
    Guard,
}

impl DanmakuKind {
    fn from_name(name: &str) -> Option<DanmakuKind> {
        match name {
            "d" => Some(DanmakuKind::D),
            "gift" => Some(DanmakuKind::Gift),
            "sc" => Some(DanmakuKind::Sc),
            "guard" => Some(DanmakuKind::Guard),
            _ => None,
        }
    }

    fn name(self) -> &'static str {
        match self {
            DanmakuKind::D => "d",
            DanmakuKind::Gift => "gift",
            DanmakuKind::Sc => "sc",
            DanmakuKind::Guard => "guard",
        }
    }
}

#[derive(Debug, Clone)]
pub struct DanmakuEntry {
    pub kind: DanmakuKind,
    pub attributes: Vec<(String, String)>,
    pub text: String,
    /// Timestamp relative to the recording start, in milliseconds.
    pub timestamp_ms: i64,
}

#[derive(Debug, Clone, Default)]
pub struct DanmakuFile {
    pub record_info: Option<Vec<(String, String)>>,
    pub start_time: Option<String>,
    pub start_ms: Option<i64>,
    pub entries: Vec<DanmakuEntry>,
}

/// Read and parse one Bilibili danmaku XML file.
pub fn read_file(path: &Path) -> Result<DanmakuFile, ToolboxError> {
    let bytes = fs::read(path)
        .map_err(|error| io_context(&format!("cannot read {}", path.display()), error))?;
    parse_bytes(&bytes)
        .map_err(|message| ToolboxError::InvalidInput(format!("{}: {message}", path.display())))
}

fn parse_bytes(bytes: &[u8]) -> Result<DanmakuFile, String> {
    let mut reader = XmlReader::new(bytes);
    let mut file = DanmakuFile::default();
    let mut fallback_start: Option<String> = None;
    loop {
        match reader.next_event().map_err(|error| error.message)? {
            None => break,
            Some(XmlEvent::Text(_)) => {}
            Some(XmlEvent::End { .. }) => {}
            Some(XmlEvent::Start {
                name,
                attributes,
                self_closing,
            }) => {
                if name == "BililiveRecorderRecordInfo" {
                    if file.start_time.is_none() {
                        if let Some(value) = xml::find_attribute(&attributes, "start_time") {
                            file.start_time = Some(value.to_string());
                            file.start_ms = time::parse_rfc3339_ms(value);
                        }
                    }
                    if file.record_info.is_none() {
                        file.record_info = Some(attributes.clone());
                    }
                    if !self_closing {
                        xml::skip_element(&mut reader, &name).map_err(|error| error.message)?;
                    }
                } else if name == "i" {
                    // Document root: descend into it instead of skipping the whole file.
                    if self_closing {
                        return Ok(DanmakuFile::default());
                    }
                } else if let Some(kind) = DanmakuKind::from_name(&name) {
                    let text = if self_closing {
                        String::new()
                    } else {
                        xml::read_text_element(&mut reader, &name).map_err(|error| error.message)?
                    };
                    let timestamp_ms = extract_timestamp(kind, &attributes)
                        .ok_or_else(|| format!("<{name}> element without a valid timestamp"))?;
                    if file.entries.len() >= MAX_ENTRIES {
                        return Err("too many danmaku entries".to_string());
                    }
                    file.entries.push(DanmakuEntry {
                        kind,
                        attributes,
                        text,
                        timestamp_ms,
                    });
                } else {
                    if fallback_start.is_none() {
                        if let Some(value) = xml::find_attribute(&attributes, "starttime") {
                            fallback_start = Some(value.to_string());
                        }
                    }
                    if !self_closing {
                        xml::skip_element(&mut reader, &name).map_err(|error| error.message)?;
                    }
                }
            }
        }
    }
    if file.start_time.is_none() {
        if let Some(value) = fallback_start {
            file.start_ms = time::parse_rfc3339_ms(&value);
            file.start_time = Some(value);
        }
    }
    Ok(file)
}

fn extract_timestamp(kind: DanmakuKind, attributes: &[(String, String)]) -> Option<i64> {
    let raw = match kind {
        DanmakuKind::D => xml::find_attribute(attributes, "p")?.split(',').next()?,
        DanmakuKind::Gift | DanmakuKind::Sc | DanmakuKind::Guard => {
            xml::find_attribute(attributes, "ts")?
        }
    };
    let seconds: f64 = raw.trim().parse().ok()?;
    if !seconds.is_finite() {
        return None;
    }
    Some((seconds * 1000.0).round() as i64)
}

/// Run `danmaku-start-time --inputs ... [--json]`.
pub fn run_start_time(inputs: &[PathBuf], json: bool) -> Result<(), ToolboxError> {
    if inputs.is_empty() {
        return Err(ToolboxError::Args(
            "danmaku-start-time requires --inputs".to_string(),
        ));
    }
    let mut log = EventLog::new();
    let mut results: Vec<(PathBuf, String)> = Vec::new();
    for path in inputs {
        let file = read_file(path)?;
        match file.start_time {
            Some(start_time) => results.push((path.clone(), start_time)),
            None => log.event(
                "warn",
                "missing_start_time",
                Some(&format!("{}: no start_time attribute", path.display())),
            ),
        }
    }
    if results.is_empty() {
        return Err(ToolboxError::InvalidInput(
            "no start_time found in the given inputs".to_string(),
        ));
    }
    if json {
        let items: Vec<String> = results
            .iter()
            .map(|(path, start_time)| {
                format!(
                    "{{\"file\":{},\"starttime\":{}}}",
                    json_string(&path.display().to_string()),
                    json_string(start_time)
                )
            })
            .collect();
        println!("{{\"start_times\":[{}]}}", items.join(","));
    } else {
        let mut out = String::new();
        for (path, start_time) in &results {
            out.push_str(&path.display().to_string());
            out.push('\t');
            out.push_str(start_time);
            out.push('\n');
        }
        print!("{out}");
    }
    Ok(())
}

/// Run `danmaku-merge --output out.xml --inputs a.xml b.xml [...] [--offsets ms,...]`.
pub fn run_merge(
    output: &Path,
    inputs: &[PathBuf],
    offsets_ms: Option<&[i64]>,
) -> Result<(), ToolboxError> {
    if inputs.len() < 2 {
        return Err(ToolboxError::Args(
            "danmaku-merge requires at least 2 --inputs".to_string(),
        ));
    }
    if let Some(offsets) = offsets_ms {
        if offsets.len() != inputs.len() {
            return Err(ToolboxError::Args(format!(
                "--offsets has {} entries but --inputs has {}",
                offsets.len(),
                inputs.len()
            )));
        }
    }

    let mut files = Vec::with_capacity(inputs.len());
    for path in inputs {
        files.push(read_file(path)?);
    }

    let shifts: Vec<i64>;
    let base_index: usize;
    match offsets_ms {
        Some(offsets) => {
            shifts = offsets.to_vec();
            base_index = index_of_minimum(&shifts);
        }
        None => {
            let mut starts = Vec::with_capacity(files.len());
            for (index, file) in files.iter().enumerate() {
                let start = file.start_ms.ok_or_else(|| {
                    ToolboxError::InvalidInput(format!(
                        "{}: missing start_time (pass --offsets to merge without it)",
                        inputs[index].display()
                    ))
                })?;
                starts.push(start);
            }
            base_index = index_of_minimum(&starts);
            let base = starts[base_index];
            shifts = starts.iter().map(|start| start - base).collect();
        }
    }

    // The merged file keeps the earliest recording start time (FEATURES-V2 §5.3).
    let mut minimum_start: Option<(i64, String)> = None;
    for file in &files {
        if let (Some(start_ms), Some(start_time)) = (file.start_ms, &file.start_time) {
            if minimum_start
                .as_ref()
                .map(|(current, _)| start_ms < *current)
                .unwrap_or(true)
            {
                minimum_start = Some((start_ms, start_time.clone()));
            }
        }
    }

    let mut record_info = files[base_index].record_info.clone().unwrap_or_default();
    if let Some((_, start_time)) = &minimum_start {
        xml::set_attribute(&mut record_info, "start_time", start_time.clone());
    }

    let mut entries: Vec<(i64, usize, usize, &DanmakuEntry)> = Vec::new();
    for (index, file) in files.iter().enumerate() {
        for (order, entry) in file.entries.iter().enumerate() {
            let adjusted = entry.timestamp_ms.saturating_add(shifts[index]).max(0);
            entries.push((adjusted, index, order, entry));
        }
    }
    entries.sort_by_key(|(adjusted, input_index, order, _)| (*adjusted, *input_index, *order));

    let document = build_merged_xml(&record_info, &entries);
    fs::write(output, document.as_bytes())
        .map_err(|error| io_context(&format!("cannot write {}", output.display()), error))?;
    Ok(())
}

fn index_of_minimum(values: &[i64]) -> usize {
    let mut best = 0usize;
    for (index, value) in values.iter().enumerate() {
        if value < &values[best] {
            best = index;
        }
    }
    best
}

fn build_merged_xml(
    record_info: &[(String, String)],
    entries: &[(i64, usize, usize, &DanmakuEntry)],
) -> String {
    let mut out = String::new();
    out.push_str("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n");
    out.push_str("<i>\n");
    out.push_str("<chatserver>chat.bilibili.com</chatserver>\n");
    out.push_str("<chatid>0</chatid>\n");
    out.push_str("<mission>0</mission>\n");
    out.push_str("<maxlimit>1000</maxlimit>\n");
    out.push_str("<state>0</state>\n");
    out.push_str("<real_name>0</real_name>\n");
    out.push_str("<source>0</source>\n");
    let version = env!("CARGO_PKG_VERSION");
    let recorder = vec![("version".to_string(), version.to_string())];
    xml::push_start_tag(&mut out, "BililiveRecorder", &recorder, true, 0);
    xml::push_start_tag(&mut out, "BililiveRecorderRecordInfo", record_info, true, 0);

    for (adjusted, _, _, entry) in entries {
        let timestamp = time::format_seconds_3(*adjusted);
        let mut attributes = entry.attributes.clone();
        match entry.kind {
            DanmakuKind::D => {
                if let Some((_, value)) = attributes.iter_mut().find(|(key, _)| key == "p") {
                    let rest = value.split_once(',').map(|(_, rest)| rest).unwrap_or("");
                    *value = format!("{timestamp},{rest}");
                }
                xml::push_text_element(&mut out, entry.kind.name(), &attributes, &entry.text, 0);
            }
            DanmakuKind::Gift | DanmakuKind::Guard => {
                xml::set_attribute(&mut attributes, "ts", timestamp);
                xml::push_start_tag(&mut out, entry.kind.name(), &attributes, true, 0);
            }
            DanmakuKind::Sc => {
                xml::set_attribute(&mut attributes, "ts", timestamp);
                xml::push_text_element(&mut out, entry.kind.name(), &attributes, &entry.text, 0);
            }
        }
    }

    out.push_str("</i>\n");
    out
}

/// Validate that an element name is one of the known danmaku element names (used by tests).
pub fn is_danmaku_element(name: &str) -> bool {
    DANMAKU_ELEMENT_NAMES.contains(&name)
}

#[cfg(test)]
mod tests {
    use super::*;

    const SAMPLE: &str = r#"<?xml version="1.0" encoding="utf-8"?>
<i>
<chatserver>chat.bilibili.com</chatserver>
<BililiveRecorderRecordInfo roomid="5" name="主播&amp;x" start_time="2026-10-20T10:00:00+08:00"/>
<d p="1.000,1,25,16777215,1700000000,0,42,0" user="a&amp;b">hello &lt;3</d>
<gift ts="2.500" user="bob" uid="7" giftname="rocket" giftcount="2"/>
<sc ts="3.000" user="carol" uid="8" price="30">super</sc>
<guard ts="4.250" user="dave" uid="9" level="3" count="1"/>
</i>
"#;

    #[test]
    fn parses_sample_file() {
        let file = parse_bytes(SAMPLE.as_bytes()).expect("parse");
        assert_eq!(
            file.start_time.as_deref(),
            Some("2026-10-20T10:00:00+08:00")
        );
        assert_eq!(file.entries.len(), 4);
        assert_eq!(file.entries[0].kind, DanmakuKind::D);
        assert_eq!(file.entries[0].timestamp_ms, 1000);
        assert_eq!(file.entries[0].text, "hello <3");
        assert_eq!(file.entries[1].timestamp_ms, 2500);
        assert_eq!(file.entries[2].timestamp_ms, 3000);
        assert_eq!(file.entries[3].timestamp_ms, 4250);
    }

    #[test]
    fn merged_document_is_sorted_and_uses_min_start_time() {
        let mut file_a = parse_bytes(SAMPLE.as_bytes()).expect("a");
        let mut file_b = parse_bytes(SAMPLE.as_bytes()).expect("b");
        // Shift file B by two seconds and use an earlier start time for file A.
        file_a.start_time = Some("2026-10-20T10:00:00+08:00".to_string());
        file_a.start_ms = time::parse_rfc3339_ms("2026-10-20T10:00:00+08:00");
        file_b.start_time = Some("2026-10-20T10:00:02+08:00".to_string());
        file_b.start_ms = time::parse_rfc3339_ms("2026-10-20T10:00:02+08:00");
        let record_info = file_a.record_info.clone().unwrap_or_default();
        let entries = vec![(1000i64, 0usize, 0usize, &file_a.entries[0])];
        let document = build_merged_xml(&record_info, &entries);
        assert!(document.contains("start_time=\"2026-10-20T10:00:00+08:00\""));
        assert!(document.contains("<d p=\"1.000,1,25,16777215,1700000000,0,42,0\""));
        assert!(document.contains("hello &lt;3"));
        assert!(document.trim_end().ends_with("</i>"));
    }
}
