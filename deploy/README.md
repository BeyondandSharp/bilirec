# deploy/ — container image

Multi-stage image for `bilirec` + the Rust `flvrepair` sidecar, based on
`docs/INTERFACES.md` §2.3 (env contract), `docs/FEATURES-V2.md` §1.1 (new env
defaults) and §5 (build).

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

Build args (mirrors): `GO_IMAGE`, `RUST_IMAGE`, `RUNTIME_IMAGE`,
`GOPROXY_URL`, `SWAG_VERSION`, `STREAMLINK_VERSION`.

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

## Run

```bash
docker run --rm -p 8080:8080 \
  -v bilirec-records:/data/records \
  -v bilirec-database:/data/database \
  -v bilirec-secrets:/data/secrets \
  bilirec-rust:dev
```

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
#    overrides live in /data/database/settings.db and survive the upgrade)
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
