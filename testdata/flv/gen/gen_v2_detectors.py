#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Generate synthetic FLV fixtures for the Go split detectors (FEATURES-V2 §5.2).

The Go recording pipeline gains two streaming detectors:

  * ScriptTagSplitDetector — after the first media data tag, a Script tag
    (FLV tag type 18) in the stream means the upstream re-emitted metadata and
    the segment should rotate (``ErrScriptTagSplit``).
  * AnnexBDetector — a video tag carrying Annex-B start codes
    (``00 00 01`` / ``00 00 00 01``) in its NALU payload means the upstream sent
    Annex-B instead of length-prefixed AVCC (``ErrAnnexBSplit``).

This generator derives small (prefix of ``base/clean.flv``) deterministic
fixtures for exercising those detectors:

  fixtures/v2_clean_prefix.flv         control: first media tags, one header
                                       Script tag before media, no triggers.
  fixtures/v2_script_tag_mid.flv       control + a second Script tag inserted
                                       after the first media data tag.
  fixtures/v2_annexb_video.flv         control + first AVC NALU payload's
                                       length prefix replaced by 00 00 00 01.

Nothing here is a repair golden; the files only pin detector-relevant structure.
The generator is stdlib-only and deterministic: same ``base/clean.flv`` bytes
produce identical fixtures (recorded in ``fixtures/v2_detectors.manifest.json``).

Usage:
    python3 testdata/flv/gen/gen_v2_detectors.py            # write fixtures
    python3 testdata/flv/gen/gen_v2_detectors.py --check    # verify committed
"""

from __future__ import annotations

import hashlib
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
FLV_ROOT = os.path.dirname(HERE)
BASE = os.path.join(FLV_ROOT, "base", "clean.flv")
FIXTURES = os.path.join(FLV_ROOT, "fixtures")
MANIFEST = os.path.join(FIXTURES, "v2_detectors.manifest.json")

PREFIX_MS = 2000          # keep ~2 s of media: small, fast detector tests
SCRIPT_TAG = 18
VIDEO_TAG = 9
AUDIO_TAG = 8

# Minimal valid AMF0 onMetaData with an empty ECMA array.
SCRIPT_PAYLOAD = b"\x02\x00\x0aonMetaData\x08\x00\x00\x00\x00"
ANNEXB_START_CODE = b"\x00\x00\x00\x01"


class Tag:
    __slots__ = ("type", "ts", "data")

    def __init__(self, tag_type: int, ts: int, data: bytes):
        self.type = tag_type
        self.ts = ts
        self.data = data

    def encode(self) -> bytes:
        size = len(self.data)
        out = bytearray()
        out.append(self.type)
        out += size.to_bytes(3, "big")
        out += (self.ts & 0xFFFFFF).to_bytes(3, "big")
        out.append((self.ts >> 24) & 0xFF)
        out += b"\x00\x00\x00"  # stream id
        out += self.data
        out += (11 + size).to_bytes(4, "big")
        return bytes(out)


def parse_tags(blob: bytes) -> tuple[bytes, list[Tag]]:
    if blob[:3] != b"FLV":
        raise SystemExit("base is not an FLV file: %s" % BASE)
    header = blob[:9]
    off = 9
    if len(blob) < off + 4:
        raise SystemExit("truncated FLV header")
    off += 4  # PreviousTagSize0
    tags: list[Tag] = []
    while off + 11 <= len(blob):
        tag_type = blob[off]
        size = int.from_bytes(blob[off + 1:off + 4], "big")
        ts = int.from_bytes(blob[off + 4:off + 7], "big")
        ts_ext = blob[off + 7]
        body_start = off + 11
        body_end = body_start + size
        if body_end + 4 > len(blob):
            break  # incomplete tail: not needed for prefix fixtures
        tags.append(Tag(tag_type, ts | (ts_ext << 24), blob[body_start:body_end]))
        off = body_end + 4
    return header, tags


def build(header: bytes, tags: list[Tag]) -> bytes:
    out = bytearray(header)
    out += b"\x00\x00\x00\x00"
    for tag in tags:
        out += tag.encode()
    return bytes(out)


def is_media_tag(tag: Tag) -> bool:
    """True for a media *data* tag, not a codec configuration header.

    AVC/HEVC sequence headers (``AVCPacketType == 0``) and the AAC sequence
    header (``AACPacketType == 0``) are configuration, not media data. The Go
    split detectors only start looking for Script/Annex-B anomalies after the
    first media data tag, so the fixture generator must use the same definition
    (otherwise an injected Script tag could land before any media data).
    """
    if tag.type == VIDEO_TAG:
        if len(tag.data) < 2:
            return False
        return tag.data[1] != 0
    if tag.type == AUDIO_TAG:
        if not tag.data:
            return False
        sound_format = tag.data[0] >> 4
        if sound_format == 10 and len(tag.data) >= 2:  # AAC: byte 1 = AACPacketType
            return tag.data[1] != 0
        return True
    return False


def make_prefix_control(header: bytes, tags: list[Tag]) -> tuple[bytes, int]:
    prefix = [tag for tag in tags if tag.ts <= PREFIX_MS]
    if not prefix:
        raise SystemExit("no tags in the first %d ms" % PREFIX_MS)
    first_script = prefix[0].type == SCRIPT_TAG
    if not first_script:
        raise SystemExit("expected onMetaData Script tag as first tag")
    if not any(is_media_tag(tag) for tag in prefix):
        raise SystemExit("prefix has no media data tag")
    return build(header, prefix), len(prefix)


def make_script_tag_mid(header: bytes, tags: list[Tag], prefix_len: int) -> bytes:
    prefix = tags[:prefix_len]
    media_index = next(i for i, tag in enumerate(prefix) if is_media_tag(tag))
    injected = Tag(SCRIPT_TAG, prefix[media_index].ts, SCRIPT_PAYLOAD)
    merged = prefix[:media_index + 1] + [injected] + prefix[media_index + 1:]
    return build(header, merged)


def make_annexb_video(header: bytes, tags: list[Tag], prefix_len: int) -> bytes:
    prefix = [Tag(t.type, t.ts, t.data) for t in tags[:prefix_len]]
    for tag in prefix:
        if tag.type != VIDEO_TAG or len(tag.data) < 9:
            continue
        if tag.data[1] != 1:          # AVCPacketType: 1 = NALU data
            continue
        if (tag.data[0] >> 4) != 1:   # frame type: 1 = keyframe
            continue
        # data[0:1] frame/type, [1] AVCPacketType, [2:5] composition time,
        # [5:9] first NALU length prefix -> replace with an Annex-B start code.
        patched = bytearray(tag.data)
        patched[5:9] = ANNEXB_START_CODE
        # Also place a 3-byte start code further inside the NALU payload so
        # detectors that scan beyond the first NALU still observe Annex-B.
        inner = 5 + 8 + 24
        if inner + 3 <= len(patched):
            patched[inner:inner + 3] = b"\x00\x00\x01"
        tag.data = bytes(patched)
        return build(header, prefix)
    raise SystemExit("no AVC keyframe NALU tag found in prefix")


def first_media_index(tags: list[Tag]) -> int:
    for idx, tag in enumerate(tags):
        if is_media_tag(tag):
            return idx
    return -1


def script_tags_after_media(tags: list[Tag]) -> int:
    """Number of Script tags that appear after the first media data tag."""
    idx = first_media_index(tags)
    if idx < 0:
        return 0
    return sum(1 for tag in tags[idx + 1:] if tag.type == SCRIPT_TAG)


def has_annexb_nalu_prefix(tags: list[Tag]) -> bool:
    return any(
        tag.type == VIDEO_TAG and len(tag.data) >= 9 and tag.data[5:9] == ANNEXB_START_CODE
        for tag in tags
    )


def verify(control: bytes, script_mid: bytes, annexb: bytes) -> None:
    """Fail the generator instead of shipping structurally misleading fixtures."""
    problems: list[str] = []
    _, control_tags = parse_tags(control)
    _, script_tags = parse_tags(script_mid)
    _, annexb_tags = parse_tags(annexb)

    if first_media_index(control_tags) < 0:
        problems.append("control has no media data tag")
    if script_tags_after_media(control_tags) != 0:
        problems.append("control must not have a Script tag after media data")
    if script_tags_after_media(script_tags) != 1:
        problems.append(
            "script fixture must have exactly one Script tag after the first "
            "media data tag, got %d" % script_tags_after_media(script_tags)
        )
    if has_annexb_nalu_prefix(control_tags) or has_annexb_nalu_prefix(script_tags):
        problems.append("control/script fixtures must not look like Annex-B")
    if not has_annexb_nalu_prefix(annexb_tags):
        problems.append("annexb fixture has no Annex-B NALU length prefix")
    if script_tags_after_media(annexb_tags) != 0:
        problems.append("annexb fixture must not have a Script tag after media data")
    if problems:
        raise SystemExit("fixture self-check failed: " + "; ".join(problems))


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def generate() -> tuple[dict[str, bytes], dict]:
    blob = open(BASE, "rb").read()
    header, tags = parse_tags(blob)
    control, prefix_len = make_prefix_control(header, tags)
    script_mid = make_script_tag_mid(header, tags, prefix_len)
    annexb = make_annexb_video(header, tags, prefix_len)
    verify(control, script_mid, annexb)
    fixtures = {
        "v2_clean_prefix.flv": control,
        "v2_script_tag_mid.flv": script_mid,
        "v2_annexb_video.flv": annexb,
    }
    manifest = {
        "schema": 1,
        "generator": "testdata/flv/gen/gen_v2_detectors.py",
        "purpose": "Go FLV split detectors (FEATURES-V2 §5.2): ScriptTagSplitDetector / AnnexBDetector",
        "base": {"path": "base/clean.flv", "size": len(blob), "sha256": sha256(blob)},
        "prefix_ms": PREFIX_MS,
        "prefix_tags": prefix_len,
        "fixtures": {
            name: {
                "size": len(data),
                "sha256": sha256(data),
                "expect": expect,
            }
            for (name, data), expect in zip(
                fixtures.items(),
                (
                    {"script_tag_after_first_media_tag": False, "annexb_start_codes_in_video_payload": False},
                    {"script_tag_after_first_media_tag": True, "annexb_start_codes_in_video_payload": False},
                    {"script_tag_after_first_media_tag": False, "annexb_start_codes_in_video_payload": True},
                ),
            )
        },
    }
    return fixtures, manifest


def main(argv: list[str]) -> int:
    check = "--check" in argv
    if not os.path.isfile(BASE):
        print("missing base fixture: %s" % BASE, file=sys.stderr)
        return 1
    fixtures, manifest = generate()
    if check:
        failed = 0
        for name, data in fixtures.items():
            path = os.path.join(FIXTURES, name)
            if not os.path.isfile(path):
                print("FAIL missing fixture %s" % path, file=sys.stderr)
                failed += 1
                continue
            disk = open(path, "rb").read()
            if disk != data:
                print("FAIL %s differs from regenerated bytes" % name, file=sys.stderr)
                failed += 1
            elif sha256(disk) != manifest["fixtures"][name]["sha256"]:
                print("FAIL %s sha256 mismatch" % name, file=sys.stderr)
                failed += 1
        if os.path.isfile(MANIFEST):
            disk_manifest = json.load(open(MANIFEST))
            if disk_manifest != manifest:
                print("FAIL manifest differs from regenerated manifest", file=sys.stderr)
                failed += 1
        else:
            print("FAIL missing manifest %s" % MANIFEST, file=sys.stderr)
            failed += 1
        if failed:
            return 1
        print("OK: v2 detector fixtures reproducible (%d files)" % len(fixtures))
        return 0

    os.makedirs(FIXTURES, exist_ok=True)
    for name, data in fixtures.items():
        path = os.path.join(FIXTURES, name)
        with open(path, "wb") as fh:
            fh.write(data)
        print("%8d  %s" % (len(data), path))
    with open(MANIFEST, "w") as fh:
        json.dump(manifest, fh, indent=2, sort_keys=True)
        fh.write("\n")
    print("wrote %s" % MANIFEST)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
