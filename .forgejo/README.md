# docker-build-push

Forgejo Action：**推送 tag 即构建并推送容器镜像**，发布到 **ghcr.io** 与 **Docker Hub**（其余 registry 不在范围内）。

实现参照 Docker 官方三个 Action —— `docker/build-push-action` v7.4.0、`docker/metadata-action` v6.2.0、`docker/login-action` v4.6.0（源码以 submodule 形式放在仓库的 `ref/docker-build-push/`，仅供对照，**不随本目录复制**）。官方用 action inputs，这里换成**变体表/仓库变量 → 环境变量 → 脚本**，因此复制到目标仓库后**只需要配变量和变体表**，不需要改 YAML：workflow 里没有任何项目名。

```
push tag v1.2.3
      │
      ├─ plan 作业：校验变体表 → 顺序变体列表（同时给出矩阵，供可选并行模式）   plan-matrix
      │
      ├─ build 作业（job 容器 = docker:dind），按表内顺序逐个变体：
      │     ├─ 容器内装运行环境（node/git/curl）                    Bootstrap（内联 sh）
      │     ├─ 解析版本与变体                                       resolve
      │     ├─ 容器内准备构建环境（dockerd / buildx / 额外包 / binfmt） prepare
      │     ├─ 预检：凭据 / 变体 Dockerfile / tag 冲突 / daemon      preflight
      │     ├─ docker login ghcr.io、docker.io（token 走 stdin）     login
      │     ├─ 推导 tags 与 OCI labels（每个变体一份）               meta
      │     ├─ 每个变体各自 buildx build --platform … --push 到 staging  build-push
      │     └─ 汇总（staging 标签 + digest）                         summary
      │
      └─ publish 作业（needs: build；不需要 daemon）：
            ├─ 重新解析版本/标签，校验所有 staging 都在                 resolve / preflight / meta
            ├─ docker login（同样两个 registry）                        login
            ├─ buildx imagetools create：staging → 最终标签（含 latest） publish
            └─ 汇总（最终标签 + digest）                                summary
```

```
alpine       → 1.2.3-alpine / 1.2-alpine / sha-…-alpine / alpine          （linux/amd64 + linux/arm64）
trixie-slim  → 1.2.3 / 1.2 / latest / sha-…            ← 默认变体，独占无后缀标签
               + 1.2.3-trixie-slim / 1.2-trixie-slim / sha-…-trixie-slim / trixie-slim
               （linux/amd64 + linux/arm64）
```

## 安装

把整个 `docker-build-push/` 目录复制到目标仓库并改名为 `.forgejo`：

```bash
cp -r docker-build-push /path/to/target-repo/.forgejo
```

```
.forgejo/
├── workflows/
│   └── docker-publish.yml    # 只有编排；唯一的 shell 是 bootstrap 与定位入口
├── variants.txt.example      # 变体表示例：复制成 variants.txt 后按项目修改
└── scripts/                  # 全部逻辑，普通 .mjs 文件
    ├── run.mjs               # 分发器：每个步骤一行调用它
    ├── endpoints.mjs         # 端点判别（镜像 vs 代理）+ docker/buildx 能力探测
    ├── retry.mjs             # 网络失败的有界重试（registry / Docker Hub / GitHub）
    ├── locate-action.mjs     # 找 Action 目录，并导出变体表路径
    ├── config.mjs            # 变体表/仓库变量 → 归一化配置
    ├── matrix.mjs            # 变体表 → 顺序列表 + 矩阵（plan 作业）
    ├── prepare.mjs           # 容器内准备：dockerd / buildx / 额外包 / QEMU binfmt
    ├── resolve.mjs           # tag → 版本、变体、状态文件
    ├── preflight.mjs         # 预检（凭据、Dockerfile、tag 冲突、daemon）
    ├── meta.mjs              # tags 与 OCI labels 推导（对应 metadata-action）
    ├── login.mjs             # docker login（对应 login-action）
    ├── build-push.mjs        # 每变体一次构建，推到 staging（对应 build-push-action）
    ├── publish.mjs           # staging → 最终标签（buildx imagetools，对应 metadata 之后的发布）
    └── summary.mjs           # digest 汇总
```

workflow 里每一步都长这样（`配置里没有内嵌脚本`）：

```yaml
      - name: Build and stage
        run: |
          dir="${{ steps.locate.outputs.forgejo_dir }}"
          node "$dir/scripts/run.mjs" build-push
```

唯一的例外是 `Bootstrap job container`（要先有 node 才能跑脚本）与定位步骤本身（要先找到 `run.mjs` 才能调用它）。

可用子命令：`locate-action`、`verify-action`、`plan-matrix`、`prepare`、`resolve`、`preflight`、`login`、`meta`、`build-push`、`publish`、`summary`；都能在本地直接跑，例如
`GITHUB_WORKSPACE=$PWD node scripts/run.mjs locate-action`。

复制不全会被 `verify-action` 拦下并逐个列出缺哪个文件。

## 目标仓库需要准备什么

1. `设置 → Actions`：勾选 **Enable Repository Actions**。
2. **变体表**：`.forgejo/variants.txt`（每行一个变体，见下），或者改用仓库变量 `DOCKER_VARIANTS`。
3. **每个变体的 Dockerfile**：默认约定 `Dockerfile.alpine` 与 `Dockerfile.trixie-slim`（可在变体表里改）。
4. 至少一套 registry 凭据（见下表）。
5. runner 打开 `container.privileged: true`（见「runner 前置条件」）。

### 变体 Dockerfile 示例（alpine / trixie，各自独立编译）

两个变体**各编译各的**：不共享编译产物、不依赖静态链接，因此各自链接各自 libc（musl / glibc）。下面是 Go + 前端项目的写法（把 `web/` 换成你自己的构建步骤）：

```dockerfile
# Dockerfile.alpine
FROM node:24-alpine AS frontend
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /build/web
RUN npm install -g pnpm --registry=${NPM_REGISTRY}
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --ignore-scripts --registry=${NPM_REGISTRY}
COPY web/ ./
RUN pnpm run build

FROM golang:1.27-alpine AS backend
ARG VERSION=dev
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache build-base              # go-sqlite3 需要 CGO 工具链
WORKDIR /build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=frontend /build/web/build ./web/build
# 注意：不要 -extldflags '-static' —— 动态链接到 musl
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags "-s -w -X 'message-pusher/common.Version=${VERSION}'" -o message-pusher

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata
COPY --from=backend /build/message-pusher /
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/message-pusher"]
```

```dockerfile
# Dockerfile.trixie-slim（默认变体，承载 latest）
FROM node:24-trixie AS frontend
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /build/web
RUN npm install -g pnpm --registry=${NPM_REGISTRY}
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --ignore-scripts --registry=${NPM_REGISTRY}
COPY web/ ./
RUN pnpm run build

FROM golang:1.27-trixie AS backend
ARG VERSION=dev
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=frontend /build/web/build ./web/build
# 同样不静态链接 —— 动态链接到 glibc
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags "-s -w -X 'message-pusher/common.Version=${VERSION}'" -o message-pusher

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*
COPY --from=backend /build/message-pusher /
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/message-pusher"]
```

> 用 `--target` 的单文件写法也支持：变体表的第三段就是 target。

## 变体表（`.forgejo/variants.txt` 或 `DOCKER_VARIANTS`）

**每行一个变体**：

```
<名字>|<Dockerfile>|<target>|<后缀>|<是否默认>|<KEY=VALUE;KEY=VALUE>|<平台,平台>
```

只有名字必需；其余默认 `Dockerfile.<名字>`、无 target、`-<名字>`、非默认、无额外构建参数、用全局 `DOCKER_PLATFORMS`。后缀写 `none` 表示**不加后缀**。默认表等价于：

```
alpine|Dockerfile.alpine||-alpine|false||linux/amd64,linux/arm64
trixie-slim|Dockerfile.trixie-slim||-trixie-slim|true||linux/amd64,linux/arm64
```

**变体表的来源与优先级**（`plan-matrix`、`prepare`、`resolve` 共用同一套，因此不会打架）：

1. 仓库变量 `DOCKER_VARIANTS`（最高）；
2. `.forgejo/variants.txt`（`locate-action` 会把它的路径导出给后续步骤；不存在就用下一条）；
3. 内置默认表（alpine + trixie-slim，双架构）。

> 内置的 workflow 不在 YAML 里写字面变体：`plan` 作业校验变体表并输出**有序变体列表**（`variants=alpine,trixie-slim`），发布作业按这个顺序**逐个变体**构建。所以**改变体只改变体表，不碰 workflow**。

**第五列（是否默认）是唯一决定 `latest` 归属的地方**：整个表里必须**恰好一个** `true`；一个都没有（且有多个变体）会直接报错，只有一个变体时它天然是默认。写 `false` 的行是「明确不要 latest」，不会被单变体回退悄悄变成默认，因此两个 job 绝不会同时推 `latest`。

**第七列是平台**（逗号分隔，如 `linux/amd64,linux/arm64`）：每个变体一条 `docker buildx build --platform …`，产出多架构 manifest list；留空则用仓库变量 `DOCKER_PLATFORMS`。跨架构构建需要宿主机内核的 QEMU binfmt，`prepare` 会在需要时注册（见下）。

**第六列是该变体自己的 build args**，这是「让仓库指定工具链镜像」的通用做法——Dockerfile 里声明 `ARG`，值随变体走：

```yaml
DOCKER_VARIANTS: |
  alpine|Dockerfile.alpine||-alpine|false|NODE_IMAGE=node:lts-alpine|linux/amd64,linux/arm64
  trixie-slim|Dockerfile.trixie-slim||-trixie-slim|true|NODE_IMAGE=node:lts-trixie-slim|linux/amd64,linux/arm64
```

对应 Dockerfile（`ARG` 必须在**第一个 `FROM` 之前**声明才能用于 `FROM`）：

```dockerfile
ARG NODE_IMAGE=node:24-alpine
FROM ${NODE_IMAGE} AS frontend
```

优先级：**`DOCKER_BUILD_ARGS` > 变体第六列 > 自动注入**（同一 key 冲突时）。

每个变体**恰好一次** `docker buildx build` 调用，各自 `--file`/`--target`，**不会**用「编译一次、复制进两个 runtime stage」或「打一个 tag 再 retag」的做法；同一镜像上出现重复 tag 会被 preflight 直接拒绝。

## 触发方式

| 触发 | 说明 |
| --- | --- |
| 推送 tag `v1.2.3` / `1.2.3` | 主路径，版本 = tag（允许 `v` 前缀，允许 `1.2.3-rc.1` 预发布） |
| 手工 `workflow_dispatch` | 在**分支**上运行时必须填 `version`（它就是本次发布的版本）；在 **tag ref** 上运行时 `version` 必须与之一致；`variants` 只构建指定变体（在 plan 阶段过滤）；`dry_run` 只验证构建（不登录、不推送） |

预发布版本（含 `-`）**不会**产出 `latest`，但 `alpine` / `trixie-slim` 浮动标签仍会更新。
非 semver 的 tag 一律被拒绝（没有开关）：版本号必须形如 `v1.2.3` 或 `1.2.3-rc.1`。

### runner 不展开 `${{ inputs.* }}` 时

`workflow_dispatch` 的输入由 workflow 映射进环境变量（`INPUT_VERSION` / `INPUT_VARIANTS` / `INPUT_DRY_RUN`）。Forgejo 的表达式求值器遇到**当前事件里不存在的上下文**时会**原样保留字面量**：tag 触发时没有 `inputs`，于是 `INPUT_VERSION` 的值就是字符串 `${{ inputs.version }}`，而不是空串。

脚本对此有兜底，不需要你做什么：

- **长得像未展开表达式的 `INPUT_*` 一律按「未填写」处理**，并在日志里打一条警告说明是哪个输入、以及最终用了什么值；
- 真正的来源改为 `$GITHUB_EVENT_PATH` 指向的**事件载荷**（`{"inputs": {...}}`），它不依赖表达式引擎；
- 环境变量里的**真实值仍然优先**（在会正常求值的 runner 上行为不变）。

`plan-matrix` 用的是同一套兜底，所以 tag 推送时不会被 `variants` 输入卡住。

## 配置项

### Secrets（token；放 Variables 里也能用，workflow 两边都读）

| 名称 | 必需 | 说明 |
| --- | --- | --- |
| `GHCR_TOKEN` | 二选一 | 带 `write:packages` 的 GitHub PAT（classic）或 fine-grained token（Packages: write）。**存在即启用 ghcr.io** |
| `DOCKERHUB_TOKEN` | 二选一 | Docker Hub access token（不是账号密码）。**存在即启用 Docker Hub** |
| `GHCR_IMAGE` | 否 | GHCR 命名空间通常与 Forgejo 的 owner **不同名**：`GHCR_IMAGE=ghcr.io/<账号>/<仓库>`。登录用户就是命名空间；不设时按仓库 owner 推导 |
| `DOCKERHUB_USERNAME` | Docker Hub 必需 | 同时作为登录用户与命名空间 |

> token 只以环境变量进入脚本：`docker login --password-stdin`，**不进命令行**；`preflight` 拒绝在没有任何凭据时发布（dry run 除外）。

### Variables（构建与标签策略，全部可选）

> **留空 = 未配置**：workflow 里是 `X: ${{ vars.X }}`，变量没配时导出的是**空字符串**，脚本会把空值/纯空白一律当作「没设置」并使用默认值（不会报「配置为空」）。

| 名称 | 默认 | 说明 |
| --- | --- | --- |
| `DOCKER_VARIANTS` | 空（用 `variants.txt` / 内置表） | 变体表，语法同上；设置后**优先于** `.forgejo/variants.txt` |
| `DOCKER_META_IMAGES` | 空（按 registry 推导） | **唯一**的镜像名入口：覆盖基名（换行/逗号分隔），例如 `ghcr.io/acme/app`、`myorg/app` |
| `DOCKER_META_TAGS` | `type=ref,event=tag` / `type=semver,pattern={{version}}` / `type=semver,pattern={{major}}.{{minor}}` / `type=sha` | 每行一条规则；见下 |
| `DOCKER_META_FLAVOR` | `latest=auto` | `latest=auto\|true\|false`、`prefix=`、`suffix=`（可带 `,onlatest=true`） |
| `DOCKER_META_LABELS` | 空 | 覆盖/追加 OCI label，每行 `KEY=VALUE` |
| `DOCKER_BUILD_ARGS` | 空 | 每行 `KEY=VALUE`（值里可以有逗号）。`VERSION` 已自动注入，这里写了就覆盖它 |
| `DOCKER_PLATFORMS` | 空（= 各变体自己的第七列；都没有则 = 主机平台） | 例如 `linux/amd64,linux/arm64`；>1 平台时自动建 `docker-container` builder |
| `DOCKER_JOB_PACKAGES` | 空 | 工作台镜像缺少的额外包（空格/逗号分隔），由 `prepare` 用容器里的包管理器安装 |
| `DOCKER_FORCE_BUILD` | 空 | 设为 `1/true` 时即使 staging 标签已存在也重建（默认复用已 staged 的变体） |
| `DOCKER_SKIP_BINFMT` | 空 | 设为 `1/true` 时 `prepare` 不再尝试注册 QEMU binfmt |
| `DOCKER_BINFMT_IMAGE` | `tonistiigi/binfmt` | 注册 binfmt 用的镜像（内网镜像可覆盖） |
| `DOCKER_BUILDKIT_IMAGE` | 空（buildx 默认） | 覆盖 BuildKit 容器镜像（`--driver-opt image=`），内网场景有用 |
| `DOCKER_HOST` | 空 | daemon 地址（`tcp://…` 或非默认 unix socket）；不设即容器内的 `/var/run/docker.sock` |
| `DOCKER_RETRY_ATTEMPTS` | `3` | registry / Docker Hub / GitHub 调用的总尝试次数（含第一次）；`1` = 不重试 |
| `DOCKER_RETRY_DELAY` | `2` | 退避基数（秒），第 n 次重试前等待 `2^n × 基数`；限流（429）从 10s 起 |
| `DOCKER_RETRY_DISABLED` | 空 | 设为 `1/true` 时关闭所有网络重试（排障用） |
| `DOCKER_BUILD_RETRY_ATTEMPTS` | `2` | 单次 `docker buildx build` 的总尝试次数（含第一次）；**只重试网络错误**，编译错误不重试 |
| `APT_PROXY` / `NPM_PROXY` / `PIP_PROXY` / `GOPROXY` | 空 | 内网端点（会探测一次），见下面「代理、镜像与 registry」 |
| `APK_REPO` / `YUM_REPO` | 空 | **显式仓库地址**（不探测）：`APK_REPO` 是 `APK_PROXY` 构建参数的来源，`YUM_REPO` 原样作为 `--build-arg YUM_REPO` 传给 Dockerfile |

### 代理、镜像与 registry（内网怎么接）

runner 没有直连外网时，先分清三件不同的事：**代理**（forward proxy）、**镜像/仓库**（repository）、**registry**。端点只有三类用途：apt 镜像给 Dockerfile 里的 `apt-get` 用，npm registry 给前端 `pnpm install` 用，pip index 给 Python 依赖安装用——三者都以构建参数的形式传进构建。

**最省事的用法：只填下面这几个地址。**
`APT_PROXY` / `NPM_PROXY` / `PIP_PROXY` 的值可以是**真代理**，也可以是**内网镜像/registry/index**：`Resolve version and variants` 步骤会**探测一次**（取该地址自己的仓库索引 / `/-/ping` / `/<项目>/`），再决定怎么用。判定结果会打进日志（`构建参数：…`）。

| 变量 | 你填什么 | 判为**镜像 / registry / index** | 判为**代理** |
| --- | --- | --- | --- |
| `APT_PROXY` | Debian/Ubuntu 镜像根，如 `https://apt.internal` | 注入 `--build-arg APT_PROXY=<根>/debian`（Dockerfile 里 `ARG APT_PROXY` 后改写 sources）；`APK_PROXY` 同时复用这个根；作业容器自身的 apt 安装也会改写 sources | 不注入任何东西：真代理走 `HTTP(S)_PROXY`，由本 Action 显式作为 `--build-arg` 转发 |
| `NPM_PROXY` | 内网 npm registry，如 `https://npm.internal` | 构建时注入 `--build-arg NPM_REGISTRY=<url>` | 不注入：真代理走 `HTTP(S)_PROXY`，由本 Action 显式作为 `--build-arg` 转发 |
| `PIP_PROXY` | 内网 pip 源，如 `https://pip.internal` | 探测 `/simple/<项目>/` 与 `/index/<项目>/`，把**真正应答的那个**注入 `--build-arg PIP_INDEX_URL=<url>`（裸域名会补上 `/simple` 或 `/index`；已带路径的原样保留） | 不注入：真代理走 `HTTP(S)_PROXY`，由本 Action 显式作为 `--build-arg` 转发 |
| `GOPROXY` | Go module proxy（Athens 等） | 构建时注入 `--build-arg GOPROXY=<url>` | — |

> pip 源的两种常见布局都支持：PyPI 兼容源在 `/simple/<项目>/`（PEP 503），proxpi 之类的缓存服务在 `/index/<项目>/`。填裸域名（`https://pip.internal`）即可，探测出哪个路径应答就把哪个作为 `PIP_INDEX_URL`；也可以直接填完整路径（`https://pip.internal/index`），此时不再补路径。

**apk / yum 的仓库用这两个（显式指定，不探测）**：

| 变量 | 你填什么 | 结果 |
| --- | --- | --- |
| `APK_REPO` | apk 镜像根或完整仓库地址，如 `https://apk.internal` | 注入 `--build-arg APK_PROXY=<值>`；**优先于**上面判出来的 apt 镜像根；作业容器自身（bootstrap / `prepare`）的 apk 安装也会改写 `/etc/apk/repositories` |
| `YUM_REPO` | yum/dnf 镜像根或完整 baseurl | 注入 `--build-arg YUM_REPO=<值>`；作业容器自身（bootstrap / `prepare`）的 dnf/yum 安装也会改写 `/etc/yum.repos.d` 里的 `baseurl` |

**真正的 HTTP 转发代理**（只当代理，不做判别）：`ALL_PROXY`、`HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`。构建时由本 Action **显式转发**——作为 `--build-arg` 传给每个变体的 `docker buildx build`（BuildKit 会自动补齐小写形式，`RUN` 步骤可见），需要 `docker-container` builder（多架构）时同时作为 `--driver-opt env.<变量>` 传给 builder 容器，让 buildkitd 自己也能拉基础镜像；`DOCKER_BUILD_ARGS` 里写了同名项时以显式项为准。注意 docker CLI 只会从自己的 `~/.docker/config.json`（`proxies`）自动补代理 build arg，**不会**读自己的环境变量，所以这一步必须显式做。

**作业容器自身的包安装**（bootstrap 装的 node/git/curl，以及 `prepare` 装的 `DOCKER_JOB_PACKAGES`、buildx）走的是同一套仓库变量，约定与 Dockerfile 一致——`APK_REPO` / `APT_PROXY` / `YUM_REPO` 是**仓库根**，改写容器的 `/etc/apk/repositories`、apt sources（含 deb822 的 `URIs:`）、yum `baseurl`；真代理走 `HTTP(S)_PROXY`（同时派生一份小写的 `http_proxy`/`https_proxy`/`no_proxy`/`all_proxy`，因为 apk / apt / dnf 读的是小写名）。改写是 grep 守卫、幂等的，bootstrap（node 还不存在）和 `prepare`（node 已存在）各做一次不会叠加前缀；填了哪个变量就改哪个，一个都没填时行为和以前完全一样。`APT_PROXY` 在作业容器里同样按「仓库根」处理（默认工作台是 Alpine，这条通常用不到）。

### 网络重试（Docker Hub / GitHub 抖动时）

发布过程里真正会跨网络的调用都带**有界重试**（`scripts/retry.mjs`）：指数退避、只重试**传输类**失败、认证与「不存在」类答案立即失败。

| 调用 | 目标 | 重试策略 |
| --- | --- | --- |
| `docker login` | registry 认证服务（ghcr.io / auth.docker.io） | 默认 3 次；`unauthorized`/`denied` 不重试 |
| `buildx create`（builder 镜像） | Docker Hub（可用 `DOCKER_BUILDKIT_IMAGE` 换内网） | 默认 3 次；重试前先删同名 builder，避免 `already exists` |
| `buildx imagetools inspect` | ghcr.io / Docker Hub | 默认 3 次；结果分「存在 / 不存在 / 网络不可达」三态 |
| `buildx imagetools create`（publish） | ghcr.io / Docker Hub | 默认 3 次；同 source → 同 tags，幂等 |
| `buildx build --push` | 推镜像 + 拉基础镜像 | `DOCKER_BUILD_RETRY_ATTEMPTS`（默认 2，即 1 次重试）；**只对网络错误重试**，编译错误立刻失败 |
| `curl` 下载 buildx | GitHub Releases | curl 自带 `--retry 3 --retry-connrefused --connect-timeout 15` |
| `docker run tonistiigi/binfmt` | 拉 binfmt 镜像（Docker Hub） | 默认 3 次；仍失败只告警 |
| 作业容器 bootstrap / `prepare` 装包 | 发行版镜像、`DOCKER_JOB_PACKAGES` | workflow 里的 `retry()` 三次 + `prepare` 同一策略 |
| `actions/checkout` | GitHub | `continue-on-error` + 一个 `Retry checkout` 步骤（再试一次） |

**不会重试的**：认证/权限（`unauthorized`、`denied`、token 失效）、明确的「不存在」（`manifest unknown`、`not found`）、编译/构建错误、磁盘满。判为网络不可达时，publish 会明确报「查询 staging 镜像时遇到网络错误」，而不是误报「找不到 staging 镜像」。

> **唯一管不到的一处**：job 镜像 `docker:dind` 是 **runner** 在启动容器前拉取的（Docker Hub），发生在 workflow 之前，脚本无法重试。 runner 侧不稳定时请用内网镜像 / 预拉取镜像。

### 自动注入的 build arg

`resolve` 会把运行期才知道的值作为 `--build-arg` 传进构建，**Dockerfile 里声明了 `ARG` 才会被消费**：

| build arg | 来源 | 覆盖方式 |
| --- | --- | --- |
| `VERSION` | 本次 tag 的版本（去掉 `v` 前缀） | `DOCKER_BUILD_ARGS: VERSION=...` |
| `GOPROXY` | 变量 `GOPROXY` | 同上 |
| `NPM_REGISTRY` | `NPM_PROXY` 被判为 registry 的结果 | 同上 |
| `PIP_INDEX_URL` | `PIP_PROXY` 被判为 index 的结果（裸域名补成实际应答的 `/simple` 或 `/index` 路径） | 同上 |
| `APT_PROXY` | 变量 `APT_PROXY`，**仅当它被判为镜像** | 同上 |
| `APK_PROXY` | 变量 `APK_REPO`（显式仓库，优先），否则复用判出的 apt 镜像根 | `DOCKER_BUILD_ARGS: APK_PROXY=...` |
| `YUM_REPO` | 变量 `YUM_REPO` | `DOCKER_BUILD_ARGS: YUM_REPO=...` |
| 变体自己的参数 | 变体表第六列，例如 `NODE_IMAGE=node:lts-alpine` | `DOCKER_BUILD_ARGS` 里写同名项 |

> **判别为真代理时不会注入 `APT_PROXY`**：镜像（repository）与代理（forward proxy）不是一回事，把代理地址塞给一个会做 `s|^https?://|<root>/|` 的 Dockerfile 会生成坏掉的 sources。`NPM_REGISTRY` / `PIP_INDEX_URL` 同理，只有判为 repository/index 才会出现。

Python 项目在 Dockerfile 里声明 `ARG PIP_INDEX_URL` 即可用它装依赖：

```dockerfile
ARG PIP_INDEX_URL=https://pypi.org/simple
RUN pip install --index-url "${PIP_INDEX_URL}" -r requirements.txt
```

### `DOCKER_META_TAGS` 支持的规则

`type=semver`（`pattern` 支持 `{{version}}`/`{{major}}`/`{{minor}}`/`{{patch}}`/`{{raw}}`，可带 `match`/`value`）、`type=ref`（`event=branch|tag|pr`）、`type=sha`（`format=short|long`，默认前缀 `sha-`）、`type=raw`（`value`）、`type=match`（`pattern`/`group`）。
通用属性：`enable`、`priority`、`prefix`、`suffix`；表达式只支持 `{{branch}}`、`{{tag}}`、`{{sha}}`。

**未实现的一律报错，不会静默产出别的 tag**：`type=schedule|pep440|edge`、`{{date}}`、`{{commit_date}}`、`{{is_default_branch}}`、`{{is_not_default_branch}}`。

标签规则（以 tag `v1.2.3`、sha `abc1234` 为例）：

| 变体 | ghcr.io / Docker Hub 上得到的标签 |
| --- | --- |
| `alpine` | `v1.2.3-alpine`、`1.2.3-alpine`、`1.2-alpine`、`sha-abc1234-alpine`、`alpine` |
| `trixie-slim`（默认） | 上面一套（换成 `-trixie-slim`）**＋** 无后缀：`v1.2.3`、`1.2.3`、`1.2`、`sha-abc1234`、`latest` |

## 先构建全部变体，再统一发布（build / publish 两个作业）

一次发布分两步，**所有变体和架构都建好之前不会出现任何最终标签**：

| 作业 | 干什么 | 时间 |
| --- | --- | --- |
| `plan` | 校验变体表，输出有序变体列表（`variants=alpine,trixie-slim`）和矩阵形式 | 秒级 |
| `build` | 按表内顺序逐个变体构建（每变体一次 `buildx build --platform …`），**只推 staging 标签**：`<镜像>:staging-<版本>-<sha>-<变体>` | 几分钟～几十分钟 |
| `publish` | 先确认每个变体的 staging 都在，再用 `docker buildx imagetools create` 把 staging **提升**为最终标签（含 `latest`） | 秒级 |

要点：

- **staging 标签不是最终产物**，只是"候选镜像"；它带版本号和短 sha，所以同名 tag 用新代码重推时会生成新的 staging ref（触发重建），不会把旧候选提升出去。
- **提升是同仓库的 manifest 拷贝**（不重传 blob、不重新构建），所以 `latest` 只在最后一个变体也成功后出现。
- **任何变体失败 → build 作业失败 → publish 作业不运行 → 一个最终标签都不会产生**。

### 发布失败怎么重发（不用重新构建）

| 情况 | 做法 |
| --- | --- |
| publish 失败（网络/registry 5xx/token 过期） | 先看日志：传输类错误已经自动重试过（`DOCKER_RETRY_ATTEMPTS`）；仍然失败时只重跑 **publish 作业**（Forgejo 17：`POST /repos/{owner}/{repo}/actions/jobs/{job_id}/rerun`），秒级完成 |
| 实例只支持整轮重跑（Forgejo 16） | 直接重跑整轮：build 作业会打印 `已存在 staging 镜像，跳过构建`，只补建缺失的变体，然后 publish 再试一次 |
| 想强制重建全部 | 设仓库变量 `DOCKER_FORCE_BUILD=1`（或删掉对应的 staging 标签） |

> staging 标签会留在 registry 里（每个变体每条 registry 1 个），属于内部标签，可定期清理；清理后重跑 build 即可重建。

### 顺序构建与可选并行模式

`build` 作业按表内顺序逐个变体构建。为什么不用 matrix 并行：

- `strategy.max-parallel` 在 Forgejo 里**会被接受但不生效**（[runner#1540](https://code.forgejo.org/forgejo/runner/issues/1540)），matrix 无法限流；
- 顺序执行让两个变体共用同一个 job 容器里的 dockerd 与 buildx builder（BuildKit 的模块/层缓存也共享），也避免两份 QEMU 跨架构构建抢同一台机器的 CPU；
- **第一个失败的变体会中止后续变体**，加上 build/publish 分离，一次失败的发布绝不会把 `latest` 移到不完整的产物上。

`build` 作业覆盖全部变体，所以它的 `timeout-minutes: 180` 是全部变体的总预算，外层还有 runner 的 `runner.timeout`（默认 3h）；两个变体加起来超过它的话两处都要调大。

**想换回并行**（变体互相独立、一个失败不取消另一个）时改三行即可——`plan` 作业已经同时输出了矩阵形式：

```yaml
  build:
    needs: plan
    strategy:
      fail-fast: false
      matrix: ${{ fromJSON(needs.plan.outputs.matrix) }}
    ...
    env:
      INPUT_VARIANTS: ${{ matrix.variant }}      # 原来是 needs.plan.outputs.variants
```

这需要 runner + Forgejo 支持「`strategy.matrix` 引用 `needs.*.outputs`」（上游 runner PR #1190 / Forgejo PR #10244，2025-11 合入）。旧实例上就别切并行；注意并行时若某个变体失败，`publish` 仍然不会运行（它 needs 整个 `build` 作业）。

## runner 前置条件（重要）

job 容器默认就是 **`docker:dind`**。要注意 runner 的两个行为：

1. runner 用 `tail -f /dev/null` 启动 job 容器，所以镜像里的 dockerd **不会自动运行**；
2. workflow 里写 `options: --privileged` **会被 runner 忽略**——权限只能由 runner 侧给。

因此 runner 需要：

```yaml
# forgejo-runner 配置
container:
  privileged: true     # dind 必需；改完重启 runner
```

| 需要 | 谁提供 |
| --- | --- |
| `docker` CLI + `dockerd` | 工作台镜像（默认 `docker:dind` 自带）；只有 `build` 作业需要 daemon |
| `buildx` 插件 | 同上；换镜像时 `prepare` 会尝试安装（`publish` 用它的 `imagetools`） |
| node / git / curl | 每个作业第一步 `Bootstrap job container` 用容器里的 apk/apt/dnf/yum 安装（sources 按仓库变量改写） |
| 额外工具（gcc、make…） | 仓库变量 `DOCKER_JOB_PACKAGES`，由 `prepare` 安装 |
| QEMU binfmt（多架构） | `prepare` 注册（除非 `DOCKER_SKIP_BINFMT=1`）；只有 `build` 作业需要 |

**不再需要任何 volume 挂载，也不需要 `valid_volumes`。** `publish` 作业不启动 dockerd、不需要 QEMU，也不受 privileged 之外的影响（它只和 registry 打交道）。

### `prepare` 做什么

只在 `build` 作业里运行，按顺序、幂等：

1. 探测 daemon；不可达且镜像里有 `dockerd` → 在容器内启动它（日志 `/var/log/dockerd.log`，默认等待 90s，可用 `DOCKER_DIND_WAIT` 调整；overlay 起不来时自动用 `--storage-driver=vfs` 再试一次）。失败会打印 dockerd 日志尾部与上面的 runner 前置条件。
2. 按仓库变量改写容器自身的包源（`APK_REPO` / `APT_PROXY` / `YUM_REPO`，grep 守卫、幂等，与 bootstrap 里的改写一致），并把大写代理变量派生为小写；`DOCKER_JOB_PACKAGES` 的安装遇到网络错误会自动重试。
3. 补 `buildx`：优先发行版包（alpine `docker-cli-buildx`、apt/dnf `docker-buildx-plugin`，安装失败会重试），再不行从 `DOCKER_BUILDX_URL`（默认 buildx 官方 latest）下载插件（curl 自带重试与连接超时）。
4. 安装 `DOCKER_JOB_PACKAGES`。
5. 按需注册 QEMU binfmt（镜像 `DOCKER_BINFMT_IMAGE`，默认 `tonistiigi/binfmt`；拉取失败会重试，仍失败只告警）。
6. 打一行摘要：`[prepare] docker=27.0.0（本次启动） buildx=image packages=- platforms=linux/amd64,linux/arm64 binfmt=ok`。

### 两种接法

**1. 默认：privileged job 容器 + 容器内 dockerd（推荐）**

runner 只需 `container.privileged: true`，其余由 workflow 完成。这是内置 workflow 的接法。

**2. daemon 由 runner 提供**

如果 runner 已经配了 `container.docker_host: automount`（把宿主 socket 挂进 job 容器），或仓库变量 `DOCKER_HOST` 指向一个可达 daemon，`prepare` 探测到 daemon 可达后会**直接使用**，不会再启动 dockerd。此时 `container.privileged` 可以不开（daemon 在别处），但跨架构构建要靠那个 daemon 所在宿主的 binfmt。

### 多架构与 QEMU

第七列（或 `DOCKER_PLATFORMS`）写了多个平台时，`build-push` 会为本次运行创建 `docker-container` builder（可用 `DOCKER_BUILDKIT_IMAGE` 指定内网 BuildKit 镜像），并把两个架构打进同一个 manifest list。非本机架构的 `RUN` 步骤靠宿主内核的 binfmt 模拟执行：`prepare` 会在缺少处理器时用 privileged 容器注册，失败只告警（宿主机已有 binfmt 时构建照常成功，`DOCKER_SKIP_BINFMT=1` 可显式关闭这一步）。

### 别把三层 image 混淆（`Start image=` 不是你的产物）

| 层面 | 日志里长什么样 | 由谁决定 |
| --- | --- | --- |
| ① job 容器：跑脚本的「工作台」 | runner 启动时 `🚀 Start image=docker:dind` | workflow 的 `container.image`。换它只影响脚本在哪跑，**不影响产物**（想换内网镜像就改这一行） |
| ② 构建阶段：Dockerfile 里的 `FROM … AS frontend/backend` | `docker buildx build --file Dockerfile.trixie-slim …` 过程中拉取 `node:24-trixie`、`golang:1.27-trixie` 等 | 你的 Dockerfile |
| ③ 最终产物：推送出去的镜像 | 同一条命令的 `--tag …:1.0.0-trixie-slim` 列表 | 变体表 + Dockerfile **最后一个** `FROM` |

验证产物（不依赖日志）：

```bash
docker buildx imagetools inspect <镜像:tag>          # 看 manifest list 里的两个架构
docker run --rm --entrypoint sh <镜像:tag> -c 'head -2 /etc/os-release'
```

## 本地校验（不需要 docker daemon / registry）

```bash
# 在临时仓库里把目录复制成 .forgejo，然后用假的 docker 跑通全链路：
cp -r docker-build-push /tmp/fixture/.forgejo
GITHUB_WORKSPACE=/tmp/fixture node /tmp/fixture/.forgejo/scripts/run.mjs locate-action

# 变体表 → 顺序列表 + 矩阵（完全不碰 docker）：
GITHUB_WORKSPACE=/tmp/fixture DOCKER_VARIANTS_FILE=/tmp/fixture/.forgejo/variants.txt \
  node /tmp/fixture/.forgejo/scripts/run.mjs plan-matrix

# 只验证标签推导（完全不碰 docker）：
node --input-type=module -e "
const { resolveRelease } = await import('/tmp/fixture/.forgejo/scripts/resolve.mjs');
const { computeBuilds } = await import('/tmp/fixture/.forgejo/scripts/meta.mjs');
const state = resolveRelease({ GITHUB_REPOSITORY: 'acme/app', GITHUB_REF: 'refs/tags/v1.2.3', GITHUB_REF_NAME: 'v1.2.3',
  GITHUB_SHA: 'abc1234567890', GITHUB_EVENT_NAME: 'push', GHCR_TOKEN: 'x', RUNNER_TEMP: '/tmp' });
for (const build of computeBuilds({ state })) console.log(build.variant.padEnd(12), build.platforms.join('+'), build.tagNames.join(' '));
"
```

`test/docker-layout-e2e.test.mjs` 就是这么做的：PATH 上放一个假 `docker`（记录 argv、伪造 `buildx build` / `imagetools` / `login` / `run`、并记住哪些 staging ref 已存在），按真实步骤顺序跑 `locate → verify → prepare → resolve → preflight → login → meta → build-push → summary`，再跑 `publish` 那套步骤，并断言「每个变体一次构建」「只推 staging、不出现最终标签」「其中一个变体缺失 staging 时一个最终标签都不建」「重跑会跳过已 staged 的变体」「token 不进 argv/日志/状态文件」。

## 行为细节

- **只发布、不改仓库**：不 commit、不 push、不打 tag，也不修改工作区里的版本号。
- **幂等性**：重复推同一个 tag 会重建候选并覆盖同名 staging 标签（Docker 本身就是这个语义），然后把最终标签提升过去，不会报错；staging 已存在时会跳过重建（`DOCKER_FORCE_BUILD=1` 可强制）。
- **digest / 平台**：用 `--metadata-file` 读 `containerimage.digest`；汇总（日志与 `$GITHUB_STEP_SUMMARY`）会列出每个变体的平台、staging/最终标签与 digest。
- **label**：`created/revision/version/source/url/title` 由脚本生成，`DOCKER_META_LABELS` 可覆盖。
- **dry run**：`--output type=cacheonly`，不登录、不推送、不写 image store，`publish` 直接跳过；没有任何凭据也能跑（会用 `ghcr.io/<owner>/<仓库>` 作为预览镜像名）。仍然需要 docker CLI + daemon + buildx。
- **构建与发布分离**：`build` 作业只推 staging，`publish` 作业在全部变体成功后统一提升；第一个失败的变体会中止后续变体，因此一次失败的发布不会把 `latest` 移到不完整的产物上。`latest` 只属于默认变体。
- **多 registry**：一次构建同时把 staging 推到两个 registry（各自登录一次），提升时也是在各自 registry 内做 manifest 拷贝。

## 已知限制

- 只支持 **ghcr.io 与 Docker Hub**；其它 registry、`registry-auth` 多 registry YAML、`scope`、OIDC、ECR 相关能力未实现（超出范围）。
- 需要 runner 允许 privileged job 容器（默认接法）；否则只能走「runner 提供 daemon」。
- 不支持 Docker Bake（HCL）、annotations 输出、`--secret`/`--ssh`、named contexts、`add-hosts`/`ulimit`/`shm-size`/`network`。
- **没有缓存导入导出**（`type=gha` 在 Forgejo 不存在，`--cache-from/--cache-to` 也未开放）；重复构建靠 BuildKit 自身的层缓存。
- 构建上下文固定为仓库根目录，tag 必须是 semver（没有任意 tag 的开关）。
- 没有 webhook 通知：推送失败或成功都以 job 状态与日志呈现。
- `secrets.X || vars.X` 依赖 Forgejo 的表达式实现；若你使用的版本不支持，把 token 直接放 Secrets 并改成 `${{ secrets.X }}` 即可。
- 依赖 `actions/checkout@v4`（Forgejo 默认 actions registry）。若实例无法访问，改成全限定 URL `https://code.forgejo.org/actions/checkout@v4`。
- 多架构构建在 QEMU 下明显慢于本机构建；一个变体的产物不能复用给另一个变体（**这是设计目标**）。
- 变体默认**顺序**构建：第一个失败会中止后续变体（见「顺序构建与可选并行模式」）；`strategy.max-parallel` 在 Forgejo 里不生效，并行要靠 matrix + needs 动态矩阵。
- **staging 标签会留在 registry 里**（`staging-<版本>-<sha>-<变体>`，每变体每 registry 一个），属于内部标签；本项目不做自动清理（GHCR/Docker Hub 删除需要额外权限）。
- 「只重跑 publish」依赖实例支持重跑单个 job（Forgejo 17 的 API / UI）；更老的实例可以重跑整轮——`build` 作业会跳过已 staged 的变体，代价最小的那部分仍会重新执行。

### 已移除的变量与替代做法

| 移除的变量 | 替代 |
| --- | --- |
| `GHCR_OWNER` / `GHCR_USER` | 用 `GHCR_IMAGE` 给完整镜像名（登录用户即命名空间） |
| `DOCKER_IMAGE_NAME` | 用 `DOCKER_META_IMAGES` |
| `DOCKER_DEFAULT_VARIANT` | 在变体表第五列给一个变体写 `true` |
| `DOCKER_CONTEXT` | 构建上下文固定为仓库根目录；Dockerfile 路径仍可带子目录 |
| `DOCKER_CACHE_FROM` / `DOCKER_CACHE_TO` / `DOCKER_PULL` / `DOCKER_NO_CACHE` / `DOCKER_PROVENANCE` | 无（缓存与 attestation 不再开放） |
| `DOCKER_PUSH` | 只有 dry run 不推送；tag 触发即推送 |
| `DOCKER_ALLOW_ANY_TAG` | 无（只支持 semver tag） |
| `APK_REPOSITORY` / `YUM_REPOSITORY` | 用短名 `APK_REPO` / `YUM_REPO` |
| `SKIP_TOOL_INSTALL` | 无：容器内缺什么由 Bootstrap 与 `prepare` 自动安装，`DOCKER_JOB_PACKAGES` 可加额外包 |
| `GO_PROXY` | 用 `GOPROXY` |
| `APK_PROXY` / `YUM_PROXY`（此处指仓库变量） | 用 `APT_PROXY`（镜像判别）或 `DOCKER_BUILD_ARGS` 显式传构建参数 |
| 构建镜像 `Dockerfile.builder` / `builder-image` 作业 / `container.options` 挂载 | job 容器改用 `docker:dind`，缺什么在容器内装；不再需要挂载与 `valid_volumes` |

## 首次使用需要在真实实例上确认的点

本目录的代码在源仓库经过了 167 个用例的单元测试与假 docker 端到端，但以下几项只有真实 Forgejo + runner + registry 才能确认，建议先 `dry_run: true` 演练一次：

1. runner 是否打开了 `container.privileged: true` 并重启过：日志里 `prepare` 应打印 `docker=…（本次启动）`，否则会带着 runner 侧清单失败。
2. `actions/checkout@v4` 在该实例是否可达（Bootstrap 已装好 node/git）。
3. `GHCR_TOKEN` / `DOCKERHUB_TOKEN` 的权限是否足够（`write:packages` / Docker Hub 读写+删除；提升是写操作，需要同样的写权限）。
4. `secrets.GHCR_TOKEN || vars.GHCR_TOKEN` 这类表达式在该实例的解析结果是否符合预期。
5. `${{ inputs.* }}` 是否被求值：不被求值时脚本会退回事件载荷并打警告，功能不受影响。
6. 多架构：首次跑看 `prepare` 的 `binfmt=` 一栏；`missing:arm64` 说明宿主机没有 QEMU 处理器，需要宿主安装 qemu-user-static 或让 job 容器保持 privileged。
7. 顺序构建的总耗时是否在 `timeout-minutes: 180` 与 runner 的 `runner.timeout`（默认 3h）之内。
8. `build` 之后 `publish` 是否只做 manifest 提升：`publish` 日志里应只有 `buildx imagetools` 调用；想验证重发路径，可以在首次成功后重跑一次整轮，确认 `build` 打印「跳过构建」。

## 测试

```bash
node --test test/docker-*.test.mjs                        # 本 Action 的 167 个用例
node --test test/*.test.mjs test/npm-publish/*.test.mjs   # 本仓库全部用例
```

用例直接 import 这里发布的同一批 `.mjs` 文件（`test/action-files.mjs` 的 `dockerBuildPush` 绑定），并有把整目录复制成 `.forgejo` 后按真实步骤跑通的端到端用例。
