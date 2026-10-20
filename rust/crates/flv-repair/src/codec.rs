// SPDX-License-Identifier: GPL-3.0-or-later
//
// Codec configuration parsing: AVC/HEVC decoder configuration records, SPS dimensions,
// AAC AudioSpecificConfig and H.264/H.265 filler NAL unit removal.

/// H.264 NAL unit types used by the repair rules.
pub const H264_NALU_FILLER_DATA: u8 = 12;
/// H.265 NAL unit type for filler data.
pub const H265_NALU_FILLER_DATA: u8 = 38;

/// Remove emulation prevention bytes (0x00 0x00 0x03) from an RBSP payload.
fn remove_emulation_prevention(data: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(data.len());
    let mut zeros = 0;
    for &byte in data {
        if zeros >= 2 && byte == 0x03 {
            zeros = 0;
            continue;
        }
        out.push(byte);
        if byte == 0 {
            zeros += 1;
        } else {
            zeros = 0;
        }
    }
    out
}

/// Big endian bit reader for RBSP parsing. All reads are bounds checked.
struct BitReader {
    data: Vec<u8>,
    bit: usize,
}

impl BitReader {
    fn new(data: Vec<u8>) -> Self {
        BitReader { data, bit: 0 }
    }

    fn bits_left(&self) -> usize {
        self.data.len().saturating_mul(8).saturating_sub(self.bit)
    }

    fn read_bit(&mut self) -> Option<u32> {
        if self.bit >= self.data.len().checked_mul(8)? {
            return None;
        }
        let byte = *self.data.get(self.bit / 8)?;
        let shift = 7 - (self.bit % 8);
        self.bit += 1;
        Some(((byte >> shift) & 1) as u32)
    }

    fn read_bits(&mut self, count: usize) -> Option<u32> {
        if count > 32 || self.bits_left() < count {
            return None;
        }
        let mut value = 0u32;
        for _ in 0..count {
            value = (value << 1) | self.read_bit()?;
        }
        Some(value)
    }

    fn read_ue(&mut self) -> Option<u32> {
        let mut leading = 0usize;
        while self.read_bit()? == 0 {
            leading += 1;
            if leading > 31 {
                return None;
            }
        }
        if leading == 0 {
            return Some(0);
        }
        let suffix = self.read_bits(leading)?;
        ((1u32 << leading) - 1).checked_add(suffix)
    }

    fn read_se(&mut self) -> Option<i32> {
        let value = self.read_ue()?;
        let magnitude = (value as i64 + 1) / 2;
        let signed = if value % 2 == 0 {
            -magnitude
        } else {
            magnitude
        };
        i32::try_from(signed).ok()
    }
}

/// First SPS NAL unit from an AVCDecoderConfigurationRecord (without the FLV 5-byte header).
pub fn avc_config_sps(record: &[u8]) -> Option<&[u8]> {
    if record.len() < 7 || record[0] != 1 {
        return None;
    }
    let num_sps = (record[5] & 0x1f) as usize;
    if num_sps == 0 {
        return None;
    }
    let length = u16::from_be_bytes([record[6], record[7]]) as usize;
    let start = 8usize;
    let end = start.checked_add(length)?;
    record.get(start..end)
}

/// First SPS NAL unit from a HEVCDecoderConfigurationRecord (without the FLV 5-byte header).
pub fn hevc_config_sps(record: &[u8]) -> Option<&[u8]> {
    if record.len() < 23 {
        return None;
    }
    let num_arrays = record[22] as usize;
    let mut pos = 23usize;
    for _ in 0..num_arrays {
        let header = *record.get(pos)?;
        let nal_type = header & 0x3f;
        pos += 1;
        let num_nalus = u16::from_be_bytes([*record.get(pos)?, *record.get(pos + 1)?]) as usize;
        pos += 2;
        for _ in 0..num_nalus {
            let length = u16::from_be_bytes([*record.get(pos)?, *record.get(pos + 1)?]) as usize;
            pos += 2;
            let end = pos.checked_add(length)?;
            let nalu = record.get(pos..end)?;
            if nal_type == 33 || nalu.first().map(|byte| (byte >> 1) & 0x3f) == Some(33) {
                return Some(nalu);
            }
            pos = end;
        }
    }
    None
}

/// Compute (width, height) in pixels from an H.264 SPS NAL unit.
pub fn avc_sps_dimensions(sps: &[u8]) -> Option<(u32, u32)> {
    if sps.len() < 4 {
        return None;
    }
    // Skip the 1-byte NAL header and strip emulation prevention bytes.
    let mut reader = BitReader::new(remove_emulation_prevention(&sps[1..]));
    let profile_idc = reader.read_bits(8)?;
    reader.read_bits(8)?; // constraint flags
    reader.read_bits(8)?; // level_idc
    reader.read_ue()?; // seq_parameter_set_id

    let mut chroma_format_idc = 1u32;
    if matches!(
        profile_idc,
        100 | 110 | 122 | 244 | 44 | 83 | 86 | 118 | 128 | 138 | 139 | 134 | 135
    ) {
        chroma_format_idc = reader.read_ue()?;
        if chroma_format_idc == 3 {
            reader.read_bit()?; // separate_colour_plane_flag
        }
        reader.read_ue()?; // bit_depth_luma_minus8
        reader.read_ue()?; // bit_depth_chroma_minus8
        reader.read_bit()?; // qpprime_y_zero_transform_bypass_flag
        if reader.read_bit()? == 1 {
            let list_count = if chroma_format_idc != 3 { 8 } else { 12 };
            for index in 0..list_count {
                if reader.read_bit()? == 1 {
                    let size = if index < 6 { 16 } else { 64 };
                    let mut last_scale = 8i32;
                    let mut next_scale = 8i32;
                    for _ in 0..size {
                        if next_scale != 0 {
                            let delta = reader.read_se()?;
                            next_scale = (last_scale + delta + 256).rem_euclid(256);
                        }
                        if next_scale != 0 {
                            last_scale = next_scale;
                        }
                    }
                }
            }
        }
    }

    reader.read_ue()?; // log2_max_frame_num_minus4
    let pic_order_cnt_type = reader.read_ue()?;
    match pic_order_cnt_type {
        0 => {
            reader.read_ue()?; // log2_max_pic_order_cnt_lsb_minus4
        }
        1 => {
            reader.read_bit()?; // delta_pic_order_always_zero_flag
            reader.read_se()?; // offset_for_non_ref_pic
            reader.read_se()?; // offset_for_top_to_bottom_field
            let cycle = reader.read_ue()?;
            if cycle > 255 {
                return None;
            }
            for _ in 0..cycle {
                reader.read_se()?;
            }
        }
        _ => {}
    }
    reader.read_ue()?; // max_num_ref_frames
    reader.read_bit()?; // gaps_in_frame_num_value_allowed_flag
    let width_in_mbs = reader.read_ue()?.checked_add(1)?;
    let height_in_map_units = reader.read_ue()?.checked_add(1)?;
    let frame_mbs_only = reader.read_bit()?;
    if frame_mbs_only == 0 {
        reader.read_bit()?; // mb_adaptive_frame_field_flag
    }
    reader.read_bit()?; // direct_8x8_inference_flag
    let mut crop_left = 0u32;
    let mut crop_right = 0u32;
    let mut crop_top = 0u32;
    let mut crop_bottom = 0u32;
    if reader.read_bit()? == 1 {
        crop_left = reader.read_ue()?;
        crop_right = reader.read_ue()?;
        crop_top = reader.read_ue()?;
        crop_bottom = reader.read_ue()?;
    }

    let crop_unit_x: u32 = if chroma_format_idc == 0 || chroma_format_idc == 3 {
        1
    } else {
        2
    };
    let crop_unit_y: u32 = (2 - frame_mbs_only) * if chroma_format_idc == 1 { 2 } else { 1 };
    let width = width_in_mbs.checked_mul(16)?;
    let height = (2 - frame_mbs_only)
        .checked_mul(height_in_map_units)?
        .checked_mul(16)?;
    let width = width.checked_sub(crop_unit_x.checked_mul(crop_left.checked_add(crop_right)?)?)?;
    let height =
        height.checked_sub(crop_unit_y.checked_mul(crop_top.checked_add(crop_bottom)?)?)?;
    if width == 0 || height == 0 {
        return None;
    }
    Some((width, height))
}

/// Compute (width, height) in pixels from a HEVC SPS NAL unit.
pub fn hevc_sps_dimensions(sps: &[u8]) -> Option<(u32, u32)> {
    if sps.len() < 3 {
        return None;
    }
    let mut reader = BitReader::new(remove_emulation_prevention(&sps[2..]));
    reader.read_bits(4)?; // sps_video_parameter_set_id
    let max_sub_layers_minus1 = reader.read_bits(3)?;
    reader.read_bit()?; // sps_temporal_id_nesting_flag
                        // profile_tier_level(1, max_sub_layers_minus1)
    reader.read_bits(2)?; // general_profile_space
    reader.read_bit()?; // general_tier_flag
    reader.read_bits(5)?; // general_profile_idc
    reader.read_bits(32)?; // general_profile_compatibility_flags
    reader.read_bits(32)?; // constraint flags (48 bits total)
    reader.read_bits(16)?;
    reader.read_bits(8)?; // general_level_idc
    let mut profile_present = Vec::new();
    let mut level_present = Vec::new();
    for _ in 0..max_sub_layers_minus1 {
        profile_present.push(reader.read_bit()?);
        level_present.push(reader.read_bit()?);
    }
    if max_sub_layers_minus1 > 0 {
        for _ in max_sub_layers_minus1..8 {
            reader.read_bits(2)?; // reserved_zero_2bits
        }
        for (present, level) in profile_present.iter().zip(level_present.iter()) {
            if *present == 1 {
                // Sub-layer profile/level syntax is not needed for dimensions; its
                // exact length depends on the profile. Bail out instead of guessing.
                let _ = level;
                return None;
            }
        }
    }
    reader.read_ue()?; // sps_seq_parameter_set_id
    let chroma_format_idc = reader.read_ue()?;
    if chroma_format_idc == 3 {
        reader.read_bit()?; // separate_colour_plane_flag
    }
    let width = reader.read_ue()?;
    let height = reader.read_ue()?;
    let (mut left, mut right, mut top, mut bottom) = (0u32, 0u32, 0u32, 0u32);
    if reader.read_bit()? == 1 {
        left = reader.read_ue()?;
        right = reader.read_ue()?;
        top = reader.read_ue()?;
        bottom = reader.read_ue()?;
    }
    let sub_width: u32 = if chroma_format_idc == 1 || chroma_format_idc == 2 {
        2
    } else {
        1
    };
    let sub_height: u32 = if chroma_format_idc == 1 { 2 } else { 1 };
    let width = width.checked_sub(sub_width.checked_mul(left.checked_add(right)?)?)?;
    let height = height.checked_sub(sub_height.checked_mul(top.checked_add(bottom)?)?)?;
    if width == 0 || height == 0 {
        return None;
    }
    Some((width, height))
}

/// Sampling rates for AAC samplingFrequencyIndex.
const AAC_SAMPLE_RATES: [u32; 13] = [
    96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350,
];

/// Parse an AAC AudioSpecificConfig: returns (sample_rate, channels).
pub fn aac_audio_specific_config(data: &[u8]) -> Option<(u32, u8)> {
    let mut reader = BitReader::new(data.to_vec());
    let mut object_type = reader.read_bits(5)?;
    if object_type == 31 {
        object_type = 32 + reader.read_bits(6)?;
    }
    if object_type == 0 {
        return None;
    }
    let frequency_index = reader.read_bits(4)? as usize;
    let sample_rate = match frequency_index {
        0..=12 => AAC_SAMPLE_RATES[frequency_index],
        15 => reader.read_bits(24)?,
        _ => return None,
    };
    let channel_config = reader.read_bits(4)?;
    let channels = u8::try_from(channel_config).ok()?;
    Some((sample_rate, channels))
}

/// Human readable audio codec name for a FLV audio format nibble.
pub fn audio_codec_name(format: u8) -> Option<&'static str> {
    match format {
        0 => Some("pcm"),
        1 => Some("adpcm"),
        2 => Some("mp3"),
        3 => Some("pcm-le"),
        4 => Some("nellymoser16k"),
        5 => Some("nellymoser8k"),
        6 => Some("nellymoser"),
        7 => Some("g711a"),
        8 => Some("g711u"),
        10 => Some("aac"),
        11 => Some("speex"),
        14 => Some("mp3-8k"),
        _ => None,
    }
}

/// Count the length prefixed NAL units in an FLV AVC/HEVC NALU packet payload.
/// Returns `(prefix_offset, total_size, nalu_type)` triples; `None` on malformed data.
fn parse_nalus(data: &[u8], hevc: bool) -> Option<Vec<(usize, usize, u8)>> {
    if data.len() < 6 {
        return Some(Vec::new());
    }
    let mut position = 5usize;
    let mut nalus = Vec::new();
    while position < data.len() {
        let length_bytes = data.get(position..position.checked_add(4)?)?;
        let length = u32::from_be_bytes([
            length_bytes[0],
            length_bytes[1],
            length_bytes[2],
            length_bytes[3],
        ]) as usize;
        let payload_start = position.checked_add(4)?;
        let end = payload_start.checked_add(length)?;
        let payload = data.get(payload_start..end)?;
        let nalu_type = if hevc {
            (payload.first()? >> 1) & 0x3f
        } else {
            payload.first()? & 0x1f
        };
        nalus.push((position, length + 4, nalu_type));
        position = end;
    }
    Some(nalus)
}

/// One length-prefixed NAL unit as recorded by the reference parser
/// (`H264Nalu.StartPosition` = offset of the NAL header, `FullSize` = NAL payload size).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct NaluInfo {
    pub start_position: usize,
    pub full_size: usize,
    pub nalu_type: u8,
}

/// Parse the length-prefixed NALU list of a legacy AVC (`codec id 7`) video tag body.
/// Returns `None` when the payload is not parseable as an AVC NALU packet.
pub fn avc_nalus(body: &[u8]) -> Option<Vec<NaluInfo>> {
    if body.len() < 5 || body[0] & 0x80 != 0 || body[0] & 0x0f != 7 {
        return None;
    }
    let mut position = 5usize;
    let mut nalus = Vec::new();
    while position < body.len() {
        let length_bytes = body.get(position..position.checked_add(4)?)?;
        let length = u32::from_be_bytes([
            length_bytes[0],
            length_bytes[1],
            length_bytes[2],
            length_bytes[3],
        ]) as usize;
        let payload_start = position.checked_add(4)?;
        let end = payload_start.checked_add(length)?;
        let payload = body.get(payload_start..end)?;
        nalus.push(NaluInfo {
            start_position: payload_start,
            full_size: length,
            nalu_type: payload.first()? & 0x1f,
        });
        position = end;
    }
    Some(nalus)
}

/// Remove filler NAL units from a length prefixed AVC/HEVC packet in place.
/// Returns the number of removed NAL units (0 when the packet is not a parseable
/// NALU packet or contains no filler data).
pub fn strip_filler_nalus(data: &mut Vec<u8>) -> usize {
    if data.len() < 5 {
        return 0;
    }
    let codec_id = data[0] & 0x0f;
    let hevc = match codec_id {
        7 => false,
        12 => true,
        _ => return 0,
    };
    let filler_type = if hevc {
        H265_NALU_FILLER_DATA
    } else {
        H264_NALU_FILLER_DATA
    };
    if data[0] & 0x80 != 0 {
        // Enhanced-FLV payloads have a different header layout; not handled here.
        return 0;
    }
    let Some(nalus) = parse_nalus(data, hevc) else {
        return 0;
    };
    let removed: Vec<&(usize, usize, u8)> = nalus
        .iter()
        .filter(|(_, _, nalu_type)| *nalu_type == filler_type)
        .collect();
    if removed.is_empty() {
        return 0;
    }
    let removed_count = removed.len();
    let mut rebuilt = Vec::with_capacity(data.len());
    rebuilt.extend_from_slice(&data[..5]);
    for (offset, size, nalu_type) in &nalus {
        if *nalu_type == filler_type {
            continue;
        }
        if let Some(slice) = data.get(*offset..offset + size) {
            rebuilt.extend_from_slice(slice);
        }
    }
    *data = rebuilt;
    removed_count
}

/// Parse dimensions from a complete FLV video sequence header tag body.
pub fn sequence_header_dimensions(body: &[u8]) -> Option<(Option<u32>, Option<u32>)> {
    if body.len() < 6 {
        return None;
    }
    let (avc, record) = if body[0] & 0x80 != 0 {
        // Enhanced-FLV: 1 byte header + 4 byte FourCC + decoder configuration record.
        let fourcc = body.get(1..5)?;
        let record = body.get(5..)?;
        match fourcc {
            b"avc1" => (true, record),
            b"hvc1" | b"hev1" => (false, record),
            _ => return None,
        }
    } else {
        match body[0] & 0x0f {
            7 => (true, body.get(5..)?),
            12 => (false, body.get(5..)?),
            _ => return None,
        }
    };
    if avc {
        let sps = avc_config_sps(record)?;
        let dimensions = avc_sps_dimensions(sps);
        Some((dimensions.map(|d| d.0), dimensions.map(|d| d.1)))
    } else {
        let sps = hevc_config_sps(record)?;
        let dimensions = hevc_sps_dimensions(sps);
        Some((dimensions.map(|d| d.0), dimensions.map(|d| d.1)))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn removes_emulation_prevention() {
        assert_eq!(
            remove_emulation_prevention(&[0, 0, 3, 1, 0, 0, 3]),
            vec![0, 0, 1, 0, 0]
        );
    }

    #[test]
    fn rejects_garbage_sps() {
        assert!(avc_sps_dimensions(&[0x67, 0x00]).is_none());
        assert!(hevc_sps_dimensions(&[0x42, 0x01, 0x00]).is_none());
        assert!(aac_audio_specific_config(&[]).is_none());
    }

    fn hex_to_vec(hex: &str) -> Vec<u8> {
        (0..hex.len())
            .step_by(2)
            .map(|index| u8::from_str_radix(&hex[index..index + 2], 16).expect("hex"))
            .collect()
    }

    #[test]
    fn parses_hevc_sps_dimensions_legacy_and_enhanced() {
        // SPS from the HEVC fixture base (x265, 640x360 with conformance window).
        let sps = hex_to_vec(
            "42010101600000030090000003000003003fa005020171f2e595952930bc05a020000003002000000303c1",
        );
        assert_eq!(hevc_sps_dimensions(&sps), Some((640, 360)));

        let mut record = vec![
            1, 0x01, 0x60, 0, 0, 0, 0x90, 0, 0, 0, 0, 0, 0x5d, 0xf0, 0, 0xfc, 0xfd, 0xf8, 0xf8, 0,
            0, 0x0f,
        ];
        record.push(1); // numOfArrays
        record.push(0x20 | 33); // SPS array
        record.extend_from_slice(&1u16.to_be_bytes());
        record.extend_from_slice(&(sps.len() as u16).to_be_bytes());
        record.extend_from_slice(&sps);

        let mut enhanced = vec![0x90, b'h', b'v', b'c', b'1'];
        enhanced.extend_from_slice(&record);
        assert_eq!(
            sequence_header_dimensions(&enhanced),
            Some((Some(640), Some(360)))
        );

        let mut legacy = vec![0x1c, 0x00, 0, 0, 0];
        legacy.extend_from_slice(&record);
        assert_eq!(
            sequence_header_dimensions(&legacy),
            Some((Some(640), Some(360)))
        );
    }

    #[test]
    fn filler_stripping_bounds_checks() {
        // Not an AVC NALU packet.
        let mut data = vec![0x17, 0x00, 0, 0, 0];
        data.extend_from_slice(&[0xff, 0xff, 0xff, 0xff]);
        assert_eq!(strip_filler_nalus(&mut data), 0);

        // Length prefix pointing past the end must not panic and must not modify data.
        let original = data.clone();
        assert_eq!(strip_filler_nalus(&mut data), 0);
        assert_eq!(data, original);
    }
}
