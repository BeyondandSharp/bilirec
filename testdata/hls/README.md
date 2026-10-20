# testdata/hls — HLS replay fixtures (owner: qa-harness)

Served by `scripts/origin.py` under `/hls/`:

- `ts/playlist.m3u8` + `ts/seg000..005.ts` — MPEG-TS segments (H.264 + AAC).
- `fmp4/playlist.m3u8` + `fmp4/init.mp4` + `fmp4/seg000..005.m4s` —
  fragmented-MP4 segments (`-hls_segment_type fmp4`).

Properties:

- 12 s, 320x180 @ 25 fps, H.264 *baseline* (no B-frames) + AAC-LC 48 kHz stereo,
  2 s segments aligned to keyframes, ~1.2 MB total.
- Playlists contain **relative URIs only** (no host/port), so they work from any
  origin port.
- Fully synthetic (`testsrc2` + `sine` via ffmpeg); no real broadcaster content.

Regenerate / verify determinism:

```bash
scripts/gen_hls_fixtures.sh          # rewrite these files
scripts/gen_hls_fixtures.sh --check  # regenerate to temp dir + diff (exit 1 on drift)
```

Quick manual check:

```bash
ffprobe -v error -show_entries format=format_name,duration \
  -of default=nw=1 testdata/hls/ts/playlist.m3u8
# format_name=hls
# duration=12.000000
```

These fixtures back the offline E2E: `ffmpeg -i http://127.0.0.1:<origin>/hls/
{ts,fmp4}/playlist.m3u8 -c copy -f flv -` and the `streamlink ... -O | ffmpeg
...` hybrid both yield a valid 12 s FLV. `testdata/flv/` (FLV repair fixtures)
is owned by `port-spec`; `scripts/origin.py` serves it from `/flv/<file>` when
the flag/env `ORIGIN_FLV_ROOT` points at it (default: `testdata/flv`).
