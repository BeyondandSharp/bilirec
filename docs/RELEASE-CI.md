# 自动发布（Forgejo Actions）

推送 **semver tag**（`v0.5.0` / `0.5.0` / `0.5.0-rc.1`）会触发**两个** workflow：

| 入口 | 触发 | 做什么 |
|---|---|---|
| [.forgejo/workflows/docker-publish.yml](../.forgejo/workflows/docker-publish.yml) | `push` tag | 构建各变体镜像并推送到 Docker Hub 与 ghcr.io：先推 staging，全部变体成功后才提升为最终 tag |
| [.forgejo/workflows/release.yml](../.forgejo/workflows/release.yml) | `push` tag | 在 **Forgejo 与 GitHub** 创建/复用同一 tag 的 Release（自动 changelog） |

两个手动入口：[.forgejo/workflows/docker-publish-manual.yml](../.forgejo/workflows/docker-publish-manual.yml)
（`version` / `variants` / `dry_run`）与
[.forgejo/workflows/release-manual.yml](../.forgejo/workflows/release-manual.yml)（`version` / `dry_run`）。

四个入口都是**复制一次、之后不再修改的冻结入口**：编排、程序和重试策略都在模板仓库
`BeyondandSharp/action-template` 的 reusable workflow 里（入口里的 `@main`）。改发布流程只需改模板仓库，
本仓库的 `.forgejo` 不动；本仓库自己维护的只有 `.forgejo/release/` 里的 hook（见下）。

两个 run **相互独立**：镜像构建失败不会阻止 Release 创建，Release 失败也不会回滚镜像。需要“镜像成功才发
Release”时，先停掉一边（或手动 dispatch），另一边成功后再补发。

## 一次性配置

1. Forgejo 仓库 **Settings → Actions**：启用 Actions。
2. Runner：
   - 镜像作业使用 `docker:dind`，需要 runner 打开 `container.privileged: true`（不需要 volume 挂载）；
   - Release 作业默认 `node:22-bookworm`，可用仓库变量 `RELEASE_IMAGE` / `RELEASE_RUNNER_LABEL` 覆盖。
3. 仓库变量 / 密钥：

### 镜像（docker-publish）

| 名称 | 类型 | 说明 |
|---|---|---|
| `DOCKERHUB_USERNAME` | Variable | Docker Hub 用户名，同时作为镜像命名空间 |
| `DOCKERHUB_TOKEN` | Secret | Docker Hub access token；存在即启用 Docker Hub |
| `GHCR_TOKEN` | Secret | GitHub PAT（`write:packages`）；存在即启用 ghcr.io |
| `GHCR_IMAGE` | Variable（可选） | 例如 `ghcr.io/BeyondandSharp/bilirec`；GHCR 命名空间与 Forgejo owner 不同名时必填 |
| `DOCKER_VARIANTS` | Variable（可选） | 变体表（见“变体与 tag”）；不设置时用 Action 内置默认表 |
| `DOCKER_PLATFORMS` | Variable（可选） | 覆盖平台；默认按变体表（当前为 `linux/amd64`） |
| `DOCKER_FORCE_BUILD`、`DOCKER_RETRY_*`、`APT_PROXY`、`NPM_PROXY` 等 | Variable（可选） | 语义见 action-template 的 `docker-build-push/README.md` |

### Release（release）

| 名称 | 类型 | 说明 |
|---|---|---|
| `RELEASE_GITHUB_TOKEN` | Secret | GitHub PAT，`Contents: read/write`；GitHub 开关打开时必需。**不是** Forgejo 的 `GITHUB_TOKEN` |
| `RELEASE_FORGEJO_TOKEN` | Secret（可选） | Forgejo 仓库写权限 PAT；未设置时回退 `github.token` |
| `RELEASE_GITHUB_REPO` | Variable（可选） | GitHub 仓库 `owner/repo`；默认 = 本仓库的 `owner/repo`（`BeyondandSharp/bilirec`） |
| `RELEASE_TO_FORGEJO` / `RELEASE_ASSETS_TO_FORGEJO` | Variable（可选，默认 true） | Forgejo 侧创建 Release / 上传资产 |
| `RELEASE_TO_GITHUB` / `RELEASE_ASSETS_TO_GITHUB` | Variable（可选，默认 true） | GitHub 侧同上；只发 Forgejo 时把两个都设 `false`（同时不再需要 GitHub token，也跳过同步等待） |
| `RELEASE_BRANCHES` | Variable（可选） | 发布分支允许列表，逗号/空白分隔；不设置 = `main,master` |
| `RELEASE_PRECHECK_OVERRIDE` | Variable（可选） | `pass` / `fail` / `skip`，临时覆盖本仓库的 preflight；正常发布不要长期打开 |
| `ACTION_TOKEN` / `ACTION_REF` | Secret / Variable（可选） | 模板仓库私有时的只读 checkout token；钉版本时与入口的 `@ref` 保持一致 |

GitHub 侧通常由 **Forgejo push mirror** 异步同步 tag。Release Action 会轮询 GitHub，直到同一 tag 指向同一
commit 才继续；没有 mirror 或同步不了时 `Wait GitHub sync` 会超时——修 mirror，或把两个 `*_TO_GITHUB`
设为 `false` 只发 Forgejo。

## 变体与 tag（镜像）

变体表是**仓库变量** `DOCKER_VARIANTS`，不是仓库里的文件；当前配置的 8 个变体：

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
- 其他变体得到带后缀的版本 tag，如 `0.5.0-trixie`、`0.5.0-alpine-full-headless`；
- 浮动 tag 只在 `main`/`master` 上移动；允许列表里的其它分支改用分支名浮动 tag（如 `0.5.0-dev`、`dev`）。

镜像名：

- Docker Hub：`docker.io/<DOCKERHUB_USERNAME>/bilirec:<tag>`
- GHCR：`ghcr.io/<namespace>/bilirec:<tag>`（namespace 默认取仓库 owner；不同名时用 `GHCR_IMAGE`）

> 当前平台为 `linux/amd64`。trixie 变体的 full 镜像使用 amd64 静态 ffmpeg；需要 arm64 时，先为 trixie full
> 提供 arm64 的 ffmpeg 方案，再把变体表的平台列改成 `linux/amd64,linux/arm64`（或在仓库变量
> `DOCKER_PLATFORMS` 设置）。变体表语法、tag 规则与构建/发布分离的细节见 action-template 的
> `docker-build-push/README.md`。

## Release 内容

- **Release 页**：标题是 tag，body 由 Action 用 `git log --oneline` 自动生成（上一个 tag 到本 tag，最多 50 条
  提交；没有上一个 tag 时取最近 50 条）；重复运行同一个 tag 会**复用**已有 Release，不会重复创建。
- **资产**：本仓库**不附加自定义资产**——发布物是容器镜像，源码归档由 Forgejo / GitHub 自动提供。需要附加
  二进制或打包产物时，写 `.forgejo/release/prepare.sh`，把每个待上传文件的**绝对路径逐行**写进
  `$RELEASE_ASSETS_FILE`（契约见 `release/README.md`）。
- **预发布**：版本里含 `-`（如 `0.5.0-rc.1`）时标记为 prerelease。
- **分支门禁**：tag 的提交必须能从 `RELEASE_BRANCHES`（默认 `main,master`）里的某个分支到达，否则整个 job
  **绿色跳过**（不 checkout tag、不跑 hook、不建 Release）。tag 事件不携带“从哪个分支打的”，所以打 tag 前
  确认提交已在允许分支上。
- **dry run**：`release-manual.yml` 的 `dry_run=true` 只跑前置检查与打包，不创建 Release、不上传资产。

## 本仓库的 hook：`.forgejo/release/preflight.sh`

Action 不知道项目如何声明版本，所以前置检查由本仓库提供（POSIX `sh`，cwd 为 workspace）；它运行在 **tag 的
提交**上。当前检查：

1. `RELEASE_TAG` 与 `RELEASE_VERSION` 只差一个可选 `v` 前缀（版本号的唯一来源是 tag）；
2. `HEAD` 就是 tag 指向的提交（`RELEASE_SHA`）；
3. tag 树里仍有镜像构建所需入口（`go.mod`、`Makefile`、`cmd/backend`、`rust/`、`web/`、`deploy/*.Dockerfile`、
   `.forgejo/workflows/docker-publish*.yml` 等）；
4. 四个发布用 Dockerfile 都声明了 `ARG VERSION`（镜像里的版本号/更新检查依赖它）。

hook 能看到的变量（不含任何 token）：`RELEASE_TAG`、`RELEASE_VERSION`、`RELEASE_PRERELEASE`、`RELEASE_SHA`、
`GITHUB_REPOSITORY`、`GITHUB_WORKSPACE`、`FORGEJO_DIR`；退出码非 0 即拒绝发布。

## 发布步骤

```bash
git tag -a v0.5.0 -m "v0.5.0"
git push origin v0.5.0
```

tag 必须是 semver（`v1.2.3` / `1.2.3` / `1.2.3-rc.1`），否则两个 workflow 都不会触发。workflow 与 hook 都从
**tag 的树**读取，所以 `.forgejo` 的改动必须先提交并包含在 tag 里。

## 失败重试

- 镜像 publish 失败：registry 侧复制失败，重跑 publish 作业即可，不必重新构建；build 部分变体失败时修好后重跑
  整个 run，已 staged 的变体会被跳过（`DOCKER_FORCE_BUILD=1` 可强制重建）；所有变体成功前不会移动最终 tag。
- Release 失败：幂等重跑即可——Release 复用、已有资产跳过，只补缺失的部分。
- 两个 run 互不影响：一个失败不会回滚另一个。

## 本地检查（不推送）

```bash
sh -n .forgejo/release/preflight.sh

# 用当前提交模拟一次前置检查（不会创建任何 Release）
RELEASE_TAG=v0.5.0 RELEASE_VERSION=0.5.0 \
RELEASE_SHA="$(git rev-parse HEAD)" GITHUB_WORKSPACE="$PWD" \
  sh .forgejo/release/preflight.sh
```

完整的发布演练用 `workflow_dispatch` + `dry_run`：镜像入口只构建不登录/不推送，Release 入口只跑检查与打包。
