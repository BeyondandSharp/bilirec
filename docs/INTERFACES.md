# INTERFACES v1 — frozen contract

Frozen by Lead on 2026-10-20. Any change requires Lead approval and a `docs/DECISIONS.md` entry.
Owners must implement against this document; do not redesign without approval.

## 0. Context

- Repo: `/root/tmp11` (single git repo). The repository root **is** the Bilirec v0.3.1 fork (Go module at root).
  (upstream commit `26f4f8d`, MIT). See `UPSTREAM.md`.
- Reference-only, gitignored: `refs/BililiveRecorder` (GPL-3.0), `refs/biliup` (MIT).
- Project license: GPL-3.0-or-later, because `rust/crates/flv-repair` is a port of
  BililiveRecorder.Flv logic. See `NOTICE`.
- Scope: recording only. No upload, no mobile, no WebUI changes, no DB schema change in v1.
- Native Bilirec behavior must remain byte-for-byte unchanged when the new features are off
  (defaults: `DOWNLOADER=auto`, `FLV_REPAIR=auto`).

## 1. Rust sidecar `flvrepair` (owner: rust-flv)

Crate: `rust/crates/flv-repair` (lib + bin `flvrepair`). GPL-3.0-or-later headers required.

### 1.1 CLI

```
flvrepair stream  [--mode best-effort|strict] [--max-buffer 64MiB]
flvrepair file    --input <in> --output <out> [--mode best-effort|strict]
flvrepair analyze --input <path> [--json]
flvrepair --version
```

- `stream`: read raw FLV from stdin, write repaired FLV to stdout. Must stream
  incrementally (bounded internal buffering); no `read_to_end`.
- `file`: same processing, file in/out (used for batch repair and post-processing).
- `analyze`: parse and emit a report (JSON on stdout; human text if `--json` absent).
- `--max-buffer`: hard cap on internal buffered input+output, default 64 MiB, MiB suffix.

### 1.2 Process contract

- Exit codes: `0` clean EOF; `1` I/O error; `2` invalid arguments; `3` unrecoverable input
  (strict mode only; best-effort must still exit 0 and emit best-effort output).
- stderr: one JSON object per line, e.g.
  `{"level":"warn","event":"timestamp_jump","count":1,"class":"R1"}`. Never write logs to stdout.
- Must flush stdout promptly after each processed tag group (do not wait for 64 KiB).
- Backpressure: block on stdin/stdout I/O; do not drop bytes unless `--mode best-effort` and the
  data is unrecoverable.
- SIGTERM/SIGINT: flush what is buffered, exit 0 (best-effort) or 1 (I/O failure).
- No panics on malformed input; all parser errors are recoverable by resync in best-effort mode.

### 1.3 Repair guarantees

- Output is a valid FLV: correct `FLV` header (version 1, flags reflect present tracks),
  `PreviousTagSize0`, then tags with correct `PreviousTagSize` fields.
- Per-track DTS is monotonically non-decreasing (video and audio independent, interleaving
  preserved; no negative timestamps).
- AVC/HEVC sequence headers preserved; first video tag of each new file is a keyframe when one
  exists in input; script `onMetaData` preserved and its `duration` fixed when known.
- Truncated input: emit all complete tags, drop the partial tail.
- Covered corruption classes (all must have fixtures + tests):
  - R1 timestamp jump forward > 500 ms (rebuild offset, keep continuity)
  - R2 timestamp rollback / negative diff
  - R3 duplicated tag groups (same timestamp + identical payload)
  - R4 filler / AVC end-of-sequence / zero-size filler tags
  - R5 audio header arriving after audio data (delayed header)
  - R6 missing or zero `duration` in metadata; wrong header flags
  - R7 truncated tail / partial tag / garbage between tags (resync)
  - R8 HEVC-in-FLV and AAC timestamp anomalies (duplicate/rollback)
- Clean input: output may be byte-identical, but must not grow by more than 1% and must not
  add/remove media tags.

### 1.4 `analyze` JSON (schema v1)

```json
{
  "version": 1, "valid": true, "bytes": 1234567, "duration_ms": 61234,
  "video": {"codec": "avc", "width": 1920, "height": 1080},
  "audio": {"codec": "aac", "sample_rate": 48000, "channels": 2},
  "timestamps": {
    "video": {"first": 0, "last": 61000, "monotonic": true, "jumps": 0},
    "audio": {"first": 0, "last": 61200, "monotonic": true, "jumps": 0}
  },
  "anomalies": [{"class": "R1", "count": 2}]
}
```

### 1.5 Performance / resources

- Throughput >= 2x realtime for a 1080p 6 Mbps stream on the reference machine (4 vCPU).
- Peak RSS <= 64 MiB at `--max-buffer 64MiB`.
- Startup < 100 ms.

### 1.6 Porting rules

- Port behavior from `refs/BililiveRecorder/BililiveRecorder.Flv` (rules under
  `Pipeline/Rules/`, parser under `Parser/`). Record the mapping C# file -> Rust module in
  `docs/PORTING.md` (owner: port-spec writes, rust-flv keeps updated).
- Every Rust source file starts with:
  `// SPDX-License-Identifier: GPL-3.0-or-later`
  plus a short attribution comment when directly derived from a specific C# file.

## 2. Go integration in the repo root (owner: go-integration)

### 2.1 New packages

`internal/services/downloader`:

```go
type Kind string
const (
    KindNative     Kind = "native"
    KindFFmpeg     Kind = "ffmpeg"
    KindStreamlink Kind = "streamlink"
)

type Spec struct {
    URL     string            // resolved stream/playlist URL (from bilibili.StreamURLInfo)
    Format  string            // source format: "flv" | "ts" | "fmp4"
    Headers map[string]string // Cookie, User-Agent, Referer, Origin
}

type Downloader interface {
    Name() string
    // Open spawns the process tree; Close on the returned ReadCloser must kill the whole tree.
    Open(ctx context.Context, spec Spec) (io.ReadCloser, error)
}
```

- `FFmpegDownloader`: `ffmpeg -loglevel error -y -headers <hdr> -rw_timeout 20000000
  [-max_reload 1000 if HLS] -fflags +genpts -i <url> -c copy -reset_timestamps 1
  -avoid_negative_ts make_zero -f flv -` (derived from biliup, MIT).
- `StreamlinkDownloader`: pipeline `streamlink --stream-segment-threads 3
  --hls-playlist-reload-attempts 1 [--http-header K=V ...] <url> best -O` -> `ffmpeg
  -fflags +genpts -i pipe:0 ... -f flv -`. Close kills both processes.
- URL normalization (copy biliup): URLs already prefixed (`httpstream://`, `hls://`,
  `dash://`) unchanged; `*.m3u8`/`*.mpd` unchanged; progressive `*.flv` gets `httpstream://`.
- Headers: obtained from `bilibili.Client` (new method `StreamRequestHeaders()` returning
  Cookie/User-Agent/Referer/Origin; reuse existing live client constants).
- stderr of child processes: drain line-by-line to bilirec logger with `[ffmpeg]`/`[streamlink]`
  prefixes.

`internal/services/stream/external.go`:

```go
func (r *Service) ReadExternalStream(rc io.ReadCloser, ctx context.Context, qn int,
    chunkPool *pool.BucketedBytesPool, releasePool func()) <-chan []byte
```

- Same idle-timeout/channel semantics as `ReadFlvStream`; closes `rc` and releases pool on exit.

`internal/services/repair`:

```go
func (s *Service) Enabled() bool
// WrapStream pumps ch -> `flvrepair stream` stdin -> stdout -> returned channel.
// Returned buffers MUST come from chunkPool (recorder.rev returns them to the pool).
// Input buffers from ch MUST be Put back to chunkPool after being written to the sidecar.
// If the sidecar is unavailable, returns the input channel unchanged.
func (s *Service) WrapStream(ctx context.Context, ch <-chan []byte,
    chunkPool *pool.BucketedBytesPool) (<-chan []byte, error)
```

- Restart policy: on sidecar death while streaming, restart once; second death -> pass-through
  for the rest of that session (log `repair=degraded`). Never abort the recording because of
  repair failure.
- Shutdown: close stdin, wait max 5 s, then kill.

### 2.2 Hook points (exact files)

1. `internal/modules/config/config.go`: add fields + env parsing (table below).
2. `internal/controllers/record/types.go`: add optional `Downloader *string`
   (json `downloader`, query `downloader`).
3. `internal/controllers/record/start_params.go`: validate `auto|native|ffmpeg|streamlink`,
   pass `recorder.WithDownloader(kind)`.
4. `internal/services/recorder/start_options.go`: store the option.
5. `internal/services/recorder/start_internal.go`:
   - in `connectStream` (around the `case "flv"` / `case "ts", "fmp4"` branches) add the
     external-downloader branch; external sessions use `rs.NewFlvStrategy(qn)` and report
     `Format="flv"` in session info (log the original source format).
   - on external start failure (spawn error, non-zero exit before first byte, or no first byte
     within `EXTERNAL_START_TIMEOUT_SECS`) log and fall through to native for the same
     `streamInfo` (no new session).
6. `internal/services/recorder/start_internal.go` `internalStart`: after
   `connectStream` succeeds, if `streamInfo.Format == "flv"` and repair enabled, wrap `ch` with
   `repair.WrapStream` before `commitSession`/`prepare`. HLS paths are not wrapped.
7. `internal/services/recorder/recorder.go`: pass the repair service into `Service`
   (fx wiring in the module that provides recorder). No changes to `rev`/`rotate`.

### 2.3 Environment variables

| Key | Values | Default | Meaning |
|---|---|---|---|
| `DOWNLOADER` | auto/native/ffmpeg/streamlink | auto | auto = native (no external) |
| `DOWNLOADER_FALLBACK` | comma list | native | reserved; v1 only falls back to native |
| `FFMPEG_BIN` | path | ffmpeg | ffmpeg binary |
| `STREAMLINK_BIN` | path | streamlink | streamlink binary |
| `EXTERNAL_START_TIMEOUT_SECS` | int | 20 | first-byte deadline |
| `FLV_REPAIR` | auto/on/off | auto | auto = on iff `flvrepair` resolves |
| `FLV_REPAIR_BIN` | path | flvrepair | sidecar binary |
| `FLV_REPAIR_MODE` | best-effort/strict | best-effort | passed to sidecar |
| `FLV_REPAIR_MAX_BUFFER_MB` | int | 64 | passed as `--max-buffer` |

No DB migration. Per-room persistence is v2.

### 2.4 Logging fields

Every session log must include: `room`, `downloader` (native/ffmpeg/streamlink),
`repair` (off/on/degraded), `source_format` (flv/ts/fmp4), `bytes_in`, `bytes_out`
(repair), `repair_restarts`.

### 2.5 Invariants

- Defaults reproduce v0.3.1 behavior exactly.
- External downloader output is always FLV (ffmpeg remux), so `FlvStrategy`/`.flv` extension is
  used; session info `Format` = `flv`. Original `ts`/`fmp4` source is only logged.
- One repair process per active FLV session; no repair for HLS sessions.
- Repair failure never errors the recording.
- No goroutine/process leaks: every session's child processes are reaped on cancel/close.

## 3. Data flow

```
internalStart
  -> bilibili.GetLiveRoomInfo / GetStreamURLsV2
  -> connectStream
       native: resty body / HLS session -> <-chan []byte
       external: downloader.Open -> stream.ReadExternalStream -> <-chan []byte
  -> repair.WrapStream (FLV only)
  -> commitSession + prepare -> rotate -> pipeline (existing processors/writer)
```

## 4. Fixtures and local origin

- `testdata/flv/` owner: port-spec (fixtures + `gen/` + `*.expect.json`).
- `testdata/hls/` owner: qa-harness (ts + fmp4 playlists/segments).
- `scripts/origin.py` owner: qa-harness. Python stdlib HTTP server, port from `ORIGIN_PORT`
  (default 18080), endpoints:
  - `/flv/<file>` chunked with throttle (`THROTTLE_BPS`, default 1 MiB/s)
  - `/hls/ts/playlist.m3u8`, `/hls/fmp4/playlist.m3u8`
  - fault injection via query: `?drop_after=<bytes>`, `?corrupt=1`
- `scripts/e2e.sh` owner: qa-harness: builds via `make build`, starts origin + backend
  (`BILIBILI_LOGIN_MODE=anonymous`, temp dirs), records through API, validates output with
  `ffprobe` + `flvrepair analyze`, and greps logs for expected fields.

## 5. Build & CI

- Toolchain present: Go 1.27.2 (`/usr/local/go`), Rust 1.99.1 (`/root/.cargo`), ffmpeg 7.1.5,
  streamlink 8.6.2, Python 3.13. `GOPROXY=https://goproxy.cn,direct`, `GOSUMDB=off` are set.
- `docs/swagger.json` is generated (gitignored):
  `go install github.com/swaggo/swag/cmd/swag@v1.16.6 && swag init -g internal/modules/rest/rest.go -o docs`.
- Root targets: `make fmt lint test build e2e`. `make test` must pass offline (`-short`).
- Git policy: teammates do NOT run `git add/commit` (shared worktree, avoid index races and
  mixed commits). Leave changes in the working tree; Lead reviews and commits per task with
  message `T<n>: <summary>`.

## 6. Ownership / write scopes / review

| Owner | Write scope |
|---|---|
| Lead | `docs/INTERFACES.md`, `docs/DECISIONS.md`, root `Makefile`, `README.md`, `LICENSE`, `NOTICE`, `UPSTREAM.md`, `.gitignore`, final integration fixes |
| port-spec | `docs/PORTING.md`, `testdata/flv/**` |
| rust-flv | `rust/**` |
| go-integration | `internal/**, cmd/**, pkg/**, utils/**` |
| qa-harness | `scripts/**`, `deploy/**`, `testdata/hls/**` |

- Definition of done per task: code + tests + `make fmt lint test` green for touched scope +
  evidence commands in the task + Lead review.
- Lead merges; owners fix review findings. No cross-scope edits without Lead approval.

## 7. Out of scope for v1

- Batch repair HTTP API / `bilirec repair` subcommand (Rust CLI `flvrepair file` covers batch).
- Per-room persisted downloader/repair config (DB/WebUI).
- HLS fallback that preserves raw ts/m4s segments (external fallback remuxes to FLV).
- Upload, mobile, playback features, metrics dashboards.
- Cross-platform builds beyond linux/amd64 (build tags must still compile on other OS where
  possible; no Android).
