# deploy/ — container image

Multi-stage image for `bilirec` + the Rust `flvrepair` sidecar, based on
`docs/INTERFACES.md` §2.3 (env contract), `docs/FEATURES-V2.md` §1.1 (new env
defaults), §5 (build) and `docs/CONFIG-V2.md` §1/§7/§8 (config center, PUID/PGID,
VERSION).

## Build

```bash
# from the repo root
docker build -f deploy/Dockerfile -t bilirec-rust:dev .

# frontend image (sources live in web/)
docker build -f deploy/web.Dockerfile -t bilirec-web:dev .

# compose (named volumes for records/database/secrets; starts backend + web)
docker compose -f deploy/docker-compose.yml up --build
```

The repository root **is** the Go module (`go.mod`, `cmd/`, `internal/`,
`pkg/`, `utils/` at the root), so the Docker build context is the repo root.
`deploy/Dockerfile.dockerignore` keeps that context small (excludes `web/`,
`refs/`, `testdata/`, `scripts/`, `backups/`, `dist/`, `docs/`, `deploy/` and
`rust/target`, but keeps the `rust/` sources for the sidecar stage).

Stages: `go-build` (Go backend **with generated swagger docs**), `rust-build`
(`cargo build --release`, binary `flvrepair`), `runtime` (Debian/Python slim +
`ffmpeg` + `streamlink`; both binaries on `PATH`; non-root uid/gid 10001).

Build args: `GO_IMAGE`, `RUST_IMAGE`, `RUNTIME_IMAGE`, `FFMPEG_IMAGE`,
`GOPROXY_URL`, `SWAG_VERSION`, `STREAMLINK_VERSION`, `WITH_EXTRA` (`0` = slim,
`1` = full ffmpeg/streamlink) and `VERSION` (default `dev`; embedded into
`pkg/updatecheck.currentVersionInjected`). Example:

```bash
docker build --build-arg VERSION=v0.4.0 -f deploy/Dockerfile -t bilirec-rust:v0.4.0 .
```

`./build.sh` / `./build-alpine.sh` pass `VERSION` from the environment or
`git describe --tags --always --dirty`; the Forgejo release workflow passes the
release tag automatically.

## Configuration (`.env`)

```bash
cp deploy/.env.example deploy/.env   # deploy/.env is gitignored
$EDITOR deploy/.env
docker compose -f deploy/docker-compose.yml up -d --build
```

`deploy/.env.example` documents every supported key, grouped as:

- publishing (`BILIREC_PORT`, `WEB_PORT`, `FRONTEND_URLS`, `TZ`)
- Bilibili login (`BILIBILI_LOGIN_MODE`) and optional API auth
  (`USERNAME`, `PASSWORD`, `VIEWER_USERNAME`, `VIEWER_PASSWORD`, `JWT_SECRET`)
- recording pipeline (`DOWNLOADER*`, `FLV_REPAIR*`, `FFMPEG_BIN`,
  `STREAMLINK_BIN`, `EXTERNAL_START_TIMEOUT_SECS`)
- **FEATURES-V2 settings defaults**: `SEGMENT_*` (`MODE/MINUTES/SIZE_MB/BY_TITLE`),
  `TITLE_POLL_SECS`, `MIN_FILE_SIZE_MB`, `FILENAME_TEMPLATE`,
  `TITLE_FILTER_PATTERNS`, `SAVE_STREAM_COVER`, `DANMAKU_RECORD_TYPES`,
  `DANMAKU_TRANSPORT`, `FLV_WRITE_METADATA`, `FLV_SPLIT_ON_SCRIPT_TAG`,
  `FLV_DISABLE_SPLIT_ON_ANNEXB`, `BILI_LIVE_API_HOST`,
  `BILI_LIVE_API_FALLBACK_HOST`, `BILI_CDN`, `BILI_CDN_FALLBACK`,
  `BILI_FORCE_SOURCE`, `BILI_REPLACE_CN01`

The compose `bilirec` service loads `deploy/.env` as an optional `env_file`, so
any uncommented key becomes container environment; unset keys keep the backend
built-in defaults. Env values are only **defaults** — persisted overrides from
`PUT /settings` and per-room overrides from `PUT /room/<id>/config` win, and all
setting changes apply to new sessions only. See
[`docs/OPERATIONS-V2.md`](../docs/OPERATIONS-V2.md) for precedence, API examples
and troubleshooting.

## Config center: `config.json` + `secrets.json` (CONFIG-V2 §1)

Runtime settings can live in flat JSON files instead of (or in addition to) the
environment:

| File | Container path | Mode | Contents |
|---|---|---|---|
| `CONFIG_FILE` | `/app/config/config.json` | `0644` | non-secret `{"ENV_KEY":"value"}` map |
| `SECRETS_FILE` | `/app/config/secrets.json` | `0600` | only `PASSWORD`, `VIEWER_PASSWORD`, `JWT_SECRET`, `CLOUDCONVERT_API_KEY`, `FRP_TOKEN`, `NOTIFY_SSE_TOKEN` |

- compose mounts the named `config` volume at `/app/config`; standalone runs use
  `-v bilirec-config:/app/config`. Values in a mounted `config.json`/`secrets.json`
  are applied on startup.
- precedence: **system env > config/secrets file > `.env` > built-in default**
  (FEATURES-V2 room overrides still win per session). See
  `docs/CONFIG-V2.md` for the full contract and `docs/OPERATIONS-V2.md` for
  day-2 operations.
- `deploy/config.example.json` is a minimal non-secret example.
- config/secrets files are never baked into the image; the container never
  auto-writes `.env`. The entrypoint chowns `/app/config` (and `/app/secrets`,
  which is the `SECRET_DIR` for Bilibili cookies) to `PUID:PGID`.
- HTTP API: `GET/PUT /config`, `GET/PUT /config/secrets`, `POST
  /config/reset`, `POST /config/secrets/reset` (admin write, admin+viewer read).

## PUID / PGID (backend + combined images, CONFIG-V2 §7)

`PUID`/`PGID` default to `10001`. The POSIX-sh entrypoint
(`deploy/entrypoint/docker-entrypoint.sh`, used by both Debian and Alpine and by
the backend-only and combined images):

1. validates that both values are numeric and fails with a clear message if the
   requested id is already used by another user/group;
2. adjusts the `bilirec` user/group ids (`usermod`/`groupmod` on Debian,
   `adduser`/`addgroup` on Alpine);
3. creates and chowns `/app/config`, `/app/secrets`, `/app/database`,
   `/app/records` (records recursively only when its top-level owner differs, so
   large recording trees stay cheap);
4. drops privileges via `gosu` (Debian) / `su-exec` (Alpine). Backend images exec
   `bilirec` as `PUID:PGID`; combined images keep nginx running as root and run
   the bilirec child as `PUID:PGID`, forwarding SIGTERM/SIGINT to both.

```bash
docker run --rm -p 8080:8080 -e PUID=1234 -e PGID=2345 \
  -v bilirec-config:/app/config \
  -v bilirec-records:/app/records \
  -v bilirec-database:/app/database \
  -v bilirec-secrets:/app/secrets \
  bilirec-rust:dev
```

The frontend nginx-only image is unaffected (it keeps its default nginx user).

## Run

```bash
docker run --rm -p 8080:8080 \
  -e PUID=10001 -e PGID=10001 \
  -v bilirec-config:/app/config \
  -v bilirec-records:/app/records \
  -v bilirec-database:/app/database \
  -v bilirec-secrets:/app/secrets \
  bilirec-rust:dev
```

`/version` is the health endpoint; the image has a `HEALTHCHECK` that polls it.
To mount existing files directly instead of the `config` volume, override
`CONFIG_FILE`/`SECRETS_FILE` (e.g. `-v $PWD/config.json:/app/config/config.json`).

Defaults keep native v0.3.1 behavior (`DOWNLOADER=auto`, `FLV_REPAIR=auto`);
override `DOWNLOADER=ffmpeg|streamlink`, `FLV_REPAIR=on|off`, etc. per
`docs/INTERFACES.md` §2.3. `/version` is the health endpoint; the image has a
`HEALTHCHECK` that polls it.

`docs/swagger.json` is generated in the `go-build` stage (the `docs/` directory
is gitignored in the repo) and copied to `/app/docs/swagger.json`, which is
where the backend expects it when `WORKDIR` is `/app`.

## Rebuild / upgrade (v0.3.0, FEATURES-V2)

```bash
# 1) rebuild both images with the current source (--pull refreshes base images)
docker compose -f deploy/docker-compose.yml build --pull

# 2) restart in place; named volumes keep records/database/secrets (settings
#    overrides live in /app/database/settings.db and survive the upgrade)
docker compose -f deploy/docker-compose.yml up -d

# 3) smoke checks: version, settings API, recording list, backend health
curl -fsS http://127.0.0.1:8080/version
curl -fsS http://127.0.0.1:8080/settings        # add auth cookie if enabled
curl -fsS http://127.0.0.1:8080/record/list
docker inspect --format '{{.State.Health.Status}}' bilirec

# 4) rollback: rebuild the previous tag and `up -d`, e.g.
#    BILIREC_TAG=v0.2.0 docker compose -f deploy/docker-compose.yml up -d --build
```

Notes:

- The database volume must persist across upgrades — it holds the bbolt
  `settings.db` (global overrides) and `subscribes.db` (room overrides).
- The `config` volume holds `config.json` (0644) and `secrets.json` (0600); back
  it up before upgrades. PUID/PGID changes re-chown config/secrets/database and
  records top-level automatically.
- `docker compose up` without `--build` reuses the previous image tag; use
  `build` (or `up --build`) to pick up source changes.
- With API auth enabled, uncomment `USERNAME`/`PASSWORD` in `deploy/.env`
  **before** the first start; JWT sessions use `JWT_SECRET`.

## Offline E2E / no Docker daemon

`scripts/e2e.sh` is Docker-independent; the image is a packaging deliverable.
If the daemon or base-image network is unavailable, validate the Dockerfile
without building (`docker build --check -f deploy/Dockerfile .` on BuildKit
≥ 0.11) and `docker compose -f deploy/docker-compose.yml config`.

## 一键构建（推荐，输出 tar）

仓库根目录的 `build.sh` / `build-alpine.sh` 把镜像导出为 docker-loadable tar（不留在本机 images）。
默认 **slim**（无 ffmpeg/streamlink），`--full` 构建带 ffmpeg+streamlink 的 full 镜像（tag `-full`）：

```bash
./build.sh                     # dist/local.tar（slim）
./build.sh --full              # dist/local-full.tar
./build.sh --backend / --frontend / --frontend-static / --all [--full]

./build-alpine.sh              # dist/local-alpine.tar（slim, musl）
./build-alpine.sh --full       # dist/local-alpine-full.tar
./build-alpine.sh --all [--full]
```

full 镜像内有 ffmpeg（Alpine 用 apk 版本；Debian 用静态 129MB 版本）与 streamlink；
slim 只有 native 录制，`downloader=ffmpeg|streamlink` 与 `CONVERT_TO_MP4` 不可用。
`deploy/docker-compose.yml` 默认 `WITH_EXTRA=1`（full），设置 `WITH_EXTRA=0` 可构建 slim。

## Alpine slim 前后端合一 compose 示例

`deploy/docker-compose.alpine-slim.yml`：基于 `local-alpine`（非 full，无 ffmpeg/streamlink），
单容器内 nginx 在 80 端口托管前端并把非静态请求反代到 bilirec 8080，因此前端与 API 同源。
默认映射 8080（Web+API）和 8081（直连 API），可用 `ALPINE_WEB_PORT`/`ALPINE_API_PORT` 覆盖；
变量名带 `ALPINE_` 前缀且不读取 `deploy/.env`，不会和主部署（50300/50301）冲突。

```bash
./build-alpine.sh
docker load -i dist/local-alpine.tar
docker compose -f deploy/docker-compose.alpine-slim.yml up -d
# 或者让 compose 直接构建：docker compose -f deploy/docker-compose.alpine-slim.yml up -d --build
```

### 转码菜单能力探测

前端在登录后调用 `GET /convert/providers`：

```json
{"providers": [], "enabled": false}
```

`enabled=false`（例如 slim 镜像没有 ffmpeg/cloudconvert）时，“转换任务”导航项会自动隐藏；
full 镜像返回 `{"providers":["ffmpeg"],"enabled":true}`。旧后端没有该接口时前端保持显示（兼容）。

## 镜像环境变量默认值

镜像内的数据目录与上游 bilirec 文档一致：相对 `WORKDIR /app` 解析，即

```text
OUTPUT_DIR=records     -> /app/records
DATABASE_DIR=database  -> /app/database
SECRET_DIR=secrets     -> /app/secrets
HOST=（空，监听所有网卡）
BILIBILI_LOGIN_MODE=controller
FRONTEND_URLS=https://app.bilirec.org
```

compose 示例把卷挂到 `/app/records`、`/app/database`、`/app/secrets`；如需沿用旧的
`/data` 布局，显式覆盖这三个变量并把卷挂到对应路径即可。
