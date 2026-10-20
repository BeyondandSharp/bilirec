# FLV repair fixtures (`testdata/flv`)

Deterministic, ffmpeg-generated FLV corruption fixtures for the Rust `flvrepair` sidecar
(`rust/crates/flv-repair`). Owner: `port-spec`. Contract: `docs/INTERFACES.md` §1.3 (repair
guarantees R1–R8) and §4 (fixtures). Behavior spec: `docs/PORTING.md`.

The fixtures are **inputs plus invariant expectations**, never byte-exact repaired goldens:
each `<class>.flv` is a corrupted FLV stream and `<class>.expect.json` states what a correct
repair must guarantee (valid FLV, per-track monotonic timestamps, expected duration range,
expected anomaly class/count). This lets the Rust implementation evolve without re-blessing
goldens.

## Layout

```
testdata/flv/
├── README.md                    # this file
├── base/
│   ├── clean.flv                # clean base: H.264 + AAC, 640x360@30, 20 s (~1.5 MB)
│   └── clean_hevc.flv           # clean base: HEVC (Enhanced FLV) + AAC, 20 s (~1.3 MB)
├── fixtures/
│   ├── r1_timestamp_jump.flv    # R1 ... R8 inputs
│   └── ...
│   ├── r1_timestamp_jump.expect.json
│   └── ...
└── gen/
    ├── gen.py                   # stdlib-only generator + `--check`
    └── manifest.json            # toolchain, base/fixture sizes + sha256
```

Total fixtures: **11.65 MiB** (cap: 20 MiB), 8 fixtures + 2 bases = **14.36 MiB** on disk.

## Regeneration

Requirements: `python3` (stdlib only), `ffmpeg`/`ffprobe` on `PATH` (generation only).

```bash
# verify the committed fixtures without ffmpeg (deterministic replay + hashes + corruption checks)
python3 testdata/flv/gen/gen.py --check

# create the base FLVs if missing, (re)generate all fixtures + expect.json + manifest.json
python3 testdata/flv/gen/gen.py

# force-regenerate the base FLVs with ffmpeg (e.g. after a toolchain change)
python3 testdata/flv/gen/gen.py --force
```

`--check` is the CI-friendly check: it re-runs the pure-Python mutations from `base/` in memory,
compares the bytes and every `.expect.json` against disk, verifies the recorded hashes, and
re-asserts that each corruption is actually present (jump/rollback/duplicate group/filler/
truncation/…). It needs no ffmpeg. Put it in `make test`/CI if desired.

### Determinism

- Base generation uses fixed lavfi sources and single-threaded x264/x265 (`-x264-params
  "threads=1:sliced-threads=0"`, `-x265-params "log-level=error:frame-threads=1:pools=1"`,
  `-fflags +bitexact -flags +bitexact -map_metadata -1`). Two runs produced identical sha256 on
  ffmpeg 7.1.5.
- All mutations are pure Python over the base tag list: same base bytes → identical fixtures.
- `gen/manifest.json` records the ffmpeg version, base hashes and fixture hashes. If the base
  hash changes (different ffmpeg/x264/x265 build), `--check` fails; run `--force`, review the
  fixture sizes/analyses, and re-run the Rust tests (behavior invariants should be unchanged).
- Every `.expect.json` carries `fixture_sha256` and `fixture_bytes`, so tests can detect a
  silently modified fixture.

## Fixture classes

Command shape used by tests:

```bash
./target/release/flvrepair file --input testdata/flv/fixtures/r2_timestamp_rollback.flv \
    --output /tmp/r2.fixed.flv --mode best-effort
./target/release/flvrepair analyze --input /tmp/r2.fixed.flv --json
```

| class | fixture | size | tags | raw last ts (ms) | expected output |
|---|---|---|---|---|---|
| R1 | `r1_timestamp_jump.flv` | 1,537,568 | 1,543 | 22,056 (jump +2000 at ~8 s) | dur 19000–21000 ms, tags 1541–1545, `R1≥1` |
| R2 | `r2_timestamp_rollback.flv` | 1,537,568 | 1,543 | 17,056 (rollback −3000 at ~10 s) | dur 19000–21000 ms, tags 1541–1545, `R2≥1` |
| R3 | `r3_duplicated_chunk.flv` | 1,611,437 | 1,620 | 20,056 (77-tag group duplicated at 4 s) | duplicate group dropped, dur 19056–21056 ms, tags 1541–1545, `R3≥1` |
| R4 | `r4_filler_data.flv` | 1,537,770 | 1,547 | 20,056 (12 filler NALUs, 2 zero-size tags, 2 mid-stream END tags) | filler/zero/mid-stream-END dropped, terminal END kept, tags 1541–1545, `R4≥1` |
| R5 | `r5_delayed_audio_header.flv` | 1,537,568 | 1,543 | 20,056 (AAC header moved after 2.5 s of audio data) | AAC header emitted before first audio data, dur 19056–21056 ms, tags 1541–1545, `R5≥1` |
| R6 | `r6_bad_metadata.flv` | 1,537,549 | 1,543 | 20,056 (`duration` removed, header flags `0x00`) | flags fixed to `0x05`, metadata preserved (duration optional/consistent), tags 1541–1545, `R6≥1` |
| R7 | `r7_truncated_tail.flv` | 1,537,436 | 1,541 | 20,035 (20 garbage bytes at ~10 s; terminal END + half of last audio tag truncated) | complete tags only (last 2 dropped), dur 19235–20835 ms, tags 1539–1543, `R7≥1` |
| R8 | `r8_hevc_aac.flv` | 1,377,070 | 1,620 | 19,967 (HEVC base + AAC −1200 ms rollback at ~6 s + one 77-tag data group duplicated at ~15 s) | hevc+aac headers preserved, monotonic, duplicate group dropped, tags 1541–1545, `R8≥1` |

The clean bases must satisfy INTERFACES §1.3: output byte-identical is allowed, must not grow by
>1 %, must not add/remove media tags. `clean.flv` ends with an AVC end-of-sequence tag, so a
correct sidecar preserves that terminal END tag (mid-stream END tags are dropped, see
`docs/PORTING.md` §8.8).

### What each corruption is

- **R1**: every tag from the first data tag at ≥8 s is shifted +2000 ms. Raw input stays
  monotonic but has a >500 ms forward jump; output must keep continuity and end near 20 s.
- **R2**: every tag from ≥10 s is shifted −3000 ms. Raw audio and video become non-monotonic;
  output must be monotonic non-decreasing per track with no negative timestamps.
- **R3**: one complete C#-grouping group (keyframe + following audio/video tags, 4.000–4.995 s,
  77 tags) is duplicated verbatim immediately after itself. Repair must drop the whole duplicate
  group (group fingerprint ignores timestamps, like the reference `RemoveDuplicatedChunkRule`).
- **R4**: twelve H.264 filler NAL units (type 12) are appended to six AVC payloads; two zero-size
  tags (one video, one audio) and two mid-stream AVC end-of-sequence tags (packet type 2) are
  inserted at 6.5/12/13.5/16 s. The terminal END tag of the base stays.
- **R5**: the AAC sequence header (originally tag 2, ts 0) is removed and re-inserted just before
  the first audio data tag at ts ≈ 2520 ms, so 116 audio data tags precede the header.
- **R6**: the `duration` key is removed from the `onMetaData` ECMA array (count field
  decremented, script tag size updated) and the FLV header flags byte is set to `0x00` while both
  tracks are present.
- **R7**: 20 bytes (`ff ff ff ff` + `GARBAGE!` + 8 × `ff`) are spliced between tags at ~10 s; the
  terminal AVC END tag and the second half of the last audio tag are cut off mid-body, leaving a
  partial tag without its trailing `PreviousTagSize`.
- **R8**: an HEVC (Enhanced FLV `hvc1`) + AAC base; all audio tags from ~6 s are shifted
  −1200 ms, and one complete data group near 15 s (which contains AAC tags) is duplicated
  verbatim, so the duplicate is both an R3-style group repeat and an R8 AAC-timestamp repeat.

### Validating an output

Use the fixture's `.expect.json` as the test oracle. Recommended checks for
`flvrepair file/stream` output:

1. `valid_flv`: parse output strictly (FLV header, correct `PreviousTagSize` chain, no trailing
   partial tag).
2. `header_flags == expect.header_flags` (0x05) and `expect.video_codec/audio_codec` preserved.
3. per-track DTS non-decreasing, non-negative, first timestamp in `[first_ms_min, first_ms_max]`.
4. `duration_ms` = max last media timestamp within the given range.
5. total tag count within `tag_count` range; for R3/R4/R8 also assert the structural drops
   (`duplicate_groups_removed`, `filler_nalus_removed`, …).
6. `anomalies` from `flvrepair analyze --json` **on the corrupted input fixture** (not the
   repaired output): the class in `required` with count ≥ `min_count`; extra classes are
   allowed. On the repaired output anomalies are not asserted (best-effort may still report
   what it fixed), only the `expect.*` output invariants above apply.
7. `preserved.onmetadata/video_sequence_header/audio_sequence_header` and R5
   `aac_header_before_audio_data`, R6 `metadata_duration.file_mode` policy.

`ffprobe` is **not** a sufficient oracle for these fixtures: it reports the container metadata
`duration` (20.067 s) even for R1/R2, and it does not expose timestamp rollbacks. Use
`flvrepair analyze` + the invariants above.

## `.expect.json` schema (schema v1)

```jsonc
{
  "schema": 1,
  "class": "R2",
  "fixture": "r2_timestamp_rollback.flv",
  "fixture_bytes": 1537568,
  "fixture_sha256": "…",
  "base": "clean.flv",                 // clean_hevc.flv for R8
  "corruption": { "kind": "…", "rule": "…", … },   // how the fixture was made
  "input_analysis": {                 // raw scan of the corrupted input (see below)
    "valid_flv_structure": false,     // R7 only; true for the rest
    "header_flags": 5,
    "tag_count": 1543, "tag_counts": {"video":…, "audio":…, "script":…},
    "timestamps": {"video": {"first":…,"last":…,"monotonic":…,"jumps":…,"rollbacks":…}, "audio": {…}},
    "raw_last_timestamp_ms": 17056,
    "resync_events": 0, "resynced_bytes": 0, "truncated_tail_bytes": 0,
    "duplicate_groups": 0,            // duplicated *data* groups (reference rule scope)
    "adjacent_duplicate_tags": 0,
    "zero_size_data_tags": 0, "end_tag_count": 1, "filler_nalu_count": 0,
    "aac_header_before_audio_data": true,
    "metadata": {"present": true, "parsed": true, "duration_present": true, "duration": 20.067}
  },
  "expect": {
    "class": "R2",
    "valid_flv": true,
    "header_flags": 5,
    "video_codec": "avc", "audio_codec": "aac",
    "timestamps": {"video": {"monotonic": true, "negative": false,
                              "first_ms_min": 0, "first_ms_max": 500}, "audio": {…}},
    "duration_ms": {"min": 19000, "max": 21000},
    "tag_count": {"min": 1541, "max": 1545},
    "output_bytes": {"min": 0, "max": 1541664},
    "preserved": {"onmetadata": true, "video_sequence_header": true, "audio_sequence_header": true},
    "anomalies": {"required": ["R2"], "min_count": 1},
    "repair_command": "flvrepair file --input fixtures/r2_timestamp_rollback.flv --output out.flv --mode best-effort",
    "rollback_repaired": true,
    "input_raw_last_timestamp_ms": 17056,
    "notes": ["…"]
  }
}
```

Class-specific `expect` additions: R1 `jump_repaired`, R3 `duplicate_groups_removed` +
`duplicated_group_min_tags`, R4 `filler_nalus_removed`/`zero_size_tags_removed`/
`midstream_end_tags_removed` + `preserved.terminal_end_tag`, R5 `aac_header_before_audio_data` +
`aac_sequence_header_count`, R6 `metadata_duration` policy, R7 `truncated_tail_dropped` +
`resync`, R8 `aac_duplicates_removed` + `rollback_repaired` + `video_codec: "hevc"`.

Ranges rather than exact numbers are intentional: best-effort repair may adjust timestamps by a
few milliseconds depending on grouping decisions; the ranges were validated against a Python
simulation of the `docs/PORTING.md` pipeline (jump rule + duplicate-group drop + structural
drops) and are wide enough for reasonable implementations.

## How the fixture was generated (details)

Base command (H.264/AAC; HEVC variant changes the encoder and bitrate only):

```bash
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i testsrc2=size=640x360:rate=30 \
  -f lavfi -i sine=frequency=440:sample_rate=48000 \
  -t 20 -map 0:v -map 1:a \
  -c:v libx264 -preset veryfast -b:v 500k -pix_fmt yuv420p -g 30 \
  -x264-params "threads=1:sliced-threads=0" \
  -c:a aac -b:a 96k -ar 48000 -ac 2 \
  -fflags +bitexact -flags +bitexact -map_metadata -1 -threads 1 -f flv clean.flv
```

`gen.py` then parses the base into `(type, timestamp, payload)` tags, applies the class mutation
at tag/byte level, and re-serializes the FLV with correct `PreviousTagSize` fields (`R7` splices
raw garbage bytes and truncates the tail directly). Both clean bases are ~20.067 s and have
1543 tags:

- `clean.flv` (AVC): 1 script tag + 602 video tags (1 AVC sequence header, 600 data tags,
  1 terminal AVC end-of-sequence tag) + 940 audio tags (1 AAC sequence header, 939 data tags).
- `clean_hevc.flv` (HEVC): 1 script tag + 602 video tags (1 Enhanced-FLV HEVC sequence header,
  599 coded-frame tags, 1 Enhanced-FLV metadata packet `colorInfo`) + 940 audio tags
  (1 AAC sequence header, 939 data tags). The HEVC base has no terminal END tag.

See `input_analysis.tag_counts` / `input_analysis.timestamps` in each `.expect.json` for the
exact per-fixture numbers.

## Mutator limitations

- Synthetic single-file corruption only. It does not reproduce live-stream patterns such as
  repeated `onMetaData`, `@setDataFrame` wrappers, mid-stream sequence-header changes, or
  connection reconnects; those are covered by unit tests in the Rust crate (and by
  `docs/PORTING.md` §8.5/§8.6).
- R3/R8 duplicate a group aligned to the reference grouping rules; an implementation that groups
  differently must still drop it (Rust tests may hash the duplicate span directly). R8's
  duplicate group contains AAC tags (the reference reports R8 when an R3 duplicate group
  contains AAC).
- R5 models a delayed header by moving the real header tag. The C# `HandleDelayedAudioHeaderRule`
  is largely dormant with current grouping; the fixture tests the required *intent* (header
  before audio data), not C#-specific action order (see `docs/PORTING.md` §8.7).
- R4's zero-size and END tags are structurally valid FLV tags (correct `PreviousTagSize`) but
  semantically junk; the filler NALUs are appended to real AVC payloads.
- R6 tests missing `duration` + wrong header flags, not malformed AMF0. Malformed/truncated AMF0,
  unknown AMF0 markers and `@setDataFrame` should be covered by unit tests.
- R7's resync model matches `docs/PORTING.md` §3.6 (self-consistent trailing `PreviousTagSize`).
  Garbage bytes are chosen to be clearly non-tag-like; a parser that skips 1 byte at a time will
  resync, a parser that only looks for the next `FLV` magic will not.
- R8 uses **Enhanced FLV HEVC** (`0x80|…` header + `hvc1` FourCC), which is what ffmpeg 7.1
  writes. Legacy HEVC codec id 12 tags are not in the fixture; unit-test them synthetically per
  `docs/PORTING.md` §3.3.
- Bases are 640×360, ~500 kbps, CBR-ish, CQP-free; not a throughput/RSS benchmark (that is a
  separate 1080p smoke fixture/test, not part of this 20 MiB budget).
- Timestamps stay within 32-bit range; 24-bit timestamp wraparound (streams > 4.6 h) and >2^31 ms
  are not covered.
- No byte-exact repaired outputs by design (the contract asks for invariants).

## Evidence / quick verification

```bash
# generator consistency
python3 testdata/flv/gen/gen.py --check

# clean bases are valid and ~20 s
ffprobe -v error -show_entries format=duration -show_entries stream=codec_name,codec_type \
        -of default=nw=1 testdata/flv/base/clean.flv
ffprobe -v error -show_entries format=duration -show_entries stream=codec_name,codec_type \
        -of default=nw=1 testdata/flv/base/clean_hevc.flv

# fixture inventory + hashes
cat testdata/flv/gen/manifest.json
du -b testdata/flv/base/*.flv testdata/flv/fixtures/*.flv
```

Note: `gen.py` writes `gen/manifest.json` and `fixtures/*.expect.json`; do not hand-edit them —
regenerate instead. `gen.py` is the only writer of `testdata/flv/**` besides the README.

## V2 split-detector fixtures (owner: qa-harness, `docs/FEATURES-V2.md` §5.2)

Added for the Go streaming detectors `ScriptTagSplitDetector` / `AnnexBDetector`
(`pkg/flv`). These are **structural** fixtures for detector tests, not repair inputs and
not Rust `flvrepair` goldens. They are deterministic prefixes (~2 s) of `base/clean.flv`:

| fixture | structure | expected detector result |
|---|---|---|
| `fixtures/v2_clean_prefix.flv` | `onMetaData` Script tag only *before* any media tag | no `ErrScriptTagSplit`, no `ErrAnnexBSplit` |
| `fixtures/v2_script_tag_mid.flv` | control + a second Script tag (type 18, valid AMF0 `onMetaData` empty array) inserted immediately **after the first media data tag** | `ErrScriptTagSplit` once |
| `fixtures/v2_annexb_video.flv` | control + first AVC keyframe NALU payload: 4-byte length prefix at payload `[5:9]` replaced by `00 00 00 01`, plus `00 00 01` inside the NALU body | `ErrAnnexBSplit` once |

`fixtures/v2_detectors.manifest.json` records sizes, sha256 and the expected flags.
`gen/gen_v2_detectors.py` is stdlib-only and deterministic; it is the writer for these three
files (so the last sentence above applies to `gen.py`'s outputs, not these):

```bash
python3 testdata/flv/gen/gen_v2_detectors.py          # (re)write the three fixtures + manifest
python3 testdata/flv/gen/gen_v2_detectors.py --check  # verify committed bytes/hashes
```

Detector semantics pinned by the fixtures: a Script tag **before any media data tag** is normal
(initial `onMetaData`) and must not rotate; sequence-header tags (AVC/AAC config) do not count as
media data; the Annex-B fixture has a valid container structure with codec-level Annex-B NALUs
(ffprobe reports decode errors by design).
