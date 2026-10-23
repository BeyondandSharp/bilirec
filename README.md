# bilirec — Bilibili 直播录制服务（仅录制）

以 [Bilirec](https://github.com/bilirec/bilirec) 为底座（调度 / REST API / WebUI 后端 / 三格式原生拉流），
吸收录播姬的 FLV 修复能力与 biliup 的 streamlink/ffmpeg 拉流方式。**仅专注录制**：不做上传、不做移动端。
整体 **GPL-3.0-only**（`flvrepair` 移植自 GPL-3.0-only 的录播姬 FLV 规则），MIT 归属见 `NOTICE`。

本文档面向**部署与使用**。开发/测试/架构/验证见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)；
配置契约见 [docs/CONFIG-V2.md](docs/CONFIG-V2.md)，运维/排障见 [docs/OPERATIONS-V2.md](docs/OPERATIONS-V2.md)。

## 镜像与变体

镜像发布在 Docker Hub / ghcr.io（命名空间以实际发布为准，下例为 `beyondandsharp/bilirec`）：

| tag | 内容 |
|---|---|
| `latest`、`alpine` | Alpine 前后端合一，slim：native 录制 + `flvrepair`，无 ffmpeg/streamlink |
| `trixie` | Debian 13 前后端合一，slim |
| `alpine-full`、`trixie-full` | 额外含 ffmpeg + streamlink：外部下载器与 mp4/m4a 转码可用 |
| `alpine-headless`、`trixie-headless`（及 `-full-headless`） | 仅后端 API，不含前端 |

- slim 镜像只支持 native 录制；前端会自动探测 `GET /convert/providers`，没有转码后端时隐藏“转换任务”。
- `latest` = `alpine` 变体；需要转码/外部下载器时使用 `-full` tag。
- 镜像内已内置 `flvrepair`。

## 快速开始（compose，前后端合一）

```yaml
services:
  bilirec:
    image: ${BILIREC_IMAGE:-beyondandsharp/bilirec:latest}
    container_name: bilirec
    init: true
    restart: unless-stopped
    stop_grace_period: 30s
    ports:
      - "${WEB_PORT:-8080}:80"    # 前端 + 同源 API（nginx 反代到容器内 8080）
      - "${API_PORT:-8081}:8080"  # 可选：直连后端 API
    environment:
      TZ: ${TZ:-Asia/Shanghai}
      BILIBILI_LOGIN_MODE: ${BILIBILI_LOGIN_MODE:-anonymous}
      PUID: ${PUID:-10001}
      PGID: ${PGID:-10001}
    volumes:
      - config:/app/config
      - records:/app/records
      - database:/app/database
      - secrets:/app/secrets

volumes:
  config:
  records:
  database:
  secrets:
```

```bash
BILIREC_IMAGE=yourname/bilirec:latest docker compose up -d
# 打开 http://localhost:8080 ；登录页后端地址填 http://localhost:8080
# 需要 ffmpeg/streamlink 与转码时，把 image 换成 :alpine-full 或 :trixie-full
```

仅后端镜像（`-headless`）直接暴露 API：`-p 8080:8080`，并同样挂载 `config`/`records`/`database`/`secrets` 卷。

## 配置中心：`config.json` 与 `secrets.json`

应用设置可以直接写环境变量，也可以放在两个扁平 JSON 文件里（`{"KEY":"value"}`）：

| 文件 | 容器内默认路径 | 权限 | 说明 |
|---|---|---|---|
| `CONFIG_FILE` | `/app/config/config.json` | `0644` | 非敏感配置 |
| `SECRETS_FILE` | `/app/config/secrets.json` | `0600` | 仅 `PASSWORD`、`VIEWER_PASSWORD`、`JWT_SECRET`、`CLOUDCONVERT_API_KEY`、`FRP_TOKEN`、`NOTIFY_SSE_TOKEN` |

- 路径可用 `CONFIG_FILE` / `SECRETS_FILE` / `ENV_FILE` 环境变量覆盖；compose 中通常使用 `config` 卷。
- 优先级：**系统环境变量 > 配置文件 > `.env` > 内置默认**；密钥同理（环境变量 > secrets 文件 > `.env` > 默认）。
  房间级覆盖（FEATURES-V2）始终高于上述全局来源。
- `.env` 搜索顺序：`ENV_FILE` → `./.env.local` → `./.env` → 可执行文件同目录 `.env`；容器镜像不会自动写 `.env`。
- 文件由应用原子写入；secrets 文件过宽时会收紧为 `0600`；未知键在读写/重置中保留。
- 启动时会把误放在 `config.json` 中的密钥迁移到 `secrets.json`；旧 `settings.db` 的 FEATURES-V2 全局覆盖会导入
  `config.json`（幂等）。
- `MAX_RECORDING_HOURS` 默认 `0` 表示单次录制不限制时长；显式指定 `duration_minutes` 时仍按指定值执行。
- 存储大小设置支持 `B`、`KiB`、`MiB`、`GiB`、`TiB`、`PiB` 单位；设置页保存为带单位的形式
  （如 `2 GiB`），后端解析后转换为内部字节/MiB 值。`SEGMENT_SIZE_MB`、`MIN_FILE_SIZE_MB` 等
  录制大小设置也可在设置页选择单位。

- 修改生效规则：FEATURES-V2 特性值对**新会话**生效；`WEBHOOK_URLS`、`FRONTEND_URLS`/
  `FRONTEND_URL`、`LOCAL_LOGS_GROUPS` 通过 UI 保存后热更新；其余环境类配置需要重启，
  `/config` 的 `restart_required` 会列出待重启键。

HTTP API（`GET` 为 admin+viewer，写操作为 admin；启用认证时需登录 cookie）：

| 方法 | 路径 | 作用 |
|---|---|---|
| GET/PUT | `/config` | 统一非敏感配置：schema、values、overrides、defaults、sources、restart_required |
| POST | `/config/reset` | 清除 `config.json` 中的受管键（保留未知键） |
| GET/PUT | `/config/secrets` | 敏感配置：GET 只返回 `set` 状态，PUT 写入 secrets 文件 |
| POST | `/config/secrets/reset` | 清除受管密钥 |
| GET/PUT | `/settings`、POST `/settings/reset` | FEATURES-V2 特性设置 |
| GET/PUT | `/room/{id}/config` | 房间级覆盖（`null`/缺省 = 继承全局） |
| GET | `/convert/providers` | 检测可用转码后端（`{providers, enabled}`） |

示例：

```bash
# 查看统一配置与来源
curl -fsS -b cookies.txt http://127.0.0.1:8080/config | jq '{config_file,secrets_file,sources,restart_required}'

# 修改两个非敏感项（只接受 /config schema 中的键；环境变量提供的键会返回 400）
curl -fsS -b cookies.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"values":{"DOWNLOADER":"ffmpeg","EXTERNAL_START_TIMEOUT_SECS":"30"}}' \
  http://127.0.0.1:8080/config

# 写入敏感配置（值不会回显；GET 只显示是否已设置）
curl -fsS -b cookies.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"values":{"JWT_SECRET":"<random>"}}' http://127.0.0.1:8080/config/secrets
```

## 录制断流恢复

- 主播正常下播（`live_status != 1`）后立即结束录制会话，不再长时间“恢复中”；
- 只有直播仍在、但拉流断开时才重试，默认窗口 30 秒（`MAX_RETRY_SECONDS`，0 关闭）；
- 恢复仍受 `MAX_RECOVERY_ATTEMPTS` 限制；设置页/环境变量修改需重启；
- 旧版 `MAX_RETRY_MINUTES` 仍兼容，但不再显示在设置页。

## PUID / PGID（后端与前后端合一镜像）

- `PUID`/`PGID` 默认 `10001`；入口允许与镜像内已有用户/组重复 ID（LinuxServer.io 风格 `groupmod -o` / `usermod -o`），因此 `PGID=100`（`users` 组）也可用。
- 入口会调整镜像内 `bilirec` 用户/组为该 uid/gid，然后用 `gosu`/`su-exec` 降权运行。
- **不会递归修改挂载目录里的文件**：只在挂载点目录对 PUID/PGID 不可写时修改该目录本身；
  已存在的 config/secrets/数据库文件仅在目标用户不可读写时修改该文件；录像文件不 chown。
- 新写入的配置/密钥文件由应用以 PUID 创建，权限分别为 0644/0600；新录制文件由 bilirec
  以 PUID 创建（`umask 0022`，通常 0644），不需要 owner 迁移。
- 前后端合一镜像里 **nginx 保持 root**（绑定 80），只有 bilirec 子进程降权到 `PUID`/`PGID`。
- 如果修改 PUID/PGID，旧录像目录可能仍属于旧用户；需要写入旧目录时请在宿主机自行
  `chown -R` 或调整目录权限，容器不会自动递归处理。

## 本地日志查看（web 日志页 / 下载）

镜像默认**不写本地日志文件**。需要在线查看/下载日志时：

- 设置 `LOCAL_LOGS_ENABLED=true`（默认 `false`）；可选 `LOCAL_LOGS_PATH` 指定主日志路径，
  默认是 `<DATABASE_DIR>/bilirec.log`（镜像内即 `/app/database/bilirec.log`）。路径所在目录需
  对 `PUID`/`PGID` 可写；`database` 卷会保留日志文件。
- `LOCAL_LOGS_SPLIT=true`（默认）会按日志类别拆分：
  - 主日志：`/app/database/bilirec.log`，包含无名称日志和所有已启用类别中除 access INFO 外的日志；
  - 类别文件：`/app/database/bilirec.log.d/<group>/current.log`；
  - 类别：`live`（bilibili/danmaku/stream）、`system`（logging/notify/subcheck 等）、
    `recorder`（convert/pipeline/recorder/repair 等）、`access`（HTTP 请求）、`update`（updatecheck）；
  - HTTP 请求 INFO/DEBUG 只写入 `access/current.log`，WARN/ERROR 仍进入主日志；
  - 可选 `LOCAL_LOGS_GROUPS=live,system,recorder,access,update` 勾选要输出的类别，
    取消的类别不写本地文件（stdout 不受影响）；
  - 如需旧版单文件行为，设置 `LOCAL_LOGS_SPLIT=false` 并重启。
- `LOCAL_LOGS_ENABLED`、`LOCAL_LOGS_PATH`、格式/轮转等属于需要重启的配置；`LOCAL_LOGS_GROUPS`
  可通过设置页热更新。相关键还有 `LOCAL_LOGS_FORMAT`（`pretty`/`json`）、
  `LOCAL_LOGS_MAX_SIZE_MB`/`LOCAL_LOGS_MAX_AGE_DAYS`/`LOCAL_LOGS_MAX_BACKUPS`/`LOCAL_LOGS_COMPRESS`
  （轮转/保留）与刷新/缓冲相关项。
- 开启后，管理员在 web 侧边栏会看到“日志”页：可选择日志来源，自动刷新、按级别过滤、搜索、
  跟随尾部、复制与下载。只读（viewer）账号不会看到该入口。
- API（仅管理员）：
  - `GET /logs/sources` 列出主日志和各来源；
  - `GET /logs?lines=N&source=access` 读取指定来源（默认 `main`）；
  - `GET /logs/download?source=access` 下载指定来源当前文件；
  - `lines` 默认 500、范围 1–2000；`/logs` 每次最多读取文件尾部 2 MiB，下载返回完整当前文件；
  - 只暴露当前文件，不暴露轮转/压缩备份；未启用或文件不存在时下载 404。

## Webhook 测试

系统配置中的 `WEBHOOK_URLS` 旁提供“发送测试 Webhook”按钮（仅管理员）。点击后后端向当前生效的
所有 webhook 地址发送一条完整的 `FileClosed` 示例事件，覆盖 `SessionId`、房间信息、相对路径、
文件大小/时间/时长等全部字段，并返回每个目标的 HTTP 成功/失败结果。测试使用示例数据，不读取或
创建真实录制文件；保存新的 `WEBHOOK_URLS` 后立即生效，无需重启。

## 列表布局、排序与分组

- 录制管理、录制文件、订阅页面支持网格/列表布局切换，偏好保存在浏览器本地；
- 录制管理可按房间号、名称、添加时间排序；录制文件可按修改时间、名称排序；订阅可按房间号、
  名称、添加时间排序，均支持升降序；
- 订阅页支持按录制状态分组（录制中/恢复中/未录制），可随时关闭。

## 重启与热更新

管理员可以在右上角点击红色电源图标重启后端，重启会先安全结束录制、刷新日志文件并重新读取配置。
接口为 `POST /system/restart`（202；viewer 403；Android/嵌入式 501）。

无需重启即可生效的设置：

- 19 个 FEATURES-V2 录制设置（保存后对之后启动的会话生效）；
- `WEBHOOK_URLS`（保存后立即替换 webhook 目标）；
- `FRONTEND_URLS` / `FRONTEND_URL`（保存后立即更新 CORS 允许来源）；
- `LOCAL_LOGS_GROUPS`（保存后立即更新本地日志类别过滤）。

其余 `/config` 配置仍需要重启；列表布局、排序、分组等前端偏好保存在浏览器本地。

## 数据目录、端口与卷

镜像内数据目录（相对 `WORKDIR /app`，可分别用 `OUTPUT_DIR`/`DATABASE_DIR`/`SECRET_DIR` 覆盖）：

```text
/app/records    录制文件（OUTPUT_DIR，路径模板相对此目录）
/app/database   bbolt 数据库（subscribes.db 等）
/app/secrets    B 站登录 cookie 等（SECRET_DIR）
/app/config     config.json + secrets.json
```

compose 默认把四个卷挂到上述路径；直接 `docker run` 时同样建议挂载，尤其是 `config` 与 `records`。
后端 API 默认端口 `8080`，前后端合一镜像前端端口 `80`（compose 示例映射为 `8080`/`8081`）。

升级/备份/回滚与排障见 [docs/OPERATIONS-V2.md](docs/OPERATIONS-V2.md)；版本（`/version`）来自镜像构建参数
`VERSION`，更新检查仓库为 <https://github.com/BeyondandSharp/bilirec>。

## 文档

- [docs/OPERATIONS-V2.md](docs/OPERATIONS-V2.md)：部署、配置/密钥、PUID/PGID、升级备份、排障
- [docs/CONFIG-V2.md](docs/CONFIG-V2.md)：统一配置中心契约（文件/优先级/API/默认文件名）
- [docs/FEATURES-V2.md](docs/FEATURES-V2.md)：分段、路径模板、过滤、封面、弹幕、FLV、B05 高级拉流
- [docs/LOGS-VIEW.md](docs/LOGS-VIEW.md)：本地日志查看/下载契约（`LOCAL_LOGS_*`、`/logs` API）

## 许可证

本仓库整体 **GPL-3.0-only**；Bilirec / biliup / bilirec-web 的 MIT 归属保留在 `NOTICE`，
上游来源见 `UPSTREAM.md`。
