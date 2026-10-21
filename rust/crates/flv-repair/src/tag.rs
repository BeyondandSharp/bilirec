// SPDX-License-Identifier: GPL-3.0-only
//
// FLV tag model. Ported from `refs/BililiveRecorder/BililiveRecorder.Flv/Tag.cs`,
// `TagExtentions.cs`, `TagFlag.cs`, `TagType.cs` (GPL-3.0-only). Enhanced-FLV (E-RTMP) video
// classification follows `pkg/flv/video_tag.go` (MIT), see docs/PORTING.md §3.3.

/// Size of an FLV tag header in bytes (`type` + 24-bit size + timestamp + stream id).
pub const TAG_HEADER_LEN: usize = 11;
/// Size of the trailing `PreviousTagSize` field.
pub const PREVIOUS_TAG_SIZE_LEN: usize = 4;
/// FLV file header size (before `PreviousTagSize0`).
pub const FLV_HEADER_LEN: usize = 9;

/// Timestamps are kept in milliseconds as signed 64-bit values internally so that
/// offsets can never overflow; the FLV wire format is a 32-bit unsigned field.
pub type Timestamp = i64;

/// FLV tag types.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum TagType {
    Audio = 8,
    Video = 9,
    Script = 18,
}

impl TagType {
    pub fn from_byte(byte: u8) -> Option<TagType> {
        match byte {
            8 => Some(TagType::Audio),
            9 => Some(TagType::Video),
            18 => Some(TagType::Script),
            _ => None,
        }
    }

    pub fn to_byte(self) -> u8 {
        self as u8
    }
}

/// Derived tag flags (never part of the wire format; recomputed from the tag body).
#[derive(Clone, Copy, PartialEq, Eq, Debug, Default)]
pub struct TagFlags {
    /// AVC/HEVC sequence header or AAC AudioSpecificConfig.
    pub header: bool,
    /// Video keyframe.
    pub keyframe: bool,
    /// AVC/HEVC end-of-sequence packet.
    pub end: bool,
}

/// A single FLV tag with its body copied into memory.
#[derive(Clone, Debug)]
pub struct Tag {
    pub tag_type: TagType,
    pub flags: TagFlags,
    pub timestamp: Timestamp,
    pub data: Vec<u8>,
    pub index: u64,
}

impl Tag {
    pub fn new(tag_type: TagType, timestamp: Timestamp, data: Vec<u8>, index: u64) -> Tag {
        let flags = flags_from_body(tag_type, &data);
        Tag {
            tag_type,
            flags,
            timestamp,
            data,
            index,
        }
    }

    pub fn is_script(&self) -> bool {
        self.tag_type == TagType::Script
    }

    pub fn is_header(&self) -> bool {
        self.flags.header && matches!(self.tag_type, TagType::Video | TagType::Audio)
    }

    pub fn is_end(&self) -> bool {
        self.flags.end
    }

    /// Live media data (not a sequence header, not an end-of-sequence packet).
    pub fn is_data(&self) -> bool {
        !self.flags.header
            && !self.flags.end
            && matches!(self.tag_type, TagType::Video | TagType::Audio)
    }

    pub fn is_non_keyframe_data(&self) -> bool {
        self.is_data() && !self.flags.keyframe
    }

    pub fn is_keyframe_data(&self) -> bool {
        self.is_data() && self.tag_type == TagType::Video && self.flags.keyframe
    }

    /// True for Enhanced-FLV video tags (bit 7 of the first byte set), as written by
    /// ffmpeg 6+/7 for HEVC (FourCC `hvc1`/`hev1`).
    pub fn is_enhanced_video(&self) -> bool {
        self.tag_type == TagType::Video
            && self
                .data
                .first()
                .map(|byte| byte & 0x80 != 0)
                .unwrap_or(false)
    }

    /// FourCC of an Enhanced-FLV video tag.
    pub fn video_fourcc(&self) -> Option<[u8; 4]> {
        if !self.is_enhanced_video() {
            return None;
        }
        let fourcc = self.data.get(1..5)?;
        Some([fourcc[0], fourcc[1], fourcc[2], fourcc[3]])
    }

    /// Legacy FLV video codec id (low nibble of the first byte), if this is a video tag.
    /// For Enhanced-FLV tags this is the packet type, use [`Tag::video_codec_name`] instead.
    pub fn video_codec_id(&self) -> Option<u8> {
        if self.tag_type != TagType::Video || self.is_enhanced_video() {
            return None;
        }
        self.data.first().map(|byte| byte & 0x0f)
    }

    /// Human readable video codec name for the tag header (legacy or Enhanced-FLV).
    pub fn video_codec_name(&self) -> Option<&'static str> {
        if self.tag_type != TagType::Video {
            return None;
        }
        if let Some(fourcc) = self.video_fourcc() {
            return Some(match &fourcc {
                b"avc1" => "avc",
                b"hvc1" | b"hev1" => "hevc",
                b"av01" => "av1",
                b"vp09" => "vp9",
                _ => "unknown",
            });
        }
        let codec_id = self.data.first()? & 0x0f;
        Some(match codec_id {
            2 => "sorenson-h263",
            3 => "screen-video",
            4 => "vp6",
            5 => "vp6-alpha",
            6 => "screen-video-v2",
            7 => "avc",
            12 => "hevc",
            _ => "unknown",
        })
    }

    /// Legacy AVC/HEVC packet type (second byte), if present.
    pub fn video_packet_type(&self) -> Option<u8> {
        if self.tag_type != TagType::Video || self.is_enhanced_video() {
            return None;
        }
        self.data.get(1).copied()
    }

    /// True when the video tag carries an actual coded frame (used for the "first video
    /// tag must be a keyframe" guarantee; Enhanced-FLV metadata packets are not frames).
    pub fn is_coded_video_frame(&self) -> bool {
        if self.tag_type != TagType::Video || !self.is_data() {
            return false;
        }
        if self.video_fourcc().is_some() {
            // Enhanced-FLV: packet type 4/5 are metadata / MPEG-TS sequence packets.
            return matches!(self.data[0] & 0x0f, 1 | 3);
        }
        // Legacy layout: packet type 1 is an AVC/HEVC NALU packet.
        self.data.get(1).map(|packet| *packet == 1).unwrap_or(false)
    }

    /// FLV audio format (high nibble of the first byte), if this is an audio tag.
    pub fn audio_format(&self) -> Option<u8> {
        if self.tag_type != TagType::Audio {
            return None;
        }
        self.data.first().map(|byte| byte >> 4)
    }

    /// AAC packet type (second byte) for AAC audio tags.
    pub fn aac_packet_type(&self) -> Option<u8> {
        if self.tag_type != TagType::Audio || self.audio_format() != Some(10) {
            return None;
        }
        self.data.get(1).copied()
    }

    pub fn is_aac(&self) -> bool {
        self.audio_format() == Some(10)
    }

    /// Legacy codec 7 AVC (the layout this crate strips NALUs from).
    pub fn is_avc(&self) -> bool {
        !self.is_enhanced_video() && self.video_codec_id() == Some(7)
    }

    pub fn is_hevc(&self) -> bool {
        self.video_codec_name() == Some("hevc")
    }

    /// Payload hash used for duplicate group detection. FNV-1a 64 is used instead of
    /// the reference implementation's FarmHash; only equality within one process
    /// matters, so the exact hash function is irrelevant.
    pub fn data_hash(&self) -> u64 {
        fnv1a64(&self.data)
    }

    /// Compute the wire body offset of the AVC/HEVC `composition time` field (used for
    /// DTS/PTS bookkeeping in the analysis report).
    pub fn composition_time(&self) -> Option<i32> {
        if self.tag_type != TagType::Video || !self.is_data() {
            return None;
        }
        if self.data.len() < 5 {
            return None;
        }
        let raw = i32::from_be_bytes([self.data[2], self.data[3], self.data[4], 0]);
        Some(raw >> 8)
    }
}

/// Derive tag flags from a tag body exactly like the reference parser
/// (`FlvTagPipeReader.ParseTagData`).
pub fn flags_from_body(tag_type: TagType, body: &[u8]) -> TagFlags {
    let mut flags = TagFlags::default();
    if body.len() <= 2 {
        return flags;
    }
    match tag_type {
        TagType::Audio => {
            let format = body[0] >> 4;
            if format == 10 && body[1] == 0 {
                flags.header = true;
            }
        }
        TagType::Video => {
            if body[0] & 0x80 != 0 {
                // Enhanced-FLV (E-RTMP): bit7 + 3-bit frame type + 4-bit packet type.
                if body.len() < 5 {
                    return flags;
                }
                let frame_type = (body[0] & 0x70) >> 4;
                let packet_type = body[0] & 0x0f;
                flags.keyframe = frame_type == 1;
                flags.header = packet_type == 0;
                flags.end = packet_type == 2;
                return flags;
            }
            if (body[0] & 0xf0) == 0x10 {
                flags.keyframe = true;
            }
            match body[1] {
                0 => flags.header = true,
                2 => flags.end = true,
                _ => {}
            }
        }
        TagType::Script => {}
    }
    flags
}

/// FNV-1a 64-bit hash (documented substitute for the reference FarmHash64 in the
/// duplicate-group rule and the toolbox XML `DataHash`/`NaluHash` fields).
pub fn fnv1a64(data: &[u8]) -> u64 {
    let mut hash: u64 = 0xcbf2_9ce4_8422_2325;
    for byte in data {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    hash
}

/// Encode an FLV tag (header + body + PreviousTagSize) into `out`.
pub fn encode_tag_into(tag: &Tag, timestamp: Timestamp, out: &mut Vec<u8>) {
    let clamped = timestamp.clamp(0, u32::MAX as Timestamp) as u32;
    let size = tag.data.len().min(0x00ff_ffff) as u32;
    out.push(tag.tag_type.to_byte());
    out.extend_from_slice(&size.to_be_bytes()[1..]);
    out.extend_from_slice(&clamped.to_be_bytes()[1..4]);
    out.push((clamped >> 24) as u8);
    out.extend_from_slice(&[0, 0, 0]); // stream id
                                       // The clamp above keeps `size` in sync with the copied bytes for valid tags; a tag
                                       // larger than the wire format allows is truncated rather than growing the buffer.
    let copied = tag.data.len().min(size as usize);
    out.extend_from_slice(&tag.data[..copied]);
    let previous = (size + TAG_HEADER_LEN as u32).to_be_bytes();
    out.extend_from_slice(&previous);
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tag(tag_type: TagType, body: Vec<u8>) -> Tag {
        Tag::new(tag_type, 0, body, 1)
    }

    #[test]
    fn derives_avc_flags() {
        // keyframe + AVC NALU packet
        assert!(tag(TagType::Video, vec![0x17, 0x01, 0, 0, 0, 0]).is_keyframe_data());
        // AVC sequence header
        let header = tag(TagType::Video, vec![0x17, 0x00, 0, 0, 0, 0]);
        assert!(header.is_header());
        // AVC end of sequence
        let end = tag(TagType::Video, vec![0x17, 0x02, 0, 0, 0, 0]);
        assert!(end.is_end());
        // non-keyframe data
        let data = tag(TagType::Video, vec![0x27, 0x01, 0, 0, 0, 0]);
        assert!(data.is_data());
        assert!(!data.flags.keyframe);
    }

    #[test]
    fn derives_aac_flags() {
        let header = tag(TagType::Audio, vec![0xaf, 0x00, 0x12, 0x10]);
        assert!(header.is_header());
        let data = tag(TagType::Audio, vec![0xaf, 0x01, 0x00]);
        assert!(data.is_data());
        assert!(data.is_aac());
    }

    #[test]
    fn encode_roundtrip_offsets() {
        let tag = tag(TagType::Audio, vec![0xaf, 0x01, 1, 2, 3]);
        let mut out = Vec::new();
        encode_tag_into(&tag, 1234, &mut out);
        assert_eq!(out.len(), TAG_HEADER_LEN + 5 + PREVIOUS_TAG_SIZE_LEN);
        assert_eq!(out[0], 8);
        assert_eq!(&out[1..4], &[0, 0, 5]);
        assert_eq!(&out[4..8], &[0, 0x04, 0xd2, 0]);
        assert_eq!(&out[out.len() - 4..], &16u32.to_be_bytes());
    }
}
