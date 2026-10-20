// SPDX-License-Identifier: GPL-3.0-or-later
//
// Shared test helpers: minimal JSON reader (for `.expect.json`), synthetic FLV builder,
// strict FLV validator, and binary/library drivers. No external dependencies by design.

#![allow(dead_code)]

use flv_repair::analyze::{analyze, AnalyzeReport};
use flv_repair::group::GroupReader;
use flv_repair::log::NullLog;
use flv_repair::parser::TagReader;
use flv_repair::pipeline::{RepairPipeline, RepairStats};
use flv_repair::tag::Tag;
use flv_repair::writer::{FlvWriter, Sink};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Command, Output, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};

// ---------------------------------------------------------------------------------------
// Minimal JSON parser (only what `.expect.json` needs).
// ---------------------------------------------------------------------------------------

#[derive(Debug, Clone, PartialEq)]
pub enum Json {
    Null,
    Bool(bool),
    Number(f64),
    String(String),
    Array(Vec<Json>),
    Object(Vec<(String, Json)>),
}

impl Json {
    pub fn parse(text: &str) -> Result<Json, String> {
        let mut parser = JsonParser {
            bytes: text.as_bytes(),
            pos: 0,
        };
        let value = parser.value()?;
        parser.skip_whitespace();
        if parser.pos != parser.bytes.len() {
            return Err(format!("trailing data at {}", parser.pos));
        }
        Ok(value)
    }

    pub fn get(&self, key: &str) -> Option<&Json> {
        match self {
            Json::Object(entries) => entries
                .iter()
                .find(|(name, _)| name == key)
                .map(|(_, value)| value),
            _ => None,
        }
    }

    pub fn index(&self, index: usize) -> Option<&Json> {
        match self {
            Json::Array(values) => values.get(index),
            _ => None,
        }
    }

    pub fn as_bool(&self) -> Option<bool> {
        match self {
            Json::Bool(value) => Some(*value),
            _ => None,
        }
    }

    pub fn as_f64(&self) -> Option<f64> {
        match self {
            Json::Number(value) => Some(*value),
            _ => None,
        }
    }

    pub fn as_u64(&self) -> Option<u64> {
        self.as_f64().map(|value| value as u64)
    }

    pub fn as_str(&self) -> Option<&str> {
        match self {
            Json::String(value) => Some(value),
            _ => None,
        }
    }

    pub fn as_array(&self) -> Option<&[Json]> {
        match self {
            Json::Array(values) => Some(values),
            _ => None,
        }
    }
}

struct JsonParser<'a> {
    bytes: &'a [u8],
    pos: usize,
}

impl<'a> JsonParser<'a> {
    fn skip_whitespace(&mut self) {
        while matches!(self.bytes.get(self.pos), Some(b' ' | b'\t' | b'\n' | b'\r')) {
            self.pos += 1;
        }
    }

    fn byte(&mut self) -> Result<u8, String> {
        let byte = *self
            .bytes
            .get(self.pos)
            .ok_or_else(|| "unexpected end of JSON".to_string())?;
        self.pos += 1;
        Ok(byte)
    }

    fn expect(&mut self, byte: u8) -> Result<(), String> {
        let actual = self.byte()?;
        if actual == byte {
            Ok(())
        } else {
            Err(format!(
                "expected {} got {} at {}",
                byte as char, actual as char, self.pos
            ))
        }
    }

    fn value(&mut self) -> Result<Json, String> {
        self.skip_whitespace();
        let byte = *self
            .bytes
            .get(self.pos)
            .ok_or_else(|| "unexpected end of JSON".to_string())?;
        match byte {
            b'{' => self.object(),
            b'[' => self.array(),
            b'"' => Ok(Json::String(self.string()?)),
            b't' => {
                self.literal("true")?;
                Ok(Json::Bool(true))
            }
            b'f' => {
                self.literal("false")?;
                Ok(Json::Bool(false))
            }
            b'n' => {
                self.literal("null")?;
                Ok(Json::Null)
            }
            b'-' | b'0'..=b'9' => self.number(),
            other => Err(format!("unexpected byte {} at {}", other as char, self.pos)),
        }
    }

    fn literal(&mut self, expected: &str) -> Result<(), String> {
        if self.bytes[self.pos..].starts_with(expected.as_bytes()) {
            self.pos += expected.len();
            Ok(())
        } else {
            Err(format!("expected {expected} at {}", self.pos))
        }
    }

    fn number(&mut self) -> Result<Json, String> {
        let start = self.pos;
        while matches!(
            self.bytes.get(self.pos),
            Some(b'-' | b'+' | b'.' | b'e' | b'E' | b'0'..=b'9')
        ) {
            self.pos += 1;
        }
        let text = std::str::from_utf8(&self.bytes[start..self.pos])
            .map_err(|_| "invalid number".to_string())?;
        text.parse::<f64>()
            .map(Json::Number)
            .map_err(|_| format!("invalid number {text}"))
    }

    fn string(&mut self) -> Result<String, String> {
        self.expect(b'"')?;
        let mut out = String::new();
        loop {
            let byte = self.byte()?;
            match byte {
                b'"' => return Ok(out),
                b'\\' => match self.byte()? {
                    b'"' => out.push('"'),
                    b'\\' => out.push('\\'),
                    b'/' => out.push('/'),
                    b'n' => out.push('\n'),
                    b'r' => out.push('\r'),
                    b't' => out.push('\t'),
                    b'b' => out.push('\u{0008}'),
                    b'f' => out.push('\u{000c}'),
                    b'u' => {
                        let mut code = 0u32;
                        for _ in 0..4 {
                            let digit = self.byte()? as char;
                            code = code * 16
                                + digit
                                    .to_digit(16)
                                    .ok_or_else(|| format!("invalid unicode escape {digit}"))?;
                        }
                        out.push(char::from_u32(code).unwrap_or('\u{fffd}'));
                    }
                    other => return Err(format!("invalid escape {}", other as char)),
                },
                other if other < 0x20 => return Err("control character in string".to_string()),
                other => out.push(other as char),
            }
        }
    }

    fn array(&mut self) -> Result<Json, String> {
        self.expect(b'[')?;
        let mut values = Vec::new();
        self.skip_whitespace();
        if self.bytes.get(self.pos) == Some(&b']') {
            self.pos += 1;
            return Ok(Json::Array(values));
        }
        loop {
            values.push(self.value()?);
            self.skip_whitespace();
            match self.byte()? {
                b',' => continue,
                b']' => return Ok(Json::Array(values)),
                other => return Err(format!("unexpected {} in array", other as char)),
            }
        }
    }

    fn object(&mut self) -> Result<Json, String> {
        self.expect(b'{')?;
        let mut entries = Vec::new();
        self.skip_whitespace();
        if self.bytes.get(self.pos) == Some(&b'}') {
            self.pos += 1;
            return Ok(Json::Object(entries));
        }
        loop {
            self.skip_whitespace();
            let key = self.string()?;
            self.skip_whitespace();
            self.expect(b':')?;
            let value = self.value()?;
            entries.push((key, value));
            self.skip_whitespace();
            match self.byte()? {
                b',' => continue,
                b'}' => return Ok(Json::Object(entries)),
                other => return Err(format!("unexpected {} in object", other as char)),
            }
        }
    }
}

// ---------------------------------------------------------------------------------------
// Synthetic FLV construction / strict validation.
// ---------------------------------------------------------------------------------------

#[derive(Debug, Clone)]
pub struct RawTag {
    pub tag_type: u8,
    pub timestamp: i64,
    pub body: Vec<u8>,
}

pub struct ParsedFlv {
    pub flags: u8,
    pub tags: Vec<RawTag>,
}

pub fn build_flv(flags: u8, tags: &[RawTag]) -> Vec<u8> {
    let mut out = Vec::new();
    out.extend_from_slice(b"FLV");
    out.push(1);
    out.push(flags);
    out.extend_from_slice(&9u32.to_be_bytes());
    out.extend_from_slice(&0u32.to_be_bytes());
    for tag in tags {
        encode_raw_tag(tag, &mut out);
    }
    out
}

pub fn encode_raw_tag(tag: &RawTag, out: &mut Vec<u8>) {
    let timestamp = tag.timestamp.clamp(0, u32::MAX as i64) as u32;
    let size = tag.body.len();
    out.push(tag.tag_type);
    out.extend_from_slice(&(size as u32).to_be_bytes()[1..]);
    out.extend_from_slice(&timestamp.to_be_bytes()[1..4]);
    out.push((timestamp >> 24) as u8);
    out.extend_from_slice(&[0, 0, 0]);
    out.extend_from_slice(&tag.body);
    out.extend_from_slice(&((size + 11) as u32).to_be_bytes());
}

/// Strict FLV structure validation: magic, version, header size, tag types and the
/// `PreviousTagSize` chain. Fails on trailing bytes.
pub fn parse_flv(bytes: &[u8]) -> Result<ParsedFlv, String> {
    if bytes.len() < 13 {
        return Err("file shorter than FLV header".to_string());
    }
    if &bytes[0..3] != b"FLV" {
        return Err("missing FLV magic".to_string());
    }
    if bytes[3] != 1 {
        return Err(format!("unexpected version {}", bytes[3]));
    }
    let flags = bytes[4];
    let data_offset = u32::from_be_bytes([bytes[5], bytes[6], bytes[7], bytes[8]]) as usize;
    if data_offset != 9 {
        return Err(format!("unexpected data offset {data_offset}"));
    }
    if data_offset + 4 > bytes.len() {
        return Err("missing PreviousTagSize0".to_string());
    }
    if bytes[data_offset..data_offset + 4] != [0, 0, 0, 0] {
        return Err("PreviousTagSize0 is not zero".to_string());
    }
    let mut position = data_offset + 4;
    let mut tags = Vec::new();
    while position < bytes.len() {
        if position + 11 > bytes.len() {
            return Err(format!("partial tag header at {position}"));
        }
        let tag_type = bytes[position];
        if !matches!(tag_type, 8 | 9 | 18) {
            return Err(format!("unexpected tag type {tag_type} at {position}"));
        }
        let size = u32::from_be_bytes([
            0,
            bytes[position + 1],
            bytes[position + 2],
            bytes[position + 3],
        ]) as usize;
        let timestamp = i64::from(
            (u32::from(bytes[position + 7]) << 24)
                | (u32::from(bytes[position + 4]) << 16)
                | (u32::from(bytes[position + 5]) << 8)
                | u32::from(bytes[position + 6]),
        );
        let body_start = position + 11;
        let body_end = body_start
            .checked_add(size)
            .ok_or_else(|| "tag size overflow".to_string())?;
        let previous_end = body_end.checked_add(4).ok_or("tag overflow")?;
        if previous_end > bytes.len() {
            return Err(format!("truncated tag at {position}"));
        }
        let previous = u32::from_be_bytes([
            bytes[body_end],
            bytes[body_end + 1],
            bytes[body_end + 2],
            bytes[body_end + 3],
        ]);
        if previous as usize != size + 11 {
            return Err(format!(
                "bad PreviousTagSize at {position}: {previous} != {}",
                size + 11
            ));
        }
        tags.push(RawTag {
            tag_type,
            timestamp,
            body: bytes[body_start..body_end].to_vec(),
        });
        position = previous_end;
    }
    if position != bytes.len() {
        return Err(format!("trailing {} bytes", bytes.len() - position));
    }
    Ok(ParsedFlv { flags, tags })
}

// ---------------------------------------------------------------------------------------
// FLV tag classification helpers for validators (mirrors the crate's rules).
// ---------------------------------------------------------------------------------------

pub fn is_enhanced_video(tag: &RawTag) -> bool {
    tag.tag_type == 9
        && tag
            .body
            .first()
            .map(|byte| byte & 0x80 != 0)
            .unwrap_or(false)
}

pub fn video_fourcc(tag: &RawTag) -> Option<[u8; 4]> {
    if !is_enhanced_video(tag) || tag.body.len() < 5 {
        return None;
    }
    Some([tag.body[1], tag.body[2], tag.body[3], tag.body[4]])
}

pub fn is_video_header(tag: &RawTag) -> bool {
    if tag.tag_type != 9 || tag.body.len() <= 2 {
        return false;
    }
    if tag.body[0] & 0x80 != 0 {
        return tag.body.len() >= 5 && (tag.body[0] & 0x0f) == 0;
    }
    tag.body[1] == 0
}

pub fn is_video_end(tag: &RawTag) -> bool {
    if tag.tag_type != 9 || tag.body.len() <= 2 {
        return false;
    }
    if tag.body[0] & 0x80 != 0 {
        return tag.body.len() >= 5 && (tag.body[0] & 0x0f) == 2;
    }
    tag.body[1] == 2
}

pub fn is_video_keyframe(tag: &RawTag) -> bool {
    if tag.tag_type != 9 || tag.body.is_empty() {
        return false;
    }
    if tag.body[0] & 0x80 != 0 {
        return (tag.body[0] & 0x70) >> 4 == 1;
    }
    tag.body[0] & 0xf0 == 0x10
}

pub fn is_audio_header(tag: &RawTag) -> bool {
    tag.tag_type == 8 && tag.body.len() > 2 && (tag.body[0] >> 4) == 10 && tag.body[1] == 0
}

pub fn is_video_data(tag: &RawTag) -> bool {
    tag.tag_type == 9 && !is_video_header(tag) && !is_video_end(tag)
}

pub fn is_audio_data(tag: &RawTag) -> bool {
    tag.tag_type == 8 && !is_audio_header(tag)
}

pub fn media_tags(tags: &[RawTag]) -> Vec<&RawTag> {
    tags.iter()
        .filter(|tag| {
            (tag.tag_type == 9 && is_video_data(tag)) || (tag.tag_type == 8 && is_audio_data(tag))
        })
        .collect()
}

pub fn track_timestamps(tags: &[RawTag], video: bool) -> Vec<i64> {
    tags.iter()
        .filter(|tag| {
            if video {
                is_video_data(tag)
            } else {
                is_audio_data(tag)
            }
        })
        .map(|tag| tag.timestamp)
        .collect()
}

pub fn is_sorted(values: &[i64]) -> bool {
    values.windows(2).all(|pair| pair[1] >= pair[0])
}

// ---------------------------------------------------------------------------------------
// Library drivers.
// ---------------------------------------------------------------------------------------

#[derive(Clone, Default)]
struct SharedSink {
    data: std::rc::Rc<std::cell::RefCell<Vec<u8>>>,
}

impl Write for SharedSink {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.data.borrow_mut().extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

impl Sink for SharedSink {}

/// Repair a whole input in memory through the library pipeline (best-effort).
pub fn repair_library(input: &[u8]) -> (Vec<u8>, RepairStats) {
    repair_library_with_limit(input, 8 * 1024 * 1024)
}

pub fn repair_library_with_limit(input: &[u8], max_buffer: usize) -> (Vec<u8>, RepairStats) {
    repair_library_with_options(input, max_buffer, true)
}

/// Repair with the new `--write-metadata` option (FEATURES-V2 §5.1).
pub fn repair_library_with_options(
    input: &[u8],
    max_buffer: usize,
    write_metadata: bool,
) -> (Vec<u8>, RepairStats) {
    let reader = TagReader::new(input, max_buffer, false);
    let mut groups = GroupReader::new(reader);
    let mut pipeline = RepairPipeline::new();
    let mut log = NullLog;
    let sink = SharedSink::default();
    let sink_handle = sink.clone();
    let mut writer = FlvWriter::new(Box::new(sink), max_buffer, write_metadata);
    loop {
        let group = match groups.next_group() {
            Ok(Some(group)) => group,
            Ok(None) => break,
            Err(error) => panic!("parse error: {error}"),
        };
        let actions = pipeline.process(group, &mut log);
        for action in &actions {
            writer.write_action(action).expect("write action");
        }
    }
    pipeline.add_parser_corruption(groups.reader().stats.corruption_events, &mut log);
    let actions = pipeline.finish(&mut log);
    for action in &actions {
        writer.write_action(action).expect("write action");
    }
    writer.finish().expect("finish writer");
    // Streaming sinks are not seekable, so `finish` does not patch anything: the bytes
    // collected so far are the complete output.
    let bytes = sink_handle.data.borrow().clone();
    (bytes, pipeline.stats)
}

/// Analyze bytes through the library pass.
pub fn analyze_library(input: &[u8]) -> AnalyzeReport {
    let reader = TagReader::new(input, 8 * 1024 * 1024, false);
    analyze(reader).expect("analyze")
}

/// Group the tags of a parsed FLV and return one hash per data group.
pub fn data_group_hashes(parsed: &ParsedFlv) -> Vec<u64> {
    let mut bytes = Vec::new();
    bytes.extend_from_slice(b"FLV");
    bytes.push(1);
    bytes.push(parsed.flags);
    bytes.extend_from_slice(&9u32.to_be_bytes());
    bytes.extend_from_slice(&0u32.to_be_bytes());
    for tag in &parsed.tags {
        encode_raw_tag(tag, &mut bytes);
    }
    let reader = TagReader::new(&bytes[..], 8 * 1024 * 1024, false);
    let mut groups = GroupReader::new(reader);
    let mut hashes = Vec::new();
    while let Some(group) = groups.next_group().expect("group") {
        if let flv_repair::group::Group::Data(tags) = &group {
            if let Some(first) = tags.first() {
                let _ = first;
            }
            hashes.push(hash_tags(tags));
        }
    }
    hashes
}

fn hash_tags(tags: &[Tag]) -> u64 {
    let mut hash: u64 = 0xcbf2_9ce4_8422_2325;
    let mut mix = |value: u64| {
        hash ^= value;
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    };
    mix(tags.len() as u64);
    for tag in tags {
        mix(tag.data_hash());
    }
    hash
}

/// Count length-prefixed filler NAL units (H.264 type 12) in video data payloads.
pub fn count_filler_nalus(tags: &[RawTag]) -> usize {
    let mut count = 0;
    for tag in tags.iter().filter(|tag| is_video_data(tag)) {
        if is_enhanced_video(tag) || tag.body.len() < 6 {
            continue;
        }
        let mut position = 5usize;
        while position + 4 <= tag.body.len() {
            let length = u32::from_be_bytes([
                tag.body[position],
                tag.body[position + 1],
                tag.body[position + 2],
                tag.body[position + 3],
            ]) as usize;
            if length == 0 || position + 4 + length > tag.body.len() {
                break;
            }
            if tag.body[position + 4] & 0x1f == 12 {
                count += 1;
            }
            position += 4 + length;
        }
    }
    count
}

// ---------------------------------------------------------------------------------------
// Binary drivers.
// ---------------------------------------------------------------------------------------

pub fn binary() -> PathBuf {
    PathBuf::from(env!("CARGO_BIN_EXE_flvrepair"))
}

static COUNTER: AtomicU64 = AtomicU64::new(0);

pub struct TempDir {
    path: PathBuf,
}

impl TempDir {
    pub fn new() -> TempDir {
        let unique = COUNTER.fetch_add(1, Ordering::SeqCst);
        let path =
            std::env::temp_dir().join(format!("flvrepair-test-{}-{unique}", std::process::id()));
        std::fs::create_dir_all(&path).expect("create temp dir");
        TempDir { path }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn file(&self, name: &str) -> PathBuf {
        self.path.join(name)
    }
}

impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.path);
    }
}

/// Run the binary with optional stdin bytes.
pub fn run_binary(args: &[&str], stdin: Option<Vec<u8>>) -> Output {
    run_binary_in(None, args, stdin)
}

/// Run the binary with an optional working directory and stdin bytes.
pub fn run_binary_in(dir: Option<&Path>, args: &[&str], stdin: Option<Vec<u8>>) -> Output {
    let mut command = Command::new(binary());
    command.args(args);
    if let Some(dir) = dir {
        command.current_dir(dir);
    }
    command.stdin(if stdin.is_some() {
        Stdio::piped()
    } else {
        Stdio::null()
    });
    command.stdout(Stdio::piped());
    command.stderr(Stdio::piped());
    let mut child = command.spawn().expect("spawn flvrepair");
    let writer = stdin.map(|bytes| {
        let mut handle = child.stdin.take().expect("child stdin");
        std::thread::spawn(move || {
            let _ = handle.write_all(&bytes);
        })
    });
    let output = child.wait_with_output().expect("wait for flvrepair");
    if let Some(writer) = writer {
        let _ = writer.join();
    }
    output
}

/// Repair `input` with `flvrepair file` into a temp dir and return the output bytes.
pub fn repair_via_cli_file(input: &[u8], extra_args: &[&str]) -> Vec<u8> {
    let temp = TempDir::new();
    let input_path = temp.file("in.flv");
    let output_path = temp.file("out.flv");
    std::fs::write(&input_path, input).expect("write input");
    let mut args = vec![
        "file".to_string(),
        "--input".to_string(),
        input_path.display().to_string(),
        "--output".to_string(),
        output_path.display().to_string(),
    ];
    args.extend(extra_args.iter().map(|arg| (*arg).to_string()));
    let mut command = Command::new(binary());
    command.args(&args);
    let output = command.output().expect("run flvrepair file");
    assert!(
        output.status.success(),
        "file mode failed: {:?} {}",
        output.status,
        String::from_utf8_lossy(&output.stderr)
    );
    std::fs::read(&output_path).expect("read repaired output")
}

/// Parse analyze JSON from a file path through the binary.
pub fn analyze_json_file(path: &Path) -> Json {
    let output = run_binary(
        &["analyze", "--input", &path.display().to_string(), "--json"],
        None,
    );
    assert!(
        output.status.success(),
        "analyze failed: {:?} {}",
        output.status,
        String::from_utf8_lossy(&output.stderr)
    );
    Json::parse(&String::from_utf8_lossy(&output.stdout)).expect("valid analyze JSON")
}

/// Extract the repaired metadata duration (seconds) from a parsed FLV, if present.
pub fn metadata_duration(parsed: &ParsedFlv) -> Option<f64> {
    for tag in &parsed.tags {
        if tag.tag_type != 18 {
            continue;
        }
        if let Ok(values) = flv_repair::amf::parse_script_body(&tag.body) {
            if let Some(duration) = flv_repair::amf::metadata_duration(&values) {
                return Some(duration);
            }
        }
    }
    None
}

/// Read all bytes from a reader (test helper).
pub fn read_all(mut reader: impl Read) -> Vec<u8> {
    let mut bytes = Vec::new();
    let _ = reader.read_to_end(&mut bytes);
    bytes
}

/// Repository root (workspace top level in the shared checkout).
pub fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("..")
        .join("..")
        .join("..")
        .canonicalize()
        .unwrap_or_else(|_| PathBuf::from(env!("CARGO_MANIFEST_DIR")))
}

pub fn fixtures_dir() -> PathBuf {
    repo_root().join("testdata").join("flv").join("fixtures")
}

pub fn base_dir() -> PathBuf {
    repo_root().join("testdata").join("flv").join("base")
}

// ---------------------------------------------------------------------------------------
// Synthetic AVC/AAC/HEVC stream builder.
// ---------------------------------------------------------------------------------------

pub fn avc_config_body() -> Vec<u8> {
    // 5-byte FLV video header + AVCDecoderConfigurationRecord.
    let sps: &[u8] = &[
        0x67, 0x64, 0x00, 0x1e, 0xac, 0xd9, 0x40, 0x50, 0x1e, 0xd0, 0x0f, 0x12, 0x26, 0xa0,
    ];
    let pps: &[u8] = &[0x68, 0xeb, 0xe3, 0xcb, 0x22, 0xc0];
    let mut body = vec![0x17, 0x00, 0, 0, 0, 1, 0x64, 0x00, 0x1e, 0xff, 0xe1];
    body.extend_from_slice(&(sps.len() as u16).to_be_bytes());
    body.extend_from_slice(sps);
    body.push(1);
    body.extend_from_slice(&(pps.len() as u16).to_be_bytes());
    body.extend_from_slice(pps);
    body
}

pub fn hevc_config_body(enhanced: bool) -> Vec<u8> {
    // Minimal HEVCDecoderConfigurationRecord with one SPS array entry.
    let sps: &[u8] = &[
        0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03, 0x00, 0x90, 0x00, 0x00, 0x03, 0x00, 0x00,
        0x03, 0x00, 0x5d, 0xac, 0x59,
    ];
    let mut record = vec![1u8, 0x01, 0x60, 0, 0, 0, 0x90, 0, 0, 0, 0, 0, 0x5d];
    record.extend_from_slice(&[0xf0, 0x00, 0xfc, 0xfd, 0xf8, 0xf8, 0x00, 0x00, 0x0f, 0x03]);
    record.push(1); // numOfArrays
    record.push(0x20 | 33); // SPS array
    record.extend_from_slice(&1u16.to_be_bytes());
    record.extend_from_slice(&(sps.len() as u16).to_be_bytes());
    record.extend_from_slice(sps);
    if enhanced {
        let mut body = vec![0x90, b'h', b'v', b'c', b'1'];
        body.extend_from_slice(&record);
        body
    } else {
        let mut body = vec![0x1c, 0x00, 0, 0, 0];
        body.extend_from_slice(&record);
        body
    }
}

pub fn avc_video_frame(timestamp: i64, keyframe: bool, nalu_type: u8, payload: &[u8]) -> RawTag {
    let mut body = vec![if keyframe { 0x17 } else { 0x27 }, 0x01, 0, 0, 0];
    body.extend_from_slice(&((payload.len() + 1) as u32).to_be_bytes());
    body.push(nalu_type);
    body.extend_from_slice(payload);
    RawTag {
        tag_type: 9,
        timestamp,
        body,
    }
}

/// AVC video frame with an arbitrary NALU list (used to inject filler NALUs).
pub fn avc_frame_with_nalus(timestamp: i64, keyframe: bool, nalus: &[(u8, &[u8])]) -> RawTag {
    let mut body = vec![if keyframe { 0x17 } else { 0x27 }, 0x01, 0, 0, 0];
    for (nalu_type, payload) in nalus {
        body.extend_from_slice(&((payload.len() + 1) as u32).to_be_bytes());
        body.push(*nalu_type);
        body.extend_from_slice(payload);
    }
    RawTag {
        tag_type: 9,
        timestamp,
        body,
    }
}

/// Enhanced-FLV metadata packet (packet type 4, `hvc1` FourCC) carrying AMF data.
pub fn enhanced_metadata_packet() -> RawTag {
    let mut body = vec![0xd4, b'h', b'v', b'c', b'1'];
    // Minimal AMF0: string "colorInfo" + object "colorConfig" with no entries.
    body.extend_from_slice(&[0x02, 0x00, 0x09]);
    body.extend_from_slice(b"colorInfo");
    body.extend_from_slice(&[0x03, 0x00, 0x0b]);
    body.extend_from_slice(b"colorConfig");
    body.extend_from_slice(&[0x03, 0x00, 0x00, 0x09, 0x00, 0x00, 0x09]);
    RawTag {
        tag_type: 9,
        timestamp: 0,
        body,
    }
}

/// Legacy (non-Enhanced-FLV) HEVC video frame, codec id 12.
pub fn legacy_hevc_frame(timestamp: i64, keyframe: bool, nalu_payload: &[u8]) -> RawTag {
    let mut body = vec![if keyframe { 0x1c } else { 0x2c }, 0x01, 0, 0, 0];
    body.extend_from_slice(&((nalu_payload.len()) as u32).to_be_bytes());
    body.extend_from_slice(nalu_payload);
    RawTag {
        tag_type: 9,
        timestamp,
        body,
    }
}

pub fn hevc_video_frame(timestamp: i64, keyframe: bool, payload: &[u8]) -> RawTag {
    let mut body = vec![if keyframe { 0x91 } else { 0xa1 }, b'h', b'v', b'c', b'1'];
    body.extend_from_slice(&[0, 0, 0]);
    body.extend_from_slice(payload);
    RawTag {
        tag_type: 9,
        timestamp,
        body,
    }
}

pub fn audio_header(timestamp: i64) -> RawTag {
    RawTag {
        tag_type: 8,
        timestamp,
        body: vec![0xaf, 0x00, 0x12, 0x10],
    }
}

pub fn audio_frame(timestamp: i64, payload: u8) -> RawTag {
    RawTag {
        tag_type: 8,
        timestamp,
        body: vec![0xaf, 0x01, payload, 0x21, 0x00],
    }
}

pub fn metadata_tag(duration: Option<f64>) -> RawTag {
    use flv_repair::amf::{AmfValue, Metadata};
    let mut entries: Vec<(String, AmfValue)> =
        vec![("videocodecid".to_string(), AmfValue::Number(7.0))];
    if let Some(duration) = duration {
        entries.push(("duration".to_string(), AmfValue::Number(duration)));
    }
    let metadata = Metadata {
        entries,
        duration,
        had_extra_value: false,
    };
    let values = flv_repair::amf::normalize_metadata(&metadata, duration.unwrap_or(0.0));
    let mut body = Vec::new();
    flv_repair::amf::write_script_body(&values, &mut body).expect("serialize metadata");
    RawTag {
        tag_type: 18,
        timestamp: 0,
        body,
    }
}

pub fn zero_size_tag(tag_type: u8, timestamp: i64) -> RawTag {
    RawTag {
        tag_type,
        timestamp,
        body: Vec::new(),
    }
}

pub fn end_tag(timestamp: i64) -> RawTag {
    RawTag {
        tag_type: 9,
        timestamp,
        body: vec![0x17, 0x02, 0, 0, 0],
    }
}

/// A small clean AVC+AAC stream (3 s, ~30 fps video, ~43 ms audio), interleaved by
/// timestamp like a real live FLV stream.
pub fn clean_avc_stream() -> Vec<u8> {
    let mut tags = vec![
        metadata_tag(Some(3.0)),
        RawTag {
            tag_type: 9,
            timestamp: 0,
            body: avc_config_body(),
        },
        audio_header(0),
    ];
    let mut frames: Vec<(i64, u8, RawTag)> = Vec::new();
    for index in 0..90i64 {
        let timestamp = index * 33;
        frames.push((
            timestamp,
            0,
            avc_video_frame(
                timestamp,
                index % 30 == 0,
                if index % 30 == 0 { 5 } else { 1 },
                &[index as u8; 8],
            ),
        ));
    }
    for index in 0..70i64 {
        let timestamp = index * 43;
        frames.push((timestamp, 1, audio_frame(timestamp, index as u8)));
    }
    frames.sort_by_key(|(timestamp, order, _)| (*timestamp, *order));
    tags.extend(frames.into_iter().map(|(_, _, tag)| tag));
    build_flv(0x05, &tags)
}
