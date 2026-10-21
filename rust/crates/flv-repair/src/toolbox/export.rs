// SPDX-License-Identifier: GPL-3.0-only
//
// `flvrepair export`: dump FLV tags into the BililiveRecorder `XmlFlvFile` XML schema
// (or a `.zip` containing that XML). Schema derived from
// `refs/BililiveRecorder/BililiveRecorder.Flv/Xml/XmlFlvFile.cs`, `Tag.cs` and
// `BililiveRecorder.ToolBox/Tool/Export/ExportHandler.cs` (GPL-3.0-only).
//
// Documented substitutions:
// - `DataHash`/`NaluHash` use FNV-1a 64 (uppercase hex) instead of FarmHash64, because the
//   crate must stay dependency-free; the field is informational for debugging tools.
// - ExportTime/File*Time are UTC; .NET writes the local offset.
// - Meta.Version is `flvrepair <version>`.

use crate::amf::{self, AmfValue};
use crate::codec;
use crate::parser::{ParseError, TagReader};
use crate::tag::{Tag, TagType};
use crate::toolbox::time;
use crate::toolbox::xml;
use crate::toolbox::zip::{self, ZipEntry};
use crate::toolbox::{io_context, json_string, ToolboxError};
use std::fs;
use std::io::BufReader;
use std::path::Path;

struct Meta {
    version: String,
    export_time: i64,
    file_size: u64,
    creation_time: i64,
    modification_time: i64,
}

/// Run `export --input <flv> --output <xml|zip> [--json]`.
pub fn run(input: &Path, output: &Path, json: bool) -> Result<(), ToolboxError> {
    let metadata = fs::metadata(input).map_err(|error| {
        io_context(
            &format!("cannot read metadata for {}", input.display()),
            error,
        )
    })?;
    let file = fs::File::open(input)
        .map_err(|error| io_context(&format!("cannot open {}", input.display()), error))?;

    let mut reader = TagReader::new(
        BufReader::with_capacity(64 * 1024, file),
        64 * 1024 * 1024,
        false,
    );
    let mut tags: Vec<Tag> = Vec::new();
    loop {
        match reader.read_tag() {
            Ok(Some(tag)) => tags.push(tag),
            Ok(None) => break,
            Err(ParseError::Io(error)) => {
                return Err(io_context(
                    &format!("cannot read {}", input.display()),
                    error,
                ))
            }
            Err(ParseError::Unrecoverable(message)) => {
                return Err(ToolboxError::InvalidInput(format!(
                    "{}: {message}",
                    input.display()
                )))
            }
        }
    }
    if !reader.header().present {
        return Err(ToolboxError::InvalidInput(format!(
            "{}: not an FLV file",
            input.display()
        )));
    }

    let now = time::now_unix_ms();
    let modification_time = metadata
        .modified()
        .map(time::system_time_to_unix_ms)
        .unwrap_or(now);
    let creation_time = metadata
        .created()
        .map(time::system_time_to_unix_ms)
        .unwrap_or(modification_time);
    let meta = Meta {
        version: format!("flvrepair {}", env!("CARGO_PKG_VERSION")),
        export_time: now,
        file_size: metadata.len(),
        creation_time,
        modification_time,
    };

    let document = build_xml(&meta, &tags);
    let zip_output = output
        .extension()
        .map(|extension| extension.to_string_lossy().eq_ignore_ascii_case("zip"))
        .unwrap_or(false);

    let mut xml_bytes = Vec::with_capacity(document.len() + 3);
    xml_bytes.extend_from_slice(&[0xef, 0xbb, 0xbf]); // .NET StreamWriter UTF-8 BOM
    xml_bytes.extend_from_slice(document.as_bytes());

    let (format, output_bytes) = if zip_output {
        let entry_name = zip_entry_name(input);
        let archive = zip::build_zip(&[ZipEntry {
            name: entry_name,
            data: xml_bytes,
        }]);
        ("zip", archive)
    } else {
        ("xml", xml_bytes)
    };

    fs::write(output, &output_bytes)
        .map_err(|error| io_context(&format!("cannot write {}", output.display()), error))?;

    if json {
        println!(
            "{{\"version\":1,\"format\":\"{}\",\"input\":{},\"output\":{},\"tags\":{},\"output_bytes\":{}}}",
            format,
            json_string(&input.display().to_string()),
            json_string(&output.display().to_string()),
            tags.len(),
            output_bytes.len(),
        );
    } else {
        println!(
            "exported {} tags to {} ({} bytes)",
            tags.len(),
            output.display(),
            output_bytes.len()
        );
    }
    Ok(())
}

fn zip_entry_name(input: &Path) -> String {
    let file_name = input
        .file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| "input.flv".to_string());
    format!("{file_name}.brec.xml")
}

fn build_xml(meta: &Meta, tags: &[Tag]) -> String {
    let mut out = String::new();
    out.push_str("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n");
    xml::push_start_tag(&mut out, "BililiveRecorderFlv", &[], false, 0);
    xml::push_start_tag(&mut out, "Meta", &[], false, 1);
    xml::push_text_element(&mut out, "Version", &[], &meta.version, 2);
    xml::push_text_element(
        &mut out,
        "ExportTime",
        &[],
        &time::format_o(meta.export_time),
        2,
    );
    xml::push_text_element(&mut out, "FileSize", &[], &meta.file_size.to_string(), 2);
    xml::push_text_element(
        &mut out,
        "FileCreationTime",
        &[],
        &time::format_o(meta.creation_time),
        2,
    );
    xml::push_text_element(
        &mut out,
        "FileModificationTime",
        &[],
        &time::format_o(meta.modification_time),
        2,
    );
    xml::push_end_tag(&mut out, "Meta", 1);
    xml::push_start_tag(&mut out, "Tags", &[], false, 1);
    for tag in tags {
        write_tag(&mut out, tag);
    }
    xml::push_end_tag(&mut out, "Tags", 1);
    xml::push_end_tag(&mut out, "BililiveRecorderFlv", 0);
    out
}

fn write_tag(out: &mut String, tag: &Tag) {
    let mut attributes: Vec<(String, String)> =
        vec![("Type".to_string(), tag_type_name(tag.tag_type).to_string())];
    let flag = flag_string(tag);
    if !flag.is_empty() {
        attributes.push(("Flag".to_string(), flag));
    }
    attributes.push(("Size".to_string(), tag.data.len().to_string()));
    attributes.push(("Timestamp".to_string(), tag.timestamp.to_string()));
    attributes.push(("DataHash".to_string(), format!("{:016X}", tag.data_hash())));

    let mut children = String::new();

    if tag.tag_type == TagType::Script {
        match amf::parse_script_body(&tag.data) {
            Ok(values) => {
                xml::push_text_element(&mut children, "ScriptData", &[], &amf_json(&values), 3);
            }
            Err(_) => {
                // Robustness extension: keep the raw body when AMF parsing fails (the
                // reference implementation would export a tag without ScriptData).
                xml::push_text_element(&mut children, "BinaryData", &[], &hex(&tag.data), 3);
            }
        }
    }

    if tag.data.len() >= 5 {
        let mut extra_attributes = vec![(
            "FirstBytes".to_string(),
            format!("{:02X}{:02X}", tag.data[0], tag.data[1]),
        )];
        if tag.tag_type == TagType::Video {
            let composition = composition_time(&tag.data);
            extra_attributes.push(("CompositionTime".to_string(), composition.to_string()));
            extra_attributes.push((
                "FinalTime".to_string(),
                tag.timestamp
                    .saturating_add(i64::from(composition))
                    .to_string(),
            ));
        }
        xml::push_start_tag(&mut children, "ExtraData", &extra_attributes, true, 3);
    }

    if tag.tag_type == TagType::Video && !tag.flags.header {
        if let Some(nalus) = codec::avc_nalus(&tag.data) {
            for nalu in nalus {
                let end = nalu
                    .start_position
                    .saturating_add(nalu.full_size)
                    .min(tag.data.len());
                let hash =
                    crate::tag::fnv1a64(tag.data.get(nalu.start_position..end).unwrap_or_default());
                let attributes = vec![
                    ("StartPosition".to_string(), nalu.start_position.to_string()),
                    ("FullSize".to_string(), nalu.full_size.to_string()),
                    (
                        "Type".to_string(),
                        nalu_type_name(nalu.nalu_type).to_string(),
                    ),
                    ("NaluHash".to_string(), format!("{hash:016X}")),
                ];
                xml::push_start_tag(&mut children, "Nalus", &attributes, true, 3);
            }
        }
    }

    if tag.is_header() {
        xml::push_text_element(&mut children, "BinaryData", &[], &hex(&tag.data), 3);
    }

    xml::push_start_tag(out, "Tag", &attributes, children.is_empty(), 2);
    if !children.is_empty() {
        out.push_str(&children);
        xml::push_end_tag(out, "Tag", 2);
    }
}

fn hex(data: &[u8]) -> String {
    let mut out = String::with_capacity(data.len() * 2);
    for byte in data {
        out.push_str(&format!("{byte:02X}"));
    }
    out
}

fn tag_type_name(tag_type: TagType) -> &'static str {
    match tag_type {
        TagType::Audio => "Audio",
        TagType::Video => "Video",
        TagType::Script => "Script",
    }
}

fn flag_string(tag: &Tag) -> String {
    let mut parts = Vec::new();
    if tag.flags.header {
        parts.push("Header");
    }
    if tag.flags.keyframe {
        parts.push("Keyframe");
    }
    if tag.flags.end {
        parts.push("End");
    }
    parts.join(", ")
}

/// Signed 24-bit composition time offset at body[2..5] (reference `Tag.UpdateExtraData`).
fn composition_time(body: &[u8]) -> i32 {
    let raw = (i32::from(body[2]) << 16) | (i32::from(body[3]) << 8) | i32::from(body[4]);
    if raw & 0x0080_0000 != 0 {
        raw | !0x00ff_ffff
    } else {
        raw
    }
}

fn nalu_type_name(nalu_type: u8) -> &'static str {
    match nalu_type {
        0 => "Unspecified0",
        1 => "CodedSliceOfANonIdrPicture",
        2 => "CodedSliceDataPartitionA",
        3 => "CodedSliceDataPartitionB",
        4 => "CodedSliceDataPartitionC",
        5 => "CodedSliceOfAnIdrPicture",
        6 => "Sei",
        7 => "Sps",
        8 => "Pps",
        9 => "AccessUnitDelimiter",
        10 => "EndOfSequence",
        11 => "EndOfStream",
        12 => "FillerData",
        13 => "SpsExtension",
        14 => "PrefixNalUnit",
        15 => "SubsetSps",
        16 => "DepthParameterSet",
        17 => "Reserved17",
        18 => "Reserved18",
        19 => "SliceLayerWithoutPartitioning",
        20 => "SliceLayerExtension20",
        21 => "SliceLayerExtension21",
        22 => "Reserved22",
        23 => "Reserved23",
        24 => "Unspecified24",
        25 => "Unspecified25",
        26 => "Unspecified26",
        27 => "Unspecified27",
        28 => "Unspecified28",
        29 => "Unspecified29",
        30 => "Unspecified30",
        _ => "Unspecified31",
    }
}

/// Serialize AMF0 script values in the JSON shape produced by the reference
/// `ScriptTagBody.ToJson()` (Newtonsoft.Json + JsonSubtypes).
fn amf_json(values: &[AmfValue]) -> String {
    let mut out = String::new();
    out.push('[');
    for (index, value) in values.iter().enumerate() {
        if index > 0 {
            out.push(',');
        }
        push_amf_value(&mut out, value);
    }
    out.push(']');
    out
}

fn push_amf_value(out: &mut String, value: &AmfValue) {
    match value {
        AmfValue::Number(number) => {
            out.push_str("{\"Type\":\"Number\",\"Value\":");
            push_json_number(out, *number);
            out.push('}');
        }
        AmfValue::Boolean(boolean) => {
            out.push_str("{\"Type\":\"Boolean\",\"Value\":");
            out.push_str(if *boolean { "true" } else { "false" });
            out.push('}');
        }
        AmfValue::String(bytes) => {
            out.push_str("{\"Type\":\"String\",\"Value\":");
            push_json_bytes(out, bytes);
            out.push('}');
        }
        AmfValue::Object(entries) | AmfValue::EcmaArray(entries) => {
            let type_name = if matches!(value, AmfValue::EcmaArray(_)) {
                "EcmaArray"
            } else {
                "Object"
            };
            out.push_str("{\"Type\":\"");
            out.push_str(type_name);
            out.push_str("\",\"Value\":{");
            for (index, (key, entry)) in entries.iter().enumerate() {
                if index > 0 {
                    out.push(',');
                }
                push_json_bytes(out, key.as_bytes());
                out.push(':');
                push_amf_value(out, entry);
            }
            out.push_str("}}");
        }
        AmfValue::Null => out.push_str("{\"Type\":\"Null\"}"),
        AmfValue::Undefined => out.push_str("{\"Type\":\"Undefined\"}"),
        AmfValue::Reference(reference) => {
            out.push_str("{\"Type\":\"Reference\",\"Value\":");
            out.push_str(&reference.to_string());
            out.push('}');
        }
        AmfValue::StrictArray(values) => {
            out.push_str("{\"Type\":\"StrictArray\",\"Value\":[");
            for (index, entry) in values.iter().enumerate() {
                if index > 0 {
                    out.push(',');
                }
                push_amf_value(out, entry);
            }
            out.push_str("]}");
        }
        AmfValue::Date { millis, timezone } => {
            out.push_str("{\"Type\":\"Date\",\"Value\":");
            let unix_millis = if millis.is_finite() {
                *millis as i64
            } else {
                0
            };
            push_json_string(out, &time::format_datetime_offset(unix_millis, *timezone));
            out.push('}');
        }
        AmfValue::LongString(bytes) => {
            out.push_str("{\"Type\":\"LongString\",\"Value\":");
            push_json_bytes(out, bytes);
            out.push('}');
        }
    }
}

fn push_json_number(out: &mut String, number: f64) {
    if number.is_nan() {
        out.push_str("\"NaN\"");
    } else if number == f64::INFINITY {
        out.push_str("\"Infinity\"");
    } else if number == f64::NEG_INFINITY {
        out.push_str("\"-Infinity\"");
    } else if number.fract() == 0.0 && number.abs() < 1e15 {
        // Newtonsoft.Json writes an integral double with a trailing ".0".
        out.push_str(&format!("{number:.1}"));
    } else {
        out.push_str(&number.to_string());
    }
}

fn push_json_bytes(out: &mut String, bytes: &[u8]) {
    push_json_string(out, &String::from_utf8_lossy(bytes));
}

fn push_json_string(out: &mut String, value: &str) {
    out.push('"');
    out.push_str(&crate::log::escape(value));
    out.push('"');
}
