# OPERATIONS-V2 — 配置中心、部署与运维排障

面向运维/部署的操作手册（v0.4.x）。覆盖统一配置中心（`config.json` + `secrets.json`）、
FEATURES-V2 设置与房间覆盖、数据目录/卷、PUID/PGID、升级备份与常见故障。
冻结契约：[CONFIG-V2.md](CONFIG-V2.md)（§1 文件/优先级/API、§4 配置 API、§7 PUID/PGID、§8 版本）、
[LOGS-VIEW.md](LOGS-VIEW.md)（本地日志查看/下载）；
特性契约：[FEATURES-V2.md](FEATURES-V2.md)；开发/构建见 [DEVELOPMENT.md](DEVELOPMENT.md)。
部署入口（根 README）：[../README.md](../README.md)。

## 1. 配置文件、来源与优先级

### 1.1 文件与路径

| 文件 | 容器内默认 | 本地默认 | 权限 | 内容 |
|---|---|---|---|---|
| `CONFIG_FILE` | `/app/config/config.json` | `./config.json` | `0644` | 非敏感配置，扁平 `{"ENV_KEY":"value"}` |
| `SECRETS_FILE` | `/app/config/secrets.json` | `./secrets.json` | `0600` | 仅 6 个敏感键（见 §1.4） |
| `ENV_FILE` | 可选 | 可选 | — | 显式指定 `.env` 路径（覆盖搜索链） |

- `CONFIG_FILE`/`SECRETS_FILE` 可用环境变量覆盖；未设置时按"是否运行在容器内"选择上表默认。
- 扁平 JSON：键为环境变量名，值为字符串（数字/布尔在内部按字符串处理）；`null` 删除键。
- 写入为原子写（临时文件 + rename）；`secrets.json` 强制 `0600`，已有文件权限过宽时会收紧并告警。
- 未知键在读取、写入、`POST /config/reset` 中都会保留；`POST /config/reset` 只删除受管键。

### 1.2 优先级

非敏感键（低 → 高）：

```
内置默认  <  .env  <  config.json  <  系统环境变量
```

敏感键（低 → 高）：

```
内置默认  <  .env  <  secrets.json  <  系统环境变量
```

FEATURES-V2 的房间覆盖始终高于以上全局链（见 §5）。`GET /config.sources` 会为每个键给出
`env | file | secret-file | dotenv | default`。

### 1.3 `.env`、启动顺序与迁移

- `.env` 搜索：`ENV_FILE` → `./.env.local` → `./.env` → 可执行文件同目录 `.env`；先找到的键优先。
  容器镜像**不会**自动生成/写回 `.env`。
- 加载器在 `logger.Init` 之前运行，只设置"进程环境中尚不存在"的键；因此系统环境变量始终最高优先级，
  且是"环境锁定"（无法通过 `/config` 修改）。
- 启动迁移（幂等）：
  - `config.json` 中误放的密钥自动迁移到 `secrets.json`；
  - 旧 `settings.db` 中的 FEATURES-V2 全局覆盖在 `config.json` 无对应键时导入 `config.json`。
- 文件损坏：`config.json` 解析失败 → 告警并当作空文件继续；`secrets.json` 解析失败 → 告警且不从该文件
  应用密钥。

### 1.4 引导键与敏感键

- **引导/测试键**（不可通过 `/config` 或 `/settings` 修改）：
  `CONFIG_FILE`、`SECRETS_FILE`、`ENV_FILE`、`PUID`、`PGID`，以及测试专用 env。
- **敏感键**（只在 `secrets.json` / `/config/secrets`）：
  `PASSWORD`、`VIEWER_PASSWORD`、`JWT_SECRET`、`CLOUDCONVERT_API_KEY`、`FRP_TOKEN`、`NOTIFY_SSE_TOKEN`。
- 敏感值永不出现在 `/config`、`/settings` 或 GET 响应中；`GET /config/secrets` 只返回 `set` 布尔状态。

## 2. 生效时机（restart semantics）

- 系统环境变量在启动时读取，运行期不可变。
- **FEATURES-V2 特性值**（分段/命名/过滤/封面/弹幕/FLV/B05）：对**新会话**生效；运行中的会话使用启动时
  的不可变快照。通过 `/settings`、房间配置或 `config.json` 修改都是如此。
- **其余环境类配置**（端口、目录、并发、日志、下载器/修复二进制、FRP、转码等）：写入后需要重启进程；
  `PUT /config` 返回的 `restart_required` 会列出待重启键，重启后清空。API 也以 `restart_required` 字段
  暴露该列表。
- `GET /settings` 的 `values` 是"全局默认值 + 全局覆盖"合并结果；房间覆盖在解析会话快照时再叠加。
- 默认文件名模板（CONFIG-V2 §3）：`{roomid}-{uname}/{date}_{time}_{title}`，相对 `OUTPUT_DIR`，`/` 建子目录；
  显式设置为空字符串则保留旧版命名 `<uname>-<roomid>/<title>-YYYYMMDD_HHMMSS.ext`。
- `GET /convert/providers` 返回 `{providers: [...], enabled: bool}`；slim 镜像（无 ffmpeg/cloudconvert）
  `enabled=false`，前端隐藏转码入口，`/config` schema 中 `requires:"ffmpeg"` 字段带 `disabled_reason`。

## 3. 数据目录与卷（容器内 `/app`）

| 目录 | 环境变量 | 内容 |
|---|---|---|
| `/app/records` | `OUTPUT_DIR`（默认 `records`） | 录制文件；路径模板相对此目录 |
| `/app/database` | `DATABASE_DIR`（默认 `database`） | bbolt 数据库（`subscribes.db` 等；旧 `settings.db` 仍保留） |
| `/app/secrets` | `SECRET_DIR`（默认 `secrets`） | B 站登录 cookie 等运行期密钥文件 |
| `/app/config` | `CONFIG_FILE`/`SECRETS_FILE` | `config.json` + `secrets.json` |

compose 示例挂载四个命名卷：`config:/app/config`、`records:/app/records`、`database:/app/database`、
`secrets:/app/secrets`。`config` 卷必须持久化，否则重启后配置/密钥丢失。

## 4. HTTP API

启用认证（`USERNAME`+`PASSWORD`）后：GET 需要 admin 或 viewer 会话 cookie，写操作仅 admin；
未登录返回 401，角色不足返回 403。CORS 由 `FRONTEND_URLS` 控制。

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/config` | schema、values、overrides、defaults、sources、restart_required、config/secrets 路径 |
| PUT | `/config` | `{"values":{"KEY":"value"},"clear":["KEY"]}`；仅接受受管非敏感键 |
| POST | `/config/reset` | 从 `config.json` 删除受管键（未知键保留） |
| GET | `/config/secrets` | `{"set":{...},"schema":[...]}`，永不返回值 |
| PUT | `/config/secrets` | 写 `secrets.json`（0600）；空值表示保持原值；`clear` 删除 |
| POST | `/config/secrets/reset` | 删除受管密钥 |
| GET/PUT | `/settings`、POST `/settings/reset` | FEATURES-V2 特性（body 为 19 个特性键） |
| GET/PUT | `/room/{id}/config` | 房间覆盖，`null`/缺省 = 继承全局 |
| GET | `/convert/providers` | 可用转码后端 |

`PUT /config` 校验：未知键、引导键、敏感键、FEATURES-V2 键、系统环境变量提供的"环境锁定"键均返回 400；
值按 schema 校验（`kind`/`min`/`max`/`enum`/duration）。`PUT /config/secrets` 只接受 6 个敏感键，
环境锁定的敏感键同样返回 400。

示例（假设已登录并保存 cookie）：

```bash
# 查看统一配置（含来源与待重启键）
curl -fsS -b cookies.txt http://127.0.0.1:8080/config | jq \
  '{config_file,secrets_file,restart_required,sources}'

# 修改非敏感项：DOWNLOADER + 首字节超时
curl -fsS -b cookies.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"values":{"DOWNLOADER":"ffmpeg","EXTERNAL_START_TIMEOUT_SECS":"30"}}' \
  http://127.0.0.1:8080/config

# 清除单个覆盖（回到 env/.env/default）
curl -fsS -b cookies.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"clear":["EXTERNAL_START_TIMEOUT_SECS"]}' http://127.0.0.1:8080/config

# 写敏感配置；GET 只显示 set 状态
curl -fsS -b cookies.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"values":{"JWT_SECRET":"<openssl rand -hex 32>"}}' \
  http://127.0.0.1:8080/config/secrets
curl -fsS -b cookies.txt http://127.0.0.1:8080/config/secrets | jq .set

# 重置：非敏感（保留未知键）/ 敏感
curl -fsS -b cookies.txt -X POST http://127.0.0.1:8080/config/reset
curl -fsS -b cookies.txt -X POST http://127.0.0.1:8080/config/secrets/reset

# FEATURES-V2 特性设置（新会话生效）与房间覆盖（null=继承）
curl -fsS -b cookies.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"segment_mode":"time","segment_minutes":90}' http://127.0.0.1:8080/settings
curl -fsS -b cookies.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"auto_record":true,"segment_mode":null}' http://127.0.0.1:8080/room/12345/config
```

## 4b. 本地日志查看（LOGS-VIEW）

日志页面/下载读取 `internal/modules/logging` 写出的**当前本地日志文件/来源文件**，仅在
`LOCAL_LOGS_ENABLED=true` 时启用；不会接受任意路径，也不会暴露轮转/压缩备份。

| 配置键 | 默认 | 说明 |
|---|---|---|
| `LOCAL_LOGS_ENABLED` | `false` | 是否写本地日志（启用后日志页/下载可用；修改需重启） |
| `LOCAL_LOGS_SPLIT` | `true` | 按日志类别拆分；`false` = 旧版单文件 |
| `LOCAL_LOGS_GROUPS` | `live,system,recorder,access,update` | 要写入本地文件的类别；空值 = 不写 named logger |
| `LOCAL_LOGS_PATH` | 空 = `<DATABASE_DIR>/bilirec.log` | 主日志路径；拆分文件在其旁 `<path>.d/<group>/current.log` |
| `LOCAL_LOGS_FORMAT` | `pretty` | `pretty` 或 `json`（响应里的 `format` 字段） |
| `LOCAL_LOGS_MAX_SIZE_MB` | `20` | 单文件轮转阈值 |
| `LOCAL_LOGS_MAX_AGE_DAYS` / `LOCAL_LOGS_MAX_BACKUPS` / `LOCAL_LOGS_COMPRESS` | `7` / `3` / `false` | 轮转后的保留/压缩 |
| `LOCAL_LOGS_FLUSH_INTERVAL` / `LOCAL_LOGS_BATCH_BYTES` / `LOCAL_LOGS_BUFFER_SIZE` / `LOCAL_LOGS_OVERFLOW` | `1s` / `65536` / `4096` / 空 | 写入刷新与溢出策略 |

- 拆分规则（`LOCAL_LOGS_SPLIT=true`）：
  - `main`（`LOCAL_LOGS_PATH`）：无 logger name 的日志 + 已启用类别中除 `access` INFO/DEBUG 外的日志；
  - `<group>/current.log`：该类别全部日志；
  - 类别：`live`、`system`、`recorder`、`access`、`update`（映射见 `docs/LOGS-VIEW.md`）；
  - `access` 的 INFO/DEBUG HTTP 请求日志**不进入 main**；WARN/ERROR 仍进入 main；
  - `LOCAL_LOGS_GROUPS` 取消的类别完全不写本地文件。
- `LOCAL_LOGS_SPLIT=false`：所有日志写入单个 `LOCAL_LOGS_PATH`，兼容旧行为。
- 修改配置：环境变量 / `deploy/.env`，或 web 设置 → 系统配置 → 日志；该组键均为
  `restart_required`，修改后重启容器生效。
- 页面/API 权限：仅管理员（admin）。viewer 看不到“日志”导航，直接请求 `/logs` 返回 403；
  未登录返回 401。
- API：
  - `GET /logs/sources`：返回 `{enabled,split,base_path,sources_dir,sources[]}`；
    只列 `main` 与 `<sources_dir>/<source>/current.log` 普通文件。
  - `GET /logs?lines=N&source=S`：`source` 默认 `main`；非法/不存在的来源 400；
    返回 `{source,split,enabled,path,exists,size,mtime,format,lines,truncated}`；
    `lines` 默认 500、范围 1–2000；每次最多读取文件尾部 2 MiB。
  - `GET /logs/download?source=S`：下载当前来源完整文件；`main` 文件名 `bilirec.log`，
    其他来源 `<source>.log`；未启用或文件不存在 404。
- 未启用（`enabled=false`）：`GET /logs` 仍返回 200，`lines=[]`，`exists` 反映文件是否存在；
  下载 404。启用但文件尚未写入：`exists=false`、`lines=[]`，页面显示“等待日志写入”并继续轮询。
- 只读取当前文件；`lumberjack` 轮转出的备份文件不会通过 API 暴露。
- 拆分后磁盘占用约增加一倍：main 保留非网络日志副本，来源文件各自保存完整日志。

## 5. FEATURES-V2 设置与房间覆盖

解析顺序（低 → 高）：**内置默认 < env < `config.json` 全局覆盖 < 房间覆盖**（房间 `null` = 继承）。
全局特性覆盖现在持久化在 `config.json`（`/settings` API 写文件）；房间覆盖仍在
`database/subscribes.db`；旧 `settings.db` 数据启动时幂等导入。会话启动时解析为不可变快照。

| 组 | 键（默认值） |
|---|---|
| 分段 | `SEGMENT_MODE=off`、`SEGMENT_MINUTES=120`、`SEGMENT_SIZE_MB=2048`、`SEGMENT_BY_TITLE=false`、`TITLE_POLL_SECS=20` |
| 命名/过滤 | `FILENAME_TEMPLATE={roomid}-{uname}/{date}_{time}_{title}`（空=旧版命名）、`TITLE_FILTER_PATTERNS=`（按行 RE2） |
| 文件 | `MIN_FILE_SIZE_MB=0` |
| 封面 | `SAVE_STREAM_COVER=false` |
| 弹幕 | `DANMAKU_RECORD_TYPES=super_chat,gift,guard`（csv）、`DANMAKU_TRANSPORT=auto` |
| FLV | `FLV_WRITE_METADATA=true`、`FLV_SPLIT_ON_SCRIPT_TAG=false`、`FLV_DISABLE_SPLIT_ON_ANNEXB=true` |
| B 站高级 | `BILI_LIVE_API_HOST=https://api.live.bilibili.com`、`BILI_LIVE_API_FALLBACK_HOST=`、`BILI_CDN=`、`BILI_CDN_FALLBACK=false`、`BILI_FORCE_SOURCE=false`、`BILI_REPLACE_CN01=` |

- 路径模板变量与 strftime：`{roomid}` `{shortid}` `{uname}` `{title}` `{year}` `{month}` `{day}`
  `{hour}` `{minute}` `{second}` `{date}` `{time}` `{segment}`；`%Y %m %d %H %M %S` 也可用。
  安全规则：拒绝绝对路径、`..`/`.` 段、反斜杠、控制字符、空段；单段 ≤200B、总长 ≤512B（模板 ≤256B）。
- 修改只对新会话生效；运行中的录制不受影响。房间覆盖用 `null` 清除后回到全局。

## 5b. 断流恢复策略

录制任务在直播流断开后会调用 `recover()`：

- 每次恢复都会重新查询直播间状态；
- **`live_status != 1`（主播已下播）→ 立即停止恢复并结束录制会话**，不再等待旧的 10 分钟窗口；
- 只有直播间仍在直播、但流地址断开/不可达时才继续重试；
- 断流重试窗口默认 **30 秒**，由 `MAX_RETRY_SECONDS` 控制（0 = 不重试）；
- 同时仍受 `MAX_RECOVERY_ATTEMPTS`（默认 15 次）限制；
- 重试失败后结束会话；若主播之后再次开播，由 `subcheck` 作为新的直播场次重新触发自动录制。

`MAX_RETRY_SECONDS` / `MAX_RECOVERY_ATTEMPTS` 均为重启生效。旧配置中的
`MAX_RETRY_MINUTES` 仍可作为兼容别名（未设置 `MAX_RETRY_SECONDS` 时按分钟×60 换算），
但不会在设置页显示。

> 注意：B 站直播状态接口可能短暂滞后。正常下播时最多仍可能看到约 30 秒的“恢复中”，
> 这是状态接口尚未返回 `live_status=0` 时的重试窗口。

## 6. PUID / PGID

- `PUID`/`PGID` 默认 `10001`，由 POSIX-sh 入口读取；必须为数字。ID 映射采用 LinuxServer.io
  的 `groupmod -o` / `usermod -o` 语义：**允许与镜像内已有用户/组重复**。例如 `PGID=100` 会复用
  镜像里的 `users` 组（gid 100），不会被拒绝。
- 入口行为：调整 `bilirec` 用户/组 → 创建 `/app/config`、`/app/secrets`、`/app/database`、
  `/app/records` → `gosu`/`su-exec` 降权。
- **不递归修改挂载内容**：挂载点目录仅在 PUID/PGID 不可写时 chown 该目录本身；已存在的
  `config.json`/`secrets.json` 和各 `*.db` 文件仅在目标用户不可读写时修改该文件；录像文件不 chown。
  新配置/密钥文件由应用以 PUID 原子写入（0644/0600）；新录制文件以 PUID 创建，`umask 0022`。
- 更换 PUID/PGID 后，旧录像目录可能属于旧用户；如需继续写入请在宿主机自行处理这些目录。
- 前后端合一镜像：nginx 以 root 启动并绑定 80，bilirec 子进程降权到 `PUID`/`PGID`；SIGTERM/SIGINT
  会转发给两个子进程，停止时正常退出。
- 卷属主：宿主机 bind mount 时把卷目录 `chown` 成 `PUID:PGID` 可避免入口修改目录本身；容器不会递归修改卷内文件。
- 前端 nginx-only 镜像不涉及 PUID/PGID。

## 7. 镜像、部署与版本

- 镜像变体：`alpine`/`trixie` × `combined`/`headless` × `slim`/`full`；`latest` = `alpine` slim
  前后端合一。slim 无 ffmpeg/streamlink；full 含两者。
- compose 部署（推荐）：

```bash
cp deploy/.env.example deploy/.env      # deploy/.env 被 gitignore
$EDITOR deploy/.env
docker compose -f deploy/docker-compose.yml up -d --build
```

- compose 的 `bilirec` 服务读取 `deploy/.env`（可选 env_file），并显式传递数据目录/端口等；
  `config` 卷挂到 `/app/config`。
- 数据目录默认 `/app/{records,database,secrets,config}`；旧 `/data` 布局可通过覆盖
  `OUTPUT_DIR`/`DATABASE_DIR`/`SECRET_DIR` 与对应卷继续使用。
- 版本：镜像 `ARG VERSION` 注入 `pkg/updatecheck.currentVersionInjected`，`GET /version` 显示；
  CI 发布时传 release tag，本地构建脚本默认用 `git describe`。
- `GET /convert/providers` 可确认当前镜像是否具备转码能力（slim 为 `enabled=false`）。

## 8. 升级、备份与回滚

**升级前备份（必须）**：

```bash
# compose 命名卷：导出 config（含 secrets，注意 0600）、database、records
docker run --rm -v deploy_config:/config -v "$PWD/backup":/backup alpine:3.24 \
  sh -c 'tar czf /backup/config.tgz -C /config . && chmod 600 /backup/config.tgz'
docker run --rm -v deploy_database:/db -v "$PWD/backup":/backup alpine:3.24 \
  sh -c 'tar czf /backup/database.tgz -C /db .'
# records 按需备份（可能很大）
```

**升级步骤**：

```bash
docker compose -f deploy/docker-compose.yml pull        # 或 build（本地源码）
docker compose -f deploy/docker-compose.yml up -d
curl -fsS http://127.0.0.1:8080/version
docker inspect --format '{{.State.Health.Status}}' bilirec
```

- 卷（`config`/`records`/`database`/`secrets`）必须保留；`config.json` 与 `secrets.json` 在升级中保持
  未知键与用户覆盖，启动迁移会自动处理旧 `settings.db` 与误放密钥。
- 回滚：切回旧 tag/镜像后再 `up -d`；配置/密钥文件向后兼容（新键旧版本会忽略或以默认值运行）。
- `docker compose up` 不会自动更新镜像 tag；升级需 `pull` 或 `build`。

## 9. 环境变量参考

- 受管非敏感键（约 120+）及三语言标签/类型/默认值/范围/是否需重启：运行中的实例查看
  `GET /config.schema`（需登录）。不要手写清单，schema 是唯一权威。
- 敏感键（6 个）见 §1.4；引导键见 §1.4。
- `deploy/.env.example` 按用途分组给出了常用键与示例（端口/来源、登录、管道、FEATURES-V2、PUID/PGID、
  CONFIG_FILE/SECRETS_FILE、VERSION 等）。
- 本地日志查看相关键：`LOCAL_LOGS_ENABLED`、`LOCAL_LOGS_PATH`、`LOCAL_LOGS_FORMAT`、
  `LOCAL_LOGS_MAX_SIZE_MB`、`LOCAL_LOGS_MAX_AGE_DAYS`、`LOCAL_LOGS_MAX_BACKUPS`、`LOCAL_LOGS_COMPRESS`、
  `LOCAL_LOGS_GROUPS`、`LOCAL_LOGS_FLUSH_INTERVAL`、`LOCAL_LOGS_BATCH_BYTES`、`LOCAL_LOGS_BUFFER_SIZE`、`LOCAL_LOGS_OVERFLOW`
  （见 §4b；schema 中 group=`logging`，均需重启生效）。
- 无效 env 值：启动时告警一次并回退内置默认（FEATURES-V2 键同规则）。

## 10. 排障

| 现象 | 原因/处理 |
|---|---|
| `PUT /config` 400 "未知的配置项" | 键不在 `/config` schema；FEATURES-V2 键请用 `/settings`，敏感键用 `/config/secrets`。 |
| 400 "该配置项由环境变量提供" | 系统环境变量优先级最高且锁定；改 `.env`/`config.json` 不生效，需改环境变量并重启。 |
| 400 校验失败（`必须是整数`/`不能大于`/`必须是 true 或 false`） | 按 schema 的 kind/min/max/enum 修正；数值键不能带单位。 |
| `secrets.json` 权限不是 0600 | `PUT /config/secrets` 写入时会收紧并告警；也可靠入口/手工 `chmod 600`。 |
| 改了配置但行为没变 | 非特性键需要重启（看 `restart_required`）；特性键只对新会话生效。 |
| `.env` 不生效 | 搜索顺序 `ENV_FILE` → `./.env.local` → `./.env` → exe 同目录；同名键系统环境变量优先。 |
| `config.json` 损坏/被忽略 | 启动日志有告警，文件被当作空；修复 JSON 后重启（未知键会被保留写入）。 |
| `PUID/PGID` 与镜像内 ID 重复 | 入口按 LinuxServer.io 的 `-o` 语义允许重复，如 `PGID=100` 复用 `users` 组；若不想重复，换一个空闲 id（默认 10001）。 |
| records 启动很慢 | 入口不会递归 chown 录像目录；若旧目录属主不同，请宿主机自行 `chown -R` 或调整目录权限。 |
| `GET /convert/providers` 为空 | slim 镜像无转码后端（`enabled=false`），转码入口/相关配置会禁用；需换 `-full` 镜像。 |
| 文件名不是期望的新模板 | 默认模板为 `{roomid}-{uname}/{date}_{time}_{title}`；显式空模板=旧版命名；旧文件不会重命名。 |
| 标题正则/模板 400 | `TITLE_FILTER_PATTERNS` 每行须为合法 RE2；模板禁止绝对路径、`..`、反斜杠、控制字符、空段。 |
| 标题切段不触发 | `SEGMENT_BY_TITLE=true` 且需要访问直播 API；`TITLE_POLL_SECS` ≥5（默认 20），错误仅告警。 |
| 封面缺失 | `SAVE_STREAM_COVER=true`；封面抓取为尽力而为（5s 超时、20MB、image/*），失败仅告警。 |
| 修复降级（repair=degraded） | 侧车崩溃/不可用时会重试一次后透传，录制不中断；检查 `FLV_REPAIR_BIN` 与 `flvrepair --version`。 |
| B05 自定义 host 失败 | 自定义 API host 必须反代官方 API（含 WBI）；失败自动回退官方/original URL，录制不中断。 |
| 设置 API 401/403 | 启用认证时 GET 需 admin/viewer、写需 admin；先 `POST /login` 并携带 cookie。 |
| 日志页提示“未启用” | `LOCAL_LOGS_ENABLED=true` 并重启（该键 `restart_required`）；可在 `deploy/.env` 或设置 → 系统配置 → 日志 中开启。 |
| 日志页一直“等待日志写入” | 检查 `LOCAL_LOGS_PATH` 目录存在且对 `PUID`/`PGID` 可写（默认 `<DATABASE_DIR>/bilirec.log`，镜像内 `/app/database/bilirec.log`）。 |
| 下载日志 404 | `LOCAL_LOGS_ENABLED=false` 或当前日志文件不存在（轮转后的备份不提供下载）；先启用并产生新日志。 |
| viewer 看不到“日志”或请求 403 | 日志查看仅管理员；viewer 只读权限不能访问 `/logs`、`/logs/download`。 |
| `/logs?lines=` 返回 400 | 值必须是整数且在 1–2000；默认 500。 |
| `/logs` 看不到最新一行 | 每次最多读取/展示尾部 2 MiB，或请求行数小于文件行数（`truncated=true`）；下载接口返回当前文件完整内容。 |

## 11. 验证与已知限制

- 离线 E2E：`make e2e`（构建 → 回放源 → 下载器 → flvrepair → 后端 API → FEATURES-V2/CONFIG-V2
  配置/鉴权/来源优先级/默认文件名/转码能力）。`python3 scripts/origin.py --selftest` 验证回放源。
- 真实直播录制为可选：设置 `E2E_LIVE_ROOM_ID` 后运行（需要外网与正在直播的房间）；离线不会伪造开录。
- 已知限制：B05 依赖 B 站 API 可用性（失败回退）；CloudConvert 需要密钥；前端 nginx-only 镜像不涉及
  PUID/PGID；Docker 推送/发布流程见 [RELEASE-CI.md](RELEASE-CI.md)。
