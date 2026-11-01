# CONFIG-V2 contract (frozen)

Approved plan: unified config center with separate secrets file, default filename change,
three-language UI/title fix, convert-capability UI cleanup, PUID/PGID, our-repo links,
deployment-only root README. Implementation contract; do not change names/shapes without Lead approval.

## 1. Files, sources, precedence

| Item | Value |
|---|---|
| `CONFIG_FILE` | Docker `/app/config/config.json`; native `./config.json`; env override |
| `SECRETS_FILE` | Docker `/app/config/secrets.json`; native `./secrets.json`; env override |
| `ENV_FILE` | optional `.env` path override |
| config file mode | `0644` (non-sensitive) |
| secrets file mode | `0600` (enforced on write; warn + chmod when too broad) |

- Both files are flat JSON maps `{"ENV_KEY":"string"}`. Values may be strings/numbers/bools
  (coerced to string); `null` deletes a key. Unknown keys are preserved.
- `.env` search: `ENV_FILE` → `./.env.local` (overrides) → `./.env` → exe-dir `.env`.
  Container images never auto-write `.env`.
- Bootstrap keys (not in UI/config/secrets): `CONFIG_FILE`, `SECRETS_FILE`, `ENV_FILE`,
  `PUID`, `PGID`, plus test-only env (`CI`, `BILIBILI_TEST_*`, `KEEP_TEST_FILES`,
  `BILIREC_WRITE_SIM_ARTIFACTS`, CloudConvert test IDs).
- Precedence:
  - non-secret: **system env > config file > .env > built-in default**
  - secret: **system env > secrets file > .env > built-in default**
  - FEATURES-V2 room overrides remain above the global chain (existing contract).
- Loader runs before `logger.Init`; only sets `os.Environ` keys absent from the system env.
  Sources are recorded: `env|file|secret-file|dotenv|default`.
- Malformed config: warn and continue. Malformed secrets: warn; secret keys from that file are
  not applied.
- Startup migration: secret keys found in config.json are moved to secrets.json; old
  `settings.db` FEATURES-V2 global overrides are imported into config.json when the file has no
  corresponding keys (idempotent; settings.db is not deleted).
- Config changes:
  - FEATURES-V2 feature values hot-apply to new sessions.
  - Other env keys apply after restart; API reports `restart_required`, except for the hot keys
    `WEBHOOK_URLS`, `FRONTEND_URLS`/`FRONTEND_URL`, and `LOCAL_LOGS_GROUPS`.

### Secret keys (only these go to secrets file/API)

`PASSWORD`, `VIEWER_PASSWORD`, `JWT_SECRET`, `CLOUDCONVERT_API_KEY`, `FRP_TOKEN`,
`NOTIFY_SSE_TOKEN`.

## 2. Managed env keys

All application-level keys (~128): every `os.Getenv` key except bootstrap/test keys, plus the 19
FEATURES-V2 keys. FEATURES-V2 keys stay in `/settings`; all others in `/config`.

Feature keys: `SEGMENT_MODE`, `SEGMENT_MINUTES`, `SEGMENT_SIZE_MB`, `SEGMENT_BY_TITLE`,
`MIN_FILE_SIZE_MB`, `FILENAME_TEMPLATE`, `TITLE_FILTER_PATTERNS`, `SAVE_STREAM_COVER`,
`DANMAKU_RECORD_TYPES`, `DANMAKU_TRANSPORT`, `FLV_WRITE_METADATA`, `FLV_SPLIT_ON_SCRIPT_TAG`,
`FLV_DISABLE_SPLIT_ON_ANNEXB`, `BILI_LIVE_API_HOST`, `BILI_LIVE_API_FALLBACK_HOST`,
`BILI_CDN`, `BILI_CDN_FALLBACK`, `BILI_FORCE_SOURCE`, `BILI_REPLACE_CN01`.

upstream `v0.4.0` 合并新增一个受管非敏感键（schema 已随 `internal/modules/config/config.go`
的 `os.Getenv` 重新生成登记）：

| 键 | 类型 | 默认 | 生效 | schema group | 说明 |
|---|---|---|---|---|---|
| `PREVIEW_MEDIA_MAX_QUEUED_BYTES` | number（字节） | `4194304` | 重启 | `recording` | 每个预览 `/preview/.../media` 连接的出站队列上限；≤0 回退默认 |

- 该键可写在 `config.json` 或环境变量（优先级不变），修改后进入 `/config`
  `restart_required`，重启生效。
- `SILENT_ACCESS_LOG` **不是本 fork 的受管键**：上游 `v0.4.0` 对该开关的 Skip 时机修复不恢复，
  fork 已在 `63eaf4c` 移除该键，访问日志改由分来源日志承担（D018/D022）。
- `record_subtitle` **不是**全局受管键：它是开录参数/房间配置（见 `FEATURES-V2.md` §3.5、
  `INTERFACES.md` §8.4），不进入 `/config` schema。

## 3. Default filename template

`FILENAME_TEMPLATE` default is now:

```
{roomid}-{uname}/{date}_{time}_{title}
```

- Relative path under `OUTPUT_DIR`; `/` creates directories; `{date}`=YYYYMMDD, `{time}`=HHMMSS.
- Existing path safety rules remain (no absolute/`..`/backslash, per-segment sanitization).
- Explicit empty string keeps legacy naming:
  `<uname>-<roomid>/<title truncated 20>-YYYYMMDD_HHMMSS.ext`.
- New default applies to new sessions only; existing files are not renamed.

## 4. API

### `/settings` (FEATURES-V2, additive)

Keep `defaults/overrides/values` (typed). Add:

```json
{
  "sources": {"segment_mode": "env|file|dotenv|default"},
  "config_file": "/app/config/config.json",
  "secrets_file": "/app/config/secrets.json",
  "restart_required": []
}
```

Persistence of feature global overrides moves from settings.db to config.json. Existing GET/PUT body
shape remains backward compatible.

### New `/config` endpoints (admin write, admin+viewer read)

`GET /config`:

```json
{
  "config_file": "...", "secrets_file": "...",
  "schema": {"groups": [{
    "id": "server",
    "label": {"zh-CN": "服务", "zh-TW": "服務", "en": "Server"},
    "description": {"zh-CN": "...", "zh-TW": "...", "en": "..."},
    "fields": [{
      "key": "PORT", "kind": "number|bool|string|textarea|enum|csv|duration|secret",
      "default": "8080", "min": 1, "max": 65535, "enum": null,
      "restart_required": true, "sensitive": false,
      "requires": "ffmpeg", "disabled_reason": "",
      "label": {"zh-CN": "...", "zh-TW": "...", "en": "..."},
      "description": {"zh-CN": "...", "zh-TW": "...", "en": "..."}
    }]
  }]},
  "values": {"PORT": "8080"},
  "overrides": {"PORT": "8080"},
  "defaults": {"PORT": "8080"},
  "sources": {"PORT": "env|file|dotenv|default"},
  "restart_required": ["PORT"]
}
```

- Secret fields are not part of `/config` schema/values.
- `PUT /config` body `{"values":{"KEY":"..."},"clear":["KEY"]}`. Reject unknown/bootstrap/secret
  keys (400). Returns the same shape as GET.
- `POST /config/reset`: remove managed non-secret keys from config.json, preserve unknown keys.
- `GET /config/secrets`: `{"set":{"PASSWORD":true,...}}` (never values).
- `PUT /config/secrets`: `{"values":{"PASSWORD":"..."},"clear":["JWT_SECRET"]}`; write secrets.json 0600.
- `POST /config/secrets/reset`: remove managed secret keys.
- All routes: Swagger annotations; GET viewer+admin, mutations admin-only.
- Validation: schema enum/min/max/type plus existing FEATURES-V2 validators; invalid → 400 Chinese message.
- `requires:"ffmpeg"` fields get `disabled_reason` when `/convert/providers` is empty.

## 5. Schema metadata

Groups: `server`, `storage`, `recording`, `danmaku`, `logging`, `metrics`, `frp`, `convert`,
`subcheck`, `downloader`, `repair`, `update`, `features`, `secrets`.

Each field carries three-language `label`/`description` objects (backend-provided; frontend picks
the active language). FEATURES-V2 fields may keep frontend i18n keys and be rendered by the existing
feature form; generic env fields render from `/config`.

## 6. Frontend

- SettingsView sections: 录制设置 (existing FEATURES-V2), 系统配置 (`/config` schema, folded groups,
  search, source badges, env-locked disabled, restart-required badge), 敏感配置 (`/config/secrets`,
  write-only, set/unset state).
- Convert capability: App-level `ConvertCapabilityContext` from `GET /convert/providers`; hide
  nav entry, FileCard desktop convert button, mobile menu item, convert Dialog; disable settings
  fields with `requires:"ffmpeg"`; deep link falls back.
- Languages: `zh-TW`, `zh-CN`, `en`; full `en` resource; fallback `en`; language menu includes
  English. `document.title`/`documentElement.lang`/meta description set from i18n on language change.
  New `app.documentTitle` keys; index.html/manifest neutral (no hardcoded Traditional title).
- Repo links: `REPO_URL=https://github.com/BeyondandSharp/bilirec`; About changelog/issues and
  release fallback use this; no GitHub-bilirec upstream links in these UI surfaces.
- `GET /config` field labels render directly from schema (no per-key i18n file needed).

## 7. PUID / PGID (backend + combined images only)

- `PUID`/`PGID` default `10001`; read by a POSIX-sh entrypoint; values must be numeric.
- Entrypoint follows LinuxServer.io mapping semantics: `groupmod -o` / `usermod -o` allow
  non-unique UID/GID, so host ids already present in the image (e.g. `PGID=100` = `users`) are
  accepted instead of rejected. Alpine installs `shadow` to provide these tools.
- Entrypoint adjusts bilirec user/group, creates/chowns `/app/config`, `/app/secrets`,
  `/app/database`, `/app/records` (records recursive only when top-level owner differs from PUID),
  then drops privileges via `gosu` (Debian) or `su-exec` (Alpine).
- Combined images: nginx stays root; bilirec child drops to PUID; existing signal handling preserved.
- Backend Dockerfiles remove `USER`, install gosu/su-exec, set `ENTRYPOINT`.
- Frontend nginx-only image is unaffected.
- Dockerfiles set `CONFIG_FILE`/`SECRETS_FILE`; compose examples mount `config:/app/config` and
  pass `PUID`/`PGID`.

## 8. Version / update check

- `pkg/updatecheck` default owner/repo: `BeyondandSharp/bilirec`; default release URL
  `https://github.com/BeyondandSharp/bilirec/releases/latest`; API remains GitHub
  (`GITHUB_API_URL` override supported). Add `UPDATE_CHECK_REPO`/`UPDATE_CHECK_RELEASE_URL`
  envs to schema; keep error codes `github_*`.
- Dockerfiles accept `ARG VERSION=dev` and inject
  `-X 'github.com/bilirec/bilirec/pkg/updatecheck.currentVersionInjected=${VERSION}'`.
  `.forgejo` already passes `VERSION`; Makefile local build passes `git describe`.
- About/Release/Issues links use the repo URL above.

## 9. README split

- Root `README.md`: deployment/service only — intro, image tags, compose examples, config/secrets
  files + precedence, PUID/PGID, volumes/ports, upgrade/backup/ops links, license.
- Move build/dev/test/architecture/verification content to `docs/DEVELOPMENT.md`; root README must
  not contain `make`, `go build`, `cargo`, `pnpm`, `build.sh`, `e2e`.
- Update docs: `docs/OPERATIONS-V2.md`, `docs/DECISIONS.md`, `docs/CONFIG-V2.md`.

## 10. Acceptance

- Precedence matrix (env/file/secrets/.env/default) passes for both config and secrets.
- Every managed env key appears exactly once in schema (`/settings` or `/config`), no bootstrap/test keys.
- `/config` + `/config/secrets` round-trip/persist/reset/permissions/masking; env-sourced locked.
- Default filename produces `{roomid}-{uname}/{date}_{time}_{title}`; empty template fallback legacy.
- Convert UI hidden with no providers; shown with ffmpeg; no other convert entry points.
- zh-CN title simplified; en UI/title/description English; zh-TW normal; i18n key parity.
- PUID/PGID container runs as requested uid/gid, config/secrets writable and 0600.
- About/Release/Issues links and update check point to `https://github.com/BeyondandSharp/bilirec`.
- Root README contains no build/dev instructions.
- `go test`, `make e2e`, `pnpm lint/build`, i18n check, Docker smoke green.
