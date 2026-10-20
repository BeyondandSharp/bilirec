# scripts/ — local replay origin, offline E2E & test helpers

Owned by `qa-harness` (see `docs/INTERFACES.md` §4/§6 and `docs/FEATURES-V2.md` §9).

| File | Purpose |
|---|---|
| `origin.py` | Python 3 stdlib local replay origin: throttled chunked FLV + HLS ts/fMP4 static serving + fault injection. No third-party deps. |
| `gen_hls_fixtures.sh` | Deterministically regenerate `testdata/hls/{ts,fmp4}` with ffmpeg; `--check` verifies the committed fixtures are reproducible. |
| `e2e.sh` | Offline-first end-to-end test (run via `make e2e`). |
| `roomcfg_seed.go` | Test-only helper (`//go:build ignore`): seeds a bilirec `subscribes.db` with a subscribed room so the room-config API can be tested offline. Run from the repo root so deps resolve through the root `go.mod`. |

Additional fixture generators live under `testdata/`:
`testdata/flv/gen/gen.py` (R1–R8 repair fixtures) and
`testdata/flv/gen/gen_v2_detectors.py` (FEATURES-V2 split-detector fixtures).

## origin.py

```bash
python3 scripts/origin.py                # 127.0.0.1:18080
ORIGIN_PORT=0 python3 scripts/origin.py  # ephemeral port, printed to stdout
python3 scripts/origin.py --selftest     # ephemeral server + self checks, exit 0/1
```

Environment: `ORIGIN_PORT` (18080), `ORIGIN_HOST` (127.0.0.1), `THROTTLE_BPS`
(1048576), `ORIGIN_FLV_ROOT` (`testdata/flv`), `ORIGIN_HLS_ROOT`
(`testdata/hls`).

Endpoints:

- `GET /healthz` — JSON `{"ok":true,...}` (E2E readiness probe).
- `GET /flv/<path>` — serve a file from `ORIGIN_FLV_ROOT`. HTTP/1.1 chunked and
  throttled at `THROTTLE_BPS` bytes/s (`0` = unlimited).
  - `?drop_after=<bytes>` — send exactly `<bytes>` payload bytes, then abort the
    connection mid-stream (no terminal chunk; `curl` exits 18). Values ≥ file
    size do not abort.
  - `?corrupt=1` — XOR bytes in the bounded region `[64, 64+256)` with `0xA5`,
    leaving the FLV container header intact so parsers can resync.
- `GET /hls/<path>` — static HLS files with correct content types
  (`.m3u8` → `application/vnd.apple.mpegurl`, `.ts` → `video/mp2t`,
  `.m4s` → `video/iso.segment`, `.mp4` → `video/mp4`), byte ranges (206) and
  HEAD. Playlists use relative URIs, so any host/port works.
- Path traversal (`/flv/../...`) is rejected with 404.

Example:

```bash
python3 scripts/origin.py &
# FLV fixture: put a file in testdata/flv/ or set ORIGIN_FLV_ROOT=/tmp/flv
curl -o out.flv 'http://127.0.0.1:18080/flv/base.flv'
curl -o partial.flv 'http://127.0.0.1:18080/flv/base.flv?drop_after=200000' || true
ffmpeg -i http://127.0.0.1:18080/hls/ts/playlist.m3u8 -c copy -f flv out.flv
ffmpeg -i http://127.0.0.1:18080/hls/fmp4/playlist.m3u8 -c copy -f flv out.flv
```

## gen_hls_fixtures.sh

```bash
scripts/gen_hls_fixtures.sh          # rewrite testdata/hls/{ts,fmp4}
scripts/gen_hls_fixtures.sh --check  # generate to a temp dir and diff; CI-friendly
```

12 s, 320x180 @ 25 fps, H.264 baseline + AAC 48 kHz stereo, 2 s GOP-aligned
segments, ~1.2 MB total. See `testdata/hls/README.md`.

## roomcfg_seed.go

`PUT /room/<id>/config` requires the room to be subscribed, and bilirec's real
`Subscribe()` resolves the room through the Bilibili live API (network). For
offline E2E the script seeds the bbolt `subscribes.db` directly with a
v0.2.0-format gob `RoomConfig` (new pointer fields absent → `nil` → inherit):

```bash
go run scripts/roomcfg_seed.go <database-dir> <room-id> [room-id...]
```

The file has `//go:build ignore`, lives outside the `bilirec` module and is not
picked up by `make test` / `go build ./...`. The E2E invokes it only when the
FEATURES-V2 settings sections are enabled (`E2E_SKIP_SETTINGS_V2` unset).

## e2e.sh

```bash
make e2e                # full offline E2E (builds, then runs all checks)
E2E_KEEP_TMP=1 make e2e # keep artifacts/logs under /tmp/bilirec-e2e.*
```

Environment / escape hatches:

| Variable | Meaning |
|---|---|
| `E2E_LIVE_ROOM_ID=<id>` | **opt-in** real live recording test through the REST API (needs outbound network + a currently-live room). |
| `E2E_LIVE_WAIT_SECS` | how long to wait for the first `.flv` (default 60). |
| `E2E_DOWNLOADER=ffmpeg\|streamlink` | force `DOWNLOADER=` for the backend in the opt-in live test (default: backend default `auto`). |
| `E2E_SKIP_BUILD=1` | reuse existing `dist/` binaries. |
| `E2E_SKIP_FLVREPAIR=1` | developer escape hatch only: skip repair sections while the sidecar is WIP. |
| `E2E_SKIP_SETTINGS_V2=1` | developer escape hatch only: skip FEATURES-V2 settings/room-config/auth sections while T9 is in flight. **Acceptance runs must not set it.** |
| `E2E_SEED_ROOM_ID` | offline-seeded subscribed room for room-config checks (default `12345`). |
| `E2E_ADMIN_USER` / `E2E_ADMIN_PASS` / `E2E_VIEWER_USER` / `E2E_VIEWER_PASS` | credentials for the auth-matrix backend restart (defaults are throwaway values). |
| `E2E_KEEP_TMP=1` | keep logs/artifacts and print the path. |
| `E2E_ORIGIN_PORT` / `E2E_BACKEND_PORT` | fixed ports (default: random free ports). |
| `THROTTLE_BPS` | origin throttle used by the E2E (default 1048576). |

Coverage (no network):

- `make build`; origin serving (relative HLS, fault injection) and both HLS
  variants.
- the `ffmpeg` and `streamlink|ffmpeg` downloader command lines from
  `docs/INTERFACES.md` §2.1 against both HLS variants.
- `flvrepair stream/file/analyze` over HTTP on clean, truncated and corrupted
  input, with `ffprobe` validation.
- bilirec backend startup (`BILIBILI_LOGIN_MODE=anonymous`, temp
  `OUTPUT_DIR`/`DATABASE_DIR`/`SECRET_DIR`, random `PORT`) waiting on
  `GET /version` plus `/record/list` / 404 checks.
- **FEATURES-V2 settings** (docs/FEATURES-V2.md §1/§2): `GET /settings`
  defaults from env incl. invalid-env fallback; partial `PUT` round-trip;
  unknown keys ignored; 14 invalid-payload 400 cases (segment ranges, filename
  template length, RE2 title filter, danmaku transport, API host URLs, CDN
  entry charset/count); `POST /settings/reset`; persistence across restart.
- **room config** (§1.3): offline-seeded subscribed room → GET shows all new
  fields inherit (`nil`); PUT all extended fields → GET round-trip; explicit
  `null`s clear selected overrides to inherit while siblings survive; 404 for
  unsubscribed rooms.
- **auth matrix**: restart with `USERNAME`/`PASSWORD` + `VIEWER_*`: `/version`
  public, protected endpoints 401 without a cookie, wrong password 401,
  admin/viewer login (`POST /login` sets the JWT cookie), viewer GET 200 but
  PUT/reset 403, admin PUT/reset 200.
- SKIP lines are limited to the opt-in live test and (only if the documented
  developer escape hatch is set) the settings sections.

Not covered offline: `POST /record/<room>/start` resolves a real Bilibili room
and its stream URLs (no local-stream injection), so the full
"record → `.flv` on disk → repaired output → session log fields" flow is the
opt-in `E2E_LIVE_ROOM_ID` test. This limitation is deliberate and documented
here; it is not faked. The FEATURES-V2 feature *behavior* (segmentation,
naming, cover, danmaku filters, detectors, B05) is covered by Go/Rust unit tests
and the opt-in live smoke; the E2E verifies the settings/room-config contract
and API semantics offline.
