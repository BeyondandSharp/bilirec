# LOGS-VIEW contract (log file viewer)

Adds an admin-only log viewer to the web UI. It reads the **current local log file**
written by `internal/modules/logging` only when `LOCAL_LOGS_ENABLED=true`; it never accepts
an arbitrary path and never exposes rotated backups.

## Backend

New package `internal/controllers/logs` (registered in `internal/bootstrap/bootstrap.go`):

### `GET /logs?lines=N`

Admin only (`rest.AdminOnly`). Default/log path rules match `wireLocalLogger`:

- `LOCAL_LOGS_PATH` if set, otherwise `<DATABASE_DIR>/bilirec.log`
- `lines` default 500, min 1, max 2000; invalid → 400.
- Tail-read at most the last **2 MiB** of the file; drop a partial first line when seeking.
- Only the current file; no rotated files/gz.

Response:

```json
{
  "enabled": true,
  "path": "/app/database/bilirec.log",
  "exists": true,
  "size": 123456,
  "mtime": "2026-10-22T03:00:00+08:00",
  "format": "pretty",
  "lines": ["..."],
  "truncated": false
}
```

- `enabled` = effective `LOCAL_LOGS_ENABLED` (requires restart after changing it).
- `enabled=false`: return 200 with `lines: []`, `truncated: false`, `exists` reflecting the file.
- File missing: `exists=false`, `lines: []`.
- `truncated=true` when the file was larger than the read window or more lines existed than requested.
- Never return file contents as a path parameter injection; only the configured file is opened.

### `GET /logs/download`

Admin only; streams the current log file as `text/plain; charset=utf-8` with
`Content-Disposition: attachment; filename="bilirec.log"`.
404 when `enabled=false` or the file does not exist. A read error is 500.

Swagger annotations; tests for tail/filters/disabled/missing/permission and path helper.

## Frontend

- New `LogsView` + `AppTab="logs"` (`?tab=logs`), nav entry in `LeftSidebar` and `BottomNav` only when
  `!isReadOnly` (admin); viewer mode never sees it.
- `LogsView` calls `GET /logs` on mount and every 3s while auto-refresh is on; refresh immediately
  on manual refresh/tab activation.
- States:
  - `enabled=false`: guidance to enable `LOCAL_LOGS_ENABLED` in Settings → 系统配置 → 日志 and restart.
  - enabled but `exists=false`: “等待日志写入” + current path, keep polling.
  - loading/error states with retry.
- Controls: refresh, auto-refresh toggle, line-count select (200/500/1000/2000), level filter
  (all/debug/info/warn/error/fatal), text search, follow-tail toggle, download, copy filtered view.
- Viewer: monospace scroll area, line numbers, level coloring, auto-scroll to bottom while following;
  scrolling up pauses follow; “回到底部” resumes.
- Display path/size/mtime/format/truncated hint.
- i18n keys under `logs.*` and `nav.logs` in zh-TW, zh-CN, en (all three files, parity check).
- API types in `types.ts`; methods `getLogs`, `downloadLogs` in `api.ts`.

## Acceptance

- With `LOCAL_LOGS_ENABLED=true` + temp `LOCAL_LOGS_PATH`, `/logs` returns current tail; download works.
- With `LOCAL_LOGS_ENABLED=false`, page shows disabled guidance, download 404, nav hidden for viewer.
- line limit/max, file missing, oversized file, invalid `lines` covered.
- `pnpm lint/build`, Go build/vet/tests, E2E/Playwright checks; no arbitrary file read.
