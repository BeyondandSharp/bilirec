# PORTING — BililiveRecorder.Flv → Rust `flv-repair`

Owner: `port-spec` (this document), `rust-flv` (keeps the module table current as the crate
lands). Contract: `docs/INTERFACES.md` §1. Reference sources are read-only at
`refs/BililiveRecorder` (GPL-3.0-only). The Rust crate is GPL-3.0-only; every file needs an
SPDX header and a short attribution comment naming the C# file it was derived from.

Read this together with:

- `refs/BililiveRecorder/BililiveRecorder.Flv/**` — behavior source of truth (rules, parser,
  writer, AMF).
- `refs/BililiveRecorder/BililiveRecorder.Core/Recording/StandardRecordTask.cs` — how the
  pipeline is assembled and driven.
- `testdata/flv/README.md` — fixtures R1–R8 and their invariants.

**Guiding principle:** behavioral parity with the C# rules is the default. Divergences are
allowed only where `docs/INTERFACES.md` requires a single continuous output stream instead of
"new recording file" segments, or requires robustness where the C# code throws. Every
divergence is listed in §10 and must be flagged in code comments.

**Implementation status (T2, `rust-flv`):** `rust/crates/flv-repair` implements the rule chain
below except `UpdateTimestampOffsetRule` (upstream-disabled; see §8.2/§10.14), with a per-track
monotonic clamp as a safety net (§8.1/§10.13). `testdata/flv` fixtures R1–R8 are green:
`cargo test` includes `tests/fixtures.rs`, which parses each `.expect.json` and asserts the
output invariants and the required `analyze` anomaly class; `flvrepair` preserves the clean
AVC/HEVC bases byte-identically in stream mode and does not add/remove media tags in file mode.
The module table in §1 reflects the shipped crate layout.

---

## 1. Source tree → proposed Rust modules

Crate root `rust/crates/flv-repair/src/`. The table reflects the current crate layout as
created by `rust-flv`; `rust-flv` owns final names and must keep this table current, but the
mapping must stay 1:1 enough that code review can trace behavior to the C# file. Several small
C# files collapse into one Rust module (e.g. all pipeline rules live in `pipeline.rs`), so use
the section references (§8.x) to locate behavior.

| C# file (refs/…/BililiveRecorder.Flv) | Rust module (`rust/crates/flv-repair/src/`) | Contents |
|---|---|---|
| `Tag.cs` | `tag.rs` | `Tag` model, `TagFlags`, `DataHash`, `UpdateExtraData` (composition time), `UpdateDataHash`; `flags_from_body` = Enhanced-FLV-aware classification (AVC/legacy HEVC/enhanced `hvc1`/`hev1`, metadata packets kept as data, §3.3) |
| `TagType.cs`, `TagFlag.cs` | `tag.rs` | `TagType` (Audio=8, Video=9, Script=18), `TagFlags` bitflags (Header=1, Keyframe=2, End=4) |
| `TagExtentions.cs` | `tag.rs`, `writer.rs` | `is_header`/`is_data`/`is_end`/`is_keyframe_data`, `encode_tag_into` (tag serialization) |
| `TagExtraData.cs` | `tag.rs` | composition time / final time (video only) |
| `H264Nalu.cs` | `codec.rs` (parse/strip), `parser.rs` (attach to tag) | length-prefixed AVC NALU iteration, NALU type; `strip_filler_nalus`; also AVC/HEVC SPS dimensions and AAC audio-specific-config parsing for `analyze` (Enhanced-FLV codec logic borrowed from `pkg/flv/video_tag.go`, MIT) |
| `Parser/FlvTagPipeReader.cs` | `parser.rs` | `TagReader`: FLV file header, incremental tag parse, flag classification, NALU/AMF attach, resync (§3.6) |
| `Parser/BigEndianBinaryReader.cs` | `parser.rs`, `amf.rs` | inlined big-endian helpers (no separate reader) |
| `Parser/FlvException.cs` | `parser.rs` (`ParseError`), `log.rs` | error enum (`NotFlv`, `UnknownTagType`, `Amf`, `Truncated`, `BufferLimit`) |
| `Grouping/TagGroupReader.cs` | `group.rs` | `GroupReader`: read first tag, pick rule, append while accepted, leftover tag |
| `Grouping/Rules/{Script,End,Header,Data}GroupingRule.cs` | `group.rs` | four grouping rules (`Group`, `Rule`, `can_append`), exact predicates in §5 |
| `IGroupingRule.cs`, `ITagGroupReader.cs`, `IFlvTagReader.cs` | `group.rs`, `parser.rs` | traits/impls |
| `Pipeline/FlvProcessingContext.cs` | `pipeline.rs` | action list, session state, comments/events |
| `Pipeline/Actions/*.cs` | `pipeline.rs` (`Action` enum) | Data/Header/Script/End/NewFile/Disconnect/LogMessage |
| `Pipeline/ProcessingPipelineSettings.cs` | `pipeline.rs` | `split_on_script_tag=false`, `disable_split_on_h264_annexb=false` |
| `Pipeline/ProcessingComment.cs`, `CommentType.cs` | `log.rs` (`class_event`), `pipeline.rs` | comment → stderr JSON event, class mapping (§6.3) |
| `Pipeline/ProcessingPipelineBuilder.cs`, `IProcessingPipelineBuilderExtensions.cs` | `pipeline.rs` (`RepairPipeline`) | fixed rule chain, exact order in §6.4 |
| `Pipeline/ISimpleProcessingRule.cs`, `IFullProcessingRule.cs`, `ProcessingDelegate.cs` | `pipeline.rs` | rule ordering implemented as the single chained pipeline |
| `Pipeline/Rules/UpdateTimestampJumpRule.cs` | `pipeline.rs` (`TimestampStore`, `calculate_new_target`) | §8.1 |
| `Pipeline/Rules/UpdateTimestampOffsetRule.cs` | **not ported** | upstream `AddDefaultRules` excludes it; R2/R8 covered by §8.1 + monotonic clamp. Algorithm kept in §8.2 for reference/tests |
| `Pipeline/Rules/RemoveDuplicatedChunkRule.cs` | `pipeline.rs` (`hash_group`) | §8.3 |
| `Pipeline/Rules/RemoveFillerDataRule.cs` | `codec.rs` (`strip_filler_nalus`), `pipeline.rs` | §8.4 |
| `Pipeline/Rules/HandleNewHeaderRule.cs` | `pipeline.rs` (`select_headers`/`select_one`, AnnexB state) | §8.5 |
| `Pipeline/Rules/HandleNewScriptRule.cs` | `pipeline.rs`, `amf.rs` | §8.6 |
| `Pipeline/Rules/HandleDelayedAudioHeaderRule.cs` | `pipeline.rs` (buffered header reorder) | §8.7 |
| `Pipeline/Rules/HandleEndTagRule.cs` | `pipeline.rs`, `writer.rs` (terminal END buffering) | §8.8 |
| `Pipeline/Rules/FfmpegDetectionRule.cs` | `analyze.rs` | §8.9 (AnalyzeHandler subset; analyzer-only, not part of the default chain) |
| `Amf/ScriptTagBody.cs` | `amf.rs` (`AmfValue`, `parse_script_body`, `write_script_body`) | AMF0 value list parse/serialize (§4) |
| `Amf/ScriptData*.cs` | `amf.rs` (`AmfValue` variants) | value enum + writers |
| `Amf/ScriptTagBodyExtensions.cs` | `amf.rs` (`Metadata`, `parse_metadata`, `metadata_duration`) | `get_metadata_value()` semantics |
| `Writer/FlvTagFileWriter.cs` | `writer.rs` (`FlvWriter`, sinks) | single-output FLV preamble (flags reflect tracks), tag serialization, in-place metadata duration rewrite; no keyframes index, no file rotation |
| `Pipeline/FlvProcessingContextWriter.cs` | `writer.rs` | action→bytes state machine: buffered pre-roll until both tracks' sequence headers are known (or the hold limit is hit), deferred/at-front header write, preamble order, terminal-END buffering |
| `Writer/KeyframesScriptDataValue.cs` | **not ported** (optional) | `keyframes` index; not required by INTERFACES, see §7.5 |
| `Writer/FlvTagListWriter.cs` | test-only | in-memory writer used by tests |
| (CLI / process contract, not in C# reference) | `cli.rs`, `log.rs` | INTERFACES §1.1/§1.2: args, exit codes, stderr JSON lines |

Not ported: `UpdateTimestampOffsetRule` (upstream-disabled default; §8.2/§10.14),
`KeyframesScriptDataValue` (optional; §7.5), `Xml/*`, `DefaultMemoryStreamProvider.cs`,
`StreamExtensions.cs`, `LinqExtensions.cs`/`LinqFunctions.cs` (iterate natively),
`DependencyInjectionExtensions.cs`, `FileClosedEventArgs.cs`, `IFlvProcessingContextWriter.cs`
(interface only). Recording-only machinery (`StatsRule`, `SplitRule`, file rotation) is not
needed because the sidecar writes a single stream (§10.1).

---

## 2. Tag model

```rust
pub enum TagType { Unknown = 0, Audio = 8, Video = 9, Script = 18 }

bitflags TagFlag { NONE = 0, HEADER = 1, KEYFRAME = 2, END = 4 }

pub struct Tag {
    pub tag_type: TagType,
    pub flag: TagFlag,
    pub index: i64,          // monotonically increasing parse index, starts at 1 (C# Interlocked.Increment)
    pub size: u32,           // tag body length as declared on the wire
    pub timestamp: i32,      // decoded 24+8 bit timestamp, reinterpreted as i32
    pub data: Bytes,         // raw tag body (for script/header/video data as needed)
    pub data_hash: Option<u64>,
    pub script_data: Option<ScriptTagBody>,
    pub nalus: Option<Vec<H264Nalu>>,
    pub extra: Option<TagExtraData>,
}
```

- `index` is used by tests/analysis only; repair logic does not depend on it.
- `data` must be retained for: script tags, header tags, and any tag whose payload is
  re-serialized (video tags with NALUs after filler removal). The C# reader keeps
  `BinaryData` when `skipData == false` or the tag is a Header; the sidecar should simply keep
  the body for all tags (bounded by `--max-buffer`).
- `TagExtraData` (video only): `FirstBytes = body[0..2]` hex; composition time = the 3-byte
  signed offset after the AVC/HEVC packet byte, sign-extended from 24 bits:
  `value = (body[2]<<16 | body[3]<<8 | body[4])`; if bit 23 set, `value |= !0xFFFFFF`
  (i.e. `value - 0x1000000`); `FinalTime = timestamp + composition_time`. Audio:
  `composition_time = i32::MIN` and omitted from XML.
- `Tag::is_header()` = `flag.contains(HEADER) && type in {Video, Audio}`.
  `is_data()` = `!flag.contains(HEADER|END) && type in {Video, Audio}`.
  `is_non_keyframe_data()` = `type in {Video, Audio} && flag == NONE`.
  `is_keyframe_data()` = `type == Video && flag == KEYFRAME` (exactly, not "contains").
  `is_end()` = `flag.contains(END)`.
- Clean-input requirement: for a clean stream every `data` action must be emitted with the same
  media tags in the same order, timestamps unchanged except as required by the rules.

---

## 3. Parser (`FlvTagPipeReader`)

### 3.1 File header

C# `ParseFileHeader` (9 bytes, strict):

1. `b[0..3] == "FLV"` and `b[3] == 1` else `NotFlvFileException("Data is not FLV.")`.
2. `b[5..9] == {0,0,0,9}` else `NotFlvFileException("Not Supported FLV format.")`.
3. Consume exactly 9 bytes. The next 4 bytes (`PreviousTagSize0`) are not validated; the tag
   loop unconditionally skips them as the "previous tag size" prefix of the first tag.

Rust divergence (R7): accept any big-endian data offset `>= 9` and skip
`data_offset - 9` bytes after the 9-byte header; do not fail on non-9 offsets. `flags` byte:
bit0 video, bit2 audio — record both, used by `analyze` and to validate/reconstruct R6.

### 3.2 Tag parse

Loop with an incremental buffer; require at least `4 + 11 + body_len` bytes to parse one tag.
For each tag, given `buf` starting at the 4-byte `PreviousTagSize` field:

| step | bytes | notes |
|---|---|---|
| skip previous tag size | 4 | value is **ignored** by C# (not validated). Keep the raw value for `analyze` warnings. |
| type | 1 | must be 8/9/18; otherwise C# throws `UnknownFlvTagTypeException` |
| data size | 3 | big-endian u24 → `size` |
| timestamp | 3 + 1 | `ts = b[4..7]` big-endian 24-bit is the **low** 24 bits; `b[7]` is the **high** 8 bits: `ts = (b[7] << 24) | (b[4] << 16) | (b[5] << 8) | b[6]`. Reinterpret the 32-bit result as `i32`. |
| stream id | 3 | ignored on parse; always written as `0,0,0` |
| body | `size` | may be 0 |

Bounds: `size` is u24, so max 16 MiB − 1 (0xFFFFFF); INTERFACES `--max-buffer` default 64 MiB is
the cap for buffering, not a tag limit. Reject/never allocate a body that has not fully arrived.
C# check is `buffer.Length < tagSize → need more data` (it does not validate the declared size
against a max; the u24 width does that).

### 3.3 Flag classification (exactly C# lines 229–270)

```
flag = NONE; is_avc = true
if body.len() > 2:
  match type:
    Audio:
      format = body[0] >> 4
      if format == 10 (AAC): if body[1] == 0 { flag = HEADER }
    Video:                                  // legacy AVC/HEVC layout only in C#
      frame = body[0]
      is_avc = (frame & 0x0F) == 7
      if (frame & 0xF0) == 0x10 { flag |= KEYFRAME }
      flag |= match body[1] { 0 => HEADER, 2 => END, _ => NONE }
```

If `body.len() <= 2`, no flags are set (zero/short tags stay data with `Flag::NONE`).

Rust extension for R8 (HEVC-in-FLV): the C# parser only understands the legacy layout
`frame_type<<4 | codec_id`. ffmpeg 7.x writes **Enhanced FLV** HEVC (`ExHeader` bit 7 set,
4-byte FourCC `hvc1`/`hev1`). Required classification (matches
`pkg/flv/video_tag.go::ClassifyVideoTag`, which is MIT and already proven in-tree):

```
if body[0] & 0x80 != 0:                    // Enhanced FLV
    if body.len() < 5: no flags
    frame_type  = (body[0] & 0x70) >> 4
    packet_type =  body[0] & 0x0F
    fourcc      = body[1..5]
    if fourcc in {hvc1, hev1}:
        kind = HEVC_HEVC_ENHANCED
        HEADER    if packet_type == 0 (SequenceStart)
        END       if packet_type == 2 (SequenceEnd)
    KEYFRAME if frame_type == 1
else: legacy path above; codec_id 7 => AVC, 12 => HEVC; header/end from body[1]
```

Keep `is_avc` semantics: attach NALUs only when video, not a header, and the codec is AVC
(legacy codec 7). Do not parse HEVC config as AVC NALUs.

Enhanced-FLV also carries **metadata packets** (`packet_type == 4`, e.g. ffmpeg's `colorInfo` /
`colorConfig` AMF payload; first byte like `0xd4 68 76 63 31 …` for `hvc1`). Treat these as
ordinary video **data** tags: not HEADER, not END, not KEYFRAME, no NALU parsing, and preserve
the tag/body unchanged. They are media tags for the clean-input invariant (ffmpeg HEVC bases
contain one at timestamp 0), so dropping them violates INTERFACES §1.3. `packet_type == 5`
(MPEG2-TS sequence start) should likewise be preserved as data.

### 3.4 NALUs (`H264Nalu.TryParseNalu`)

Only for AVC video data tags (`type == Video && !flag.contains(HEADER)` and legacy codec 7):

- Start at body offset **5** (1 byte frame/codec, 1 byte packet type, 3 bytes composition time).
- Loop while `pos < body.len()`:
  - read u32 BE `nalu_len`; read 1 byte `first`.
  - if `first & 0x80 != 0` → parse fails, `nalus = None` (do not partially trust).
  - `nalu_type = first & 0x1F`;
  - `H264Nalu { start_position = pos_of_first_byte, full_size = nalu_len, nalu_type }`;
    `start_position` points at the NALU header (after the 4-byte length prefix);
    `full_size` includes the 4-byte length prefix (writer re-writes `full_size` as the prefix).
  - advance `pos = start_position + nalu_len`.
- Any overrun/exception → `nalus = None` (tag falls back to raw body).
- `nalu_type` values used by rules: `SPS = 7`, `PPS = 8`, `EndOfSequence = 10`,
  `FillerData = 12` (see `H264NaluType` in `H264Nalu.cs`).

### 3.5 Body retention / script / NALUs

After flags:

1. `type == Script` → try `ScriptTagBody::parse(body)`; on error log `warn` event and keep the
   tag with `script_data = None`, **raw body preserved** (C# leaves ScriptData null and the
   writer later throws; we must not lose the tag — see §10.7).
2. Video data tag with `nalus` parsed → store NALUs.
3. Keep the raw body for all tags.

### 3.6 Truncation / resync (R7; divergence from C#)

C# treats a stream as finished when `PipeReader.ReadAsync().IsCompleted` and returns `null`
after emitting complete tags; a partial tail is silently dropped. It does **not** resync: an
unknown tag type throws and ends the session. The sidecar must:

- drop an incomplete trailing tag (no error) — this matches C# EOF behavior and INTERFACES
  §1.3 "Truncated input: emit all complete tags, drop the partial tail".
- on a malformed tag prefix (unknown type, or inconsistent boundaries), **resync** in
  best-effort mode: scan forward byte-by-byte (or over a bounded window, e.g. 64 KiB) for a
  candidate at `p` such that:
  - `p+15 <= buf.len()`,
  - `buf[p] ∈ {8,9,18}`, `size = u24(buf[p+1..p+4])`,
  - `p + 11 + size + 4 <= buf.len()` and, when the trailing field is available, the u32 at
    `p+11+size` equals `11 + size` (self-consistent `PreviousTagSize`).
  - Otherwise wait for more data; at EOF drop the remaining tail.
- In `--mode strict`, a resync or a truncated tail is an error (exit 3 after emitting what was
  produced, per INTERFACES §1.2).
- Log `{"level":"warn","event":"resync","skipped_bytes":N,"class":"R7"}` once per resync.

---

## 4. AMF0 (`ScriptTagBody`)

`ScriptTagBody` is a flat `Vec<Value>` parsed until EOF (no leading element count). Values:

| code | C# type | Rust `Value` variant | encoding |
|---|---|---|---|
| 0x00 | `ScriptDataNumber` | `Number(f64)` | 8-byte BE IEEE-754 |
| 0x01 | `ScriptDataBoolean` | `Boolean(bool)` | 1 byte |
| 0x02 | `ScriptDataString` | `String(String)` | u16 BE len + UTF-8 |
| 0x03 | `ScriptDataObject` | `Object(Vec<(String, Value)>)` | key/value pairs terminated by `00 00 09` |
| 0x04 | MovieClip | error | unsupported, parse error |
| 0x05 | `ScriptDataNull` | `Null` | — |
| 0x06 | `ScriptDataUndefined` | `Undefined` | — |
| 0x07 | `ScriptDataReference` | `Reference(u16)` | u16 BE |
| 0x08 | `ScriptDataEcmaArray` | `EcmaArray(Vec<(String, Value)>)` | u32 BE count (advisory only) then key/value pairs until `00 00 09` |
| 0x09 | ObjectEndMarker | n/a | only valid where a terminator is expected |
| 0x0A | `ScriptDataStrictArray` | `StrictArray(Vec<Value>)` | u32 BE count then values |
| 0x0B | `ScriptDataDate` | `Date { millis: f64, offset_min: i16 }` | f64 BE + i16 BE |
| 0x0C | `ScriptDataLongString` | `LongString(String)` | u32 BE len + UTF-8 |

String details (must match): u16/u32 length is **bytes**, content UTF-8; C# strips all `\0`
characters from parsed strings (`str.Replace("\0","")`) — replicate for parity so
`onMetaData` detection and comparisons behave identically.

Object/ECMA-array key encoding: u16 BE length + UTF-8; empty length means terminator, then
expect the `0x09` marker. ECMA array's count is **not trusted** — parse until terminator
(C# reads and discards it). Serializers must write the count = number of pairs; key order must
be preserved (C#'s `Dictionary` preserves insertion order on .NET; use `Vec`).

`ScriptTagBody::get_metadata_value()` = if `values.len() > 1`, match `values[1]`:
`Object|EcmaArray => Some`, else `None` (used by FfmpegDetection and the writer's duration fix).

Writer must re-serialize deterministically: no reordering, no re-encoding of numbers. For the
duration fix, in-place patch is preferred (see §7.4).

---

## 5. Grouping (`TagGroupReader`)

The pipeline consumes **tag groups**, not individual tags. Group selection (rules checked in
this exact order):

1. `ScriptGroupingRule`: start/append iff `tag.is_script()`. A group is exactly one script tag.
2. `EndGroupingRule`: start iff `tag.is_end()`. Never appends.
3. `HeaderGroupingRule`: start/append iff `tag.is_header()` (audio/video sequence headers).
4. `DataGroupingRule`: start iff `tag.is_data()`. Append iff **any** of:
   - `tag.is_non_keyframe_data()` AND (`tags` empty OR `tag.ts - last.ts < 24999`), **or**
   - `tag.type == Video && tag.is_keyframe_data() && all existing tags are Audio`, **or**
   - `tag.type == Audio && tag.is_header() && all existing tags are Audio => header`.
5. If no rule accepts the first tag → C# throws; Rust: log `warn` and skip the tag (best-effort),
   or resync.

Group construction: push the first tag, then read tags until the rule rejects one; the rejected
tag becomes `leftover` and starts the next group. Note `DataGroupingRule` clause 2 means a
keyframe video tag is appended only while the group has audio-only so far — this is how
`VideoKeyframe, Audio*, Video*` groups form; clause 1 lets non-keyframe tags join with
`< 25 s` gap (24 999 ms).

Rule interactions depend on this grouping; fixtures assume it. A group's first tag decides the
`UpdateTimestampJumpRule` "current timestamp" (§8.1).

---

## 6. Pipeline framework

### 6.1 Actions

```rust
enum Action {
    Data(Vec<Tag>),
    Header { all: Vec<Tag>, video: Option<Tag>, audio: Option<Tag> },
    Script(Tag),
    End(Tag),
    NewFile,                       // C#: PipelineNewFileAction.Instance
    Disconnect(String),            // C#: PipelineDisconnectAction
    LogMessage(String),            // C#: PipelineLogMessageWithLocationAction
}
```

- `Header.all` = the original header tags of the group; `video`/`audio` are the selected
  current headers (used by the writer on file-open; in the sidecar, on first output).
- `NewFile` means "close current recording file and open a new one". In a one-output-file
  sidecar it must **not** reset the output; see §10.1.

### 6.2 Context

```
Context { actions: Vec<Action>, session: HashMap<SessionKey, SessionValue>, local: HashMap<..>, comments: Vec<Comment> }
```

Session items persist for the whole stream (in C# they live in `StandardRecordTask.session`,
i.e. per recording task, across pipeline runs). Keys used (equivalent Rust constants):

| key (C#) | state |
|---|---|
| `Timestamp_Store_Key` | timestamp store (§8.1) |
| `DeDuplicationQueue` | `VecDeque<group_hash>` max 16 (§8.3) |
| `DuplicatedFlvDataCount` | consecutive duplicate groups (§8.3) |
| `HandleNewHeaderRule_VideoHeader` / `_AudioHeader` | last written header `Tag` clone (§8.5) |
| `HandleNewHeaderRule_AnnexB` | AnnexB state enum (§8.5) |
| `HandleNewScriptRule_MetaDataReceived` | bool first onMetaData seen (§8.6) |
| `--max-buffer` accounting | sidecar-only: buffered input/output bytes |

### 6.3 Comments → stderr JSON events

`ProcessingComment { type: CommentType, action_required: bool, comment: String }` with
`CommentType ∈ {Other, Logging, Unrepairable, TimestampJump, TimestampOffset, DecodingHeader,
RepeatingData, OnMetaData}`. C# only logs them (`logger.Debug`) — except
`PipelineDisconnectAction`, which stops the recording loop. The sidecar must map them to the
INTERFACES §1.2 stderr JSON-line contract, e.g.:

| CommentType / condition | event | class |
|---|---|---|
| `TimestampJump` with negative diff | `timestamp_rollback` | R2 |
| `TimestampJump` with positive diff > 500 | `timestamp_jump` | R1 |
| `RepeatingData` | `duplicate_group` | R3 |
| `TimestampOffset` (offset rule) | `timestamp_offset` | R2/R8 |
| `DecodingHeader` (new header / split) | `header_change` | R5/R6 |
| `OnMetaData` repeated | `duplicate_metadata` | R6 |
| filler NALUs / zero tags dropped | `filler_dropped` | R4 |
| `Unrepairable` | `unrepairable` (never fatal in best-effort) | R7/R8 |
| `Logging`, `Other` | `info` | — |

`action_required=false` comments are informational. `analyze` aggregates these into
`anomalies: [{class, count}]` (e.g. two R1 jumps → count 2; one file may carry several classes).
Do not emit logs on stdout.

### 6.4 Rule execution order (pre/post-order semantics)

The C# builder composes rules so that `Run(context, next)` is nested. Some rules transform
`context.actions` *before* calling `next` ("pre-order"), others call `next` first and then
transform ("post-order"). This matters for exact parity. Default chain (outermost first):

```
RemoveFillerDataRule            (post-order)
  HandleEndTagRule              (pre-order: yield action, then NewFile)
    HandleDelayedAudioHeaderRule(pre-order: header/data split)
      UpdateTimestampJumpRule   (post-order)
        HandleNewScriptRule     (pre-order: script rewrite/drop)
          HandleNewHeaderRule   (pre-order: header dedupe/change, NewFile)
            RemoveDuplicatedChunkRule (pre-order: drop duplicate group)
              (discard)
```

C# builds it from `AddDefaultRules()` + `AddRemoveFillerDataRule()`; recordings also prepend
`StatsRule` and `SplitRule` (not ported — sidecar has no file rotation). `AddDefaultRules` does
**not** include `UpdateTimestampOffsetRule` (commented out at
`IProcessingPipelineBuilderExtensions.cs:35`); `FfmpegDetectionRule` is only used by the
analyzer/tests.

Consequences to preserve:

- Header changes are materialized (`Header` action or dropped) **before** the timestamp rule
  sees the action list.
- Duplicate groups are dropped **before** the timestamp rule runs (it is post-order, so it
  observes the already-deduped `actions`), i.e. a replayed duplicate does not move the offset.
- Filler removal runs last, after timestamps were fixed, and re-serializes video payloads.
- A `Disconnect` action means the C# recording loop stops; the sidecar never aborts a stream for
  repair reasons (INTERFACES §2.1/§2.5) — log the event and continue (§10.2).

### 6.5 Driver loop

```
loop { group = reader.read_group()?; if group.none break;
       ctx.reset(group, session); pipeline(ctx); writer.write(ctx.actions) }
```

`NewFile` closes/reopens output in C#; `Disconnect` stops. `UpdateTimestampJumpRule` and
`RemoveFillerDataRule` rely on running **after** inner rules in the same `pipeline(ctx)` call.

---

## 7. Writer

### 7.1 File preamble

C# constant (`FlvTagFileWriter.FLV_FILE_HEADER`):

```
46 4C 56 01 05 00 00 00 09 00 00 00 00
F  L  V  ver flags data_offset=9   prevTagSize0=0
```

Flags are hard-coded `0x05` (audio+video) in C#. INTERFACES §1.3 requires flags to reflect
present tracks, so the sidecar writes `0x01 | (audio?0x04:0)` and also validates/fixes input
header flags for R6. See §10.6.

### 7.2 When the preamble is written (deferred open)

C# `FlvProcessingContextWriter`:

- `Script` and `Header` actions only stash `nextScriptTag` / `nextAudioHeaderTag` /
  `nextVideoHeaderTag` ("delay writing"). Nothing hits the file yet.
- The first `Data` or `End` action opens the file: create stream, write preamble, then write
  **script tag (timestamp 0)**, then **video header (timestamp 0)**, then **audio header
  (timestamp 0)**, then the action's tags. If no script tag arrived, C# synthesizes
  `onMetaData` with an empty `ScriptDataEcmaArray`; if a header is missing and
  `allowMissingHeader == false` C# throws (recording: allowMissingHeader derived from config).
  The sidecar is best-effort: synthesize metadata only if none was seen, and emit whatever
  headers exist.
- Header tags are always written with timestamp 0; script tags with timestamp 0.
- C# writer's `OverwriteMetadata` seeks to offset `9+4+11` and patches the serialized script
  body only when the new body length equals the previous one; otherwise logs a warning.

For the sidecar: to satisfy "correct header flags" while streaming, buffer the pre-roll until
the script tag (or first data) and all currently-known headers are available; see §10.6/§10.9.
`file` mode may do a two-pass (scan for streaming flags, then repair) which also enables the
in-place duration patch.

### 7.3 Tag serialization (`TagExtentions.WriteTo`)

```
[u8 type][u24 body_len][u24 ts_low][u8 ts_high][00 00 00][body][u32 11+body_len]
```

- Header/script tags are written with timestamp 0 regardless of `tag.timestamp` (C# writer
  passes 0 for headers; script serialization writes zeros).
- Data tags are written with the repaired timestamp. Timestamps must be non-negative and fit in
  32 bits (mask to 32 bits; C# writes the low 24 bits plus the 8-bit high byte).
- Body serialization:
  - Script: `script_data.serialize()`; if `script_data` is None, write the raw body
    (sidecar robustness; C# throws).
  - Video with `nalus = Some(list)`: write the first 5 bytes of the original body, then for each
    NALU `[u32 full_size][body[start_position .. start_position+full_size]]` (note C# copies
    `FullSize` bytes starting at `StartPosition`, which is the NALU header, i.e. `FullSize`
    includes the 4-byte prefix). This is how filler removal shrinks a tag.
  - Otherwise: raw body.
- `PreviousTagSize` after each tag = `11 + written_body_len`; the first preamble value is 0.

### 7.4 Metadata `duration` fix (R6)

Reference: `FlvProcessingContextWriter` sets `value["duration"] = last_data_tag.timestamp / 1000`
after writing each data group, and `FlvTagFileWriter::OverwriteMetadata` re-serializes the
script body; it patches in place only if the serialized length is unchanged.

Sidecar:

- `file` mode: after finishing, locate the output script tag, set `duration` = actual media
  duration in seconds (f64), re-serialize; if the serialized length matches the original,
  patch in place; otherwise leave the script tag as-is and log `warn` event
  `metadata_duration_not_rewritten`. (Same length is the common case: both are AMF0 Number.)
- `stream` mode: duration is not known while streaming; if the input had `duration <= 0`, keep
  the value as-is (or set to 0) and log `metadata_duration_unknown`. Do not buffer the whole
  stream just to compute it.
- If `duration` is absent (R6 fixture), add it only in `file` mode if the script body length can
  stay compatible; otherwise leave absent and log. The invariant in fixtures: metadata
  `duration`, when present, must not contradict the media duration by more than the stated
  tolerance.

### 7.5 `keyframes` metadata (optional, not required)

C# optionally adds a `keyframes` object (`times`, `filepositions`, `spacer`; `MaxDataCount =
6300`, `MinInterval = 1900` ms; spacer = NaN array padded to 2*6300 entries). INTERFACES does
not require it. Do not implement in v1 unless free; document if added.

---

## 8. Rule specifications

Notation: all timestamps are `i32` milliseconds; internal deltas are computed in `i64` to avoid
overflow. "session" = §6.2. Rules operate on the `actions` list of one pipeline run.

### 8.1 `UpdateTimestampJumpRule` — fix timestamp jumps (R1, R2, R5/R6 header ts, R8)

Post-order: call `next()` first, then process `context.actions` in order (non-data actions may
also be modified). State (`Timestamp_Store_Key`):

```
TimestampStore { first_chunk: bool = true,
                 next_timestamp_target: i32 = 0,
                 last_original: i32 = 0,
                 current_offset: i32 = 0 }
reset() => { first_chunk=true; next_target=0; last_original=0; current_offset=0 }
```

Constants: `JUMP_THRESHOLD = 500` ms; per-track duration fallback/min/max:
audio `22 / 20 / 24`, video `33 / 15 / 50`.

For each action:

1. `Data(tags)`:
   - `current = tags[0].timestamp`; `is_first_chunk = store.first_chunk`.
   - If `is_first_chunk`: `store.first_chunk = false`; `current = min(tag.timestamp for tag in tags)`.
   - `diff = current - store.last_original`.
   - If `diff < -(500)` **or** (`is_first_chunk` and `diff < 0`): emit `TimestampJump`
     comment (message "时间戳变小, curr: {current}, diff: {diff}"), then
     `store.current_offset = current - store.next_timestamp_target`.
   - Else if `diff > 500`: emit `TimestampJump` comment ("时间戳间隔过大, …"), then
     `store.current_offset = current - store.next_timestamp_target`.
   - `store.last_original = tags.last().timestamp` (**original**, before offset).
   - For every tag: `tag.timestamp -= store.current_offset` (wrapping-safe; clamp to >= 0?
     C# does not clamp — do not clamp, but the fixture invariants require no negative
     timestamps in output; first-chunk min offset guarantees 0 for a sane first group).
   - `store.next_timestamp_target = calculate_new_target(tags)` (using **adjusted**
     timestamps).
2. `End(tag)`: `diff = tag.timestamp - store.last_original`; if `diff < 0 || diff > 500` →
   `tag.timestamp = store.next_timestamp_target`; else `tag.timestamp -= store.current_offset`.
   (C# comment: "End Tag really doesn't matter"; the sidecar drops End actions, §10.3.)
3. `NewFile`: `store.reset()`. (Sidecar: swallow the action; do **not** reset in single-file
   mode unless the caller asked for per-file semantics. For the sidecar's single output, keep
   monotonic timestamps, so do not reset here. Log if it matters.)
4. `Script(tag)`: `tag.timestamp = store.next_timestamp_target` (C# comment says script tag ts
   used to be forced to 0; resetting on script tags is intentionally disabled). The writer
   still writes script tags at ts 0 — sidecar output should use 0 for the preamble metadata.
5. `Header { video, audio, .. }`: `annexb = get_annexb_state(session)`; if `annexb != IsAnnexB`
   → set `video.timestamp = 0` and `audio.timestamp = 0` if present. (Sidecar: headers are
   always written at ts 0 anyway; keep this for parity of the stored `Tag` used in comparisons.)

`calculate_new_target(tags)`:

```
video = 0 if no Video tag in tags, else per_channel(tags, VIDEO, fallback=33, min=15, max=50)
audio = 0 if no Audio tag in tags, else per_channel(tags, AUDIO, fallback=22, min=20, max=24)
return max(video, audio)

per_channel: sample = first 2 tags of that type;
  if sample.len() != 2: duration = fallback
  else: duration = sample[1].ts - sample[0].ts;
        if duration < min || duration > max: duration = fallback
  return duration + last tag of that type .ts
```

Notes: only the first two tags of each type in the group are sampled; the "last tag of that
type" is the last occurrence anywhere in the group. Mixed groups therefore predict the next
target as `max(last_video_ts + vdur, last_audio_ts + adur)`.

**Sidecar safety net (`clamp_monotonic`, divergence §10.13):** after the jump rule, walk the
data tags and for each track keep `last_output_timestamp`; if a tag would go negative or below
the previous same-track timestamp, set it to `max(0, last_output_timestamp)` and count R2 (plus
R8 when the clamped tag is AAC audio or HEVC video). This guarantees the INTERFACES §1.3
"per-track DTS monotonically non-decreasing, no negative timestamps" invariant even for
pathological inputs where the reference rule can still leave a rollback. It changes timestamps
only by moving them *up*; document the A/V drift trade-off in code, and never let it move a
timestamp backwards.

### 8.2 `UpdateTimestampOffsetRule` — audio/video offset (disabled upstream)

Not in `AddDefaultRules` (commented out at `IProcessingPipelineBuilderExtensions.cs:35`), so
C# recordings never run it. **Status: not ported in `rust/crates/flv-repair`** — R2/within-GOP
cases are covered by `UpdateTimestampJumpRule` (§8.1) plus the per-track monotonic clamp
(§8.1/§10.13), which is what the fixtures and INTERFACES require. The algorithm below is kept
as the reference specification; if it is ever ported, keep it behind a config flag (default
**off**) and do not let it alter default behavior.

Pre-order: transform each action, then `next()`.

1. For a `Data(tags)`: let "not normal" = there exists an adjacent pair (`a` then `b`) in
   `tags` with `a.timestamp > b.timestamp` (`Any2`).
   - If not normal → pass through.
   - Else, if within the Audio subsequence **or** within the Video subsequence such an adjacent
     inversion exists (`Any2` per track) → unrepairable: emit `Unrepairable` comment
     ("GOP 内音频或视频时间戳不连续") and **Disconnect** action; drop data.
   - Else (only cross-track inversions): compute a single offset for **video** tags:
     - scan tags left→right; maintain `last_audio`, stack of pending video tags,
       `min_offset = i32::MIN`, `max_offset = i32::MAX`.
     - when an Audio tag is seen: pop all pending videos; for each video with `left_audio`:
       `min_offset = max(min_offset, left_audio.ts - video.ts)`; with the current audio
       `right_audio`: `max_offset = min(max_offset, right_audio.ts - video.ts)`; set
       `last_audio = audio`.
     - when a Video tag is seen: push on stack. Other types → unreachable (error).
     - after the loop, pop remaining videos with `left = last_audio, right = null`.
   - Combine:
     - `min == max` → `offset = min`.
     - `min < max`:
       - `min != i32::MIN`: if `max != i32::MAX` → `offset = ((min as i64 + max as i64) / 2)` as
         `i32` (truncates toward zero, like C# `(long)sum / 2`); else `offset = min + 1`.
       - else: if `max != i32::MAX` → `offset = max - 1`; else → invalid.
     - `min > max` → invalid.
   - invalid → `Unrepairable` comment + Disconnect.
   - valid and `offset != 0` → emit `TimestampOffset` comment ("音视频时间戳偏移, D: {offset}"),
     add `offset` to **every Video tag** in the group; pass data through.
2. Other actions pass through unchanged.

Overflow: use `i64` for `left.ts - video.ts` and `(min+max)/2`.

### 8.3 `RemoveDuplicatedChunkRule` — duplicate group removal (R3, R8 audio dup)

Pre-order (drop the action before inner rules see it). Constants: `MAX_HISTORY = 16`,
`DISCONNECT_ON_DUPLICATED_DATA_COUNT = 10` (C# disconnects when consecutive count **> 10**).

1. For each `Data(tags)` build a group fingerprint:
   - `text = u32_be(tags.len())` followed by, for each tag in order, the tag data hash
     (C#: `tag.DataHash ?? tag.UpdateDataHash()`; DataHash is FarmHash64 of the raw payload,
     hex-encoded in C#, then UTF-8 bytes appended).
   - `hash = FarmHash64(text)` in C#. The sidecar substitutes **FNV-1a 64** (`offset basis
     0xcbf29ce484222325`, `prime 0x100000001b3`) over `tag count` followed by each tag's
     payload hash — `hash_group` in `pipeline.rs`. The substitution preserves the contract:
     (a) covers **tag count, order, and every payload byte**, (b) excludes timestamps and
     type/size, so re-timestamped copies are still caught (payload bytes already imply size).
     Reference deliberately excludes timestamps; the Rust hash must keep doing the same.
2. Look up `session["DeDuplicationQueue"]` (`VecDeque<u64>`, capacity 16). If any element
   equals the hash:
   - emit `RepeatingData` comment; increment `session["DuplicatedFlvDataCount"]` (starts 1);
   - if count > 10: C# emits Disconnect("连续收到了 10 段重复数据") and clears the counter.
     Sidecar: log `dedup_storm` event (class R3) and **continue dropping** (never disconnect a
     live recording; §10.2). To avoid starving a real stream, reset the counter after logging.
   - drop the whole `Data` action (all its tags).
3. Else: enqueue hash, trim to max 16 (`pop_front` while `len > 16`), clear the consecutive
   counter, pass the action through.
4. Non-`Data` actions pass through.

Note: this drops all tags in a duplicated group, exactly like the reference; it does **not**
compare per tag. The R3 fixture duplicates a whole group.

An implementation may additionally drop a tag that is byte-identical to the immediately
preceding tag of the same type/timestamp (`type, timestamp, payload`) and log `duplicate_tag`
(class R3/R8) — useful for server replays that are not aligned to a whole group. This is safe
for AAC (real consecutive frames never share payload *and* timestamp), but it is **not** a
fixture requirement; the fixtures duplicate whole groups.

### 8.4 `RemoveFillerDataRule` — H.264 filler NAL units (R4)

Post-order: `next()` first, then walk `context.actions`; for every `Data(tags)` and every tag
with `nalus = Some(list)`:

- remove all NALUs with `nalu_type == FillerData (12)`;
- keep the tag's NALU order otherwise;
- sidecar divergence: if the list becomes empty, **drop the tag** (C# would serialize a 5-byte
  body with zero NALUs, which is not useful); log `filler_dropped` with the byte count saved.
- If `nalus` is `None` (unparseable payload), leave the body untouched.
- Recompute `tag.size` from the re-serialized body and invalidate `data_hash`.

Tags with no NALUs (audio/script/headers) are untouched. The rule only saves space; it must
not change timestamps.

### 8.5 `HandleNewHeaderRule` — audio/video sequence headers (R5, R6, header changes)

Pre-order. Constants/enum: `AnnexBState { Unknown, Pending, IsAnnexB }`; session keys listed in
§6.2. Settings: `disable_split_on_h264_annexb` (default **false**), i.e. C# recordings only
run the AnnexB path when the room config enables it.

1. AnnexB detection (only when `disable_split_on_h264_annexb == true`):
   - if state == `IsAnnexB` → pass the action unchanged (never split again).
   - for a `Data(actions)`: find the first keyframe data tag; if it has parsed NALUs containing
     **both** SPS (7) and PPS (8) → `annexb = true`, break. If `annexb`: `Unknown → Pending`
     (comment "检测到一次 AnnexB 格式", action_required=false); `Pending → IsAnnexB` (comment).
     Pass action through. (Only the first keyframe tag is inspected; break after it.)
   - a `Header` action clears the AnnexB session key (C# line 116) when the setting is on.
2. `Header(all)` action:
   - read `last_video_header`, `last_audio_header` from session (clones of previously **selected**
     headers).
   - `multiple_header_present = false`.
   - `current_video = select_header(all.video_tags, &mut multiple)`:
     - if >1 video header tag in `all`: if every tag's binary data equals the first's → first;
       else → **last** tag and set `multiple_header_present = true`.
     - if exactly 1 → it; if 0 → `None`.
   - same for audio.
   - if `multiple_header_present` → comment `DecodingHeader` "收到了连续多个 Header，新建文件".
   - `split = multiple_header_present`.
   - `decide_split(&mut last_video, &mut current_video, &mut split)`:
     - last is None, current Some → split = true.
     - last Some, current None → `current = last` (keep old header; no split decided here).
     - both Some → if `current.data == last.data` → no split; else split = true.
     - both None → nothing.
   - repeat for audio.
   - store clones: if `current_video` Some → session video header = clone; same audio.
   - `not_first_time = last_video.is_some() || last_audio.is_some()`.
   - if `not_first_time && split && !multiple` → comment `DecodingHeader` "因为 Header 问题新建文件".
   - if `not_first_time && split` → emit `NewFile` (**sidecar: swallow, log `header_change`**).
   - if `split` → emit `Header { all: vec![], video: current_video.clone(),
     audio: current_audio.clone() }` (the selected headers; C# discards `all`).
   - if `multiple_header_present` → emit a `LogMessage` action containing all header tags and
     their raw bodies (sidecar: warn event with hex digests only, not megabytes).
3. Non-header actions pass through.

Sidecar adaptation (single output file): because `NewFile` is swallowed, a mid-stream header
change must still be emitted *before the next data tag*, otherwise the decoder keeps using the
old config. Emit the selected new headers immediately at their stream position (timestamps 0
as headers always are) and keep media timestamps monotonic — do **not** reset the timestamp
store (no new file). If headers are unchanged, they are dropped (this is what makes clean
input byte-identical: ffmpeg repeats its AVC header only once, but live streams repeat them).

### 8.6 `HandleNewScriptRule` — script / onMetaData (R6)

Pre-order. Constant names: `onMetaData = "onMetaData"`; session flag
`HandleNewScriptRule_MetaDataReceived`.

1. Only `Script(tag)` actions are considered; `script_data = None` → non-metadata path.
2. Is onMetaData? `values.len() == 2 && values[0] is String("onMetaData")` **or**
   `values.len() == 3 && values[2] is Null && values[0] is String("onMetaData")` (the
   `d1--ov-gotcha07` server variant). Anything else → "non onMetaData script tag":
   comment `Logging` (action_required=false), **drop the tag** (C# yields nothing).
3. On first metadata (`!metaDataReceived`): mark session, comment `OnMetaData`
   ("收到了 onMetaData", false), emit `NewFile` (sidecar: swallow), then emit
   `Script(normalized)` where body = `[String("onMetaData"), value]` with
   `value = values[1]` coerced to `ScriptDataEcmaArray` if it was the other type. Sidecar:
   hold the normalized metadata for the output preamble.
4. On repeated metadata: log warning with the body JSON (`duplicate_metadata`, class R6); if
   `split_on_script_tag` (default false) emit `NewFile` else emit `LogMessage`. Then emit the
   normalized script action again. **Sidecar:** do not re-emit onMetaData mid-stream in a single
   output file (players dislike it); keep the first metadata, log the event. Document.
5. Rust extension (recommended, documented divergence): recognize the common
   `@setDataFrame` wrapper `values = [String("@setDataFrame"), String("onMetaData"), value]`
   as onMetaData and normalize it to `[String("onMetaData"), value]`. C# drops these streams'
   metadata (then the writer synthesizes empty metadata). Accepting the wrapper is strictly
   better and changes no fixture (ffmpeg fixtures use plain onMetaData).

### 8.7 `HandleDelayedAudioHeaderRule` — audio header after audio data (R5)

Pre-order. Constants: two `ProcessingComment`s
(`Unrepairable` "音频数据出现在音频头之前", `DecodingHeader` "检测到延后收到的音频头").

C# algorithm for a `Data(tags)` action that contains any header tag:

1. Scan `tags` **backwards**; track `should_report_error`:
   - Audio tag with `flag != NONE` (a header) → `should_report_error = true`.
   - Audio tag with `flag == NONE` (audio data): if `should_report_error` was already set
     (i.e. a header occurred *later* in the group than this data) → unrepairable: emit
     `Unrepairable` comment, `Disconnect`, `NewFile`, then stop processing this action (C# yields
     `null` which aborts the `PerActionRun` transform).
2. Otherwise: emit `DecodingHeader` comment; split the group into
   `Header(all header tags)` followed by `Data(all non-header tags)`; emit both in that order.
3. Non-data actions pass through unchanged.

**Reality check (important for the Rust design).** With the grouping rules of §5 the rule is
almost unreachable: `HeaderGroupingRule` takes any header as the start of a header-only group,
and `DataGroupingRule` cannot append an audio header once the group contains audio data. So a
mixed "header + data" data-group does not normally occur. The C# rule therefore mainly sets the
intent; the R5 corruption is not actually repaired in-file by C# (the header is stashed for the
*next* recorded file by the writer's deferred-header mechanism).

The sidecar must achieve the intent in its single output stream: **when audio data arrives before
the AAC sequence header, buffer the audio tags (bounded by `--max-buffer`) and emit the sequence
header first** once it arrives. Concretely, in the delayed-header rule / writer:

- maintain `pending_audio_header` and a queue of audio data tags awaiting it;
- on AAC header arrival: emit header (ts 0) at the preamble position, then flush queued audio
  tags in original order with their (repaired) timestamps;
- if `--max-buffer` would be exceeded before the header arrives: emit `Unrepairable`/`R5` event,
  flush the buffered audio data anyway (best-effort) and stop buffering;
- if the header never arrives (EOF): flush with a warning (`R5`, no header).
- For the R5 fixture the expected result is: exactly one AAC sequence header, positioned before
  the first audio data tag in the output, video untouched.

### 8.8 `HandleEndTagRule` — AVC end-of-sequence (R4)

Pre-order: for every action, first yield the action, then if it is `End` emit `NewFile` and a
`Logging` comment ("因收到 End Tag 分段", action_required=false). C# writer writes the End tag
then closes the file and opens a new one.

Sidecar (single output), because dropping every END tag would violate the clean-input invariant
"must not add/remove media tags":

- **buffer** the latest `End` action instead of writing it immediately;
- when the next media action arrives (a `Data`/`Header`/`Script` action with media tags after
  it), a buffered END tag was mid-stream → **drop** it, log `end_tag_dropped` (class R4), and
  emit nothing for it; a mid-stream AVC packet-type-2 tag would make players stop early;
- at clean EOF, if an `End` action is still buffered (it is the terminal tag of the input), emit
  it as the last tag of the output. This keeps a clean ffmpeg recording byte-identical
  (`clean.flv` ends with an AVC end-of-sequence tag);
- never split the output on an END tag.
- If the END tag was the only tag in the stream, the output is the FLV preamble plus that END tag
  (still a valid FLV file).

### 8.9 `FfmpegDetectionRule` — ffmpeg-origin detection (analyzer only)

State: `end_tag_detected: bool`, `lavf_encoder_detected: bool` (sticky, never reset by the rule).
Before calling `next()`:

- if `!end_tag_detected` and any action in the context is `End` → set true.
- if `!lavf_encoder_detected` and any `Script` action has
  `script_data.get_metadata_value()` containing key `"encoder"` whose value is a String starting
  with `"Lavf"` (ordinal, case-sensitive) → set true.

No actions are modified. C# does not use it in the recording pipeline (only ToolBox
`AnalyzeHandler` + rule tests). Sidecar: implement the same detection in `analyze` for logging
(e.g. `{"event":"ffmpeg_source","end_tag":bool,"lavf":bool}`); the INTERFACES §1.4 JSON schema is
frozen — do not add fields to it, use stderr events if needed.

---

## 9. What Bilirec's `pkg/flv` already covers (do not re-implement blindly)

`pkg/flv` (MIT, in-tree) has a `RealtimeFixer`/`AccumulateFixer` used by
`internal/processors/flv_stream_fixer.go` and `flv_header_split_detector.go`. It is a useful
cross-check but **does not replace** the sidecar; the Rust port is the mandated implementation.

| behavior | Bilirec `RealtimeFixer` | Gap → Rust port must cover |
|---|---|---|
| FLV header / tag parsing (`Fix`) | yes: incremental buffer, skips 9-byte header, requires `PrevTagSize+TagHeader`, parses timestamp 24+8 | no resync: non-tag garbage breaks parsing (`ErrNotFlvFile` only for magic); partial/garbage handling differs (R7) |
| Timestamp jump/rollback | partial: `fixTimestamp` per tag (first chunk sets offset = first tag ts; `diff < -500 || (LastOriginal==0 && diff<0)` or `diff > 500` → offset), clamps negatives to 0 | per-*tag*, not per-group; uses `CalculateNextTarget` per tag (`NextTarget = tag.ts + 33/22 fallback`, no 2-sample min/max logic); no first-chunk min-of-group; no header/script/end special cases → R1/R2 continuity differs from C# |
| Dedup (R3) | yes, different algorithm: FNV-1a signature over type+timestamp+size+first 32 payload bytes, 1000-entry cache, ±5 s window, per-tag drop | no group-level hash (may drop legitimate identical tags, may miss identical group with shifted timestamps); no history-16/group semantics; no duplicate-storm counter |
| HEADER flags | `ClassifyVideoTag` supports AVC, legacy HEVC (codec 12), enhanced HEVC (hvc1/hev1) — **reuse the semantics** | C# parser lacks enhanced HEVC; Rust needs the bilirec logic for R8 |
| sequence-header change → split | `HeaderChangeDetector` detects changed video header and signals processor rotation (in `internal/processors`, not in the fixer) | sidecar has no rotation: must emit new headers in-stream / keep single output |
| AAC header detection | yes (`tagData[0]>>4==10 && tagData[1]==0`) | no delayed-header reordering (R5) |
| script/onMetaData, duration fix | **no** | Rust must implement AMF0 + metadata (R6) |
| filler NALU removal | **no** | Rust must implement (R4) |
| AVC end-of-sequence handling | **no** (End flag never set; `ClassifyVideoTag` ignores packet type) | Rust must classify END, drop mid-stream ones and keep a terminal one (R4) |
| zero-size / unknown tags | no special handling | Rust must drop/resync (R4/R7) |
| output writer | writes header once `FLV`-magic; buffers 4 bytes; writes `PrevTagSize` | no deferred preamble/headers/script, no metadata rewrite; header flags fixed from input |
| dedup storm / disconnect | none (drops all duplicates forever) | sidecar logs and continues (never aborts recording) |

Practical guidance for `rust-flv`:

1. Write the parser/tag/writer/AMF0 fresh — `RealtimeFixer` is optimized for the Go hot path and
   its buffer aliasing/`bytes.Buffer` design does not fit a rule pipeline.
2. Reuse (re-implement in Rust, crediting `pkg/flv/video_tag.go`) the video-codec
   classification for HEVC/Enhanced FLV; it is already validated in-tree and needed for R8.
3. For timestamps, dedup, headers, script, delayed audio header and filler: follow the C#
   rules in §8, not the Go fixer — the Go fixer's semantics diverge (per-tag target calculation,
   timestamp-inclusive dedup, no rule pipeline).
4. Do not wire the sidecar's repair into `pkg/flv`; the Go side calls the sidecar
   process (INTERFACES §2.1). `FLV_REPAIR=auto` means the Rust sidecar when present.

---

## 10. Divergences from the C# reference (single-output sidecar)

Each divergence must be commented in code and covered by a fixture/invariant.

1. **No file rotation.** `PipelineNewFileAction`/`HandleEndTagRule` splits become no-ops or
   in-stream header emissions. The sidecar emits one valid FLV stream: `flvrepair file` writes
   exactly one output file.
2. **Never abort the stream for repair reasons.** `Disconnect` actions are logged (stderr JSON
   `unrepairable`/class) and the pipeline continues best-effort. INTERFACES §2.1/§2.5: repair
   failure must not fail the recording; exit code 3 only in `strict`.
3. **Mid-stream AVC end-of-sequence tags are dropped; a terminal one is preserved.** C# writes
   the END tag and then splits the file. The sidecar buffers END actions and only emits a
   terminal END tag at EOF, so clean input is not modified and mid-stream END tags (R4) do not
   truncate playback.
4. **Delayed AAC header is repaired in-stream** (buffer audio data until header, then emit header
   first). C# only re-orders within a (rare) mixed group and otherwise stashes the header for the
   next file; its in-file intent is what we implement.
5. **Resync on malformed bytes** (R7) instead of throwing; strict mode exits 3.
6. **Preamble flags reflect tracks** (`0x01|0x04`), and are computed from the stream (buffer
   pre-roll until both headers are seen, or until `--max-buffer` / first-N-bytes threshold). C#
   hard-codes `0x05`.
7. **Malformed AMF0/script**: keep the raw tag and log; C# would leave `ScriptData = null` and
   later throw in the writer.
8. **Repeated onMetaData** is not re-emitted mid-stream (single file); the first metadata is
   preserved and the duplicate is logged.
9. **`@setDataFrame` wrapper** accepted as onMetaData (R6 improvement; C# drops it).
10. **Metadata duration**: only patched when the serialized length is unchanged (`file` mode);
    `stream` mode leaves/keeps `0` and logs. C# rewrites after every group with seeks.
11. **All-empty NALU list** (filler-only video tag) drops the tag; C# writes a 5-byte body.
12. **Timestamp store is not reset** on swallowed `NewFile`; output timestamps stay monotonic
    across header changes. C# resets because it starts a new file at 0.
13. **Per-track monotonic clamp (safety net).** After the jump rule the sidecar clamps each
    track: negative timestamps → 0, rollbacks → previous same-track timestamp, counting R2
    (and R8 for AAC/HEVC tags). The C# rules can leave negative/rollback timestamps in
    pathological inputs and rely on the recording pipeline to cope; INTERFACES §1.3 makes
    monotonicity a hard guarantee, so the clamp is mandatory. It only moves timestamps forward
    and may introduce small A/V drift; count it under R2 (and R8 for AAC/HEVC tags) in
    `analyze`/stats and keep the fixture invariants green (the shipped crate does not emit a
    separate `monotonic_clamped` event).
14. **`UpdateTimestampOffsetRule` is not ported.** It is upstream-disabled (commented out of
    `AddDefaultRules`), so this is not a behavior change versus the C# default. R2 and
    within-GOP audio/video offsets are handled by the jump rule + the §10.13 clamp. The
    algorithm remains specified in §8.2 for tests/future work.

---

## 11. Fixture → rule coverage map

`testdata/flv/fixtures/` (generated by `testdata/flv/gen/gen.py`; see README for regeneration):

| fixture | corruption | rules involved | key invariants (see `.expect.json`) |
|---|---|---|---|
| `r1_timestamp_jump.flv` | +2000 ms forward jump at ~8 s on all following tags | 8.1 | valid, per-track monotonic, duration ≈ 20 s, `R1 >= 1` |
| `r2_timestamp_rollback.flv` | −3000 ms rollback at ~10 s | 8.1 (8.2 if enabled) | valid, monotonic, duration ≈ 20 s, `R2 >= 1` |
| `r3_duplicated_chunk.flv` | whole ~1 s tag group duplicated verbatim | 8.3 | duplicate group dropped, no duration growth, `R3 >= 1` |
| `r4_filler_data.flv` | filler NALU (type 12) injected into video tags, zero-size audio/video tags, mid-stream AVC packet-type-2 end tags | 8.4, 8.8 | filler NALUs/zero tags/mid-stream end tags gone, terminal END kept, valid, monotonic, `R4 >= 1` |
| `r5_delayed_audio_header.flv` | first AAC sequence header moved to ~2.5 s after audio data begins | 8.7, 8.1 | AAC header present and before any audio data, valid, monotonic, `R5 >= 1` |
| `r6_bad_metadata.flv` | `duration` removed from `onMetaData`, FLV header flags `0x00` | 8.6, writer §7.4 | flags fixed to `0x05`, metadata preserved, `R6 >= 1` |
| `r7_truncated_tail.flv` | 20 garbage bytes between tags at ~10 s + terminal END tag and half of the last audio tag truncated | §3.6, 8.1 | valid, complete tags only (last 2 dropped), resync, duration slightly < 20 s, `R7 >= 1` |
| `r8_hevc_aac.flv` | HEVC-in-FLV base + AAC rollback (−1200 ms at ~6 s) + a duplicated data group containing AAC tags | §3.3, 8.1, 8.3 | codec hevc+aac preserved, headers preserved, per-track monotonic, duplicate group dropped, duration ≈ 20 s, `R8 >= 1` |

Clean fixtures: `base/clean.flv` (AVC+AAC) and `base/clean_hevc.flv` (HEVC+AAC) are the
unmutated bases. A clean input run must be byte-identical or within +1 % size with identical
media tag count (INTERFACES §1.3).

---

## 12. Status / resolved decisions (T2) and remaining open items

Resolved by the shipped implementation (`rust/crates/flv-repair`, T2):

- <b>Dedup hash</b>: FNV-1a 64 over group tag count + per-tag payload hashes (`hash_group`),
  timestamps excluded (§8.3).
- <b>Stream-mode pre-roll</b>: the writer buffers start data until both tracks' sequence headers
  are known, or until the hold limit (300 pending tags / `min(max_buffer/8, 2 MiB)`), then writes
  the FLV preamble with track-derived flags; delayed audio headers are emitted in front of the
  buffered audio data (§7.2/§8.7/§10.6).
- <b>`analyze` classes</b>: R1–R8 counters and `timestamps.{video,audio}.{first,last,monotonic,jumps}`
  are implemented per INTERFACES §1.4 and §6.3.
- <b>Module table</b>: §1 now reflects the actual crate layout, including the intentionally
  unported rules.
- <b>Clean-input behavior</b>: stream mode reproduces the clean AVC/HEVC bases byte-identically;
  file mode preserves media tags and only fixes `onMetaData.duration` in place when possible.

Remaining / optional:

- Keep §1's module table and §10 divergences current when rules are refactored.
- Optional: `keyframes` metadata index (`KeyframesScriptDataValue`, §7.5) and the
  `UpdateTimestampOffsetRule` (§8.2) — only if a concrete need appears; keep them off by default.
- If fixture expectations must change, update them only together with `testdata/flv/gen/gen.py`
  and the manifest (`gen.py --check` must stay green).
