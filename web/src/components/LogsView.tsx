import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowDownIcon,
  ArrowsClockwiseIcon,
  CopySimpleIcon,
  DownloadSimpleIcon,
  MagnifyingGlassIcon,
  WarningCircleIcon,
} from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { LoadingScreen } from '@/components/LoadingScreen'
import { apiClient } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/api-error'
import { usePageVisibility } from '@/hooks/use-visibility'
import { useLanguage } from '@/hooks/use-language'
import { getDateLocale } from '@/lib/language'
import { cn, formatFileSize } from '@/lib/utils'
import type { LogsResponse } from '@/lib/types'

/** Auto-refresh interval required by the LOGS-VIEW contract. */
const AUTO_REFRESH_MS = 3000
const LINE_COUNT_OPTIONS = [200, 500, 1000, 2000] as const
const LEVEL_FILTERS = ['all', 'debug', 'info', 'warn', 'error', 'fatal'] as const

type LogLevel = Exclude<(typeof LEVEL_FILTERS)[number], 'all'>
type LevelFilter = (typeof LEVEL_FILTERS)[number]

interface ParsedLine {
  /** Line index within the returned tail (1-based). */
  number: number
  line: string
  level: LogLevel | null
}

const LEVEL_PATTERN = /\b(DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\b/i

function normalizeLevel(raw: string): LogLevel | null {
  switch (raw.trim().toLowerCase()) {
    case 'debug':
      return 'debug'
    case 'info':
      return 'info'
    case 'warn':
    case 'warning':
      return 'warn'
    case 'error':
      return 'error'
    case 'fatal':
      return 'fatal'
    default:
      return null
  }
}

/** Best-effort level detection for pretty and JSON log formats. */
function detectLevel(line: string): LogLevel | null {
  const trimmed = line.trimStart()
  if (trimmed.startsWith('{')) {
    try {
      const parsed = JSON.parse(trimmed) as Record<string, unknown>
      const raw = parsed.level ?? parsed.Level ?? parsed.LEVEL
      if (typeof raw === 'string') {
        const normalized = normalizeLevel(raw)
        if (normalized) {
          return normalized
        }
      }
    } catch {
      // Not JSON after all: fall through to the textual detection below.
    }
  }
  const match = LEVEL_PATTERN.exec(line)
  return match ? normalizeLevel(match[1]) : null
}

const LEVEL_TEXT_CLASS: Record<LogLevel, string> = {
  debug: 'text-muted-foreground',
  info: 'text-foreground',
  warn: 'text-amber-600 dark:text-amber-400',
  error: 'text-red-600 dark:text-red-400',
  fatal: 'text-red-700 font-semibold dark:text-red-300',
}

interface LogsViewProps {
  /** Jump to Settings → System config (used by the disabled-state guidance). */
  onOpenSettings?: () => void
}

/**
 * Admin-only log viewer (docs/LOGS-VIEW.md): tails the backend's current local
 * log file, with auto-refresh, level/search filtering, follow-tail and download.
 * The backend owns the path; the UI can never request an arbitrary file.
 */
export function LogsView({ onOpenSettings }: LogsViewProps) {
  const { t } = useTranslation()
  const { language } = useLanguage()
  const isVisible = usePageVisibility()

  const [data, setData] = useState<LogsResponse | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isDownloading, setIsDownloading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [lineCount, setLineCount] = useState<number>(500)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [levelFilter, setLevelFilter] = useState<LevelFilter>('all')
  const [search, setSearch] = useState('')
  const [follow, setFollow] = useState(true)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)

  const inFlightRef = useRef(false)
  const hasLoadedRef = useRef(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  const fetchLogs = useCallback(
    async (options?: { silent?: boolean }) => {
      if (inFlightRef.current) {
        return
      }
      inFlightRef.current = true
      if (!options?.silent) {
        setIsLoading(true)
      }
      setError(null)
      try {
        const response = await apiClient.getLogs(lineCount)
        setData(response)
        setLastUpdated(new Date())
        hasLoadedRef.current = true
      } catch (err) {
        console.error('Failed to load logs:', err)
        setError(getServerErrorMessage(err, t('logs.errorTitle')))
      } finally {
        inFlightRef.current = false
        setIsLoading(false)
      }
    },
    [lineCount, t],
  )

  // Initial load and refetch when the line-count selection changes.
  useEffect(() => {
    void fetchLogs()
  }, [fetchLogs])

  // Auto-refresh every 3s while enabled (skipped while the tab is hidden).
  useEffect(() => {
    if (!autoRefresh) {
      return
    }
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'hidden') {
        return
      }
      void fetchLogs({ silent: true })
    }, AUTO_REFRESH_MS)
    return () => window.clearInterval(timer)
  }, [autoRefresh, fetchLogs])

  // Refresh immediately when the tab becomes visible again.
  useEffect(() => {
    if (!isVisible || !hasLoadedRef.current) {
      return
    }
    void fetchLogs({ silent: true })
  }, [isVisible, fetchLogs])

  const parsedLines = useMemo<ParsedLine[]>(() => {
    const keyword = search.trim().toLowerCase()
    const result: ParsedLine[] = []
    ;(data?.lines ?? []).forEach((line, index) => {
      const level = detectLevel(line)
      if (levelFilter !== 'all' && level !== levelFilter) {
        return
      }
      if (keyword && !line.toLowerCase().includes(keyword)) {
        return
      }
      result.push({ number: index + 1, line, level })
    })
    return result
  }, [data, levelFilter, search])

  // Follow-tail: keep the viewport pinned to the newest line.
  useEffect(() => {
    if (!follow) {
      return
    }
    const element = scrollRef.current
    if (!element) {
      return
    }
    element.scrollTop = element.scrollHeight
  }, [follow, parsedLines])

  const handleScroll = () => {
    const element = scrollRef.current
    if (!element || !follow) {
      return
    }
    const distanceToBottom = element.scrollHeight - element.scrollTop - element.clientHeight
    if (distanceToBottom > 32) {
      // Scrolling up pauses follow-tail; "back to bottom" resumes it.
      setFollow(false)
    }
  }

  const handleDownload = async () => {
    setIsDownloading(true)
    try {
      const blob = await apiClient.downloadLogs()
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = 'bilirec.log'
      document.body.appendChild(anchor)
      anchor.click()
      anchor.remove()
      URL.revokeObjectURL(url)
    } catch (err) {
      console.error('Failed to download log file:', err)
      toast.error(getServerErrorMessage(err, t('logs.downloadFailed')), { position: 'bottom-center' })
    } finally {
      setIsDownloading(false)
    }
  }

  const handleCopy = async () => {
    if (parsedLines.length === 0) {
      toast.error(t('logs.copyEmpty'), { position: 'bottom-center' })
      return
    }
    try {
      await navigator.clipboard.writeText(parsedLines.map((entry) => entry.line).join('\n'))
      toast.success(t('logs.copySuccess', { count: parsedLines.length }), { position: 'bottom-center' })
    } catch (err) {
      console.error('Failed to copy logs:', err)
      toast.error(t('logs.copyFailed'), { position: 'bottom-center' })
    }
  }

  const formatMtime = (mtime: string | undefined): string => {
    if (!mtime) {
      return '—'
    }
    const parsed = new Date(mtime)
    if (Number.isNaN(parsed.getTime())) {
      return mtime
    }
    return parsed.toLocaleString(getDateLocale(language))
  }

  const isDisabled = data !== null && !data.enabled
  const isMissing = data !== null && data.enabled && !data.exists
  const showViewer = data !== null && data.enabled && data.exists
  const totalLines = data?.lines.length ?? 0

  return (
    <div className="flex flex-col h-full">
      <div className="sticky top-0 z-10 border-b border-border bg-background p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-xl font-bold">{t('logs.title')}</h2>
            <p className="text-sm text-muted-foreground">{t('logs.description')}</p>
            {lastUpdated && (
              <p className="mt-1 text-xs text-muted-foreground">
                {t('logs.lastUpdated', {
                  time: lastUpdated.toLocaleTimeString(getDateLocale(language)),
                })}
              </p>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Button
              variant="outline"
              onClick={() => void fetchLogs()}
              disabled={isLoading && data !== null}
            >
              <ArrowsClockwiseIcon size={16} className={isLoading && data !== null ? 'animate-spin' : undefined} />
              {isLoading && data !== null ? t('logs.refreshing') : t('logs.refresh')}
            </Button>
            <div className="flex items-center gap-2">
              <Label htmlFor="logs-auto-refresh" className="text-sm font-normal text-muted-foreground">
                {t('logs.autoRefresh')}
              </Label>
              <Switch
                id="logs-auto-refresh"
                checked={autoRefresh}
                onCheckedChange={setAutoRefresh}
              />
              <span className="hidden text-xs text-muted-foreground sm:inline">
                {t('logs.autoRefreshHint')}
              </span>
            </div>
          </div>
        </div>
      </div>

      <div className="flex-1 space-y-3 overflow-y-auto p-4 pb-20">
        {error && (
          <Alert variant="destructive">
            <WarningCircleIcon size={16} />
            <AlertTitle>{t('logs.errorTitle')}</AlertTitle>
            <AlertDescription className="flex flex-wrap items-center gap-2">
              <span>{error}</span>
              <Button variant="outline" size="sm" onClick={() => void fetchLogs()}>
                {t('logs.retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}

        {isLoading && data === null ? (
          <LoadingScreen />
        ) : isDisabled ? (
          <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
            <div className="flex items-start gap-3">
              <WarningCircleIcon size={22} className="mt-0.5 shrink-0 text-amber-600 dark:text-amber-400" />
              <div className="space-y-2">
                <h3 className="font-semibold">{t('logs.disabledTitle')}</h3>
                <p className="text-sm text-muted-foreground">{t('logs.disabledDescription')}</p>
                <p className="text-xs text-muted-foreground">{t('logs.disabledHint')}</p>
                {data?.path && (
                  <p className="break-all font-mono text-xs text-muted-foreground">
                    {t('logs.path')}: {data.path}
                  </p>
                )}
                {onOpenSettings && (
                  <Button variant="outline" size="sm" onClick={onOpenSettings}>
                    {t('logs.openSettings')}
                  </Button>
                )}
              </div>
            </div>
          </section>
        ) : isMissing ? (
          <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
            <div className="flex items-start gap-3">
              <span className="relative mt-1 flex size-3 shrink-0" aria-hidden="true">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber-400 opacity-75" />
                <span className="relative inline-flex size-3 rounded-full bg-amber-500" />
              </span>
              <div className="space-y-2">
                <h3 className="font-semibold">{t('logs.missingTitle')}</h3>
                <p className="text-sm text-muted-foreground">{t('logs.missingDescription')}</p>
                {data?.path && (
                  <p className="break-all font-mono text-xs text-muted-foreground">
                    {t('logs.path')}: {data.path}
                  </p>
                )}
              </div>
            </div>
          </section>
        ) : showViewer ? (
          <>
            {/* Filter / action toolbar */}
            <div className="space-y-3 rounded-xl border border-border bg-card p-3 shadow-sm">
              <div className="flex flex-wrap items-end gap-3">
                <div className="space-y-1">
                  <Label htmlFor="logs-line-count" className="text-xs text-muted-foreground">
                    {t('logs.lineCount')}
                  </Label>
                  <Select
                    value={String(lineCount)}
                    onValueChange={(value) => setLineCount(Number(value))}
                  >
                    <SelectTrigger id="logs-line-count" className="w-28">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {LINE_COUNT_OPTIONS.map((count) => (
                        <SelectItem key={count} value={String(count)}>
                          {count}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1">
                  <Label htmlFor="logs-level" className="text-xs text-muted-foreground">
                    {t('logs.level')}
                  </Label>
                  <Select
                    value={levelFilter}
                    onValueChange={(value) => setLevelFilter(value as LevelFilter)}
                  >
                    <SelectTrigger id="logs-level" className="w-32">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {LEVEL_FILTERS.map((level) => (
                        <SelectItem key={level} value={level}>
                          {level === 'all'
                            ? t('logs.levelAll')
                            : t(`logs.level${level.charAt(0).toUpperCase()}${level.slice(1)}`)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="min-w-48 flex-1 space-y-1">
                  <Label htmlFor="logs-search" className="text-xs text-muted-foreground">
                    {t('logs.search')}
                  </Label>
                  <div className="relative">
                    <MagnifyingGlassIcon
                      size={14}
                      className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground"
                    />
                    <Input
                      id="logs-search"
                      value={search}
                      placeholder={t('logs.searchPlaceholder')}
                      className="pl-8"
                      onChange={(event) => setSearch(event.target.value)}
                      clearable
                      onClear={() => setSearch('')}
                      clearAriaLabel={t('logs.search')}
                    />
                  </div>
                </div>
                <div className="flex items-center gap-2 pb-0.5">
                  <Label htmlFor="logs-follow" className="text-sm font-normal text-muted-foreground">
                    {t('logs.follow')}
                  </Label>
                  <Switch id="logs-follow" checked={follow} onCheckedChange={setFollow} />
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="outline" size="sm" onClick={() => void handleDownload()} disabled={isDownloading}>
                  <DownloadSimpleIcon size={16} />
                  {isDownloading ? t('logs.downloading') : t('logs.download')}
                </Button>
                <Button variant="outline" size="sm" onClick={() => void handleCopy()}>
                  <CopySimpleIcon size={16} />
                  {t('logs.copy')}
                </Button>
                {!follow && (
                  <Badge variant="secondary" className="text-xs font-normal">
                    {t('logs.paused')}
                  </Badge>
                )}
                <span className="ml-auto text-xs text-muted-foreground">
                  {t('logs.filteredCount', { shown: parsedLines.length, total: totalLines })}
                </span>
              </div>
            </div>

            {/* File metadata */}
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border border-border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
              <span className="min-w-0 truncate font-mono" title={data?.path}>
                {t('logs.path')}: {data?.path}
              </span>
              <span>
                {t('logs.size')}: {formatFileSize(data?.size ?? 0)}
              </span>
              <span>
                {t('logs.mtime')}: {formatMtime(data?.mtime)}
              </span>
              <span className="flex items-center gap-1.5">
                {t('logs.format')}:
                <Badge variant="outline" className="text-xs font-normal">
                  {data?.format || '—'}
                </Badge>
              </span>
              {data?.truncated && (
                <Badge variant="destructive" className="text-xs font-normal">
                  {t('logs.truncatedHint')}
                </Badge>
              )}
            </div>

            {/* Log viewer */}
            <div className="relative overflow-hidden rounded-xl border border-border bg-card">
              <div
                ref={scrollRef}
                role="log"
                aria-label={t('logs.viewerAria')}
                aria-live="off"
                onScroll={handleScroll}
                className="max-h-[62vh] min-h-64 overflow-auto font-mono text-xs leading-5"
              >
                {parsedLines.length === 0 ? (
                  <p className="p-4 text-sm text-muted-foreground">{t('logs.empty')}</p>
                ) : (
                  parsedLines.map((entry) => (
                    <div key={entry.number} className="flex gap-3 px-3 hover:bg-muted/40">
                      <span className="w-12 shrink-0 select-none text-right text-muted-foreground/60">
                        {entry.number}
                      </span>
                      {entry.level && (
                        <span
                          className={cn(
                            'w-12 shrink-0 select-none border-r border-border/60 pr-2 text-[10px] uppercase',
                            LEVEL_TEXT_CLASS[entry.level],
                          )}
                        >
                          {entry.level}
                        </span>
                      )}
                      <span
                        className={cn(
                          'min-w-0 whitespace-pre-wrap wrap-break-word',
                          entry.level ? LEVEL_TEXT_CLASS[entry.level] : undefined,
                        )}
                      >
                        {entry.line}
                      </span>
                    </div>
                  ))
                )}
              </div>
              {!follow && (
                <Button
                  size="sm"
                  className="absolute bottom-3 right-3 shadow-md"
                  onClick={() => setFollow(true)}
                >
                  <ArrowDownIcon size={14} />
                  {t('logs.backToBottom')}
                </Button>
              )}
            </div>

          </>
        ) : null}
      </div>
    </div>
  )
}
