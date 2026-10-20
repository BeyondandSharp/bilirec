# bilirec + Rust FLV repair (recording only)

以 [Bilirec](https://github.com/bilirec/bilirec) 为底座（调度 / REST API / WebUI 后端 / 三格式原生拉流），
吸收录播姬的 FLV 修复能力与 biliup 的 streamlink/ffmpeg 拉流方式。**仅关注录制**：不做上传、不做移动端。

## 组成

| 路径 | 内容 |
|---|---|
| `cmd/` `internal/` `pkg/` `utils/` | Bilirec v0.3.1（MIT，commit `26f4f8d`）——**仓库根目录就是 fork 后的 Go module**：调度/REST API/WebUI 后端/三格式原生拉流 + 外部下载器、`flvrepair` 侧车接入 |
| `web/` | vendored bilirec-web（React PWA，MIT，commit `ab62df3`）：下载器选择与录制流水线徽章 |
| `rust/crates/flv-repair/` | 零依赖 Rust crate + `flvrepair` CLI（GPL-3.0-or-later，移植自录播姬 FLV 处理规则） |
| `testdata/flv/` | R1–R8 冻结损坏样本 + `*.expect.json` 不变量 + 确定性生成器 |
| `testdata/hls/` | 本地 HLS ts / fMP4 回放样本 |
| `scripts/` | 本地回放源（限速/断流/损坏注入）、E2E、fixtures 生成 |
| `deploy/` | 多阶段 Dockerfile + docker-compose |
| `docs/` | `INTERFACES.md`（冻结契约）、`PORTING.md`（C#→Rust 规格）、`DECISIONS.md` |

## 仓库结构（fork 布局）

本仓库直接从 Bilirec 源码树 fork：**根目录就是 Go module**（`go.mod`、`cmd/`、`internal/`、`pkg/`、`utils/`），
新增能力以并行目录加入，而不是把上游塞进子目录：

```
.
├── cmd/ internal/ pkg/ utils/     # Bilirec fork 主体（Go）
├── dotenv.go swagger.go go.mod    # 上游根文件
├── docs/                          # 契约/决策/运维文档 + 生成的 swagger（gitignored）
├── rust/crates/flv-repair/        # flvrepair 侧车（Rust）
├── web/                           # vendored bilirec-web（React PWA）
├── deploy/                        # 本仓库部署（Dockerfile/compose/env 示例）
├── scripts/ testdata/ refs/       # 回放源/E2E/fixtures/只读参考
├── build.sh build-alpine.sh       # 一键构建 tar（Debian / Alpine）
├── .github/ CONTRIBUTING.md       # 上游 CI/贡献文件（保留）
├── LICENSES/Bilirec-MIT.txt       # 上游 MIT 许可
└── docs/upstream-bilirec-*.md     # 上游 README/Makefile 存档
```

上游 `Dockerfile` 存档在 `deploy/upstream.Dockerfile`；实际部署用 `deploy/` 下的多阶段镜像。

## 构建与测试

```bash
make build   # dist/bilirec + dist/flvrepair
make fmt     # 只格式化本 worktree 修改过的 Go 文件 + cargo fmt
make lint    # go vet + cargo clippy -D warnings
make test    # go test -short ./... + cargo test
make e2e     # 本地回放源端到端（离线，109 项检查）
```

工具链：Go 1.27+、Rust stable、ffmpeg、streamlink、Python 3；前端需要 Node 24+ 与 pnpm。

## 一键构建镜像（导出 tar，不落本机 images）

默认构建 **slim** 镜像（**不含 ffmpeg/streamlink**，只支持 native 录制）；
加 `--full` 构建全功能镜像（带 ffmpeg + streamlink，支持外部下载器与 mp4 转换），tag 加 `-full` 后缀。
两种脚本都通过 `docker buildx build --output type=docker,dest=...` 直接导出 docker-loadable tar，
**不会留在本机 `docker images`**；有任一选择参数时不产出 all-in-one。

```bash
./build.sh                     # slim all-in-one -> dist/local.tar
./build.sh --full              # full all-in-one -> dist/local-full.tar
./build.sh --backend           # slim backend   -> dist/local-backend.tar
./build.sh --backend --full    # full backend   -> dist/local-backend-full.tar
./build.sh --frontend          # slim frontend  -> dist/local-frontend.tar
./build.sh --all               # 全部 slim tar + ./dist 静态文件
./build.sh --all --full        # 全部 full tar + ./dist 静态文件

./build-alpine.sh              # Alpine slim all-in-one -> dist/local-alpine.tar
./build-alpine.sh --full       # Alpine full all-in-one -> dist/local-alpine-full.tar
./build-alpine.sh --all        # 全部 Alpine slim 产物
./build-alpine.sh --all --full # 全部 Alpine full 产物
```

| 产物 | Debian slim | Debian full | Alpine slim | Alpine full |
|---|---|---|---|---|
| all-in-one tar | `local.tar` 57MB | `local-full.tar` 131MB | `local-alpine.tar` 18MB | `local-alpine-full.tar` 98MB |
| all-in-one 镜像 | 238MB | 525MB | 69MB | 390MB |
| backend tar / 镜像 | 53MB / 220MB | 127MB / 507MB | 16MB / 60MB | 97MB / 383MB |
| frontend tar / 镜像 | 21MB / 76MB | 21MB / 76MB | 29MB / 105MB | 29MB / 105MB |

- **slim（默认）**：无 ffmpeg/streamlink。native 录制 FLV/HLS 正常，`flvrepair` 修复正常；
  `downloader=ffmpeg|streamlink` 不可用（回退 native），`CONVERT_TO_MP4` 转码不可用。
  前端启动时查询 `GET /convert/providers`，没有可用转码后端（如 slim 镜像）时自动隐藏“转换任务”菜单。
- **full（`--full`）**：额外包含 ffmpeg + streamlink，支持外部下载器与录制后 mp4/m4a 转换。
- 前端镜像本身不依赖 ffmpeg/streamlink，`-full` 后缀仅用于命名对称，内容与 slim 相同。
- `deploy/docker-compose.yml` 的 compose 部署默认 `WITH_EXTRA=1`（full，保持原有行为）；
  想要 slim 构建传 `WITH_EXTRA=0`。

**ffmpeg 选择**
- Debian full：复制 `mwader/static-ffmpeg:7.1` 的静态 `ffmpeg`（129MB），不装 apt ffmpeg
  （避免拉 LLVM/mesa/x265 约 450MB 依赖）。静态构建**不含 `ffprobe`**；转码仍可用（校验走 moov 扫描），
  但 `NO_CONVERT_IF_INVALID=true` 时缺少 ffprobe 会阻止转码。
- Alpine full：实测 `apk add ffmpeg` 整套约 243MiB，复制 129MB 静态二进制约 248MiB，**apk 更小且原生 musl**，
  因此用 apk 的 ffmpeg（自带 ffprobe）+ streamlink（8.4）。

**musl 说明**
- `flvrepair`（Rust）在 Alpine 镜像里用 `rust:1-alpine` 重新编译为 musl 目标；Debian 的 glibc 版本不能在 Alpine 运行。
- `bilirec`（Go）为 `CGO_ENABLED=0` 静态二进制；Alpine 的 nginx/ffmpeg/streamlink（full）均为 apk 原生 musl 包。
- `streamlink`：Debian full 为 pip 8.6.2，Alpine full 为 apk 8.4。

使用：

```bash
docker load -i dist/local.tar        # 或 dist/local-full.tar / dist/local-alpine-full.tar
docker run --rm -p 8080:80 -v bilirec-data:/data local
# 打开 http://localhost:8080
```

**Alpine slim compose 示例**：[deploy/docker-compose.alpine-slim.yml](deploy/docker-compose.alpine-slim.yml)
—— 单容器前后端同源（nginx 80 + 容器内 bilirec 8080），基于 `local-alpine`（非 full，无 ffmpeg/streamlink），
端口默认 8080/8081，可用 `ALPINE_WEB_PORT` / `ALPINE_API_PORT` 覆盖；不读取 `deploy/.env`，避免和主部署冲突。
先用 `./build-alpine.sh && docker load -i dist/local-alpine.tar`，再
`docker compose -f deploy/docker-compose.alpine-slim.yml up -d`（或直接 `up -d --build`）。

## 前端（web/，本仓库已内置改造）

`web/` 是上游 `bilirec-web` 的 vendored 副本（MIT，commit `ab62df3`），本地改动：

- **开始录制对话框**（records 视图）新增“下载器”选择：自动 / 原生 / FFmpeg / Streamlink；
  自动 = 不发送参数、沿用后端 `DOWNLOADER` 默认；其他值作为 `downloader=` query 传给
  `POST /record/{room}/start`。
- **录制卡片**新增徽章：实际下载器（仅 ffmpeg/streamlink）、修复状态（“修复”/“修复降级”），
  以及外部下载器把源 ts/fMP4 转封装为 FLV 时显示“源 HLS-TS / 源 HLS-FMP4”。
- 订阅（RoomConfig）路径不显示下载器选择：后端房间配置没有该字段，保持 `auto`。
- **设置页（FEATURES-V2）**：新增“设定”导航页，覆盖分段/命名与过滤/封面/弹幕/FLV 处理/B 站高级六组设置；
  房间配置对话框对每组提供“继承全局 / 自定义”三态覆盖。详细字段与优先级见
  [docs/FEATURES-V2.md](docs/FEATURES-V2.md) 与 [docs/OPERATIONS-V2.md](docs/OPERATIONS-V2.md)。

构建与自托管：

```bash
cd web
pnpm install --frozen-lockfile
pnpm lint && pnpm build     # dist/ 为静态产物
```

- 继续使用官方 PWA：打开 <https://app.bilirec.org/>，登录页填后端地址即可。
- 自托管：`docker compose -f deploy/docker-compose.yml up --build`，`web` 服务在
  `WEB_PORT`（默认 8081）提供前端，`bilirec` 在 `BILIREC_PORT`（默认 8080）；打开
  `http://<host>:8081` 填后端地址。后端 CORS 由 `FRONTEND_URLS` 控制（compose 默认
  `http://localhost:8081`，反代/域名场景记得改成实际 origin）。

## 自动发布镜像（Forgejo Actions）

推送 **semver tag**（如 `v0.5.0`）会触发
[.forgejo/workflows/docker-publish.yml](.forgejo/workflows/docker-publish.yml)，
按 [.forgejo/variants.txt](.forgejo/variants.txt) 构建 8 个变体并发布到 **Docker Hub** 与 **ghcr.io**：

```text
alpine (latest), trixie,
alpine-full, trixie-full,
alpine-headless, trixie-headless,
alpine-full-headless, trixie-full-headless
```

- `alpine` 是默认变体：额外拥有 `latest` 与无后缀版本 tag；
- `headless` = 仅后端；`full` = 含 ffmpeg/streamlink；`trixie` = Debian 13 运行时；
- 平台默认 `linux/amd64`；需要 arm64 时改 variants.txt 第七列或设 `DOCKER_PLATFORMS`；
- 一次性配置（Actions、privileged runner、`DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN`/`GHCR_TOKEN`/`GHCR_IMAGE`）
  与 tag 列表详见 [docs/RELEASE-CI.md](docs/RELEASE-CI.md)。

## 使用已发布镜像（latest）

CI 发布后可以直接用 `latest` 镜像起一个“前后端合一”的实例（`latest` = `alpine` 变体，
slim：不含 ffmpeg/streamlink，native 录制；转码菜单会自动隐藏）。把
`BILIREC_IMAGE` 换成你的 Docker Hub 命名空间（或 `ghcr.io/<owner>/bilirec:latest`）：

```yaml
services:
  bilirec:
    image: ${BILIREC_IMAGE:-beyondandsharp/bilirec:latest}
    container_name: bilirec
    init: true
    restart: unless-stopped
    stop_grace_period: 30s
    ports:
      # 前端 + 同源 API（容器内 nginx 80，反代到 bilirec 8080）
      - "${WEB_PORT:-8080}:80"
      # 可选：直连后端 API
      - "${API_PORT:-8081}:8080"
    environment:
      TZ: ${TZ:-Asia/Shanghai}
      BILIBILI_LOGIN_MODE: ${BILIBILI_LOGIN_MODE:-anonymous}
    volumes:
      - records:/data/records
      - database:/data/database
      - secrets:/data/secrets

volumes:
  records:
  database:
  secrets:
```

```bash
# 使用自己的镜像名
BILIREC_IMAGE=yourname/bilirec:latest docker compose -f docker-compose.yml up -d

# 打开 http://localhost:8080 ；登录页后端地址填 http://localhost:8080
# 需要 ffmpeg/streamlink 与 mp4 转码时，把 image 改为 :alpine-full 或 :trixie-full
```

- `:latest` / `:alpine`：Alpine 前后端合一，slim；
- `:trixie`：Debian 13 前后端合一，slim；
- `:alpine-full` / `:trixie-full`：带 ffmpeg + streamlink（外部下载器、转码可用）；
- `:alpine-headless` / `:trixie-headless`（及 `-full-headless`）：仅后端，不带前端。

> 完整 tag 列表与发布配置见 [docs/RELEASE-CI.md](docs/RELEASE-CI.md)。

## 配置

| 环境变量 | 取值 | 默认 | 说明 |
|---|---|---|---|
| `DOWNLOADER` | auto/native/ffmpeg/streamlink | auto | auto 等同 native（不启外部进程） |
| `FFMPEG_BIN` / `STREAMLINK_BIN` | path | ffmpeg / streamlink | 外部下载器二进制 |
| `EXTERNAL_START_TIMEOUT_SECS` | int | 20 | 首字节超时；超时杀进程树并回退 native |
| `FLV_REPAIR` | auto/on/off | auto | auto = 能找到 `flvrepair` 即启用 |
| `FLV_REPAIR_BIN` | path | flvrepair | 修复侧车 |
| `FLV_REPAIR_MODE` | best-effort/strict | best-effort | 透传侧车 |
| `FLV_REPAIR_MAX_BUFFER_MB` | int | 64 | 透传侧车 `--max-buffer` |

录制启动接口支持单次覆盖：`POST /record/{room}/start?downloader=ffmpeg`（或 JSON `{"downloader":"ffmpeg"}`）。
批量/离线修复直接用 Rust CLI：`flvrepair file --input in.flv --output out.flv`。

## 录制状态接口（T7，加性变更）

`POST /record/stats`（以及批量 stats）每条记录新增：

| 字段 | 取值 | 说明 |
|---|---|---|
| `downloader` | native/ffmpeg/streamlink | 本会话实际使用的下载器（外部启动失败回退时为 native） |
| `source_format` | flv/ts/fmp4 | B 站原始流格式；`actual_stream_format` 仍是落盘格式（外部下载器为 flv） |
| `repair` | off/on/degraded | FLV 修复实时状态（on 后若侧车降级会变 degraded） |

前端已消费这些字段（录制卡片徽章）；全局设置已加入设置页（见下节）。

**已知限制**（D004）：Enhanced-FLV HEVC 房间（如部分 4K/HDR 赛事）上，`ffmpeg -c copy -f flv`
可能报 “dimensions not set” 并回退 native；native 录制与 `flvrepair` 修复对这类流正常。

## FEATURES-V2（v0.3.0，设置与录制能力补齐）

对应录播姬/biliup 的以下能力已实现（契约见 [docs/FEATURES-V2.md](docs/FEATURES-V2.md)）：

| 编号 | 能力 | 入口 |
|---|---|---|
| R02/B02 | 按时间/大小/标题变化分段；过小文件过滤 | 设置页“分段” / 房间覆盖 |
| R03 | 路径模板（相对 OUTPUT_DIR，可用 `/` 建子目录；`{uname}` `{roomid}` `{title}` `{date}`…，支持 strftime） | 设置页“命名与过滤” |
| R04 | 标题正则过滤（自动录制跳过） | 设置页“命名与过滤” |
| R05 | 保存直播封面（`<视频名>-cover.jpg`） | 设置页“封面” |
| R06/R08 | 弹幕类型开关（raw 事件/SC/礼物/舰长）、传输 auto/ws/wss | 设置页“弹幕” |
| R14 | FLV 写 metadata、Script tag 切段、Annex-B 切段开关 | 设置页“FLV 处理” |
| R15 | `flvrepair export` / `danmaku-start-time` / `danmaku-merge` | Rust CLI |
| R17 | 全局设置页 + 房间级覆盖 | `GET/PUT /settings`、`POST /settings/reset`、`GET/PUT /room/{id}/config` |
| B05 | B 站高级拉流：API host/备用 host、CDN 优选/回退、强制原画、cn01 替换 | 设置页“B 站高级” |

设置优先级：**房间覆盖 > DB 全局设置 > env 默认 > 内置默认**；变更只对**新会话**生效。
环境变量清单、API 示例、部署/排障见 [docs/OPERATIONS-V2.md](docs/OPERATIONS-V2.md)。

## 验证状态（v0.4.0）

- `cargo test --workspace`：**95 passed**；`go test -short ./...`：全绿；`go vet`、`cargo clippy -D warnings` 通过。
- `flvrepair`：R1–R8 全部修复并满足 `.expect.json`；clean AVC/HEVC 流 `stream` 模式字节级 round-trip；
  `analyze` schema v1；1080p 6.4 Mbps 约 800× 实时，峰值 RSS ≈ 11 MiB；`--write-metadata` 与 Toolbox 三命令通过 golden 测试。
- `make e2e`：离线 **109/109** 通过（设置 API/房间覆盖/校验矩阵/鉴权矩阵/持久化 + 原有下载器/断流/损坏注入/backend API）。
- 前端：`pnpm lint` 通过、`pnpm build`（tsc + vite）通过；设置页与房间覆盖有 Playwright 无头浏览器 smoke（27/27）。
- 真实直播录制（房间 6，匿名）：
  - 默认设置：`【标题】-YYYYMMDD_HHMMSS.flv` 命名，`downloader=native repair=on`，行为与 v0.2.0 一致；
  - 自定义设置：模板命名生效、`segment_mode=time/1min` 产出 `...-1.flv` 分段、封面 jpg 生成、
    `flv_write_metadata=false` 时输出无 onMetaData；重置设置后恢复默认命名。
- 路径模板（v0.3.1 追加）：真实录制 `{uname}-{roomid}/{date}/{title}-{time}` 正确生成
  `records/<uname>-<roomid>/<date>/<title>-<time>.flv` + 同目录封面；绝对路径/`..` 模板返回 400。
- 目录重构（v0.4.0）：仓库根目录即 Bilirec fork（Go module），`web/`/`rust/`/`deploy/` 等为并行目录；
  `go build/vet/test`、`make e2e` 109/109、Docker 双镜像构建、部署后录制与设置持久化均通过。
- Docker：`deploy/Dockerfile` + `deploy/web.Dockerfile` 构建成功；部署于 `50300/50301`（镜像 `v0.4.0`），
  设置持久化跨容器重启验证通过。

## 许可证

本仓库整体 **GPL-3.0-or-later**（`rust/crates/flv-repair` 移植自 GPL-3.0 的录播姬 FLV 规则）。
Bilirec / biliup / bilirec-web 的 MIT 归属保留在 `NOTICE`。上游来源见 `UPSTREAM.md`。
