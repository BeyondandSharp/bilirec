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
<LOCAL_LOGS_PATH>.d/<source>/current.log per-source file
```

- `main` receives: entries with no logger name, all named logger entries except
  `rest` INFO/DEBUG, and all WARN/ERROR entries (including `rest`).
- `<source>/current.log` receives every entry for that logger.
- `rest` is the Fiber HTTP access logger; its INFO/DEBUG entries are deliberately
  kept out of main so request logs do not drown application logs.
- Source names are derived from `zapcore.Entry.LoggerName`, lowered and sanitized
  to `[a-z0-9._-]`, max 64 chars; `main` is reserved; invalid/overflow names fall
  back to `other`/main.
- `LOCAL_LOGS_SPLIT=false` preserves the old single-file behavior.

Only `current.log` per source is exposed; rotated lumberjack backups are never
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
    {"name": "rest", "path": ".../rest/current.log", "exists": true, "size": 456, "mtime": "..."}
  ]
}
```

- `main` is always first, followed by source names sorted alphabetically.
- Regular files only; symlinks and directories are skipped.
- `split=false`: only `main`, `sources_dir` empty.

### `GET /logs?lines=N&source=S`

Admin only. `source` defaults to `main`; `main` reads `LOCAL_LOGS_PATH`; other
values must be an existing safe source directory below `<LOCAL_LOGS_PATH>.d`.

- `lines` default 500, min 1, max 2000; invalid → 400.
- Tail-read at most the last **2 MiB** of the selected current file; drop a
  partial first line when seeking.
- Invalid/missing source → 400; split disabled + non-main source → 400.
- File missing for `main` → 200 with `exists=false`, `lines=[]`.

Response:

```json
{
  "source": "rest",
  "split": true,
  "enabled": true,
  "path": "/app/database/bilirec.log.d/rest/current.log",
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
- i18n `logs.source`, `logs.sourceMain` + all existing keys in zh-TW/zh-CN/en.

## Acceptance

- Split enabled: `main` and `rest` sources appear; `rest` INFO access lines are
  absent from main but present in rest; `rest` WARN appears in both; downloads use
  the correct filenames.
- Split disabled: only `main`, old single-file tail/download behavior.
- Invalid/traversal source is rejected; disabled downloads 404; line limits/tail
  window preserved.
- `go build/vet/tests`, `pnpm lint/build`, E2E and Playwright pass.
