# OPERATIONS-V2 — settings, deployment and troubleshooting (v0.3.0)

Operational guide for the FEATURES-V2 batch (`docs/FEATURES-V2.md`): global settings,
room overrides, the new environment keys, API workflows, container rebuild/redeploy and
common failure modes. Frozen behavior/field names are defined in `docs/FEATURES-V2.md`;
this document explains how to operate them.

Owner: `qa-harness` (one-off for this batch; `docs/**` is otherwise Lead-owned).

## 1. Settings model and precedence

settings are resolved **once per recording session** into an immutable snapshot; changing
them never affects a running session ("changes apply to new sessions only").

Resolution order, lowest to highest priority:

```
built-in default  <  env value (defaults)  <  DB global override  <  room override (nil = inherit)
```

| Layer | Where | Set by | Notes |
|---|---|---|---|
| built-in default | code | — | all new features default to the v0.2.0 behavior |
| env defaults | process env (`deploy/.env`, compose, systemd) | operator | parsed at startup; immutable while running; **invalid value logs once and falls back to the built-in default** |
| global overrides | `DATABASE_DIR/settings.db`, bbolt bucket `Settings`, key `global` | `PUT /settings` | only explicitly saved keys are persisted; `POST /settings/reset` deletes the whole override record. The API still returns `overrides` as a full settings object — keys without an override show zero/empty values, not the defaults |
| room overrides | `DATABASE_DIR/subscribes.db`, per-room `RoomConfig` (pointer fields) | `PUT /room/<id>/config` | `null`/absent = inherit; `GET` returns `null`/omitted for inherited fields |
| session snapshot | recorder memory | — | what the recording actually used; survives no restart |

`GET /settings` returns three objects: `defaults` (from env), `overrides` (DB) and
`values` (defaults merged with overrides). `values` is what a new global session sees
before room overrides. Room overrides are applied on top when resolving per room.

Persistence files (volume `/app/database` in the container image):

- `settings.db` — global overrides (survives restarts/upgrades; reset clears it).
- `subscribes.db` — subscribed rooms **and** their overrides (old records decode with
  `nil` new fields, so upgrading from v0.2.0 needs no migration).

## 2. New environment keys (defaults = current behavior)

All keys are optional. They seed the `defaults` object; see §1 for precedence.
`deploy/.env.example` contains a ready-to-copy, commented version of this table.

### Segmentation (R02/B02)

| Key | Values / default | Meaning |
|---|---|---|
| `SEGMENT_MODE` | `off` \| `time` \| `size` — default `off` | rotate when the segment reaches a time/size threshold |
| `SEGMENT_MINUTES` | 1–1440, default `120` | time threshold (used with `time`) |
| `SEGMENT_SIZE_MB` | 16–65536, default `2048` | size threshold (used with `size`) |
| `SEGMENT_BY_TITLE` | `true`/`false`, default `false` | rotate when the live room title changes |
| `TITLE_POLL_SECS` | int ≥ 5, default `20` | title poll interval (only when `SEGMENT_BY_TITLE`) |

```bash
SEGMENT_MODE=time
SEGMENT_MINUTES=90
SEGMENT_BY_TITLE=true
```

Manual stop/convert behavior is unchanged; header-change rotation still happens
independently, first trigger wins.

### Naming and filtering (R03/R04)

| Key | Values / default | Meaning |
|---|---|---|
| `FILENAME_TEMPLATE` | string (≤256 bytes), default empty = legacy name | video basename template |
| `TITLE_FILTER_PATTERNS` | newline-separated RE2 regexes, default empty | auto-record skips rooms whose title matches; manual start bypasses |

Template variables: `{roomid}` `{shortid}` `{uname}` `{title}` `{year}` `{month}`
`{day}` `{hour}` `{minute}` `{second}` `{date}` `{time}` `{segment}`. `%Y %m %d %H %M %S`
are also translated. Unknown `{names}` stay literal. Substituted values are sanitized
(path separators/control chars removed); title is truncated to 80 runes and the final
basename to 200 bytes. Segment > 0 always gets a `-<segment>` suffix; collisions append
`-1`..`-999` then a timestamp — files are never overwritten. Cover and danmaku sidecars
share the video basename.

```bash
FILENAME_TEMPLATE={uname}-{roomid}/{date}_{time}_{title}
TITLE_FILTER_PATTERNS=^【轮播】.*$
```

### Cover / danmaku (R05/R06/R08)

| Key | Values / default | Meaning |
|---|---|---|
| `SAVE_STREAM_COVER` | `true`/`false`, default `false` | save `<video basename>-cover.jpg` once per session (5 s timeout, 20 MB, `image/*` only; errors are warnings) |
| `DANMAKU_RECORD_TYPES` | csv of `raw_events,super_chat,gift,guard`, default = all except `raw_events` | enabled danmaku record types |
| `DANMAKU_TRANSPORT` | `auto` \| `ws` \| `wss`, default `auto` | `auto` = wss with one ws fallback; `tcp` is rejected by the API (400) |

`DANMAKU_OUTPUT_FORMAT` (`jsonl`/`xml`) and `DANMAKU_OVERFLOW_POLICY` (existing bilirec
options) are unchanged. With `raw_events=true`, unknown commands are additionally stored
as `{"type":"raw","cmd":...,"data":...}` JSONL records (xml output ignores raw records).

### FLV handling (R14)

| Key | Values / default | Meaning |
|---|---|---|
| `FLV_WRITE_METADATA` | `true` \| `false` — default `true` | passed to the `flvrepair` sidecar as `--write-metadata keep|drop`; `false` strips script/onMetaData tags from repaired FLV |
| `FLV_SPLIT_ON_SCRIPT_TAG` | `true`/`false`, default `false` | rotate when a Script tag appears after media data started |
| `FLV_DISABLE_SPLIT_ON_ANNEXB` | `true`/`false`, default `true` | `true` tolerates Annex-B NALUs; set `false` to rotate on Annex-B |

Default (`keep`, no script split, annexb split disabled) keeps repaired output
byte-identical to v0.2.0.

### Bilibili advanced fetching (B05)

| Key | Values / default | Meaning |
|---|---|---|
| `BILI_LIVE_API_HOST` | absolute http(s) URL, default `https://api.live.bilibili.com` | primary live API host (must reverse-proxy the official API incl. WBI) |
| `BILI_LIVE_API_FALLBACK_HOST` | absolute http(s) URL, default empty | retried once on network error / geo code 60005 / empty URL set |
| `BILI_CDN` | csv, ≤8 entries `[A-Za-z0-9.-]+`, default empty | force `cdn=<name>`; iterated in order |
| `BILI_CDN_FALLBACK` | `true`/`false`, default `false` | collect all successful CDN URL sets instead of stopping at the first |
| `BILI_FORCE_SOURCE` | `true`/`false`, default `false` | prefer TS/fMP4 source profiles (qn ≥ 10000) within the session profile allow-list |
| `BILI_REPLACE_CN01` | csv, ≤8 host candidates, default empty | replace `*-cn-gotcha01*` hosts, probe each (range 0-1023, 3 s) and keep the first 2xx |

All B05 failures fall back to the official host / original URLs; recording is never
blocked by these settings.

## 3. REST API

| Method | Path | Auth when enabled | Body |
|---|---|---|---|
| GET | `/settings` | admin + viewer | — |
| PUT | `/settings` | admin | partial global settings (only present keys overwrite overrides) |
| POST | `/settings/reset` | admin | — (clears global overrides) |
| GET | `/room/<id>/config` | admin + viewer | — (nil/omitted = inherit) |
| PUT | `/room/<id>/config` | admin | room config; `null`/absent for a new field = inherit |

With API auth disabled (no `USERNAME`/`PASSWORD`) all endpoints are open. With auth
enabled, `POST /login` (JSON `{"user":...,"pass":...}`) returns a JWT **cookie**; send it
back as a cookie (browsers do this automatically). Missing/expired token → 401, wrong
role → 403. `/version` stays public. CORS is controlled by `FRONTEND_URLS`
(comma-separated origins) — add every frontend origin you use.

Examples (auth-enabled; cookie jar):

```bash
# login (admin) and reuse the cookie
curl -fsS -c /tmp/bilirec.cookies -H 'Content-Type: application/json' \
  -d '{"user":"admin","pass":"change-me"}' http://127.0.0.1:8080/login

# read defaults/overrides/values
curl -fsS -b /tmp/bilirec.cookies http://127.0.0.1:8080/settings | jq .

# change two globals (partial patch; everything else keeps its override/state)
curl -fsS -b /tmp/bilirec.cookies -X PUT -H 'Content-Type: application/json' \
  -d '{"segment_mode":"size","segment_size_mb":512}' \
  http://127.0.0.1:8080/settings

# clear all global overrides (back to env/built-in defaults)
curl -fsS -b /tmp/bilirec.cookies -X POST http://127.0.0.1:8080/settings/reset

# per-room override: only segment_mode/custom; null clears => inherit global again
curl -fsS -b /tmp/bilirec.cookies -X PUT -H 'Content-Type: application/json' \
  -d '{"auto_record":true,"segment_mode":"time","segment_minutes":30,
       "filename_template":"{uname}-{date}-{time}","danmaku_types":null}' \
  http://127.0.0.1:8080/room/12345/config

# read room config: inherited fields come back null/omitted
curl -fsS -b /tmp/bilirec.cookies http://127.0.0.1:8080/room/12345/config | jq .
```

Notes:

- `PUT /settings` replaces only the keys present in the body; to go back to
  "inherit env/default" for one key, `POST /settings/reset` (global) or send
  `null` for that key in the room config. Validation runs against the merged
  effective values, so e.g. setting `segment_mode=time` together with
  `segment_minutes=0` is rejected even if the previous mode was `size`.
- `GET/PUT /settings` always return all three objects as full settings shapes:
  `defaults` (env/built-in), `overrides` (zero for keys never overridden) and
  `values` (defaults merged with overrides). Compare against `defaults` to see
  which keys are inherited.
- `PUT /room/<id>/config` requires the room to be subscribed (otherwise 404
  `未订阅该房间`); subscribe via `POST /room/<id>` first.
- Unknown JSON keys are ignored (forward-compatible).
- Validation errors return **400** with a Chinese message; see §6.

## 4. Deployment: rebuild and redeploy

The image and compose file live in `deploy/`; see `deploy/README.md` for build details.

```bash
# 0) configuration (first time only; deploy/.env is gitignored)
cp deploy/.env.example deploy/.env && $EDITOR deploy/.env

# 1) rebuild binaries + images (with current source)
docker compose -f deploy/docker-compose.yml build --pull

# 2) redeploy (volumes keep records/database/secrets)
docker compose -f deploy/docker-compose.yml up -d

# 3) verify: container health, version, settings API, recording list
docker inspect --format '{{.State.Health.Status}}' bilirec
curl -fsS http://127.0.0.1:8080/version
curl -fsS http://127.0.0.1:8080/settings      # cookie if auth enabled
curl -fsS http://127.0.0.1:8080/record/list

# 4) verify settings persistence across restart
curl -fsS -X PUT -H 'Content-Type: application/json' -d '{"segment_mode":"time"}' \
  http://127.0.0.1:8080/settings
docker compose -f deploy/docker-compose.yml restart bilirec
curl -fsS http://127.0.0.1:8080/settings | jq '.values.segment_mode'   # -> "time"
```

Upgrade notes:

- Back up the `database` volume before upgrading (`settings.db` + `subscribes.db`).
  There is **no DB schema migration** in v0.3.0; old room records simply decode with
  `nil` overrides (= inherit).
- Changing an env default does not change a key that has a persisted override; clear it
  with `POST /settings/reset` (global) or `null` in the room config.
- `docker compose up` alone reuses the current image; always `build`/`up --build` after
  pulling source changes.
- The compose `bilirec` service loads `deploy/.env` as an optional `env_file`
  (requires Docker Compose ≥ 2.24 for `required: false`); missing file is fine and all
  unset keys keep the built-in defaults.

## 5. Operational checks after a deploy (defaults unchanged)

1. `GET /settings` → `overrides` empty on a fresh DB, `values` equal `defaults`.
2. Start a short manual recording with no overrides → same filename pattern and FLV
   behavior as v0.2.0 (`{title}-{yyyyMMdd}_{HHmmss}.flv`, metadata kept).
3. POST `/settings/reset` → 200; recording again uses env/built-in defaults.
4. Set one feature at a time (e.g. `segment_mode=time`, `segment_minutes=1`), record a
   new session, verify the rotation/logs, then reset. Changes never affect a session
   that is already running.
5. Offline harness: `python3 scripts/origin.py --selftest` and `make e2e` (the E2E
   covers the settings API round-trip/reset, auth matrix and room-config inheritance
   without network; full recording still needs `E2E_LIVE_ROOM_ID`).

## 6. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `PUT /settings` → 400 with a Chinese message | validation failed: check §2 ranges; `segment_mode` ∈ off/time/size; `segment_minutes` 1–1440 (only with `time`); `segment_size_mb` 16–65536 (only with `size`); `min_file_size_mb` 0–1024; `filename_template` ≤256 bytes; `danmaku_transport` not `tcp`. Fix the payload, retry — nothing is persisted on 400. |
| 400 for `title_filter_patterns` | one non-empty line is not a valid RE2 regex (e.g. `(`). Quote/escape per RE2; test with `grep -P`/Go regexp. |
| 400 for `bili_cdn` / `bili_replace_cn01` | entries may only be `[A-Za-z0-9.-]+`, at most 8 entries; no `http://`, paths or commas inside an entry. |
| 400 for `bili_live_api_host` | must be an absolute `http://` or `https://` URL. |
| Settings API returns 401/403 | auth is enabled: login via `POST /login`, send the JWT cookie; viewer role can GET but not PUT/reset. Wrong role/cookie → 403/401. |
| `PUT /room/<id>/config` → 404 `未订阅该房间` | subscribe the room first (`POST /room/<id>`); overrides are stored per subscribed room. |
| Settings changed but running recording unaffected | by design: snapshot at session start. Stop/start the recording (or wait for the next auto session). |
| Settings not visible after restart | check `DATABASE_DIR` points at the persistent volume (`/app/database` in the image); overrides live in `settings.db`. Reset+restart to confirm. |
| Title-based split not triggering | `SEGMENT_BY_TITLE=true`; title poller needs network access to the live room API; `TITLE_POLL_SECS` ≥5 (default 20). Errors are warnings and do not stop recording. |
| Cover image missing | `SAVE_STREAM_COVER=true`; cover fetch is best-effort (5 s timeout, 20 MB, `image/*`). Check backend warnings; no retries by design. |
| HEVC stream records but no repair | HEVC-in-FLV is supported by the sidecar, but use `FLV_REPAIR=on`/`auto`; if the sidecar is unavailable the recorder degrades to pass-through (`repair=degraded`) rather than failing. Check `flvrepair --version` and `FLV_REPAIR_BIN`. |
| B05 custom API host fails / signature errors | a custom `BILI_LIVE_API_HOST` must reverse-proxy the official API including WBI endpoints; on failures the client falls back to the official host/original URLs, recording continues. Check logs for `api_host`/`fallback_reason`. |
| `BILI_CDN` set but playback quality unchanged | unknown CDN names yield no URLs and fall back to normal selection; `BILI_FORCE_SOURCE` only prefers TS/fMP4 profiles allowed by the room/session profile list. |
| Invalid env value | startup logs one warning and uses the built-in default; fix `deploy/.env` and restart (env values are read at startup). |
| E2E settings checks skipped | `E2E_SKIP_SETTINGS_V2=1` was exported (developer escape hatch). Final acceptance runs must not set it. |

## 7. Known limitations

- Global settings are **global**: there is no per-room UI/DB schema change beyond the
  existing room config; per-room values live in `subscribes.db` room records.
- Settings changes are not versioned/historied; only the current override record exists.
- Offline E2E cannot start a real recording (backend resolves real Bilibili rooms); the
  live path remains opt-in via `E2E_LIVE_ROOM_ID` (see `scripts/README.md`).
- A custom Bilibili API host is assumed to be API-compatible with the official one
  (including WBI signing endpoints); B05 never blocks recording when it misbehaves.
