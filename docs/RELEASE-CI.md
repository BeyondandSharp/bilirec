# 自动发布镜像（Forgejo Actions）

向仓库推送 **semver tag**（例如 `v0.5.0` / `0.5.0`）会触发
[.forgejo/workflows/docker-publish.yml](../.forgejo/workflows/docker-publish.yml)：

1. **plan**：校验 `.forgejo/variants.txt`，得到 8 个变体的有序列表；
2. **build**：逐个变体执行 `docker buildx build --push`，推到临时 staging tag（不移动最终 tag）；
3. **publish**：所有变体/架构都成功后，用 `docker buildx imagetools create` 把 staging 提升为最终 tag
   （registry 侧复制，不重新构建）。

发布到 **Docker Hub** 与 **GitHub Container Registry (ghcr.io)**。

## 变体与 tag

`.forgejo/variants.txt` 定义 8 个变体：

| 变体 | 内容 | Dockerfile | build arg |
|---|---|---|---|
| `alpine`（默认，拥有 `latest`） | 前后端合一，slim（无 ffmpeg/streamlink） | `deploy/alpine-combined.Dockerfile` | `WITH_EXTRA=0` |
| `trixie` | 前后端合一，slim | `deploy/combined.Dockerfile` | `WITH_EXTRA=0` |
| `alpine-full` | 前后端合一，full（ffmpeg+streamlink） | `deploy/alpine-combined.Dockerfile` | `WITH_EXTRA=1` |
| `trixie-full` | 前后端合一，full | `deploy/combined.Dockerfile` | `WITH_EXTRA=1` |
| `alpine-headless` | 仅后端，slim | `deploy/alpine.Dockerfile` | `WITH_EXTRA=0` |
| `trixie-headless` | 仅后端，slim | `deploy/Dockerfile` | `WITH_EXTRA=0` |
| `alpine-full-headless` | 仅后端，full | `deploy/alpine.Dockerfile` | `WITH_EXTRA=1` |
| `trixie-full-headless` | 仅后端，full | `deploy/Dockerfile` | `WITH_EXTRA=1` |

- 每个变体都会得到 **浮动 tag**：`alpine`、`trixie`、`alpine-full`、`trixie-full`、
  `alpine-headless`、`trixie-headless`、`alpine-full-headless`、`trixie-full-headless`；
- 默认变体 `alpine` 额外拥有 `latest` 与无后缀的版本 tag（`0.5.0`、`0.5`、`v0.5.0`、`sha-…`）；
- 其他变体得到带后缀的版本 tag，如 `0.5.0-trixie`、`0.5.0-alpine-full-headless`。

镜像名：

- Docker Hub：`docker.io/<DOCKERHUB_USERNAME>/bilirec:<tag>`
- GHCR：`ghcr.io/<namespace>/bilirec:<tag>`（namespace 默认取仓库 owner；不同名时用 `GHCR_IMAGE`）

> 当前平台为 `linux/amd64`。trixie 变体的 full 镜像使用 amd64 静态 ffmpeg；需要 arm64 时，
> 先为 trixie full 提供 arm64 的 ffmpeg 方案，再把 variants.txt 第七列改成
> `linux/amd64,linux/arm64`（或在仓库变量 `DOCKER_PLATFORMS` 设置）。

## 一次性配置

1. Forgejo 仓库 **Settings → Actions**：启用 Actions。
2. Runner：`container.privileged: true`（build 作业使用 `docker:dind` 并在容器内启动 dockerd；
   workflow 里的 `options: --privileged` 会被 runner 忽略）。
3. 仓库 Variables / Secrets：

| 名称 | 类型 | 说明 |
|---|---|---|
| `DOCKERHUB_USERNAME` | Variable | Docker Hub 用户名，同时作为镜像命名空间 |
| `DOCKERHUB_TOKEN` | Secret（或 Variable） | Docker Hub access token |
| `GHCR_TOKEN` | Secret（或 Variable） | GitHub PAT，需 `write:packages` |
| `GHCR_IMAGE` | Variable（可选） | 例如 `ghcr.io/BeyondandSharp/bilirec`；GHCR 命名空间与 Forgejo owner 不同名时必填 |
| `DOCKER_PLATFORMS` | Variable（可选） | 覆盖默认平台，如 `linux/amd64,linux/arm64` |
| `DOCKER_FORCE_BUILD` | Variable（可选） | `1` 时即使 staging 已存在也重建 |
| `DOCKER_META_IMAGES` | Variable（可选） | 完全覆盖镜像基名，如 `ghcr.io/acme/bilirec,custom/bilirec` |

4. 确保 Docker Hub 上已创建（或允许自动创建）`<DOCKERHUB_USERNAME>/bilirec` 仓库；
   GHCR 侧 PAT 需有创建/写入 package 的权限。
5. 推送 tag：

```bash
git tag v0.5.0
git push origin v0.5.0
```

tag 必须是 semver（`v1.2.3` / `1.2.3` / `1.2.3-rc.1`），否则 workflow 不会触发。

## 本地预检（不推送）

```bash
export GITHUB_WORKSPACE=$PWD
export DOCKER_VARIANTS_FILE=$PWD/.forgejo/variants.txt
node .forgejo/scripts/run.mjs verify-action
node .forgejo/scripts/run.mjs plan-matrix
```

`plan-matrix` 会打印变体顺序和矩阵；更完整的发布演练可看
[.forgejo/README.md](../.forgejo/README.md) 的 `workflow_dispatch` + `dry_run`。

## 失败重试

- publish 失败：registry 侧复制失败，重跑 publish 作业即可，无需重建；
- build 部分变体失败：修好后重跑整个 run，已存在 staging tag 的变体会被跳过；
  `DOCKER_FORCE_BUILD=1` 可强制重建；
- 所有变体成功之前不会创建任何最终 tag，所以 `latest` 不会被半成品移动。
