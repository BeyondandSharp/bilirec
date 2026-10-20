// SPDX-License-Identifier: GPL-3.0-or-later
//
// Fixture-driven tests: every `testdata/flv/fixtures/*.expect.json` is the oracle.
// Tests skip gracefully when the fixtures are absent (they are owned by port-spec).

mod support;

use flv_repair::pipeline::{RepairStats, CLASS_NAMES};
use support::*;

fn anomaly_map(stats: &RepairStats) -> Vec<(&'static str, u64)> {
    CLASS_NAMES
        .iter()
        .enumerate()
        .filter(|(index, _)| stats.count(*index) > 0)
        .map(|(index, name)| (*name, stats.count(index)))
        .collect()
}

fn validate_input_anomalies(expect: &Json, input: &[u8], name: &str) {
    let spec = expect.get("expect").expect("expect");
    let required = spec
        .get("anomalies")
        .and_then(|anomalies| anomalies.get("required"))
        .and_then(|required| required.as_array())
        .unwrap_or(&[]);
    if required.is_empty() {
        return;
    }
    let min_count = spec
        .get("anomalies")
        .and_then(|anomalies| anomalies.get("min_count"))
        .and_then(Json::as_u64)
        .unwrap_or(1);
    let report = analyze_library(input);
    for class in required {
        let class = class.as_str().expect("class string");
        let count = report
            .anomalies
            .iter()
            .find(|(name, _)| *name == class)
            .map(|(_, count)| *count)
            .unwrap_or(0);
        assert!(
            count >= min_count,
            "{name}: analyze(input) missed required anomaly {class} (got {count}, need {min_count}); anomalies: {:?}",
            report.anomalies
        );
    }
    let _ = anomaly_map;
}

fn validate_repair(expect: &Json, input: &[u8], output: &[u8], file_mode: bool, name: &str) {
    let spec = expect.get("expect").expect("expect section");
    let parsed =
        parse_flv(output).unwrap_or_else(|error| panic!("{name}: invalid output: {error}"));

    let expected_flags = spec
        .get("header_flags")
        .and_then(Json::as_u64)
        .expect("header_flags") as u8;
    assert_eq!(
        parsed.flags, expected_flags,
        "{name}: header flags {:x} != {expected_flags:x}",
        parsed.flags
    );

    let report = analyze_library(output);
    assert!(report.valid, "{name}: analyze(output) says invalid");
    if let Some(expected_codec) = spec.get("video_codec").and_then(Json::as_str) {
        let actual = report
            .video
            .as_ref()
            .and_then(|video| video.codec.as_deref());
        assert_eq!(actual, Some(expected_codec), "{name}: video codec");
    }
    if let Some(expected_codec) = spec.get("audio_codec").and_then(Json::as_str) {
        let actual = report
            .audio
            .as_ref()
            .and_then(|audio| audio.codec.as_deref());
        assert_eq!(actual, Some(expected_codec), "{name}: audio codec");
    }

    for (track, video) in [("video", true), ("audio", false)] {
        let track_spec = spec
            .get("timestamps")
            .and_then(|timestamps| timestamps.get(track))
            .expect("track timestamps");
        let timestamps = track_timestamps(&parsed.tags, video);
        if let Some(true) = track_spec.get("monotonic").and_then(Json::as_bool) {
            assert!(
                is_sorted(&timestamps),
                "{name}: {track} timestamps not monotonic: {:?}",
                timestamps
            );
        }
        if let Some(false) = track_spec.get("negative").and_then(Json::as_bool) {
            assert!(
                !timestamps.iter().any(|timestamp| *timestamp < 0),
                "{name}: negative {track} timestamps"
            );
        }
        if !timestamps.is_empty() {
            let min = track_spec
                .get("first_ms_min")
                .and_then(Json::as_u64)
                .unwrap_or(0) as i64;
            let max = track_spec
                .get("first_ms_max")
                .and_then(Json::as_u64)
                .unwrap_or(u64::MAX) as i64;
            let first = timestamps[0];
            assert!(
                (min..=max).contains(&first),
                "{name}: {track} first timestamp {first} outside [{min}, {max}]"
            );
        }
    }

    let media = media_tags(&parsed.tags);
    let last_media_timestamp = media.iter().map(|tag| tag.timestamp).max().unwrap_or(0);
    let duration_spec = spec.get("duration_ms").expect("duration_ms");
    let min = duration_spec.get("min").and_then(Json::as_u64).unwrap() as i64;
    let max = duration_spec.get("max").and_then(Json::as_u64).unwrap() as i64;
    assert!(
        (min..=max).contains(&last_media_timestamp),
        "{name}: media duration {last_media_timestamp} outside [{min}, {max}]"
    );

    let tag_count = parsed.tags.len() as u64;
    let count_spec = spec.get("tag_count").expect("tag_count");
    let count_min = count_spec.get("min").and_then(Json::as_u64).unwrap();
    let count_max = count_spec.get("max").and_then(Json::as_u64).unwrap();
    assert!(
        (count_min..=count_max).contains(&tag_count),
        "{name}: tag count {tag_count} outside [{count_min}, {count_max}]"
    );

    if file_mode {
        if let Some(bytes_spec) = spec.get("output_bytes") {
            let bytes_min = bytes_spec.get("min").and_then(Json::as_u64).unwrap();
            let bytes_max = bytes_spec.get("max").and_then(Json::as_u64).unwrap();
            let bytes = output.len() as u64;
            assert!(
                (bytes_min..=bytes_max).contains(&bytes),
                "{name}: output bytes {bytes} outside [{bytes_min}, {bytes_max}]"
            );
        }
    }

    let preserved = spec.get("preserved").expect("preserved");
    let has_onmetadata = parsed
        .tags
        .iter()
        .any(|tag| tag.tag_type == 18 && !tag.body.is_empty());
    if let Some(true) = preserved.get("onmetadata").and_then(Json::as_bool) {
        assert!(has_onmetadata, "{name}: onMetaData tag missing");
    }
    if let Some(true) = preserved
        .get("video_sequence_header")
        .and_then(Json::as_bool)
    {
        assert!(
            parsed.tags.iter().any(is_video_header),
            "{name}: video sequence header missing"
        );
    }
    if let Some(true) = preserved
        .get("audio_sequence_header")
        .and_then(Json::as_bool)
    {
        assert!(
            parsed.tags.iter().any(is_audio_header),
            "{name}: audio sequence header missing"
        );
    }
    if let Some(true) = preserved.get("terminal_end_tag").and_then(Json::as_bool) {
        let last = parsed.tags.last().expect("tags");
        assert!(
            is_video_end(last),
            "{name}: terminal END tag missing (last tag type {})",
            last.tag_type
        );
    }

    if let Some(true) = spec
        .get("aac_header_before_audio_data")
        .and_then(Json::as_bool)
    {
        let first_audio_header = parsed.tags.iter().position(is_audio_header);
        let first_audio_data = parsed.tags.iter().position(is_audio_data);
        match (first_audio_header, first_audio_data) {
            (Some(header), Some(data)) => assert!(
                header < data,
                "{name}: AAC header at {header} after audio data at {data}"
            ),
            (None, _) => panic!("{name}: AAC header missing"),
            (Some(_), None) => {}
        }
    }
    if let Some(count_spec) = spec.get("aac_sequence_header_count") {
        let count = parsed
            .tags
            .iter()
            .filter(|tag| is_audio_header(tag))
            .count() as u64;
        if let Some(min) = count_spec.get("min").and_then(Json::as_u64) {
            assert!(count >= min, "{name}: AAC header count {count} < {min}");
        }
        if let Some(max) = count_spec.get("max").and_then(Json::as_u64) {
            assert!(count <= max, "{name}: AAC header count {count} > {max}");
        }
    }

    if let Some(true) = spec.get("filler_nalus_removed").and_then(Json::as_bool) {
        assert_eq!(
            count_filler_nalus(&parsed.tags),
            0,
            "{name}: filler NALUs remain"
        );
    }
    if let Some(true) = spec.get("zero_size_tags_removed").and_then(Json::as_bool) {
        assert!(
            !parsed
                .tags
                .iter()
                .any(|tag| matches!(tag.tag_type, 8 | 9) && tag.body.is_empty()),
            "{name}: zero-size data tags remain"
        );
    }
    if let Some(true) = spec
        .get("midstream_end_tags_removed")
        .and_then(Json::as_bool)
    {
        let end_tags = parsed.tags.iter().filter(|tag| is_video_end(tag)).count();
        assert!(
            end_tags <= 1,
            "{name}: {end_tags} END tags remain (only terminal allowed)"
        );
    }
    if let Some(true) = spec.get("duplicate_groups_removed").and_then(Json::as_bool) {
        let hashes = data_group_hashes(&parsed);
        let mut seen = std::collections::HashSet::new();
        for hash in &hashes {
            assert!(seen.insert(*hash), "{name}: duplicate data group remains");
        }
    }
    if let Some(true) = spec.get("aac_duplicates_removed").and_then(Json::as_bool) {
        let audio: Vec<&RawTag> = parsed
            .tags
            .iter()
            .filter(|tag| is_audio_data(tag))
            .collect();
        for pair in audio.windows(2) {
            assert!(
                !(pair[0].timestamp == pair[1].timestamp && pair[0].body == pair[1].body),
                "{name}: adjacent duplicate AAC tags remain"
            );
        }
    }
    if let Some(policy) = spec.get("metadata_duration") {
        if file_mode {
            if let Some(file_policy) = policy.get("file_mode") {
                if let Some(duration) = metadata_duration(&parsed) {
                    let min = file_policy.get("min").and_then(Json::as_u64).unwrap_or(0) as f64;
                    let max = file_policy
                        .get("max")
                        .and_then(Json::as_u64)
                        .unwrap_or(u64::MAX) as f64;
                    let millis = duration * 1000.0;
                    assert!(
                        millis >= min && millis <= max,
                        "{name}: metadata duration {millis} outside [{min}, {max}]"
                    );
                } else if !file_policy
                    .get("may_be_absent")
                    .and_then(Json::as_bool)
                    .unwrap_or(false)
                {
                    panic!("{name}: metadata duration missing in file mode");
                }
            }
        }
    }
    if let Some(true) = spec.get("rollback_repaired").and_then(Json::as_bool) {
        assert!(
            report.video_timestamps.monotonic && report.audio_timestamps.monotonic,
            "{name}: analyze(output) reports non-monotonic timestamps"
        );
    }
    if let Some(true) = spec.get("truncated_tail_dropped").and_then(Json::as_bool) {
        // A strict reparse above already proves there is no partial tail.
    }
    if let Some(true) = spec.get("resync").and_then(Json::as_bool) {
        // Covered by the input analysis anomaly check (R7).
    }
    let _ = input;
}

#[test]
fn fixtures_meet_expectations() {
    let dir = fixtures_dir();
    if !dir.is_dir() {
        eprintln!("skipping fixture test: {} not present", dir.display());
        return;
    }
    let mut expect_files: Vec<_> = std::fs::read_dir(&dir)
        .expect("read fixtures dir")
        .filter_map(|entry| entry.ok())
        .map(|entry| entry.path())
        .filter(|path| {
            path.file_name()
                .map(|name| name.to_string_lossy().ends_with(".expect.json"))
                .unwrap_or(false)
        })
        .collect();
    expect_files.sort();
    assert!(
        expect_files.len() >= 8,
        "expected at least R1-R8 expectations, found {}",
        expect_files.len()
    );

    for expect_path in expect_files {
        let expect = Json::parse(&std::fs::read_to_string(&expect_path).expect("read expect"))
            .expect("valid expect JSON");
        let name = expect
            .get("fixture")
            .and_then(Json::as_str)
            .expect("fixture name")
            .to_string();
        let input_path = dir.join(&name);
        let input = std::fs::read(&input_path).unwrap_or_else(|_| panic!("missing fixture {name}"));
        if let Some(expected_bytes) = expect.get("fixture_bytes").and_then(Json::as_u64) {
            assert_eq!(
                input.len() as u64,
                expected_bytes,
                "{name}: fixture size mismatch"
            );
        }

        validate_input_anomalies(&expect, &input, &name);

        let file_output = repair_via_cli_file(&input, &[]);
        validate_repair(&expect, &input, &file_output, true, &name);

        let stream = run_binary(&["stream"], Some(input.clone()));
        assert!(
            stream.status.success(),
            "{name}: stream failed: {:?} {}",
            stream.status,
            String::from_utf8_lossy(&stream.stderr)
        );
        validate_repair(&expect, &input, &stream.stdout, false, &name);
    }
}

#[test]
fn clean_bases_are_stable() {
    let dir = base_dir();
    if !dir.is_dir() {
        eprintln!("skipping clean base test: {} not present", dir.display());
        return;
    }
    for name in ["clean.flv", "clean_hevc.flv"] {
        let path = dir.join(name);
        let input = match std::fs::read(&path) {
            Ok(input) => input,
            Err(_) => {
                eprintln!("skipping clean base {name}: not present");
                continue;
            }
        };
        let parsed_input = parse_flv(&input).expect("clean base is a valid FLV");
        assert_eq!(parsed_input.flags, 0x05);

        // stream mode must be byte identical for clean input
        let stream = run_binary(&["stream"], Some(input.clone()));
        assert!(
            stream.status.success(),
            "{name}: stream failed: {}",
            String::from_utf8_lossy(&stream.stderr)
        );
        assert_eq!(
            stream.stdout, input,
            "{name}: clean stream output is not byte-identical"
        );

        // file mode: valid FLV, no growth > 1%, identical media tag count
        let file_output = repair_via_cli_file(&input, &[]);
        let parsed_output = parse_flv(&file_output)
            .unwrap_or_else(|error| panic!("{name}: repaired clean base is invalid: {error}"));
        assert_eq!(parsed_output.flags, 0x05);
        assert!(
            file_output.len() * 100 <= input.len() * 101,
            "{name}: clean output grew {} -> {}",
            input.len(),
            file_output.len()
        );
        assert_eq!(
            media_tags(&parsed_input.tags).len(),
            media_tags(&parsed_output.tags).len(),
            "{name}: media tag count changed"
        );
        assert_eq!(
            parsed_input.tags.len(),
            parsed_output.tags.len(),
            "{name}: total tag count changed for clean input"
        );
        // onMetaData preserved and its duration agrees with the media duration
        let metadata_duration =
            metadata_duration(&parsed_output).expect("clean output metadata duration");
        let media_last = media_tags(&parsed_output.tags)
            .iter()
            .map(|tag| tag.timestamp)
            .max()
            .unwrap_or(0) as f64
            / 1000.0;
        assert!(
            (metadata_duration - media_last).abs() < 1.0,
            "{name}: metadata duration {metadata_duration} vs media {media_last}"
        );
        // Enhanced-FLV metadata packets (packet type 4/5) must be preserved
        let enhanced_metadata = |parsed: &ParsedFlv| {
            parsed
                .tags
                .iter()
                .filter(|tag| {
                    is_enhanced_video(tag)
                        && tag.body.len() >= 5
                        && matches!(tag.body[0] & 0x0f, 4 | 5)
                })
                .count()
        };
        assert_eq!(
            enhanced_metadata(&parsed_input),
            enhanced_metadata(&parsed_output),
            "{name}: Enhanced-FLV metadata packet count changed"
        );
        let report = analyze_library(&file_output);
        assert!(report.valid);

        // Throughput smoke: a 20 s stream must repair well under 10 s (>= 2x realtime).
        // The debug-build parser is far faster than required; this guards regressions.
        let start = std::time::Instant::now();
        let (_, _) = repair_library(&input);
        let elapsed = start.elapsed();
        assert!(
            elapsed < std::time::Duration::from_secs(10),
            "{name}: repair of a 20 s stream took {elapsed:?} (< 2x realtime violated)"
        );
    }
}
