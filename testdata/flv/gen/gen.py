#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Generate the R1-R8 FLV repair fixtures for `flvrepair` (see docs/INTERFACES.md §1.3).

Pipeline:
  1. generate two clean ~20 s FLV bases with ffmpeg (AVC+AAC and HEVC+AAC),
     deterministically (fixed filters/encoders/params, single-threaded);
  2. parse the bases into FLV tags (complete tag model, stdlib only);
  3. apply byte/tag-level mutations that reproduce corruption classes R1-R8;
  4. write `fixtures/<class>.flv`, `fixtures/<class>.expect.json` (invariants only,
     never byte-exact repaired goldens) and `gen/manifest.json` (hashes);
  5. `--check` replays the mutations from the committed bases and verifies that the
     fixtures/expect/manifest on disk match byte-for-byte and that the expected
     corruption is actually present.

The mutation step is pure Python and deterministic: the same base bytes always produce
the same fixtures (sha256 recorded in the manifest and in every `.expect.json`).
Base generation is deterministic for a given ffmpeg/x264/x265 build; the manifest
records the base hashes and the ffmpeg version. If the toolchain changes, run
`gen.py --force` and re-check.

Usage:
    python3 testdata/flv/gen/gen.py            # generate bases if missing + fixtures
    python3 testdata/flv/gen/gen.py --force    # regenerate bases too
    python3 testdata/flv/gen/gen.py --check    # offline consistency check (no ffmpeg)

Requires: python3 (stdlib only), ffmpeg/ffprobe on PATH (generation only).
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import struct
import subprocess
import sys
from dataclasses import dataclass, replace
from pathlib import Path

# ---------------------------------------------------------------------------
# paths / constants
# ---------------------------------------------------------------------------

GEN_DIR = Path(__file__).resolve().parent
FLV_DIR = GEN_DIR.parent
BASE_DIR = FLV_DIR / "base"
FIX_DIR = FLV_DIR / "fixtures"
MANIFEST = GEN_DIR / "manifest.json"

FFMPEG = os.environ.get("FFMPEG", "ffmpeg")

BASE_SECONDS = 20
BASE_SIZE = "640x360"
BASE_FPS = 30
BASE_VIDEO_BITRATE = "500k"
BASE_AUDIO_BITRATE = "96k"

FLV_HEADER_FLAGS = 0x05  # audio + video
TAG_TYPE_AUDIO = 8
TAG_TYPE_VIDEO = 9
TAG_TYPE_SCRIPT = 18

TAG_HEADER_SIZE = 11
FLV_FILE_HEADER_SIZE = 9
MAX_TAG_BODY = 0xFFFFFF

JUMP_THRESHOLD = 500  # mirror of the C# rule constant, used only for input analysis

# exact fixture names required by the task / INTERFACES R1-R8
FIXTURES = [
    ("R1", "r1_timestamp_jump.flv"),
    ("R2", "r2_timestamp_rollback.flv"),
    ("R3", "r3_duplicated_chunk.flv"),
    ("R4", "r4_filler_data.flv"),
    ("R5", "r5_delayed_audio_header.flv"),
    ("R6", "r6_bad_metadata.flv"),
    ("R7", "r7_truncated_tail.flv"),
    ("R8", "r8_hevc_aac.flv"),
]

MAX_FIXTURE_BYTES_TOTAL = 20 * 1024 * 1024  # hard cap from the task


# ---------------------------------------------------------------------------
# small helpers
# ---------------------------------------------------------------------------

def sha256(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


def u16(b: bytes, pos: int) -> int:
    return (b[pos] << 8) | b[pos + 1]


def u24(b: bytes, pos: int) -> int:
    return (b[pos] << 16) | (b[pos + 1] << 8) | b[pos + 2]


def u32(b: bytes, pos: int) -> int:
    return struct.unpack_from(">I", b, pos)[0]


def be32(v: int) -> bytes:
    return struct.pack(">I", v & 0xFFFFFFFF)


def run(cmd: list[str]) -> str:
    try:
        out = subprocess.run(cmd, check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as exc:  # pragma: no cover - env dependent
        sys.stderr.write(exc.stdout + exc.stderr)
        raise SystemExit(f"command failed ({exc.returncode}): {' '.join(cmd)}")
    return out.stdout


# ---------------------------------------------------------------------------
# FLV model: parse / classify / encode
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Tag:
    tag_type: int
    timestamp: int
    data: bytes


@dataclass
class FlvFile:
    header_flags: int
    tags: list[Tag]


def classify_flag(t: Tag) -> int:
    """Replicate FlvTagPipeReader flag classification, plus Enhanced-FLV HEVC.

    TagFlag: Header=1, Keyframe=2, End=4.
    """
    flag = 0
    body = t.data
    if t.tag_type == TAG_TYPE_AUDIO:
        if len(body) > 2:
            fmt = body[0] >> 4
            if fmt == 10 and body[1] == 0:  # AAC sequence header
                flag |= 1
        return flag
    if t.tag_type != TAG_TYPE_VIDEO or not body:
        return flag
    b0 = body[0]
    if b0 & 0x80:  # Enhanced FLV (pkg/flv/video_tag.go semantics)
        if len(body) < 5:
            return flag
        frame_type = (b0 & 0x70) >> 4
        packet_type = b0 & 0x0F
        fourcc = body[1:5]
        if frame_type == 1:
            flag |= 2
        if fourcc in (b"hvc1", b"hev1"):
            if packet_type == 0:
                flag |= 1
            elif packet_type == 2:
                flag |= 4
        return flag
    # legacy layout
    if len(body) > 2:
        frame_type = b0 & 0xF0
        if frame_type == 0x10:
            flag |= 2
        packet = body[1]
        if packet == 0:
            flag |= 1
        elif packet == 2:
            flag |= 4
    return flag


def is_header(t: Tag) -> bool:
    return t.tag_type in (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO) and bool(classify_flag(t) & 1)


def is_end(t: Tag) -> bool:
    return bool(classify_flag(t) & 4)


def is_data(t: Tag) -> bool:
    return t.tag_type in (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO) and (classify_flag(t) & (1 | 4)) == 0


def is_non_keyframe_data(t: Tag) -> bool:
    return t.tag_type in (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO) and classify_flag(t) == 0


def is_keyframe_data(t: Tag) -> bool:
    return t.tag_type == TAG_TYPE_VIDEO and classify_flag(t) == 2


def is_audio_data(t: Tag) -> bool:
    return t.tag_type == TAG_TYPE_AUDIO and is_data(t)


def is_video_data(t: Tag) -> bool:
    return t.tag_type == TAG_TYPE_VIDEO and is_data(t)


def _is_enhanced_metadata(t: Tag) -> bool:
    """Enhanced-FLV metadata packets (packet type 4, e.g. colorInfo) are not A/V frames."""
    return (t.tag_type == TAG_TYPE_VIDEO and len(t.data) >= 5
            and bool(t.data[0] & 0x80) and (t.data[0] & 0x0F) == 4)


def video_codec(t: Tag) -> str | None:
    """Return 'avc'/'hevc' from a video sequence header (or first video tag)."""
    if t.tag_type != TAG_TYPE_VIDEO or not t.data:
        return None
    b0 = t.data[0]
    if b0 & 0x80:
        if len(t.data) >= 5:
            fourcc = t.data[1:5]
            if fourcc in (b"hvc1", b"hev1"):
                return "hevc"
        return None
    codec_id = b0 & 0x0F
    if codec_id == 7:
        return "avc"
    if codec_id == 12:
        return "hevc"
    return None


def parse_flv(data: bytes) -> FlvFile:
    """Strict parse of a well-formed FLV file (used for the clean bases and R1-R6/R8)."""
    if len(data) < FLV_FILE_HEADER_SIZE + 4:
        raise ValueError("file too short for FLV header")
    if data[0:3] != b"FLV":
        raise ValueError("missing FLV magic")
    if data[3] != 1:
        raise ValueError(f"unsupported FLV version {data[3]}")
    flags = data[4]
    data_offset = u32(data, 5)
    if data_offset < FLV_FILE_HEADER_SIZE or data_offset > len(data):
        raise ValueError(f"bad data offset {data_offset}")
    pos = data_offset
    if pos + 4 > len(data):
        raise ValueError("missing PreviousTagSize0")
    if u32(data, pos) != 0:
        raise ValueError("PreviousTagSize0 != 0")
    pos += 4
    tags: list[Tag] = []
    while pos < len(data):
        if pos + TAG_HEADER_SIZE > len(data):
            raise ValueError(f"truncated tag header at {pos}")
        tag_start = pos
        tag_type = data[tag_start]
        size = u24(data, tag_start + 1)
        ts = (data[tag_start + 7] << 24) | (data[tag_start + 4] << 16) | \
             (data[tag_start + 5] << 8) | data[tag_start + 6]
        if tag_type not in (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO, TAG_TYPE_SCRIPT):
            raise ValueError(f"unknown tag type {tag_type} at {tag_start}")
        body_start = tag_start + TAG_HEADER_SIZE
        body_end = body_start + size
        prev_pos = body_end
        if prev_pos + 4 > len(data):
            raise ValueError(f"truncated tag body at {body_start}")
        prev = u32(data, prev_pos)
        if prev != TAG_HEADER_SIZE + size:
            raise ValueError(f"PreviousTagSize mismatch at {prev_pos}: {prev} != {TAG_HEADER_SIZE + size}")
        tags.append(Tag(tag_type, ts, data[body_start:body_end]))
        pos = prev_pos + 4
    if pos != len(data):
        raise ValueError(f"trailing bytes: {len(data) - pos}")
    return FlvFile(flags, tags)


def tag_bytes(t: Tag) -> bytes:
    size = len(t.data)
    if size > MAX_TAG_BODY:
        raise ValueError("tag body too large")
    out = bytearray()
    out.append(t.tag_type)
    out += size.to_bytes(3, "big")
    # 24-bit timestamp low bits then the high byte (C#: stackTemp[1..3] = BE(ts), [0] = high)
    out += (t.timestamp & 0xFFFFFF).to_bytes(3, "big")
    out.append((t.timestamp >> 24) & 0xFF)
    out += b"\x00\x00\x00"  # stream id, always zero on output
    out += t.data
    out += be32(TAG_HEADER_SIZE + size)
    return bytes(out)


def encode_flv(flags: int, tags: list[Tag]) -> bytes:
    out = bytearray()
    out += b"FLV"
    out.append(1)
    out.append(flags & 0xFF)
    out += be32(FLV_FILE_HEADER_SIZE)
    out += be32(0)  # PreviousTagSize0
    for t in tags:
        out += tag_bytes(t)
    return bytes(out)


# ---------------------------------------------------------------------------
# input analysis (tolerant: resync + truncation, mirrors docs/PORTING.md §3.6)
# ---------------------------------------------------------------------------

def _plausible_tag(data: bytes, p: int) -> int | None:
    """Return one-past-the-end (including its trailing PreviousTagSize) of a plausible
    tag starting at p, or None. Used for resync only."""
    if p + 4 + TAG_HEADER_SIZE + 4 > len(data):
        return None
    if data[p] not in (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO, TAG_TYPE_SCRIPT):
        return None
    size = u24(data, p + 1)
    end = p + TAG_HEADER_SIZE + size
    if end + 4 > len(data):
        return None
    if u32(data, end) != TAG_HEADER_SIZE + size:
        return None
    return end + 4


def analyze_input(data: bytes) -> dict:
    """Tolerant analysis of a fixture input; also used to describe why it is corrupt."""
    info: dict = {
        "bytes": len(data),
        "sha256": sha256(data),
        "valid_flv_structure": True,
        "header_flags": None,
        "resync_events": 0,
        "resynced_bytes": 0,
        "truncated_tail_bytes": 0,
        "prev_size_mismatches": 0,
    }
    if len(data) < FLV_FILE_HEADER_SIZE + 4 or data[0:3] != b"FLV":
        info["valid_flv_structure"] = False
        return info
    info["header_flags"] = data[4]
    pos = u32(data, 5)
    if pos + 4 > len(data):
        info["valid_flv_structure"] = False
        return info
    if u32(data, pos) != 0:
        info["prev_size_mismatches"] += 1
    pos += 4
    tags: list[Tag] = []
    while pos < len(data):
        if pos + TAG_HEADER_SIZE > len(data):
            info["truncated_tail_bytes"] = len(data) - pos
            info["valid_flv_structure"] = False
            break
        tag_start = pos
        tag_type = data[tag_start]
        size = u24(data, tag_start + 1)
        body_start = tag_start + TAG_HEADER_SIZE
        if tag_type in (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO, TAG_TYPE_SCRIPT):
            body_end = body_start + size
            if body_end + 4 <= len(data) and u32(data, body_end) == TAG_HEADER_SIZE + size:
                ts = (data[tag_start + 7] << 24) | (data[tag_start + 4] << 16) | \
                     (data[tag_start + 5] << 8) | data[tag_start + 6]
                tags.append(Tag(tag_type, ts, data[body_start:body_end]))
                pos = body_end + 4
                continue
            if body_end + 4 > len(data):
                # plausible truncated final tag
                info["truncated_tail_bytes"] = len(data) - pos
                info["valid_flv_structure"] = False
                break
        # malformed prefix: resync by scanning forward
        found = None
        limit = min(len(data), pos + 65536)
        for p in range(pos + 1, limit):
            end = _plausible_tag(data, p)
            if end is not None:
                found = p
                break
        if found is None:
            info["truncated_tail_bytes"] = len(data) - pos
            info["valid_flv_structure"] = False
            info["resync_events"] += 1
            break
        info["resync_events"] += 1
        info["resynced_bytes"] += found - pos
        info["valid_flv_structure"] = False
        pos = found

    # ---- derived facts -----------------------------------------------------
    video = [(i, t) for i, t in enumerate(tags) if t.tag_type == TAG_TYPE_VIDEO]
    audio = [(i, t) for i, t in enumerate(tags) if t.tag_type == TAG_TYPE_AUDIO]
    info["tag_count"] = len(tags)
    info["tag_counts"] = {
        "video": len(video),
        "audio": len(audio),
        "script": sum(1 for t in tags if t.tag_type == TAG_TYPE_SCRIPT),
    }
    info["timestamps"] = {
        "video": _track_stats([t for _, t in video]),
        "audio": _track_stats([t for _, t in audio]),
    }
    last_tss = [t.timestamp for _, t in video[-1:]] + [t.timestamp for _, t in audio[-1:]]
    info["raw_last_timestamp_ms"] = max(last_tss) if last_tss else 0
    info["video_sequence_headers"] = sum(
        1 for t in tags if t.tag_type == TAG_TYPE_VIDEO and is_header(t))
    info["audio_sequence_headers"] = sum(
        1 for t in tags if t.tag_type == TAG_TYPE_AUDIO and is_header(t))
    codec = None
    for t in tags:
        if t.tag_type == TAG_TYPE_VIDEO and is_header(t):
            codec = video_codec(t)
            break
    info["video_codec"] = codec
    info["audio_codec"] = "aac" if info["audio_sequence_headers"] else None
    info["end_tag_count"] = sum(1 for t in tags if is_end(t))
    info["zero_size_data_tags"] = sum(
        1 for t in tags
        if t.tag_type in (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO) and len(t.data) == 0)
    info["filler_nalu_count"] = sum(_count_filler_nalus(t) for t in tags)
    info["adjacent_duplicate_tags"] = sum(
        1 for i in range(1, len(tags))
        if tags[i].tag_type == tags[i - 1].tag_type
        and tags[i].timestamp == tags[i - 1].timestamp
        and tags[i].data == tags[i - 1].data)
    groups = group_tags(tags)
    info["group_count"] = len(groups)
    # RemoveDuplicatedChunkRule only sees PipelineDataAction groups; count those only
    data_hashes = [group_hash(g) for g in groups if g and all(is_data(t) for t in g)]
    info["duplicate_groups"] = len(data_hashes) - len(set(data_hashes))
    info["last_tag_is_end"] = bool(tags) and is_end(tags[-1])
    aac_header_idx = next(
        (i for i, t in enumerate(tags) if t.tag_type == TAG_TYPE_AUDIO and is_header(t)), None)
    first_audio_data_idx = next((i for i, t in enumerate(tags) if is_audio_data(t)), None)
    info["aac_header_index"] = aac_header_idx
    info["first_audio_data_index"] = first_audio_data_idx
    info["aac_header_before_audio_data"] = (
        aac_header_idx is not None
        and first_audio_data_idx is not None
        and aac_header_idx < first_audio_data_idx)
    info["metadata"] = _metadata_facts(tags)
    return info


def _track_stats(tags: list[Tag]) -> dict:
    if not tags:
        return {"count": 0, "first": None, "last": None, "monotonic": True,
                "jumps": 0, "rollbacks": 0}
    rollbacks = 0
    jumps = 0
    for a, b in zip(tags, tags[1:]):
        d = b.timestamp - a.timestamp
        if d < 0:
            rollbacks += 1
            jumps += 1
        elif d > JUMP_THRESHOLD:
            jumps += 1
    return {
        "count": len(tags),
        "first": tags[0].timestamp,
        "last": tags[-1].timestamp,
        "monotonic": rollbacks == 0,
        "jumps": jumps,
        "rollbacks": rollbacks,
    }


def _count_filler_nalus(t: Tag) -> int:
    """Count H.264 filler NAL units (type 12) in a legacy AVC data tag."""
    if t.tag_type != TAG_TYPE_VIDEO or len(t.data) < 6:
        return 0
    if t.data[0] & 0x80 or (t.data[0] & 0x0F) != 7 or t.data[1] != 1:
        return 0
    pos = 5
    count = 0
    while pos + 4 <= len(t.data):
        n = u32(t.data, pos)
        if n == 0 or pos + 4 + n > len(t.data):
            return count
        if (t.data[pos + 4] & 0x1F) == 12:
            count += 1
        pos += 4 + n
    return count


def _metadata_facts(tags: list[Tag]) -> dict:
    for t in tags:
        if t.tag_type != TAG_TYPE_SCRIPT:
            continue
        try:
            values = parse_amf0_list(t.data)
        except ValueError:
            return {"present": True, "parsed": False}
        if len(values) >= 2 and values[0][0] == "string" and values[0][1] == "onMetaData":
            obj = values[1]
            if obj[0] in ("object", "ecma"):
                pairs = obj[1]
                duration = next((v for k, v in pairs if k == "duration"), None)
                return {
                    "present": True,
                    "parsed": True,
                    "duration_present": duration is not None,
                    "duration": duration[1] if duration and duration[0] == "number" else None,
                }
    return {"present": False, "parsed": False}


# ---------------------------------------------------------------------------
# AMF0 (only what the fixture mutations need)
# ---------------------------------------------------------------------------

def parse_amf0_value(b: bytes, pos: int) -> tuple[tuple, int]:
    if pos >= len(b):
        raise ValueError("amf0 eof")
    marker = b[pos]
    pos += 1
    if marker == 0x00:
        return ("number", struct.unpack_from(">d", b, pos)[0]), pos + 8
    if marker == 0x01:
        return ("boolean", bool(b[pos])), pos + 1
    if marker == 0x02:
        n = u16(b, pos)
        return ("string", b[pos + 2:pos + 2 + n].decode("utf-8", "replace")), pos + 2 + n
    if marker == 0x03:
        pairs, pos = _amf0_pairs(b, pos)
        return ("object", pairs), pos
    if marker == 0x05:
        return ("null", None), pos
    if marker == 0x06:
        return ("undefined", None), pos
    if marker == 0x07:
        return ("reference", u16(b, pos)), pos + 2
    if marker == 0x08:
        count = u32(b, pos)
        pos += 4
        pairs, pos = _amf0_pairs(b, pos)
        if count != len(pairs):
            # advisory count; keep parsing result, but flag mismatch through type info
            pass
        return ("ecma", pairs), pos
    if marker == 0x0A:
        count = u32(b, pos)
        pos += 4
        items = []
        for _ in range(count):
            v, pos = parse_amf0_value(b, pos)
            items.append(v)
        return ("strict_array", items), pos
    if marker == 0x0B:
        millis = struct.unpack_from(">d", b, pos)[0]
        offset = struct.unpack_from(">h", b, pos + 8)[0]
        return ("date", (millis, offset)), pos + 10
    if marker == 0x0C:
        n = u32(b, pos)
        return ("long_string", b[pos + 4:pos + 4 + n].decode("utf-8", "replace")), pos + 4 + n
    raise ValueError(f"unsupported amf0 marker 0x{marker:02x} at {pos - 1}")


def _amf0_pairs(b: bytes, pos: int) -> tuple[list[tuple[str, tuple]], int]:
    pairs: list[tuple[str, tuple]] = []
    while True:
        if pos + 2 > len(b):
            raise ValueError("amf0 object eof")
        n = u16(b, pos)
        pos += 2
        if n == 0:
            if pos >= len(b) or b[pos] != 0x09:
                raise ValueError("amf0 missing object end marker")
            return pairs, pos + 1
        key = b[pos:pos + n].decode("utf-8", "replace")
        pos += n
        value, pos = parse_amf0_value(b, pos)
        pairs.append((key, value))


def parse_amf0_list(b: bytes) -> list[tuple]:
    values = []
    pos = 0
    while pos < len(b):
        value, pos = parse_amf0_value(b, pos)
        values.append(value)
    return values


def _amf0_skip(b: bytes, pos: int) -> int:
    _, pos = parse_amf0_value(b, pos)
    return pos


def remove_metadata_duration(body: bytes) -> bytes:
    """Remove the `duration` key from the onMetaData ECMA array / object.

    Rewrites the ECMA-array count when present. Raises ValueError if not onMetaData or
    the key is absent.
    """
    if not body:
        raise ValueError("empty script body")
    pos = 0
    marker = body[pos]
    if marker != 0x02:
        raise ValueError("first amf0 value is not a string")
    pos += 1
    n = u16(body, pos)
    name = body[pos + 2:pos + 2 + n].decode("utf-8", "replace")
    pos += 2 + n
    if name != "onMetaData":
        raise ValueError(f"not onMetaData: {name!r}")
    marker = body[pos]
    pos += 1
    count_pos: int | None = None
    if marker == 0x08:
        count_pos = pos
        pos += 4
    elif marker != 0x03:
        raise ValueError(f"metadata value is not object/ecma array (0x{marker:02x})")
    head_end = pos  # pairs start here
    pairs: list[tuple[bytes, int, int]] = []
    while True:
        if pos + 2 > len(body):
            raise ValueError("script body eof")
        n = u16(body, pos)
        if n == 0:
            if pos + 3 > len(body) or body[pos + 2] != 0x09:
                raise ValueError("missing object end marker")
            end_marker = pos
            break
        ks = pos
        key = body[pos + 2:pos + 2 + n]
        pos += 2 + n
        ve = _amf0_skip(body, pos)
        pairs.append((key, ks, ve))
        pos = ve
    kept = [p for p in pairs if p[0] != b"duration"]
    if len(kept) == len(pairs):
        raise ValueError("duration key not found")
    out = bytearray(body[:head_end])
    for _, ks, ve in kept:
        out += body[ks:ve]
    out += b"\x00\x00\x09"
    if count_pos is not None:
        struct.pack_into(">I", out, count_pos, len(kept))
    return bytes(out)


# ---------------------------------------------------------------------------
# C#-equivalent grouping (TagGroupReader; docs/PORTING.md §5)
# ---------------------------------------------------------------------------

def _pick_rule(t: Tag) -> str | None:
    if t.tag_type == TAG_TYPE_SCRIPT:
        return "script"
    if is_end(t):
        return "end"
    if is_header(t):
        return "header"
    if is_data(t):
        return "data"
    return None


def _can_append(rule: str, t: Tag, group: list[Tag]) -> bool:
    if rule == "script":
        return False
    if rule == "end":
        return False
    if rule == "header":
        return is_header(t)
    # data rule (docs/PORTING.md §5)
    if is_non_keyframe_data(t) and (not group or t.timestamp - group[-1].timestamp < 24999):
        return True
    if t.tag_type == TAG_TYPE_VIDEO and is_keyframe_data(t) and all(
            x.tag_type == TAG_TYPE_AUDIO for x in group):
        return True
    if t.tag_type == TAG_TYPE_AUDIO and is_header(t) and all(
            x.tag_type != TAG_TYPE_AUDIO or is_header(x) for x in group):
        return True
    return False


def group_tags(tags: list[Tag]) -> list[list[Tag]]:
    groups: list[list[Tag]] = []
    i = 0
    while i < len(tags):
        rule = _pick_rule(tags[i])
        group = [tags[i]]
        i += 1
        if rule is not None:
            while i < len(tags) and _can_append(rule, tags[i], group):
                group.append(tags[i])
                i += 1
        groups.append(group)
    return groups


def group_hash(group: list[Tag]) -> str:
    """Reference-equivalent group fingerprint: count + per-tag payload hash, no timestamps."""
    h = hashlib.sha256()
    h.update(be32(len(group)))
    for t in group:
        h.update(hashlib.sha256(t.data).digest())
    return h.hexdigest()


# ---------------------------------------------------------------------------
# mutations R1-R8
# ---------------------------------------------------------------------------

def _first_index(tags: list[Tag], pred) -> int:
    for i, t in enumerate(tags):
        if pred(t):
            return i
    raise ValueError("no matching tag")


def _find_tag_at_or_after(tags: list[Tag], target_ts: int, pred=None) -> int:
    for i, t in enumerate(tags):
        if t.timestamp >= target_ts and (pred is None or pred(t)):
            return i
    return len(tags)


def mutate_r1(tags: list[Tag]) -> tuple[list[Tag], dict]:
    idx = _first_index(tags, lambda t: is_data(t) and t.timestamp >= 8000)
    jump = 2000
    out = list(tags)
    for i in range(idx, len(out)):
        out[i] = replace(out[i], timestamp=out[i].timestamp + jump)
    return out, {
        "kind": "timestamp_jump_forward",
        "at_tag_index": idx,
        "at_timestamp_ms": tags[idx].timestamp,
        "delta_ms": jump,
        "rule": "UpdateTimestampJumpRule (JUMP_THRESHOLD=500)",
    }


def mutate_r2(tags: list[Tag]) -> tuple[list[Tag], dict]:
    idx = _first_index(tags, lambda t: is_data(t) and t.timestamp >= 10000)
    delta = -3000
    out = list(tags)
    for i in range(idx, len(out)):
        out[i] = replace(out[i], timestamp=out[i].timestamp + delta)
    return out, {
        "kind": "timestamp_rollback",
        "at_tag_index": idx,
        "at_timestamp_ms": tags[idx].timestamp,
        "delta_ms": delta,
        "rule": "UpdateTimestampJumpRule (negative diff < -500)",
    }


def mutate_r3(tags: list[Tag]) -> tuple[list[Tag], dict]:
    groups = group_tags(tags)
    # choose a whole data group in the middle of the stream (4 s..15 s)
    chosen = None
    for g in groups:
        if g and all(is_data(t) for t in g) and g[0].timestamp >= 4000 and g[-1].timestamp <= 15000:
            chosen = g
            break
    if chosen is None:
        raise ValueError("no suitable data group for R3")
    first = tags.index(chosen[0])
    out = tags[:first + len(chosen)] + list(chosen) + tags[first + len(chosen):]
    return out, {
        "kind": "duplicated_tag_group",
        "group_start_tag_index": first,
        "group_start_timestamp_ms": chosen[0].timestamp,
        "group_end_timestamp_ms": chosen[-1].timestamp,
        "group_tag_count": len(chosen),
        "rule": "RemoveDuplicatedChunkRule (MAX_HISTORY=16, whole-group drop)",
    }


FILLER_TARGETS_MS = [2000, 4000, 6000, 8000, 10000, 12000]
FILLER_EXTRA_PER_TAG = 2


def mutate_r4(tags: list[Tag]) -> tuple[list[Tag], dict]:
    out = list(tags)
    filler_tags = 0
    # 1) append H.264 filler NALUs to AVC data tags
    for target in FILLER_TARGETS_MS:
        idx = None
        for i, t in enumerate(out):
            if (t.tag_type == TAG_TYPE_VIDEO and is_data(t) and abs(t.timestamp - target) <= 100
                    and len(t.data) >= 6 and not (t.data[0] & 0x80)
                    and (t.data[0] & 0x0F) == 7 and t.data[1] == 1):
                idx = i
                break
        if idx is None:
            continue
        body = bytearray(out[idx].data)
        for _ in range(FILLER_EXTRA_PER_TAG):
            filler = bytes([0x0C]) + b"\xff" * 6  # nal_unit_type 12, 7 bytes incl. header
            body += be32(len(filler)) + filler
        out[idx] = replace(out[idx], data=bytes(body))
        filler_tags += 1
    # 2) inject two zero-size data tags
    zero_count = 0
    for target, ttype in ((6500, TAG_TYPE_VIDEO), (13500, TAG_TYPE_AUDIO)):
        idx = _find_tag_at_or_after(out, target)
        out = out[:idx] + [Tag(ttype, target, b"")] + out[idx:]
        zero_count += 1
    # 3) inject two AVC end-of-sequence tags (legacy layout, AVCPacketType=2 => TagFlag.End)
    end_count = 0
    for target in (12000, 16000):
        idx = _find_tag_at_or_after(out, target)
        out = out[:idx] + [Tag(TAG_TYPE_VIDEO, target, bytes([0x27, 0x02, 0x00, 0x00, 0x00]))] + out[idx:]
        end_count += 1
    return out, {
        "kind": "filler_and_end_tags",
        "filler_tags_modified": filler_tags,
        "filler_nalus_added": filler_tags * FILLER_EXTRA_PER_TAG,
        "zero_size_tags_added": zero_count,
        "end_tags_added": end_count,
        "rule": "RemoveFillerDataRule + HandleEndTagRule + zero-size drop",
    }


def mutate_r5(tags: list[Tag]) -> tuple[list[Tag], dict]:
    header_idx = _first_index(tags, lambda t: t.tag_type == TAG_TYPE_AUDIO and is_header(t))
    header = tags[header_idx]
    out = tags[:header_idx] + tags[header_idx + 1:]
    target_idx = _first_index(out, lambda t: is_audio_data(t) and t.timestamp >= 2500)
    late_ts = out[target_idx].timestamp
    moved = replace(header, timestamp=late_ts)
    out = out[:target_idx] + [moved] + out[target_idx:]
    return out, {
        "kind": "delayed_audio_sequence_header",
        "original_header_tag_index": header_idx,
        "late_header_timestamp_ms": late_ts,
        "audio_data_tags_before_header": sum(1 for t in out[:target_idx] if is_audio_data(t)),
        "rule": "HandleDelayedAudioHeaderRule (intent: header before audio data)",
    }


def mutate_r6(tags: list[Tag]) -> tuple[list[Tag], dict]:
    script_idx = _first_index(tags, lambda t: t.tag_type == TAG_TYPE_SCRIPT)
    old_body = tags[script_idx].data
    new_body = remove_metadata_duration(old_body)
    out = list(tags)
    out[script_idx] = replace(out[script_idx], data=new_body)
    return out, {
        "kind": "metadata_duration_missing_and_bad_flags",
        "script_tag_index": script_idx,
        "removed_key": "duration",
        "original_script_body_bytes": len(old_body),
        "new_script_body_bytes": len(new_body),
        "header_flags_written": 0x00,
        "rule": "HandleNewScriptRule + writer metadata duration fix + header flags",
    }


def mutate_r7(tags: list[Tag], flags: int) -> tuple[bytes, dict]:
    idx = _first_index(tags, lambda t: is_data(t) and t.timestamp >= 10000)
    garbage = b"\xff" * 4 + b"GARBAGE!" + b"\xff" * 8  # 4 + 8 + 8 = 20 bytes
    prefix = encode_flv(flags, tags[:idx])
    rest = b"".join(tag_bytes(t) for t in tags[idx:])
    data = prefix + garbage + rest
    # Drop the terminal AVC end-of-sequence tag plus the tail half of the last media tag,
    # leaving a partial tag body (no trailing PreviousTagSize) at EOF.
    end_tag = tags[-1]
    media_tag = tags[-2]
    cut = (4 + TAG_HEADER_SIZE + len(end_tag.data)) + (TAG_HEADER_SIZE + len(media_tag.data) // 2)
    data = data[:-cut]
    return data, {
        "kind": "garbage_between_tags_and_truncated_tail",
        "garbage_bytes": len(garbage),
        "garbage_after_tag_index": idx - 1,
        "garbage_at_timestamp_ms": tags[idx - 1].timestamp,
        "truncated_tail_bytes": cut,
        "truncated_tag_index": len(tags) - 2,
        "dropped_tags": [len(tags) - 1, len(tags) - 2],
        "complete_tag_count": len(tags) - 2,
        "rule": "parser resync (§3.6) + drop partial tail",
    }


def mutate_r8(tags: list[Tag]) -> tuple[list[Tag], dict]:
    # 1) AAC rollback: -1200 ms on every audio tag from ~6 s onwards
    rollback_idx = _first_index(tags, lambda t: t.tag_type == TAG_TYPE_AUDIO and t.timestamp >= 6000)
    out = list(tags)
    for i in range(rollback_idx, len(out)):
        if out[i].tag_type == TAG_TYPE_AUDIO:
            out[i] = replace(out[i], timestamp=out[i].timestamp - 1200)
    # 2) duplicate one complete data group (contains AAC tags, so the duplicate is both an
    #    R3-class group repeat and an R8-class AAC timestamp duplication)
    groups = group_tags(out)
    chosen = None
    for grp in groups:
        if grp and all(is_data(t) for t in grp) and 14000 <= grp[0].timestamp <= 16000:
            chosen = grp
            break
    if chosen is None:
        raise ValueError("no suitable data group for R8 duplication")
    first = out.index(chosen[0])
    out = out[:first + len(chosen)] + list(chosen) + out[first + len(chosen):]
    return out, {
        "kind": "hevc_aac_timestamp_rollback_and_duplicate_group",
        "rollback_start_tag_index": rollback_idx,
        "rollback_ms": -1200,
        "duplicate_group_start_tag_index": first,
        "duplicate_group_start_timestamp_ms": chosen[0].timestamp,
        "duplicate_group_tag_count": len(chosen),
        "duplicate_group_audio_tags": sum(1 for t in chosen if t.tag_type == TAG_TYPE_AUDIO),
        "rule": "UpdateTimestampJumpRule + RemoveDuplicatedChunkRule (group contains AAC)",
    }


# ---------------------------------------------------------------------------
# expectation builder
# ---------------------------------------------------------------------------

def _track_expect() -> dict:
    return {
        "monotonic": True,
        "negative": False,
        "first_ms_min": 0,
        "first_ms_max": 500,
    }


def build_expect(cls: str, name: str, data: bytes, analysis: dict,
                 corruption: dict, extra: dict, base_total_tags: int,
                 base_duration_ms: int) -> dict:
    total = analysis["tag_count"]
    duration_min, duration_max = 19000, 21000
    tag_count_min, tag_count_max = total - 4, total + 4
    expected = {
        "class": cls,
        "valid_flv": True,
        "header_flags": FLV_HEADER_FLAGS,
        "video_codec": analysis["video_codec"] or "avc",
        "audio_codec": "aac",
        "timestamps": {
            "video": _track_expect(),
            "audio": _track_expect(),
        },
        "duration_ms": {"min": duration_min, "max": duration_max},
        "tag_count": {"min": tag_count_min, "max": tag_count_max},
        "output_bytes": {"min": 0, "max": len(data) + 4096},
        "preserved": {
            "onmetadata": analysis["metadata"]["present"],
            "video_sequence_header": analysis["video_sequence_headers"] >= 1,
            "audio_sequence_header": analysis["audio_sequence_headers"] >= 1,
        },
        "anomalies": {"required": [cls], "min_count": 1},
        "repair_command": f"flvrepair file --input fixtures/{name} --output out.flv --mode best-effort",
    }

    if cls == "R1":
        expected["duration_ms"] = {"min": 19000, "max": 21000}
        expected["tag_count"] = {"min": base_total_tags - 2, "max": base_total_tags + 2}
        expected["jump_repaired"] = True
        expected["input_raw_last_timestamp_ms"] = analysis["raw_last_timestamp_ms"]
    elif cls == "R2":
        expected["tag_count"] = {"min": base_total_tags - 2, "max": base_total_tags + 2}
        expected["rollback_repaired"] = True
        expected["input_raw_last_timestamp_ms"] = analysis["raw_last_timestamp_ms"]
    elif cls == "R3":
        expected["tag_count"] = {"min": base_total_tags - 2, "max": base_total_tags + 2}
        expected["duplicate_groups_removed"] = True
        expected["duplicated_group_min_tags"] = corruption["group_tag_count"] - 2
        expected["duration_ms"] = {"min": base_duration_ms - 1000, "max": base_duration_ms + 1000}
    elif cls == "R4":
        # base keeps its terminal AVC end-of-sequence tag; the 4 injected junk tags are dropped
        expected["tag_count"] = {"min": base_total_tags - 2, "max": base_total_tags + 2}
        expected["filler_nalus_removed"] = True
        expected["zero_size_tags_removed"] = True
        expected["midstream_end_tags_removed"] = True
        expected["preserved"]["terminal_end_tag"] = True
        expected["duration_ms"] = {"min": base_duration_ms - 1000, "max": base_duration_ms + 1000}
    elif cls == "R5":
        expected["tag_count"] = {"min": base_total_tags - 2, "max": base_total_tags + 2}
        expected["aac_header_before_audio_data"] = True
        expected["aac_sequence_header_count"] = {"min": 1, "max": 1}
        expected["duration_ms"] = {"min": base_duration_ms - 1000, "max": base_duration_ms + 1000}
    elif cls == "R6":
        expected["tag_count"] = {"min": base_total_tags - 2, "max": base_total_tags + 2}
        expected["metadata_duration"] = {
            "input": "missing",
            "file_mode": {"min": 18500, "max": 25000, "may_be_absent": True},
            "stream_mode": "unspecified",
            "note": "if duration is present it must agree with the media duration within the range",
        }
        expected["duration_ms"] = {"min": base_duration_ms - 1000, "max": base_duration_ms + 1000}
    elif cls == "R7":
        complete = corruption["complete_tag_count"]
        last_ts = analysis["raw_last_timestamp_ms"]
        expected["tag_count"] = {"min": complete - 2, "max": complete + 2}
        expected["truncated_tail_dropped"] = True
        expected["resync"] = True
        expected["duration_ms"] = {"min": max(0, last_ts - 800), "max": last_ts + 800}
    elif cls == "R8":
        expected["tag_count"] = {"min": base_total_tags - 2, "max": base_total_tags + 2}
        expected["video_codec"] = "hevc"
        expected["duplicate_groups_removed"] = True
        expected["aac_duplicates_removed"] = True
        expected["duplicated_group_min_tags"] = corruption["duplicate_group_tag_count"] - 2
        expected["rollback_repaired"] = True
        expected["duration_ms"] = {"min": 18500, "max": 22500}

    expected.update(extra)
    expected["notes"] = _notes_for(cls)
    return expected


def _notes_for(cls: str) -> list[str]:
    notes = {
        "R1": ["input has a +2000 ms jump at the fixture boundary; output timestamps must stay "
               "continuous and end near the original duration"],
        "R2": ["input rolls back 3000 ms; output must be monotonic non-decreasing per track"],
        "R3": ["a whole tag group is duplicated verbatim; the duplicate group must be dropped, "
               "not merely re-timestamped"],
        "R4": ["H.264 filler NALUs (type 12) must be removed from video payloads; zero-size tags "
               "and mid-stream AVC end-of-sequence (packet type 2) tags must be dropped. A "
               "terminal END tag is preserved so clean input stays byte-identical"],
        "R5": ["the AAC sequence header is moved after audio data; output must place it before the "
               "first audio data tag"],
        "R6": ["duration is missing from onMetaData and the FLV header flags are 0x00; output "
               "flags must be 0x05 (audio+video). file mode should fix duration when possible"],
        "R7": ["20 garbage bytes sit between tags and the final two tags (terminal END tag + half of "
               "the last audio tag) are truncated; output must contain only complete tags "
               "(resync + drop partial tail), no parser abort"],
        "R8": ["HEVC enhanced-FLV + AAC; audio rolls back 1200 ms at ~6 s and one complete data "
               "group (containing AAC tags) is duplicated at ~15 s. Output keeps hevc/aac sequence "
               "headers, is monotonic and drops the duplicate group"],
    }
    return notes.get(cls, [])


# ---------------------------------------------------------------------------
# base generation
# ---------------------------------------------------------------------------

BASE_CMDS = {
    "clean.flv": [
        FFMPEG, "-hide_banner", "-loglevel", "error", "-y",
        "-f", "lavfi", "-i", f"testsrc2=size={BASE_SIZE}:rate={BASE_FPS}",
        "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
        "-t", str(BASE_SECONDS), "-map", "0:v", "-map", "1:a",
        "-c:v", "libx264", "-preset", "veryfast", "-b:v", BASE_VIDEO_BITRATE,
        "-pix_fmt", "yuv420p", "-g", str(BASE_FPS),
        "-x264-params", "threads=1:sliced-threads=0",
        "-c:a", "aac", "-b:a", BASE_AUDIO_BITRATE, "-ar", "48000", "-ac", "2",
        "-fflags", "+bitexact", "-flags", "+bitexact", "-map_metadata", "-1",
        "-threads", "1", "-f", "flv",
    ],
    "clean_hevc.flv": [
        FFMPEG, "-hide_banner", "-loglevel", "error", "-y",
        "-f", "lavfi", "-i", f"testsrc2=size={BASE_SIZE}:rate={BASE_FPS}",
        "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
        "-t", str(BASE_SECONDS), "-map", "0:v", "-map", "1:a",
        "-c:v", "libx265", "-preset", "ultrafast", "-b:v", "400k",
        "-pix_fmt", "yuv420p", "-g", str(BASE_FPS),
        "-x265-params", "log-level=error:frame-threads=1:pools=1",
        "-c:a", "aac", "-b:a", BASE_AUDIO_BITRATE, "-ar", "48000", "-ac", "2",
        "-fflags", "+bitexact", "-flags", "+bitexact", "-map_metadata", "-1",
        "-threads", "1", "-f", "flv",
    ],
}


def ffmpeg_version() -> str:
    try:
        out = run([FFMPEG, "-version"]).splitlines()
        return out[0] if out else "unknown"
    except (OSError, SystemExit):
        return "unavailable"


def ensure_base(name: str, force: bool, manifest: dict | None) -> tuple[bytes, dict]:
    path = BASE_DIR / name
    entry = {"size": None, "sha256": None}
    if path.exists() and not force:
        data = path.read_bytes()
        if manifest and name in manifest.get("base", {}):
            recorded = manifest["base"][name]
            if recorded.get("sha256") != sha256(data):
                sys.stderr.write(
                    f"warning: {name} content differs from gen/manifest.json; "
                    f"run --force to regenerate consistently\n")
    else:
        BASE_DIR.mkdir(parents=True, exist_ok=True)
        cmd = BASE_CMDS[name] + [str(path)]
        print(f"[gen] ffmpeg -> {path.relative_to(FLV_DIR)}")
        run(cmd)
        data = path.read_bytes()
    if len(data) == 0:
        raise SystemExit(f"generated base is empty: {path}")
    # verify the base is structurally sound and has the expected tracks/order
    flv = parse_flv(data)
    if flv.header_flags != FLV_HEADER_FLAGS:
        raise SystemExit(f"{name}: unexpected header flags {flv.header_flags:#04x}")
    if not flv.tags or flv.tags[0].tag_type != TAG_TYPE_SCRIPT:
        raise SystemExit(f"{name}: expected script tag first")
    if not any(t.tag_type == TAG_TYPE_VIDEO and is_header(t) for t in flv.tags):
        raise SystemExit(f"{name}: no video sequence header")
    if not any(t.tag_type == TAG_TYPE_AUDIO and is_header(t) for t in flv.tags):
        raise SystemExit(f"{name}: no AAC sequence header")
    warn_if_nondeterministic(flv.tags)
    entry = {"size": len(data), "sha256": sha256(data)}
    return data, entry


def warn_if_nondeterministic(tags: list[Tag]) -> None:
    """Sanity check the generated base: monotonic, starts with keyframe."""
    for ttype in (TAG_TYPE_VIDEO, TAG_TYPE_AUDIO):
        last = None
        for t in tags:
            if t.tag_type != ttype:
                continue
            if last is not None and t.timestamp < last:
                raise SystemExit(f"generated base is non-monotonic for tag type {ttype}")
            last = t.timestamp
    video = [t for t in tags
             if t.tag_type == TAG_TYPE_VIDEO and is_data(t) and not _is_enhanced_metadata(t)]
    if not video or not is_keyframe_data(video[0]):
        raise SystemExit("generated base does not start with a video keyframe")


# ---------------------------------------------------------------------------
# assembly
# ---------------------------------------------------------------------------

def fixture_bytes_and_expect(cls: str, name: str, base_data: bytes) -> tuple[bytes, dict]:
    base = parse_flv(base_data)
    base_total = len(base.tags)
    base_duration = max(t.timestamp for t in base.tags if t.tag_type in
                        (TAG_TYPE_AUDIO, TAG_TYPE_VIDEO))
    flags = base.header_flags
    tags = list(base.tags)

    if cls == "R1":
        tags, corruption = mutate_r1(tags)
        data = encode_flv(flags, tags)
    elif cls == "R2":
        tags, corruption = mutate_r2(tags)
        data = encode_flv(flags, tags)
    elif cls == "R3":
        tags, corruption = mutate_r3(tags)
        data = encode_flv(flags, tags)
    elif cls == "R4":
        tags, corruption = mutate_r4(tags)
        data = encode_flv(flags, tags)
    elif cls == "R5":
        tags, corruption = mutate_r5(tags)
        data = encode_flv(flags, tags)
    elif cls == "R6":
        tags, corruption = mutate_r6(tags)
        flags = 0x00  # wrong header flags on the wire
        data = encode_flv(flags, tags)
    elif cls == "R7":
        data, corruption = mutate_r7(tags, flags)
        tags = None  # type: ignore[assignment]
    elif cls == "R8":
        tags, corruption = mutate_r8(tags)
        data = encode_flv(flags, tags)
    else:  # pragma: no cover
        raise AssertionError(cls)

    analysis = analyze_input(data)
    extra: dict = {}
    expect = build_expect(cls, name, data, analysis, corruption, extra,
                          base_total, base_duration)
    doc = {
        "schema": 1,
        "class": cls,
        "fixture": name,
        "fixture_bytes": len(data),
        "fixture_sha256": sha256(data),
        "base": "clean_hevc.flv" if cls == "R8" else "clean.flv",
        "corruption": corruption,
        "input_analysis": analysis,
        "expect": expect,
    }
    # invariant sanity: the corruption must be detectable in the input
    _assert_corruption_present(cls, analysis, corruption)
    return data, doc


def _assert_corruption_present(cls: str, analysis: dict, corruption: dict) -> None:
    if cls == "R1":
        assert analysis["timestamps"]["video"]["jumps"] >= 1 or \
            analysis["timestamps"]["audio"]["jumps"] >= 1, "R1: no forward jump detected"
    elif cls == "R2":
        assert analysis["timestamps"]["video"]["rollbacks"] >= 1 or \
            analysis["timestamps"]["audio"]["rollbacks"] >= 1, "R2: no rollback detected"
    elif cls == "R3":
        assert analysis["duplicate_groups"] >= 1, "R3: no duplicate group detected"
    elif cls == "R4":
        assert analysis["zero_size_data_tags"] >= 2, "R4: zero-size tags missing"
        assert analysis["end_tag_count"] >= 2, "R4: end tags missing"
        assert analysis["filler_nalu_count"] >= 1, "R4: filler NALUs missing"
    elif cls == "R5":
        assert analysis["aac_header_before_audio_data"] is False, \
            "R5: header still before audio data"
    elif cls == "R6":
        assert analysis["header_flags"] == 0, "R6: header flags not corrupted"
        assert analysis["metadata"]["duration_present"] is False, \
            "R6: duration still present"
    elif cls == "R7":
        assert analysis["truncated_tail_bytes"] > 0, "R7: no truncated tail"
        assert analysis["resync_events"] >= 1, "R7: garbage did not need resync"
        assert analysis["valid_flv_structure"] is False
    elif cls == "R8":
        assert analysis["video_codec"] == "hevc", "R8: base is not HEVC"
        assert analysis["timestamps"]["audio"]["rollbacks"] >= 1, "R8: no audio rollback"
        assert analysis["duplicate_groups"] >= 1, "R8: duplicate group missing"


def build_all(base_avc: bytes, base_hevc: bytes) -> dict[str, tuple[bytes, dict]]:
    out: dict[str, tuple[bytes, dict]] = {}
    for cls, name in FIXTURES:
        base = base_hevc if cls == "R8" else base_avc
        data, doc = fixture_bytes_and_expect(cls, name, base)
        out[name] = (data, doc)
    return out


# ---------------------------------------------------------------------------
# manifest
# ---------------------------------------------------------------------------

def make_manifest(base_entries: dict, built: dict[str, tuple[bytes, dict]]) -> dict:
    fixtures = {}
    total = 0
    for cls, name in FIXTURES:
        data, doc = built[name]
        fixtures[name] = {
            "class": cls,
            "size": len(data),
            "sha256": sha256(data),
            "expect_sha256": sha256(json.dumps(doc, indent=2, sort_keys=False).encode() + b"\n"),
        }
        total += len(data)
    return {
        "schema": 1,
        "generator": "testdata/flv/gen/gen.py",
        "ffmpeg_version": ffmpeg_version(),
        "base_seconds": BASE_SECONDS,
        "base_size": BASE_SIZE,
        "base_fps": BASE_FPS,
        "video_bitrate": BASE_VIDEO_BITRATE,
        "audio_bitrate": BASE_AUDIO_BITRATE,
        "base": base_entries,
        "fixtures": fixtures,
        "total_fixture_bytes": total,
        "max_allowed_fixture_bytes": MAX_FIXTURE_BYTES_TOTAL,
        "notes": [
            "Fixtures are derived deterministically from the committed base FLVs:",
            "re-running gen.py replays the pure-Python mutations and must reproduce these hashes.",
            "Base FLVs are generated by ffmpeg; re-run with --force if the toolchain changes.",
        ],
    }


# ---------------------------------------------------------------------------
# commands
# ---------------------------------------------------------------------------

def write_json(path: Path, doc: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    text = json.dumps(doc, indent=2, ensure_ascii=False) + "\n"
    path.write_text(text, encoding="utf-8")


def load_manifest() -> dict | None:
    if not MANIFEST.exists():
        return None
    try:
        return json.loads(MANIFEST.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None


def cmd_generate(force: bool) -> int:
    manifest_old = load_manifest()
    if not shutil.which(FFMPEG):
        sys.stderr.write(
            f"error: {FFMPEG!r} not found on PATH (needed to create the base FLVs; "
            f"use --check to verify existing fixtures offline)\n")
        return 2
    base_entries = {}
    base_data = {}
    for name in BASE_CMDS:
        data, entry = ensure_base(name, force, manifest_old)
        base_data[name] = data
        base_entries[name] = entry
    built = build_all(base_data["clean.flv"], base_data["clean_hevc.flv"])
    total = sum(len(d) for d, _ in built.values())
    if total >= MAX_FIXTURE_BYTES_TOTAL:
        sys.stderr.write(f"error: fixtures total {total} bytes >= {MAX_FIXTURE_BYTES_TOTAL}\n")
        return 1
    FIX_DIR.mkdir(parents=True, exist_ok=True)
    manifest = make_manifest(base_entries, built)
    for cls, name in FIXTURES:
        data, doc = built[name]
        (FIX_DIR / name).write_bytes(data)
        write_json(FIX_DIR / name.replace(".flv", ".expect.json"), doc)
    write_json(MANIFEST, manifest)
    print(f"[gen] wrote {len(FIXTURES)} fixtures, total {total} bytes "
          f"({total / 1024 / 1024:.2f} MiB), cap {MAX_FIXTURE_BYTES_TOTAL // 1024 // 1024} MiB")
    for cls, name in FIXTURES:
        data, doc = built[name]
        a = doc["input_analysis"]
        print(f"  {cls} {name:32s} {len(data):>9d} B  tags={a['tag_count']:>5d}  "
              f"raw_last_ts={a['raw_last_timestamp_ms']:>7d}  "
              f"flags=0x{a['header_flags']:02x}")
    print(f"[gen] manifest: {MANIFEST.relative_to(FLV_DIR.parent.parent)}")
    return 0


def _verify_fixture_docs(name: str, data: bytes, doc: dict) -> list[str]:
    problems: list[str] = []
    expected_path = FIX_DIR / name.replace(".flv", ".expect.json")
    if not expected_path.exists():
        return [f"missing {expected_path.name}"]
    try:
        on_disk = json.loads(expected_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        return [f"{expected_path.name}: invalid json: {exc}"]
    if on_disk != doc:
        problems.append(f"{expected_path.name}: content differs from regenerated expectation")
    if on_disk.get("fixture_sha256") != sha256(data):
        problems.append(f"{expected_path.name}: fixture_sha256 mismatch")
    # structural checks on the generated input
    analysis = analyze_input(data)
    if analysis["sha256"] != doc["input_analysis"]["sha256"]:
        problems.append(f"{name}: input analysis sha256 mismatch")
    return problems


def cmd_check() -> int:
    manifest = load_manifest()
    if manifest is None:
        sys.stderr.write("error: gen/manifest.json missing; run gen.py first\n")
        return 1
    problems: list[str] = []
    # 1) base files match the manifest
    base_data = {}
    for name, recorded in manifest.get("base", {}).items():
        path = BASE_DIR / name
        if not path.exists():
            problems.append(f"missing base file {path.relative_to(FLV_DIR)}")
            continue
        data = path.read_bytes()
        base_data[name] = data
        if len(data) != recorded.get("size"):
            problems.append(f"{name}: size {len(data)} != manifest {recorded.get('size')}")
        if sha256(data) != recorded.get("sha256"):
            problems.append(f"{name}: sha256 differs from manifest (run --force to regenerate)")
        try:
            parse_flv(data)
        except ValueError as exc:
            problems.append(f"{name}: not a valid clean FLV: {exc}")
    if problems:
        for p in problems:
            sys.stderr.write(f"FAIL {p}\n")
        return 1
    if "clean.flv" not in base_data or "clean_hevc.flv" not in base_data:
        sys.stderr.write("error: manifest does not list both base files\n")
        return 1
    # 2) replay mutations and compare bytes + expect.json + manifest hashes
    built = build_all(base_data["clean.flv"], base_data["clean_hevc.flv"])
    for cls, name in FIXTURES:
        data, doc = built[name]
        path = FIX_DIR / name
        if not path.exists():
            problems.append(f"missing fixture {path.name}")
            continue
        on_disk = path.read_bytes()
        if on_disk != data:
            problems.append(f"{name}: bytes differ from deterministic replay "
                            f"(disk {len(on_disk)} B, replay {len(data)} B)")
        recorded = manifest.get("fixtures", {}).get(name)
        if not recorded:
            problems.append(f"{name}: missing from manifest")
        else:
            if recorded.get("size") != len(data):
                problems.append(f"{name}: size != manifest")
            if recorded.get("sha256") != sha256(data):
                problems.append(f"{name}: sha256 != manifest")
        problems.extend(_verify_fixture_docs(name, data, doc))
    # 3) size cap
    total = sum(len(built[n][0]) for _, n in FIXTURES)
    if total >= MAX_FIXTURE_BYTES_TOTAL:
        problems.append(f"fixtures total {total} B >= {MAX_FIXTURE_BYTES_TOTAL} B")
    if problems:
        for p in problems:
            sys.stderr.write(f"FAIL {p}\n")
        return 1
    print(f"[check] OK: 2 bases, {len(FIXTURES)} fixtures, {total} bytes "
          f"({total / 1024 / 1024:.2f} MiB)")
    for cls, name in FIXTURES:
        data, _ = built[name]
        print(f"  {cls} {name:32s} {len(data):>9d} B sha256={sha256(data)[:16]}…")
    return 0


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Generate/verify FLV repair fixtures R1-R8 (stdlib only).")
    parser.add_argument("--check", action="store_true",
                        help="verify fixtures/expect/manifest against deterministic replay "
                             "(offline; no ffmpeg needed)")
    parser.add_argument("--force", action="store_true",
                        help="regenerate the base FLVs with ffmpeg even if present")
    args = parser.parse_args(argv)
    if args.check:
        return cmd_check()
    return cmd_generate(args.force)


if __name__ == "__main__":
    raise SystemExit(main())
