# FEATURES-V2 contract (frozen for M1–M5)

Status: implemented in v0.3.0 (2026-10-20). Frozen field names/API kept.

Approved plan: R02/R03/R04/R05/R06/R08/R14/R15/R17 + B02/B05.
This file is the implementation contract. Do not change field names/semantics without Lead approval.

## 0. Non-negotiables

- **Defaults must reproduce current behavior** (all new features off/inherit unless explicitly set).
- Settings are resolved at **session start** into an immutable snapshot; changes apply to new sessions only.
- Global settings persist in bbolt DB; env values are defaults/seed; room config overrides global.
- Existing APIs (`/record/*`, `/room/{id}/config` shape extended additively) stay backward compatible.

## 1. Data model

### 1.1 Global settings (`internal/services/settings`)

Stored as JSON in `DATABASE_DIR/settings.db`, bucket `Settings`, key `global`.
On first start (or after `POST /settings/reset`) values are seeded from env, then persisted? **No**:
- `defaults` = parsed from env at startup (immutable).
- `overrides` = JSON persisted in DB (only fields explicitly saved).
- `values` = defaults merged with overrides.
PUT persists `overrides`; reset deletes the DB key.

JSON shape (snake_case, also used by API):
```jsonc
{
  "segment_mode": "off",            // off|time|size
  "segment_minutes": 120,
  "segment_size_mb": 2048,
  "segment_by_title": false,
  "min_file_size_mb": 0,
  "filename_template": "",
  "title_filter_patterns": "",
  "save_stream_cover": false,
  "danmaku_types": {"raw_events": false, "super_chat": true, "gift": true, "guard": true},
  "danmaku_transport": "auto",      // auto|ws|wss
  "flv_write_metadata": true,
  "flv_split_on_script_tag": false,
  "flv_disable_split_on_annexb": true,
  "bili_live_api_host": "https://api.live.bilibili.com",
  "bili_live_api_fallback_host": "",
  "bili_cdn": [],
  "bili_cdn_fallback": false,
  "bili_force_source": false,
  "bili_replace_cn01": []
}
```

Env defaults: `SEGMENT_MODE`, `SEGMENT_MINUTES`, `SEGMENT_SIZE_MB`, `SEGMENT_BY_TITLE`,
`MIN_FILE_SIZE_MB`, `FILENAME_TEMPLATE`, `TITLE_FILTER_PATTERNS`, `SAVE_STREAM_COVER`,
`DANMAKU_RECORD_TYPES` (csv `raw_events,super_chat,gift,guard`), `DANMAKU_TRANSPORT`,
`FLV_WRITE_METADATA`, `FLV_SPLIT_ON_SCRIPT_TAG`, `FLV_DISABLE_SPLIT_ON_ANNEXB`,
`BILI_LIVE_API_HOST`, `BILI_LIVE_API_FALLBACK_HOST`, `BILI_CDN` (csv),
`BILI_CDN_FALLBACK`, `BILI_FORCE_SOURCE`, `BILI_REPLACE_CN01` (csv).
Invalid env value => use built-in default and log once at startup.

### 1.2 Effective snapshot (`settings.Effective`)

```go
type Effective struct {
    SegmentMode string; SegmentMinutes int; SegmentSizeMB int; SegmentByTitle bool
    MinFileSizeMB int
    FilenameTemplate string
    TitleFilterPatterns string
    SaveStreamCover bool
    DanmakuTypes DanmakuTypes
    DanmakuTransport string
    FlvWriteMetadata bool; FlvSplitOnScriptTag bool; FlvDisableSplitOnAnnexB bool
    BiliLiveAPIHost, BiliLiveAPIFallbackHost string
    BiliCDN []string; BiliCDNFallback, BiliForceSource bool; BiliReplaceCN01 []string
}
```
`settings.Service`:
- `Resolve(roomID int) Effective` — values + room overrides (nil = inherit).
- `Get() (defaults, overrides, values GlobalSettings)` / `Update(patch GlobalSettings)` / `Reset()`.
- Cache `values` under `atomic.Pointer` + RWMutex; DB write then swap.
- Depends on `*config.Config` and `*subscribe.Service` only (no cycles).

### 1.3 Room overrides (`subscribe.RoomConfig`, JSON add-only, nil = inherit)

Pointer fields: `SegmentMode *string`, `SegmentMinutes *int`, `SegmentSizeMB *int`,
`SegmentByTitle *bool`, `MinFileSizeMB *int`, `FilenameTemplate *string`,
`TitleFilterPatterns *string`, `SaveStreamCover *bool`, `DanmakuTypes *DanmakuTypes`,
`DanmakuTransport *string`, `FlvWriteMetadata *bool`, `FlvSplitOnScriptTag *bool`,
`FlvDisableSplitOnAnnexB *bool`, `BiliLiveAPIHost *string`, `BiliLiveAPIFallbackHost *string`,
`BiliCDN *[]string`, `BiliCDNFallback *bool`, `BiliForceSource *bool`, `BiliReplaceCN01 *[]string`.
Old DB records decode with nil fields (no migration needed).

`RoomConfigResponse` / `UpdateRoomConfigRequest` in `controllers/room/types.go` add the same
fields with `omitempty`; GET returns nil for inherit, PUT stores what is sent (null/absent = inherit,
clears previous override). Keep existing fields unchanged.

## 2. REST API

| Method | Path | Auth | Body / response |
|---|---|---|---|
| GET | `/settings` | admin+viewer | `{ "defaults": Global, "overrides": Global, "values": Global }` |
| PUT | `/settings` | admin | partial `Global` (only present keys overwrite overrides); returns `{defaults,overrides,values}` |
| POST | `/settings/reset` | admin | `200`, clears overrides |

Validation (400 with Chinese message):
- `segment_mode` ∈ {off,time,size}; `segment_minutes` 1–1440 (when time); `segment_size_mb` 16–65536 (when size).
- `min_file_size_mb` 0–1024.
- `filename_template` ≤256 bytes; `title_filter_patterns` ≤4096 bytes and every non-empty line compiles as RE2.
- `danmaku_transport` ∈ {auto,ws,wss} (TCP rejected).
- `bili_live_api_host` / fallback: absolute http(s) URL; `bili_cdn`/`bili_replace_cn01` entries `[A-Za-z0-9.-]+` only.
- `bili_cdn` ≤8 entries; `bili_replace_cn01` ≤8 entries.
- Unknown JSON keys ignored (Go default).

Swagger annotations required for all three endpoints.

## 3. Recording core

### 3.1 Segmentation (R02/B02)
- `Info` gains: `effective settings.Effective`, `segmentBytes atomic.Int64`,
  `segmentStart time.Time`, `rotationRequested atomic.Bool`, `rotationReason string` (guarded by mutex).
- In `rev` after `pipe.Process`: add `len(data)` to segmentBytes; compute trigger:
  - size: `segmentBytes >= SegmentSizeMB*1024*1024`
  - time: `time.Since(segmentOpenTime) >= SegmentMinutes*time.Minute`
  - title: set by poller when title changes (`SegmentByTitle`)
  - On trigger: set `rotationRequested` with reason (`size|time|title`); before processing next chunk return `rs.ErrRotationRequested`.
- `record_strategies`: define `ErrRotationRequested = errors.New("按配置切段")`; every strategy's
  `HandleErr` maps `errors.Is(err, ErrRotationRequested)` to `ErrActionRotate` (no state reset; headers carried as today).
- Title poller (only if `SegmentByTitle`): one goroutine per active session, ticker `TITLE_POLL_SECS`
  (env, default 20, min 5), calls `bilic.GetLiveRoomInfo`; on title change: atomically update
  `Info.title` (new method `SetTitle/Title`, used by naming/webhook/stop messages) and request rotation;
  errors: warn at most once/min, keep recording.
- `MinFileSizeMB > 0`: in `finalize` before convert/webhook, `os.Stat`; if size < threshold: remove file,
  `dm.Discard`, metrics `ReasonSkippedSmall`, log, return (no FileClosed webhook).
- Interaction: existing header-change rotation unchanged; first trigger wins; reason logged.

### 3.2 Path template (R03, amended 2026-10-20: `/` allowed)

New `internal/services/naming` with `RenderPath(template string, vars naming.Vars) (string, error)`
returning a **relative path under OUTPUT_DIR**:
- Vars: `{roomid}`, `{shortid}`, `{uname}`, `{title}`, `{year}`, `{month}`, `{day}`,
  `{hour}`, `{minute}`, `{second}`, `{date}` (YYYYMMDD), `{time}` (HHMMSS), `{segment}`.
- strftime: `%Y %m %d %H %M %S` translated to Go layout; other `%` kept literal.
- `/` in the template is a **path separator** (creates subdirectories). Values substituted for
  variables are still sanitized (no `/ \ : * ? " < > |`, no control chars, trim dots/spaces,
  title ≤80 runes); each literal path segment is sanitized the same way.
- Forbidden (API 400 and `RenderPath` error): absolute path (leading `/`), any `..` segment,
  backslash `\`, control chars, empty segment (`//`, trailing `/`), final segment empty;
  template ≤256 bytes; each rendered segment ≤200 bytes; total relative path ≤512 bytes.
- Empty template keeps the legacy v0.2.0 behavior exactly: directory `OUTPUT_DIR/<uname>-<roomid>/`
  and filename `title(20 runes)-YYYYMMDD_HHMMSS`.
- Non-empty template defines the **full relative path from OUTPUT_DIR** (it does not get an implicit
  `<uname>-<roomid>/` prefix; include `{uname}-{roomid}/` in the template if wanted).
  `rotateFilePath` creates parent dirs with `MkdirAll(0755)` and verifies the result stays inside
  OUTPUT_DIR via `filepath.Rel` (defense in depth).
- segment>0 appends `-<segment>` to the final basename only.
- Collision: if the final basename exists (segment 0), append `-1`..`-999`; then timestamp; never overwrite.
- Cover path and danmaku sidecar use the same relative directory and basename.
- Frontend hint/placeholder must say: 相对 OUTPUT_DIR、可用 `/` 建子目录、禁止 `..`/绝对路径/`\`。

### 3.3 Title filter (R04)
- Applied in `subcheck` before auto-start: compile cached regex set (per room override > global);
  any match on `room.Title` => skip start, log `skip_reason=title_filter`, metrics counter, publish state as before.
- Manual `POST /record/{room}/start` bypasses filter.
- Compile on setting save; invalid => 400. Invalid env at startup => warn once, empty rules.

### 3.4 Stream cover (R05)
- New `internal/services/cover` (or recorder helper): after first segment opens, async GET `room.Cover`
  (5s timeout, 20MB cap, content-type `image/*`), save `<video basename>-cover.jpg`; skip if exists;
  all errors warn only. Uses bilibili live headers (`StreamRequestHeaders`).
- Respect `SaveStreamCover`; no retry loop.

## 4. Danmaku (R06/R08)

- `danmaku.SessionOptions{Types DanmakuTypes, Transport string}` passed by recorder.
- `raw_events=false`: only known chat/SC/gift/guard events (current behavior); `true`: also persist unknown
  command payloads as JSONL records `{"type":"raw","cmd":...,"data":...}` (xml format ignores raw).
- `super_chat/gift/guard=false`: drop those message types before encoding (both jsonl/xml).
- Transport: `auto` = wss (current) with one ws fallback on TLS/dial error; `ws` = plain ws;
  `wss` = wss only. TCP unsupported -> API 400.
- Existing `DANMAKU_OUTPUT_FORMAT` etc unchanged.

## 5. FLV handling + Toolbox

### 5.1 Sidecar flag
- `flvrepair stream|file` add `--write-metadata <keep|drop>` (default keep). drop = do not emit
  script/onMetaData tags in output (duration patch ignored).
- Go `repair.WrapStream(ctx, ch, pool, repair.SessionOptions{Mode, MaxBufferMB, WriteMetadata bool})`;
  recorder passes effective value. Default keep => byte-identical to current behavior.

### 5.2 Script-tag / Annex-B split
- `pkg/flv`: `ScriptTagSplitDetector` — after the first media data tag, presence of a Script tag
  (type 18) raises `ErrScriptTagSplit`. `AnnexBDetector` — detects `00 00 01`/`00 00 00 01` start codes
  in video NALU payload, raises `ErrAnnexBSplit` once.
- Recorder pipeline adds detectors only when enabled:
  - `FlvSplitOnScriptTag` (default false) => `ErrScriptTagSplit` -> rotate.
  - `FlvDisableSplitOnAnnexB` (default true) => when set to false, `ErrAnnexBSplit` -> rotate.
- Rotation carry: same as header-change (new detector resets on new segment; no state needed).

### 5.3 Toolbox CLI (Rust)
```
flvrepair export --input a.flv --output a.xml|a.zip [--json]
flvrepair danmaku-start-time --inputs a.xml [b.xml ...] [--json]
flvrepair danmaku-merge --output out.xml --inputs a.xml b.xml [...] [--offsets 0,5000,...]
```
- `export`: XML per BililiveRecorder `XmlFlvFile` schema (read `refs/BililiveRecorder/BililiveRecorder.Flv/Xml/**`);
  `--output *.zip` writes zip containing xml + raw tags; exit codes follow existing contract.
- `danmaku-start-time`: read Bilibili XML `<d starttime="...">`; output text `file<TAB>starttime` or JSON with `--json`.
- `danmaku-merge`: merge Bilibili XML; use starttime offsets unless `--offsets` given (ms, same count as inputs);
  output monotonic timestamps, preserve `starttime` = min; error on count mismatch/invalid xml.
- Golden fixtures under `rust/crates/flv-repair/tests/fixtures/`; no new deps unless unavoidable.

## 6. Bilibili advanced fetching (B05)

- `bilibili.Client` gets `StreamFetchConfig` (from Effective):
  `APIHost`, `FallbackAPIHost`, `CDN []string`, `CDNFallback bool`, `ForceSource bool`, `ReplaceCN01 []string`.
- `GetStreamURLsV2(roomID, opts...)` add `WithFetchConfig(cfg)`:
  1. Primary host; on network error / code 60005 (geo) / 0 URLs: retry with fallback host once.
  2. `CDN` non-empty: set query `cdn=<name>`; iterate list (first success unless `CDNFallback=true`,
     in which case append all successful CDN URL sets in order, de-duplicated).
  3. `ForceSource`: prefer TS/fMP4 profiles (only if user profile allow-list includes them) with qn≥10000.
  4. `ReplaceCN01` (independent of `ForceSource`): whenever the resolved URL host is `*-cn-gotcha01*`
     and `ReplaceCN01` is non-empty, replace the host with candidates in random order; probe each with
     GET range 0-1023 (3s timeout) and keep the first 2xx; fall back to the original URL if none respond.
  5. Preserve existing sorting/fallback behavior; log `api_host`, `cdn`, `force_source`, `replace_cn01`,
     `fallback_reason`.
- WBI signing stays on current `wbi` client; doc note: custom API host must reverse-proxy the same
  Bilibili API (including WBI endpoints); otherwise signature failures fall back to official host.
- All failures fall back to official host/original URLs; recording must never be blocked by B05 settings.

## 7. Frontend (R17)

- `src/lib/types.ts`: `DanmakuTypes`, `GlobalSettings`, `SettingsResponse`, extended `RoomConfig`/`UpdateRoomConfigRequest`.
- `src/lib/api.ts`: `getSettings()`, `updateSettings(patch)`, `resetSettings()`; room config methods unchanged shape.
- New `src/components/SettingsView.tsx` + nav tab `settings` (`LeftSidebar` + `BottomNav` + `App.tsx` union type):
  sections: 分段 / 命名与过滤 / 封面 / 弹幕 / FLV 处理 / B 站高级; load `GET /settings`, save `PUT`, reset button;
  admin editable, viewer read-only; saved toast "新会话生效"; server errors rendered.
- `RoomConfigDialog`: add same fields with per-group "继承全局" switch; inherit => field omitted (nil);
  custom => value; show global value as placeholder; `buildStartRecordParams` unchanged.
- i18n zh-CN + zh-TW keys; `pnpm lint && pnpm build` must pass.

## 8. Tests / acceptance

- Go: settings validation & persistence; Resolve inheritance; template table tests; segment size/time/title
  triggers & rotation state; title poller with httptest bilibili mock? (mock client injection or interface);
  filter regex; cover httptest; danmaku filters; script/annexb detectors; B05 query/host/CDN/replace/fallback.
- Rust: metadata keep/drop; export schema; danmaku merge/start-time golden; existing 67+ tests stay green.
- Frontend: lint/build; manual checklist in task report.
- Regression: `make fmt lint test`, `cargo test`, `pnpm lint && pnpm build`, `make e2e`, live smoke (30s) with
  defaults and with each new setting (one session each).
- Defaults check: with no settings saved, `GET /settings` values equal env defaults, and a recording session
  produces same filenames/behavior as v0.2.0.

## 9. Ownership / write scopes

| Owner | Scope |
|---|---|
| go-integration | `internal/**, cmd/**, pkg/**, utils/**` (T9→T12 sequential) |
| rust-flv | `rust/**` (T13) |
| web-frontend | `web/**` (T14) |
| qa-harness | `scripts/**`, `deploy/**`, `testdata/**`, `docs/OPERATIONS` additions (T15) |
| Lead | `docs/**`, `Makefile`, root files, review, commits, tag (T16) |

No git commits by teammates. Evidence required per task. Lead reviews and runs gates.
