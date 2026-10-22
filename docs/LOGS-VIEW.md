# LOGS-VIEW contract (source-separated log viewer)

Adds an admin-only log viewer for the local log files written by
`internal/modules/logging` when `LOCAL_LOGS_ENABLED=true`. Logs are split by
logger source by default; the API never accepts an arbitrary path and never
exposes rotated backups.

## File layout

`LOCAL_LOGS_PATH` (default `<DATABASE_DIR>/bilirec.log`) is the **main log file**.

With `LOCAL_LOGS_SPLIT=true` (default):

```text
<LOCAL_LOGS_PATH>                       main aggregate
<LOCAL_LOGS_PATH>.d/live/current.log
<LOCAL_LOGS_PATH>.d/system/current.log
<LOCAL_LOGS_PATH>.d/recorder/current.log
<LOCAL_LOGS_PATH>.d/access/current.log
<LOCAL_LOGS_PATH>.d/update/current.log
```

- `main` receives: entries with no logger name, and all enabled groups except
  `access` INFO/DEBUG.
- `<group>/current.log` receives every entry for that group.
- Group mapping:
  - `live`: `bilibili`, `danmaku`, `stream`
  - `system`: `logging`, `notify`, `subcheck`, `auth`, `config`, `settings`,
    `room`, `subscribe`, `webhook`, `path`, `metrics`, `janitor`, `expose`, `fx`, `logs`
  - `recorder`: `convert`, `pipeline`, `recorder`, `repair`, `downloader`,
    `media`, `file`, `ffmpeg_core`, `fallback`
  - `access`: `rest` (Fiber HTTP access logger; INFO/DEBUG kept out of main)
  - `update`: `updatecheck`
  - Unknown named loggers fall back to `system`.
- `LOCAL_LOGS_GROUPS` (default all five) controls which groups are written at all;
  an unchecked group writes neither its file nor a main copy. Empty value = no
  named groups. Requires restart.
- `LOCAL_LOGS_SPLIT=false` preserves the old single-file behavior and ignores
  `LOCAL_LOGS_GROUPS`.

Only `current.log` per group is exposed; rotated lumberjack backups are never
listed or downloadable.

## Backend

### `GET /logs/sources`

Admin only. Returns the main file and every current source file:

```json
{
  "enabled": true,
  "split": true,
  "base_path": "/app/database/bilirec.log",
  "sources_dir": "/app/database/bilirec.log.d",
  "sources": [
    {"name": "main", "path": "/app/database/bilirec.log", "exists": true, "size": 123, "mtime": "..."},
    {"name": "access", "path": ".../access/current.log", "exists": true, "size": 456, "mtime": "..."}
  ]
}
```

- `main` is always first, followed by source names sorted alphabetically.
- Regular files only; symlinks and directories are skipped.
- `split=false`: only `main`, `sources_dir` empty.

### `GET /logs?lines=N&source=S`

Admin only. `source` defaults to `main`; group values are `live|system|recorder|access|update`; `main` reads `LOCAL_LOGS_PATH`; other values must be an existing safe group directory below `<LOCAL_LOGS_PATH>.d`.

- `lines` default 500, min 1, max 2000; invalid → 400.
- Tail-read at most the last **2 MiB** of the selected current file; drop a
  partial first line when seeking.
- Invalid/missing source → 400; split disabled + non-main source → 400.
- File missing for `main` → 200 with `exists=false`, `lines=[]`.

Response:

```json
{
  "source": "access",
  "split": true,
  "enabled": true,
  "path": "/app/database/bilirec.log.d/access/current.log",
  "exists": true,
  "size": 456,
  "mtime": "2026-10-22T03:00:00+08:00",
  "format": "pretty",
  "lines": ["..."],
  "truncated": false
}
```

### `GET /logs/download?source=S`

Admin only; streams the selected current file as `text/plain; charset=utf-8`:

- `main` → `Content-Disposition: attachment; filename="bilirec.log"`
- other source → `filename="<source>.log"`

404 when `enabled=false` or the file is missing; invalid source → 400.

Swagger annotations; tests for source listing/routing, split-disabled behavior,
tail/filters, invalid/traversal source, disabled/missing/download and admin RBAC.

## Frontend

- `LogsView` (`?tab=logs`) is admin-only; viewer mode never sees it.
- Loads `GET /logs/sources`, shows a **source selector** (`main` + per-source files).
- Defaults to `main`; switching source refetches that file and resets follow mode.
- Auto-refreshes the selected source and source list every 3s while enabled;
  manual refresh; tab visibility pause.
- States:
  - `LOCAL_LOGS_ENABLED=false`: guidance to enable + restart.
  - enabled but selected file missing: “等待日志写入” + path, keep polling.
  - loading/error with retry.
- Controls: refresh, auto-refresh, line count 200/500/1000/2000, level filter,
  search, follow-tail, download selected source, copy filtered view.
- Viewer: monospace, line numbers, level colors; scrolling up pauses follow;
  “回到底部” resumes.
- Metadata: selected source, path, size, mtime, format, truncated, filtered count.
- i18n `logs.source`, `logs.sourceMain`, `logs.sourceLive/System/Recorder/Access/Update` + all existing keys in zh-TW/zh-CN/en.

## Acceptance

- Split enabled: `main` and `access` groups appear; `access` INFO lines are absent
  from main but present in `access`; `access` WARN appears in both; downloads use
  the correct filenames. Disabled groups write neither their file nor a main copy.
- Split disabled: only `main`, old single-file tail/download behavior.
- Invalid/traversal source is rejected; disabled downloads 404; line limits/tail
  window preserved.
- `go build/vet/tests`, `pnpm lint/build`, E2E and Playwright pass.
