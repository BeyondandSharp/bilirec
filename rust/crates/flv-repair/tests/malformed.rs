// SPDX-License-Identifier: GPL-3.0-or-later
//
// Robustness: arbitrary malformed input must never panic and best-effort output must
// always be a structurally valid FLV.

mod support;

use flv_repair::parser::TagReader;
use support::*;

fn xorshift(state: &mut u64) -> u64 {
    let mut value = *state;
    value ^= value << 13;
    value ^= value >> 7;
    value ^= value << 17;
    *state = value;
    value
}

#[test]
fn random_bytes_never_panic_and_produce_valid_flv() {
    let mut state = 0x1234_5678_9abc_def0u64;
    for case in 0..120u32 {
        let length = (xorshift(&mut state) % 4096) as usize;
        let mut bytes = vec![0u8; length];
        for byte in &mut bytes {
            *byte = (xorshift(&mut state) & 0xff) as u8;
        }
        // TagReader must not panic in either mode.
        let mut reader = TagReader::new(&bytes[..], 4096, false);
        while let Ok(Some(_tag)) = reader.read_tag() {}
        let mut strict = TagReader::new(&bytes[..], 4096, true);
        let _ = strict.read_tag();

        let (output, _stats) = repair_library_with_limit(&bytes, 4096);
        parse_flv(&output)
            .unwrap_or_else(|error| panic!("case {case}: best-effort output invalid: {error}"));
    }
}

#[test]
fn mutated_clean_stream_never_panics() {
    let clean = clean_avc_stream();
    let mut state = 0xdead_beef_cafe_babeu64;
    for case in 0..100u32 {
        let mut bytes = clean.clone();
        let mutations = 1 + (xorshift(&mut state) % 8);
        for _ in 0..mutations {
            if bytes.is_empty() {
                break;
            }
            let position = (xorshift(&mut state) as usize) % bytes.len();
            bytes[position] = (xorshift(&mut state) & 0xff) as u8;
        }
        // Random truncation too.
        if case % 3 == 0 {
            let keep = (xorshift(&mut state) as usize) % bytes.len().max(1);
            bytes.truncate(keep);
        }
        let (output, _stats) = repair_library_with_limit(&bytes, 256 * 1024);
        parse_flv(&output)
            .unwrap_or_else(|error| panic!("case {case}: best-effort output invalid: {error}"));
    }
}

#[test]
fn empty_and_tiny_inputs_are_handled() {
    for bytes in [
        Vec::new(),
        b"F".to_vec(),
        b"FL".to_vec(),
        b"FLV".to_vec(),
        b"FLV\x01".to_vec(),
        b"FLV\x01\x05\x00\x00\x00\x09".to_vec(),
        b"FLV\x01\x05\x00\x00\x00\x09\x00\x00\x00\x00".to_vec(),
    ] {
        let (output, _stats) = repair_library_with_limit(&bytes, 64 * 1024);
        parse_flv(&output)
            .unwrap_or_else(|error| panic!("input {bytes:?}: invalid output: {error}"));
    }
}
