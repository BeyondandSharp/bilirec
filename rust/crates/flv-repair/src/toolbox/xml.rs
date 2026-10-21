// SPDX-License-Identifier: GPL-3.0-only
//
// Dependency-free XML helpers for the toolbox commands: a small pull parser (enough for
// Bilibili danmaku XML) plus escaping/writing helpers used by the export and merge
// outputs.

use std::fmt;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum XmlEvent {
    Start {
        name: String,
        attributes: Vec<(String, String)>,
        self_closing: bool,
    },
    End {
        name: String,
    },
    Text(String),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct XmlError {
    pub message: String,
}

impl fmt::Display for XmlError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for XmlError {}

fn error(message: impl Into<String>) -> XmlError {
    XmlError {
        message: message.into(),
    }
}

/// Minimal streaming XML parser. Comments, processing instructions, doctypes and CDATA
/// sections are handled; entity references in text/attribute values are decoded.
pub struct XmlReader<'a> {
    bytes: &'a [u8],
    position: usize,
}

impl<'a> XmlReader<'a> {
    pub fn new(bytes: &'a [u8]) -> Self {
        let position = if bytes.starts_with(&[0xef, 0xbb, 0xbf]) {
            3
        } else {
            0
        };
        XmlReader { bytes, position }
    }

    fn find(&self, from: usize, needle: &[u8]) -> Option<usize> {
        if from > self.bytes.len() {
            return None;
        }
        self.bytes[from..]
            .windows(needle.len())
            .position(|window| window == needle)
            .map(|offset| from + offset)
    }

    fn skip_whitespace(&mut self) {
        while matches!(
            self.bytes.get(self.position),
            Some(b' ' | b'\t' | b'\r' | b'\n')
        ) {
            self.position += 1;
        }
    }

    fn take_name(&mut self) -> Result<String, XmlError> {
        let start = self.position;
        while let Some(byte) = self.bytes.get(self.position) {
            match byte {
                b' ' | b'\t' | b'\r' | b'\n' | b'/' | b'>' | b'=' => break,
                _ => self.position += 1,
            }
        }
        if self.position == start {
            return Err(error("empty XML element or attribute name"));
        }
        let raw = &self.bytes[start..self.position];
        std::str::from_utf8(raw)
            .map(|name| name.to_string())
            .map_err(|_| error("invalid UTF-8 in XML element name"))
    }

    /// Read the next XML event. `Ok(None)` at end of document.
    pub fn next_event(&mut self) -> Result<Option<XmlEvent>, XmlError> {
        loop {
            if self.position >= self.bytes.len() {
                return Ok(None);
            }
            if self.bytes[self.position] != b'<' {
                let start = self.position;
                while self.position < self.bytes.len() && self.bytes[self.position] != b'<' {
                    self.position += 1;
                }
                let raw = std::str::from_utf8(&self.bytes[start..self.position])
                    .map_err(|_| error("invalid UTF-8 in XML text"))?;
                return Ok(Some(XmlEvent::Text(decode_entities(raw))));
            }

            // Comments.
            if self.bytes[self.position..].starts_with(b"<!--") {
                let start = self.position + 4;
                let end = self
                    .find(start, b"-->")
                    .ok_or_else(|| error("unterminated XML comment"))?;
                self.position = end + 3;
                continue;
            }
            // Processing instructions / XML declaration.
            if self.bytes[self.position..].starts_with(b"<?") {
                let start = self.position + 2;
                let end = self
                    .find(start, b"?>")
                    .ok_or_else(|| error("unterminated XML processing instruction"))?;
                self.position = end + 2;
                continue;
            }
            // CDATA.
            if self.bytes[self.position..].starts_with(b"<![CDATA[") {
                let start = self.position + 9;
                let end = self
                    .find(start, b"]]>")
                    .ok_or_else(|| error("unterminated CDATA section"))?;
                let raw = std::str::from_utf8(&self.bytes[start..end])
                    .map_err(|_| error("invalid UTF-8 in CDATA"))?;
                self.position = end + 3;
                return Ok(Some(XmlEvent::Text(raw.to_string())));
            }
            // Doctype / other declarations: skip to the closing '>'.
            if self.bytes[self.position..].starts_with(b"<!") {
                let end = self
                    .find(self.position + 2, b">")
                    .ok_or_else(|| error("unterminated XML declaration"))?;
                self.position = end + 1;
                continue;
            }
            // End tag.
            if self.bytes[self.position..].starts_with(b"</") {
                self.position += 2;
                let name = self.take_name()?;
                self.skip_whitespace();
                if self.bytes.get(self.position) != Some(&b'>') {
                    return Err(error(format!("unterminated closing tag </{name}>")));
                }
                self.position += 1;
                return Ok(Some(XmlEvent::End { name }));
            }

            // Start tag.
            self.position += 1;
            let name = self.take_name()?;
            let mut attributes = Vec::new();
            loop {
                self.skip_whitespace();
                match self.bytes.get(self.position) {
                    None => return Err(error(format!("unterminated start tag <{name}>"))),
                    Some(b'>') => {
                        self.position += 1;
                        return Ok(Some(XmlEvent::Start {
                            name,
                            attributes,
                            self_closing: false,
                        }));
                    }
                    Some(b'/') => {
                        if self.bytes.get(self.position + 1) == Some(&b'>') {
                            self.position += 2;
                            return Ok(Some(XmlEvent::Start {
                                name,
                                attributes,
                                self_closing: true,
                            }));
                        }
                        return Err(error(format!("invalid '/' in tag <{name}>")));
                    }
                    Some(_) => {
                        let attribute_name = self.take_name()?;
                        self.skip_whitespace();
                        if self.bytes.get(self.position) != Some(&b'=') {
                            return Err(error(format!(
                                "missing '=' after attribute {attribute_name} in <{name}>"
                            )));
                        }
                        self.position += 1;
                        self.skip_whitespace();
                        let quote = match self.bytes.get(self.position) {
                            Some(quote @ (b'"' | b'\'')) => *quote,
                            _ => {
                                return Err(error(format!(
                                    "unquoted attribute value for {attribute_name}"
                                )))
                            }
                        };
                        self.position += 1;
                        let value_start = self.position;
                        while let Some(byte) = self.bytes.get(self.position) {
                            if *byte == quote {
                                break;
                            }
                            self.position += 1;
                        }
                        if self.bytes.get(self.position) != Some(&quote) {
                            return Err(error(format!(
                                "unterminated attribute value for {attribute_name}"
                            )));
                        }
                        let raw = std::str::from_utf8(&self.bytes[value_start..self.position])
                            .map_err(|_| error("invalid UTF-8 in attribute value"))?;
                        self.position += 1;
                        attributes.push((attribute_name, decode_entities(raw)));
                    }
                }
            }
        }
    }
}

/// Consume events until the matching end tag for the element that was just started.
pub fn skip_element(reader: &mut XmlReader<'_>, name: &str) -> Result<(), XmlError> {
    let mut depth = 1usize;
    while let Some(event) = reader.next_event()? {
        match event {
            XmlEvent::Start {
                self_closing: false,
                ..
            } => depth += 1,
            XmlEvent::Start { .. } => {}
            XmlEvent::End { .. } => {
                depth -= 1;
                if depth == 0 {
                    return Ok(());
                }
            }
            XmlEvent::Text(_) => {}
        }
    }
    Err(error(format!("unexpected end of XML inside <{name}>")))
}

/// Read the text content of an element that was just started (nested elements are
/// skipped; this is enough for Bilibili `<d>`/`<sc>` message bodies).
pub fn read_text_element(reader: &mut XmlReader<'_>, name: &str) -> Result<String, XmlError> {
    let mut text = String::new();
    loop {
        match reader.next_event()? {
            None => return Err(error(format!("unexpected end of XML inside <{name}>"))),
            Some(XmlEvent::Text(value)) => text.push_str(&value),
            Some(XmlEvent::Start {
                name: nested,
                self_closing,
                ..
            }) => {
                if !self_closing {
                    skip_element(reader, &nested)?;
                }
            }
            Some(XmlEvent::End { .. }) => return Ok(text),
        }
    }
}

pub fn find_attribute<'a>(attributes: &'a [(String, String)], name: &str) -> Option<&'a str> {
    attributes
        .iter()
        .find(|(key, _)| key == name)
        .map(|(_, value)| value.as_str())
}

/// Replace the value of an attribute in place, or append it at the end.
pub fn set_attribute(attributes: &mut Vec<(String, String)>, name: &str, value: String) {
    if let Some((_, existing)) = attributes.iter_mut().find(|(key, _)| key == name) {
        *existing = value;
    } else {
        attributes.push((name.to_string(), value));
    }
}

fn decode_entities(raw: &str) -> String {
    if !raw.contains('&') {
        return raw.to_string();
    }
    let mut out = String::with_capacity(raw.len());
    let mut rest = raw;
    while let Some(position) = rest.find('&') {
        out.push_str(&rest[..position]);
        rest = &rest[position..];
        let Some(semicolon) = rest.find(';') else {
            out.push_str(rest);
            return out;
        };
        let entity = &rest[1..semicolon];
        let decoded = match entity {
            "amp" => Some('&'),
            "lt" => Some('<'),
            "gt" => Some('>'),
            "quot" => Some('"'),
            "apos" => Some('\''),
            _ => {
                if let Some(hex) = entity
                    .strip_prefix("#x")
                    .or_else(|| entity.strip_prefix("#X"))
                {
                    u32::from_str_radix(hex, 16).ok().and_then(char::from_u32)
                } else if let Some(decimal) = entity.strip_prefix('#') {
                    decimal.parse::<u32>().ok().and_then(char::from_u32)
                } else {
                    None
                }
            }
        };
        match decoded {
            Some(character) => out.push(character),
            None => out.push_str(&rest[..=semicolon]),
        }
        rest = &rest[semicolon + 1..];
    }
    out.push_str(rest);
    out
}

fn is_invalid_xml_char(character: char) -> bool {
    matches!(character, '\u{0}'..='\u{8}' | '\u{b}' | '\u{c}' | '\u{e}'..='\u{1f}' | '\u{fffe}' | '\u{ffff}')
}

/// Escape text content for XML 1.0 (invalid control characters become U+FFFD).
pub fn escape_text_into(value: &str, out: &mut String) {
    for character in value.chars() {
        match character {
            '&' => out.push_str("&amp;"),
            '<' => out.push_str("&lt;"),
            '>' => out.push_str("&gt;"),
            '"' => out.push_str("&quot;"),
            '\'' => out.push_str("&apos;"),
            other if is_invalid_xml_char(other) => out.push('\u{fffd}'),
            other => out.push(other),
        }
    }
}

/// Escape an attribute value for XML 1.0.
pub fn escape_attr_into(value: &str, out: &mut String) {
    escape_text_into(value, out);
}

pub fn escape_attr(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    escape_attr_into(value, &mut out);
    out
}

pub fn escape_text(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    escape_text_into(value, &mut out);
    out
}

/// Push `<name attr="value" ...>` (or `... />` when `self_closing`).
pub fn push_start_tag(
    out: &mut String,
    name: &str,
    attributes: &[(String, String)],
    self_closing: bool,
    indent: usize,
) {
    push_indent(out, indent);
    out.push('<');
    out.push_str(name);
    for (key, value) in attributes {
        out.push(' ');
        out.push_str(key);
        out.push_str("=\"");
        escape_attr_into(value, out);
        out.push('"');
    }
    if self_closing {
        out.push_str(" />");
    } else {
        out.push('>');
    }
    out.push('\n');
}

/// Push `<name attr="value">text</name>` on one line.
pub fn push_text_element(
    out: &mut String,
    name: &str,
    attributes: &[(String, String)],
    text: &str,
    indent: usize,
) {
    push_indent(out, indent);
    out.push('<');
    out.push_str(name);
    for (key, value) in attributes {
        out.push(' ');
        out.push_str(key);
        out.push_str("=\"");
        escape_attr_into(value, out);
        out.push('"');
    }
    out.push('>');
    escape_text_into(text, out);
    out.push_str("</");
    out.push_str(name);
    out.push_str(">\n");
}

pub fn push_end_tag(out: &mut String, name: &str, indent: usize) {
    push_indent(out, indent);
    out.push_str("</");
    out.push_str(name);
    out.push_str(">\n");
}

fn push_indent(out: &mut String, indent: usize) {
    for _ in 0..indent {
        out.push_str("  ");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn events(xml: &str) -> Vec<XmlEvent> {
        let mut reader = XmlReader::new(xml.as_bytes());
        let mut result = Vec::new();
        while let Some(event) = reader.next_event().expect("xml event") {
            result.push(event);
        }
        result
    }

    #[test]
    fn parses_elements_attributes_and_entities() {
        let xml = "<?xml version=\"1.0\"?><!-- c --><i><d p=\"1.5,1,25\" user=\"a&amp;b\">hi &lt;b&gt;</d><x/></i>";
        let parsed = events(xml);
        assert!(matches!(&parsed[0], XmlEvent::Start { name, .. } if name == "i"));
        let XmlEvent::Start {
            name, attributes, ..
        } = &parsed[1]
        else {
            panic!("expected start");
        };
        assert_eq!(name, "d");
        assert_eq!(find_attribute(attributes, "user"), Some("a&b"));
        assert!(matches!(&parsed[2], XmlEvent::Text(text) if text == "hi <b>"));
        assert!(
            matches!(&parsed[4], XmlEvent::Start { name, self_closing: true, .. } if name == "x")
        );
    }

    #[test]
    fn roundtrips_escaped_text() {
        let value = "a<b>&\"c'";
        let escaped = escape_text(value);
        assert_eq!(escaped, "a&lt;b&gt;&amp;&quot;c&apos;");
        let parsed = events(&format!("<x>{escaped}</x>"));
        assert!(matches!(&parsed[1], XmlEvent::Text(text) if text == value));
    }

    #[test]
    fn skips_nested_and_style_markup() {
        let xml = "<i><style><z:stylesheet version=\"1.0\"><z:template match=\"/\"><html/></z:template></z:stylesheet></style><d>ok</d></i>";
        let mut reader = XmlReader::new(xml.as_bytes());
        assert!(matches!(
            reader.next_event().unwrap(),
            Some(XmlEvent::Start { .. })
        ));
        let XmlEvent::Start { name, .. } = reader.next_event().unwrap().unwrap() else {
            panic!()
        };
        assert_eq!(name, "style");
        skip_element(&mut reader, &name).expect("skip style");
        let XmlEvent::Start { name, .. } = reader.next_event().unwrap().unwrap() else {
            panic!()
        };
        assert_eq!(name, "d");
        assert_eq!(read_text_element(&mut reader, "d").unwrap(), "ok");
    }

    #[test]
    fn rejects_malformed_xml() {
        for input in ["<a", "<a href=\"x>"] {
            let mut reader = XmlReader::new(input.as_bytes());
            let mut error = None;
            loop {
                match reader.next_event() {
                    Ok(Some(_)) => continue,
                    Ok(None) => break,
                    Err(found) => {
                        error = Some(found);
                        break;
                    }
                }
            }
            assert!(error.is_some(), "expected an error for {input:?}");
        }
    }

    #[test]
    fn attribute_set_and_find() {
        let mut attributes = vec![("p".to_string(), "1.000,1".to_string())];
        set_attribute(&mut attributes, "p", "2.000,1".to_string());
        assert_eq!(find_attribute(&attributes, "p"), Some("2.000,1"));
        set_attribute(&mut attributes, "user", "x".to_string());
        assert_eq!(find_attribute(&attributes, "user"), Some("x"));
    }
}
