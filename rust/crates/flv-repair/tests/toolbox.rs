// SPDX-License-Identifier: GPL-3.0-or-later
//
// Toolbox command tests: FLV XML/zip export and Bilibili danmaku start-time/merge,
// including golden fixtures under `tests/fixtures/danmaku/`.

mod support;

use flv_repair::toolbox::danmaku;
use flv_repair::toolbox::xml::{self, XmlEvent, XmlReader};
use flv_repair::toolbox::zip;
use std::path::PathBuf;
use support::*;

fn export_fixture_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("tests")
        .join("fixtures")
        .join("export")
}

fn fixture_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("tests")
        .join("fixtures")
        .join("danmaku")
}

fn normalize_version(text: &str) -> String {
    let version = format!("version=\"{}\"", env!("CARGO_PKG_VERSION"));
    text.replace(&version, "version=\"<version>\"")
}

fn collect_events(xml_text: &str) -> Vec<XmlEvent> {
    let mut reader = XmlReader::new(xml_text.as_bytes());
    let mut events = Vec::new();
    while let Some(event) = reader.next_event().expect("well-formed XML") {
        events.push(event);
    }
    events
}

// ---------------------------------------------------------------------------------------
// export
// ---------------------------------------------------------------------------------------

#[test]
fn export_xml_schema() {
    let temp = TempDir::new();
    let input = temp.file("sample.flv");
    let output = temp.file("sample.xml");
    let flv = clean_avc_stream();
    std::fs::write(&input, &flv).expect("write input");
    let expected_tags = parse_flv(&flv).expect("valid flv").tags.len();

    let result = run_binary(
        &[
            "export",
            "--input",
            &input.display().to_string(),
            "--output",
            &output.display().to_string(),
        ],
        None,
    );
    assert!(
        result.status.success(),
        "export failed: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    let bytes = std::fs::read(&output).expect("read xml");
    assert!(bytes.starts_with(&[0xef, 0xbb, 0xbf]), "expected UTF-8 BOM");
    let text = String::from_utf8(bytes[3..].to_vec()).expect("utf-8 xml");
    assert!(text.starts_with("<?xml version=\"1.0\" encoding=\"utf-8\"?>"));

    let events = collect_events(&text);
    // Root element and Meta section.
    assert!(
        events.iter().any(
            |event| matches!(event, XmlEvent::Start { name, .. } if name == "BililiveRecorderFlv")
        ),
        "missing BililiveRecorderFlv root"
    );
    let meta_names: Vec<&str> = events
        .iter()
        .filter_map(|event| match event {
            XmlEvent::Start { name, .. } if name == "Meta" => Some("Meta"),
            XmlEvent::Start { name, .. }
                if matches!(
                    name.as_str(),
                    "Version"
                        | "ExportTime"
                        | "FileSize"
                        | "FileCreationTime"
                        | "FileModificationTime"
                ) =>
            {
                Some(name)
            }
            _ => None,
        })
        .collect();
    assert_eq!(
        meta_names,
        vec![
            "Meta",
            "Version",
            "ExportTime",
            "FileSize",
            "FileCreationTime",
            "FileModificationTime"
        ]
    );

    // Tags list: every input tag must appear with Type/Size/Timestamp/DataHash.
    let mut tags_seen = 0usize;
    let mut tag_names: Vec<(&str, Option<&str>)> = Vec::new();
    let mut has_script_data = false;
    let mut has_binary_data = false;
    let mut has_nalus = false;
    let mut composition_time_seen = false;
    for event in &events {
        match event {
            XmlEvent::Start {
                name, attributes, ..
            } if name == "Tag" => {
                tags_seen += 1;
                let tag_type = xml::find_attribute(attributes, "Type").unwrap_or("");
                let size = xml::find_attribute(attributes, "Size").unwrap_or("");
                assert!(!size.is_empty(), "Tag missing Size");
                assert!(
                    xml::find_attribute(attributes, "Timestamp").is_some(),
                    "Tag missing Timestamp"
                );
                let hash = xml::find_attribute(attributes, "DataHash").expect("DataHash");
                assert_eq!(hash.len(), 16, "DataHash should be 16 hex chars");
                assert!(hash.chars().all(|character| character.is_ascii_hexdigit()));
                tag_names.push((
                    match tag_type {
                        "Audio" => "Audio",
                        "Video" => "Video",
                        "Script" => "Script",
                        other => panic!("unexpected tag type {other}"),
                    },
                    None,
                ));
            }
            XmlEvent::Start {
                name, attributes, ..
            } if name == "ScriptData" => {
                has_script_data = true;
            }
            XmlEvent::Start { name, .. } if name == "BinaryData" => has_binary_data = true,
            XmlEvent::Start { name, .. } if name == "Nalus" => has_nalus = true,
            XmlEvent::Start {
                name, attributes, ..
            } if name == "ExtraData"
                && xml::find_attribute(attributes, "CompositionTime").is_some() =>
            {
                composition_time_seen = true;
            }
            _ => {}
        }
    }
    let flagged_headers = tag_names
        .iter()
        .filter(|(tag_type, _)| *tag_type == "Video")
        .count();
    assert!(flagged_headers > 0);
    assert_eq!(
        tags_seen, expected_tags,
        "export must contain every FLV tag"
    );
    assert!(has_script_data, "script tag must export ScriptData");
    assert!(has_binary_data, "header tags must export BinaryData");
    assert!(has_nalus, "AVC video tags must export NALUs");
    assert!(
        composition_time_seen,
        "video tags must export composition time"
    );
    assert!(text.contains("<Tags>") && text.contains("</BililiveRecorderFlv>"));
    assert!(text.contains("onMetaData"));
}

#[test]
fn export_json_summary() {
    let temp = TempDir::new();
    let input = temp.file("sample.flv");
    let output = temp.file("sample.xml");
    std::fs::write(&input, clean_avc_stream()).expect("write input");
    let result = run_binary(
        &[
            "export",
            "--input",
            &input.display().to_string(),
            "--output",
            &output.display().to_string(),
            "--json",
        ],
        None,
    );
    assert!(result.status.success());
    let json = Json::parse(&String::from_utf8_lossy(&result.stdout)).expect("valid json");
    assert_eq!(json.get("version").and_then(Json::as_u64), Some(1));
    assert_eq!(json.get("format").and_then(Json::as_str), Some("xml"));
    assert_eq!(
        json.get("output_bytes").and_then(Json::as_u64),
        Some(std::fs::metadata(&output).expect("metadata").len())
    );
    assert!(json.get("tags").and_then(Json::as_u64).unwrap_or(0) > 0);
}

#[test]
fn export_zip_contains_the_same_xml() {
    let temp = TempDir::new();
    let input = temp.file("sample.flv");
    let output = temp.file("sample.zip");
    let flv = clean_avc_stream();
    std::fs::write(&input, &flv).expect("write input");
    let expected_tags = parse_flv(&flv).expect("valid flv").tags.len();

    let result = run_binary(
        &[
            "export",
            "--input",
            &input.display().to_string(),
            "--output",
            &output.display().to_string(),
        ],
        None,
    );
    assert!(
        result.status.success(),
        "export failed: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    let archive = std::fs::read(&output).expect("read zip");
    let (name, data) = zip::read_single_stored_entry(&archive).expect("read zip entry");
    assert_eq!(name, "sample.flv.brec.xml");
    assert!(data.starts_with(&[0xef, 0xbb, 0xbf]));
    let text = String::from_utf8(data[3..].to_vec()).expect("utf8");
    assert!(text.starts_with("<?xml"));
    let tag_count = collect_events(&text)
        .iter()
        .filter(|event| matches!(event, XmlEvent::Start { name, .. } if name == "Tag"))
        .count();
    assert_eq!(tag_count, expected_tags);
}

#[test]
fn export_invalid_input_and_io_errors() {
    let temp = TempDir::new();
    let garbage = temp.file("garbage.flv");
    std::fs::write(&garbage, b"this is not an flv file").expect("write garbage");
    let output = temp.file("out.xml");
    let result = run_binary(
        &[
            "export",
            "--input",
            &garbage.display().to_string(),
            "--output",
            &output.display().to_string(),
        ],
        None,
    );
    assert_eq!(result.status.code(), Some(3), "invalid input must exit 3");
    assert!(
        String::from_utf8_lossy(&result.stderr).contains("\"event\":\"invalid_input\""),
        "stderr: {}",
        String::from_utf8_lossy(&result.stderr)
    );

    let missing = run_binary(
        &[
            "export",
            "--input",
            "/definitely/not/here.flv",
            "--output",
            &output.display().to_string(),
        ],
        None,
    );
    assert_eq!(missing.status.code(), Some(1));

    let input = temp.file("sample.flv");
    std::fs::write(&input, clean_avc_stream()).expect("write input");
    let bad_output = run_binary(
        &[
            "export",
            "--input",
            &input.display().to_string(),
            "--output",
            &temp.path().display().to_string(), // directory, cannot be written as file
        ],
        None,
    );
    assert_eq!(bad_output.status.code(), Some(1));
}

// ---------------------------------------------------------------------------------------
// danmaku-start-time
// ---------------------------------------------------------------------------------------

#[test]
fn danmaku_start_time_text_matches_golden() {
    let dir = fixture_dir();
    if !dir.is_dir() {
        eprintln!("skipping: {} missing", dir.display());
        return;
    }
    let result = run_binary_in(
        Some(&dir),
        &["danmaku-start-time", "--inputs", "a.xml", "b.xml"],
        None,
    );
    assert!(
        result.status.success(),
        "start-time failed: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    let expected =
        std::fs::read_to_string(dir.join("expected_start_times.txt")).expect("golden start times");
    assert_eq!(String::from_utf8_lossy(&result.stdout), expected);
}

#[test]
fn danmaku_start_time_json() {
    let dir = fixture_dir();
    if !dir.is_dir() {
        eprintln!("skipping: {} missing", dir.display());
        return;
    }
    let result = run_binary_in(
        Some(&dir),
        &["danmaku-start-time", "--inputs", "a.xml", "b.xml", "--json"],
        None,
    );
    assert!(result.status.success());
    let json = Json::parse(&String::from_utf8_lossy(&result.stdout)).expect("valid json");
    let start_times = json
        .get("start_times")
        .and_then(Json::as_array)
        .expect("start_times array");
    assert_eq!(start_times.len(), 2);
    assert_eq!(
        start_times[0].get("file").and_then(Json::as_str),
        Some("a.xml")
    );
    assert_eq!(
        start_times[0].get("starttime").and_then(Json::as_str),
        Some("2026-10-20T10:00:00+08:00")
    );
}

#[test]
fn danmaku_start_time_errors() {
    let temp = TempDir::new();
    let missing = run_binary(&["danmaku-start-time", "--inputs", "no-such.xml"], None);
    assert_eq!(missing.status.code(), Some(1));

    let malformed = temp.file("bad.xml");
    std::fs::write(&malformed, b"<i><d p=\"oops\">x</d></i>").expect("write");
    let invalid = run_binary(
        &[
            "danmaku-start-time",
            "--inputs",
            &malformed.display().to_string(),
        ],
        None,
    );
    assert_eq!(invalid.status.code(), Some(3));

    let no_start = temp.file("nostart.xml");
    std::fs::write(&no_start, b"<i><d p=\"1.0,1\">x</d></i>").expect("write");
    let result = run_binary(
        &[
            "danmaku-start-time",
            "--inputs",
            &no_start.display().to_string(),
        ],
        None,
    );
    assert_eq!(result.status.code(), Some(3));
}

// ---------------------------------------------------------------------------------------
// danmaku-merge
// ---------------------------------------------------------------------------------------

#[test]
fn danmaku_merge_default_matches_golden() {
    let dir = fixture_dir();
    if !dir.is_dir() {
        eprintln!("skipping: {} missing", dir.display());
        return;
    }
    let temp = TempDir::new();
    let output = temp.file("merged.xml");
    let result = run_binary_in(
        Some(&dir),
        &[
            "danmaku-merge",
            "--output",
            &output.display().to_string(),
            "--inputs",
            "a.xml",
            "b.xml",
        ],
        None,
    );
    assert!(
        result.status.success(),
        "merge failed: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    let actual = std::fs::read_to_string(&output).expect("read merged");
    let expected = std::fs::read_to_string(dir.join("expected_merge.xml")).expect("golden merge");
    assert_eq!(
        normalize_version(&actual),
        normalize_version(&expected),
        "merged output differs from golden fixture"
    );

    // Structural invariants: min start time and monotonically increasing timestamps.
    let parsed = danmaku::read_file(&output).expect("parse merged output");
    assert_eq!(
        parsed.start_time.as_deref(),
        Some("2026-10-20T10:00:00+08:00"),
        "merged start_time must be the minimum"
    );
    assert_eq!(parsed.entries.len(), 8);
    let timestamps: Vec<i64> = parsed
        .entries
        .iter()
        .map(|entry| entry.timestamp_ms)
        .collect();
    assert!(
        timestamps.windows(2).all(|pair| pair[1] >= pair[0]),
        "merged timestamps must be monotonic: {timestamps:?}"
    );
}

#[test]
fn danmaku_merge_explicit_offsets_match_golden() {
    let dir = fixture_dir();
    if !dir.is_dir() {
        eprintln!("skipping: {} missing", dir.display());
        return;
    }
    let temp = TempDir::new();
    let output = temp.file("merged.xml");
    let result = run_binary_in(
        Some(&dir),
        &[
            "danmaku-merge",
            "--output",
            &output.display().to_string(),
            "--inputs",
            "a.xml",
            "b.xml",
            "--offsets",
            "0,0",
        ],
        None,
    );
    assert!(
        result.status.success(),
        "merge failed: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    let actual = std::fs::read_to_string(&output).expect("read merged");
    let expected =
        std::fs::read_to_string(dir.join("expected_merge_offsets.xml")).expect("golden merge");
    assert_eq!(normalize_version(&actual), normalize_version(&expected));

    // Negative offsets must clamp at zero rather than emit negative timestamps.
    let clamped_output = temp.file("clamped.xml");
    let clamped = run_binary_in(
        Some(&dir),
        &[
            "danmaku-merge",
            "--output",
            &clamped_output.display().to_string(),
            "--inputs",
            "a.xml",
            "b.xml",
            "--offsets",
            "0,-5000",
        ],
        None,
    );
    assert!(clamped.status.success());
    let parsed = danmaku::read_file(&clamped_output).expect("parse clamped output");
    assert!(
        parsed.entries.iter().all(|entry| entry.timestamp_ms >= 0),
        "negative offsets must be clamped"
    );
}

#[test]
fn danmaku_merge_errors() {
    let dir = fixture_dir();
    if !dir.is_dir() {
        eprintln!("skipping: {} missing", dir.display());
        return;
    }
    let temp = TempDir::new();
    let output = temp.file("merged.xml");

    // offsets count mismatch
    let mismatch = run_binary_in(
        Some(&dir),
        &[
            "danmaku-merge",
            "--output",
            &output.display().to_string(),
            "--inputs",
            "a.xml",
            "b.xml",
            "--offsets",
            "0,1000,2000",
        ],
        None,
    );
    assert_eq!(mismatch.status.code(), Some(2));

    // single input rejected by argument validation
    let single = run_binary_in(
        Some(&dir),
        &[
            "danmaku-merge",
            "--output",
            &output.display().to_string(),
            "--inputs",
            "a.xml",
        ],
        None,
    );
    assert_eq!(single.status.code(), Some(2));

    // invalid XML input
    let bad = temp.file("bad.xml");
    std::fs::write(&bad, b"<i><d p=\"not-a-timestamp\">x</d></i>").expect("write");
    let invalid = run_binary_in(
        Some(&dir),
        &[
            "danmaku-merge",
            "--output",
            &output.display().to_string(),
            "--inputs",
            "a.xml",
            &bad.display().to_string(),
        ],
        None,
    );
    assert_eq!(invalid.status.code(), Some(3));

    // unwritable output directory
    let unwritable = run_binary_in(
        Some(&dir),
        &[
            "danmaku-merge",
            "--output",
            &temp.path().display().to_string(),
            "--inputs",
            "a.xml",
            "b.xml",
        ],
        None,
    );
    assert_eq!(unwritable.status.code(), Some(1));
}

#[test]
fn danmaku_merge_without_start_times_requires_offsets() {
    let temp = TempDir::new();
    let a = temp.file("a.xml");
    let b = temp.file("b.xml");
    std::fs::write(&a, b"<i><d p=\"1.000,1\">a</d></i>").expect("write a");
    std::fs::write(&b, b"<i><d p=\"2.000,1\">b</d></i>").expect("write b");
    let output = temp.file("out.xml");

    let without_offsets = run_binary(
        &[
            "danmaku-merge",
            "--output",
            &output.display().to_string(),
            "--inputs",
            &a.display().to_string(),
            &b.display().to_string(),
        ],
        None,
    );
    assert_eq!(without_offsets.status.code(), Some(3));

    let with_offsets = run_binary(
        &[
            "danmaku-merge",
            "--output",
            &output.display().to_string(),
            "--inputs",
            &a.display().to_string(),
            &b.display().to_string(),
            "--offsets",
            "0,1000",
        ],
        None,
    );
    assert!(with_offsets.status.success());
    let parsed = danmaku::read_file(&output).expect("parse output");
    assert_eq!(parsed.entries.len(), 2);
}

fn tags_section(xml_text: &str) -> &str {
    let start = xml_text.find("  <Tags>\n").expect("Tags start");
    let end = xml_text.find("  </Tags>\n").expect("Tags end") + "  </Tags>\n".len();
    &xml_text[start..end]
}

fn export_synthetic_tags_section() -> String {
    let temp = TempDir::new();
    let input = temp.file("sample.flv");
    let output = temp.file("sample.xml");
    std::fs::write(&input, clean_avc_stream()).expect("write input");
    let result = run_binary(
        &[
            "export",
            "--input",
            &input.display().to_string(),
            "--output",
            &output.display().to_string(),
        ],
        None,
    );
    assert!(result.status.success());
    let bytes = std::fs::read(&output).expect("read xml");
    let text = String::from_utf8(bytes[3..].to_vec()).expect("utf8");
    tags_section(&text).to_string()
}

#[test]
fn export_tags_section_matches_golden() {
    let dir = export_fixture_dir();
    let golden_path = dir.join("expected_tags.xml");
    if !golden_path.is_file() {
        eprintln!(
            "skipping: {} missing (run `cargo test --test toolbox regenerate_export_golden -- --ignored`)",
            golden_path.display()
        );
        return;
    }
    let expected = std::fs::read_to_string(&golden_path).expect("golden export tags");
    let actual = export_synthetic_tags_section();
    assert_eq!(
        actual, expected,
        "export tag serialization differs from golden fixture"
    );
}

#[test]
#[ignore = "regenerates tests/fixtures/export/expected_tags.xml"]
fn regenerate_export_golden() {
    let dir = export_fixture_dir();
    std::fs::create_dir_all(&dir).expect("create export fixture dir");
    let actual = export_synthetic_tags_section();
    std::fs::write(dir.join("expected_tags.xml"), actual.as_bytes()).expect("write golden");
}

#[test]
fn export_fixture_path_matches_manifest() {
    // The fixture directory is part of the crate; make sure golden files stay in sync with
    // the command output format.
    let dir = fixture_dir();
    if !dir.is_dir() {
        return;
    }
    for name in [
        "a.xml",
        "b.xml",
        "expected_merge.xml",
        "expected_merge_offsets.xml",
        "expected_start_times.txt",
    ] {
        assert!(dir.join(name).is_file(), "missing toolbox fixture {name}");
    }
    let export_dir = export_fixture_dir();
    assert!(
        export_dir.join("expected_tags.xml").is_file(),
        "missing export golden fixture"
    );
}
