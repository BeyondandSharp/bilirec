// SPDX-License-Identifier: GPL-3.0-or-later
//
// Minimal ZIP writer (stored/no compression) used by `flvrepair export --output *.zip`.
// Implemented from the PKWARE APPNOTE structure; no external dependencies.

const LOCAL_FILE_HEADER_SIGNATURE: u32 = 0x0403_4b50;
const CENTRAL_DIRECTORY_SIGNATURE: u32 = 0x0201_4b50;
const END_OF_CENTRAL_DIRECTORY_SIGNATURE: u32 = 0x0605_4b50;
/// General purpose flag bit 11: file names are UTF-8.
const UTF8_FLAG: u16 = 0x0800;

pub struct ZipEntry {
    pub name: String,
    pub data: Vec<u8>,
}

/// CRC-32 (IEEE 802.3), as required by the ZIP format.
pub fn crc32(data: &[u8]) -> u32 {
    let mut crc = 0xffff_ffffu32;
    for byte in data {
        crc ^= u32::from(*byte);
        for _ in 0..8 {
            let mask = (crc & 1).wrapping_neg();
            crc = (crc >> 1) ^ (0xedb8_8320 & mask);
        }
    }
    !crc
}

/// Build a complete ZIP archive containing the given entries (stored, no compression).
pub fn build_zip(entries: &[ZipEntry]) -> Vec<u8> {
    let mut out = Vec::new();
    let mut central_directory = Vec::new();

    for entry in entries {
        let name = entry.name.as_bytes();
        let crc = crc32(&entry.data);
        let size = entry.data.len() as u32;
        let offset = out.len() as u32;

        // Local file header.
        out.extend_from_slice(&LOCAL_FILE_HEADER_SIGNATURE.to_le_bytes());
        out.extend_from_slice(&20u16.to_le_bytes()); // version needed
        out.extend_from_slice(&UTF8_FLAG.to_le_bytes());
        out.extend_from_slice(&0u16.to_le_bytes()); // method: stored
        out.extend_from_slice(&0u16.to_le_bytes()); // mod time (fixed, deterministic)
        out.extend_from_slice(&0x0021u16.to_le_bytes()); // mod date: 1980-01-01
        out.extend_from_slice(&crc.to_le_bytes());
        out.extend_from_slice(&size.to_le_bytes()); // compressed size
        out.extend_from_slice(&size.to_le_bytes()); // uncompressed size
        out.extend_from_slice(&(name.len() as u16).to_le_bytes());
        out.extend_from_slice(&0u16.to_le_bytes()); // extra field length
        out.extend_from_slice(name);
        out.extend_from_slice(&entry.data);

        // Central directory record.
        central_directory.extend_from_slice(&CENTRAL_DIRECTORY_SIGNATURE.to_le_bytes());
        central_directory.extend_from_slice(&20u16.to_le_bytes()); // version made by
        central_directory.extend_from_slice(&20u16.to_le_bytes()); // version needed
        central_directory.extend_from_slice(&UTF8_FLAG.to_le_bytes());
        central_directory.extend_from_slice(&0u16.to_le_bytes()); // method
        central_directory.extend_from_slice(&0u16.to_le_bytes()); // mod time
        central_directory.extend_from_slice(&0x0021u16.to_le_bytes()); // mod date
        central_directory.extend_from_slice(&crc.to_le_bytes());
        central_directory.extend_from_slice(&size.to_le_bytes());
        central_directory.extend_from_slice(&size.to_le_bytes());
        central_directory.extend_from_slice(&(name.len() as u16).to_le_bytes());
        central_directory.extend_from_slice(&0u16.to_le_bytes()); // extra
        central_directory.extend_from_slice(&0u16.to_le_bytes()); // comment
        central_directory.extend_from_slice(&0u16.to_le_bytes()); // disk number
        central_directory.extend_from_slice(&0u16.to_le_bytes()); // internal attrs
        central_directory.extend_from_slice(&0u32.to_le_bytes()); // external attrs
        central_directory.extend_from_slice(&offset.to_le_bytes());
        central_directory.extend_from_slice(name);
    }

    let central_directory_offset = out.len() as u32;
    out.extend_from_slice(&central_directory);

    // End of central directory.
    out.extend_from_slice(&END_OF_CENTRAL_DIRECTORY_SIGNATURE.to_le_bytes());
    out.extend_from_slice(&0u16.to_le_bytes()); // this disk
    out.extend_from_slice(&0u16.to_le_bytes()); // central directory disk
    out.extend_from_slice(&(entries.len() as u16).to_le_bytes());
    out.extend_from_slice(&(entries.len() as u16).to_le_bytes());
    out.extend_from_slice(&(central_directory.len() as u32).to_le_bytes());
    out.extend_from_slice(&central_directory_offset.to_le_bytes());
    out.extend_from_slice(&0u16.to_le_bytes()); // comment length
    out
}

/// Read the (single) stored entry of a ZIP archive. Used by `export` tests; also handy
/// for verifying our own output.
pub fn read_single_stored_entry(archive: &[u8]) -> Result<(&str, &[u8]), String> {
    if archive.len() < 30
        || u32::from_le_bytes(archive[0..4].try_into().map_err(|_| "short")?)
            != LOCAL_FILE_HEADER_SIGNATURE
    {
        return Err("not a ZIP local file header".to_string());
    }
    let flags = u16::from_le_bytes(archive[6..8].try_into().map_err(|_| "short")?);
    let method = u16::from_le_bytes(archive[8..10].try_into().map_err(|_| "short")?);
    if method != 0 {
        return Err("only stored entries are supported".to_string());
    }
    let crc = u32::from_le_bytes(archive[14..18].try_into().map_err(|_| "short")?);
    let size = u32::from_le_bytes(archive[18..22].try_into().map_err(|_| "short")?) as usize;
    let name_length = u16::from_le_bytes(archive[26..28].try_into().map_err(|_| "short")?) as usize;
    let extra_length =
        u16::from_le_bytes(archive[28..30].try_into().map_err(|_| "short")?) as usize;
    let name_start = 30;
    let name_end = name_start + name_length;
    let data_start = name_end + extra_length;
    let data_end = data_start + size;
    if data_end > archive.len() {
        return Err("truncated ZIP entry".to_string());
    }
    let name = std::str::from_utf8(&archive[name_start..name_end])
        .map_err(|_| "entry name is not UTF-8".to_string())?;
    let data = &archive[data_start..data_end];
    if crc32(data) != crc {
        return Err("CRC mismatch".to_string());
    }
    let _ = flags;
    Ok((name, data))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn crc32_known_values() {
        assert_eq!(crc32(b""), 0);
        assert_eq!(crc32(b"123456789"), 0xcbf4_3926);
    }

    #[test]
    fn zip_roundtrip() {
        let archive = build_zip(&[ZipEntry {
            name: "a.txt".to_string(),
            data: b"hello".to_vec(),
        }]);
        let (name, data) = read_single_stored_entry(&archive).expect("read entry");
        assert_eq!(name, "a.txt");
        assert_eq!(data, b"hello");
    }
}
