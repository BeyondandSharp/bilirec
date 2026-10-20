#!/usr/bin/env bash
# Deterministically (re)generate the HLS replay fixtures served by scripts/origin.py.
#
#   testdata/hls/ts/playlist.m3u8 + seg000.ts ...
#   testdata/hls/fmp4/playlist.m3u8 + init.mp4 + seg000.m4s ...
#
# Properties: 12 s, 320x180 @ 25 fps, H.264 baseline + AAC 48 kHz stereo,
# 2 s segments, fixed GOP and no B-frames so segment boundaries are keyframes.
# All playlists use relative URIs (no absolute host/port), which is required by
# the frozen contract (docs/INTERFACES.md section 4).
#
# Usage: scripts/gen_hls_fixtures.sh [--check]
#   --check  generate into a temp dir and diff against the committed fixtures
#            instead of overwriting them (useful in CI / for reproducibility).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/testdata/hls"
DUR=12
RATE=25
SIZE=320x180
GOP=50
SEG=2

CHECK=0
if [[ "${1:-}" == "--check" ]]; then
  CHECK=1
elif [[ -n "${1:-}" ]]; then
  echo "usage: $0 [--check]" >&2
  exit 2
fi

command -v ffmpeg >/dev/null || { echo "ffmpeg is required" >&2; exit 1; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/hls-fixtures.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
DEST="$WORK/hls"
mkdir -p "$DEST/ts" "$DEST/fmp4"

COMMON=(
  -hide_banner -loglevel error -y
  -f lavfi -i "testsrc2=size=$SIZE:rate=$RATE:duration=$DUR"
  -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=$DUR"
  -map 0:v:0 -map 1:a:0
  -c:v libx264 -preset veryfast -profile:v baseline -level 3.0 -pix_fmt yuv420p
  -b:v 300k -g "$GOP" -keyint_min "$GOP" -sc_threshold 0 -bf 0
  -c:a aac -b:a 64k -ar 48000 -ac 2
  -fflags +bitexact -flags +bitexact -map_metadata -1
  -t "$DUR"
)

echo "==> generating TS HLS fixture (${DUR}s ${SIZE} avc+aac)"
ffmpeg "${COMMON[@]}" -f hls -hls_time "$SEG" -hls_list_size 0 \
  -hls_flags independent_segments \
  -hls_segment_filename "$DEST/ts/seg%03d.ts" "$DEST/ts/playlist.m3u8"

echo "==> generating fMP4 HLS fixture (${DUR}s ${SIZE} avc+aac)"
ffmpeg "${COMMON[@]}" -f hls -hls_time "$SEG" -hls_list_size 0 \
  -hls_flags independent_segments -hls_segment_type fmp4 \
  -hls_fmp4_init_filename init.mp4 \
  -hls_segment_filename "$DEST/fmp4/seg%03d.m4s" "$DEST/fmp4/playlist.m3u8"

# Keep only what the origin serves/needs.
[[ -f "$DEST/ts/playlist.m3u8" && -f "$DEST/fmp4/playlist.m3u8" && -f "$DEST/fmp4/init.mp4" ]] || {
  echo "generation incomplete: expected playlist/init files are missing" >&2
  exit 1
}

if [[ "$CHECK" == "1" ]]; then
  echo "==> --check: comparing against committed fixtures"
  if ! diff -rq "$OUT/ts" "$DEST/ts" >/dev/null || ! diff -rq "$OUT/fmp4" "$DEST/fmp4" >/dev/null; then
    echo "FAIL: committed fixtures differ from freshly generated ones" >&2
    diff -rq "$OUT/ts" "$DEST/ts" >&2 || true
    diff -rq "$OUT/fmp4" "$DEST/fmp4" >&2 || true
    exit 1
  fi
  echo "OK: fixtures are reproducible"
  exit 0
fi

rm -rf "$OUT/ts" "$OUT/fmp4"
mkdir -p "$OUT"
cp -a "$DEST/ts" "$DEST/fmp4" "$OUT/"
echo "==> wrote:"
find "$OUT" -type f -printf '%10s  %p\n' | sort -k2
