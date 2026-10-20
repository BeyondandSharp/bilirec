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
├── build.sh                       # 一键构建镜像/静态文件
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

## 前端（bilirec-web，本仓库已内置改造）

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
