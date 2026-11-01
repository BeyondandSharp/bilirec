import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { WrenchIcon, TrashSimpleIcon, PlayIcon } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Badge } from '@/components/ui/badge'
import { EmptyState } from '@/components/EmptyState'
import { LoadingScreen } from '@/components/LoadingScreen'
import { apiClient } from '@/lib/api'
import { isNetworkError, markOnline, noteTransportFailure } from '@/lib/network-status'
import { usePageVisibility } from '@/hooks/use-visibility'
import type { RepairListResponse, RepairTask, RepairTaskStatus } from '@/lib/types'
import { cn } from '@/lib/utils'

const EMPTY: RepairListResponse = { enabled: true, items: [] }

const STATUS_KEY: Record<RepairTaskStatus, string> = {
  pending: 'repairView.statusPending',
  queued: 'repairView.statusQueued',
  running: 'repairView.statusRunning',
  done: 'repairView.statusDone',
  failed: 'repairView.statusFailed',
}

const STATUS_CLASS: Record<RepairTaskStatus, string> = {
  pending: 'border-border text-muted-foreground',
  queued: 'border-sky-500/40 text-sky-600 dark:text-sky-300',
  running: 'border-amber-500/40 text-amber-600 dark:text-amber-300',
  done: 'border-emerald-500/40 text-emerald-600 dark:text-emerald-300',
  failed: 'border-destructive/50 text-destructive',
}

export function RepairView() {
  const { t } = useTranslation()
  const isVisible = usePageVisibility()
  const [data, setData] = useState<RepairListResponse>(EMPTY)
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [isLoading, setIsLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const selectedRef = useRef(selected)
  selectedRef.current = selected

  const fetchTasks = useCallback(async (initial = false) => {
    if (initial) setIsLoading(true)
    try {
      const next = await apiClient.getRepairTasks()
      setData({ enabled: next.enabled !== false, items: Array.isArray(next.items) ? next.items : [] })
      markOnline()
    } catch (error) {
      console.error('Failed to fetch repair tasks:', error)
      if (isNetworkError(error)) {
        noteTransportFailure(error)
      } else {
        toast.error(t('repairView.loadError'))
      }
    } finally {
      setIsLoading(false)
    }
  }, [t])

  useEffect(() => {
    if (!isVisible) return
    void fetchTasks(true)
    const timer = window.setInterval(() => void fetchTasks(false), 3000)
    return () => window.clearInterval(timer)
  }, [fetchTasks, isVisible])

  // Drop selected paths that disappeared from the list.
  useEffect(() => {
    const paths = new Set(data.items.map((item) => item.path))
    setSelected((current) => {
      const next = new Set([...current].filter((path) => paths.has(path)))
      return next.size === current.size ? current : next
    })
  }, [data.items])

  const selectable = useMemo(
    () => data.items.filter((item) => item.status !== 'running'),
    [data.items],
  )
  const selectedItems = useMemo(
    () => data.items.filter((item) => selected.has(item.path)),
    [data.items, selected],
  )
  const canRepair = data.enabled && selectedItems.length > 0 && !busy
  const canDelete = selectedItems.length > 0 && !busy && selectedItems.every((item) => item.status !== 'running')

  const toggle = (path: string) => {
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }

  const toggleAll = () => {
    setSelected((current) => {
      if (selectable.length > 0 && selectable.every((item) => current.has(item.path))) {
        return new Set()
      }
      return new Set(selectable.map((item) => item.path))
    })
  }

  const runSelected = async () => {
    if (!canRepair) return
    setBusy(true)
    try {
      const next = await apiClient.runRepairTasks([...selectedRef.current])
      setData({ enabled: next.enabled !== false, items: Array.isArray(next.items) ? next.items : [] })
      toast.success(t('repairView.repairQueued'))
    } catch (error: any) {
      toast.error(t('repairView.repairFailed') + (error?.response?.data ? `: ${error.response.data}` : ''))
    } finally {
      setBusy(false)
    }
  }

  const deleteSelected = async () => {
    if (!canDelete) return
    setBusy(true)
    try {
      const next = await apiClient.deleteRepairTasks([...selectedRef.current])
      setData({ enabled: next.enabled !== false, items: Array.isArray(next.items) ? next.items : [] })
      setSelected(new Set())
      toast.success(t('repairView.deleteSuccess'))
    } catch (error: any) {
      toast.error(t('repairView.deleteFailed') + (error?.response?.data ? `: ${error.response.data}` : ''))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div className="sticky top-0 z-10 border-b border-border bg-background p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-xl font-bold">{t('repairView.title')}</h2>
          <span className="text-xs text-muted-foreground">{t('repairView.description')}</span>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={selectable.length === 0 || busy}
            onClick={toggleAll}
          >
            {selectable.length > 0 && selectable.every((item) => selected.has(item.path))
              ? t('repairView.clearSelection')
              : t('repairView.selectAll')}
          </Button>
          <span className="text-xs text-muted-foreground">
            {t('repairView.selectedCount', { count: selectedItems.length })}
          </span>
          <div className="ml-auto flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={!canDelete}
              onClick={() => void deleteSelected()}
            >
              <TrashSimpleIcon size={16} />
              {t('repairView.delete')}
            </Button>
            <Button size="sm" disabled={!canRepair} onClick={() => void runSelected()}>
              <PlayIcon size={16} weight="fill" />
              {t('repairView.repair')}
            </Button>
          </div>
        </div>
        {!data.enabled && (
          <p className="mt-2 text-sm text-destructive">{t('repairView.unavailable')}</p>
        )}
      </div>

      <div className="flex-1 overflow-y-auto p-4 pb-20">
        {isLoading ? (
          <LoadingScreen />
        ) : data.items.length === 0 ? (
          <EmptyState
            icon={<span className="text-muted-foreground"><WrenchIcon size={40} /></span>}
            title={t('repairView.emptyTitle')}
            description={t('repairView.emptyDescription')}
          />
        ) : (
          <div className="space-y-2">
            {data.items.map((item) => (
              <RepairRow
                key={item.path}
                item={item}
                checked={selected.has(item.path)}
                disabled={busy || item.status === 'running'}
                onToggle={() => toggle(item.path)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function RepairRow({
  item,
  checked,
  disabled,
  onToggle,
}: {
  item: RepairTask
  checked: boolean
  disabled: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  return (
    <div
      className={cn(
        'flex items-start gap-3 rounded-lg border border-border bg-card p-3',
        checked && 'border-primary/40 bg-primary/5',
      )}
    >
      <Checkbox
        checked={checked}
        disabled={disabled}
        onCheckedChange={onToggle}
        aria-label={t('fileCard.selectRepair')}
      />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="min-w-0 break-all text-sm font-medium">{item.path}</span>
          <Badge variant="outline" className={cn('shrink-0', STATUS_CLASS[item.status])}>
            {t(STATUS_KEY[item.status])}
          </Badge>
        </div>
        <p className="mt-1 break-all text-xs text-muted-foreground">
          {t('repairView.output', { path: item.output_path })}
        </p>
        {item.error && <p className="mt-1 break-all text-xs text-destructive">{item.error}</p>}
      </div>
    </div>
  )
}
