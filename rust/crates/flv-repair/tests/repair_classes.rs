// SPDX-License-Identifier: GPL-3.0-only
//
// Synthetic R1-R8 class tests: each corruption class is constructed in memory (no
// fixtures required) and the repaired output is validated structurally.

mod support;

use flv_repair::pipeline::{
    RepairStats, CLASS_R1, CLASS_R2, CLASS_R3, CLASS_R4, CLASS_R5, CLASS_R6, CLASS_R7, CLASS_R8,
};
use flv_repair::tag::TagType;
use support::*;

fn script_header(flags: u8, tags: &[RawTag]) -> Vec<u8> {
    build_flv(flags, tags)
}

fn full_stream(frames: Vec<RawTag>) -> Vec<RawTag> {
    let mut tags = vec![metadata_tag(Some(5.0))];
    tags.push(RawTag {
        tag_type: 9,
        timestamp: 0,
        body: avc_config_body(),
    });
    tags.push(audio_header(0));
    tags.extend(frames);
    tags
}

fn interleave(mut frames: Vec<RawTag>) -> Vec<RawTag> {
    frames.sort_by_key(|tag| (tag.timestamp, if tag.tag_type == 9 { 0 } else { 1 }));
    frames
}

fn repair_and_parse(input: &[u8]) -> (Vec<u8>, ParsedFlv, RepairStats) {
    let (output, stats) = repair_library(input);
    let parsed =
        parse_flv(&output).unwrap_or_else(|error| panic!("invalid repaired output: {error}"));
    assert_eq!(parsed.flags, 0x05);
    (output, parsed, stats)
}

fn video_timestamps(parsed: &ParsedFlv) -> Vec<i64> {
    track_timestamps(&parsed.tags, true)
}

fn audio_timestamps(parsed: &ParsedFlv) -> Vec<i64> {
    track_timestamps(&parsed.tags, false)
}

#[test]
fn clean_synthetic_stream_round_trips_identically() {
    let input = clean_avc_stream();
    let (output, stats) = repair_library(&input);
    assert_eq!(output, input, "library repair changed a clean stream");
    assert_eq!(stats.total(), 0, "clean stream reported anomalies");

    let cli = run_binary(&["stream"], Some(input.clone()));
    assert!(cli.status.success());
    assert_eq!(cli.stdout, input, "stream mode changed a clean stream");
}

#[test]
fn r1_forward_jump_is_rebuilt() {
    let mut frames = vec![];
    for index in 0..4i64 {
        frames.push(avc_video_frame(index * 33, index == 0, 5, &[1, 2, 3]));
        frames.push(audio_frame(index * 33, index as u8));
    }
    // Next group jumps forward by 5 seconds.
    frames.push(avc_video_frame(5000, true, 5, &[4, 5, 6]));
    frames.push(audio_frame(5000, 9));
    frames.push(avc_video_frame(5033, false, 1, &[7, 8, 9]));
    let input = script_header(0x05, &full_stream(interleave(frames)));
    let (_output, parsed, stats) = repair_and_parse(&input);
    assert!(stats.count(CLASS_R1) >= 1, "R1 not detected: {stats:?}");
    assert!(is_sorted(&video_timestamps(&parsed)), "video not monotonic");
    assert!(is_sorted(&audio_timestamps(&parsed)), "audio not monotonic");
    let last = video_timestamps(&parsed).last().copied().unwrap_or(0);
    assert!(
        last < 500,
        "jump was not collapsed to continuity, last video ts = {last}"
    );
}

#[test]
fn r2_rollback_keeps_timestamps_monotonic() {
    // Explicit group boundaries: the third group starts 1000 ms in the past.
    let frames = vec![
        avc_video_frame(0, true, 5, &[1, 2, 3]),
        audio_frame(0, 0),
        audio_frame(43, 1),
        avc_video_frame(1000, true, 5, &[4, 5, 6]),
        audio_frame(1000, 2),
        audio_frame(1043, 3),
        avc_video_frame(500, true, 5, &[7, 8, 9]),
        audio_frame(500, 4),
        audio_frame(543, 5),
    ];
    let input = script_header(0x05, &full_stream(frames));
    let (_output, parsed, stats) = repair_and_parse(&input);
    assert!(stats.count(CLASS_R2) >= 1, "R2 not detected: {stats:?}");
    assert!(is_sorted(&video_timestamps(&parsed)), "video not monotonic");
    assert!(is_sorted(&audio_timestamps(&parsed)), "audio not monotonic");
    assert!(video_timestamps(&parsed).iter().all(|ts| *ts >= 0));
}

#[test]
fn r3_duplicate_group_is_dropped() {
    // Groups must stay contiguous; the duplicate is appended verbatim after group 2.
    let frames = vec![
        avc_video_frame(0, true, 5, &[1, 2, 3]),
        audio_frame(0, 1),
        audio_frame(43, 2),
        avc_video_frame(500, true, 5, &[4, 5, 6]),
        audio_frame(500, 3),
        audio_frame(543, 4),
        // duplicate of the group above
        avc_video_frame(500, true, 5, &[4, 5, 6]),
        audio_frame(500, 3),
        audio_frame(543, 4),
    ];
    let input = script_header(0x05, &full_stream(frames));
    let (_output, parsed, stats) = repair_and_parse(&input);
    assert!(stats.count(CLASS_R3) >= 1, "R3 not detected: {stats:?}");
    let hashes = data_group_hashes(&parsed);
    let mut seen = std::collections::HashSet::new();
    for hash in hashes {
        assert!(seen.insert(hash), "duplicate group survived repair");
    }
}

#[test]
fn r4_filler_zero_size_and_midstream_end_are_removed() {
    let mut frames = vec![
        avc_frame_with_nalus(0, true, &[(5, &[1, 2, 3]), (12, &[0, 0])]),
        audio_frame(0, 1),
        zero_size_tag(9, 10),
        zero_size_tag(8, 10),
        end_tag(43),
        avc_video_frame(66, false, 1, &[4, 5, 6]),
        audio_frame(66, 2),
        end_tag(99),
        avc_video_frame(99, true, 5, &[7, 8, 9]),
        audio_frame(99, 3),
        end_tag(120),
    ];
    frames.sort_by_key(|tag| (tag.timestamp, tag.tag_type));
    let input = script_header(0x05, &full_stream(frames));
    let (_output, parsed, stats) = repair_and_parse(&input);
    assert!(stats.count(CLASS_R4) >= 1, "R4 not detected: {stats:?}");
    assert_eq!(count_filler_nalus(&parsed.tags), 0, "filler NALUs remain");
    assert!(
        !parsed
            .tags
            .iter()
            .any(|tag| matches!(tag.tag_type, 8 | 9) && tag.body.is_empty()),
        "zero-size tags remain"
    );
    let ends: Vec<&RawTag> = parsed.tags.iter().filter(|tag| is_video_end(tag)).collect();
    assert_eq!(ends.len(), 1, "expected exactly the terminal END tag");
    assert!(is_video_end(parsed.tags.last().expect("tags")));
}

#[test]
fn r5_delayed_audio_header_is_moved_before_audio_data() {
    let mut tags = vec![
        metadata_tag(Some(5.0)),
        RawTag {
            tag_type: 9,
            timestamp: 0,
            body: avc_config_body(),
        },
    ];
    tags.push(avc_video_frame(0, true, 5, &[1, 2, 3]));
    for index in 0..5i64 {
        tags.push(audio_frame(index * 43, index as u8));
    }
    tags.push(audio_header(0)); // delayed AAC sequence header
    for index in 5..10i64 {
        tags.push(audio_frame(index * 43, index as u8));
    }
    let input = script_header(0x05, &tags);
    let (_output, parsed, stats) = repair_and_parse(&input);
    assert!(stats.count(CLASS_R5) >= 1, "R5 not detected: {stats:?}");
    let header = parsed
        .tags
        .iter()
        .position(is_audio_header)
        .expect("header");
    let data = parsed
        .tags
        .iter()
        .position(is_audio_data)
        .expect("audio data");
    assert!(
        header < data,
        "AAC header at {header} after audio data at {data}"
    );
}

#[test]
fn r6_missing_duration_and_bad_flags_are_fixed() {
    let mut frames = vec![];
    for index in 0..6i64 {
        frames.push(avc_video_frame(
            index * 33,
            index % 3 == 0,
            5,
            &[index as u8],
        ));
        frames.push(audio_frame(index * 33, index as u8));
    }
    let mut tags = vec![metadata_tag(None)];
    tags.push(RawTag {
        tag_type: 9,
        timestamp: 0,
        body: avc_config_body(),
    });
    tags.push(audio_header(0));
    tags.extend(interleave(frames));
    let input = script_header(0x00, &tags); // wrong header flags
    let (_output, parsed, stats) = repair_and_parse(&input);
    assert!(stats.count(CLASS_R6) >= 1, "R6 not detected: {stats:?}");
    assert_eq!(parsed.flags, 0x05);

    // file mode must add/patch the duration in onMetaData
    let file_output = repair_via_cli_file(&input, &[]);
    let file_parsed = parse_flv(&file_output).expect("valid file output");
    assert_eq!(file_parsed.flags, 0x05);
    let duration = metadata_duration(&file_parsed).expect("duration patched in file mode");
    let media_last = media_tags(&file_parsed.tags)
        .iter()
        .map(|tag| tag.timestamp)
        .max()
        .unwrap_or(0) as f64
        / 1000.0;
    assert!(
        (duration - media_last).abs() < 0.2,
        "duration {duration} vs media {media_last}"
    );
}

#[test]
fn r7_garbage_between_tags_and_truncated_tail_are_handled() {
    let mut tags = full_stream(interleave({
        let mut frames = vec![];
        for index in 0..8i64 {
            frames.push(avc_video_frame(
                index * 33,
                index % 4 == 0,
                5,
                &[index as u8],
            ));
            frames.push(audio_frame(index * 33, index as u8));
        }
        frames
    }));
    tags.push(end_tag(300));
    let mid = tags.len() / 2;

    let mut bytes = Vec::new();
    bytes.extend_from_slice(b"FLV");
    bytes.push(1);
    bytes.push(0x05);
    bytes.extend_from_slice(&9u32.to_be_bytes());
    bytes.extend_from_slice(&0u32.to_be_bytes());
    for (index, tag) in tags.iter().enumerate() {
        if index == mid {
            bytes.extend_from_slice(b"\xff\xff\xff\xffGARBAGE!\xff\xff\xff\xff");
        }
        encode_raw_tag(tag, &mut bytes);
    }
    let expected_complete = tags.len() - 1;
    bytes.truncate(bytes.len() - 6); // cut into the last tag

    let (output, stats) = repair_library(&bytes);
    let parsed = parse_flv(&output).expect("valid output after resync/truncation");
    assert!(stats.count(CLASS_R7) >= 1, "R7 not detected: {stats:?}");
    assert_eq!(
        parsed.tags.len(),
        expected_complete,
        "partial tail was not dropped cleanly"
    );
    assert!(is_sorted(&video_timestamps(&parsed)));
    assert!(is_sorted(&audio_timestamps(&parsed)));
}

#[test]
fn r8_legacy_hevc_with_aac_rollback() {
    let mut tags = vec![
        metadata_tag(Some(5.0)),
        RawTag {
            tag_type: 9,
            timestamp: 0,
            body: hevc_config_body(false),
        },
    ];
    tags.push(audio_header(0));
    let mut frames = vec![
        legacy_hevc_frame(0, true, &[0x26, 0x01, 0xaf]),
        audio_frame(0, 1),
        audio_frame(43, 2),
        legacy_hevc_frame(33, false, &[0x02, 0x01, 0xaf]),
        audio_frame(86, 3),
        legacy_hevc_frame(2000, true, &[0x26, 0x01, 0xaf]),
        audio_frame(2000, 4),
        audio_frame(2043, 5),
        // AAC rollback: the following keyframe group starts 500 ms in the past.
        legacy_hevc_frame(1500, true, &[0x26, 0x01, 0xaf]),
        audio_frame(1500, 6),
        audio_frame(1543, 7),
        audio_frame(1586, 8),
    ];
    frames.sort_by_key(|tag| (tag.timestamp, if tag.tag_type == 9 { 0 } else { 1 }));
    tags.extend(frames);
    let input = script_header(0x05, &tags);
    let (output, parsed, stats) = repair_and_parse(&input);
    assert!(stats.count(CLASS_R8) >= 1, "R8 not detected: {stats:?}");
    assert!(is_sorted(&video_timestamps(&parsed)));
    assert!(is_sorted(&audio_timestamps(&parsed)));
    let report = analyze_library(&output);
    assert_eq!(
        report
            .video
            .as_ref()
            .and_then(|video| video.codec.as_deref()),
        Some("hevc")
    );
    assert!(
        parsed.tags.iter().any(is_video_header),
        "HEVC header dropped"
    );
}

#[test]
fn r8_enhanced_hevc_metadata_packet_is_preserved() {
    let mut tags = vec![
        metadata_tag(Some(5.0)),
        RawTag {
            tag_type: 9,
            timestamp: 0,
            body: hevc_config_body(true),
        },
        audio_header(0),
        enhanced_metadata_packet(),
    ];
    let mut frames = vec![
        hevc_video_frame(0, true, &[0x26, 0x01, 0xaf]),
        audio_frame(0, 1),
        hevc_video_frame(33, false, &[0x02, 0x01, 0xaf]),
        audio_frame(21, 2),
        hevc_video_frame(66, false, &[0x02, 0x01, 0xaf]),
        audio_frame(43, 3),
        hevc_video_frame(99, true, &[0x26, 0x01, 0xaf]),
        audio_frame(64, 4),
        hevc_video_frame(132, false, &[0x02, 0x01, 0xaf]),
        audio_frame(86, 5),
    ];
    frames.sort_by_key(|tag| (tag.timestamp, if tag.tag_type == 9 { 0 } else { 1 }));
    tags.extend(frames);
    let input = script_header(0x05, &tags);
    let (output, stats) = repair_library(&input);
    let parsed = parse_flv(&output).expect("valid enhanced-FLV output");
    assert_eq!(
        stats.total(),
        0,
        "clean enhanced stream reported anomalies: {stats:?}"
    );
    assert_eq!(parsed.tags.len(), tags.len());
    let enhanced_metadata = parsed
        .tags
        .iter()
        .filter(|tag| is_enhanced_video(tag) && (tag.body[0] & 0x0f) == 4)
        .count();
    assert_eq!(enhanced_metadata, 1, "Enhanced-FLV metadata packet dropped");
    assert!(parsed.tags.iter().any(is_video_header));
    let report = analyze_library(&output);
    assert_eq!(
        report
            .video
            .as_ref()
            .and_then(|video| video.codec.as_deref()),
        Some("hevc")
    );
    // stream mode (non-seekable) must also preserve it
    let cli = run_binary(&["stream"], Some(input.clone()));
    assert!(cli.status.success());
    let cli_parsed = parse_flv(&cli.stdout).expect("valid stream output");
    assert_eq!(cli_parsed.tags.len(), tags.len());
}

#[test]
fn oversized_tag_is_skipped_with_small_buffer() {
    let mut frames = vec![avc_video_frame(0, true, 5, &[1, 2, 3])];
    frames.push(audio_frame(0, 1));
    // A huge audio payload that does not fit a 16 KiB buffer.
    frames.push(RawTag {
        tag_type: 8,
        timestamp: 43,
        body: {
            let mut body = vec![0u8; 40_000];
            body[0] = 0xaf;
            body[1] = 0x01;
            body
        },
    });
    let mut tags = full_stream(frames);
    tags.push(end_tag(100));
    let input = script_header(0x05, &tags);
    let (output, stats) = repair_library_with_limit(&input, 16 * 1024);
    let parsed = parse_flv(&output).expect("valid output");
    assert_eq!(stats.count(CLASS_R7), 1, "expected R7 skip event");
    assert!(
        !parsed
            .tags
            .iter()
            .any(|tag| tag.tag_type == 8 && tag.body.len() > 30_000),
        "oversized tag leaked into output"
    );
}

#[test]
fn zero_size_only_stream_is_valid() {
    let tags = vec![metadata_tag(Some(0.0))];
    let input = script_header(0x00, &tags);
    let (output, _stats) = repair_library(&input);
    let parsed = parse_flv(&output).expect("valid empty-media FLV");
    assert_eq!(parsed.flags, 0);
    assert_eq!(parsed.tags.len(), 1);
    assert_eq!(parsed.tags[0].tag_type, TagType::Script as u8);
}
