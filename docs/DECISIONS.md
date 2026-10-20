# Decisions log

Record every approved deviation from `docs/INTERFACES.md` here (date, owner, reason).

## D001 (2026-10-20, Lead) — batch repair API deferred

`INTERFACES.md` v1 §7: the planned `POST /api/repair` and `bilirec repair` subcommand are
deferred to v2. Rationale: the Rust `flvrepair file --input --output` CLI already covers batch
repair, including for files recorded by any tool. The Go API would add controller/DB work with
no additional recording capability. No effect on the recording pipeline.

## D002 (2026-10-20, Lead) — git policy

Teammates do not run `git add/commit`; the Lead reviews the working tree and commits per task.
Rationale: four agents share one worktree; concurrent index operations race and mix diffs.

## D003 (2026-10-20, Lead) — recording stats expose pipeline fields (user-approved)

Follow-up T7 (additive): `GET/POST /record/stats` entries now include:
- `downloader`: native|ffmpeg|streamlink actually used for the session (fallback => native);
- `source_format`: original Bilibili source format flv|ts|fmp4 (`actual_stream_format` remains
  the format written to disk, i.e. flv for external downloaders);
- `repair`: live FLV repair state off|on|degraded (per-room registry in repair.Service; falls
  back to the start-time value when the live registry has no entry).

No DB migration, no frontend changes, defaults unchanged. Frontend can consume these fields
without modification; no UI controls were added.

## D004 (2026-10-20, Lead) — external ffmpeg downloader vs Enhanced-FLV HEVC

Live validation (room 6) showed a room serving HEVC inside FLV: `ffmpeg -c copy -f flv -`
fails with "dimensions not set" / "Could not write header (incorrect codec parameters)".
The recorder correctly falls back to the native downloader (log warning; stats report
`downloader=native`). Known limitation: forcing `DOWNLOADER=ffmpeg` on Enhanced-FLV HEVC rooms
may fall back to native. Native recording and the FLV repair sidecar handle those rooms
(R8 path). Documented in README.

## D005 (2026-10-20, Lead) — frontend vendored and extended (user-approved)

`bilirec-web` (upstream MIT, pinned `ab62df3`) is vendored at `web/` like the backend
fork. Local changes (T8): downloader picker in the record-start dialog (records view only),
and downloader / repair / source-format badges on recording cards, consuming the v0.1.1 stats
fields. The subscribe/RoomConfig path keeps `auto` because the backend has no per-room
downloader field. No global settings page: `DOWNLOADER` / `FLV_REPAIR*` remain env-only.
Self-hosting: `deploy/web.Dockerfile` (nginx) + `web` service in docker-compose; backend CORS
must include the frontend origin via `FRONTEND_URLS`.

## D006 (2026-10-20, Lead) — FEATURES-V2 batch approved

User selected R02/R03/R04/R05/R06/R08/R14/R15/R17 + B02/B05. Implementation contract:
`docs/FEATURES-V2.md` (frozen). Result: global settings persisted in bbolt (`settings.db`) with env
defaults + room-level overrides; new settings/room-config APIs; recording segmentation/filename/
title filter/cover; danmaku type toggles/transport; FLV metadata/script/annex-b options; Rust
toolbox export/danmaku merge/start-time; Bilibili advanced stream fetching. Defaults preserve
v0.2.0 behavior; changes apply to new sessions only.

## D007 (2026-10-20, Lead) — FEATURES-V2 implemented in v0.3.0

Implemented (contract: docs/FEATURES-V2.md): global settings persisted in bbolt (`settings.db`,
env-seeded defaults + DB overrides), 19 room-level overrides (nil = inherit), `/settings` API and
extended `/room/{id}/config`; segmentation by time/size/title + min-file-size filter; filename
templates; title regex filter; stream cover; danmaku type toggles/transport/raw events; FLV
write-metadata + Script/Annex-B split options; Rust toolbox export/danmaku-start-time/danmaku-merge;
Bilibili advanced fetching (API host/fallback, CDN, force source, cn01 replace); frontend settings
page and room-level inherit/override UI. Defaults preserve v0.2.0 behavior; changes apply to new
sessions only. Room overrides are validated with the same rules as global settings.

Accepted limitations: TCP danmaku transport unsupported (ws/wss/auto only); transport=ws uses port
2244 fallback because `DanmuInfoHost` does not expose `ws_port`; `flv_disable_split_on_annexb`
defaults true to preserve prior behavior; pre-existing `-race` warnings in unmodified
`pkg/coordinator`/`processors` tests are not addressed by this batch.

## D008 (2026-10-20, Lead) — filename template is a path template

User-approved: a non-empty `filename_template` is a **relative path template under OUTPUT_DIR**;
`/` creates subdirectories (no implicit `<uname>-<roomid>/` prefix). Empty template keeps the
legacy v0.2.0 naming exactly. Security: reject absolute paths, `..`/`.` segments, backslash,
control chars and empty segments; render/sanitize each segment; `MkdirAll` parents and verify the
final path stays inside OUTPUT_DIR. Contract: docs/FEATURES-V2.md §3.2 (amended). Released in v0.3.1.

## D009 (2026-10-21, Lead) — repository restructured as a bilirec fork

The repo root is now the Bilirec fork itself: `cmd/`, `internal/`, `pkg/`, `utils/`, `go.mod`,
`dotenv.go`, `swagger.go` moved from `bilirec/` to the root; `bilirec-web/` became `web/`. Upstream
files are preserved (`.github/`, `CONTRIBUTING.md`), while a few that would conflict were archived:
upstream README/Makefile under `docs/upstream-bilirec-*.md/txt`, upstream Dockerfile at
`deploy/upstream.Dockerfile`, MIT license at `LICENSES/Bilirec-MIT.txt`. Our additions stay in
parallel directories: `rust/`, `web/`, `deploy/`, `scripts/`, `testdata/`, `docs/`. Build/deploy/test
paths updated accordingly; released as v0.4.0.

## D010 (2026-10-21, Lead) — image size reduction + Alpine variant

Debian images now copy the static `ffmpeg` from `mwader/static-ffmpeg:7.1` instead of installing
apt `ffmpeg` (which pulled ~450MB of LLVM/mesa/codec deps): all-in-one 958MB -> 525MB, tar
242MB -> 131MB. `ffprobe` is not included (only optional test code uses it). New
`build-alpine.sh` builds Alpine/musl variants (all-in-one 390MB image / 98MB tar): Go is static,
Rust `flvrepair` is rebuilt for musl (the Debian glibc binary is not usable on Alpine),
ffmpeg/streamlink/nginx come from apk (native musl). Measured Alpine ffmpeg: apk stack ~243MiB vs
129MB static binary stack ~248MiB, so apk is used. `streamlink` is 8.6.2 (pip) on Debian and 8.4
(apk) on Alpine.

## D011 (2026-10-21, Lead) — slim/full image variants (`--full`)

Default `build.sh` / `build-alpine.sh` images are now **slim**: no ffmpeg and no streamlink
(native recording only; external downloaders and `CONVERT_TO_MP4` unavailable). `--full` builds
full variants with the `-full` tag suffix (`local-full`, `local-backend-full`, ...), keeping
ffmpeg + streamlink. `WITH_EXTRA=1/0` is the Docker build arg; compose deployments default to 1 to
preserve previous behavior. Measured tar/image sizes: Debian slim 57MB/238MB vs full 131MB/525MB;
Alpine slim 18MB/69MB vs full 98MB/390MB. Frontend images are identical in both variants (suffix is
for naming symmetry). Slim images still record native FLV/HLS and run flvrepair.

## D012 (2026-10-21, Lead) — hide convert UI when no conversion backend

New `GET /convert/providers` returns `{providers: [...], enabled: bool}` from the convert service
manager set. The web UI fetches it after login and hides the “转换任务” nav entry (sidebar + bottom
nav) when `enabled=false`; deep links/`?tab=converts` fall back to files/records. Older backends
without the endpoint keep the menu visible. This makes slim images (no ffmpeg/cloudconvert) stop
offering a conversion menu that cannot work.

## D013 (2026-10-21, Lead) — CONFIG-V2 配置中心/部署面与 README 拆分

统一配置中心落地（契约：docs/CONFIG-V2.md；实现：T-A/T-B/T-C/T-D）：`config.json`（0644，非敏感）+
`secrets.json`（0600，仅 6 个敏感键）+ `.env` 回退，优先级为系统环境变量 > 配置文件 > `.env` > 内置默认，
来源通过 `GET /config.sources` 暴露；`/config`+`/config/secrets` API 支持读写/清除/重置、环境锁定与
`restart_required`；FEATURES-V2 全局特性覆盖从 `settings.db` 迁移到 `config.json`（旧数据启动时幂等导入），
默认文件名改为 `{roomid}-{uname}/{date}_{time}_{title}`（显式空字符串保留旧命名）。
部署面：四个 Dockerfile 增加 `ARG VERSION`（注入 `pkg/updatecheck`）、`CONFIG_FILE`/`SECRETS_FILE` 与
PUID/PGID 入口（Debian gosu / Alpine su-exec，combined 镜像 nginx 保持 root、bilirec 子进程降权），
compose 增加 `config:/app/config` 卷，数据目录统一在 `/app/{records,database,secrets,config}`。
文档按 CONFIG-V2 §9 拆分：根 README 只保留部署/服务内容（镜像 tag、compose、config/secrets、PUID/PGID、
卷/端口、运维链接、许可），构建/测试/架构/验证移到 `docs/DEVELOPMENT.md`；`docs/OPERATIONS-V2.md`
按配置中心/生效语义/目录/PUID/API/升级排障重写。理由：面向使用者的根 README 不应包含开发指令，配置/密钥
需要可持久化且可热更新的统一入口，容器需要可配置 uid/gid 以便宿主机卷权限。
