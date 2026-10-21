// SPDX-License-Identifier: GPL-3.0-only
//
// Tag grouping, ported from `refs/BililiveRecorder/BililiveRecorder.Flv/Grouping/**`
// (GPL-3.0-only). Groups are the unit of processing for the repair rules.

use crate::parser::{ParseError, TagReader};
use crate::tag::{Tag, TagType};
use std::io::Read;

/// Maximum timestamp distance (ms) inside one data group (reference constant).
const MAX_DATA_GROUP_GAP: i64 = 24_999;

/// A group of tags that the repair pipeline processes together.
#[derive(Debug)]
pub enum Group {
    Script(Tag),
    Header(Vec<Tag>),
    End(Tag),
    Data(Vec<Tag>),
}

impl Group {
    pub fn tags(&self) -> &[Tag] {
        match self {
            Group::Script(tag) | Group::End(tag) => std::slice::from_ref(tag),
            Group::Header(tags) | Group::Data(tags) => tags,
        }
    }

    pub fn tag_count(&self) -> usize {
        match self {
            Group::Script(_) | Group::End(_) => 1,
            Group::Header(tags) | Group::Data(tags) => tags.len(),
        }
    }
}

#[derive(Clone, Copy)]
enum Rule {
    Script,
    End,
    Header,
    Data,
}

/// Reads tags and emits groups using the reference grouping rules.
pub struct GroupReader<R: Read> {
    reader: TagReader<R>,
    pending: Option<Tag>,
}

impl<R: Read> GroupReader<R> {
    pub fn new(reader: TagReader<R>) -> Self {
        GroupReader {
            reader,
            pending: None,
        }
    }

    pub fn reader(&self) -> &TagReader<R> {
        &self.reader
    }

    pub fn next_group(&mut self) -> Result<Option<Group>, ParseError> {
        let first = match self.pending.take() {
            Some(tag) => tag,
            None => match self.reader.read_tag()? {
                Some(tag) => tag,
                None => return Ok(None),
            },
        };

        let rule = if first.is_script() {
            Rule::Script
        } else if first.is_end() {
            Rule::End
        } else if first.is_header() {
            Rule::Header
        } else {
            Rule::Data
        };

        let mut tags = vec![first];
        let mut group_bytes: usize = tags[0].data.len();
        let mut pending = None;
        while let Some(next) = self.reader.read_tag()? {
            // Respect `--max-buffer`: one group may not grow past the buffer cap. In
            // normal operation (default 64 MiB) groups stay far below this limit.
            if group_bytes.saturating_add(next.data.len()) > self.reader.max_buffer() {
                pending = Some(next);
                break;
            }
            if can_append(rule, &tags, &next) {
                group_bytes = group_bytes.saturating_add(next.data.len());
                tags.push(next);
            } else {
                pending = Some(next);
                break;
            }
        }
        self.pending = pending;

        Ok(Some(match rule {
            Rule::Script => Group::Script(tags.remove(0)),
            Rule::End => Group::End(tags.remove(0)),
            Rule::Header => Group::Header(tags),
            Rule::Data => Group::Data(tags),
        }))
    }
}

fn can_append(rule: Rule, tags: &[Tag], next: &Tag) -> bool {
    match rule {
        Rule::Script | Rule::End => false,
        Rule::Header => next.is_header(),
        Rule::Data => {
            let last = tags.last();
            // Non-keyframe data continues the group as long as timestamps stay close.
            (next.is_non_keyframe_data()
                && last.is_none_or(|last| next.timestamp - last.timestamp < MAX_DATA_GROUP_GAP))
                // A keyframe continues a group that so far only holds audio.
                || (next.tag_type == TagType::Video
                    && next.is_keyframe_data()
                    && tags.iter().all(|tag| tag.tag_type == TagType::Audio))
                // A delayed audio sequence header continues a group that has no audio data yet.
                || (next.tag_type == TagType::Audio
                    && next.is_header()
                    && tags
                        .iter()
                        .all(|tag| tag.tag_type != TagType::Audio || tag.is_header()))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::parser::encode_file_header;
    use crate::tag::encode_tag_into;

    fn push_tag(bytes: &mut Vec<u8>, tag_type: TagType, timestamp: i64, body: Vec<u8>) {
        let tag = Tag::new(tag_type, timestamp, body, 0);
        encode_tag_into(&tag, timestamp, bytes);
    }

    #[test]
    fn groups_headers_and_data() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, true);
        push_tag(&mut bytes, TagType::Video, 0, vec![0x17, 0x00, 0, 0, 0]);
        push_tag(&mut bytes, TagType::Audio, 0, vec![0xaf, 0x00, 0x12, 0x10]);
        push_tag(&mut bytes, TagType::Audio, 10, vec![0xaf, 0x01, 1]);
        push_tag(&mut bytes, TagType::Video, 20, vec![0x17, 0x01, 0, 0, 0]);
        push_tag(&mut bytes, TagType::Audio, 30, vec![0xaf, 0x01, 2]);
        let mut groups = GroupReader::new(TagReader::new(&bytes[..], 1 << 20, false));
        let first = groups.next_group().unwrap().unwrap();
        assert!(matches!(first, Group::Header(ref tags) if tags.len() == 2));
        let second = groups.next_group().unwrap().unwrap();
        assert!(matches!(second, Group::Data(ref tags) if tags.len() == 3));
        assert!(groups.next_group().unwrap().is_none());
    }

    #[test]
    fn degraded_header_group_is_separate() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, true);
        push_tag(&mut bytes, TagType::Audio, 0, vec![0xaf, 0x00, 0x12]);
        push_tag(&mut bytes, TagType::Video, 0, vec![0x17, 0x01, 0, 0, 0]);
        let mut groups = GroupReader::new(TagReader::new(&bytes[..], 1 << 20, false));
        let first = groups.next_group().unwrap().unwrap();
        assert!(matches!(first, Group::Header(ref tags) if tags.len() == 1));
        let second = groups.next_group().unwrap().unwrap();
        assert!(matches!(second, Group::Data(ref tags) if tags.len() == 1));
    }

    #[test]
    fn delayed_audio_header_joins_data_group() {
        let mut bytes = Vec::new();
        encode_file_header(&mut bytes, true, true);
        // A data group can only acquire a delayed audio header when it started with
        // non-audio data (reference DataGroupingRule condition).
        push_tag(&mut bytes, TagType::Video, 0, vec![0x17, 0x01, 0, 0, 0]);
        push_tag(&mut bytes, TagType::Audio, 10, vec![0xaf, 0x00, 0x12, 0x10]);
        push_tag(&mut bytes, TagType::Audio, 20, vec![0xaf, 0x01, 1]);
        let mut groups = GroupReader::new(TagReader::new(&bytes[..], 1 << 20, false));
        let first = groups.next_group().unwrap().unwrap();
        assert!(matches!(first, Group::Data(ref tags) if tags.len() == 3));
    }
}
