// SPDX-License-Identifier: GPL-3.0-or-later
//
// Repair pipeline implementing the ported BililiveRecorder FLV rules:
//   R1/R2 timestamp jump + rollback repair (UpdateTimestampJumpRule, UpdateTimestampOffsetRule)
//   R3 duplicated tag groups (RemoveDuplicatedChunkRule)
//   R4 filler data / AVC end-of-sequence / zero-size tags (RemoveFillerDataRule)
//   R5 delayed audio sequence headers (HandleDelayedAudioHeaderRule)
//   R6 onMetaData / header flags normalization (HandleNewScriptRule, header writer)
//   R7 parser resync (handled by the reader, reported as class R7)
//   R8 HEVC-in-FLV / AAC timestamp anomalies (reported alongside R2/R3)
//
// The rules run in the same order as `AddDefaultRules()` in the C# reference, but the
// output is a single FLV stream (the reference splits files; `flvrepair` rewrites one file).

use crate::amf;
use crate::codec;
use crate::group::Group;
use crate::log::LogSink;
use crate::tag::{Tag, TagType};
use std::collections::VecDeque;

pub const CLASS_R1: usize = 0;
pub const CLASS_R2: usize = 1;
pub const CLASS_R3: usize = 2;
pub const CLASS_R4: usize = 3;
pub const CLASS_R5: usize = 4;
pub const CLASS_R6: usize = 5;
pub const CLASS_R7: usize = 6;
pub const CLASS_R8: usize = 7;

pub const CLASS_NAMES: [&str; 8] = ["R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8"];

/// Reference constants from UpdateTimestampJumpRule.
const JUMP_THRESHOLD: i64 = 500;
const AUDIO_DURATION_FALLBACK: i64 = 22;
const AUDIO_DURATION_MIN: i64 = 20;
const AUDIO_DURATION_MAX: i64 = 24;
const VIDEO_DURATION_FALLBACK: i64 = 33;
const VIDEO_DURATION_MIN: i64 = 15;
const VIDEO_DURATION_MAX: i64 = 50;
/// Reference constant from RemoveDuplicatedChunkRule.
const DUPLICATE_HISTORY: usize = 16;

/// Actions emitted by the repair pipeline, consumed by the writer (or discarded by analyze).
#[derive(Debug)]
pub enum Action {
    Script(Tag),
    Headers(Vec<Tag>),
    Data(Vec<Tag>),
    End(Tag),
}

/// Counters for the repair classes and stream statistics.
#[derive(Debug, Clone, Default)]
pub struct RepairStats {
    counts: [u64; 8],
    pub groups_in: u64,
    pub groups_dropped: u64,
    pub tags_in: u64,
    pub tags_out: u64,
}

impl RepairStats {
    pub fn count(&self, class: usize) -> u64 {
        self.counts[class]
    }

    pub fn add(&mut self, class: usize) -> u64 {
        self.counts[class] = self.counts[class].saturating_add(1);
        self.counts[class]
    }

    pub fn add_many(&mut self, class: usize, count: u64) -> u64 {
        self.counts[class] = self.counts[class].saturating_add(count);
        self.counts[class]
    }

    pub fn total(&self) -> u64 {
        self.counts.iter().sum()
    }

    pub fn anomalies(&self) -> Vec<(&'static str, u64)> {
        CLASS_NAMES
            .iter()
            .enumerate()
            .filter(|(index, _)| self.counts[*index] > 0)
            .map(|(index, name)| (*name, self.counts[index]))
            .collect()
    }
}

#[derive(Debug, Default)]
struct TimestampStore {
    first_chunk: bool,
    next_target: i64,
    last_original: i64,
    current_offset: i64,
}

#[derive(Debug, Default)]
struct HeaderSelection {
    video: Option<Tag>,
    audio: Option<Tag>,
    multiple_different: bool,
}

/// Main repair pipeline. Feed it tag groups, collect actions, then call `finish`.
pub struct RepairPipeline {
    strict_header_flags: u8,
    input_flags_set: bool,
    timestamp: TimestampStore,
    last_video_header: Option<Vec<u8>>,
    last_audio_header: Option<Vec<u8>>,
    has_video_header: bool,
    has_audio_header: bool,
    has_video_data: bool,
    has_audio_data: bool,
    first_metadata: Option<amf::Metadata>,
    script_seen: bool,
    metadata_duration_zero: bool,
    dedupe: VecDeque<u64>,
    /// Last emitted audio data tag (timestamp, payload hash) for the R8 adjacent
    /// duplicate extension of PORTING §8.3.
    last_audio_emitted: Option<(i64, u64)>,
    pending_end: Option<Tag>,
    last_output_timestamp: [i64; 2], // [audio, video]
    pub stats: RepairStats,
    finished: bool,
}

impl RepairPipeline {
    pub fn new() -> Self {
        RepairPipeline {
            strict_header_flags: 0,
            input_flags_set: false,
            timestamp: TimestampStore {
                first_chunk: true,
                ..TimestampStore::default()
            },
            last_video_header: None,
            last_audio_header: None,
            has_video_header: false,
            has_audio_header: false,
            has_video_data: false,
            has_audio_data: false,
            first_metadata: None,
            script_seen: false,
            metadata_duration_zero: false,
            dedupe: VecDeque::with_capacity(DUPLICATE_HISTORY + 1),
            last_audio_emitted: None,
            pending_end: None,
            last_output_timestamp: [0, 0],
            stats: RepairStats::default(),
            finished: false,
        }
    }

    /// Input FLV header flags (0x04 video, 0x01 audio). Used to detect R6.
    pub fn set_input_flags(&mut self, flags: u8) {
        self.strict_header_flags = flags;
        self.input_flags_set = true;
    }

    /// Add parser corruption events (class R7) discovered by the tag reader.
    pub fn add_parser_corruption(&mut self, events: u64, log: &mut dyn LogSink) {
        if events == 0 {
            return;
        }
        let before = self.stats.count(CLASS_R7);
        self.stats.add_many(CLASS_R7, events);
        if before == 0 {
            log.class(CLASS_NAMES[CLASS_R7], events);
        }
    }

    fn count_class(&mut self, class: usize, log: &mut dyn LogSink) {
        let before = self.stats.count(class);
        self.stats.add(class);
        if before == 0 {
            log.class(CLASS_NAMES[class], 1);
        }
    }

    /// Process one tag group. Returns the actions to write (all may be dropped).
    pub fn process(&mut self, group: Group, log: &mut dyn LogSink) -> Vec<Action> {
        let mut actions = Vec::new();
        self.stats.groups_in += 1;
        self.stats.tags_in += group.tag_count() as u64;

        // A previously held end-of-sequence tag is only kept when it is the last tag of
        // the file; any following group means it was spurious filler (R4).
        if self.pending_end.is_some() {
            self.pending_end = None;
            self.count_class(CLASS_R4, log);
            log.event(
                "debug",
                "dropped_mid_stream_end_tag",
                Some("removed AVC/HEVC end-of-sequence tag before more media data"),
            );
        }

        match group {
            Group::End(tag) => {
                let tag = self.adjust_end_timestamp(tag);
                self.pending_end = Some(tag);
            }
            Group::Script(tag) => self.handle_script(tag, &mut actions, log),
            Group::Header(tags) => self.handle_headers(tags, &mut actions, log),
            Group::Data(tags) => self.handle_data(tags, &mut actions, log),
        }
        actions
    }

    /// Emit the last pending end-of-sequence tag and final statistics.
    pub fn finish(&mut self, log: &mut dyn LogSink) -> Vec<Action> {
        let mut actions = Vec::new();
        if self.finished {
            return actions;
        }
        self.finished = true;
        if let Some(tag) = self.pending_end.take() {
            actions.push(Action::End(tag));
        }
        if !self.script_seen {
            // No usable onMetaData was seen; the writer synthesizes one.
            self.count_class(CLASS_R6, log);
            log.event(
                "warn",
                "missing_metadata",
                Some("no onMetaData script tag found; synthesizing one"),
            );
        } else if self.metadata_duration_zero {
            self.count_class(CLASS_R6, log);
        }
        if self.input_flags_set {
            let mut expected = 0u8;
            if self.has_audio_data || self.has_audio_header {
                expected |= 0x01;
            }
            if self.has_video_data || self.has_video_header {
                expected |= 0x04;
            }
            if expected != 0 && (self.strict_header_flags & 0x05) != expected {
                self.count_class(CLASS_R6, log);
                log.event(
                    "warn",
                    "header_flags_corrected",
                    Some("FLV header flags did not match the tracks present"),
                );
            }
        }
        actions
    }

    fn handle_script(&mut self, tag: Tag, actions: &mut Vec<Action>, log: &mut dyn LogSink) {
        let values = match amf::parse_script_body(&tag.data) {
            Ok(values) => values,
            Err(_) => {
                // Malformed AMF payload: drop the tag, the writer synthesizes metadata.
                self.count_class(CLASS_R7, log);
                log.event("warn", "malformed_script_tag", None);
                return;
            }
        };
        let Some(metadata) = amf::parse_metadata(&values) else {
            // Non-onMetaData script tags are dropped by the reference implementation.
            log.event("debug", "non_metadata_script_tag", None);
            return;
        };

        let duration_valid = metadata
            .duration
            .map(|duration| duration.is_finite() && duration > 0.0)
            .unwrap_or(false);
        if !duration_valid {
            self.metadata_duration_zero = true;
        }
        if metadata.had_extra_value {
            log.event(
                "debug",
                "metadata_extra_value",
                Some("onMetaData script tag had a third value (tolerated)"),
            );
        }

        let initial_duration = if duration_valid {
            metadata.duration.unwrap_or(0.0)
        } else {
            0.0
        };
        let normalized = amf::normalize_metadata(&metadata, initial_duration);
        let mut body = Vec::new();
        if amf::write_script_body(&normalized, &mut body).is_err() {
            log.event("warn", "metadata_serialize_failed", None);
            return;
        }
        if !self.script_seen {
            self.script_seen = true;
            self.first_metadata = Some(metadata);
        }
        actions.push(Action::Script(Tag::new(
            TagType::Script,
            0,
            body,
            tag.index,
        )));
    }

    fn handle_headers(&mut self, tags: Vec<Tag>, actions: &mut Vec<Action>, log: &mut dyn LogSink) {
        let selection = select_headers(&tags);
        if selection.multiple_different {
            self.count_class(CLASS_R6, log);
            log.event(
                "warn",
                "multiple_headers",
                Some("received multiple different AVC/HEVC/AAC sequence headers"),
            );
        }

        let mut emit = Vec::new();
        if let Some(video) = selection.video {
            let changed = match &self.last_video_header {
                None => true,
                Some(previous) => previous.as_slice() != video.data.as_slice(),
            };
            if changed {
                if !self.has_video_header && self.has_video_data {
                    self.count_class(CLASS_R5, log);
                    log.event("warn", "delayed_video_header", None);
                }
                self.last_video_header = Some(video.data.clone());
                self.has_video_header = true;
                emit.push(video);
            }
        }
        if let Some(audio) = selection.audio {
            let changed = match &self.last_audio_header {
                None => true,
                Some(previous) => previous.as_slice() != audio.data.as_slice(),
            };
            if changed {
                if !self.has_audio_header && self.has_audio_data {
                    self.count_class(CLASS_R5, log);
                    log.event(
                        "warn",
                        "delayed_audio_header",
                        Some("audio sequence header arrived after audio data; moved earlier"),
                    );
                }
                self.last_audio_header = Some(audio.data.clone());
                self.has_audio_header = true;
                emit.push(audio);
            }
        }
        if !emit.is_empty() {
            actions.push(Action::Headers(emit));
        }
    }

    fn handle_data(&mut self, tags: Vec<Tag>, actions: &mut Vec<Action>, log: &mut dyn LogSink) {
        // --- R5: split out delayed sequence headers contained in a data group. ---
        let mut header_tags = Vec::new();
        let mut data_tags = Vec::new();
        let mut seen_audio_header = false;
        let mut audio_data_before_header = false;
        for tag in tags {
            if tag.is_header() {
                if tag.tag_type == TagType::Audio {
                    seen_audio_header = true;
                }
                header_tags.push(tag);
            } else {
                if tag.tag_type == TagType::Audio && !seen_audio_header {
                    audio_data_before_header = true;
                }
                data_tags.push(tag);
            }
        }
        if !header_tags.is_empty() {
            self.count_class(CLASS_R5, log);
            log.event(
                "warn",
                "delayed_audio_header",
                Some("sequence header moved in front of the data group"),
            );
            self.handle_headers(header_tags, actions, log);
        }
        let _ = audio_data_before_header;
        if data_tags.is_empty() {
            return;
        }

        // --- R3: drop duplicated tag groups (payload based, last 16 groups). ---
        let group_hash = hash_group(&data_tags);
        let duplicate = self.dedupe.contains(&group_hash);
        if duplicate {
            let mut aac = false;
            for tag in &data_tags {
                if tag.tag_type == TagType::Audio && tag.is_aac() {
                    aac = true;
                }
            }
            self.count_class(CLASS_R3, log);
            if aac {
                self.count_class(CLASS_R8, log);
            }
            self.stats.groups_dropped += 1;
            log.event("debug", "duplicate_group_dropped", None);
            return;
        }
        self.dedupe.push_back(group_hash);
        while self.dedupe.len() > DUPLICATE_HISTORY {
            self.dedupe.pop_front();
        }

        // --- R4: strip filler NAL units and empty filler tags. ---
        let mut filler_removed = false;
        for tag in &mut data_tags {
            if tag.tag_type == TagType::Video && tag.is_data() {
                let removed = codec::strip_filler_nalus(&mut tag.data);
                if removed > 0 {
                    filler_removed = true;
                    if tag.data.len() <= 5 {
                        tag.data.clear();
                    }
                }
            }
        }
        if filler_removed {
            self.count_class(CLASS_R4, log);
        }
        let before = data_tags.len();
        data_tags.retain(|tag| !tag.data.is_empty());
        if data_tags.len() != before {
            self.count_class(CLASS_R4, log);
            log.event("debug", "zero_size_tag_dropped", None);
        }
        if data_tags.is_empty() {
            return;
        }

        // --- R1/R2: timestamp jump and rollback repair. ---
        self.adjust_data_timestamps(&mut data_tags, log);

        // --- Safety net: per-track monotonic clamp (never emit a rollback). ---
        self.clamp_monotonic(&mut data_tags, log);

        // --- R8 extension: drop adjacent, byte-identical AAC tags (server replay). ---
        if data_tags.iter().any(|tag| tag.tag_type == TagType::Audio) {
            let mut filtered = Vec::with_capacity(data_tags.len());
            for tag in data_tags {
                if tag.tag_type == TagType::Audio && tag.is_aac() {
                    if let Some((timestamp, hash)) = self.last_audio_emitted {
                        if timestamp == tag.timestamp && hash == tag.data_hash() {
                            self.count_class(CLASS_R3, log);
                            self.count_class(CLASS_R8, log);
                            log.event(
                                "debug",
                                "duplicate_audio_tag",
                                Some("dropped adjacent byte-identical AAC tag"),
                            );
                            continue;
                        }
                    }
                    self.last_audio_emitted = Some((tag.timestamp, tag.data_hash()));
                }
                filtered.push(tag);
            }
            data_tags = filtered;
        }
        if data_tags.is_empty() {
            return;
        }

        for tag in &data_tags {
            match tag.tag_type {
                TagType::Video => self.has_video_data = true,
                TagType::Audio => self.has_audio_data = true,
                TagType::Script => {}
            }
        }
        actions.push(Action::Data(data_tags));
    }

    fn adjust_data_timestamps(&mut self, tags: &mut [Tag], log: &mut dyn LogSink) {
        let is_first_chunk = self.timestamp.first_chunk;
        let mut current = tags[0].timestamp;
        if is_first_chunk {
            self.timestamp.first_chunk = false;
            current = tags
                .iter()
                .map(|tag| tag.timestamp)
                .min()
                .unwrap_or(current);
        }
        let diff = current.saturating_sub(self.timestamp.last_original);
        let aac_audio = tags
            .iter()
            .any(|tag| tag.tag_type == TagType::Audio && tag.is_aac());
        let hevc_video = tags.iter().any(Tag::is_hevc);
        if diff < -JUMP_THRESHOLD || (is_first_chunk && diff < 0) {
            self.count_class(CLASS_R2, log);
            if aac_audio || hevc_video {
                self.count_class(CLASS_R8, log);
            }
            log.event(
                "warn",
                "timestamp_rollback",
                Some("group timestamp decreased; rebuilt timestamp offset"),
            );
            self.timestamp.current_offset = current.saturating_sub(self.timestamp.next_target);
        } else if diff > JUMP_THRESHOLD {
            self.count_class(CLASS_R1, log);
            if aac_audio || hevc_video {
                self.count_class(CLASS_R8, log);
            }
            log.event(
                "warn",
                "timestamp_jump",
                Some("group timestamp jumped forward; rebuilt timestamp offset"),
            );
            self.timestamp.current_offset = current.saturating_sub(self.timestamp.next_target);
        }

        self.timestamp.last_original = tags
            .last()
            .map(|tag| tag.timestamp)
            .unwrap_or(self.timestamp.last_original);
        let offset = self.timestamp.current_offset;
        for tag in tags.iter_mut() {
            tag.timestamp = tag.timestamp.saturating_sub(offset);
        }
        self.timestamp.next_target = calculate_new_target(tags);
    }

    fn clamp_monotonic(&mut self, tags: &mut [Tag], log: &mut dyn LogSink) {
        for tag in tags.iter_mut() {
            let track = match tag.tag_type {
                TagType::Audio => 0,
                TagType::Video => 1,
                TagType::Script => continue,
            };
            let last = self.last_output_timestamp[track];
            if tag.timestamp < 0 || tag.timestamp < last {
                self.count_class(CLASS_R2, log);
                if tag.tag_type == TagType::Audio && tag.is_aac() {
                    self.count_class(CLASS_R8, log);
                }
                if tag.tag_type == TagType::Video && tag.is_hevc() {
                    self.count_class(CLASS_R8, log);
                }
                if tag.timestamp < 0 {
                    tag.timestamp = 0;
                } else {
                    tag.timestamp = last;
                }
            }
            self.last_output_timestamp[track] = tag.timestamp;
        }
    }

    fn adjust_end_timestamp(&mut self, mut tag: Tag) -> Tag {
        let diff = tag.timestamp.saturating_sub(self.timestamp.last_original);
        if !(-JUMP_THRESHOLD..=JUMP_THRESHOLD).contains(&diff) {
            tag.timestamp = self.timestamp.next_target;
        } else {
            tag.timestamp = tag.timestamp.saturating_sub(self.timestamp.current_offset);
        }
        if tag.timestamp < 0 {
            tag.timestamp = 0;
        }
        tag
    }

    pub fn metadata(&self) -> Option<&amf::Metadata> {
        self.first_metadata.as_ref()
    }
}

impl Default for RepairPipeline {
    fn default() -> Self {
        Self::new()
    }
}

fn select_headers(tags: &[Tag]) -> HeaderSelection {
    let mut selection = HeaderSelection::default();
    let video: Vec<&Tag> = tags
        .iter()
        .filter(|tag| tag.tag_type == TagType::Video)
        .collect();
    let audio: Vec<&Tag> = tags
        .iter()
        .filter(|tag| tag.tag_type == TagType::Audio)
        .collect();
    selection.video = select_one(&video, &mut selection.multiple_different);
    selection.audio = select_one(&audio, &mut selection.multiple_different);
    selection
}

fn select_one(tags: &[&Tag], multiple_different: &mut bool) -> Option<Tag> {
    match tags.len() {
        0 => None,
        1 => Some(tags[0].clone()),
        _ => {
            let first = tags[0];
            if tags[1..].iter().all(|tag| tag.data == first.data) {
                Some(first.clone())
            } else {
                *multiple_different = true;
                tags.last().map(|tag| (*tag).clone())
            }
        }
    }
}

/// Port of `UpdateTimestampJumpRule.CalculateNewTarget`.
fn calculate_new_target(tags: &[Tag]) -> i64 {
    let mut video = 0i64;
    let mut audio = 0i64;
    if tags.iter().any(|tag| tag.tag_type == TagType::Video) {
        video = calculate_per_channel(
            tags,
            VIDEO_DURATION_FALLBACK,
            VIDEO_DURATION_MAX,
            VIDEO_DURATION_MIN,
            TagType::Video,
        );
    }
    if tags.iter().any(|tag| tag.tag_type == TagType::Audio) {
        audio = calculate_per_channel(
            tags,
            AUDIO_DURATION_FALLBACK,
            AUDIO_DURATION_MAX,
            AUDIO_DURATION_MIN,
            TagType::Audio,
        );
    }
    video.max(audio)
}

fn calculate_per_channel(
    tags: &[Tag],
    fallback: i64,
    max: i64,
    min: i64,
    tag_type: TagType,
) -> i64 {
    let samples: Vec<i64> = tags
        .iter()
        .filter(|tag| tag.tag_type == tag_type)
        .take(2)
        .map(|tag| tag.timestamp)
        .collect();
    let duration = if samples.len() != 2 {
        fallback
    } else {
        let delta = samples[1].saturating_sub(samples[0]);
        if !(min..=max).contains(&delta) {
            fallback
        } else {
            delta
        }
    };
    let last = tags
        .iter()
        .rev()
        .find(|tag| tag.tag_type == tag_type)
        .map(|tag| tag.timestamp)
        .unwrap_or(0);
    last.saturating_add(duration)
}

/// Hash of a data group used for duplicate detection: tag count + payload hashes.
fn hash_group(tags: &[Tag]) -> u64 {
    let mut hash: u64 = 0xcbf2_9ce4_8422_2325;
    let mix = |hash: &mut u64, value: u64| {
        *hash ^= value;
        *hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    };
    mix(&mut hash, tags.len() as u64);
    for tag in tags {
        mix(&mut hash, tag.data_hash());
    }
    hash
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::log::NullLog;

    fn video_data(timestamp: i64, keyframe: bool, payload: u8) -> Tag {
        Tag::new(
            TagType::Video,
            timestamp,
            vec![if keyframe { 0x17 } else { 0x27 }, 0x01, 0, 0, 0, payload],
            0,
        )
    }

    fn audio_data(timestamp: i64, payload: u8) -> Tag {
        Tag::new(TagType::Audio, timestamp, vec![0xaf, 0x01, payload], 0)
    }

    #[test]
    fn repairs_forward_jump() {
        let mut pipeline = RepairPipeline::new();
        let mut log = NullLog;
        let first = pipeline.process(
            Group::Data(vec![
                video_data(0, true, 1),
                audio_data(0, 1),
                audio_data(23, 2),
                video_data(33, false, 2),
            ]),
            &mut log,
        );
        assert_eq!(first.len(), 1);
        let second = pipeline.process(
            Group::Data(vec![video_data(60_000, true, 3), audio_data(60_000, 3)]),
            &mut log,
        );
        let Action::Data(tags) = &second[0] else {
            panic!("expected data action");
        };
        // Jump repaired: the new group continues right after the previous target.
        assert!(
            tags[0].timestamp < 1000,
            "timestamp was {}",
            tags[0].timestamp
        );
        assert_eq!(pipeline.stats.count(CLASS_R1), 1);
    }

    #[test]
    fn repairs_rollback_and_keeps_monotonic() {
        let mut pipeline = RepairPipeline::new();
        let mut log = NullLog;
        pipeline.process(
            Group::Data(vec![video_data(1000, true, 1), video_data(1050, false, 2)]),
            &mut log,
        );
        let out = pipeline.process(
            Group::Data(vec![video_data(900, true, 3), video_data(950, false, 4)]),
            &mut log,
        );
        let Action::Data(tags) = &out[0] else {
            panic!("expected data action");
        };
        assert!(tags[0].timestamp >= 0);
        assert!(pipeline.stats.count(CLASS_R2) >= 1);
    }

    #[test]
    fn drops_duplicate_group() {
        let mut pipeline = RepairPipeline::new();
        let mut log = NullLog;
        let group = || Group::Data(vec![video_data(0, true, 7), audio_data(0, 7)]);
        let first = pipeline.process(group(), &mut log);
        assert_eq!(first.len(), 1);
        let second = pipeline.process(group(), &mut log);
        assert!(second.is_empty());
        assert_eq!(pipeline.stats.count(CLASS_R3), 1);
    }

    #[test]
    fn removes_filler_nalus() {
        let mut body = vec![0x17, 0x01, 0, 0, 0];
        // NALU 1: type 5 (IDR) length 1
        body.extend_from_slice(&1u32.to_be_bytes());
        body.push(0x65);
        // NALU 2: filler type 12 length 2
        body.extend_from_slice(&2u32.to_be_bytes());
        body.extend_from_slice(&[0x0c, 0x00]);
        let tag = Tag::new(TagType::Video, 0, body, 0);
        let mut pipeline = RepairPipeline::new();
        let mut log = NullLog;
        let out = pipeline.process(Group::Data(vec![tag]), &mut log);
        let Action::Data(tags) = &out[0] else {
            panic!("expected data action");
        };
        assert_eq!(tags[0].data.len(), 5 + 4 + 1);
        assert_eq!(pipeline.stats.count(CLASS_R4), 1);
    }

    #[test]
    fn moves_delayed_audio_header() {
        let mut pipeline = RepairPipeline::new();
        let mut log = NullLog;
        let out = pipeline.process(
            Group::Data(vec![
                audio_data(0, 1),
                Tag::new(TagType::Audio, 0, vec![0xaf, 0x00, 0x12, 0x10], 0),
            ]),
            &mut log,
        );
        assert!(matches!(out[0], Action::Headers(_)));
        assert!(matches!(out[1], Action::Data(_)));
        assert_eq!(pipeline.stats.count(CLASS_R5), 1);
    }

    #[test]
    fn keeps_single_end_tag_at_finish() {
        let mut pipeline = RepairPipeline::new();
        let mut log = NullLog;
        let end = Tag::new(TagType::Video, 100, vec![0x17, 0x02, 0, 0, 0], 0);
        assert!(pipeline.process(Group::End(end), &mut log).is_empty());
        let finished = pipeline.finish(&mut log);
        assert!(matches!(finished.first(), Some(Action::End(_))));
        assert_eq!(pipeline.stats.count(CLASS_R4), 0);
    }
}
