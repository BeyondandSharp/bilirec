// SPDX-License-Identifier: GPL-3.0-or-later
//
// `flvrepair analyze`: parse an FLV file and emit the schema v1 report (see
// docs/INTERFACES.md §1.4). The report is generated from a single streaming pass.

use crate::amf;
use crate::codec;
use crate::group::GroupReader;
use crate::log::NullLog;
use crate::parser::{ParseError, ParserStats, TagReader};
use crate::pipeline::{RepairPipeline, RepairStats};
use crate::tag::{Tag, TagType};
use std::io::Read;

#[derive(Debug, Default, Clone, Copy)]
pub struct TrackTimestamps {
    pub present: bool,
    pub first: i64,
    pub last: i64,
    pub monotonic: bool,
    pub jumps: u64,
    pub count: u64,
}

#[derive(Debug, Default, Clone)]
pub struct VideoReport {
    pub codec: Option<String>,
    pub width: Option<u32>,
    pub height: Option<u32>,
}

#[derive(Debug, Default, Clone)]
pub struct AudioReport {
    pub codec: Option<String>,
    pub sample_rate: Option<u32>,
    pub channels: Option<u8>,
}

#[derive(Debug, Default, Clone)]
pub struct AnalyzeReport {
    pub valid: bool,
    pub bytes: u64,
    pub duration_ms: u64,
    pub video: Option<VideoReport>,
    pub audio: Option<AudioReport>,
    pub video_timestamps: TrackTimestamps,
    pub audio_timestamps: TrackTimestamps,
    pub anomalies: Vec<(&'static str, u64)>,
    pub parser: ParserStats,
    pub repair: RepairStats,
}

impl AnalyzeReport {
    pub fn to_json(&self) -> String {
        let mut out = String::with_capacity(512);
        out.push_str("{\"version\":1");
        out.push_str(&format!(",\"valid\":{}", self.valid));
        out.push_str(&format!(",\"bytes\":{}", self.bytes));
        out.push_str(&format!(",\"duration_ms\":{}", self.duration_ms));

        out.push_str(",\"video\":");
        match &self.video {
            Some(video) => {
                out.push_str("{\"codec\":");
                push_optional_string(&mut out, video.codec.as_deref());
                out.push_str(",\"width\":");
                push_optional_number(&mut out, video.width.map(u64::from));
                out.push_str(",\"height\":");
                push_optional_number(&mut out, video.height.map(u64::from));
                out.push('}');
            }
            None => out.push_str("null"),
        }

        out.push_str(",\"audio\":");
        match &self.audio {
            Some(audio) => {
                out.push_str("{\"codec\":");
                push_optional_string(&mut out, audio.codec.as_deref());
                out.push_str(",\"sample_rate\":");
                push_optional_number(&mut out, audio.sample_rate.map(u64::from));
                out.push_str(",\"channels\":");
                push_optional_number(&mut out, audio.channels.map(u64::from));
                out.push('}');
            }
            None => out.push_str("null"),
        }

        out.push_str(",\"timestamps\":{\"video\":");
        push_timestamps(&mut out, &self.video_timestamps);
        out.push_str(",\"audio\":");
        push_timestamps(&mut out, &self.audio_timestamps);
        out.push('}');

        out.push_str(",\"anomalies\":[");
        for (index, (class, count)) in self.anomalies.iter().enumerate() {
            if index > 0 {
                out.push(',');
            }
            out.push_str(&format!("{{\"class\":\"{}\",\"count\":{}}}", class, count));
        }
        out.push_str("]}");
        out
    }

    pub fn to_human(&self) -> String {
        let mut out = String::new();
        out.push_str(&format!("valid: {}\n", self.valid));
        out.push_str(&format!("bytes: {}\n", self.bytes));
        out.push_str(&format!("duration_ms: {}\n", self.duration_ms));
        match &self.video {
            Some(video) => out.push_str(&format!(
                "video: codec={} width={} height={}\n",
                video.codec.as_deref().unwrap_or("unknown"),
                optional_display(video.width.map(u64::from)),
                optional_display(video.height.map(u64::from)),
            )),
            None => out.push_str("video: none\n"),
        }
        match &self.audio {
            Some(audio) => out.push_str(&format!(
                "audio: codec={} sample_rate={} channels={}\n",
                audio.codec.as_deref().unwrap_or("unknown"),
                optional_display(audio.sample_rate.map(u64::from)),
                optional_display(audio.channels.map(u64::from)),
            )),
            None => out.push_str("audio: none\n"),
        }
        out.push_str(&format!(
            "timestamps.video: first={} last={} monotonic={} jumps={} count={}\n",
            self.video_timestamps.first,
            self.video_timestamps.last,
            self.video_timestamps.monotonic,
            self.video_timestamps.jumps,
            self.video_timestamps.count,
        ));
        out.push_str(&format!(
            "timestamps.audio: first={} last={} monotonic={} jumps={} count={}\n",
            self.audio_timestamps.first,
            self.audio_timestamps.last,
            self.audio_timestamps.monotonic,
            self.audio_timestamps.jumps,
            self.audio_timestamps.count,
        ));
        if self.anomalies.is_empty() {
            out.push_str("anomalies: none\n");
        } else {
            let list: Vec<String> = self
                .anomalies
                .iter()
                .map(|(class, count)| format!("{class}={count}"))
                .collect();
            out.push_str(&format!("anomalies: {}\n", list.join(" ")));
        }
        out
    }
}

fn optional_display(value: Option<u64>) -> String {
    match value {
        Some(value) => value.to_string(),
        None => "unknown".to_string(),
    }
}

fn push_optional_string(out: &mut String, value: Option<&str>) {
    match value {
        Some(value) => {
            out.push('"');
            out.push_str(&crate::log::escape(value));
            out.push('"');
        }
        None => out.push_str("null"),
    }
}

fn push_optional_number(out: &mut String, value: Option<u64>) {
    match value {
        Some(value) => out.push_str(&value.to_string()),
        None => out.push_str("null"),
    }
}

fn push_timestamps(out: &mut String, timestamps: &TrackTimestamps) {
    if !timestamps.present {
        out.push_str("null");
        return;
    }
    out.push_str(&format!(
        "{{\"first\":{},\"last\":{},\"monotonic\":{},\"jumps\":{}}}",
        timestamps.first, timestamps.last, timestamps.monotonic, timestamps.jumps
    ));
}

/// Inspect a tag and fold its information into the report.
fn inspect_tag(tag: &Tag, report: &mut AnalyzeReport, metadata_duration: &mut Option<f64>) {
    match tag.tag_type {
        TagType::Video => {
            let video = report.video.get_or_insert_with(VideoReport::default);
            if video.codec.is_none() {
                if let Some(codec) = tag.video_codec_name() {
                    video.codec = Some(codec.to_string());
                }
            }
            if tag.flags.header && (video.width.is_none() || video.height.is_none()) {
                if let Some((width, height)) = codec::sequence_header_dimensions(&tag.data) {
                    if video.width.is_none() {
                        video.width = width;
                    }
                    if video.height.is_none() {
                        video.height = height;
                    }
                }
            }
            if tag.is_data() {
                update_timestamps(&mut report.video_timestamps, tag.timestamp);
            }
        }
        TagType::Audio => {
            let audio = report.audio.get_or_insert_with(AudioReport::default);
            if audio.codec.is_none() {
                if let Some(format) = tag.audio_format() {
                    audio.codec = Some(
                        codec::audio_codec_name(format)
                            .unwrap_or("unknown")
                            .to_string(),
                    );
                }
            }
            if tag.flags.header && tag.is_aac() && tag.data.len() >= 4 {
                if let Some((sample_rate, channels)) =
                    codec::aac_audio_specific_config(&tag.data[2..])
                {
                    if audio.sample_rate.is_none() {
                        audio.sample_rate = Some(sample_rate);
                    }
                    if audio.channels.is_none() {
                        audio.channels = Some(channels);
                    }
                }
            }
            if tag.is_data() {
                update_timestamps(&mut report.audio_timestamps, tag.timestamp);
            }
        }
        TagType::Script => {
            if metadata_duration.is_none() {
                if let Ok(values) = amf::parse_script_body(&tag.data) {
                    if let Some(duration) = amf::metadata_duration(&values) {
                        if duration.is_finite() && duration > 0.0 {
                            *metadata_duration = Some(duration);
                        }
                    }
                }
            }
        }
    }
}

fn update_timestamps(timestamps: &mut TrackTimestamps, timestamp: i64) {
    if !timestamps.present {
        timestamps.present = true;
        timestamps.first = timestamp;
        timestamps.last = timestamp;
        timestamps.monotonic = true;
        timestamps.count = 1;
        return;
    }
    let diff = timestamp.saturating_sub(timestamps.last);
    if diff < 0 {
        timestamps.monotonic = false;
    }
    if !(-500..=500).contains(&diff) {
        timestamps.jumps += 1;
    }
    timestamps.last = timestamp;
    timestamps.count += 1;
}

/// Run a full analyze pass over the reader.
pub fn analyze<R: Read>(reader: TagReader<R>) -> Result<AnalyzeReport, ParseError> {
    let mut groups = GroupReader::new(reader);
    let mut pipeline = RepairPipeline::new();
    let mut log = NullLog;
    let mut report = AnalyzeReport::default();
    let mut metadata_duration: Option<f64> = None;

    while let Some(group) = groups.next_group()? {
        for tag in group.tags() {
            inspect_tag(tag, &mut report, &mut metadata_duration);
        }
        let _ = pipeline.process(group, &mut log);
    }

    let header = groups.reader().header();
    report.parser = groups.reader().stats;
    report.bytes = report.parser.bytes_read;
    report.valid = header.present;
    pipeline.set_input_flags(header.flags);
    pipeline.add_parser_corruption(report.parser.corruption_events, &mut log);
    let _ = pipeline.finish(&mut log);
    report.repair = pipeline.stats.clone();
    report.anomalies = report.repair.anomalies();

    report.duration_ms = match metadata_duration {
        Some(duration) if duration.is_finite() && duration > 0.0 => {
            (duration * 1000.0).round().clamp(0.0, u64::MAX as f64) as u64
        }
        _ => {
            let mut first: Option<i64> = None;
            let mut last: Option<i64> = None;
            for timestamps in [&report.video_timestamps, &report.audio_timestamps] {
                if !timestamps.present {
                    continue;
                }
                first = Some(first.map_or(timestamps.first, |value| value.min(timestamps.first)));
                last = Some(last.map_or(timestamps.last, |value| value.max(timestamps.last)));
            }
            match (first, last) {
                (Some(first), Some(last)) => last.saturating_sub(first).max(0) as u64,
                _ => 0,
            }
        }
    };
    Ok(report)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn json_shape_is_stable() {
        let report = AnalyzeReport {
            valid: true,
            bytes: 123,
            duration_ms: 456,
            video: Some(VideoReport {
                codec: Some("avc".to_string()),
                width: Some(1920),
                height: Some(1080),
            }),
            audio: Some(AudioReport {
                codec: Some("aac".to_string()),
                sample_rate: Some(48000),
                channels: Some(2),
            }),
            video_timestamps: TrackTimestamps {
                present: true,
                first: 0,
                last: 61000,
                monotonic: true,
                jumps: 0,
                count: 2,
            },
            audio_timestamps: TrackTimestamps {
                present: true,
                first: 0,
                last: 61200,
                monotonic: true,
                jumps: 0,
                count: 2,
            },
            anomalies: vec![("R1", 2)],
            ..AnalyzeReport::default()
        };
        let json = report.to_json();
        assert!(json.starts_with("{\"version\":1,\"valid\":true"));
        assert!(json.contains("\"codec\":\"avc\""));
        assert!(json.contains("\"width\":1920"));
        assert!(json.contains("\"anomalies\":[{\"class\":\"R1\",\"count\":2}]"));
        assert!(json.ends_with('}'));
    }

    #[test]
    fn unknown_dimensions_are_null() {
        let report = AnalyzeReport {
            video: Some(VideoReport {
                codec: Some("hevc".to_string()),
                width: None,
                height: None,
            }),
            ..AnalyzeReport::default()
        };
        let json = report.to_json();
        assert!(json.contains("\"width\":null"));
    }
}
