// SPDX-License-Identifier: GPL-3.0-or-later
//
// Process contract tests: CLI arguments, exit codes, stream/file/analyze behavior.

mod support;

use std::io::Write;
use std::process::{Command, Stdio};
use std::time::Duration;
use support::*;

fn validate_analyze_schema(json: &Json) {
    assert_eq!(json.get("version").and_then(Json::as_u64), Some(1));
    assert!(json.get("valid").and_then(Json::as_bool).is_some());
    assert!(json.get("bytes").and_then(Json::as_u64).is_some());
    assert!(json.get("duration_ms").and_then(Json::as_u64).is_some());
    for track in ["video", "audio"] {
        let value = json.get(track).expect("track object");
        if let Json::Null = value {
            continue;
        }
        let codec = value.get("codec").expect("codec");
        assert!(matches!(codec, Json::Null | Json::String(_)));
        if track == "video" {
            assert!(value.get("width").is_some());
            assert!(value.get("height").is_some());
        } else {
            assert!(value.get("sample_rate").is_some());
            assert!(value.get("channels").is_some());
        }
    }
    let timestamps = json.get("timestamps").expect("timestamps");
    for track in ["video", "audio"] {
        let value = timestamps.get(track).expect("track timestamps");
        if let Json::Null = value {
            continue;
        }
        assert!(value.get("first").and_then(Json::as_u64).is_some());
        assert!(value.get("last").and_then(Json::as_u64).is_some());
        assert!(value.get("monotonic").and_then(Json::as_bool).is_some());
        assert!(value.get("jumps").and_then(Json::as_u64).is_some());
    }
    let anomalies = json
        .get("anomalies")
        .and_then(Json::as_array)
        .expect("anomalies array");
    for anomaly in anomalies {
        assert!(anomaly.get("class").and_then(Json::as_str).is_some());
        assert!(anomaly.get("count").and_then(Json::as_u64).is_some());
    }
}

#[test]
fn version_and_help_exit_zero() {
    let version = run_binary(&["--version"], None);
    assert!(version.status.success());
    let stdout = String::from_utf8_lossy(&version.stdout);
    assert!(
        stdout.starts_with("flvrepair "),
        "unexpected version: {stdout}"
    );

    let help = run_binary(&["--help"], None);
    assert!(help.status.success());
    assert!(String::from_utf8_lossy(&help.stdout).contains("stream"));
}

#[test]
fn invalid_arguments_exit_2() {
    for args in [
        vec![],
        vec!["nonsense"],
        vec!["stream", "--mode", "wrong"],
        vec!["stream", "--json"],
        vec!["file", "--input", "a"],
        vec!["analyze"],
        vec!["stream", "--max-buffer", "0"],
        vec!["stream", "--unknown-flag"],
    ] {
        let output = run_binary(&args, None);
        assert_eq!(
            output.status.code(),
            Some(2),
            "args {args:?} should exit 2, stderr: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }
}

#[test]
fn stream_round_trip_is_byte_identical() {
    let input = clean_avc_stream();
    let output = run_binary(&["stream"], Some(input.clone()));
    assert!(
        output.status.success(),
        "stream failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, input);
    assert!(parse_flv(&output.stdout).is_ok());
    // stderr must be JSON lines only, one object per line
    let stderr = String::from_utf8_lossy(&output.stderr);
    for line in stderr.lines().filter(|line| !line.trim().is_empty()) {
        Json::parse(line).unwrap_or_else(|error| panic!("non-JSON stderr line {line:?}: {error}"));
    }
}

#[test]
fn stream_best_effort_on_garbage_emits_valid_flv() {
    let output = run_binary(&["stream"], Some(b"this is not an flv file".to_vec()));
    assert_eq!(output.status.code(), Some(0));
    let parsed = parse_flv(&output.stdout).expect("best-effort output must be a valid FLV");
    assert!(parsed.flags == 0);
    assert!(parsed.tags.iter().any(|tag| tag.tag_type == 18));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("\"class\":\"R7\""), "stderr: {stderr}");
}

#[test]
fn stream_strict_on_garbage_exits_3() {
    let output = run_binary(&["stream", "--mode", "strict"], Some(b"garbage".to_vec()));
    assert_eq!(output.status.code(), Some(3));
}

#[test]
fn truncated_stream_strict_exits_3_best_effort_exits_0() {
    let input = clean_avc_stream();
    let truncated = input[..input.len() / 2].to_vec();

    let best_effort = run_binary(&["stream"], Some(truncated.clone()));
    assert_eq!(best_effort.status.code(), Some(0));
    let parsed = parse_flv(&best_effort.stdout).expect("valid best-effort output");
    assert!(parsed.tags.len() > 1);

    let strict = run_binary(&["stream", "--mode", "strict"], Some(truncated));
    assert_eq!(strict.status.code(), Some(3));
    // output produced before the error must still be a valid FLV
    parse_flv(&strict.stdout).expect("strict output is still valid up to the error");
}

#[test]
fn analyze_json_schema_on_clean_and_corrupt_input() {
    let temp = TempDir::new();
    let clean_path = temp.file("clean.flv");
    std::fs::write(&clean_path, clean_avc_stream()).expect("write clean");

    let json = analyze_json_file(&clean_path);
    validate_analyze_schema(&json);
    assert_eq!(json.get("valid").and_then(Json::as_bool), Some(true));
    assert_eq!(
        json.get("video")
            .and_then(|video| video.get("codec"))
            .and_then(Json::as_str),
        Some("avc")
    );

    // human-readable mode still exits 0 and prints the key fields
    let human = run_binary(
        &["analyze", "--input", &clean_path.display().to_string()],
        None,
    );
    assert!(human.status.success());
    let text = String::from_utf8_lossy(&human.stdout);
    assert!(text.contains("valid: true"), "{text}");
    assert!(text.contains("duration_ms:"), "{text}");

    // corrupt input: valid=false but still a JSON report and exit 0
    let garbage_path = temp.file("garbage.flv");
    std::fs::write(&garbage_path, b"not an flv").expect("write garbage");
    let json = analyze_json_file(&garbage_path);
    validate_analyze_schema(&json);
    assert_eq!(json.get("valid").and_then(Json::as_bool), Some(false));
}

#[test]
fn file_mode_patches_metadata_and_flags() {
    let temp = TempDir::new();
    let input_path = temp.file("in.flv");
    let output_path = temp.file("out.flv");

    let mut tags = vec![metadata_tag(None)];
    tags.push(RawTag {
        tag_type: 9,
        timestamp: 0,
        body: avc_config_body(),
    });
    tags.push(audio_header(0));
    let mut frames = vec![];
    for index in 0..10i64 {
        frames.push(avc_video_frame(
            index * 33,
            index % 5 == 0,
            5,
            &[index as u8],
        ));
        frames.push(audio_frame(index * 33, index as u8));
    }
    frames.sort_by_key(|tag| (tag.timestamp, tag.tag_type));
    tags.extend(frames);
    std::fs::write(&input_path, build_flv(0x00, &tags)).expect("write input");

    let status = Command::new(binary())
        .args([
            "file",
            "--input",
            &input_path.display().to_string(),
            "--output",
            &output_path.display().to_string(),
        ])
        .status()
        .expect("run file mode");
    assert!(status.success());
    let output = std::fs::read(&output_path).expect("read output");
    let parsed = parse_flv(&output).expect("valid file output");
    assert_eq!(parsed.flags, 0x05);
    let duration = metadata_duration(&parsed).expect("duration present in file mode");
    assert!(duration > 0.0);
}

#[test]
fn missing_input_file_is_io_error() {
    let output = run_binary(
        &[
            "file",
            "--input",
            "/definitely/not/here.flv",
            "--output",
            "/tmp/x.flv",
        ],
        None,
    );
    assert_eq!(output.status.code(), Some(1));
    let analyze = run_binary(&["analyze", "--input", "/definitely/not/here.flv"], None);
    assert_eq!(analyze.status.code(), Some(1));
}

#[test]
fn same_input_and_output_is_rejected() {
    let temp = TempDir::new();
    let path = temp.file("same.flv");
    std::fs::write(&path, clean_avc_stream()).expect("write");
    let output = run_binary(
        &[
            "file",
            "--input",
            &path.display().to_string(),
            "--output",
            &path.display().to_string(),
        ],
        None,
    );
    assert_eq!(output.status.code(), Some(2));
}

#[test]
fn sigterm_flushes_and_exits_zero() {
    let input = clean_avc_stream();
    let mut child = Command::new(binary())
        .args(["stream"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn stream");
    let mut stdin = child.stdin.take().expect("stdin");
    // Feed the prefix only, then signal while the sidecar is still reading.
    stdin
        .write_all(&input[..input.len() / 2])
        .expect("write prefix");
    stdin.flush().expect("flush prefix");
    std::thread::sleep(Duration::from_millis(100));
    let status = Command::new("kill")
        .args(["-TERM", &child.id().to_string()])
        .status()
        .expect("run kill");
    assert!(status.success(), "kill -TERM failed");
    drop(stdin); // close stdin so the blocked read returns EOF
    let output = child.wait_with_output().expect("wait for stream");
    assert!(
        output.status.success(),
        "expected exit 0 after SIGTERM, got {:?} stderr {}",
        output.status,
        String::from_utf8_lossy(&output.stderr)
    );
    parse_flv(&output.stdout).expect("valid FLV after SIGTERM flush");
}

#[test]
fn max_buffer_is_bounded() {
    let input = clean_avc_stream();
    let output = run_binary(&["stream", "--max-buffer", "64KiB"], Some(input.clone()));
    assert_eq!(output.status.code(), Some(0));
    parse_flv(&output.stdout).expect("valid output with small buffer");
}

#[test]
fn write_metadata_drop_strips_script_tags() {
    let input = clean_avc_stream();

    let keep = run_binary(&["stream"], Some(input.clone()));
    assert!(keep.status.success());
    assert_eq!(keep.stdout, input, "default keep must stay byte-identical");

    let explicit_keep = run_binary(&["stream", "--write-metadata", "keep"], Some(input.clone()));
    assert!(explicit_keep.status.success());
    assert_eq!(explicit_keep.stdout, input);

    let drop = run_binary(&["stream", "--write-metadata", "drop"], Some(input.clone()));
    assert!(
        drop.status.success(),
        "drop stream failed: {}",
        String::from_utf8_lossy(&drop.stderr)
    );
    assert!(!drop.stdout.is_empty());
    assert!(!parse_flv(&drop.stdout)
        .expect("valid drop output")
        .tags
        .is_empty());
    let drop_parsed = parse_flv(&drop.stdout).expect("valid drop output");
    assert_eq!(drop_parsed.flags, 0x05);
    assert!(
        drop_parsed.tags.iter().all(|tag| tag.tag_type != 18),
        "drop mode must not emit script tags"
    );
    let keep_parsed = parse_flv(&keep.stdout).expect("valid keep output");
    assert_eq!(
        media_tags(&drop_parsed.tags).len(),
        media_tags(&keep_parsed.tags).len(),
        "drop mode must keep all media tags"
    );

    // file mode: same guarantee, plus correct header flags after the flags patch.
    let file_drop = repair_via_cli_file(&input, &["--write-metadata", "drop"]);
    let file_parsed = parse_flv(&file_drop).expect("valid file drop output");
    assert_eq!(file_parsed.flags, 0x05);
    assert!(file_parsed.tags.iter().all(|tag| tag.tag_type != 18));
    let temp = TempDir::new();
    let drop_path = temp.file("drop.flv");
    std::fs::write(&drop_path, &file_drop).expect("write");
    let analyze = run_binary(
        &[
            "analyze",
            "--input",
            &drop_path.display().to_string(),
            "--json",
        ],
        None,
    );
    assert!(analyze.status.success());
    let json = Json::parse(&String::from_utf8_lossy(&analyze.stdout)).expect("valid json");
    assert_eq!(json.get("valid").and_then(Json::as_bool), Some(true));
    assert_eq!(
        json.get("video")
            .and_then(|video| video.get("codec"))
            .and_then(Json::as_str),
        Some("avc")
    );

    // invalid values / wrong command
    let bad_value = run_binary(
        &["stream", "--write-metadata", "maybe"],
        Some(input.clone()),
    );
    assert_eq!(bad_value.status.code(), Some(2));
    let wrong_command = run_binary(
        &["analyze", "--input", "a.flv", "--write-metadata", "drop"],
        None,
    );
    assert_eq!(wrong_command.status.code(), Some(2));
    let export_wrong = run_binary(
        &[
            "export",
            "--input",
            "a.flv",
            "--output",
            "a.xml",
            "--write-metadata",
            "drop",
        ],
        None,
    );
    assert_eq!(export_wrong.status.code(), Some(2));
}
