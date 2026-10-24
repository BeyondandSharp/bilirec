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

## D014 (2026-10-21, Lead) — PUID/PGID 对齐 LinuxServer.io 的 `-o` 语义

入口脚本原先在 PUID/PGID 与镜像内已有用户/组重复时直接报错（例如基础镜像的 `users` 组占用 gid 100，
设置 `PGID=100` 无法启动）。现改为与 LinuxServer.io Jackett/baseimage 相同的做法：使用
`groupmod -o -g "$PGID"` 和 `usermod -o -u "$PUID"` 允许非唯一 UID/GID；不再做冲突校验，仅校验数字。
Alpine 运行时镜像安装 `shadow` 以获得 groupmod/usermod；Debian 已有。默认仍为 10001（LSIO 为 911），
home 目录在改 uid 时临时指向 `/nonexistent` 以避免递归 chown，之后恢复。理由：宿主 gid 100 很常见，
LSIO 兼容用法应当可用；重复 ID 下进程仍按请求的数值 UID/GID 运行、文件按数值归属。

## D015 (2026-10-22, Lead) — 断流恢复只对“直播仍在”生效，默认 30 秒

原 Bilirec 的 `recover()` 在直播已下线时仍以 `ErrStreamNotLive` 反复重试，最长等待
`MAX_RETRY_MINUTES`（默认 10 分钟）才停止，导致正常下播后长时间显示“恢复中”。现改为：

- `ErrStreamNotLive`（直播间 `live_status != 1`）立即结束恢复并停止会话；
- 只有直播间仍在直播、但流地址断开/不可达时才重试；
- 重试窗口由新键 `MAX_RETRY_SECONDS` 控制，默认 30 秒，0 表示不重试；
- `MAX_RECOVERY_ATTEMPTS` 仍作为重试次数上限；
- 旧键 `MAX_RETRY_MINUTES` 作为兼容别名保留（未设置新键时按分钟×60 换算），不再出现在
  设置 schema 中。

理由：下播是明确的终态，不应占着“恢复中”状态；短暂断流才需要恢复。B 站状态接口滞后时
最多仍可能有约 30 秒窗口。源码见 `internal/services/recorder/recorder.go`。

## D016 (2026-10-22, Lead) — 容器不再递归修改挂载目录权限

原入口脚本会 `chown -R` 配置/密钥/数据库目录，并在 records 顶层属主不一致时 `chown -R` 整个录像目录，
导致用户 bind mount 的既有文件 owner/mode 被改动，大目录启动也很慢。现改为：

- 不递归遍历任何挂载目录；
- 仅在挂载点目录对 PUID/PGID 不可写时 chown 该目录本身；
- 已存在的 `config.json`/`secrets.json`/`*.db` 仅在目标用户不可读写时修改该文件；
- 录像文件从不 chown；
- 入口设置 `umask 0022`，应用以 PUID 创建的新文件默认 0644/0755；
- 配置文件由 configfile 原子写入时自行设置 0644，secrets 设置 0600。

理由：挂载内容属于用户，容器只应修复自身启动/写入所必需的最小权限；迁移 PUID 后旧录像目录由用户自行处理。

## D017 (2026-10-22, Lead) — 许可证修正为 GPL-3.0-only

录播姬（BililiveRecorder）的 `LICENSE` 是标准 GPLv3 全文，项目界面明确写 “GPLv3”，
源码没有 “or any later version” 声明，GitHub 许可元数据对应 `GPL-3.0-only`，因此上游按
**GPL-3.0-only** 处理。`rust/crates/flv-repair` 是其 FLV 规则的移植，整体仓库不能
单方面授予 `or-later`。

本次将仓库声明从旧的 `or-later` 表述统一修正为 `GPL-3.0-only`：

- `LICENSE` 保持 FSF GPLv3 全文不变，项目声明写在 `NOTICE`/`README`；
- 根 README、NOTICE、UPSTREAM、开发/接口文档、CONTRIBUTING 同步；
- Rust workspace `license` 与全部项目 SPDX 头改为 `GPL-3.0-only`；
- Swagger 元数据改为 GPL-3.0-only；
- Bilirec、biliup、bilirec-web 的 MIT 许可与声明保持不变。

理由：衍生作品必须遵守上游的 only 许可；修正声明不影响代码行为。

## D018 (2026-10-22, Lead) — 本地日志按来源拆分，HTTP access 独立

原 `wireLocalLogger` 只有一个文件，`rest` 的每个 HTTP 请求 access 日志与业务日志混在
`bilirec.log` 中，导致日志页/文件被请求日志淹没。现改为按 logger source 拆分：

- `LOCAL_LOGS_SPLIT=true`（默认）：
  - 主日志 `LOCAL_LOGS_PATH` 保留无名称日志和已启用类别日志；
  - 日志合并为 `live`/`system`/`recorder`/`access`/`update` 五类，写 `<LOCAL_LOGS_PATH>.d/<group>/current.log`；
  - `access` 的 INFO/DEBUG 只写 `access/current.log`，不进入主日志；WARN/ERROR 仍进主日志；
  - `LOCAL_LOGS_GROUPS` 可勾选类别，取消的类别不写本地文件。
- `LOCAL_LOGS_SPLIT=false` 保留旧单文件行为。
- 每个类别使用独立 lumberjack/async sink，按首次写入懒创建；失败回退主日志。
- `/logs/sources` 列来源，`/logs?source=` 读取指定来源，`/logs/download?source=` 下载指定来源；
  只暴露 `current.log`，不暴露轮转备份，不接受任意路径。
- 前端日志页增加来源选择，默认 `main`。

原因：请求日志是高频来源，必须与应用日志分离；main 仍保留应用日志和所有错误，避免默认页面失去可用性。
代价：main 保留已启用类别的日志副本，磁盘占用约增加。

## D019 (2026-10-23, Lead) — 全局设置继续存 config.json/secrets.json，不迁数据库

核对原版 Bilirec v0.3.1：`internal/modules/config` 只从环境变量读取全局配置，原版不存在
`settings.db`；只有 `subscribes.db`（房间订阅/配置）、`subcheck.db`、`notify.db`、`queues.db`。
本仓库的 `settings.db` 是早期 FEATURES-V2 自己加的全局覆盖库，CONFIG-V2 后只作为一次性迁移来源，
不再写入。

因此继续使用：

- `config.json` / `secrets.json`：全局设置与密钥（支持 env/config 优先级）；
- `subscribes.db`：房间订阅和房间级覆盖；
- `settings.db`：仅兼容旧数据，迁移后可备份删除。

CONFIG-V2 启动时不再创建 `settings.db`：只有检测到已存在的旧文件才打开并迁移；
新安装的数据库目录不会生成该文件。

不把全局配置迁入 bbolt，避免破坏 CONFIG-V2 的文件优先级与 secret 文件语义。

## D020 (2026-10-23, Lead) — 在线重启与可热更新配置

后端右上角提供管理员重启入口，`POST /system/restart` 返回 202 后：
fx 生命周期优雅停止 → 录制/log sink 收尾 → `syscall.Exec` 自替换当前二进制。
combined 镜像中 nginx 不重启，只重启 bilirec 子进程；Android/嵌入式返回 501。

不是所有 `RestartRequired` 都适合无重启：需要重建监听端口、文件句柄、认证/JWT 中间件、
外部下载器/转码/FRP/metrics 进程或缓冲区池的键保持重启。对有安全运行时路径的键改为热更新：

- `WEBHOOK_URLS`：webhook Service 动态 URL，recorder/convert 只在运行时检查 enabled；
- `FRONTEND_URLS` / `FRONTEND_URL`：CORS 使用动态 `AllowOriginsFunc`；
- `LOCAL_LOGS_GROUPS`：source router 运行时替换 enabledGroups；
- 19 个 FEATURES-V2 录制设置继续保持“无需重启、新会话生效”。

`configfile.Store` 增加配置变更监听，热更新服务通过监听 `/config` 写入动作立即应用新值。

## D021 (2026-10-24, Lead) — 用 Git merge 同步 upstream v0.3.2

上游 `v0.3.1` 之后到 `v0.3.2` 只有两个提交：

- `2dd16c8 fix: subcheck auto-start record pending will mark as done`；
- `a14359f fix: bump Go toolchain to 1.26.10 for emSign CA distrust`。

本次使用 `git merge --no-ff v0.3.2` 合并，保留上游作者、提交时间和提交信息；
不 cherry-pick、不 squash、不 rebase。冲突解决原则：

- `internal/services/subcheck/check.go`：合并上游 pending 处理与 fork 的
  title filter、duration、stream options、retry backoff；
- `go.mod`：采用上游 `go 1.26.10`，保留 fork 依赖；
- 根 `Dockerfile` 和 `.github/`：保持 fork 的删除/迁移决定，继续使用 `deploy/` 与 `.forgejo/`；
- `deploy/upstream.Dockerfile`：同步 builder 到 `golang:1.26.10-alpine`。

上游 `develop` 的 preview、文件浏览时间过滤等未发布提交没有合并：它们不属于 `v0.3.2`，
且与 fork 的播放、列表与测试结构有交叉。以后需要时单独评估。
