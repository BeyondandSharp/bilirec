# DEVELOPMENT — 构建、测试、架构与验证

本文档包含开发/构建/测试/架构/验证内容，从根 `README.md` 拆分而来（CONFIG-V2 §9）。
部署与运维请看根 `README.md`、[OPERATIONS-V2.md](OPERATIONS-V2.md)；
接口契约见 [CONFIG-V2.md](CONFIG-V2.md)、[FEATURES-V2.md](FEATURES-V2.md)、[INTERFACES.md](INTERFACES.md)。

## 仓库结构（fork 布局）

本仓库直接从 Bilirec 源码树 fork：**根目录就是 Go module**（`go.mod`、`cmd/`、`internal/`、`pkg/`、
`utils/`），新增能力以并行目录加入，而不是把上游塞进子目录：

```
.
├── cmd/ internal/ pkg/ utils/     # Bilirec fork 主体（Go）
├── dotenv.go swagger.go go.mod    # 上游根文件
├── docs/                          # 契约/决策/运维文档 + 生成的 swagger（gitignored）
├── rust/crates/flv-repair/        # flvrepair 侧车（Rust，GPL-3.0-only）
├── web/                           # vendored bilirec-web（React PWA，MIT）
├── deploy/                        # 部署：多阶段 Dockerfile/compose/env 示例/entrypoint
├── scripts/ testdata/ refs/       # 回放源/E2E/fixtures/只读参考
├── build.sh build-alpine.sh       # 一键构建镜像并导出 tar（Debian / Alpine）
├── .github/ CONTRIBUTING.md       # 上游 CI/贡献文件（保留）
├── LICENSES/Bilirec-MIT.txt       # 上游 MIT 许可
└── docs/upstream-bilirec-*.md     # 上游 README/Makefile 存档
```

上游 `Dockerfile` 存档在 `deploy/upstream.Dockerfile`；实际部署用 `deploy/` 下的多阶段镜像。

## 工具链

- Go 1.27+（`/usr/local/go`），`GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=off`（环境已设置）
- Rust stable（`/root/.cargo`）
- Node 24+ 与 pnpm（`web/`）
- ffmpeg / streamlink / Python 3（E2E 与本地回放源）

## 构建与测试（根目录 Makefile）

```bash
make build   # dist/bilirec + dist/flvrepair
make fmt     # 只格式化本 worktree 修改过的 Go 文件 + cargo fmt
make lint    # go vet + cargo clippy -D warnings
make test    # go test -short ./... + cargo test
make e2e     # 本地回放源端到端（离线，见下）
make image   # docker build -f deploy/Dockerfile .
```

`make build` 会通过 `git describe --tags --always --dirty` 把版本注入
`pkg/updatecheck.currentVersionInjected`（CONFIG-V2 §8）。Docker 构建则由 `ARG VERSION` 注入
（CI 传 release tag，`build.sh`/`build-alpine.sh` 默认用 `git describe`）。

### 前端（web/）

```bash
cd web
pnpm install --frozen-lockfile
pnpm lint
pnpm build      # dist/ 静态产物（tsc + vite）
```

### Rust 侧车（rust/）

```bash
cargo test --manifest-path rust/Cargo.toml --workspace
cargo clippy --manifest-path rust/Cargo.toml --workspace --all-targets -- -D warnings
cargo build --manifest-path rust/Cargo.toml --workspace --release
# flvrepair 位于 rust/target/release/flvrepair（make build 会复制到 dist/）
```

## 架构与数据流

```
internalStart
  -> bilibili.GetLiveRoomInfo / GetStreamURLsV2（B05 高级拉流配置）
  -> connectStream
       native: resty body / HLS session -> <-chan []byte
       external: ffmpeg/streamlink downloader.Open -> stream.ReadExternalStream
  -> repair.WrapStream（FLV only，flvrepair sidecar，失败降级不阻断录制）
  -> commitSession + prepare -> rotate（分段/命名/过滤/封面）-> pipeline（processors/writer）
```

- 配置：`internal/modules/configfile` 在 `logger.Init` 之前加载 `config.json`/`secrets.json`/`.env`，
  记录来源（`env|file|secret-file|dotenv|default`），只写进程环境中缺失的键。
- 设置：`internal/services/settings` 提供 FEATURES-V2 元数据/校验/Effective 快照；全局特性覆盖存
  `config.json`，房间覆盖存 `database/subscribes.db`。
- API：`/settings`（特性）、`/config` + `/config/secrets`（统一配置中心）、`/room/{id}/config`、
  `/convert/providers`（转码能力探测）。
- 前端：`web/` 是上游 bilirec-web 的 vendored 副本，已加入下载器选择/录制流水线徽章、FEATURES-V2
  设置页、统一设置 UI（系统配置/敏感配置）、三语言与标题、转码能力隐藏、仓库链接。

## Fixtures 与本地回放源

| 目录 | 内容 |
|---|---|
| `testdata/flv/` | R1–R8 冻结损坏样本 + `*.expect.json` 不变量 + 确定性生成器；V2 split-detector 样本 |
| `testdata/hls/` | 本地 HLS ts / fMP4 回放样本（12s，320x180） |
| `scripts/origin.py` | 本地回放源：`/flv` 限速/断流/损坏注入、`/hls` 静态服务、`--selftest` |
| `scripts/gen_hls_fixtures.sh` | 确定性重生成 HLS 样本（`--check` 校验） |

```bash
python3 scripts/origin.py --selftest           # 18 项自检
python3 testdata/flv/gen/gen.py --check        # R1–R8 样本可复现
python3 testdata/flv/gen/gen_v2_detectors.py --check
scripts/gen_hls_fixtures.sh --check
```

## E2E（`scripts/e2e.sh` / `make e2e`）

离线端到端：构建 → 本地回放源 → ffmpeg/streamlink 下载器命令行 → `/flv` 故障注入 →
`flvrepair stream/file/analyze` → 后端启动（临时 `OUTPUT_DIR`/`DATABASE_DIR`/`SECRET_DIR`、
随机端口）→ FEATURES-V2 `/settings`/房间配置/鉴权矩阵/持久化 → CONFIG-V2 `/config`+
`/config/secrets`/来源优先级/默认文件名/转码能力 → 可选真实直播录制。

| 变量 | 说明 |
|---|---|
| `E2E_LIVE_ROOM_ID` | 可选真实直播房间（需要外网 + 正在直播）；离线跑不设置 |
| `E2E_SKIP_BUILD=1` | 复用已有 `dist/` 二进制 |
| `E2E_KEEP_TMP=1` | 保留临时目录与日志 |
| `E2E_ORIGIN_PORT` / `E2E_BACKEND_PORT` | 固定端口（默认随机） |
| `E2E_SKIP_FLVREPAIR` / `E2E_SKIP_SETTINGS_V2` | 仅开发排障用的跳过开关；验收不设置 |

完整列表与 109+ 检查说明见 `scripts/README.md`。离线不能真实开录（后端会解析真实 B 站房间），
真实录制路径通过 `E2E_LIVE_ROOM_ID` 显式开启，不伪造。

## 镜像构建（细节）

```bash
# 单镜像 / 多阶段（context = 仓库根）
docker build -f deploy/Dockerfile -t bilirec-rust:dev .
docker build -f deploy/web.Dockerfile -t bilirec-web:dev .        # 前端 nginx 镜像
docker build -f deploy/combined.Dockerfile -t local .             # Debian 前后端合一
docker build -f deploy/alpine.Dockerfile -t local-backend-alpine . # Alpine 仅后端
docker build -f deploy/alpine-combined.Dockerfile -t local-alpine . # Alpine 前后端合一
docker build -f deploy/alpine-web.Dockerfile -t local-frontend-alpine .
```

- Build args：`VERSION`、`WITH_EXTRA`（0=slim，1=full）、`GOPROXY_URL`、`SWAG_VERSION`、
  `STREAMLINK_VERSION`、`GO_IMAGE`/`RUST_IMAGE`/`NODE_IMAGE`/`RUNTIME_IMAGE`/`FFMPEG_IMAGE`。
- `WITH_EXTRA=0`（slim）：无 ffmpeg/streamlink；native 录制与 flvrepair 可用，转码菜单自动隐藏。
- `WITH_EXTRA=1`（full）：含 ffmpeg + streamlink（Debian 用静态 ffmpeg 7.1；Alpine 用 apk 版本）。
- 构建上下文由 `deploy/*.Dockerfile.dockerignore` 裁剪（排除 `web/`、`refs/`、`testdata/`、
  `scripts/`、`backups/`、`dist/`、`docs/`、大部分 `deploy/`、`rust/target`；保留 `rust/` 源码、
  `deploy/entrypoint/`，以及根 `.env`——`dotenv.go` 通过 `//go:embed .env` 内嵌它）。
- 运行时入口是 POSIX-sh `deploy/entrypoint/docker-entrypoint.sh`：校验/调整 `PUID`/`PGID`、
  chown `/app/{config,secrets,database,records}`、用 `gosu`/`su-exec` 降权；combined 镜像保持
  nginx root，只降权 bilirec 子进程。

### build.sh / build-alpine.sh（导出 tar）

```bash
./build.sh                     # slim all-in-one -> dist/local.tar
./build.sh --full              # full all-in-one -> dist/local-full.tar
./build.sh --backend|--frontend|--frontend-static|--all [--full]

./build-alpine.sh              # Alpine slim all-in-one -> dist/local-alpine.tar
./build-alpine.sh --full       # Alpine full all-in-one -> dist/local-alpine-full.tar
./build-alpine.sh --all [--full]
```

`VERSION` 环境变量可覆盖镜像内版本（默认 `git describe`）。镜像通过
`docker buildx build --output type=docker,dest=...` 导出为 docker-loadable tar，不留在本机 images。

| 产物 | Debian slim | Debian full | Alpine slim | Alpine full |
|---|---|---|---|---|
| all-in-one tar / 镜像 | 57MB / 238MB | 131MB / 525MB | 18MB / 69MB | 98MB / 390MB |
| backend tar / 镜像 | 53MB / 220MB | 127MB / 507MB | 16MB / 60MB | 97MB / 383MB |
| frontend tar / 镜像 | 21MB / 76MB | 21MB / 76MB | 29MB / 105MB | 29MB / 105MB |

## 发布（Forgejo Actions）

推送 semver tag（如 `v0.5.0`）触发 `.forgejo/workflows/docker-publish.yml`，按
`.forgejo/variants.txt` 构建 8 个变体并发布到 Docker Hub 与 ghcr.io：
`alpine`/`trixie` × `combined`/`headless` × `slim`/`full`。`alpine` 为默认变体（拥有 `latest`）。
平台默认 `linux/amd64`。一次性配置、tag 列表与变体语法详见 [RELEASE-CI.md](RELEASE-CI.md)。

## 验证状态（最近一次）

- `go test -short ./...`：全绿；`go vet`、`cargo clippy -D warnings` 通过；`cargo test --workspace` 95+ 通过。
- `flvrepair`：R1–R8 全部修复并满足 `.expect.json`；clean AVC/HEVC `stream` 字节级 round-trip；
  `analyze` schema v1；`--write-metadata` 与 Toolbox 三命令 golden 测试通过。
- `make e2e`：离线全绿（含 CONFIG-V2 `/config`/`/config/secrets`/来源优先级/默认文件名/转码能力）。
- 前端：`pnpm lint`、`pnpm build` 通过；Playwright 无头 smoke（设置页/房间覆盖/标题/转码隐藏）。
- Docker：Debian/Alpine、combined/backend 构建通过；PUID/PGID 与 config/secrets 卷行为有容器级验证。
- 真实直播冒烟：默认设置与各新设置（分段/模板/过滤/封面/弹幕/FLV/B05）录制验证通过；
  设置跨容器重启持久化验证通过。

## 许可证

本仓库整体 **GPL-3.0-only**（`rust/crates/flv-repair` 移植自 GPL-3.0-only 的录播姬 FLV 规则）。
Bilirec / biliup / bilirec-web 的 MIT 归属保留在 `NOTICE`。上游来源见 `UPSTREAM.md`。
