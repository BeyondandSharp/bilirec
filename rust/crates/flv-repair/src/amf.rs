// SPDX-License-Identifier: GPL-3.0-or-later
//
// Minimal AMF0 codec required for FLV script tags (`onMetaData`).
// Ported from `refs/BililiveRecorder/BililiveRecorder.Flv/Amf/**` (GPL-3.0).

use std::fmt;

/// AMF0 value. String payloads keep their raw UTF-8 bytes so that a parse/write round
/// trip is byte exact (needed for the "clean input must not grow" guarantee).
#[derive(Clone, Debug)]
pub enum AmfValue {
    Number(f64),
    Boolean(bool),
    String(Vec<u8>),
    Object(Vec<(String, AmfValue)>),
    Null,
    Undefined,
    Reference(u16),
    EcmaArray(Vec<(String, AmfValue)>),
    StrictArray(Vec<AmfValue>),
    Date { millis: f64, timezone: i16 },
    LongString(Vec<u8>),
}

impl AmfValue {
    pub fn as_number(&self) -> Option<f64> {
        match self {
            AmfValue::Number(value) => Some(*value),
            _ => None,
        }
    }

    pub fn as_bytes(&self) -> Option<&[u8]> {
        match self {
            AmfValue::String(bytes) | AmfValue::LongString(bytes) => Some(bytes),
            _ => None,
        }
    }

    pub fn as_object(&self) -> Option<&[(String, AmfValue)]> {
        match self {
            AmfValue::Object(entries) | AmfValue::EcmaArray(entries) => Some(entries),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct AmfError;

impl fmt::Display for AmfError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("invalid AMF0 data")
    }
}

impl std::error::Error for AmfError {}

/// Recursion guard for hostile input.
const MAX_DEPTH: usize = 32;
/// Absolute upper bound for aggregate element counts parsed from one tag.
const MAX_ELEMENTS: usize = 1_000_000;

struct Cursor<'a> {
    data: &'a [u8],
    pos: usize,
}

impl<'a> Cursor<'a> {
    fn new(data: &'a [u8]) -> Self {
        Cursor { data, pos: 0 }
    }

    fn remaining(&self) -> usize {
        self.data.len().saturating_sub(self.pos)
    }

    fn take(&mut self, len: usize) -> Result<&'a [u8], AmfError> {
        let end = self.pos.checked_add(len).ok_or(AmfError)?;
        let slice = self.data.get(self.pos..end).ok_or(AmfError)?;
        self.pos = end;
        Ok(slice)
    }

    fn u8(&mut self) -> Result<u8, AmfError> {
        let byte = *self.data.get(self.pos).ok_or(AmfError)?;
        self.pos += 1;
        Ok(byte)
    }

    fn u16(&mut self) -> Result<u16, AmfError> {
        let bytes = self.take(2)?;
        Ok(u16::from_be_bytes([bytes[0], bytes[1]]))
    }

    fn i16(&mut self) -> Result<i16, AmfError> {
        Ok(self.u16()? as i16)
    }

    fn u32(&mut self) -> Result<u32, AmfError> {
        let bytes = self.take(4)?;
        Ok(u32::from_be_bytes([bytes[0], bytes[1], bytes[2], bytes[3]]))
    }

    fn f64(&mut self) -> Result<f64, AmfError> {
        let bytes = self.take(8)?;
        Ok(f64::from_bits(u64::from_be_bytes([
            bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5], bytes[6], bytes[7],
        ])))
    }
}

fn decode_string(bytes: &[u8]) -> String {
    // The C# reference strips NUL characters from property names and values.
    String::from_utf8_lossy(bytes).replace('\0', "")
}

/// Parse a complete FLV script tag body into a list of AMF0 values.
pub fn parse_script_body(data: &[u8]) -> Result<Vec<AmfValue>, AmfError> {
    let mut cursor = Cursor::new(data);
    let mut values = Vec::new();
    while cursor.remaining() > 0 {
        if values.len() > MAX_ELEMENTS {
            return Err(AmfError);
        }
        values.push(parse_value(&mut cursor, 0)?);
    }
    Ok(values)
}

fn parse_properties(
    cursor: &mut Cursor<'_>,
    depth: usize,
) -> Result<Vec<(String, AmfValue)>, AmfError> {
    let mut entries = Vec::new();
    loop {
        if entries.len() > MAX_ELEMENTS {
            return Err(AmfError);
        }
        let length = cursor.u16()? as usize;
        if length == 0 {
            // Object end marker: empty key followed by 0x09.
            let marker = cursor.u8()?;
            if marker != 9 {
                return Err(AmfError);
            }
            return Ok(entries);
        }
        let key = decode_string(cursor.take(length)?);
        let value = parse_value(cursor, depth + 1)?;
        entries.push((key, value));
    }
}

fn parse_value(cursor: &mut Cursor<'_>, depth: usize) -> Result<AmfValue, AmfError> {
    if depth > MAX_DEPTH {
        return Err(AmfError);
    }
    let kind = cursor.u8()?;
    match kind {
        0 => Ok(AmfValue::Number(cursor.f64()?)),
        1 => Ok(AmfValue::Boolean(cursor.u8()? != 0)),
        2 => {
            let length = cursor.u16()? as usize;
            Ok(AmfValue::String(cursor.take(length)?.to_vec()))
        }
        3 => Ok(AmfValue::Object(parse_properties(cursor, depth)?)),
        4 => Err(AmfError), // MovieClip is not supported
        5 => Ok(AmfValue::Null),
        6 => Ok(AmfValue::Undefined),
        7 => Ok(AmfValue::Reference(cursor.u16()?)),
        8 => {
            let declared = cursor.u32()? as usize;
            let entries = parse_properties(cursor, depth)?;
            // `declared` is advisory; the C# reference ignores it as well.
            let _ = declared;
            Ok(AmfValue::EcmaArray(entries))
        }
        9 => Err(AmfError), // stray object end marker
        10 => {
            let length = cursor.u32()? as usize;
            if length > cursor.remaining() || length > MAX_ELEMENTS {
                return Err(AmfError);
            }
            let mut values = Vec::with_capacity(length);
            for _ in 0..length {
                values.push(parse_value(cursor, depth + 1)?);
            }
            Ok(AmfValue::StrictArray(values))
        }
        11 => {
            let millis = cursor.f64()?;
            let timezone = cursor.i16()?;
            Ok(AmfValue::Date { millis, timezone })
        }
        12 => {
            let length = cursor.u32()? as usize;
            if length > cursor.remaining() {
                return Err(AmfError);
            }
            Ok(AmfValue::LongString(cursor.take(length)?.to_vec()))
        }
        _ => Err(AmfError),
    }
}

/// Serialize AMF0 values. Returns an error for values that can not be represented
/// (e.g. over-long short strings).
pub fn write_script_body(values: &[AmfValue], out: &mut Vec<u8>) -> Result<(), AmfError> {
    for value in values {
        write_value(value, out)?;
    }
    Ok(())
}

fn write_key(key: &str, out: &mut Vec<u8>) -> Result<(), AmfError> {
    let bytes = key.as_bytes();
    if bytes.len() > u16::MAX as usize {
        return Err(AmfError);
    }
    out.extend_from_slice(&(bytes.len() as u16).to_be_bytes());
    out.extend_from_slice(bytes);
    Ok(())
}

fn write_properties(entries: &[(String, AmfValue)], out: &mut Vec<u8>) -> Result<(), AmfError> {
    for (key, value) in entries {
        write_key(key, out)?;
        write_value(value, out)?;
    }
    out.extend_from_slice(&[0, 0, 9]);
    Ok(())
}

fn write_value(value: &AmfValue, out: &mut Vec<u8>) -> Result<(), AmfError> {
    match value {
        AmfValue::Number(number) => {
            out.push(0);
            out.extend_from_slice(&number.to_bits().to_be_bytes());
        }
        AmfValue::Boolean(boolean) => {
            out.push(1);
            out.push(u8::from(*boolean));
        }
        AmfValue::String(bytes) => {
            if bytes.len() > u16::MAX as usize {
                return Err(AmfError);
            }
            out.push(2);
            out.extend_from_slice(&(bytes.len() as u16).to_be_bytes());
            out.extend_from_slice(bytes);
        }
        AmfValue::Object(entries) => {
            out.push(3);
            write_properties(entries, out)?;
        }
        AmfValue::Null => out.push(5),
        AmfValue::Undefined => out.push(6),
        AmfValue::Reference(reference) => {
            out.push(7);
            out.extend_from_slice(&reference.to_be_bytes());
        }
        AmfValue::EcmaArray(entries) => {
            out.push(8);
            out.extend_from_slice(&(entries.len() as u32).to_be_bytes());
            write_properties(entries, out)?;
        }
        AmfValue::StrictArray(values) => {
            out.push(10);
            out.extend_from_slice(&(values.len() as u32).to_be_bytes());
            for value in values {
                write_value(value, out)?;
            }
        }
        AmfValue::Date { millis, timezone } => {
            out.push(11);
            out.extend_from_slice(&millis.to_bits().to_be_bytes());
            out.extend_from_slice(&timezone.to_be_bytes());
        }
        AmfValue::LongString(bytes) => {
            out.push(12);
            out.extend_from_slice(&(bytes.len() as u32).to_be_bytes());
            out.extend_from_slice(bytes);
        }
    }
    Ok(())
}

/// Parsed `onMetaData` payload.
#[derive(Clone, Debug)]
pub struct Metadata {
    pub entries: Vec<(String, AmfValue)>,
    pub duration: Option<f64>,
    /// True when the script tag had a third value (seen from some CDNs).
    pub had_extra_value: bool,
}

/// Check whether two AMF values are the JSON name `name`.
fn value_is_name(value: &AmfValue, name: &[u8]) -> bool {
    matches!(value, AmfValue::String(bytes) if bytes.as_slice() == name)
}

/// Extract the metadata object from a script tag body. Accepts both the normal
/// `["onMetaData", ecma]` form and the 3-value `["onMetaData", ecma, null]` variant
/// seen from some CDNs (ported from HandleNewScriptRule).
pub fn parse_metadata(values: &[AmfValue]) -> Option<Metadata> {
    if values.len() < 2 || !value_is_name(&values[0], b"onMetaData") {
        return None;
    }
    let entries = match &values[1] {
        AmfValue::Object(entries) | AmfValue::EcmaArray(entries) => entries.clone(),
        _ => Vec::new(),
    };
    let duration = entries
        .iter()
        .find(|(key, _)| key == "duration")
        .and_then(|(_, value)| value.as_number());
    Some(Metadata {
        entries,
        duration,
        had_extra_value: values.len() > 2,
    })
}

/// Return metadata duration from a script tag body, if it is valid and positive.
pub fn metadata_duration(values: &[AmfValue]) -> Option<f64> {
    parse_metadata(values).and_then(|metadata| metadata.duration)
}

/// Build a normalized `["onMetaData", ecma]` script body with `duration` set to the
/// provided value (the key is added when missing). Entry order is preserved.
pub fn normalize_metadata(metadata: &Metadata, duration: f64) -> Vec<AmfValue> {
    let mut entries = metadata.entries.clone();
    match entries.iter_mut().find(|(key, _)| key == "duration") {
        Some((_, value)) => *value = AmfValue::Number(duration),
        None => entries.push(("duration".to_string(), AmfValue::Number(duration))),
    }
    vec![
        AmfValue::String(b"onMetaData".to_vec()),
        AmfValue::EcmaArray(entries),
    ]
}

/// Locate the byte offset (inside a serialized script tag body) of the 8-byte AMF0
/// number payload of the top-level `duration` property. Used to patch the duration in
/// place once the file processing is complete.
pub fn find_duration_offset(body: &[u8]) -> Option<usize> {
    let mut cursor = Cursor::new(body);
    // Skip the "onMetaData" name value.
    parse_value(&mut cursor, 0).ok()?;
    let kind = *cursor.data.get(cursor.pos)?;
    if kind != 3 && kind != 8 {
        return None;
    }
    cursor.pos += 1;
    if kind == 8 {
        cursor.u32().ok()?;
    }
    loop {
        let key_length = cursor.u16().ok()? as usize;
        if key_length == 0 {
            return None;
        }
        let key = cursor.take(key_length).ok()?;
        let value_start = cursor.pos;
        let value = parse_value(&mut cursor, 0).ok()?;
        if key == b"duration" && matches!(value, AmfValue::Number(_)) {
            // value_start points at the AMF type byte; the double follows it.
            return Some(value_start + 1);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roundtrip_metadata_and_find_duration() {
        let values = vec![
            AmfValue::String(b"onMetaData".to_vec()),
            AmfValue::EcmaArray(vec![
                ("width".to_string(), AmfValue::Number(1920.0)),
                ("duration".to_string(), AmfValue::Number(12.5)),
            ]),
        ];
        let mut bytes = Vec::new();
        write_script_body(&values, &mut bytes).unwrap();
        let parsed = parse_script_body(&bytes).unwrap();
        assert_eq!(parsed.len(), 2);
        assert_eq!(metadata_duration(&parsed), Some(12.5));

        let offset = find_duration_offset(&bytes).unwrap();
        assert_eq!(&bytes[offset..offset + 8], &12.5f64.to_bits().to_be_bytes());

        let metadata = parse_metadata(&parsed).unwrap();
        let normalized = normalize_metadata(&metadata, 7.0);
        let mut bytes2 = Vec::new();
        write_script_body(&normalized, &mut bytes2).unwrap();
        assert_eq!(
            parse_metadata(&parse_script_body(&bytes2).unwrap())
                .unwrap()
                .duration,
            Some(7.0)
        );
    }

    #[test]
    fn rejects_truncated_and_hostile_input() {
        assert!(parse_script_body(&[0]).is_err());
        assert!(parse_script_body(&[8, 0, 0]).is_err());
        // Deeply nested object should hit the depth guard instead of overflowing.
        let mut bytes = Vec::new();
        for _ in 0..100 {
            bytes.push(3);
            bytes.extend_from_slice(&1u16.to_be_bytes());
            bytes.push(b'a');
        }
        assert!(parse_script_body(&bytes).is_err());
    }

    #[test]
    fn missing_duration_is_added() {
        let values = vec![
            AmfValue::String(b"onMetaData".to_vec()),
            AmfValue::EcmaArray(vec![("width".to_string(), AmfValue::Number(1.0))]),
        ];
        let metadata = parse_metadata(&values).unwrap();
        assert_eq!(metadata.duration, None);
        let normalized = normalize_metadata(&metadata, 0.0);
        let AmfValue::EcmaArray(entries) = &normalized[1] else {
            panic!("expected ecma array");
        };
        assert_eq!(entries.len(), 2);
    }
}
