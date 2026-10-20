#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Offline-first end-to-end test for the bilirec + Rust FLV repair project.
#
#   make e2e                 # or: bash scripts/e2e.sh
#
# What it covers WITHOUT network access:
#   1. `make build` (Go backend -> dist/bilirec, Rust sidecar -> dist/flvrepair
#      unless E2E_SKIP_BUILD=1).
#   2. scripts/gen_hls_fixtures.sh fixtures served by scripts/origin.py.
#   3. Downloader command lines from docs/INTERFACES.md §2.1, run against the
#      local origin: ffmpeg for ts+fmp4 HLS and the streamlink|ffmpeg hybrid.
#   4. /flv replay + fault injection: full throttled download, ?drop_after=
#      (aborted mid-stream), ?corrupt=1 (bounded corruption).
#   5. dist/flvrepair over HTTP: `stream`, `file` and `analyze --json` on clean,
#      truncated and corrupted input; output validated with ffprobe.
#   6. bilirec backend startup with BILIBILI_LOGIN_MODE=anonymous, temporary
#      OUTPUT_DIR/DATABASE_DIR/SECRET_DIR, random PORT, waiting for /version and
#      checking the REST API is up (/version + /record/list).
#   7. FEATURES-V2 (docs/FEATURES-V2.md): settings API round-trip/reset,
#      invalid-payload 400 matrix (segment/naming/filter/danmaku/FLV/B05),
#      env->defaults seeding incl. invalid-env fallback, settings persistence
#      across a backend restart, and room-config nil/inherit round-trip. The
#      room is seeded offline into subscribes.db (scripts/roomcfg_seed.go)
#      because PUT /room/<id>/config requires a subscribed room and the real
#      subscribe path calls Bilibili. A second backend instance with
#      USERNAME/PASSWORD + VIEWER_USERNAME/VIEWER_PASSWORD exercises the auth
#      matrix (401 unauth, viewer read-only, admin write, reset). Set
#      E2E_SKIP_SETTINGS_V2=1 only as a developer escape hatch while T9 is in
#      flight — acceptance runs must include these checks.
#
# What CANNOT be covered offline (documented, not faked):
#   * Starting a recording through the REST API needs a real, live Bilibili room
#     (GET /record/<room>/start resolves the room and its stream URLs through
#     Bilibili's API; there is no local-stream injection in v1).
#   * Therefore the real "record -> repaired .flv on disk -> log fields" path is
#     opt-in:
#         E2E_LIVE_ROOM_ID=<live-room-id> make e2e
#     Prerequisites: outbound network to Bilibili + a room that is live right
#     now. It then starts the recording, waits for a .flv in OUTPUT_DIR, stops
#     it, ffprobes the result and greps the backend log for the session fields
#     required by docs/INTERFACES.md §2.4 (room/downloader/repair/source_format).
#     Optional: E2E_LIVE_WAIT_SECS (default 60), E2E_DOWNLOADER=ffmpeg|streamlink
#     to force a downloader kind (default: backend default, DOWNLOADER=auto).
#
# Developer escape hatches (not for acceptance runs):
#   E2E_SKIP_BUILD=1        reuse existing dist/ binaries
#   E2E_SKIP_FLVREPAIR=1    skip repair sections (only while the sidecar is WIP)
#   E2E_SKIP_SETTINGS_V2=1  skip FEATURES-V2 settings/room-config/auth sections
#                           (only while T9 is in flight; final runs must not set it)
#   E2E_KEEP_TMP=1          keep the temp work dir and print its path
#   E2E_ORIGIN_PORT=... / E2E_BACKEND_PORT=...  use fixed ports
#   E2E_SEED_ROOM_ID=...    seeded subscribed room id for room-config checks
#                           (default 12345); credentials: E2E_ADMIN_USER/PASS,
#                           E2E_VIEWER_USER/PASS.
#
# Exit code: 0 iff every non-skipped check passed.
set -u
set -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PASSED=0
FAILED=0
SKIPPED=0
ORIGIN_PID=""
BACKEND_PIDFILE=""
WORK=""
OPORT=""
BPORT=""
ORIGIN_LOG=""
BACKEND_LOG=""
FLVREPAIR="${FLVREPAIR_BIN:-$ROOT/dist/flvrepair}"

THROTTLE_BPS="${THROTTLE_BPS:-1048576}"
E2E_SKIP_BUILD="${E2E_SKIP_BUILD:-0}"
E2E_SKIP_FLVREPAIR="${E2E_SKIP_FLVREPAIR:-0}"
E2E_KEEP_TMP="${E2E_KEEP_TMP:-0}"
E2E_LIVE_ROOM_ID="${E2E_LIVE_ROOM_ID:-}"
E2E_LIVE_WAIT_SECS="${E2E_LIVE_WAIT_SECS:-60}"
E2E_DOWNLOADER="${E2E_DOWNLOADER:-}"

# FEATURES-V2 settings API / auth matrix (see header).
E2E_SKIP_SETTINGS_V2="${E2E_SKIP_SETTINGS_V2:-0}"
E2E_SEED_ROOM_ID="${E2E_SEED_ROOM_ID:-12345}"
E2E_ADMIN_USER="${E2E_ADMIN_USER:-e2e-admin}"
E2E_ADMIN_PASS="${E2E_ADMIN_PASS:-e2e-admin-pass}"
E2E_VIEWER_USER="${E2E_VIEWER_USER:-e2e-viewer}"
E2E_VIEWER_PASS="${E2E_VIEWER_PASS:-e2e-viewer-pass}"

# Cookie jar used by the api() helper ("-" = no auth cookie sent).
AUTH="-"

say()  { printf '\n=== %s ===\n' "$*"; }
pass() { PASSED=$((PASSED + 1)); printf 'PASS  %s\n' "$*"; }
fail() { FAILED=$((FAILED + 1)); printf 'FAIL  %s\n' "$*" >&2; }
skip() { SKIPPED=$((SKIPPED + 1)); printf 'SKIP  %s\n' "$*"; }

die() {
  fail "$*"
  summary
  exit 1
}

summary() {
  printf '\n--- e2e summary: %d passed, %d failed, %d skipped ---\n' "$PASSED" "$FAILED" "$SKIPPED"
  if [[ -n "${WORK:-}" && -d "$WORK" ]]; then
    if [[ "$E2E_KEEP_TMP" == "1" ]]; then
      printf 'artifacts kept in: %s\n' "$WORK"
    fi
  fi
}

cleanup() {
  local rc=$?
  if [[ -n "$ORIGIN_PID" ]]; then
    kill -TERM -- "-$ORIGIN_PID" 2>/dev/null || kill -TERM "$ORIGIN_PID" 2>/dev/null || true
  fi
  if [[ -n "$BACKEND_PIDFILE" && -f "$BACKEND_PIDFILE" ]]; then
    local bpid
    bpid=$(cat "$BACKEND_PIDFILE" 2>/dev/null || true)
    if [[ -n "$bpid" ]]; then
      kill -TERM -- "-$bpid" 2>/dev/null || kill -TERM "$bpid" 2>/dev/null || true
      for _ in $(seq 1 30); do
        kill -0 "$bpid" 2>/dev/null || break
        sleep 0.1
      done
      kill -KILL -- "-$bpid" 2>/dev/null || kill -KILL "$bpid" 2>/dev/null || true
    fi
  fi
  if [[ -n "${WORK:-}" && -d "$WORK" && "$E2E_KEEP_TMP" != "1" ]]; then
    rm -rf "$WORK"
  fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

# wait_http <url> <timeout-seconds>
wait_http() {
  local url=$1 timeout=$2 i=0
  while (( i < timeout * 5 )); do
    if curl -fsS -o /dev/null --max-time 2 "$url" 2>/dev/null; then
      return 0
    fi
    sleep 0.2
    i=$((i + 1))
  done
  return 1
}

is_flv() {
  local fmt
  fmt=$(ffprobe -v error -show_entries format=format_name -of default=nw=1:nk=1 "$1" 2>/dev/null | head -1)
  [[ "$fmt" == *flv* ]]
}

# duration_ok <file> <min> <max>
duration_ok() {
  python3 - "$1" "$2" "$3" <<'PY'
import subprocess
import sys

path, lo, hi = sys.argv[1], float(sys.argv[2]), float(sys.argv[3])
res = subprocess.run(
    ["ffprobe", "-v", "error", "-show_entries", "format=duration",
     "-of", "default=nw=1:nk=1", path],
    capture_output=True, text=True,
)
try:
    dur = float(res.stdout.strip())
except ValueError:
    sys.exit(1)
sys.exit(0 if lo <= dur <= hi else 1)
PY
}

# median growth guard from INTERFACES §1.3: clean input must not grow > 1%
clean_no_growth() {
  python3 - "$1" "$2" <<'PY'
import os
import sys
src, out = os.path.getsize(sys.argv[1]), os.path.getsize(sys.argv[2])
sys.exit(0 if out <= src * 1.01 + 4096 else 1)
PY
}

json_has_keys() {
  python3 -c '
import json
import sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(d, dict) and all(k in d for k in sys.argv[1:]) else 1)
' "$@"
}

json_version_1() {
  python3 -c '
import json
import sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(d, dict) and d.get("version") == 1 else 1)
'
}

# run_capture <logfile> <command...>; returns command status
run_capture() {
  local log=$1
  shift
  "$@" >"$log" 2>&1
}

# ---------------------------------------------------------------------------
# FEATURES-V2 API / backend helpers
# ---------------------------------------------------------------------------
API_BODY=""

# api <method> <path> [body-file] -> prints HTTP status, body in $API_BODY.
# Sends the cookie jar in $AUTH when set to a file.
api() {
  local method=$1 path=$2 body=${3:-}
  local args=(-sS -o "$API_BODY" -w '%{http_code}' --max-time 15 -X "$method")
  if [[ "$AUTH" != "-" && -f "$AUTH" ]]; then
    args+=(-b "$AUTH")
  fi
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary @"$body")
  fi
  curl "${args[@]}" "http://127.0.0.1:$BPORT$path"
}

# expect_status <want> <got> <desc>: body is echoed on failure.
expect_status() {
  local want=$1 got=$2 desc=$3
  if [[ "$got" == "$want" ]]; then
    pass "$desc -> $got"
  else
    fail "$desc -> $got (want $want; body: $(head -c 400 "$API_BODY" 2>/dev/null | tr '\n' ' '))"
  fi
}

# mkbody <name> (JSON on stdin): writes $WORK/out/body-<name>.json, echoes path.
mkbody() {
  local f="$WORK/out/body-$1.json"
  cat >"$f"
  printf '%s' "$f"
}

# json_get <file> <dotted.path>: compact JSON of the value or __MISSING__.
json_get() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

try:
    cur = json.load(open(sys.argv[1]))
except Exception:
    print("__INVALID__")
    sys.exit(0)
for part in sys.argv[2].split("."):
    if isinstance(cur, dict) and part in cur:
        cur = cur[part]
    elif isinstance(cur, list) and part.isdigit() and int(part) < len(cur):
        cur = cur[int(part)]
    else:
        print("__MISSING__")
        sys.exit(0)
print(json.dumps(cur, ensure_ascii=False, sort_keys=True))
PY
}

# json_assert <file> <dotted.path> <expected-json> <desc>
json_assert() {
  local got want=$3 desc=$4
  got=$(json_get "$1" "$2")
  if [[ "$got" == "$want" ]]; then
    pass "$desc"
  else
    fail "$desc: $2 got $got want $want"
  fi
}

# settings_defaults_ok <file>: shape + env-seeded defaults + empty overrides.
# Expected values mirror the V2_ENV array below; invalid env values must have
# fallen back to the built-in defaults (danmaku_transport=tcp -> auto,
# bili_cdn=bad/entry -> []).
settings_defaults_ok() {
  python3 - "$1" <<'PY'
import json
import sys

try:
    d = json.load(open(sys.argv[1]))
except Exception as exc:
    sys.exit("invalid JSON: %s" % exc)
defs, ov, vals = d.get("defaults"), d.get("overrides"), d.get("values")
if not all(isinstance(x, dict) for x in (defs, ov, vals)):
    sys.exit("expected object keys defaults/overrides/values, got %r" % sorted(d))
required = [
    "segment_mode", "segment_minutes", "segment_size_mb", "segment_by_title",
    "min_file_size_mb", "filename_template", "title_filter_patterns", "save_stream_cover",
    "danmaku_types", "danmaku_transport", "flv_write_metadata", "flv_split_on_script_tag",
    "flv_disable_split_on_annexb", "bili_live_api_host", "bili_live_api_fallback_host",
    "bili_cdn", "bili_cdn_fallback", "bili_force_source", "bili_replace_cn01",
]
errs = []
missing = [k for k in required if k not in defs]
if missing:
    errs.append("missing default keys: %s" % missing)
expected = {
    "segment_mode": "time",
    "segment_minutes": 15,
    "segment_size_mb": 300,
    "segment_by_title": False,
    "min_file_size_mb": 0,
    "filename_template": "e2e-{roomid}-{date}",
    "title_filter_patterns": "^e2e-filter$",
    "save_stream_cover": False,
    "danmaku_types": {"raw_events": False, "super_chat": True, "gift": True, "guard": False},
    # DANMAKU_TRANSPORT=tcp is invalid -> built-in "auto"
    "danmaku_transport": "auto",
    "flv_write_metadata": False,
    "flv_split_on_script_tag": True,
    "flv_disable_split_on_annexb": False,
    "bili_live_api_host": "https://api.live.bilibili.com",
    # BILI_LIVE_API_FALLBACK_HOST unset -> ""
    "bili_live_api_fallback_host": "",
    # BILI_CDN=bad/entry is invalid -> built-in []
    "bili_cdn": [],
    "bili_cdn_fallback": True,
    "bili_force_source": True,
    "bili_replace_cn01": [],
}
for key, want in expected.items():
    got = defs.get(key)
    # Go marshals an empty []string as null; accept both for empty-list defaults.
    if want == [] and got is None:
        continue
    if got != want:
        errs.append("defaults.%s=%r want %r" % (key, got, want))
if vals != defs:
    errs.append("values != defaults on a fresh DB")
if errs:
    sys.exit("; ".join(errs))
print("19 default keys present, env-seeded values match, invalid env fell back, overrides empty")
PY
}

# settings_overrides_ok <file> <expected-explicit-overrides-json>
# `overrides` is returned as a full GlobalSettings shape (unset keys are zero),
# so assert the explicitly-set keys and that every other key in `values` still
# equals its env/built-in default (docs/FEATURES-V2.md §1.1).
settings_overrides_ok() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

try:
    d = json.load(open(sys.argv[1]))
    want = json.loads(sys.argv[2])
except Exception as exc:
    sys.exit("invalid JSON: %s" % exc)
errs = []
overrides = d.get("overrides", {})
vals, defs = d.get("values", {}), d.get("defaults", {})
for key, value in want.items():
    if overrides.get(key) != value:
        errs.append("overrides.%s=%r want %r" % (key, overrides.get(key), value))
    if vals.get(key) != value:
        errs.append("values.%s=%r want %r" % (key, vals.get(key), value))
for key, default in defs.items():
    if key not in want and vals.get(key) != default:
        errs.append("values.%s=%r want default %r" % (key, vals.get(key), default))
if errs:
    sys.exit("; ".join(errs))
print("explicit overrides %s; values merge over defaults" % sorted(want))
PY
}

# settings_clean_ok <file>: no explicit overrides (fresh start or after reset).
settings_clean_ok() {
  python3 - "$1" <<'PY'
import json
import sys

try:
    d = json.load(open(sys.argv[1]))
except Exception as exc:
    sys.exit("invalid JSON: %s" % exc)
errs = []
defs, vals, overrides = d.get("defaults", {}), d.get("values", {}), d.get("overrides", {})
if vals != defs:
    errs.append("values != defaults after reset")
zero = {
    "segment_mode": "", "segment_minutes": 0, "segment_size_mb": 0, "segment_by_title": False,
    "min_file_size_mb": 0, "filename_template": "", "title_filter_patterns": "",
    "save_stream_cover": False,
    "danmaku_types": {"raw_events": False, "super_chat": False, "gift": False, "guard": False},
    "danmaku_transport": "",
    "flv_write_metadata": False, "flv_split_on_script_tag": False, "flv_disable_split_on_annexb": False,
    "bili_live_api_host": "", "bili_live_api_fallback_host": "", "bili_cdn": [],
    "bili_cdn_fallback": False, "bili_force_source": False, "bili_replace_cn01": [],
}
for key, want in zero.items():
    got = overrides.get(key)
    if want == [] and got is None:
        continue
    if got != want:
        errs.append("overrides.%s=%r want zero value %r" % (key, got, want))
if errs:
    sys.exit("; ".join(errs))
print("values == defaults and no explicit overrides remain")
PY
}

# roomcfg_initial_ok <file>: seeded (v0.2.0-format) room -> all new fields inherit.
roomcfg_initial_ok() {
  python3 - "$1" <<'PY'
import json
import sys

d = json.load(open(sys.argv[1]))
extended = [
    "segment_mode", "segment_minutes", "segment_size_mb", "segment_by_title",
    "min_file_size_mb", "filename_template", "title_filter_patterns", "save_stream_cover",
    "danmaku_types", "danmaku_transport", "flv_write_metadata", "flv_split_on_script_tag",
    "flv_disable_split_on_annexb", "bili_live_api_host", "bili_live_api_fallback_host",
    "bili_cdn", "bili_cdn_fallback", "bili_force_source", "bili_replace_cn01",
]
errs = []
for key in extended:
    if d.get(key) not in (None, [], {}):
        errs.append("inherited field %s=%r should be nil/omitted" % (key, d.get(key)))
for key in ("room_id", "auto_record", "notify", "record_duration_minutes", "qn",
            "only_audio", "record_danmaku", "stream_profiles", "delete_oldest_on_low_disk"):
    if key not in d:
        errs.append("missing legacy field %s" % key)
if errs:
    sys.exit("; ".join(errs))
print("seeded room: all 19 extended fields inherit (nil), legacy fields intact")
PY
}

# roomcfg_set_ok <file> <expected-json-subset>
roomcfg_set_ok() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

d = json.load(open(sys.argv[1]))
want = json.loads(sys.argv[2])
errs = []
for key, value in want.items():
    if d.get(key) != value:
        errs.append("%s=%r want %r" % (key, d.get(key), value))
if errs:
    sys.exit("; ".join(errs))
print("room config round-trip: %d fields match" % len(want))
PY
}

# roomcfg_cleared_ok <file>: explicit nulls clear overrides, siblings survive.
roomcfg_cleared_ok() {
  python3 - "$1" <<'PY'
import json
import sys

d = json.load(open(sys.argv[1]))
errs = []
for key in ("segment_mode", "danmaku_types"):
    if d.get(key) not in (None,):
        errs.append("%s=%r should be cleared to inherit" % (key, d.get(key)))
if d.get("filename_template") != "room-{title}":
    errs.append("filename_template=%r should survive" % d.get("filename_template"))
if d.get("segment_size_mb") != 64:
    errs.append("segment_size_mb=%r should survive" % d.get("segment_size_mb"))
if errs:
    sys.exit("; ".join(errs))
print("null/absent clears selected overrides to inherit, siblings kept")
PY
}

# start_backend <pidfile> <logfile> <env...>
start_backend() {
  local pidfile=$1 logfile=$2
  shift 2
  if command -v setsid >/dev/null 2>&1; then
    setsid bash -c 'echo $$ > "$1"; shift; exec env "$@"' _ "$pidfile" "$@" >>"$logfile" 2>&1 &
  else
    env "$@" >>"$logfile" 2>&1 &
    echo $! >"$pidfile"
  fi
}

# stop_backend <pidfile>
stop_backend() {
  local pidfile=$1 pid
  [[ -f "$pidfile" ]] || return 0
  pid=$(cat "$pidfile" 2>/dev/null || true)
  [[ -n "$pid" ]] || return 0
  kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
  for _ in $(seq 1 50); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
  done
  kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
  rm -f "$pidfile"
}

# wait_backend_ready <port> <timeout-secs>; uses $BACKEND_PIDFILE for liveness.
wait_backend_ready() {
  local port=$1 timeout=$2 i=0
  while (( i < timeout * 5 )); do
    if ! kill -0 "$(cat "$BACKEND_PIDFILE" 2>/dev/null || echo 0)" 2>/dev/null; then
      return 1
    fi
    if curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:$port/version" 2>/dev/null; then
      return 0
    fi
    sleep 0.2
    i=$((i + 1))
  done
  return 1
}

# login <user> <pass> <jar>: POST /login, prints HTTP status; body in login.json.
login() {
  curl -sS -c "$3" -o "$WORK/out/login.json" -w '%{http_code}' --max-time 10 \
    -H 'Content-Type: application/json' \
    --data-binary "{\"user\":\"$1\",\"pass\":\"$2\"}" \
    "http://127.0.0.1:$BPORT/login"
}

# ---------------------------------------------------------------------------
# 0. prerequisites
# ---------------------------------------------------------------------------
say "preflight"
for cmd in curl ffmpeg ffprobe streamlink python3 make timeout; do
  if command -v "$cmd" >/dev/null 2>&1; then
    pass "found $cmd ($(command -v "$cmd"))"
  else
    die "missing required command: $cmd"
  fi
done

if [[ "$E2E_SKIP_BUILD" != "1" ]]; then
  say "make build"
  BUILD_LOG="$(mktemp "${TMPDIR:-/tmp}/bilirec-e2e-build.XXXXXX.log")"
  if run_capture "$BUILD_LOG" make build; then
    pass "make build succeeded"
  else
    tail -40 "$BUILD_LOG" >&2 || true
    rm -f "$BUILD_LOG"
    die "make build failed (see output above)"
  fi
  rm -f "$BUILD_LOG"
else
  skip "make build (E2E_SKIP_BUILD=1)"
fi
if [[ -x "$ROOT/dist/bilirec" ]]; then
  pass "dist/bilirec built"
else
  die "dist/bilirec missing (run make build)"
fi
if [[ "$E2E_SKIP_FLVREPAIR" == "1" ]]; then
  skip "flvrepair binary check (E2E_SKIP_FLVREPAIR=1)"
elif [[ -x "$FLVREPAIR" ]]; then
  pass "flvrepair binary: $FLVREPAIR"
else
  die "flvrepair binary missing: $FLVREPAIR (run make build)"
fi

WORK=$(mktemp -d "${TMPDIR:-/tmp}/bilirec-e2e.XXXXXX")
ORIGIN_LOG="$WORK/origin.log"
BACKEND_LOG="$WORK/backend.log"
mkdir -p "$WORK/flv" "$WORK/out" "$WORK/backend/records" "$WORK/backend/database" "$WORK/backend/secrets"
API_BODY="$WORK/out/api-body.json"
pass "work dir: $WORK"

# ---------------------------------------------------------------------------
# 1. start the local origin
# ---------------------------------------------------------------------------
say "local replay origin"
OPORT="${E2E_ORIGIN_PORT:-$(free_port)}"
(
  cd "$ROOT"
  exec env ORIGIN_PORT="$OPORT" THROTTLE_BPS="$THROTTLE_BPS" ORIGIN_FLV_ROOT="$WORK/flv" \
    python3 scripts/origin.py
) >"$ORIGIN_LOG" 2>&1 &
ORIGIN_PID=$!
if wait_http "http://127.0.0.1:$OPORT/healthz" 15; then
  pass "origin listening on :$OPORT (throttle=${THROTTLE_BPS} B/s)"
else
  tail -30 "$ORIGIN_LOG" >&2 || true
  die "origin did not become healthy"
fi

ORIGIN="http://127.0.0.1:$OPORT"

# HLS playlist content type + relative segments
for variant in ts fmp4; do
  playlist_url="$ORIGIN/hls/$variant/playlist.m3u8"
  body=$(curl -fsS "$playlist_url") || die "GET $playlist_url failed"
  if grep -q '^#EXTM3U' <<<"$body" && ! grep -q '^http' <<<"$body"; then
    pass "$variant playlist served with relative URIs"
  else
    fail "$variant playlist is missing or contains absolute URIs"
  fi
  ctype=$(curl -fsS -D - -o /dev/null "$playlist_url" | tr -d '\r' |
    awk 'tolower($1)=="content-type:" {print $2; exit}')
  [[ "$ctype" == "application/vnd.apple.mpegurl" ]] &&
    pass "$variant playlist content-type: $ctype" ||
    fail "$variant playlist content-type: $ctype"
done

# ---------------------------------------------------------------------------
# 2. downloader command lines (ffmpeg + streamlink hybrid) -> valid FLV
# ---------------------------------------------------------------------------
say "downloaders: ffmpeg + streamlink hybrid against origin"

ffmpeg_hls_to_flv() { # <variant> <out>
  local variant=$1 out=$2
  timeout 120 ffmpeg -hide_banner -loglevel error -y \
    -headers $'User-Agent: bilirec-e2e\r\nReferer: http://127.0.0.1/\r\n' \
    -rw_timeout 20000000 -max_reload 1000 -fflags +genpts \
    -i "$ORIGIN/hls/$variant/playlist.m3u8" \
    -c copy -reset_timestamps 1 -avoid_negative_ts make_zero -f flv "$out"
}

for variant in ts fmp4; do
  out="$WORK/out/ffmpeg-$variant.flv"
  if run_capture "$WORK/out/ffmpeg-$variant.log" ffmpeg_hls_to_flv "$variant" "$out"; then
    if is_flv "$out" && duration_ok "$out" 11 13; then
      pass "ffmpeg downloader ($variant HLS) -> valid FLV ($(stat -c%s "$out") bytes, 12s)"
    else
      fail "ffmpeg downloader ($variant HLS) produced invalid FLV"
    fi
  else
    tail -10 "$WORK/out/ffmpeg-$variant.log" >&2 || true
    fail "ffmpeg downloader ($variant HLS) exited non-zero"
  fi
done

streamlink_hybrid_to_flv() { # <variant> <out> <stderr-log>
  local variant=$1 out=$2 errlog=$3
  timeout 120 bash -c '
    set -o pipefail
    streamlink --loglevel error --stream-segment-threads 3 \
      --hls-playlist-reload-attempts 1 "$1" best -O 2>"$2" |
      ffmpeg -hide_banner -loglevel error -y -fflags +genpts -i pipe:0 \
        -c copy -reset_timestamps 1 -avoid_negative_ts make_zero -f flv "$3"
  ' _ "$ORIGIN/hls/$variant/playlist.m3u8" "$errlog" "$out"
}

for variant in ts fmp4; do
  out="$WORK/out/streamlink-$variant.flv"
  errlog="$WORK/out/streamlink-$variant.err"
  if run_capture "$WORK/out/streamlink-$variant.log" streamlink_hybrid_to_flv "$variant" "$out" "$errlog"; then
    if is_flv "$out" && duration_ok "$out" 11 13; then
      pass "streamlink|ffmpeg hybrid ($variant HLS) -> valid FLV ($(stat -c%s "$out") bytes)"
    else
      fail "streamlink|ffmpeg hybrid ($variant HLS) produced invalid FLV"
    fi
  else
    tail -10 "$errlog" >&2 || true
    fail "streamlink|ffmpeg hybrid ($variant HLS) exited non-zero"
  fi
done

# ---------------------------------------------------------------------------
# 3. /flv replay + fault injection
# ---------------------------------------------------------------------------
say "/flv streaming + fault injection"

BASE_FLV="$WORK/flv/base.flv"
if run_capture "$WORK/out/mkbase.log" ffmpeg -hide_banner -loglevel error -y \
    -i "$ORIGIN/hls/ts/playlist.m3u8" -c copy -f flv "$BASE_FLV"; then
  pass "generated /flv fixture from origin HLS ($(stat -c%s "$BASE_FLV") bytes)"
else
  tail -10 "$WORK/out/mkbase.log" >&2 || true
  die "could not generate FLV fixture for /flv tests"
fi
BASE_SIZE=$(stat -c%s "$BASE_FLV")

# full throttled download
t0=$(date +%s%N)
if curl -fsS -o "$WORK/out/dl-full.flv" "$ORIGIN/flv/base.flv"; then
  t1=$(date +%s%N)
  elapsed_ms=$(( (t1 - t0) / 1000000 ))
  if cmp -s "$BASE_FLV" "$WORK/out/dl-full.flv"; then
    pass "/flv full download byte-identical (${BASE_SIZE} B in ${elapsed_ms} ms)"
  else
    fail "/flv full download differs from source file"
  fi
  if (( elapsed_ms >= 200 )); then
    pass "/flv throttle applied (${elapsed_ms} ms for ${BASE_SIZE} B at ${THROTTLE_BPS} B/s)"
  else
    fail "/flv download was not throttled (${elapsed_ms} ms)"
  fi
else
  fail "/flv full download failed"
fi

# HEAD
head_len=$(curl -fsS -I "$ORIGIN/flv/base.flv" | tr -d '\r' |
  awk 'tolower($1)=="content-length:" {print $2; exit}')
[[ "$head_len" == "$BASE_SIZE" ]] &&
  pass "/flv HEAD Content-Length=$head_len" ||
  fail "/flv HEAD Content-Length=$head_len (want $BASE_SIZE)"

# ?drop_after -> connection aborted mid-stream
DROP_AFTER=$(( BASE_SIZE / 3 ))
curl -sS --max-time 30 -o "$WORK/out/dl-drop.flv" \
  "$ORIGIN/flv/base.flv?drop_after=$DROP_AFTER" 2>"$WORK/out/drop.err"
drop_rc=$?
drop_size=$(stat -c%s "$WORK/out/dl-drop.flv" 2>/dev/null || echo 0)
if (( drop_rc != 0 )) && (( drop_size == DROP_AFTER )); then
  pass "?drop_after=$DROP_AFTER aborted mid-stream (curl rc=$drop_rc, ${drop_size} B)"
else
  fail "?drop_after=$DROP_AFTER: curl rc=$drop_rc size=$drop_size (want rc!=0, size=$DROP_AFTER)"
fi
if is_flv "$WORK/out/dl-drop.flv"; then
  pass "truncated stream still parses as FLV"
else
  fail "truncated stream does not parse as FLV"
fi

# ?corrupt=1 -> same size, bounded region changed
curl -fsS -o "$WORK/out/dl-corrupt.flv" "$ORIGIN/flv/base.flv?corrupt=1" ||
  fail "?corrupt=1 download failed"
corrupt_size=$(stat -c%s "$WORK/out/dl-corrupt.flv" 2>/dev/null || echo 0)
if (( corrupt_size == BASE_SIZE )) && ! cmp -s "$BASE_FLV" "$WORK/out/dl-corrupt.flv"; then
  pass "?corrupt=1 returned same-size but modified bytes (${corrupt_size} B)"
else
  fail "?corrupt=1 size=$corrupt_size (want $BASE_SIZE) or bytes unchanged"
fi

# ---------------------------------------------------------------------------
# 4. flvrepair over HTTP: stream / file / analyze
# ---------------------------------------------------------------------------
if [[ "$E2E_SKIP_FLVREPAIR" == "1" ]]; then
  skip "flvrepair sections (E2E_SKIP_FLVREPAIR=1)"
else
  say "flvrepair file/stream/analyze over HTTP"

  if v=$("$FLVREPAIR" --version 2>&1); then
    if grep -q "flvrepair" <<<"$v"; then
      pass "flvrepair --version: $v"
    else
      fail "unexpected flvrepair --version output: $v"
    fi
  else
    fail "flvrepair --version exited non-zero"
  fi

  # analyze clean input
  if run_capture "$WORK/out/analyze-clean.json" "$FLVREPAIR" analyze --input "$BASE_FLV" --json; then
    if json_version_1 <"$WORK/out/analyze-clean.json"; then
      pass "flvrepair analyze --json (clean) -> schema version 1"
    else
      fail "flvrepair analyze --json (clean) -> not schema version 1"
    fi
  else
    fail "flvrepair analyze --json (clean) exited non-zero"
  fi

  # file repair on clean input
  if timeout 120 "$FLVREPAIR" file --input "$BASE_FLV" --output "$WORK/out/repair-clean.flv" \
      >"$WORK/out/repair-clean.log" 2>&1; then
    if is_flv "$WORK/out/repair-clean.flv" && duration_ok "$WORK/out/repair-clean.flv" 11 13; then
      if clean_no_growth "$BASE_FLV" "$WORK/out/repair-clean.flv"; then
        pass "flvrepair file (clean) -> valid FLV, no significant growth"
      else
        fail "flvrepair file (clean) grew more than 1%: $(stat -c%s "$BASE_FLV") -> $(stat -c%s "$WORK/out/repair-clean.flv")"
      fi
    else
      fail "flvrepair file (clean) produced invalid FLV"
    fi
  else
    fail "flvrepair file (clean) exited non-zero"
  fi

  # file repair on HTTP-triggered corruption
  if timeout 120 "$FLVREPAIR" file --input "$WORK/out/dl-corrupt.flv" \
      --output "$WORK/out/repair-corrupt.flv" >"$WORK/out/repair-corrupt.log" 2>&1; then
    if is_flv "$WORK/out/repair-corrupt.flv"; then
      pass "flvrepair file (?corrupt=1) -> still valid FLV ($(stat -c%s "$WORK/out/repair-corrupt.flv") B)"
    else
      fail "flvrepair file (?corrupt=1) produced invalid FLV"
    fi
  else
    fail "flvrepair file (?corrupt=1) exited non-zero"
  fi

  # file repair on HTTP-truncated input
  if timeout 120 "$FLVREPAIR" file --input "$WORK/out/dl-drop.flv" \
      --output "$WORK/out/repair-drop.flv" >"$WORK/out/repair-drop.log" 2>&1; then
    if is_flv "$WORK/out/repair-drop.flv"; then
      pass "flvrepair file (?drop_after) -> valid FLV from truncated input ($(stat -c%s "$WORK/out/repair-drop.flv") B)"
    else
      fail "flvrepair file (?drop_after) produced invalid FLV"
    fi
  else
    fail "flvrepair file (?drop_after) exited non-zero"
  fi

  # stream mode: clean HTTP stream -> stdout
  if ( curl -fsS "$ORIGIN/flv/base.flv" | timeout 120 "$FLVREPAIR" stream --mode best-effort >"$WORK/out/repair-stream.flv" ) \
      2>"$WORK/out/repair-stream.log"; then
    if is_flv "$WORK/out/repair-stream.flv" && duration_ok "$WORK/out/repair-stream.flv" 11 13; then
      pass "flvrepair stream (clean HTTP) -> valid FLV ($(stat -c%s "$WORK/out/repair-stream.flv") B)"
    else
      fail "flvrepair stream (clean HTTP) produced invalid FLV"
    fi
  else
    tail -10 "$WORK/out/repair-stream.log" >&2 || true
    fail "flvrepair stream (clean HTTP) exited non-zero"
  fi

  # stream mode: HTTP drop mid-stream (curl exits 18; expected failure upstream)
  if ( { curl -sS --max-time 30 "$ORIGIN/flv/base.flv?drop_after=$DROP_AFTER" || true; } |
       timeout 120 "$FLVREPAIR" stream --mode best-effort >"$WORK/out/repair-stream-drop.flv" ) \
      2>"$WORK/out/repair-stream-drop.log"; then
    if is_flv "$WORK/out/repair-stream-drop.flv"; then
      pass "flvrepair stream (HTTP drop) -> best-effort valid FLV ($(stat -c%s "$WORK/out/repair-stream-drop.flv") B)"
    else
      fail "flvrepair stream (HTTP drop) produced invalid FLV"
    fi
  else
    fail "flvrepair stream (HTTP drop) exited non-zero (best-effort must exit 0)"
  fi

  # analyze on repaired output
  if run_capture "$WORK/out/analyze-repaired.json" "$FLVREPAIR" analyze --input "$WORK/out/repair-stream.flv" --json; then
    if json_version_1 <"$WORK/out/analyze-repaired.json"; then
      pass "flvrepair analyze --json (repaired output) -> schema version 1"
    else
      fail "flvrepair analyze --json (repaired output) -> not schema version 1"
    fi
  else
    fail "flvrepair analyze --json (repaired output) exited non-zero"
  fi
fi

# ---------------------------------------------------------------------------
# 5. bilirec backend (anonymous, temp dirs, random port) + FEATURES-V2 settings
# ---------------------------------------------------------------------------
say "bilirec backend startup + REST API"

BPORT="${E2E_BACKEND_PORT:-$(free_port)}"
BACKEND_PIDFILE="$WORK/backend.pid"
SEED_ROOM_ID="$E2E_SEED_ROOM_ID"

BASE_ENV=(
  "BILIBILI_LOGIN_MODE=anonymous"
  "PORT=$BPORT"
  "HOST=127.0.0.1"
  "METRICS_ENABLED=false"
  "OUTPUT_DIR=$WORK/backend/records"
  "DATABASE_DIR=$WORK/backend/database"
  "SECRET_DIR=$WORK/backend/secrets"
  "JWT_SECRET=e2e-secret"
  "MAX_CONCURRENT_RECORDINGS=2"
  "TZ=UTC"
  "USERNAME="
  "PASSWORD="
)
if [[ -n "$E2E_DOWNLOADER" ]]; then
  BASE_ENV+=("DOWNLOADER=$E2E_DOWNLOADER")
fi

# FEATURES-V2 env defaults. Valid values must show up in GET /settings.defaults;
# deliberately invalid values (DANMAKU_TRANSPORT=tcp, BILI_CDN=bad/entry) must
# fall back to the built-in defaults (docs/FEATURES-V2.md §1.1).
V2_ENV=(
  "SEGMENT_MODE=time"
  "SEGMENT_MINUTES=15"
  "SEGMENT_SIZE_MB=300"
  "SEGMENT_BY_TITLE=false"
  "MIN_FILE_SIZE_MB=0"
  "FILENAME_TEMPLATE=e2e-{roomid}-{date}"
  "TITLE_FILTER_PATTERNS=^e2e-filter$"
  "SAVE_STREAM_COVER=false"
  "DANMAKU_RECORD_TYPES=super_chat,gift"
  "DANMAKU_TRANSPORT=tcp"
  "FLV_WRITE_METADATA=false"
  "FLV_SPLIT_ON_SCRIPT_TAG=true"
  "FLV_DISABLE_SPLIT_ON_ANNEXB=false"
  "BILI_LIVE_API_HOST=https://api.live.bilibili.com"
  "BILI_CDN=bad/entry"
  "BILI_CDN_FALLBACK=true"
  "BILI_FORCE_SOURCE=true"
  "TITLE_POLL_SECS=5"
)

# A subscribed room is required for the room-config API (PUT returns 404 for
# unsubscribed rooms) and the real subscribe path resolves the room through
# Bilibili. Seed subscribes.db offline; scripts/roomcfg_seed.go writes the
# v0.2.0-format gob RoomConfig (new pointer fields absent => nil => inherit),
# exactly the "old DB record" case from docs/FEATURES-V2.md §1.3.
if [[ "$E2E_SKIP_SETTINGS_V2" != "1" ]]; then
  say "seed subscribed room for offline room-config API"
  if ! command -v go >/dev/null 2>&1 && [[ -x /usr/local/go/bin/go ]]; then
    # `make e2e` exports this via the Makefile; direct `bash scripts/e2e.sh`
    # invocations may not have Go on PATH.
    export PATH="/usr/local/go/bin:$PATH"
  fi
  if ! command -v go >/dev/null 2>&1; then
    die "go is required to seed subscribes.db for offline room-config checks"
  fi
  if ( cd "$ROOT" && go run scripts/roomcfg_seed.go "$WORK/backend/database" "$SEED_ROOM_ID" ) \
      >"$WORK/out/seed.log" 2>&1; then
    pass "seeded subscribes.db with room $SEED_ROOM_ID ($(tail -1 "$WORK/out/seed.log"))"
  else
    tail -20 "$WORK/out/seed.log" >&2 || true
    die "scripts/roomcfg_seed.go failed"
  fi
fi

start_backend "$BACKEND_PIDFILE" "$BACKEND_LOG" "${BASE_ENV[@]}" "${V2_ENV[@]}" "$ROOT/dist/bilirec"
BACKEND_READY=0
if wait_backend_ready "$BPORT" 60; then
  BACKEND_READY=1
  curl -fsS -o "$WORK/out/version.json" --max-time 5 "http://127.0.0.1:$BPORT/version" || true
fi

if (( BACKEND_READY == 1 )); then
  pass "backend healthy on :$BPORT (GET /version)"
  if json_has_keys current url <"$WORK/out/version.json"; then
    pass "GET /version returned JSON with current/url fields: $(tr -d '\n' <"$WORK/out/version.json" | cut -c1-120)"
  else
    fail "GET /version body is not the expected JSON: $(head -c 200 "$WORK/out/version.json")"
  fi

  list_code=$(curl -sS -o "$WORK/out/record-list.json" -w '%{http_code}' \
    --max-time 5 "http://127.0.0.1:$BPORT/record/list")
  if [[ "$list_code" == "200" ]] && python3 -c 'import json,sys; json.load(open(sys.argv[1]))' \
      "$WORK/out/record-list.json" 2>/dev/null; then
    pass "GET /record/list -> 200 JSON ($(cat "$WORK/out/record-list.json"))"
  else
    fail "GET /record/list -> $list_code (invalid JSON?)"
  fi

  missing_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
    "http://127.0.0.1:$BPORT/no-such-endpoint")
  [[ "$missing_code" == "404" ]] &&
    pass "unknown API path -> 404 (no crash)" ||
    fail "unknown API path -> $missing_code (want 404)"

  grep -q "Starting bilirec backend" "$BACKEND_LOG" &&
    pass "backend log contains startup line" ||
    fail "backend log is missing the startup line"
else
  fail "backend did not become healthy on :$BPORT within 60s"
  tail -40 "$BACKEND_LOG" >&2 || true
fi

# ---------------------------------------------------------------------------
# 5b. FEATURES-V2: settings API (defaults/env, round-trip, validation, reset)
#     and room-config nil/inherit round-trip (auth disabled in this instance).
# ---------------------------------------------------------------------------
invalid_settings_payload() { # <name> <json>
  local name=$1 json=$2 body code
  body=$(printf '%s' "$json" | mkbody "invalid-$name")
  code=$(api PUT /settings "$body")
  expect_status 400 "$code" "PUT /settings invalid payload ($name)"
}

if [[ "$E2E_SKIP_SETTINGS_V2" == "1" ]]; then
  skip "FEATURES-V2 settings API + room config (E2E_SKIP_SETTINGS_V2=1)"
elif (( BACKEND_READY != 1 )); then
  fail "FEATURES-V2 settings API + room config: backend not healthy"
else
  say "FEATURES-V2 settings API (env defaults, round-trip, validation, reset)"
  AUTH="-"

  code=$(api GET /settings)
  expect_status 200 "$code" "GET /settings (auth disabled)"
  if out=$(settings_defaults_ok "$API_BODY" 2>&1); then
    pass "GET /settings env-seeded defaults: $out"
  else
    fail "GET /settings env-seeded defaults: $out"
  fi

  # --- validation matrix: every case must be 400 and persist nothing ------
  invalid_settings_payload seg-mode '{"segment_mode":"week"}'
  invalid_settings_payload time-minutes-low '{"segment_mode":"time","segment_minutes":0}'
  invalid_settings_payload time-minutes-high '{"segment_mode":"time","segment_minutes":1441}'
  invalid_settings_payload size-mb-low '{"segment_mode":"size","segment_size_mb":10}'
  invalid_settings_payload size-mb-high '{"segment_mode":"size","segment_size_mb":65537}'
  invalid_settings_payload min-file-size '{"min_file_size_mb":1025}'
  long_body=$(python3 -c 'import json; print(json.dumps({"filename_template": "x" * 257}))' |
    mkbody long-filename)
  code=$(api PUT /settings "$long_body")
  expect_status 400 "$code" "PUT /settings invalid payload (filename_template > 256 bytes)"
  invalid_settings_payload bad-regex '{"title_filter_patterns":"("}'
  invalid_settings_payload danmaku-tcp '{"danmaku_transport":"tcp"}'
  invalid_settings_payload api-host '{"bili_live_api_host":"not-a-url"}'
  invalid_settings_payload api-fallback-host '{"bili_live_api_fallback_host":"ftp://example.com"}'
  invalid_settings_payload cdn-bad-entry '{"bili_cdn":["bad/entry"]}'
  invalid_settings_payload cdn-too-many '{"bili_cdn":["a","b","c","d","e","f","g","h","i"]}'
  invalid_settings_payload cn01-too-many '{"bili_replace_cn01":["a","b","c","d","e","f","g","h","i"]}'

  code=$(api GET /settings)
  expect_status 200 "$code" "GET /settings after invalid payloads"
  if out=$(settings_clean_ok "$API_BODY" 2>&1); then
    pass "invalid payloads persisted nothing: $out"
  else
    fail "invalid payloads persisted something: $out"
  fi

  # --- partial round-trip; only present keys become overrides -------------
  body=$(printf '%s' '{"segment_mode":"size","segment_size_mb":512}' | mkbody patch-1)
  code=$(api PUT /settings "$body")
  expect_status 200 "$code" "PUT /settings partial (segment_mode=size, segment_size_mb=512)"
  if out=$(settings_overrides_ok "$API_BODY" '{"segment_mode":"size","segment_size_mb":512}' 2>&1); then
    pass "PUT /settings response: $out"
  else
    fail "PUT /settings response: $out"
  fi
  code=$(api GET /settings)
  expect_status 200 "$code" "GET /settings (after PUT)"
  if out=$(settings_overrides_ok "$API_BODY" '{"segment_mode":"size","segment_size_mb":512}' 2>&1); then
    pass "GET /settings persisted overrides: $out"
  else
    fail "GET /settings persisted overrides: $out"
  fi

  # --- unknown keys ignored, later partial patches keep earlier overrides --
  body=$(printf '%s' '{"nonsense_key":123,"segment_minutes":45}' | mkbody patch-unknown)
  code=$(api PUT /settings "$body")
  expect_status 200 "$code" "PUT /settings with unknown key (ignored)"
  if out=$(settings_overrides_ok "$API_BODY" \
      '{"segment_mode":"size","segment_size_mb":512,"segment_minutes":45}' 2>&1); then
    pass "unknown key ignored, previous overrides kept: $out"
  else
    fail "unknown key handling: $out"
  fi

  # --- segmentation boundaries + naming template accepted (API-level checks) ---
  body=$(printf '%s' '{"segment_mode":"time","segment_minutes":1440}' | mkbody bound-time)
  code=$(api PUT /settings "$body")
  expect_status 200 "$code" "PUT /settings boundary (segment_mode=time, segment_minutes=1440)"
  if out=$(settings_overrides_ok "$API_BODY" \
      '{"segment_mode":"time","segment_minutes":1440,"segment_size_mb":512}' 2>&1); then
    pass "segment time upper bound accepted: $out"
  else
    fail "segment time upper bound: $out"
  fi
  body=$(printf '%s' '{"segment_mode":"size","segment_size_mb":16}' | mkbody bound-size)
  code=$(api PUT /settings "$body")
  expect_status 200 "$code" "PUT /settings boundary (segment_mode=size, segment_size_mb=16)"
  if out=$(settings_overrides_ok "$API_BODY" \
      '{"segment_mode":"size","segment_size_mb":16,"segment_minutes":1440}' 2>&1); then
    pass "segment size lower bound accepted: $out"
  else
    fail "segment size lower bound: $out"
  fi
  body=$(printf '%s' \
    '{"filename_template":"{uname}-{roomid}-{title}-{year}{month}{day}_{hour}{minute}{second}-{segment}"}' |
    mkbody naming-template)
  code=$(api PUT /settings "$body")
  expect_status 200 "$code" "PUT /settings naming template with all documented vars"
  if out=$(settings_overrides_ok "$API_BODY" \
      '{"segment_mode":"size","segment_size_mb":16,"segment_minutes":1440,"filename_template":"{uname}-{roomid}-{title}-{year}{month}{day}_{hour}{minute}{second}-{segment}"}' 2>&1); then
    pass "naming template round-trip: $out"
  else
    fail "naming template round-trip: $out"
  fi

  # --- reset clears overrides, values fall back to env/built-in defaults ---
  code=$(api POST /settings/reset)
  expect_status 200 "$code" "POST /settings/reset"
  code=$(api GET /settings)
  expect_status 200 "$code" "GET /settings (after reset)"
  if out=$(settings_clean_ok "$API_BODY" 2>&1); then
    pass "reset cleared overrides: $out"
  else
    fail "reset did not clear overrides: $out"
  fi

  # --- leave an override for the restart/persistence check (phase B) ------
  body=$(printf '%s' \
    '{"segment_mode":"time","segment_minutes":30,"filename_template":"persist-{roomid}"}' |
    mkbody patch-persist)
  code=$(api PUT /settings "$body")
  expect_status 200 "$code" "PUT /settings (persistence marker for restart check)"
  if out=$(settings_overrides_ok "$API_BODY" \
      '{"segment_mode":"time","segment_minutes":30,"filename_template":"persist-{roomid}"}' 2>&1); then
    pass "persistence marker overrides: $out"
  else
    fail "persistence marker overrides: $out"
  fi

  # --- room config: nil/inherit round-trip on the offline-seeded room -----
  say "FEATURES-V2 room config nil/inherit round-trip (seeded room $SEED_ROOM_ID)"
  code=$(api GET "/room/$SEED_ROOM_ID/config")
  expect_status 200 "$code" "GET /room/$SEED_ROOM_ID/config (seeded subscription)"
  if out=$(roomcfg_initial_ok "$API_BODY" 2>&1); then
    pass "seeded room config inherits everything: $out"
  else
    fail "seeded room config inheritance: $out"
  fi
  code=$(api GET "/room/99999/config")
  expect_status 404 "$code" "GET /room/99999/config (not subscribed)"

  python3 - "$WORK/out/roomcfg-put.json" <<'PY'
import json
import sys

cfg = {
    "auto_record": True,
    "notify": False,
    "record_duration_minutes": 0,
    "qn": 0,
    "only_audio": False,
    "record_danmaku": False,
    "stream_profiles": [],
    "delete_oldest_on_low_disk": False,
    "segment_mode": "size",
    "segment_minutes": 20,
    "segment_size_mb": 64,
    "segment_by_title": True,
    "min_file_size_mb": 5,
    "filename_template": "room-{title}",
    "title_filter_patterns": "^room$",
    "save_stream_cover": True,
    "danmaku_types": {"raw_events": True, "super_chat": False, "gift": True, "guard": False},
    "danmaku_transport": "ws",
    "flv_write_metadata": False,
    "flv_split_on_script_tag": True,
    "flv_disable_split_on_annexb": False,
    "bili_live_api_host": "https://live.example.com",
    "bili_live_api_fallback_host": "https://live2.example.com",
    "bili_cdn": ["cn-1"],
    "bili_cdn_fallback": True,
    "bili_force_source": True,
    "bili_replace_cn01": ["a.example.com"],
}
json.dump(cfg, open(sys.argv[1], "w"), ensure_ascii=False)
PY
  code=$(api PUT "/room/$SEED_ROOM_ID/config" "$WORK/out/roomcfg-put.json")
  expect_status 200 "$code" "PUT /room/$SEED_ROOM_ID/config (all extended fields)"
  roomcfg_expect='{"segment_mode":"size","segment_minutes":20,"segment_size_mb":64,"segment_by_title":true,"min_file_size_mb":5,"filename_template":"room-{title}","title_filter_patterns":"^room$","save_stream_cover":true,"danmaku_types":{"raw_events":true,"super_chat":false,"gift":true,"guard":false},"danmaku_transport":"ws","flv_write_metadata":false,"flv_split_on_script_tag":true,"flv_disable_split_on_annexb":false,"bili_live_api_host":"https://live.example.com","bili_live_api_fallback_host":"https://live2.example.com","bili_cdn":["cn-1"],"bili_cdn_fallback":true,"bili_force_source":true,"bili_replace_cn01":["a.example.com"],"auto_record":true}'
  if out=$(roomcfg_set_ok "$API_BODY" "$roomcfg_expect" 2>&1); then
    pass "PUT room config response: $out"
  else
    fail "PUT room config response: $out"
  fi
  code=$(api GET "/room/$SEED_ROOM_ID/config")
  expect_status 200 "$code" "GET /room/$SEED_ROOM_ID/config (after PUT)"
  if out=$(roomcfg_set_ok "$API_BODY" "$roomcfg_expect" 2>&1); then
    pass "room config round-trip persisted: $out"
  else
    fail "room config round-trip persisted: $out"
  fi

  # null/absent => inherit again; sibling overrides survive (full-replace PUT).
  python3 - "$WORK/out/roomcfg-put.json" "$WORK/out/roomcfg-clear.json" <<'PY'
import json
import sys

cfg = json.load(open(sys.argv[1]))
cfg["segment_mode"] = None
cfg["danmaku_types"] = None
json.dump(cfg, open(sys.argv[2], "w"), ensure_ascii=False)
PY
  code=$(api PUT "/room/$SEED_ROOM_ID/config" "$WORK/out/roomcfg-clear.json")
  expect_status 200 "$code" "PUT room config (segment_mode=null, danmaku_types=null -> inherit)"
  code=$(api GET "/room/$SEED_ROOM_ID/config")
  expect_status 200 "$code" "GET /room/$SEED_ROOM_ID/config (after clearing)"
  if out=$(roomcfg_cleared_ok "$API_BODY" 2>&1); then
    pass "null clears selected overrides: $out"
  else
    fail "null clearing semantics: $out"
  fi
fi

# ---------------------------------------------------------------------------
# 6. opt-in real live recording through the REST API
# ---------------------------------------------------------------------------
say "live recording (opt-in)"
if [[ -z "$E2E_LIVE_ROOM_ID" ]]; then
  skip "live recording: set E2E_LIVE_ROOM_ID=<live room> to enable (needs outbound network + a live room)"
else
  if (( BACKEND_READY != 1 )); then
    fail "live recording requested but backend is not healthy"
  else
    live_code=$(curl -sS -o "$WORK/out/live-start.json" -w '%{http_code}' --max-time 30 \
      -X POST "http://127.0.0.1:$BPORT/record/$E2E_LIVE_ROOM_ID/start")
    if [[ "$live_code" == "200" ]]; then
      pass "POST /record/$E2E_LIVE_ROOM_ID/start -> 200"
      live_flv=""
      for _ in $(seq 1 "$E2E_LIVE_WAIT_SECS"); do
        live_flv=$(find "$WORK/backend/records" -type f -name '*.flv' -size +0 2>/dev/null | head -1)
        [[ -n "$live_flv" ]] && break
        sleep 1
      done
      curl -sS -o /dev/null --max-time 30 -X POST \
        "http://127.0.0.1:$BPORT/record/$E2E_LIVE_ROOM_ID/stop" || true
      sleep 2
      if [[ -n "$live_flv" ]]; then
        pass "recording produced $live_flv ($(stat -c%s "$live_flv") bytes)"
        if is_flv "$live_flv"; then
          pass "live recording output parses as FLV"
        else
          fail "live recording output does not parse as FLV"
        fi
        if "$FLVREPAIR" analyze --input "$live_flv" --json 2>/dev/null | json_version_1; then
          pass "flvrepair analyze --json (live recording) -> schema version 1"
        else
          fail "flvrepair analyze --json (live recording) failed"
        fi
      else
        fail "no .flv appeared in OUTPUT_DIR within ${E2E_LIVE_WAIT_SECS}s"
      fi
      for field in room downloader repair source_format; do
        if grep -q "$field" "$BACKEND_LOG"; then
          pass "backend log mentions session field: $field"
        else
          fail "backend log missing session field: $field"
        fi
      done
    else
      fail "POST /record/$E2E_LIVE_ROOM_ID/start -> $live_code: $(head -c 300 "$WORK/out/live-start.json")"
    fi
  fi
fi

# ---------------------------------------------------------------------------
# 7. FEATURES-V2 auth matrix + settings/room-config persistence across restart
#    Restart the same DATABASE_DIR with USERNAME/PASSWORD + VIEWER_* and verify
#    401/403/200 behaviour plus persistence of the phase 5b overrides.
# ---------------------------------------------------------------------------
if [[ "$E2E_SKIP_SETTINGS_V2" == "1" ]]; then
  skip "FEATURES-V2 auth matrix + persistence (E2E_SKIP_SETTINGS_V2=1)"
else
  say "FEATURES-V2 auth matrix + persistence across restart"
  stop_backend "$BACKEND_PIDFILE"
  AUTH_LOG="$WORK/backend-auth.log"
  AUTH_ENV=(
    "USERNAME=$E2E_ADMIN_USER"
    "PASSWORD=$E2E_ADMIN_PASS"
    "VIEWER_USERNAME=$E2E_VIEWER_USER"
    "VIEWER_PASSWORD=$E2E_VIEWER_PASS"
  )
  start_backend "$BACKEND_PIDFILE" "$AUTH_LOG" "${BASE_ENV[@]}" "${V2_ENV[@]}" "${AUTH_ENV[@]}" "$ROOT/dist/bilirec"
  BACKEND_READY=0
  if wait_backend_ready "$BPORT" 60; then
    BACKEND_READY=1
  fi
  if (( BACKEND_READY != 1 )); then
    fail "backend did not restart with auth on :$BPORT within 60s"
    tail -40 "$AUTH_LOG" >&2 || true
  else
    pass "backend restarted with JWT auth on :$BPORT (same DATABASE_DIR)"
    ADMIN_JAR="$WORK/admin.cookies"
    VIEWER_JAR="$WORK/viewer.cookies"

    # /version stays public; protected endpoints require a session cookie.
    AUTH="-"
    code=$(api GET /version)
    expect_status 200 "$code" "GET /version without token (public)"
    code=$(api GET /settings)
    expect_status 401 "$code" "GET /settings without token"
    code=$(api GET /record/list)
    expect_status 401 "$code" "GET /record/list without token"

    code=$(login "$E2E_ADMIN_USER" "definitely-wrong" "$WORK/bad.cookies")
    expect_status 401 "$code" "POST /login wrong password"

    code=$(login "$E2E_ADMIN_USER" "$E2E_ADMIN_PASS" "$ADMIN_JAR")
    expect_status 200 "$code" "POST /login admin ($E2E_ADMIN_USER)"
    json_assert "$WORK/out/login.json" role '"admin"' "admin login returns role=admin"
    code=$(login "$E2E_VIEWER_USER" "$E2E_VIEWER_PASS" "$VIEWER_JAR")
    expect_status 200 "$code" "POST /login viewer ($E2E_VIEWER_USER)"
    json_assert "$WORK/out/login.json" role '"viewer"' "viewer login returns role=viewer"

    # Persistence across the restart (overrides written in phase 5b).
    AUTH="$ADMIN_JAR"
    code=$(api GET /settings)
    expect_status 200 "$code" "GET /settings as admin"
    if out=$(settings_overrides_ok "$API_BODY" \
        '{"segment_mode":"time","segment_minutes":30,"filename_template":"persist-{roomid}"}' 2>&1); then
      pass "global overrides persisted across restart: $out"
    else
      fail "global overrides not persisted across restart: $out"
    fi
    json_assert "$API_BODY" defaults.segment_mode '"time"' \
      "env defaults unchanged after restart (defaults.segment_mode=time)"

    code=$(api GET "/room/$SEED_ROOM_ID/config")
    expect_status 200 "$code" "GET room config as admin (after restart)"
    if out=$(roomcfg_cleared_ok "$API_BODY" 2>&1); then
      pass "room overrides persisted across restart: $out"
    else
      fail "room overrides not persisted across restart: $out"
    fi

    # Viewer is read-only.
    AUTH="$VIEWER_JAR"
    code=$(api GET /settings)
    expect_status 200 "$code" "GET /settings as viewer"
    body=$(printf '%s' '{"segment_minutes":45}' | mkbody viewer-put)
    code=$(api PUT /settings "$body")
    expect_status 403 "$code" "PUT /settings as viewer (forbidden)"
    code=$(api POST /settings/reset)
    expect_status 403 "$code" "POST /settings/reset as viewer (forbidden)"
    code=$(api PUT "/room/$SEED_ROOM_ID/config" "$WORK/out/roomcfg-put.json")
    expect_status 403 "$code" "PUT room config as viewer (forbidden)"

    # Admin can write; reset clears overrides again.
    AUTH="$ADMIN_JAR"
    code=$(api PUT /settings "$body")
    expect_status 200 "$code" "PUT /settings as admin"
    if out=$(settings_overrides_ok "$API_BODY" \
        '{"segment_mode":"time","segment_minutes":45,"filename_template":"persist-{roomid}"}' 2>&1); then
      pass "admin partial PUT keeps other overrides: $out"
    else
      fail "admin partial PUT overrides: $out"
    fi
    code=$(api POST /settings/reset)
    expect_status 200 "$code" "POST /settings/reset as admin"
    code=$(api GET /settings)
    expect_status 200 "$code" "GET /settings as admin (after reset)"
    if out=$(settings_clean_ok "$API_BODY" 2>&1); then
      pass "admin reset cleared overrides: $out"
    else
      fail "admin reset: $out"
    fi

    stop_backend "$BACKEND_PIDFILE"
    AUTH="-"
  fi
fi

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------
summary
if (( FAILED > 0 )); then
  printf 'E2E FAILED: %d check(s) failed\n' "$FAILED" >&2
  exit 1
fi
printf 'E2E PASSED: all %d checks passed (%d skipped)\n' "$PASSED" "$SKIPPED"
exit 0
