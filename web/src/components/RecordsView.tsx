import { useState, useEffect, useRef } from 'react'
import { RecordCard } from './RecordCard'
import { RecordListRow } from './list/RecordListRow'
import { EmptyState } from './EmptyState'
import { RoomIdInputWithConfirmDialog } from './RoomIdInputWithConfirmDialog'
import { RecordStartOptions } from './RecordStartOptions'
import { apiClient } from '@/lib/api'
import {
  buildStartRecordParams,
  defaultRecordStartConfig,
  type RecordStartConfig,
} from '@/lib/room-config'
import { isNetworkError, markOnline, noteTransportFailure } from '@/lib/network-status'
import { applyStreamIoRates } from '@/lib/stream-io-rates'
import { toast } from 'sonner'
import type { RecordTask, RoomInfo } from '@/lib/types'
import { LoadingScreen } from './LoadingScreen'
import { usePageVisibility } from '@/hooks/use-visibility'
import { usePersistedState } from '@/hooks/use-persisted-state'
import { ViewModeToggle, type ViewMode } from '@/components/view/ViewModeToggle'
import { SortControl, type SortDirection } from '@/components/view/SortControl'
import { useRole } from '@/lib/role-context'
import useSWR from 'swr'
import { useTranslation } from 'react-i18next'

interface RecordsViewProps {
  onRefresh?: () => void
}

export function RecordsView({ onRefresh }: RecordsViewProps) {
  const { t } = useTranslation()
  const { isReadOnly } = useRole()
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const scrollPositionRef = useRef(0)
  const isVisible = usePageVisibility()
  const [startConfig, setStartConfig] = useState<RecordStartConfig>(defaultRecordStartConfig)
  const [viewMode, setViewMode] = usePersistedState<ViewMode>('records-layout', 'grid')
  const [sortField, setSortField] = usePersistedState('records-sort', 'added')
  const [sortDirection, setSortDirection] = usePersistedState<SortDirection>('records-sort-dir', 'desc')

  const resetRecordStartForm = () => {
    setStartConfig(defaultRecordStartConfig())
  }

  const {
    data: tasks = [],
    error,
    isLoading,
    mutate,
  } = useSWR<RecordTask[]>(
    'record/tasks',
    async () => {
      const recordTasks = await apiClient.getRecordTasks()
      const roomIds = Array.from(new Set(recordTasks.map((task) => task.roomId)))

      if (roomIds.length === 0) {
        return recordTasks
      }

      const roomInfoMap = await apiClient.getRoomInfos(roomIds)

      return applyStreamIoRates(
        recordTasks.map((task) => ({
          ...task,
          roomInfo: roomInfoMap[String(task.roomId)],
        }))
      )
    },
    {
      refreshInterval: isVisible ? 5000 : 0,
      refreshWhenHidden: true,
      revalidateOnFocus: true,
      keepPreviousData: true,
      onSuccess: () => markOnline(),
      onError: (err) => {
        if (isNetworkError(err)) {
          noteTransportFailure(err)
        }
      },
    }
  )

  useEffect(() => {
    if (!error || isNetworkError(error)) {
      return
    }
    console.error('Failed to fetch tasks:', error)
    toast.error(t('recordsView.loadError'))
  }, [error, t])

  useEffect(() => {
    if (scrollContainerRef.current && scrollPositionRef.current > 0) {
      scrollContainerRef.current.scrollTop = scrollPositionRef.current
    }
  }, [tasks])

  const handleConfirmRecord = async (roomInfo: RoomInfo) => {
    try {
      await apiClient.startRecord(buildStartRecordParams(roomInfo.room_id, startConfig))
    } catch (error: any) {
      console.error('Failed to start record:', error)
      toast.error(error.response?.data || t('recordsView.startFailed'))
      throw error
    }
    toast.success(t('recordsView.startSuccess'))
    await mutate()
    onRefresh?.()
  }

  const handleStop = async (roomId: number) => {
    try {
      await apiClient.stopRecord(roomId)
      toast.success(t('recordsView.stopSuccess'))
      await mutate()
      onRefresh?.()
    } catch (error: any) {
      console.error('Failed to stop record:', error)
      toast.error(error.response?.data || t('recordsView.stopFailed'))
    }
  }

  const sortedTasks = tasks.slice().sort((a, b) => {
    const value = (task: RecordTask) => {
      switch (sortField) {
        case 'room_id':
          return task.roomId
        case 'name':
          return (task.roomInfo?.uname || String(task.roomId)).toLowerCase()
        case 'added':
        default: {
          const raw = task.startTime
          if (typeof raw === 'number') return raw
          const parsed = typeof raw === 'string' ? Date.parse(raw) : Number.NaN
          return Number.isFinite(parsed) ? parsed : 0
        }
      }
    }
    const av = value(a)
    const bv = value(b)
    let cmp = 0
    if (typeof av === 'number' && typeof bv === 'number') {
      cmp = av - bv
    } else {
      cmp = String(av).localeCompare(String(bv))
    }
    if (cmp === 0) cmp = a.roomId - b.roomId
    return sortDirection === 'asc' ? cmp : -cmp
  })

  return (
    <div className="flex flex-col h-full">
      <div className="sticky top-0 bg-background z-10 p-4 border-b border-border">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="text-xl font-bold">{t('recordsView.title')}</h2>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <ViewModeToggle value={viewMode} onChange={setViewMode} />
            <SortControl
              id="records-sort"
              label={t('viewControls.sortBy')}
              value={sortField}
              options={[
                { value: 'added', label: t('viewControls.sortAdded') },
                { value: 'room_id', label: t('viewControls.sortRoomId') },
                { value: 'name', label: t('viewControls.sortName') },
              ]}
              direction={sortDirection}
              onValueChange={setSortField}
              onDirectionChange={setSortDirection}
            />
          </div>
          {!isReadOnly && (
            <RoomIdInputWithConfirmDialog
              triggerLabel={t('recordsView.add')}
              inputDialogTitle={t('recordsView.addTaskTitle')}
              confirmDialogTitle={t('recordsView.confirmTargetTitle')}
              confirmDialogDescription={t('recordsView.confirmTargetDescription')}
              confirmLabel={t('recordsView.confirmRecord')}
              confirmLoadingLabel={t('recordsView.adding')}
              onConfirmDialogOpen={resetRecordStartForm}
              confirmExtraContent={
                <RecordStartOptions
                  view="records"
                  value={startConfig}
                  onChange={setStartConfig}
                />
              }
              onConfirm={handleConfirmRecord}
            />
          )}
        </div>
      </div>

      <div ref={scrollContainerRef} className="flex-1 overflow-y-auto p-4 pb-20">
        {isLoading ? (
          <LoadingScreen />
        ) : tasks.length === 0 ? (
          <EmptyState
            icon={
              <svg className="w-10 h-10 text-muted-foreground" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <circle cx="12" cy="12" r="10" strokeWidth="2" />
                <circle cx="12" cy="12" r="3" fill="currentColor" />
              </svg>
            }
            title={t('recordsView.emptyTitle')}
            description={t('recordsView.emptyDescription')}
          />
        ) : viewMode === 'list' ? (
          <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
            {sortedTasks.map((task) => (
              <RecordListRow key={task.roomId} task={task} onStop={handleStop} />
            ))}
          </div>
        ) : (
          <div className="cards-grid grid gap-4">
            {sortedTasks.map((task) => (
              <RecordCard key={task.roomId} task={task} onStop={handleStop} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
