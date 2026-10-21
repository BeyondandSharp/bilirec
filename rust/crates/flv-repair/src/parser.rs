// SPDX-License-Identifier: GPL-3.0-only
//
// Incremental FLV tag reader with bounded buffering and best-effort resynchronisation.
// Behaviour derived from `refs/BililiveRecorder/BililiveRecorder.Flv/Parser/FlvTagPipeReader.cs`
// (GPL-3.0-only), extended with resync for corruption classes R7.

use crate::tag::{Tag, TagType, FLV_HEADER_LEN, PREVIOUS_TAG_SIZE_LEN, TAG_HEADER_LEN};
use std::io::{self, Read};

/// Default internal buffer cap (64 MiB), matching the CLI `--max-buffer` default.
pub const DEFAULT_MAX_BUFFER: usize = 64 * 1024 * 1024;
const READ_CHUNK: usize = 64 * 1024;
/// FLV tag sizes are 24-bit.
const MAX_TAG_SIZE: usize = 0x00ff_ffff;

/// Fatal reader error. `Unrecoverable` maps to exit code 3 in strict mode; in
/// best-effort mode the reader never returns it (it gives up and reports EOF instead).
#[derive(Debug)]
pub enum ParseError {
    Io(io::Error),
    Unrecoverable(&'static str),
}

impl std::fmt::Display for ParseError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ParseError::Io(error) => write!(f, "I/O error: {error}"),
            ParseError::Unrecoverable(message) => write!(f, "unrecoverable input: {message}"),
        }
    }
}

impl From<io::Error> for ParseError {
    fn from(error: io::Error) -> Self {
        ParseError::Io(error)
    }
}

/// Parsed FLV file header.
#[derive(Debug, Clone, Copy, Default)]
pub struct FlvHeader {
    pub version: u8,
    pub flags: u8,
    pub data_offset: u32,
    pub present: bool,
}

impl FlvHeader {
    pub fn has_audio(&self) -> bool {
        self.flags & 0x01 != 0
    }

    pub fn has_video(&self) -> bool {
        self.flags & 0x04 != 0
    }
}

/// Counters collected while reading, surfaced in stderr logs and `analyze`.
#[derive(Debug, Clone, Copy, Default)]
pub struct ParserStats {
    pub bytes_read: u64,
    pub tags: u64,
    pub garbage_bytes: u64,
    pub resyncs: u64,
    pub truncated_bytes: u64,
    pub bad_previous_sizes: u64,
    pub skipped_tags: u64,
    /// Number of individual corruption events; mapped to repair class R7.
    pub corruption_events: u64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum State {
    NeedHeader,
    NeedTag,
    Skip(usize),
}

enum StepResult {
    Tag(Tag),
    Progress,
    NeedMore,
}

/// Incremental FLV tag reader. `strict` selects whether genuinely invalid input is an
/// error (`Unrecoverable`) or is tolerated (best-effort: strip the unparseable bytes).
pub struct TagReader<R: Read> {
    inner: R,
    buffer: Vec<u8>,
    start: usize,
    scratch: Vec<u8>,
    eof: bool,
    gave_up: bool,
    strict: bool,
    max_buffer: usize,
    state: State,
    header: FlvHeader,
    pub stats: ParserStats,
}

impl<R: Read> TagReader<R> {
    pub fn new(inner: R, max_buffer: usize, strict: bool) -> Self {
        TagReader {
            inner,
            buffer: Vec::new(),
            start: 0,
            scratch: vec![0u8; READ_CHUNK],
            eof: false,
            gave_up: false,
            strict,
            max_buffer: max_buffer.max(1024),
            state: State::NeedHeader,
            header: FlvHeader::default(),
            stats: ParserStats::default(),
        }
    }

    pub fn header(&self) -> FlvHeader {
        self.header
    }

    /// Configured internal buffer cap (`--max-buffer`). Also used by the grouping layer
    /// to bound one in-memory tag group.
    pub fn max_buffer(&self) -> usize {
        self.max_buffer
    }

    /// Read the next complete tag. Returns `Ok(None)` on clean EOF (or, in best-effort
    /// mode, once the remaining input is unrecoverable).
    pub fn read_tag(&mut self) -> Result<Option<Tag>, ParseError> {
        loop {
            if let Some(tag) = self.pump()? {
                return Ok(Some(tag));
            }
            if self.eof || self.gave_up {
                self.drain_truncated_tail();
                return Ok(None);
            }
            self.compact();
            let read = self.fill()?;
            if read == 0 && !self.eof {
                // Internal buffer is full but no progress is possible: drop bytes.
                if !self.force_progress() {
                    self.drain_truncated_tail();
                    return Ok(None);
                }
            }
        }
    }

    fn available(&self) -> usize {
        self.buffer.len().saturating_sub(self.start)
    }

    fn data(&self) -> &[u8] {
        &self.buffer[self.start..]
    }

    fn compact(&mut self) {
        if self.start > 0 {
            self.buffer.drain(..self.start);
            self.start = 0;
        }
    }

    fn fill(&mut self) -> Result<usize, ParseError> {
        if self.available() >= self.max_buffer {
            return Ok(0);
        }
        let want = READ_CHUNK.min(self.max_buffer - self.available());
        let read = self.inner.read(&mut self.scratch[..want])?;
        if read == 0 {
            self.eof = true;
        } else {
            self.buffer.extend_from_slice(&self.scratch[..read]);
            self.stats.bytes_read = self.stats.bytes_read.saturating_add(read as u64);
        }
        Ok(read)
    }

    fn pump(&mut self) -> Result<Option<Tag>, ParseError> {
        // Each iteration either makes progress or returns; the cap protects against
        // pathological input that would otherwise spin.
        for _ in 0..10_000_000usize {
            match self.state {
                State::NeedHeader => {
                    if self.header.present {
                        self.state = State::NeedTag;
                        continue;
                    }
                    match self.header_step()? {
                        StepResult::Progress => continue,
                        _ => return Ok(None),
                    }
                }
                State::NeedTag => match self.tag_step()? {
                    StepResult::Tag(tag) => return Ok(Some(tag)),
                    StepResult::Progress => continue,
                    StepResult::NeedMore => return Ok(None),
                },
                State::Skip(remaining) => {
                    if remaining == 0 {
                        self.state = State::NeedTag;
                        continue;
                    }
                    let available = self.available();
                    if available == 0 {
                        return Ok(None);
                    }
                    let take = available.min(remaining);
                    self.start += take;
                    let left = remaining - take;
                    if left == 0 {
                        self.state = State::NeedTag;
                    } else {
                        self.state = State::Skip(left);
                        if self.eof {
                            self.stats.truncated_bytes =
                                self.stats.truncated_bytes.saturating_add(left as u64);
                            self.stats.corruption_events += 1;
                            self.state = State::NeedTag;
                        }
                    }
                    continue;
                }
            }
        }
        // Guard tripped: give up on this input.
        self.unrecoverable("input made no parse progress")?;
        Ok(None)
    }

    fn header_step(&mut self) -> Result<StepResult, ParseError> {
        let available = self.available();
        if available < 3 {
            return Ok(StepResult::NeedMore);
        }
        if self.data().get(..3) != Some(b"FLV") {
            // Try to find the FLV magic somewhere in the buffered data.
            if let Some(position) = find_subslice(self.data(), b"FLV") {
                if position > 0 {
                    self.record_garbage(position, false);
                    self.start += position;
                    return Ok(StepResult::Progress);
                }
            }
            if self.eof {
                if self.strict {
                    return Err(ParseError::Unrecoverable("input is not an FLV file"));
                }
                self.record_garbage(available, false);
                self.start += available;
                self.gave_up = true;
                return Ok(StepResult::Progress);
            }
            if available >= self.max_buffer {
                let drop = available.saturating_sub(2);
                self.record_garbage(drop, true);
                self.start += drop;
                return Ok(StepResult::Progress);
            }
            return Ok(StepResult::NeedMore);
        }

        if available < FLV_HEADER_LEN {
            if self.eof {
                self.stats.truncated_bytes =
                    self.stats.truncated_bytes.saturating_add(available as u64);
                self.stats.corruption_events += 1;
                self.start += available;
                return Ok(StepResult::Progress);
            }
            return Ok(StepResult::NeedMore);
        }

        let header_bytes = &self.data()[..FLV_HEADER_LEN];
        let version = header_bytes[3];
        let flags = header_bytes[4];
        let data_offset = u32::from_be_bytes([
            header_bytes[5],
            header_bytes[6],
            header_bytes[7],
            header_bytes[8],
        ]);
        if version != 1 || !(FLV_HEADER_LEN as u32..=4096).contains(&data_offset) {
            if self.strict {
                return Err(ParseError::Unrecoverable("not a supported FLV file"));
            }
            self.record_garbage(1, true);
            self.start += 1;
            return Ok(StepResult::Progress);
        }

        let total = data_offset as usize + PREVIOUS_TAG_SIZE_LEN;
        if available < total {
            if self.eof {
                self.stats.truncated_bytes =
                    self.stats.truncated_bytes.saturating_add(available as u64);
                self.stats.corruption_events += 1;
                self.start += available;
                return Ok(StepResult::Progress);
            }
            return Ok(StepResult::NeedMore);
        }
        let previous_size = u32::from_be_bytes([
            self.data()[data_offset as usize],
            self.data()[data_offset as usize + 1],
            self.data()[data_offset as usize + 2],
            self.data()[data_offset as usize + 3],
        ]);
        if previous_size != 0 {
            // Non-zero PreviousTagSize0 is a corruption but not fatal.
            self.stats.bad_previous_sizes += 1;
            self.stats.corruption_events += 1;
        }
        self.header = FlvHeader {
            version,
            flags,
            data_offset,
            present: true,
        };
        self.start += total;
        self.state = State::NeedTag;
        Ok(StepResult::Progress)
    }

    fn tag_step(&mut self) -> Result<StepResult, ParseError> {
        let available = self.available();
        if available < TAG_HEADER_LEN + PREVIOUS_TAG_SIZE_LEN {
            if self.eof && available > 0 {
                self.stats.truncated_bytes =
                    self.stats.truncated_bytes.saturating_add(available as u64);
                self.stats.corruption_events += 1;
                self.start += available;
            }
            return Ok(StepResult::NeedMore);
        }

        let Some(tag_type) = TagType::from_byte(self.data()[0]) else {
            return self.resync();
        };

        let size = u32::from_be_bytes([0, self.data()[1], self.data()[2], self.data()[3]]) as usize;
        let total = TAG_HEADER_LEN + size + PREVIOUS_TAG_SIZE_LEN;
        if size > MAX_TAG_SIZE || total > self.max_buffer {
            // Tag does not fit the configured buffer: skip it without buffering the body.
            self.stats.skipped_tags += 1;
            self.stats.corruption_events += 1;
            let available = self.available();
            let consume = available.min(total);
            self.start += consume;
            let left = total - consume;
            if left > 0 {
                self.state = State::Skip(left);
                self.stats.garbage_bytes = self.stats.garbage_bytes.saturating_add(left as u64);
            }
            return Ok(StepResult::Progress);
        }

        if available < total {
            if self.eof {
                // A complete tag body with a truncated PreviousTagSize is still usable:
                // emit the tag and drop only the partial tail.
                if available >= TAG_HEADER_LEN + size {
                    let timestamp = timestamp_from_bytes(&self.data()[4..8]);
                    let body = self.data()[TAG_HEADER_LEN..TAG_HEADER_LEN + size].to_vec();
                    self.stats.truncated_bytes = self
                        .stats
                        .truncated_bytes
                        .saturating_add((total - available) as u64);
                    self.stats.corruption_events += 1;
                    self.start += available;
                    self.stats.tags += 1;
                    let index = self.stats.tags;
                    return Ok(StepResult::Tag(Tag::new(tag_type, timestamp, body, index)));
                }
                self.stats.truncated_bytes =
                    self.stats.truncated_bytes.saturating_add(available as u64);
                self.stats.corruption_events += 1;
                self.start += available;
                return Ok(StepResult::Progress);
            }
            return Ok(StepResult::NeedMore);
        }

        let timestamp = timestamp_from_bytes(&self.data()[4..8]);
        let body = self.data()[TAG_HEADER_LEN..TAG_HEADER_LEN + size].to_vec();
        let previous_size = u32::from_be_bytes([
            self.data()[TAG_HEADER_LEN + size],
            self.data()[TAG_HEADER_LEN + size + 1],
            self.data()[TAG_HEADER_LEN + size + 2],
            self.data()[TAG_HEADER_LEN + size + 3],
        ]);
        if previous_size != (size as u32 + TAG_HEADER_LEN as u32) {
            self.stats.bad_previous_sizes += 1;
            self.stats.corruption_events += 1;
        }
        self.start += total;
        self.stats.tags += 1;
        let index = self.stats.tags;
        Ok(StepResult::Tag(Tag::new(tag_type, timestamp, body, index)))
    }

    fn resync(&mut self) -> Result<StepResult, ParseError> {
        let available = self.available();
        if available == 0 {
            return Ok(StepResult::NeedMore);
        }
        match find_resync_point(self.data(), self.max_buffer) {
            Some(ResyncPoint::Header(position)) => {
                self.record_garbage(position, false);
                self.start += position;
                self.state = State::NeedHeader;
                Ok(StepResult::Progress)
            }
            Some(ResyncPoint::Tag(position)) => {
                self.record_garbage(position, true);
                self.start += position;
                Ok(StepResult::Progress)
            }
            None => {
                if self.eof {
                    self.record_garbage(available, false);
                    self.start += available;
                    return Ok(StepResult::Progress);
                }
                // Keep a short tail: a tag header may straddle the buffer boundary.
                let keep = available.min(8);
                if available <= keep {
                    return Ok(StepResult::NeedMore);
                }
                let drop = available - keep;
                self.record_garbage(drop, true);
                self.start += drop;
                Ok(StepResult::Progress)
            }
        }
    }

    fn record_garbage(&mut self, bytes: usize, resync: bool) {
        self.stats.garbage_bytes = self.stats.garbage_bytes.saturating_add(bytes as u64);
        if resync {
            self.stats.resyncs += 1;
        }
        self.stats.corruption_events += 1;
    }

    fn drain_truncated_tail(&mut self) {
        let available = self.available();
        if available > 0 {
            self.stats.truncated_bytes =
                self.stats.truncated_bytes.saturating_add(available as u64);
            self.stats.corruption_events += 1;
            self.start += available;
        }
    }

    fn force_progress(&mut self) -> bool {
        let available = self.available();
        if available == 0 {
            return false;
        }
        let drop = available.min(16);
        self.record_garbage(drop, true);
        self.start += drop;
        true
    }

    fn unrecoverable(&mut self, message: &'static str) -> Result<(), ParseError> {
        if self.strict {
            Err(ParseError::Unrecoverable(message))
        } else {
            self.gave_up = true;
            Ok(())
        }
    }
}

fn timestamp_from_bytes(bytes: &[u8]) -> i64 {
    let value = (u32::from(bytes[3]) << 24)
        | (u32::from(bytes[0]) << 16)
        | (u32::from(bytes[1]) << 8)
        | u32::from(bytes[2]);
    i64::from(value as i32)
}

enum ResyncPoint {
    Header(usize),
    Tag(usize),
}

fn find_resync_point(data: &[u8], max_buffer: usize) -> Option<ResyncPoint> {
    if data.len() < 4 {
        return None;
    }
    for position in 1..data.len() {
        let tail = &data[position..];
        if tail.starts_with(b"FLV") {
            return Some(ResyncPoint::Header(position));
        }
        let tag_type = tail[0];
        if !matches!(tag_type, 8 | 9 | 18) || tail.len() < 4 {
            continue;
        }
        let size = u32::from_be_bytes([0, tail[1], tail[2], tail[3]]) as usize;
        if size > MAX_TAG_SIZE {
            continue;
        }
        let total = TAG_HEADER_LEN + size + PREVIOUS_TAG_SIZE_LEN;
        if total > max_buffer {
            continue;
        }
        if total <= tail.len() {
            let previous = u32::from_be_bytes([
                tail[TAG_HEADER_LEN + size],
                tail[TAG_HEADER_LEN + size + 1],
                tail[TAG_HEADER_LEN + size + 2],
                tail[TAG_HEADER_LEN + size + 3],
            ]);
            if previous == size as u32 + TAG_HEADER_LEN as u32 {
                return Some(ResyncPoint::Tag(position));
            }
        } else {
            // Plausible incomplete tag at the end of the buffered data.
            return Some(ResyncPoint::Tag(position));
        }
    }
    None
}

fn find_subslice(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    if needle.is_empty() || haystack.len() < needle.len() {
        return None;
    }
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

/// Build a minimal, valid FLV file header (version 1, given audio/video flags).
pub fn encode_file_header(out: &mut Vec<u8>, has_audio: bool, has_video: bool) {
    let mut flags = 0u8;
    if has_audio {
        flags |= 0x01;
    }
    if has_video {
        flags |= 0x04;
    }
    out.extend_from_slice(b"FLV");
    out.push(1);
    out.push(flags);
    out.extend_from_slice(&9u32.to_be_bytes());
    out.extend_from_slice(&0u32.to_be_bytes());
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tag::{encode_tag_into, TagType};

    fn simple_tag(tag_type: TagType, timestamp: i64, body: Vec<u8>) -> Vec<u8> {
        let tag = Tag::new(tag_type, timestamp, body, 0);
        let mut out = Vec::new();
        encode_tag_into(&tag, timestamp, &mut out);
        out
    }

    fn reader(data: &[u8]) -> TagReader<&[u8]> {
        TagReader::new(data, 1 << 20, false)
    }

    #[test]
    fn reads_clean_file() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, true);
        bytes.extend_from_slice(&simple_tag(TagType::Script, 0, vec![2, 0, 10]));
        bytes.extend_from_slice(&simple_tag(
            TagType::Video,
            100,
            vec![0x17, 0x01, 0, 0, 0, 0, 0, 0, 0],
        ));
        let mut reader = reader(&bytes);
        let mut tags = Vec::new();
        while let Some(tag) = reader.read_tag().unwrap() {
            tags.push(tag);
        }
        assert_eq!(tags.len(), 2);
        assert_eq!(tags[0].tag_type, TagType::Script);
        assert_eq!(tags[1].timestamp, 100);
        assert_eq!(reader.stats.corruption_events, 0);
        assert!(reader.header().has_video());
    }

    #[test]
    fn resyncs_garbage_between_tags() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, true);
        bytes.extend_from_slice(&simple_tag(TagType::Audio, 0, vec![0xaf, 0x01, 0]));
        bytes.extend_from_slice(b"garbagegarbage");
        bytes.extend_from_slice(&simple_tag(TagType::Video, 40, vec![0x17, 0x01, 0, 0, 0]));
        let mut reader = reader(&bytes);
        let mut tags = Vec::new();
        while let Some(tag) = reader.read_tag().unwrap() {
            tags.push(tag);
        }
        assert_eq!(tags.len(), 2);
        assert!(reader.stats.corruption_events > 0);
    }

    #[test]
    fn drops_truncated_tail() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, true);
        bytes.extend_from_slice(&simple_tag(TagType::Audio, 0, vec![0xaf, 0x01, 0]));
        let full = bytes.len();
        bytes.truncate(full - 2);
        let mut reader = reader(&bytes);
        let mut count = 0;
        while let Some(_tag) = reader.read_tag().unwrap() {
            count += 1;
        }
        assert_eq!(count, 1);
        assert!(reader.stats.corruption_events > 0);
    }

    #[test]
    fn strict_mode_rejects_non_flv() {
        let mut reader = TagReader::new(&b"not an flv file at all"[..], 1 << 20, true);
        // Best effort reader would return Ok(None) here; strict must error.
        let mut errored = false;
        for _ in 0..10 {
            match reader.read_tag() {
                Ok(Some(_)) => {}
                Ok(None) => break,
                Err(_) => {
                    errored = true;
                    break;
                }
            }
        }
        assert!(errored);
    }

    #[test]
    fn best_effort_tolerates_non_flv() {
        let mut reader = TagReader::new(&b"not an flv file at all"[..], 1 << 20, false);
        assert!(reader.read_tag().unwrap().is_none());
    }

    #[test]
    fn skips_oversized_tag() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, true);
        // tag body claims 60000 bytes but the configured cap is 32 KiB.
        let mut header = vec![9u8];
        header.extend_from_slice(&60000u32.to_be_bytes()[1..]);
        header.extend_from_slice(&[0, 0, 0, 0, 0, 0, 0]);
        bytes.extend_from_slice(&header);
        bytes.extend_from_slice(&vec![0u8; 60000]);
        bytes.extend_from_slice(&60011u32.to_be_bytes());
        bytes.extend_from_slice(&simple_tag(TagType::Audio, 500, vec![0xaf, 0x01, 0]));
        let mut reader = TagReader::new(&bytes[..], 32 * 1024, false);
        let mut tags = Vec::new();
        while let Some(tag) = reader.read_tag().unwrap() {
            tags.push(tag);
        }
        assert_eq!(tags.len(), 1);
        assert_eq!(tags[0].timestamp, 500);
        assert_eq!(reader.stats.skipped_tags, 1);
    }

    #[test]
    fn handles_negative_timestamps() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, false);
        let tag = Tag::new(TagType::Audio, 0, vec![0xaf, 0x01, 0], 0);
        let mut tag_bytes = Vec::new();
        encode_tag_into(&tag, 0, &mut tag_bytes);
        // -5 as a signed 32-bit FLV timestamp: ext=0xFF, low24=0xFFFFFB.
        tag_bytes[4] = 0xff;
        tag_bytes[5] = 0xff;
        tag_bytes[6] = 0xfb;
        tag_bytes[7] = 0xff;
        bytes.extend_from_slice(&tag_bytes);
        let mut reader = reader(&bytes);
        let tag = reader.read_tag().unwrap().unwrap();
        assert_eq!(tag.timestamp, -5);
    }
}
