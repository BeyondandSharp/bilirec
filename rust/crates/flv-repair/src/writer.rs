// SPDX-License-Identifier: GPL-3.0-only
//
// FLV output writer: correct file header, `PreviousTagSize` fields, metadata duration
// patching. Ported from `refs/BililiveRecorder/BililiveRecorder.Flv/Writer/**`
// (GPL-3.0-only), simplified to a single output stream instead of file splitting.

use crate::amf::{self, AmfValue};
use crate::parser::encode_file_header;
use crate::pipeline::Action;
use crate::tag::{encode_tag_into, Tag, TagType};
use std::fs::File;
use std::io::{self, Seek, SeekFrom, Write};

/// Output sink. File outputs support in-place patching of the header flags and the
/// metadata duration; streaming outputs (stdout) do not.
pub trait Sink: Write {
    fn seekable(&self) -> bool {
        false
    }

    fn patch(&mut self, _position: u64, _bytes: &[u8]) -> io::Result<()> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "output is not seekable",
        ))
    }
}

/// Seekable file sink used by `flvrepair file`.
pub struct FileSink {
    file: File,
}

impl FileSink {
    pub fn new(file: File) -> Self {
        FileSink { file }
    }
}

impl Write for FileSink {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.file.write(buf)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.file.flush()
    }
}

impl Sink for FileSink {
    fn seekable(&self) -> bool {
        true
    }

    fn patch(&mut self, position: u64, bytes: &[u8]) -> io::Result<()> {
        self.file.flush()?;
        let end = self.file.seek(SeekFrom::End(0))?;
        self.file.seek(SeekFrom::Start(position))?;
        self.file.write_all(bytes)?;
        self.file.seek(SeekFrom::Start(end))?;
        Ok(())
    }
}

/// Non-seekable streaming sink used by `flvrepair stream`.
pub struct StreamSink<W: Write> {
    inner: W,
}

impl<W: Write> StreamSink<W> {
    pub fn new(inner: W) -> Self {
        StreamSink { inner }
    }
}

impl<W: Write> Write for StreamSink<W> {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.inner.write(buf)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

impl<W: Write> Sink for StreamSink<W> {}

#[derive(Debug, Clone, Copy, Default)]
pub struct WriterStats {
    /// Non-keyframe video tags dropped while waiting for the first keyframe.
    pub dropped_leading_video: u64,
    /// True when no onMetaData tag existed in the input and one was synthesized.
    pub metadata_synthesized: bool,
    pub tags_out: u64,
    pub bytes_out: u64,
}

/// Writes repaired actions as a single valid FLV stream.
pub struct FlvWriter {
    sink: Box<dyn Sink>,
    max_buffer: usize,
    /// When false (`--write-metadata drop`) script/onMetaData tags are never emitted
    /// and no duration patch is performed (FEATURES-V2 §5.1).
    write_metadata: bool,
    started: bool,
    script: Option<Tag>,
    video_header: Option<Tag>,
    audio_header: Option<Tag>,
    duration_patch: Option<u64>,
    flags_patch: Option<u64>,
    max_timestamp: i64,
    has_audio: bool,
    has_video: bool,
    video_started: bool,
    pending_video: Vec<Tag>,
    pending_video_bytes: usize,
    /// Data held back until the first tag can be written with all known sequence
    /// headers (repairs delayed headers without losing stream order).
    pending_start: Vec<Tag>,
    pending_start_bytes: usize,
    pub stats: WriterStats,
}

impl FlvWriter {
    pub fn new(sink: Box<dyn Sink>, max_buffer: usize, write_metadata: bool) -> Self {
        FlvWriter {
            sink,
            max_buffer: max_buffer.max(1024),
            write_metadata,
            started: false,
            script: None,
            video_header: None,
            audio_header: None,
            duration_patch: None,
            flags_patch: None,
            max_timestamp: 0,
            has_audio: false,
            has_video: false,
            video_started: false,
            pending_video: Vec::new(),
            pending_video_bytes: 0,
            pending_start: Vec::new(),
            pending_start_bytes: 0,
            stats: WriterStats::default(),
        }
    }

    pub fn bytes_out(&self) -> u64 {
        self.stats.bytes_out
    }

    /// Whether script/onMetaData tags are written (`--write-metadata keep`).
    pub fn write_metadata(&self) -> bool {
        self.write_metadata
    }

    pub fn write_action(&mut self, action: &Action) -> io::Result<()> {
        match action {
            Action::Script(tag) => {
                if !self.write_metadata {
                    // FEATURES-V2 §5.1: drop mode never writes script/onMetaData tags.
                    return Ok(());
                }
                if self.started {
                    let mut buffer = Vec::new();
                    encode_tag_into(tag, 0, &mut buffer);
                    self.write_bytes(&buffer)?;
                    self.flush()?;
                } else {
                    self.script = Some(tag.clone());
                }
            }
            Action::Headers(tags) => {
                if self.started {
                    let mut buffer = Vec::new();
                    for tag in tags {
                        encode_tag_into(tag, 0, &mut buffer);
                        self.stats.tags_out += 1;
                    }
                    if !buffer.is_empty() {
                        self.write_bytes(&buffer)?;
                        self.flush()?;
                    }
                } else {
                    for tag in tags {
                        self.store_header(tag);
                    }
                    self.maybe_start_pending()?;
                }
            }
            Action::Data(tags) => self.write_data(tags)?,
            Action::End(tag) => {
                self.start_with_pending()?;
                let mut buffer = Vec::new();
                encode_tag_into(tag, tag.timestamp, &mut buffer);
                self.stats.tags_out += 1;
                self.write_bytes(&buffer)?;
                self.flush()?;
            }
        }
        Ok(())
    }

    fn store_header(&mut self, tag: &Tag) {
        match tag.tag_type {
            TagType::Video => {
                self.video_header = Some(tag.clone());
                self.has_video = true;
            }
            TagType::Audio => {
                self.audio_header = Some(tag.clone());
                self.has_audio = true;
            }
            TagType::Script => {}
        }
    }

    fn ensure_started(&mut self, observed: Option<&[Tag]>) -> io::Result<()> {
        if self.started {
            return Ok(());
        }
        self.started = true;
        if let Some(tags) = observed {
            for tag in tags {
                match tag.tag_type {
                    TagType::Video => self.has_video = true,
                    TagType::Audio => self.has_audio = true,
                    TagType::Script => {}
                }
            }
        }

        let mut buffer = Vec::new();
        encode_file_header(&mut buffer, self.has_audio, self.has_video);
        self.flags_patch = Some(4);

        if self.write_metadata {
            let script = match self.script.take() {
                Some(tag) => tag,
                None => {
                    self.stats.metadata_synthesized = true;
                    synthesize_metadata()
                }
            };
            let script_position = self.stats.bytes_out + buffer.len() as u64;
            if let Some(offset) = amf::find_duration_offset(&script.data) {
                self.duration_patch = Some(script_position + 11 + offset as u64);
            }
            encode_tag_into(&script, 0, &mut buffer);
            self.stats.tags_out += 1;
        } else {
            // Drop mode: do not synthesize and drop any buffered script tag.
            self.script = None;
        }

        if let Some(header) = self.video_header.take() {
            encode_tag_into(&header, 0, &mut buffer);
            self.stats.tags_out += 1;
        }
        if let Some(header) = self.audio_header.take() {
            encode_tag_into(&header, 0, &mut buffer);
            self.stats.tags_out += 1;
        }
        self.write_bytes(&buffer)?;
        self.flush()
    }

    fn write_data(&mut self, tags: &[Tag]) -> io::Result<()> {
        if !self.started {
            for tag in tags {
                self.pending_start_bytes = self.pending_start_bytes.saturating_add(tag.data.len());
                self.pending_start.push(tag.clone());
            }
            self.maybe_start_pending()?;
            return Ok(());
        }
        let mut buffer = Vec::new();
        for tag in tags {
            self.encode_data_tag(tag, &mut buffer);
        }
        if !buffer.is_empty() {
            self.write_bytes(&buffer)?;
            self.flush()?;
        }
        Ok(())
    }

    /// Whether held-back data can be written: every track with buffered data has its
    /// sequence header, or the hold limit is exceeded (best effort for headerless input).
    fn can_start(&self) -> bool {
        if self.pending_start.is_empty() {
            return true;
        }
        let mut has_audio_data = false;
        let mut has_video_data = false;
        for tag in &self.pending_start {
            match tag.tag_type {
                TagType::Audio => has_audio_data = true,
                TagType::Video => has_video_data = true,
                TagType::Script => {}
            }
        }
        let audio_ready = !has_audio_data || self.audio_header.is_some();
        let video_ready = !has_video_data || self.video_header.is_some();
        audio_ready && video_ready
            || self.pending_start.len() >= 300
            || self.pending_start_bytes >= (self.max_buffer / 8).min(2 * 1024 * 1024)
    }

    fn maybe_start_pending(&mut self) -> io::Result<()> {
        if !self.started && !self.pending_start.is_empty() && self.can_start() {
            self.start_with_pending()?;
        }
        Ok(())
    }

    fn start_with_pending(&mut self) -> io::Result<()> {
        let pending = std::mem::take(&mut self.pending_start);
        self.pending_start_bytes = 0;
        self.ensure_started(Some(&pending))?;
        if pending.is_empty() {
            return Ok(());
        }
        let mut buffer = Vec::new();
        for tag in &pending {
            self.encode_data_tag(tag, &mut buffer);
        }
        if !buffer.is_empty() {
            self.write_bytes(&buffer)?;
            self.flush()?;
        }
        Ok(())
    }

    fn encode_data_tag(&mut self, tag: &Tag, buffer: &mut Vec<u8>) {
        if tag.tag_type == TagType::Video
            && !self.video_started
            && (tag.is_coded_video_frame() || !tag.is_enhanced_video())
        {
            if !tag.is_coded_video_frame() {
                // Non-frame video payload (e.g. Enhanced-FLV metadata packet): write it
                // untouched, it must not participate in the keyframe hold-back logic.
                self.push_tag_bytes(tag, buffer);
                return;
            }
            if tag.flags.keyframe {
                self.stats.dropped_leading_video += self.pending_video.len() as u64;
                self.pending_video.clear();
                self.pending_video_bytes = 0;
                self.video_started = true;
            } else if self.pending_video.len() < 300
                && self.pending_video_bytes.saturating_add(tag.data.len()) <= self.max_buffer / 4
            {
                self.pending_video_bytes = self.pending_video_bytes.saturating_add(tag.data.len());
                self.pending_video.push(tag.clone());
                return;
            } else {
                // No keyframe showed up in the hold window: emit best effort.
                let held = std::mem::take(&mut self.pending_video);
                self.pending_video_bytes = 0;
                for held_tag in &held {
                    self.push_tag_bytes(held_tag, buffer);
                }
                self.video_started = true;
            }
        }
        if tag.tag_type == TagType::Video
            && (tag.is_coded_video_frame() || !tag.is_enhanced_video())
        {
            self.video_started = true;
        }
        self.push_tag_bytes(tag, buffer);
    }

    fn push_tag_bytes(&mut self, tag: &Tag, buffer: &mut Vec<u8>) {
        encode_tag_into(tag, tag.timestamp, buffer);
        self.max_timestamp = self.max_timestamp.max(tag.timestamp);
        match tag.tag_type {
            TagType::Video => self.has_video = true,
            TagType::Audio => self.has_audio = true,
            TagType::Script => {}
        }
        self.stats.tags_out += 1;
    }

    fn write_bytes(&mut self, bytes: &[u8]) -> io::Result<()> {
        self.sink.write_all(bytes)?;
        self.stats.bytes_out = self.stats.bytes_out.saturating_add(bytes.len() as u64);
        Ok(())
    }

    fn flush(&mut self) -> io::Result<()> {
        self.sink.flush()
    }

    /// Flush the file start, held video tags, and patch seekable outputs.
    pub fn finish(&mut self) -> io::Result<()> {
        self.start_with_pending()?;
        if !self.pending_video.is_empty() {
            let mut buffer = Vec::new();
            let held = std::mem::take(&mut self.pending_video);
            self.pending_video_bytes = 0;
            for tag in &held {
                self.push_tag_bytes(tag, &mut buffer);
            }
            self.write_bytes(&buffer)?;
        }
        if self.sink.seekable() {
            if let Some(position) = self.flags_patch {
                let mut flags = 0u8;
                if self.has_audio {
                    flags |= 0x01;
                }
                if self.has_video {
                    flags |= 0x04;
                }
                self.sink.patch(position, &[flags])?;
            }
            if let Some(position) = self.duration_patch {
                let duration = (self.max_timestamp.max(0) as f64) / 1000.0;
                self.sink
                    .patch(position, &duration.to_bits().to_be_bytes())?;
            }
        }
        self.flush()
    }
}

fn synthesize_metadata() -> Tag {
    let values = vec![
        AmfValue::String(b"onMetaData".to_vec()),
        AmfValue::EcmaArray(vec![("duration".to_string(), AmfValue::Number(0.0))]),
    ];
    let mut body = Vec::new();
    // The value set above is always serializable.
    let _ = amf::write_script_body(&values, &mut body);
    Tag::new(TagType::Script, 0, body, 0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tag::Tag;

    struct VecSink {
        data: Vec<u8>,
    }

    impl Write for VecSink {
        fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
            self.data.extend_from_slice(buf);
            Ok(buf.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    impl Sink for VecSink {}

    fn metadata_script() -> Tag {
        let mut body = Vec::new();
        let values = vec![
            AmfValue::String(b"onMetaData".to_vec()),
            AmfValue::EcmaArray(vec![("duration".to_string(), AmfValue::Number(0.0))]),
        ];
        amf::write_script_body(&values, &mut body).unwrap();
        Tag::new(TagType::Script, 0, body, 0)
    }

    #[test]
    fn writes_valid_file_structure() {
        let mut writer = FlvWriter::new(Box::new(VecSink { data: Vec::new() }), 1 << 20, true);
        writer
            .write_action(&Action::Script(metadata_script()))
            .unwrap();
        let video_header = Tag::new(TagType::Video, 0, vec![0x17, 0x00, 0, 0, 0, 1], 0);
        writer
            .write_action(&Action::Headers(vec![video_header]))
            .unwrap();
        let keyframe = Tag::new(TagType::Video, 0, vec![0x17, 0x01, 0, 0, 0, 9], 0);
        writer.write_action(&Action::Data(vec![keyframe])).unwrap();
        writer.finish().unwrap();
        // header + script + video header + keyframe
        assert_eq!(writer.stats.tags_out, 3);
        assert!(writer.bytes_out() > 13);
        assert!(!writer.stats.metadata_synthesized);
    }

    #[test]
    fn synthesizes_metadata_when_missing() {
        let mut writer = FlvWriter::new(Box::new(VecSink { data: Vec::new() }), 1 << 20, true);
        let keyframe = Tag::new(TagType::Video, 0, vec![0x17, 0x01, 0, 0, 0, 9], 0);
        writer.write_action(&Action::Data(vec![keyframe])).unwrap();
        writer.finish().unwrap();
        assert!(writer.stats.metadata_synthesized);
    }

    #[test]
    fn drops_leading_non_keyframes_until_keyframe() {
        let mut writer = FlvWriter::new(Box::new(VecSink { data: Vec::new() }), 1 << 20, true);
        let video_header = Tag::new(TagType::Video, 0, vec![0x17, 0x00, 0, 0, 0, 1], 0);
        writer
            .write_action(&Action::Headers(vec![video_header]))
            .unwrap();
        let non_key = Tag::new(TagType::Video, 0, vec![0x27, 0x01, 0, 0, 0, 1], 0);
        writer.write_action(&Action::Data(vec![non_key])).unwrap();
        assert_eq!(writer.stats.dropped_leading_video, 0); // still held
        let key = Tag::new(TagType::Video, 33, vec![0x17, 0x01, 0, 0, 0, 2], 0);
        writer.write_action(&Action::Data(vec![key])).unwrap();
        assert_eq!(writer.stats.dropped_leading_video, 1);
        writer.finish().unwrap();
    }
}
